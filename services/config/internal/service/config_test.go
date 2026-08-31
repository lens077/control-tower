package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/lens077/control-tower/api/config/v1"
	"github.com/lens077/control-tower/services/config/internal/biz"
	"github.com/lens077/control-tower/services/config/internal/presence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// 历史版本存的是写入当时的明文。GetKey 把密钥打成 ****** 而 ListRevisions/GetRevision
// 原样吐出真值的话,脱敏就等于没做 —— 历史页面正是把 revision.value 直接渲染出来的地方。
func TestToPBRevision_MasksSecret(t *testing.T) {
	now := time.Now()
	rev := &biz.ConfigRevision{
		ID:        7,
		EntryID:   3,
		Version:   5,
		Format:    biz.FormatYAML,
		Value:     "password: hunter2",
		IsSecret:  true,
		Comment:   "改密码",
		Author:    "admin_01",
		CreatedAt: now,
	}

	pb := toPBRevision(rev)

	assert.Equal(t, maskedValue, pb.Value, "密钥的历史值必须脱敏")
	// 元数据照常返回:看得到「谁在什么时候改了」,只是看不到改成了什么
	assert.Equal(t, int32(5), pb.Version)
	assert.Equal(t, "admin_01", pb.Author)
	assert.Equal(t, "改密码", pb.Comment)
	require.NotNil(t, pb.CreatedAt)
	assert.Equal(t, now.Unix(), pb.CreatedAt.Seconds)
}

func TestListClientConnections_MapsPresenceWithoutSecrets(t *testing.T) {
	registry := presence.NewRegistry()
	identity := presence.Identity{Name: "cart-service", Instance: "cart-1", Version: "dev"}
	target := presence.Target{Namespace: "cart", Environment: "dev", Key: "bootstrap.yaml"}
	registry.RecordRead(identity, target)
	registry.StartWatch(identity, []presence.Target{target})

	service := &ConfigService{presence: registry}
	response, err := service.ListClientConnections(context.Background(), connect.NewRequest(&v1.ListClientConnectionsRequest{}))
	require.NoError(t, err)
	require.Len(t, response.Msg.Connections, 1)
	connection := response.Msg.Connections[0]
	assert.Equal(t, "cart-service", connection.ClientName)
	assert.Equal(t, "cart-1", connection.ClientInstance)
	assert.True(t, connection.Watching)
	require.Len(t, connection.Targets, 1)
	assert.Equal(t, "bootstrap.yaml", connection.Targets[0].Key)
	assert.Equal(t, string(presence.ModeLocal), response.Header().Get(presence.ModeHeader))
}

func TestToPBRevision_KeepsPlainValue(t *testing.T) {
	pb := toPBRevision(&biz.ConfigRevision{
		Version:  2,
		Format:   biz.FormatYAML,
		Value:    "server:\n  addr: \"0.0.0.0:30006\"\n",
		IsSecret: false,
	})
	assert.Equal(t, "server:\n  addr: \"0.0.0.0:30006\"\n", pb.Value)
}

// toPBEntry 与 toPBRevision 必须用同一个占位值,否则前端要认两种「已脱敏」的写法。
func TestMaskedValue_SharedByEntryAndRevision(t *testing.T) {
	entry := toPBEntry(&biz.ConfigEntry{Value: "s3cret", IsSecret: true}, false)
	revision := toPBRevision(&biz.ConfigRevision{Value: "s3cret", IsSecret: true})
	assert.Equal(t, entry.Value, revision.Value)
	assert.Equal(t, maskedValue, entry.Value)
}

func TestToErr_MapsSchemaViolationToInvalidArgument(t *testing.T) {
	service := &ConfigService{}
	err := service.toErr(fmt.Errorf("%w: schema: invalid configuration at /search", biz.ErrInvalidFormat))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

type putTrackingRepo struct {
	putCount int
}

func (*putTrackingRepo) ListNamespaces(context.Context) ([]*biz.NamespaceInfo, error) {
	return nil, nil
}

func (*putTrackingRepo) ListEntries(context.Context, string, string, string) ([]*biz.ConfigEntry, error) {
	return nil, nil
}

func (*putTrackingRepo) GetEntry(context.Context, string, string, string) (*biz.ConfigEntry, error) {
	return nil, biz.ErrKeyNotFound
}

func (r *putTrackingRepo) PutEntry(_ context.Context, in biz.PutParams) (*biz.ConfigEntry, error) {
	r.putCount++
	return &biz.ConfigEntry{
		Namespace: in.Namespace, Environment: in.Environment, Key: in.Key,
		Format: in.Format, Value: in.Value, IsSecret: in.IsSecret,
	}, nil
}

func (*putTrackingRepo) DeleteEntry(context.Context, string, string, string) (bool, error) {
	return false, nil
}

func (*putTrackingRepo) ListRevisions(context.Context, string, string, string) ([]*biz.ConfigRevision, error) {
	return nil, nil
}

func (*putTrackingRepo) GetRevision(context.Context, string, string, string, int32) (*biz.ConfigRevision, error) {
	return nil, biz.ErrRevisionNotFound
}

func TestPutKeyRejectsMaskedSecretBeforePersistence(t *testing.T) {
	repo := &putTrackingRepo{}
	service := &ConfigService{uc: biz.NewConfigUseCase(repo, nil, nil, zap.NewNop())}

	_, err := service.PutKey(context.Background(), connect.NewRequest(&v1.PutKeyRequest{
		Namespace:   "gateway",
		Environment: "pre",
		Key:         "credentials.txt",
		Format:      v1.ConfigFormat_CONFIG_FORMAT_PLAINTEXT,
		Value:       maskedValue,
		IsSecret:    true,
	}))

	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	assert.Equal(t, 0, repo.putCount)
}

func TestPutKeyCannotBypassMaskedPlaceholderGuardByClearingSecretFlag(t *testing.T) {
	repo := &putTrackingRepo{}
	service := &ConfigService{uc: biz.NewConfigUseCase(repo, nil, nil, zap.NewNop())}

	_, err := service.PutKey(context.Background(), connect.NewRequest(&v1.PutKeyRequest{
		Namespace:   "gateway",
		Environment: "pre",
		Key:         "credentials.txt",
		Format:      v1.ConfigFormat_CONFIG_FORMAT_PLAINTEXT,
		Value:       maskedValue,
		IsSecret:    false,
	}))

	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	assert.Equal(t, 0, repo.putCount)
}
