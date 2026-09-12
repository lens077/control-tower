package service

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/lens077/control-tower/api/config/v1"
	"github.com/lens077/control-tower/services/config/internal/biz"
	"github.com/lens077/control-tower/services/config/internal/iam"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fakeTokenRepo 是 biz.MachineTokenRepo 的测试替身（内存表）。
type fakeTokenRepo struct {
	tokens  map[string]biz.MachineToken
	revoked []string
}

func newFakeTokenRepo(seed ...biz.MachineToken) *fakeTokenRepo {
	r := &fakeTokenRepo{tokens: map[string]biz.MachineToken{}}
	for _, t := range seed {
		r.tokens[t.ID] = t
	}
	return r
}

func (f *fakeTokenRepo) Insert(_ context.Context, t biz.MachineToken) (biz.MachineToken, error) {
	f.tokens[t.ID] = t
	return t, nil
}

func (f *fakeTokenRepo) Get(_ context.Context, id string) (biz.MachineToken, error) {
	t, ok := f.tokens[id]
	if !ok {
		return biz.MachineToken{}, biz.ErrTokenNotFound
	}
	return t, nil
}

func (f *fakeTokenRepo) List(context.Context, string, string) ([]biz.MachineToken, error) {
	out := make([]biz.MachineToken, 0, len(f.tokens))
	for _, t := range f.tokens {
		out = append(out, t)
	}
	return out, nil
}

func (f *fakeTokenRepo) Revoke(_ context.Context, id string) (biz.MachineToken, error) {
	t, ok := f.tokens[id]
	if !ok || t.Disabled {
		return biz.MachineToken{}, biz.ErrTokenNotFound
	}
	t.Disabled = true
	f.tokens[id] = t
	f.revoked = append(f.revoked, id)
	return t, nil
}

func adminCtx() context.Context {
	return iam.ContextWithPrincipal(context.Background(), iam.Principal{Name: "admin_01"})
}

func operatorCtx(environment string) context.Context {
	return iam.ContextWithPrincipal(context.Background(), iam.Principal{
		Name: "operator:harvest", Machine: true,
		Scope: &iam.MachineScope{TokenID: "op-1", Service: "harvest", Environment: environment, Namespaces: []string{"*"}, Operator: true},
	})
}

func serviceCtx(service, environment string) context.Context {
	return iam.ContextWithPrincipal(context.Background(), iam.Principal{
		Name: "service:" + service, Machine: true,
		Scope: &iam.MachineScope{TokenID: "svc-1", Service: service, Environment: environment, Namespaces: []string{service}},
	})
}

func newTestService(repo *putTrackingRepo, tokens *fakeTokenRepo) *ConfigService {
	return &ConfigService{
		uc:            biz.NewConfigUseCase(repo, nil, nil, zap.NewNop()),
		machineTokens: biz.NewMachineTokenUseCase(tokens, zap.NewNop()),
	}
}

func putRequest(namespace, environment string) *connect.Request[v1.PutKeyRequest] {
	return connect.NewRequest(&v1.PutKeyRequest{
		Namespace: namespace, Environment: environment, Key: "bootstrap.yaml",
		Format: v1.ConfigFormat_CONFIG_FORMAT_YAML, Value: "server:\n  addr: \"0.0.0.0:30006\"\n",
	})
}

// operator 可在自身 environment 内 PutKey（namespace 通配）。
func TestPutKey_OperatorWithinEnvironment(t *testing.T) {
	repo := &putTrackingRepo{}
	service := newTestService(repo, newFakeTokenRepo())

	resp, err := service.PutKey(operatorCtx("pre"), putRequest("order", "pre"))
	require.NoError(t, err)
	assert.Equal(t, 1, repo.putCount)
	assert.Equal(t, "order", resp.Msg.Entry.Namespace)
}

