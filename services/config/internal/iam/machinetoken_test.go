package iam

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
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

// 双栈第一段：legacy 共享 token 仍可用，且计入告警计数。
func TestLegacySharedTokenStillWorksAndCounts(t *testing.T) {
	a := &Authorizer{serviceToken: []byte("legacy-shared"), log: zap.NewNop()}

	rec, p := doAuth(t, a, "legacy-shared", "/config.v1.ConfigService/GetKey")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, p)
	assert.True(t, p.Machine)
	require.NotNil(t, p.Scope)
	assert.True(t, p.Scope.Legacy)
	assert.EqualValues(t, 1, a.LegacyHits())
	// legacy 范围=任意 namespace 只读。
	assert.True(t, p.Scope.AllowsRead("anything", "anywhere"))
}

func TestLegacySharedTokenPublishesHitGauge(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() {
		otel.SetMeterProvider(previous)
		require.NoError(t, provider.Shutdown(context.Background()))
	})

	a := &Authorizer{serviceToken: []byte("legacy-shared"), log: zap.NewNop()}
	require.NoError(t, registerLegacyHitMetric(a))
	doAuth(t, a, "legacy-shared", "/config.v1.ConfigService/GetKey")

	var resourceMetrics metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &resourceMetrics))
	for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
		for _, metric := range scopeMetrics.Metrics {
			if metric.Name != "machine_token_legacy_hits" {
				continue
			}
			gauge, ok := metric.Data.(metricdata.Gauge[int64])
			require.True(t, ok)
			require.Len(t, gauge.DataPoints, 1)
			assert.EqualValues(t, 1, gauge.DataPoints[0].Value)
			return
		}
	}
	t.Fatal("machine_token_legacy_hits metric was not collected")
}

// 双栈第二段：per-service token 查表命中，范围收窄。
func TestPerServiceTokenScoped(t *testing.T) {
	scope := MachineScope{TokenID: "id-1", Service: "order", Environment: "dev", Namespaces: []string{"order"}}
	a := &Authorizer{
		serviceToken: []byte("legacy-shared"),
		tokens:       newFakeStore("ct_order_dev", scope),
		log:          zap.NewNop(),
	}

	rec, p := doAuth(t, a, "ct_order_dev", "/config.v1.ConfigService/WatchKeys")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, p)
	require.NotNil(t, p.Scope)
	assert.False(t, p.Scope.Legacy)
	assert.Equal(t, "service:order", p.Name)
	assert.EqualValues(t, 0, a.LegacyHits())
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
		serviceToken: []byte("legacy-shared"),
		tokens:       newFakeStore("ct_known", MachineScope{TokenID: "x"}),
		log:          zap.NewNop(),
	}
	rec, _ := doAuth(t, a, "ct_wrong", "/config.v1.ConfigService/GetKey")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// machine token（含 legacy）不得触碰管理面 procedure。
func TestMachineTokenCannotManage(t *testing.T) {
	scope := MachineScope{TokenID: "id-1", Service: "order", Environment: "dev", Namespaces: []string{"order"}}
	a := &Authorizer{
		serviceToken: []byte("legacy-shared"),
		tokens:       newFakeStore("ct_order_dev", scope),
		log:          zap.NewNop(),
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

// 未配置 legacy 环境变量时（关闭死线后形态），只有查表路径生效。
func TestLegacyDisabledAfterDeadline(t *testing.T) {
	scope := MachineScope{TokenID: "id-1", Service: "order", Environment: "dev", Namespaces: []string{"order"}}
	a := &Authorizer{
		tokens: newFakeStore("ct_order_dev", scope),
		log:    zap.NewNop(),
	}
	rec, _ := doAuth(t, a, "ct_order_dev", "/config.v1.ConfigService/GetKey")
	assert.Equal(t, http.StatusOK, rec.Code)

	rec, _ = doAuth(t, a, "anything-else", "/config.v1.ConfigService/GetKey")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.EqualValues(t, 0, a.LegacyHits())
}
