package iam

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fakeStore 是 TokenStore 的测试替身。
type fakeStore struct {
	byHash  map[[32]byte]MachineScope
	touched []string
}

func (f *fakeStore) LookupActiveByHash(_ context.Context, hash []byte) (MachineScope, bool, error) {
	var key [32]byte
	copy(key[:], hash)
	s, ok := f.byHash[key]
	return s, ok, nil
}

func (f *fakeStore) IsActive(context.Context, string) (bool, error) { return true, nil }

func (f *fakeStore) TouchLastUsed(_ context.Context, id string) { f.touched = append(f.touched, id) }

func newFakeStore(plaintext string, scope MachineScope) *fakeStore {
	sum := sha256.Sum256([]byte(plaintext))
	return &fakeStore{byHash: map[[32]byte]MachineScope{sum: scope}}
}

func doAuth(t *testing.T, a *Authorizer, token, path string) (*httptest.ResponseRecorder, *Principal) {
	t.Helper()
	var got *Principal
	handler := a.HTTP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if p, ok := PrincipalFromContext(r.Context()); ok {
			got = &p
		}
	}))
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("x-config-center-service-token", token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec, got
}

// per-service token 查表命中，范围收窄到自身 namespace×environment。
func TestPerServiceTokenScoped(t *testing.T) {
	scope := MachineScope{TokenID: "id-1", Service: "order", Environment: "dev", Namespaces: []string{"order"}}
	a := &Authorizer{
		tokens: newFakeStore("ct_order_dev", scope),
		log:    zap.NewNop(),
	}

	rec, p := doAuth(t, a, "ct_order_dev", "/config.v1.ConfigService/WatchKeys")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, p)
	require.NotNil(t, p.Scope)
	assert.Equal(t, "service:order", p.Name)
	// TouchLastUsed 已记录。
	assert.Equal(t, []string{"id-1"}, a.tokens.(*fakeStore).touched)

	// 范围语义。
	assert.True(t, p.Scope.AllowsRead("order", "dev"))
	assert.False(t, p.Scope.AllowsRead("order", "pre"), "environment 必须相等")
	assert.False(t, p.Scope.AllowsRead("payment", "dev"), "namespace 必须在白名单")
}

// 双双未命中 → 401。
func TestUnknownTokenRejected(t *testing.T) {
	a := &Authorizer{
		tokens: newFakeStore("ct_known", MachineScope{TokenID: "x"}),
		log:    zap.NewNop(),
	}
	rec, _ := doAuth(t, a, "ct_wrong", "/config.v1.ConfigService/GetKey")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// machine token（含 legacy）不得触碰管理面 procedure。
func TestMachineTokenCannotManage(t *testing.T) {
	scope := MachineScope{TokenID: "id-1", Service: "order", Environment: "dev", Namespaces: []string{"order"}}
	a := &Authorizer{
		tokens: newFakeStore("ct_order_dev", scope),
		log:    zap.NewNop(),
	}
	for _, tok := range []string{"legacy-shared", "ct_order_dev"} {
		for _, path := range []string{
			"/config.v1.ConfigService/PutKey",
			"/config.v1.ConfigService/IssueMachineToken",
			"/config.v1.ConfigService/RevokeMachineToken",
			"/config.v1.ConfigService/ListMachineTokens",
		} {
			rec, _ := doAuth(t, a, tok, path)
			assert.Equal(t, http.StatusForbidden, rec.Code, "token=%s path=%s", tok, path)
		}
	}
}

// 共享 token 已移除：只有查表路径生效，任何非 machine token 一律 401。
func TestOnlyPerServiceTokensAccepted(t *testing.T) {
	scope := MachineScope{TokenID: "id-1", Service: "order", Environment: "dev", Namespaces: []string{"order"}}
	a := &Authorizer{
		tokens: newFakeStore("ct_order_dev", scope),
		log:    zap.NewNop(),
	}
	rec, _ := doAuth(t, a, "ct_order_dev", "/config.v1.ConfigService/GetKey")
	assert.Equal(t, http.StatusOK, rec.Code)

	rec, _ = doAuth(t, a, "anything-else", "/config.v1.ConfigService/GetKey")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// AllowsRead 的 "*" 通配：namespace 任意，environment 仍须相等。
func TestAllowsReadNamespaceWildcard(t *testing.T) {
	scope := &MachineScope{Service: "harvest", Environment: "pre", Namespaces: []string{"*"}}
	assert.True(t, scope.AllowsRead("order", "pre"))
	assert.True(t, scope.AllowsRead("anything", "pre"))
	assert.False(t, scope.AllowsRead("order", "dev"), "通配只放宽 namespace，不放宽 environment")

	mixed := &MachineScope{Service: "x", Environment: "dev", Namespaces: []string{"order", "*"}}
	assert.True(t, mixed.AllowsRead("cart", "dev"))
}

// operator token：管理面 procedure 白名单放行，超出白名单的仍 403，主体名带 operator: 前缀。
func TestOperatorTokenProcedureAllowlist(t *testing.T) {
	scope := MachineScope{TokenID: "op-1", Service: "harvest", Environment: "pre", Namespaces: []string{"*"}, Operator: true}
	a := &Authorizer{
		tokens: newFakeStore("ct_operator_pre", scope),
		log:    zap.NewNop(),
	}

	for _, path := range []string{
		"/config.v1.ConfigService/GetKey",
		"/config.v1.ConfigService/WatchKeys",
		"/config.v1.ConfigService/ListKeys",
		"/config.v1.ConfigService/ListNamespaces",
		"/config.v1.ConfigService/PutKey",
		"/config.v1.ConfigService/ListRevisions",
		"/config.v1.ConfigService/GetRevision",
		"/config.v1.ConfigService/ListMachineTokens",
		"/config.v1.ConfigService/IssueMachineToken",
		"/config.v1.ConfigService/RevokeMachineToken",
	} {
		rec, p := doAuth(t, a, "ct_operator_pre", path)
		require.Equal(t, http.StatusOK, rec.Code, "path=%s", path)
		require.NotNil(t, p, "path=%s", path)
		assert.True(t, p.Machine)
		assert.Equal(t, "operator:harvest", p.Name)
		require.NotNil(t, p.Scope)
		assert.True(t, p.Scope.Operator)
	}

	// 破坏性 procedure 仍仅限管理员 JWT。
	for _, path := range []string{
		"/config.v1.ConfigService/DeleteKey",
		"/config.v1.ConfigService/Rollback",
		"/config.v1.ConfigService/ListClientConnections",
	} {
		rec, _ := doAuth(t, a, "ct_operator_pre", path)
		assert.Equal(t, http.StatusForbidden, rec.Code, "path=%s", path)
	}
}

// 普通 service token 不因 operator 引入而放宽：PutKey 等仍 403。
func TestServiceTokenStillReadOnlyAfterOperator(t *testing.T) {
	scope := MachineScope{TokenID: "id-1", Service: "order", Environment: "dev", Namespaces: []string{"order"}}
	a := &Authorizer{tokens: newFakeStore("ct_order_dev", scope), log: zap.NewNop()}

	rec, _ := doAuth(t, a, "ct_order_dev", "/config.v1.ConfigService/PutKey")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec, _ = doAuth(t, a, "ct_order_dev", "/config.v1.ConfigService/ListKeys")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec, p := doAuth(t, a, "ct_order_dev", "/config.v1.ConfigService/GetKey")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "service:order", p.Name)
	assert.False(t, p.Scope.Operator)
}
