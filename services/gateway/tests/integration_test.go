// 端到端集成测试：file 模式装配完整链路 + 假 Connect 后端，
// 覆盖 P3 验收的本地冒烟面（真集群 smoke 在 P5 对 dev 环境执行）。
package tests

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lens077/control-tower/services/gateway/internal/app"
	"github.com/lens077/control-tower/services/gateway/internal/authn"
	"github.com/lens077/control-tower/services/gateway/internal/authz"
	"github.com/lens077/control-tower/services/gateway/internal/gwerrors"
	"github.com/lens077/control-tower/services/gateway/internal/httpmw"
	"github.com/lens077/control-tower/services/gateway/internal/loader"
	"github.com/lens077/control-tower/services/gateway/internal/resolver"

	"go.uber.org/zap"
)

const (
	issuer = "https://casdoor.apikv.com"
	aud    = "client-consumer"
)

type env struct {
	gw      *httptest.Server
	backend *httptest.Server
	key     *rsa.PrivateKey
	state   *loader.State
	// lastBackendHeaders 记录后端最近收到的请求头。
	lastBackendHeaders http.Header
}

func setup(t *testing.T) *env {
	t.Helper()
	e := &env{}

	// 假 Connect 后端：回显 procedure 与身份头。
	e.backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.lastBackendHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"echo":%q}`, r.URL.Path)
	}))
	t.Cleanup(e.backend.Close)
	backendAddr := strings.TrimPrefix(e.backend.URL, "http://")

	// 密钥与文件夹 fixtures。
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	e.key = key
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})

	dir := t.TempDir()
	writeFile := func(name, data string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("routes.yaml", fmt.Sprintf(`
version: v2
routes:
  - package: echo
    target: direct://%s
    timeout: 2s
anonymous:
  - /echo.v1.EchoService/Ping
cors:
  allow_credentials: true
  allow_origins: ["http://localhost:3000"]
  allow_headers: ["Authorization", "Content-Type", "Connect-Protocol-Version"]
  allow_methods: ["OPTIONS", "GET", "POST"]
