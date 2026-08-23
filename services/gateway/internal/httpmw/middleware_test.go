package httpmw

import (
	"net/http"
	"net/http/httptest"
	"testing"

	confv1 "github.com/lens077/control-tower/services/gateway/internal/conf/v1"
	"github.com/lens077/control-tower/services/gateway/internal/gwerrors"

	"go.uber.org/zap"
)

func TestRecoverWritesConnectError(t *testing.T) {
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}), Recover(zap.NewNop(), gwerrors.NewWriter()))

	req := httptest.NewRequest(http.MethodPost, "/x.y.Z/M", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 500 || rec.Header().Get(gwerrors.HeaderReason) != "GATEWAY_PANIC" {
		t.Fatalf("status=%d reason=%s", rec.Code, rec.Header().Get(gwerrors.HeaderReason))
	}
}

func corsCfg() *confv1.Cors {
	return &confv1.Cors{
		AllowCredentials: true,
		AllowOrigins:     []string{"https://shop.apikv.com"},
		AllowHeaders:     []string{"Authorization", "Content-Type", "Connect-Protocol-Version"},
		AllowMethods:     []string{"OPTIONS", "GET", "POST"},
	}
}

func TestCorsPreflightShortCircuitsBeforeAuth(t *testing.T) {
	s := NewCorsSwapper()
	if err := s.Update(corsCfg()); err != nil {
		t.Fatal(err)
	}
	authHit := false
	// cors 在 auth 外层：预检必须不进入内层。
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		authHit = true
		w.WriteHeader(200)
	}), s.Middleware())

	req := httptest.NewRequest(http.MethodOptions, "/user.v1.UserService/GetProfile", nil)
	req.Header.Set("Origin", "https://shop.apikv.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization,connect-protocol-version")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if authHit {
		t.Fatal("preflight must short-circuit before inner handlers")
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://shop.apikv.com" {
		t.Fatalf("preflight headers missing: %v", rec.Header())
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("credentials must be allowed")
	}
}

func TestCorsDisallowedOrigin(t *testing.T) {
	s := NewCorsSwapper()
	if err := s.Update(corsCfg()); err != nil {
		t.Fatal(err)
	}
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }), s.Middleware())

	req := httptest.NewRequest(http.MethodPost, "/user.v1.UserService/GetProfile", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("disallowed origin must not receive CORS headers")
	}
}

func TestCorsWildcardWithCredentialsRejected(t *testing.T) {
	s := NewCorsSwapper()
	cfg := corsCfg()
	cfg.AllowOrigins = []string{"*"}
	if err := s.Update(cfg); err == nil {
		t.Fatal("wildcard with credentials must be rejected")
	}
}

func TestCorsUnconfiguredPassthrough(t *testing.T) {
	s := NewCorsSwapper()
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), s.Middleware())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/a.b.C/D", nil))
	if rec.Code != 204 {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestAccessLogRecordsStatus(t *testing.T) {
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}), AccessLog(zap.NewNop()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/a.b.C/D", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d", rec.Code)
	}
}