// operator 跨 environment 写入被拒。
func TestPutKey_OperatorDeniedForOtherEnvironment(t *testing.T) {
	repo := &putTrackingRepo{}
	service := newTestService(repo, newFakeTokenRepo())

	_, err := service.PutKey(operatorCtx("pre"), putRequest("order", "dev"))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	assert.Equal(t, 0, repo.putCount)
}

// 普通 service token 即使绕过 iam 白名单到达 service 层，也不能写自身以外的范围；
// 且 iam 层本就不放行 PutKey（见 iam 测试）。这里验证纵深防御：范围外一律拒绝。
func TestPutKey_ServiceTokenDenied(t *testing.T) {
	repo := &putTrackingRepo{}
	service := newTestService(repo, newFakeTokenRepo())

	_, err := service.PutKey(serviceCtx("order", "dev"), putRequest("cart", "dev"))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	assert.Equal(t, 0, repo.putCount)
}

// 管理员 JWT 不受 machine 范围限制。
func TestPutKey_AdminUnrestricted(t *testing.T) {
	repo := &putTrackingRepo{}
	service := newTestService(repo, newFakeTokenRepo())

	_, err := service.PutKey(adminCtx(), putRequest("order", "dev"))
	require.NoError(t, err)
	assert.Equal(t, 1, repo.putCount)
}

// operator 可签发 role=service 的 token。
func TestIssueMachineToken_OperatorCanIssueServiceToken(t *testing.T) {
	tokens := newFakeTokenRepo()
	service := newTestService(&putTrackingRepo{}, tokens)

	resp, err := service.IssueMachineToken(operatorCtx("pre"), connect.NewRequest(&v1.IssueMachineTokenRequest{
		ServiceName: "order", Environment: "pre", Role: v1.MachineTokenRole_MACHINE_TOKEN_ROLE_SERVICE,
	}))
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Msg.Token)
	assert.Equal(t, v1.MachineTokenRole_MACHINE_TOKEN_ROLE_SERVICE, resp.Msg.Meta.Role)
	assert.Equal(t, []string{"order"}, resp.Msg.Meta.AllowedNamespaces)
	assert.Len(t, tokens.tokens, 1)

	// UNSPECIFIED 等价于 SERVICE。
	resp, err = service.IssueMachineToken(operatorCtx("pre"), connect.NewRequest(&v1.IssueMachineTokenRequest{
		ServiceName: "cart", Environment: "pre",
	}))
	require.NoError(t, err)
	assert.Equal(t, v1.MachineTokenRole_MACHINE_TOKEN_ROLE_SERVICE, resp.Msg.Meta.Role)
}

