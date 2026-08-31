package proxy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lens077/control-tower/services/gateway/internal/authn"
	"github.com/lens077/control-tower/services/gateway/internal/gwctx"
	"github.com/lens077/control-tower/services/gateway/internal/gwerrors"
	"github.com/lens077/control-tower/services/gateway/internal/identity"
	"github.com/lens077/control-tower/services/gateway/internal/resolver"
	"github.com/lens077/control-tower/services/gateway/internal/router"

	"go.uber.org/zap"
)

// fakeResolver 固定返回一个实例，并记录 done 反馈。
type fakeResolver struct {
	addr    string
	err     error
	lastErr atomic.Value // error
}

func (f *fakeResolver) Pick(service string) (resolver.Instance, resolver.Done, error) {
	if f.err != nil {
		return resolver.Instance{}, nil, f.err
	}
	return resolver.Instance{Addr: f.addr}, func(err error) {
		if err != nil {
			f.lastErr.Store(err)
		}
	}, nil
}

func (f *fakeResolver) Ready() bool { return true }

func claims(sub string) *authn.Claims {
	return &authn.Claims{
		Owner: "lens",
		Name:  "alice",
		Roles: []authn.Role{{Name: "customer"}},
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: sub,
		},
	}
}

// serve 用注入的 h1 transport 走完整 Proxy 链路。
func serve(t *testing.T, res resolver.Resolver, route router.Route, withClaims bool, reqMut func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	p := New(res, gwerrors.NewWriter(), zap.NewNop(), http.DefaultTransport)
	req := httptest.NewRequest(http.MethodPost, "/user.v1.UserService/SignIn", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	ctx := gwctx.WithRoute(req.Context(), route)
	if withClaims {
		ctx = gwctx.WithClaims(ctx, claims("u-alice"))
	}
	ctx, _ = gwctx.WithUpstream(ctx)
	req = req.WithContext(ctx)
	if reqMut != nil {
		reqMut(req)
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	return rec
}

func TestProxyForwardsAndInjectsIdentity(t *testing.T) {
	var got http.Header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Backend", "ok")
		_, _ = w.Write(body)
	}))
	defer backend.Close()

	addr := strings.TrimPrefix(backend.URL, "http://")
	res := &fakeResolver{addr: addr}
	rec := serve(t, res, router.Route{Package: "user", Target: "discovery:///user-identity", Timeout: 2 * time.Second}, true, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer should-not-leak")
	})

	if rec.Code != 200 || rec.Header().Get("X-Backend") != "ok" {
		t.Fatalf("status=%d headers=%v", rec.Code, rec.Header())
	}
	if got.Get(identity.HeaderUserID) != "u-alice" || got.Get(identity.HeaderRole) != "customer" {
		t.Fatalf("identity not injected: %v", got)
	}
	if got.Get("X-Forwarded-For") == "" || got.Get("X-Forwarded-Proto") == "" {
		t.Fatalf("SetXForwarded not applied: %v", got)
	}
	// Authorization 剥离：后端零消费（P3 确认），凭据不进内网。
	if got.Get("Authorization") != "" {
		t.Fatal("Authorization must be stripped before forwarding")
	}
}

func TestProxyAnonymousNoIdentity(t *testing.T) {
	var got http.Header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	}))
	defer backend.Close()

	res := &fakeResolver{addr: strings.TrimPrefix(backend.URL, "http://")}
	rec := serve(t, res, router.Route{Package: "user", Target: "discovery:///user-identity", Timeout: 2 * time.Second}, false, nil)
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	if got.Get(identity.HeaderUserID) != "" {
		t.Fatal("anonymous request must not carry identity headers")
	}
}

