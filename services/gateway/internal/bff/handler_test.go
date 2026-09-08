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

	store := session.NewMemoryStoreWithClock(session.DefaultTTL(), func() time.Time { return now })
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
	// cookie 只放 state 本身；载荷在服务端。
	raw, err := hs.store.TakeState(t.Context(), sc.Value)
	if err != nil {
		t.Fatalf("state 未落到服务端: %v", err)
	}
	var sp statePayload
	if err := json.Unmarshal(raw, &sp); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sp.Redirect, "evil.example") {
		t.Fatalf("open redirect leaked: %s", sp.Redirect)
	}
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
	hs.h.Roles = roleSourceFunc(func(owner, name string) []string { return []string{"customer"} })

	// 先 login 拿 state cookie。
	loginRec := httptest.NewRecorder()
	hs.mux.ServeHTTP(loginRec, httptest.NewRequest(http.MethodGet, "/auth/login?redirect=/cart", nil))
	var stateCookie *http.Cookie
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == stateCookieName {
			stateCookie = c
		}
	}
	// cookie 里就是 state 本身（载荷在服务端，客户端携带不到）。
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=abc&state="+url.QueryEscape(stateCookie.Value), nil)
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
	// 正常发起登录，但回调时 query 里换成另一个 state。
	loginRec := httptest.NewRecorder()
	hs.mux.ServeHTTP(loginRec, httptest.NewRequest(http.MethodGet, "/auth/login?redirect=/cart", nil))
	var sc *http.Cookie
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == stateCookieName {
			sc = c
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=abc&state=attacker", nil)
	req.AddCookie(sc)
	rec := httptest.NewRecorder()
	hs.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}

// 安全回归：/auth/callback 不接受客户端自带的 state 载荷。
//
// 背景（2026-09-08 审计）：早期版本把 {state,redirect,native} base64 后放进
// cookie 由客户端携带，而 redirect/native 只在 /auth/login 里校验过一次。
// 攻击者跳过第 1 步、自造一份载荷，就能让第 2 步把新建会话 id 拼进 URL
// 送去站外域名——「必须先走第 1 步」只是界面顺序，不是服务端保证。
func TestCallbackRejectsForgedStatePayload(t *testing.T) {
	hs := newHarness(t, func(http.ResponseWriter, *http.Request) {
		t.Error("伪造的 state 载荷不该走到换令牌这一步")
	})
	forged := statePayload{
		State:    "attacker-chosen-state",
		Redirect: "https://evil.example/steal",
		Native:   true, // 想诱使回调把会话 id 拼进 URL
	}
	payload, _ := json.Marshal(forged)

	// 两种携带姿势都试：旧格式（base64 载荷）与直接把 state 当 cookie 值。
	for name, cookieValue := range map[string]string{
		"旧格式载荷 cookie": base64.RawURLEncoding.EncodeToString(payload),
		"仅 state 值":    forged.State,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet,
				"/auth/callback?code=abc&state="+url.QueryEscape(forged.State), nil)
			req.AddCookie(&http.Cookie{Name: stateCookieName, Value: cookieValue})
			rec := httptest.NewRecorder()
			hs.mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d loc=%s（未经 /auth/login 的 state 必须拒绝）",
					rec.Code, rec.Header().Get("Location"))
			}
			if loc := rec.Header().Get("Location"); strings.Contains(loc, "evil.example") {
				t.Fatalf("会话 id 被送往站外: %s", loc)
			}
		})
	}
}

// 浏览器轨必须带 state cookie：否则攻击者可拿自己诱出的 code 在别的浏览器里完成回调。
// 原生轨的豁免由 TestNativeCallbackWorksWithoutStateCookie 单独覆盖。
func TestCallbackRequiresStateCookieForBrowserFlow(t *testing.T) {
	hs := newHarness(t, func(http.ResponseWriter, *http.Request) {
		t.Error("缺少 state cookie 时不应该去换令牌")
	})
	loginRec := httptest.NewRecorder()
	hs.mux.ServeHTTP(loginRec, httptest.NewRequest(http.MethodGet, "/auth/login?redirect=/cart", nil))
	authURL, _ := url.Parse(loginRec.Header().Get("Location"))
	state := authURL.Query().Get("state")

	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=abc&state="+url.QueryEscape(state), nil)
	// 刻意不带 cookie
	rec := httptest.NewRecorder()
	hs.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d（浏览器轨缺 state cookie 必须拒绝）", rec.Code)
	}
}