// operator 不能签发 operator token（防止自我复制）。
func TestIssueMachineToken_OperatorCannotIssueOperator(t *testing.T) {
	tokens := newFakeTokenRepo()
	service := newTestService(&putTrackingRepo{}, tokens)

	_, err := service.IssueMachineToken(operatorCtx("pre"), connect.NewRequest(&v1.IssueMachineTokenRequest{
		ServiceName: "harvest", Environment: "pre", Role: v1.MachineTokenRole_MACHINE_TOKEN_ROLE_OPERATOR,
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	assert.Empty(t, tokens.tokens)
}

// 管理员可签发 operator token；元数据带 role 与通配白名单。
func TestIssueMachineToken_AdminIssuesOperator(t *testing.T) {
	tokens := newFakeTokenRepo()
	service := newTestService(&putTrackingRepo{}, tokens)

	resp, err := service.IssueMachineToken(adminCtx(), connect.NewRequest(&v1.IssueMachineTokenRequest{
		ServiceName: "harvest", Environment: "pre", Role: v1.MachineTokenRole_MACHINE_TOKEN_ROLE_OPERATOR,
	}))
	require.NoError(t, err)
	assert.Equal(t, v1.MachineTokenRole_MACHINE_TOKEN_ROLE_OPERATOR, resp.Msg.Meta.Role)
	assert.Equal(t, []string{"*"}, resp.Msg.Meta.AllowedNamespaces)
}

// 普通 service token 不能触碰 token 管理（纵深防御）。
func TestIssueMachineToken_ServiceTokenDenied(t *testing.T) {
	service := newTestService(&putTrackingRepo{}, newFakeTokenRepo())
	_, err := service.IssueMachineToken(serviceCtx("order", "dev"), connect.NewRequest(&v1.IssueMachineTokenRequest{
		ServiceName: "order", Environment: "dev",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

// operator 可吊销 service token，不能吊销 operator token；管理员两者皆可。
func TestRevokeMachineToken_OperatorRules(t *testing.T) {
	tokens := newFakeTokenRepo(
		biz.MachineToken{ID: "svc-token", Service: "order", Environment: "pre", Role: biz.RoleService},
		biz.MachineToken{ID: "op-token", Service: "harvest", Environment: "pre", Role: biz.RoleOperator},
	)
	service := newTestService(&putTrackingRepo{}, tokens)

	_, err := service.RevokeMachineToken(operatorCtx("pre"), connect.NewRequest(&v1.RevokeMachineTokenRequest{Id: "op-token"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	assert.Empty(t, tokens.revoked)

	_, err = service.RevokeMachineToken(operatorCtx("pre"), connect.NewRequest(&v1.RevokeMachineTokenRequest{Id: "svc-token"}))
	require.NoError(t, err)
	assert.Equal(t, []string{"svc-token"}, tokens.revoked)

	_, err = service.RevokeMachineToken(operatorCtx("pre"), connect.NewRequest(&v1.RevokeMachineTokenRequest{Id: "missing"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	_, err = service.RevokeMachineToken(adminCtx(), connect.NewRequest(&v1.RevokeMachineTokenRequest{Id: "op-token"}))
	require.NoError(t, err)
	assert.Equal(t, []string{"svc-token", "op-token"}, tokens.revoked)
}

// ListMachineTokens：operator 可列全部，且元数据映射 role。
func TestListMachineTokens_OperatorSeesRoles(t *testing.T) {
	tokens := newFakeTokenRepo(
		biz.MachineToken{ID: "op-token", Service: "harvest", Environment: "pre", Role: biz.RoleOperator},
	)
	service := newTestService(&putTrackingRepo{}, tokens)

	resp, err := service.ListMachineTokens(operatorCtx("pre"), connect.NewRequest(&v1.ListMachineTokensRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Tokens, 1)
	assert.Equal(t, v1.MachineTokenRole_MACHINE_TOKEN_ROLE_OPERATOR, resp.Msg.Tokens[0].Role)

	_, err = service.ListMachineTokens(serviceCtx("order", "dev"), connect.NewRequest(&v1.ListMachineTokensRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

// operator 只能为自身 environment 签发 / 吊销 service token（最小权限，与其读写范围一致）。
func TestIssueMachineToken_OperatorDeniedForOtherEnvironment(t *testing.T) {
	service := newTestService(&putTrackingRepo{}, newFakeTokenRepo())

	_, err := service.IssueMachineToken(operatorCtx("pre"), connect.NewRequest(&v1.IssueMachineTokenRequest{
		ServiceName: "order", Environment: "dev",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestRevokeMachineToken_OperatorDeniedForOtherEnvironment(t *testing.T) {
	tokens := newFakeTokenRepo()
	service := newTestService(&putTrackingRepo{}, tokens)

	issued, err := service.IssueMachineToken(adminCtx(), connect.NewRequest(&v1.IssueMachineTokenRequest{
		ServiceName: "order", Environment: "dev",
	}))
	require.NoError(t, err)

	_, err = service.RevokeMachineToken(operatorCtx("pre"), connect.NewRequest(&v1.RevokeMachineTokenRequest{Id: issued.Msg.Meta.Id}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	_, err = service.RevokeMachineToken(operatorCtx("dev"), connect.NewRequest(&v1.RevokeMachineTokenRequest{Id: issued.Msg.Meta.Id}))
	require.NoError(t, err)
}
