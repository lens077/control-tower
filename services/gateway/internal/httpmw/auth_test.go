package httpmw

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lens077/control-tower/services/gateway/internal/authn"
	"github.com/lens077/control-tower/services/gateway/internal/authz"
	confv1 "github.com/lens077/control-tower/services/gateway/internal/conf/v1"
	"github.com/lens077/control-tower/services/gateway/internal/gwctx"
	"github.com/lens077/control-tower/services/gateway/internal/gwerrors"
	"github.com/lens077/control-tower/services/gateway/internal/router"

	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	issuer = "https://casdoor.apikv.com"
	aud    = "client-consumer"
)

var testNow = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

type fixture struct {
	deps AuthDeps
	key  *rsa.PrivateKey
	// captured 记录成功穿过流水线的请求。
	captured *http.Request
	next     http.Handler
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})

	v, err := authn.New(issuer, []string{aud})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.SetPublicKeyPEM(pubPEM); err != nil {
		t.Fatal(err)
	}

	az := authz.New()
	model := `
[request_definition]
r = sub, obj, act
[policy_definition]
p = sub, obj, act, eft
[role_definition]
g = _, _
[policy_effect]
e = some(where (p.eft == allow)) && !some(where (p.eft == deny))
[matchers]
m = g(r.sub, p.sub) && keyMatch2(r.obj, p.obj) && regexMatch(r.act, p.act)
`
	if err := az.SetPolicies(model, "p, customer, /user.v1.UserService/*, POST, allow\np, admin, /payment.v1.PaymentService/Refund, POST, allow\n"); err != nil {
		t.Fatal(err)
	}

	tbl, err := router.Build(&confv1.RouteConfig{
		Version: "v2",
		Routes: []*confv1.Route{
			{Package: "user", Target: "discovery:///user-identity", Timeout: durationpb.New(time.Second)},
			{Package: "payment", Target: "discovery:///payment-service", Timeout: durationpb.New(time.Second)},
		},
		Anonymous: []string{"/user.v1.UserService/SignIn"},
		Auth:      &confv1.Auth{OnlineCheckProcedures: []string{"/payment.v1.PaymentService/Refund"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	f := &fixture{key: key}
	f.deps = AuthDeps{
		Table:      func() *router.Table { return tbl },
		Verifier:   v,
		Enforcer:   az,
		Introspect: Disabled{},
		Errors:     gwerrors.NewWriter(),
		Now:        func() time.Time { return testNow },
	}
	f.next = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.captured = r
		w.WriteHeader(200)
	})
	return f
}

func (f *fixture) token(t *testing.T, mut ...func(*authn.Claims)) string {
	t.Helper()
	c := &authn.Claims{
		Owner:     "lens",
		Name:      "alice",
		TokenType: authn.TokenTypeAccess,
		Roles:     []authn.Role{{Name: "customer"}},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{aud},
			Subject:   "u-alice",
			ID:        "jti-1",
			IssuedAt:  jwt.NewNumericDate(testNow.Add(-time.Minute)),
			ExpiresAt: jwt.NewNumericDate(testNow.Add(14 * time.Minute)),
		},
	}
	for _, m := range mut {
		m(c)
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodRS256, c).SignedString(f.key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *fixture) do(t *testing.T, path, bearer string, mut ...func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	f.captured = nil
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for _, m := range mut {
		m(req)
	}
	rec := httptest.NewRecorder()
	Auth(f.deps)(f.next).ServeHTTP(rec, req)
	return rec
}

func reason(rec *httptest.ResponseRecorder) string {
	return rec.Header().Get(gwerrors.HeaderReason)
}

func TestAnonymousPassesAndStrips(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, "/user.v1.UserService/SignIn", "", func(r *http.Request) {
		r.Header.Set("x-md-global-user-id", "forged")
	})
	if rec.Code != 200 {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}
	if f.captured.Header.Get("x-md-global-user-id") != "" {
		t.Fatal("forged identity header must be stripped on anonymous path")
	}
	if _, ok := gwctx.Route(f.captured.Context()); !ok {
		t.Fatal("route must be attached")
	}
	if gwctx.Claims(f.captured.Context()) != nil {
		t.Fatal("anonymous request must not carry claims")
	}
}

func TestMissingTokenRejected(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, "/user.v1.UserService/GetProfile", "")
	if rec.Code != 401 || reason(rec) != "JWT_MISSING" {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}
}

func TestValidTokenAllowed(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, "/user.v1.UserService/GetProfile", f.token(t), func(r *http.Request) {
		r.Header.Set("X-Md-Global-Role", "admin") // 伪造头
	})
	if rec.Code != 200 {
		t.Fatalf("status=%d reason=%s body=%s", rec.Code, reason(rec), rec.Body.String())
	}
	c := gwctx.Claims(f.captured.Context())
	if c == nil || c.UserID() != "u-alice" {
		t.Fatalf("claims missing: %+v", c)
	}
	if f.captured.Header.Get("x-md-global-role") != "" {
		t.Fatal("forged role header must be stripped")
	}
}

func TestRefreshTokenRejected(t *testing.T) {
	f := newFixture(t)
	tok := f.token(t, func(c *authn.Claims) { c.TokenType = "refresh-token" })
	rec := f.do(t, "/user.v1.UserService/GetProfile", tok)
	if rec.Code != 401 || reason(rec) != "NOT_ACCESS_TOKEN" {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}
}

