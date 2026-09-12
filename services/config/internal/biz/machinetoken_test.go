package biz

import (
	"context"
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type fakeTokenRepo struct {
	inserted MachineToken
	revoked  string
}

func (f *fakeTokenRepo) Insert(_ context.Context, t MachineToken) (MachineToken, error) {
	f.inserted = t
	return t, nil
}

func (f *fakeTokenRepo) Get(_ context.Context, id string) (MachineToken, error) {
	if f.inserted.ID == id {
		return f.inserted, nil
	}
	return MachineToken{}, ErrTokenNotFound
}

func (f *fakeTokenRepo) List(context.Context, string, string) ([]MachineToken, error) {
	return nil, nil
}

func (f *fakeTokenRepo) Revoke(_ context.Context, id string) (MachineToken, error) {
	f.revoked = id
	return MachineToken{ID: id}, nil
}

func TestIssueGeneratesHashedToken(t *testing.T) {
	repo := &fakeTokenRepo{}
	uc := NewMachineTokenUseCase(repo, zap.NewNop())

	plaintext, meta, err := uc.Issue(context.Background(), "order", "dev", nil, "轮换测试", "admin", "")
	require.NoError(t, err)

	// 明文形态：ct_ + base64url(32B) ≥ 43 字符。
	assert.True(t, strings.HasPrefix(plaintext, "ct_"))
	assert.GreaterOrEqual(t, len(plaintext), 3+43)

	// 只存哈希，且哈希与明文对应。
	sum := sha256.Sum256([]byte(plaintext))
	assert.Equal(t, sum[:], repo.inserted.TokenHash)
	assert.NotContains(t, string(repo.inserted.TokenHash), plaintext)

	// 空白名单默认收窄到自身 namespace；空 role 按 service。
	assert.Equal(t, []string{"order"}, repo.inserted.AllowedNamespaces)
	assert.Equal(t, RoleService, repo.inserted.Role)
	assert.Equal(t, "order", meta.Service)
	assert.Equal(t, "dev", meta.Environment)
}

// operator 角色：空白名单默认通配 "*"，而不是自身 namespace。
func TestIssueOperatorDefaultsToWildcardNamespace(t *testing.T) {
	repo := &fakeTokenRepo{}
	uc := NewMachineTokenUseCase(repo, zap.NewNop())

	_, meta, err := uc.Issue(context.Background(), "harvest", "pre", nil, "自动化", "admin", RoleOperator)
	require.NoError(t, err)
	assert.Equal(t, []string{NamespaceWildcard}, repo.inserted.AllowedNamespaces)
	assert.Equal(t, RoleOperator, repo.inserted.Role)
	assert.True(t, meta.IsOperator())

	// 显式白名单则按给定值。
	_, _, err = uc.Issue(context.Background(), "harvest", "pre", []string{"order", "cart"}, "", "admin", RoleOperator)
	require.NoError(t, err)
	assert.Equal(t, []string{"order", "cart"}, repo.inserted.AllowedNamespaces)
}

func TestIssueRejectsUnknownRole(t *testing.T) {
	repo := &fakeTokenRepo{}
	uc := NewMachineTokenUseCase(repo, zap.NewNop())
	_, _, err := uc.Issue(context.Background(), "order", "dev", nil, "", "admin", "root")
	require.ErrorIs(t, err, ErrInvalidTokenRole)
	assert.Empty(t, repo.inserted.ID, "非法 role 不得落库")
}

func TestIssueUniquePerCall(t *testing.T) {
	repo := &fakeTokenRepo{}
	uc := NewMachineTokenUseCase(repo, zap.NewNop())
	a, _, err := uc.Issue(context.Background(), "order", "dev", nil, "", "admin", RoleService)
	require.NoError(t, err)
	b, _, err := uc.Issue(context.Background(), "order", "dev", nil, "", "admin", RoleService)
	require.NoError(t, err)
	assert.NotEqual(t, a, b, "两代重叠轮换依赖每次签发唯一")
}

func TestRevokePassesThrough(t *testing.T) {
	repo := &fakeTokenRepo{}
	uc := NewMachineTokenUseCase(repo, zap.NewNop())
	_, err := uc.Revoke(context.Background(), "some-id", "admin")
	require.NoError(t, err)
	assert.Equal(t, "some-id", repo.revoked)
}