`, backendAddr))
	writeFile("public.pem", string(pubPEM))
	writeFile("model.conf", `
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
`)
	writeFile("policies.csv", "p, consumer, /echo.v1.EchoService/*, POST, allow\n")

	// 装配（与 main 相同的 BuildHandler）。
	verifier, err := authn.New(issuer, []string{aud})
	if err != nil {
		t.Fatal(err)
	}
	enforcer := authz.New()
	cors := httpmw.NewCorsSwapper()
	e.state = loader.NewState(verifier, enforcer, cors, zap.NewNop())
	if err := loader.RunFileDir(dir, e.state); err != nil {
		t.Fatal(err)
	}

	handler := app.BuildHandler(app.Deps{
		State:      e.state,
		Cors:       cors,
		Introspect: httpmw.Disabled{},
		Resolver:   readyNoResolver{},
		Errors:     gwerrors.NewWriter(),
		Log:        zap.NewNop(),
		Transport:  http.DefaultTransport, // 后端是 h1 httptest
	})
	e.gw = httptest.NewServer(handler)
	t.Cleanup(e.gw.Close)
	return e
}

type readyNoResolver struct{}

func (readyNoResolver) Pick(string) (resolver.Instance, resolver.Done, error) {
	return resolver.Instance{}, nil, resolver.ErrUnknownSvc
}
func (readyNoResolver) Ready() bool { return true }

func (e *env) token(t *testing.T, mut ...func(*authn.Claims)) string {
	t.Helper()
	now := time.Now()
	c := &authn.Claims{
		Owner:     "lens",
		Name:      "alice",
		TokenType: authn.TokenTypeAccess,
		Roles:     []authn.Role{{Name: "consumer"}},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{aud},
			Subject:   "u-alice",
			ID:        "jti-int",
			IssuedAt:  jwt.NewNumericDate(now.Add(-time.Minute)),
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
		},
	}
	for _, m := range mut {
		m(c)
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodRS256, c).SignedString(e.key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (e *env) post(t *testing.T, path, bearer string, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.gw.URL+path, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestReadyz(t *testing.T) {
	e := setup(t)
	resp, err := http.Get(e.gw.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("readyz=%d", resp.StatusCode)
	}
}

func TestAnonymousProxied(t *testing.T) {
	e := setup(t)
	resp := e.post(t, "/echo.v1.EchoService/Ping", "", map[string]string{
		"x-md-global-user-id": "forged",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "/echo.v1.EchoService/Ping") {
		t.Fatalf("body=%s", body)
	}
	if e.lastBackendHeaders.Get("x-md-global-user-id") != "" {
		t.Fatal("forged identity must not reach backend")
	}
	if e.lastBackendHeaders.Get("X-Forwarded-For") == "" {
		t.Fatal("XFF must be set")
	}
}

func TestProtectedRequiresToken(t *testing.T) {
	e := setup(t)
	resp := e.post(t, "/echo.v1.EchoService/Do", "", nil)
	if resp.StatusCode != 401 || resp.Header.Get(gwerrors.HeaderReason) != "JWT_MISSING" {
		t.Fatalf("status=%d reason=%s", resp.StatusCode, resp.Header.Get(gwerrors.HeaderReason))
	}
}

func TestProtectedWithTokenProxiesIdentity(t *testing.T) {
	e := setup(t)
	resp := e.post(t, "/echo.v1.EchoService/Do", e.token(t), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d reason=%s", resp.StatusCode, resp.Header.Get(gwerrors.HeaderReason))
	}
	if e.lastBackendHeaders.Get("x-md-global-user-id") != "u-alice" {
		t.Fatalf("identity not injected: %v", e.lastBackendHeaders)
	}
	if e.lastBackendHeaders.Get("x-md-global-role") != "consumer" {
		t.Fatalf("role not injected: %v", e.lastBackendHeaders)
	}
}

func TestWrongRoleDenied(t *testing.T) {
	e := setup(t)
	tok := e.token(t, func(c *authn.Claims) { c.Roles = []authn.Role{{Name: "guest"}} })
	resp := e.post(t, "/echo.v1.EchoService/Do", tok, nil)
	if resp.StatusCode != 403 || resp.Header.Get(gwerrors.HeaderReason) != "RBAC_DENIED" {
		t.Fatalf("status=%d reason=%s", resp.StatusCode, resp.Header.Get(gwerrors.HeaderReason))
	}
}

func TestRefreshTokenBlocked(t *testing.T) {
	e := setup(t)
	tok := e.token(t, func(c *authn.Claims) { c.TokenType = "refresh-token" })
	resp := e.post(t, "/echo.v1.EchoService/Do", tok, nil)
	if resp.StatusCode != 401 || resp.Header.Get(gwerrors.HeaderReason) != "NOT_ACCESS_TOKEN" {
		t.Fatalf("status=%d reason=%s", resp.StatusCode, resp.Header.Get(gwerrors.HeaderReason))
	}
}

// 撤销演练：热应用撤销名单后，存量 token 秒级失效；刷新（新 iat）恢复。
func TestRevocationDrill(t *testing.T) {
	e := setup(t)
	old := e.token(t) // iat = now-1m

	if resp := e.post(t, "/echo.v1.EchoService/Do", old, nil); resp.StatusCode != 200 {
		t.Fatalf("precondition: token should work, got %d", resp.StatusCode)
	}

	rev := fmt.Sprintf("revocations:\n  - sub: u-alice\n    issued_before: %s\n    expires_at: %s\n    reason: DRILL\n",
		time.Now().Add(-30*time.Second).UTC().Format(time.RFC3339),
		time.Now().Add(30*time.Minute).UTC().Format(time.RFC3339))
	if err := e.state.Apply(loader.KeyRevocations, []byte(rev)); err != nil {
		t.Fatal(err)
	}

	if resp := e.post(t, "/echo.v1.EchoService/Do", old, nil); resp.StatusCode != 401 ||
		resp.Header.Get(gwerrors.HeaderReason) != "TOKEN_REVOKED" {
		t.Fatalf("revoked token must 401, got %d %s", resp.StatusCode, resp.Header.Get(gwerrors.HeaderReason))
	}

	fresh := e.token(t, func(c *authn.Claims) {
		c.IssuedAt = jwt.NewNumericDate(time.Now())
		c.ID = "jti-fresh"
	})
	if resp := e.post(t, "/echo.v1.EchoService/Do", fresh, nil); resp.StatusCode != 200 {
		t.Fatalf("refreshed token must pass, got %d", resp.StatusCode)
	}
}

func TestUnknownRouteConnectError(t *testing.T) {
	e := setup(t)
	resp := e.post(t, "/nope.v1.NopeService/X", "", nil)
	if resp.StatusCode != 404 || resp.Header.Get(gwerrors.HeaderReason) != "ROUTE_NOT_FOUND" {
		t.Fatalf("status=%d reason=%s", resp.StatusCode, resp.Header.Get(gwerrors.HeaderReason))
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"code"`) || !strings.Contains(string(body), "details") {
		t.Fatalf("must be connect error json with details: %s", body)
	}
}

func TestPreflightFromAllowedOrigin(t *testing.T) {
	e := setup(t)
	req, _ := http.NewRequest(http.MethodOptions, e.gw.URL+"/echo.v1.EchoService/Do", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatalf("preflight failed: %v", resp.Header)
	}
}

// 路由热更新：整表替换 + last-known-good。
func TestRouteHotSwapDrill(t *testing.T) {
	e := setup(t)
	// 非法更新被拒，旧表继续工作。
	if err := e.state.Apply(loader.KeyRoutes, []byte("version: v1\n")); err == nil {
		t.Fatal("invalid update must be rejected")
	}
	if resp := e.post(t, "/echo.v1.EchoService/Ping", "", nil); resp.StatusCode != 200 {
		t.Fatalf("old table must keep serving, got %d", resp.StatusCode)
	}
}