func TestProxyDirectTarget(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(204)
	}))
	defer backend.Close()

	addr := strings.TrimPrefix(backend.URL, "http://")
	rec := serve(t, &fakeResolver{err: errors.New("resolver must not be used")}, router.Route{Package: "user", Target: "direct://" + addr, Timeout: time.Second}, false, nil)
	if rec.Code != 204 {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestProxyNoInstance(t *testing.T) {
	rec := serve(t, &fakeResolver{err: resolver.ErrNoInstance}, router.Route{Package: "user", Target: "discovery:///user-identity", Timeout: time.Second}, false, nil)
	if rec.Code != 503 {
		t.Fatalf("status=%d want 503", rec.Code)
	}
	if rec.Header().Get(gwerrors.HeaderReason) != "NO_HEALTHY_UPSTREAM" {
		t.Fatalf("reason=%q", rec.Header().Get(gwerrors.HeaderReason))
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != "unavailable" {
		t.Fatalf("body=%s err=%v", rec.Body.String(), err)
	}
}

func TestProxyTimeout(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(600 * time.Millisecond):
		case <-r.Context().Done():
		}
	}))
	defer backend.Close()

	res := &fakeResolver{addr: strings.TrimPrefix(backend.URL, "http://")}
	start := time.Now()
	rec := serve(t, res, router.Route{Package: "user", Target: "discovery:///user-identity", Timeout: 200 * time.Millisecond}, false, nil)
	// 504 是 connect-go ErrorWriter 对 deadline_exceeded 的实际映射。
	if rec.Code != 504 {
		t.Fatalf("status=%d want 504", rec.Code)
	}
	if rec.Header().Get(gwerrors.HeaderReason) != "UPSTREAM_TIMEOUT" {
		t.Fatalf("reason=%q", rec.Header().Get(gwerrors.HeaderReason))
	}
	if time.Since(start) > time.Second {
		t.Fatal("route timeout not applied")
	}
}

func TestProxyFeedbackOnTransportError(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := strings.TrimPrefix(backend.URL, "http://")
	backend.Close() // 立即关闭：transport 层连接拒绝

	res := &fakeResolver{addr: addr}
	rec := serve(t, res, router.Route{Package: "user", Target: "discovery:///user-identity", Timeout: time.Second}, false, nil)
	if rec.Code != 503 {
		t.Fatalf("status=%d want 503", rec.Code)
	}
	if res.lastErr.Load() == nil {
		t.Fatal("transport error must be fed back to resolver done()")
	}
}

func TestProxyBadTarget(t *testing.T) {
	rec := serve(t, &fakeResolver{}, router.Route{Package: "user", Target: "gopher://x", Timeout: time.Second}, false, nil)
	if rec.Code != 500 || rec.Header().Get(gwerrors.HeaderReason) != "BAD_ROUTE_TARGET" {
		t.Fatalf("status=%d reason=%q", rec.Code, rec.Header().Get(gwerrors.HeaderReason))
	}
}

// 上游自带 CORS 头必须被剥除（双 Access-Control-Allow-Origin 会被浏览器整单拒收——集群实测坑）。
func TestUpstreamCORSHeadersStripped(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "http://localhost:3000")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("X-Keep", "yes")
		w.WriteHeader(200)
	}))
	defer backend.Close()

	res := &fakeResolver{addr: strings.TrimPrefix(backend.URL, "http://")}
	rec := serve(t, res, router.Route{Package: "user", Target: "discovery:///user-identity", Timeout: time.Second}, false, nil)
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	if got := rec.Header().Values("Access-Control-Allow-Origin"); len(got) != 0 {
		t.Fatalf("upstream ACAO must be stripped, got %v", got)
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("upstream ACAC must be stripped")
	}
	if rec.Header().Get("X-Keep") != "yes" {
		t.Fatal("non-CORS headers must pass through")
	}
}

func TestUpstreamAddrRecorded(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer backend.Close()
	addr := strings.TrimPrefix(backend.URL, "http://")

	p := New(&fakeResolver{addr: addr}, gwerrors.NewWriter(), zap.NewNop(), http.DefaultTransport)
	req := httptest.NewRequest(http.MethodPost, "/user.v1.UserService/SignIn", nil)
	ctx := gwctx.WithRoute(req.Context(), router.Route{Package: "user", Target: "discovery:///x", Timeout: time.Second})
	ctx, up := gwctx.WithUpstream(ctx)
	p.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))

	if up.Addr != addr {
		t.Fatalf("upstream addr=%q want %q", up.Addr, addr)
	}
}
