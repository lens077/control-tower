package bff

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lens077/control-tower/services/gateway/internal/authn"
	"github.com/lens077/control-tower/services/gateway/internal/session"

	"go.uber.org/zap"
)

const (
	issuer = "https://casdoor.example"
	aud    = "client-app"
	front  = "https://shop.example"
)

var now = time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

type harness struct {
	h       *Handler
	store   session.Store
	key     *rsa.PrivateKey
	casdoor *httptest.Server
	mux     *http.ServeMux
}

func newHarness(t *testing.T, tokenHandler http.HandlerFunc) *harness {
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

	cas := httptest.NewServer(tokenHandler)
	t.Cleanup(cas.Close)

	store := session.NewMemoryStore(session.DefaultTTL())
	h := &Handler{
		Store:            store,
		Casdoor:          NewCasdoorClient(cas.URL, "cid", "csecret"),
		Verifier:         v,
		Cookie:           CookieConfig{Name: "ct_session", Path: "/", SameSite: http.SameSiteLaxMode},
		PublicBaseURL:    "https://gateway.example",
		AllowedRedirects: []string{front},
		Log:              zap.NewNop(),
		Now:              func() time.Time { return now },
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return &harness{h: h, store: store, key: key, casdoor: cas, mux: mux}
}

func (hs *harness) mintToken(t *testing.T) string {
	t.Helper()
	claims := &authn.Claims{
		Owner: "lens", Name: "alice", TokenType: authn.TokenTypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: issuer, Audience: jwt.ClaimStrings{aud}, Subject: "u-alice",
			IssuedAt: jwt.NewNumericDate(now.Add(-time.Minute)), ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
		},
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(hs.key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLoginRedirectsWithState(t *testing.T) {
	hs := newHarness(t, func(http.ResponseWriter, *http.Request) {})
	rec := httptest.NewRecorder()
	hs.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/login?redirect=/cart", nil))

	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := loc.Query()
	if q.Get("client_id") != "cid" || q.Get("response_type") != "code" || q.Get("state") == "" {
		t.Fatalf("bad authorize url: %s", loc)
	}
	if q.Get("redirect_uri") != "https://gateway.example/auth/callback" {
		t.Fatalf("redirect_uri=%s（必须指向网关而非前端）", q.Get("redirect_uri"))
	}
	// state 必须落进 httpOnly cookie。
	var stateCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == stateCookieName {
			stateCookie = c
		}
	}
	if stateCookie == nil || !stateCookie.HttpOnly {
		t.Fatalf("state cookie missing or not httpOnly: %+v", stateCookie)
	}
}

// 防开放重定向：非白名单的绝对地址一律落回默认前端。
func TestLoginRejectsOpenRedirect(t *testing.T) {
	hs := newHarness(t, func(http.ResponseWriter, *http.Request) {})
	rec := httptest.NewRecorder()
	hs.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/login?redirect=https://evil.example/x", nil))

	var sc *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == stateCookieName {
			sc = c
		}
	}
	if sc == nil {
		t.Fatal("no state cookie")
	}
	raw, _ := base64Decode(sc.Value)
	var sp statePayload
	if err := json.Unmarshal(raw, &sp); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sp.Redirect, "evil.example") {
		t.Fatalf("open redirect leaked: %s", sp.Redirect)
	}
}

func base64Decode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

func TestCallbackCreatesSession(t *testing.T) {
	var hs *harness
	hs = newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "access_token") {
			t.Errorf("unexpected casdoor path %s", r.URL.Path)
		}
		_ = r.ParseForm()
		if r.Form.Get("client_secret") != "csecret" || r.Form.Get("grant_type") != "authorization_code" {
			t.Errorf("bad exchange form: %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"rt","expires_in":900}`, hs.mintToken(t))
	})
	hs.h.Roles = roleSourceFunc(func(owner, name string) []string { return []string{"consumer"} })

	// 先 login 拿 state cookie。
	loginRec := httptest.NewRecorder()
	hs.mux.ServeHTTP(loginRec, httptest.NewRequest(http.MethodGet, "/auth/login?redirect=/cart", nil))
	var stateCookie *http.Cookie
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == stateCookieName {
			stateCookie = c
		}
	}
	raw, _ := base64Decode(stateCookie.Value)
	var sp statePayload
	_ = json.Unmarshal(raw, &sp)

	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=abc&state="+url.QueryEscape(sp.State), nil)
	req.AddCookie(stateCookie)
	rec := httptest.NewRecorder()
	hs.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/cart" {
		t.Fatalf("status=%d loc=%s body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	var sessCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "ct_session" {
			sessCookie = c
		}
	}
	if sessCookie == nil || !sessCookie.HttpOnly || sessCookie.Value == "" {
		t.Fatalf("session cookie missing/not httpOnly: %+v", sessCookie)
	}
	// 会话里必须有角色，且 token 留在服务端。
	s, err := hs.store.Get(req.Context(), sessCookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	if s.Sub != "u-alice" || len(s.Roles) != 1 || s.RefreshToken != "rt" {
		t.Fatalf("session=%+v", s)
	}
}

func TestCallbackRejectsStateMismatch(t *testing.T) {
	hs := newHarness(t, func(http.ResponseWriter, *http.Request) {
		t.Error("state 不匹配时不应该去换令牌")
	})
	payload, _ := json.Marshal(statePayload{State: "expected", Redirect: "/"})
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=abc&state=attacker", nil)
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: base64.RawURLEncoding.EncodeToString(payload)})
	rec := httptest.NewRecorder()
	hs.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestMeAndLogout(t *testing.T) {
	hs := newHarness(t, func(http.ResponseWriter, *http.Request) {})
	_ = hs.store.Create(t.Context(), &session.Session{
		ID: "sid", Sub: "u-alice", Owner: "lens", Name: "alice",
		Roles: []string{"consumer"}, CreatedAt: now,
	})

	// 未认证
	rec := httptest.NewRecorder()
	hs.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/me", nil))
	if !strings.Contains(rec.Body.String(), `"authenticated":false`) {
		t.Fatalf("body=%s", rec.Body.String())
	}

	// 已认证：返回身份但**绝不含 token**
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: "ct_session", Value: "sid"})
	rec = httptest.NewRecorder()
	hs.mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `"authenticated":true`) || !strings.Contains(body, "alice") {
		t.Fatalf("body=%s", body)
	}
	if strings.Contains(body, "access_token") || strings.Contains(body, "refresh") {
		t.Fatalf("/auth/me 泄露了令牌: %s", body)
	}

	// 登出 = 删会话（即时撤权）
	req = httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: "ct_session", Value: "sid"})
	rec = httptest.NewRecorder()
	hs.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d", rec.Code)
	}
	if _, err := hs.store.Get(req.Context(), "sid"); err != session.ErrNotFound {
		t.Fatal("登出必须删除会话")
	}
}

type roleSourceFunc func(owner, name string) []string

func (f roleSourceFunc) Roles(_ context.Context, owner, name string) ([]string, error) {
	return f(owner, name), nil
}