func TestRevokedTokenRejected(t *testing.T) {
	f := newFixture(t)
	table, err := authn.ParseRevocations([]byte(`
revocations:
  - sub: u-alice
    issued_before: 2026-08-23T11:59:30Z
    expires_at: 2026-08-23T12:30:00Z
    reason: ROLE_CHANGED
`), testNow)
	if err != nil {
		t.Fatal(err)
	}
	f.deps.Verifier.SetRevocations(table)
	rec := f.do(t, "/user.v1.UserService/GetProfile", f.token(t))
	if rec.Code != 401 || reason(rec) != "TOKEN_REVOKED" {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}
}

func TestRBACDenied(t *testing.T) {
	f := newFixture(t)
	// customer 角色打 payment.Refund：策略只给 admin。
	tok := f.token(t)
	rec := f.do(t, "/payment.v1.PaymentService/Refund", tok)
	// Refund 同时是 online_check 路由，Disabled introspector 先 fail-close。
	if rec.Code != 403 || reason(rec) != "ONLINE_CHECK_FAILED" {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}
}

func TestOnlineCheckPassThenRBAC(t *testing.T) {
	f := newFixture(t)
	f.deps.Introspect = introspectFunc(func(context.Context, string, *authn.Claims) error { return nil })

	// admin 角色 + introspect 放行 → 通过。
	adminTok := f.token(t, func(c *authn.Claims) { c.Roles = []authn.Role{{Name: "admin"}} })
	rec := f.do(t, "/payment.v1.PaymentService/Refund", adminTok)
	if rec.Code != 200 {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}

	// customer 角色 + introspect 放行 → RBAC 拒绝。
	rec = f.do(t, "/payment.v1.PaymentService/Refund", f.token(t))
	if rec.Code != 403 || reason(rec) != "RBAC_DENIED" {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}
}

type introspectFunc func(context.Context, string, *authn.Claims) error

func (fn introspectFunc) Check(ctx context.Context, tok string, c *authn.Claims) error {
	return fn(ctx, tok, c)
}

func TestOnlineCheckFailCloses(t *testing.T) {
	f := newFixture(t)
	f.deps.Introspect = introspectFunc(func(context.Context, string, *authn.Claims) error {
		return errors.New("casdoor unreachable")
	})
	adminTok := f.token(t, func(c *authn.Claims) { c.Roles = []authn.Role{{Name: "admin"}} })
	rec := f.do(t, "/payment.v1.PaymentService/Refund", adminTok)
	if rec.Code != 403 || reason(rec) != "ONLINE_CHECK_FAILED" {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}
}

func TestEscapedPathRejected(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/user.v1.UserService/GetProfile", nil)
	req.URL = &url.URL{Path: "/user.v1.UserService/Get/Profile", RawPath: "/user.v1.UserService/Get%2FProfile"}
	rec := httptest.NewRecorder()
	Auth(f.deps)(f.next).ServeHTTP(rec, req)
	if rec.Code != 404 || reason(rec) != "PATH_ESCAPED" {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}
}

func TestTableNotReady(t *testing.T) {
	f := newFixture(t)
	f.deps.Table = func() *router.Table { return nil }
	rec := f.do(t, "/user.v1.UserService/SignIn", "")
	if rec.Code != 503 || reason(rec) != "GATEWAY_NOT_READY" {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}
}

func TestUnknownRoute(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, "/order.v1.OrderService/CreateOrder", "")
	if rec.Code != 404 || reason(rec) != "ROUTE_NOT_FOUND" {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}
}

// 回退分支：claims 无 roles 时经 RoleSource 补齐（P3 真 token 实测本部署 JWT 不嵌角色）。
func TestRoleSourceFallback(t *testing.T) {
	f := newFixture(t)
	f.deps.Roles = roleSourceFunc(func(_ context.Context, owner, name string) ([]string, error) {
		if owner != "lens" || name != "alice" {
			t.Errorf("unexpected identity %s/%s", owner, name)
		}
		return []string{"customer"}, nil
	})
	// token 不带任何角色。
	tok := f.token(t, func(c *authn.Claims) { c.Roles = nil })
	rec := f.do(t, "/user.v1.UserService/GetProfile", tok)
	if rec.Code != 200 {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}
	// 回填进 claims：下游身份头与授权口径一致。
	c := gwctx.Claims(f.captured.Context())
	if len(c.RoleNames()) != 1 || c.RoleNames()[0] != "customer" {
		t.Fatalf("claims roles=%v", c.RoleNames())
	}
}

// 回退源失败 → 无角色 → RBAC 拒绝（收窄不放大）。
func TestRoleSourceErrorDenies(t *testing.T) {
	f := newFixture(t)
	f.deps.Roles = roleSourceFunc(func(context.Context, string, string) ([]string, error) {
		return nil, errors.New("casdoor down")
	})
	tok := f.token(t, func(c *authn.Claims) { c.Roles = nil })
	rec := f.do(t, "/user.v1.UserService/GetProfile", tok)
	if rec.Code != 403 || reason(rec) != "RBAC_DENIED" {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}
}

type roleSourceFunc func(context.Context, string, string) ([]string, error)

func (fn roleSourceFunc) Roles(ctx context.Context, owner, name string) ([]string, error) {
	return fn(ctx, owner, name)
}

func TestAuthzNotReady(t *testing.T) {
	f := newFixture(t)
	f.deps.Enforcer = authz.New() // 未加载策略
	rec := f.do(t, "/user.v1.UserService/GetProfile", f.token(t))
	if rec.Code != 503 || reason(rec) != "AUTHZ_NOT_READY" {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}
}