func TestMeAndLogout(t *testing.T) {
	hs := newHarness(t, func(http.ResponseWriter, *http.Request) {})
	_ = hs.store.Create(t.Context(), &session.Session{
		ID: "sid", Sub: "u-alice", Owner: "lens", Name: "alice",
		Roles: []string{"customer"}, CreatedAt: now,
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

// native 模式（桌面端）：会话 id 经回环回调交回，不下发 cookie。
func TestNativeCallbackReturnsSessionViaLoopback(t *testing.T) {
	var hs *harness
	hs = newHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"rt","expires_in":900}`, hs.mintToken(t))
	})
	hs.h.Roles = roleSourceFunc(func(string, string) []string { return []string{"customer"} })

	loopback := "http://127.0.0.1:54321/oauth/callback"
	loginRec := httptest.NewRecorder()
	hs.mux.ServeHTTP(loginRec, httptest.NewRequest(http.MethodGet,
		"/auth/login?mode=native&redirect="+url.QueryEscape(loopback), nil))
	if loginRec.Code != http.StatusFound {
		t.Fatalf("login status=%d body=%s", loginRec.Code, loginRec.Body.String())
	}
	var stateCookie *http.Cookie
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == stateCookieName {
			stateCookie = c
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=abc&state="+url.QueryEscape(stateCookie.Value), nil)
	req.AddCookie(stateCookie)
	rec := httptest.NewRecorder()
	hs.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	sid := loc.Query().Get("code")
	if sid == "" || loc.Query().Get("state") != stateCookie.Value {
		t.Fatalf("loopback callback missing session/state: %s", loc)
	}
	// 会话必须真的建出来，且 id 与回调里给的一致。
	if _, err := hs.store.Get(req.Context(), sid); err != nil {
		t.Fatalf("session not created: %v", err)
	}
	// native 模式不下发 cookie——原生窗口收不到，发了只会造成误解。
	for _, c := range rec.Result().Cookies() {
		if c.Name == "ct_session" && c.Value != "" {
			t.Fatal("native mode must not set a session cookie")
		}
	}
}

// native 模式拒绝非回环回调：会话 id 会出现在 URL 上，不能送出本机。
func TestNativeRejectsNonLoopbackRedirect(t *testing.T) {
	hs := newHarness(t, func(http.ResponseWriter, *http.Request) {})
	rec := httptest.NewRecorder()
	hs.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/auth/login?mode=native&redirect="+url.QueryEscape("https://evil.example/grab"), nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d（非回环必须拒绝）", rec.Code)
	}
}

// 原生流程：**不带 state cookie** 也能完成回调（state 存服务端）。
// 这是 2026-08-24 真机实测「missing oauth state」的回归——Tauri 登录子窗口
// 是独立 WebView，回写的 cookie 在回调时拿不到。
func TestNativeCallbackWorksWithoutStateCookie(t *testing.T) {
	var hs *harness
	hs = newHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"rt","expires_in":900}`, hs.mintToken(t))
	})
	hs.h.Roles = roleSourceFunc(func(string, string) []string { return []string{"customer"} })

	loopback := "http://127.0.0.1:54321/oauth/callback"
	loginRec := httptest.NewRecorder()
	hs.mux.ServeHTTP(loginRec, httptest.NewRequest(http.MethodGet,
		"/auth/login?mode=native&redirect="+url.QueryEscape(loopback), nil))

	// 从跳转地址里取出 state（模拟 Casdoor 原样回传），**刻意不带任何 cookie**。
	authURL, _ := url.Parse(loginRec.Header().Get("Location"))
	state := authURL.Query().Get("state")
	if state == "" {
		t.Fatal("no state in authorize url")
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=abc&state="+url.QueryEscape(state), nil)
	rec := httptest.NewRecorder()
	hs.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s（无 cookie 的原生回调必须成功）", rec.Code, rec.Body.String())
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Query().Get("code") == "" {
		t.Fatalf("回调未交回 session id: %s", loc)
	}

	// state 单次使用：重放必须失败。
	replay := httptest.NewRequest(http.MethodGet, "/auth/callback?code=abc&state="+url.QueryEscape(state), nil)
	rec2 := httptest.NewRecorder()
	hs.mux.ServeHTTP(rec2, replay)
	if rec2.Code == http.StatusFound {
		t.Fatal("state 必须单次使用，重放不能成功")
	}
}

// 原生客户端用会话头访问 /auth/me 与 /auth/logout（桌面端没有 cookie）。
// 回归：只读 cookie 会让桌面端登录成功后仍显示未登录、登出也删不掉会话。
func TestSessionHeaderWorksForMeAndLogout(t *testing.T) {
	hs := newHarness(t, func(http.ResponseWriter, *http.Request) {})
	_ = hs.store.Create(t.Context(), &session.Session{
		ID: "sid-native", Sub: "u-alice", Owner: "lens", Name: "alice",
		Roles: []string{"customer"}, CreatedAt: now,
	})

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.Header.Set("X-CT-Session", "sid-native") // 刻意不带任何 cookie
	rec := httptest.NewRecorder()
	hs.mux.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"authenticated":true`) {
		t.Fatalf("会话头必须被 /auth/me 识别: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.Header.Set("X-CT-Session", "sid-native")
	rec = httptest.NewRecorder()
	hs.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d", rec.Code)
	}
	if _, err := hs.store.Get(t.Context(), "sid-native"); err != session.ErrNotFound {
		t.Fatal("原生客户端登出必须删除会话")
	}
}
