package bff

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lens077/control-tower/services/gateway/internal/authn"
	"github.com/lens077/control-tower/services/gateway/internal/session"

	"go.uber.org/zap"
)

// CookieConfig 决定会话 cookie 的属性。
//
// 不用 __Host- 前缀：该前缀禁止 Domain 属性，而 shop/gateway 分属两个子域，
// 需要 Domain=.apikv.com 才能共享（二者同属 apikv.com，是 same-site，
// 因此 SameSite=Lax 即可，无需 None，也不受三方 cookie 淘汰影响）。
type CookieConfig struct {
	Name     string
	Domain   string
	Path     string
	Secure   bool
	SameSite http.SameSite
}

// DefaultCookieConfig 返回生产缺省值。
func DefaultCookieConfig() CookieConfig {
	return CookieConfig{
		Name:     "__Secure-ct_session",
		Path:     "/",
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
}

const (
	stateCookieName = "ct_oauth_state"
	stateTTL        = 10 * time.Minute
	// defaultSessionHeader 与 httpmw 的 SessionHeader 默认值一致。
	defaultSessionHeader = "X-CT-Session"
)

// Handler 提供 /auth/{login,callback,logout,me}。
type Handler struct {
	Store    session.Store
	Casdoor  *CasdoorClient
	Verifier *authn.Verifier
	Roles    authn.RoleSource
	Cookie   CookieConfig
	// PublicBaseURL 是网关对外基地址，用于拼 redirect_uri。
	// 必须显式配置，不从 Host 头推导——那会给 host 头注入留口子。
	PublicBaseURL string
	// AllowedRedirects 是登录后允许跳回的前端来源白名单（防开放重定向）。
	AllowedRedirects []string
	// SessionHeader 是原生客户端携带 session id 的头名；空则用默认值。
	SessionHeader string
	Log           *zap.Logger
	Now           func() time.Time
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Handler) redirectURI() string {
	return strings.TrimSuffix(h.PublicBaseURL, "/") + "/auth/callback"
}

// Handler 返回 BFF 端点处理器，供 app 层套中间件后整体挂载。
func (h *Handler) Handler() http.Handler {
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

// Register 把 BFF 端点挂到 mux；调用方需保证它们先于包路由注册（永不被代理）。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/auth/login", h.login)
	mux.HandleFunc("/auth/callback", h.callback)
	mux.HandleFunc("/auth/logout", h.logout)
	mux.HandleFunc("/auth/me", h.me)
}

type statePayload struct {
	State    string `json:"s"`
	Redirect string `json:"r"`
	// Native 标记桌面端（Tauri）流程：会话 id 经 loopback 回调交回原生客户端，
	// 而不是下发 cookie——原生窗口的源是 tauri://localhost，拿不到浏览器 cookie。
	// 见 bff-migration.md P3。
	Native bool `json:"n,omitempty"`
}

// isLoopback 判定是否为本机回环地址。
// native 模式只接受回环回调：会话 id 会出现在回调 URL 上，只有回环才不出本机。
func isLoopback(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// login 生成 state 并跳 Casdoor。state 存进短时 httpOnly cookie，回调时比对——
// 不落服务端状态，省一次存储往返。
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	state := base64.RawURLEncoding.EncodeToString(raw)

	redirect := r.URL.Query().Get("redirect")
	// native=桌面端：回调必须是回环地址，且不走 AllowedRedirects 白名单
	// （每次运行的端口都不同，白名单表达不了）。回环本身就是边界。
	native := r.URL.Query().Get("mode") == "native"
	if native {
		if !isLoopback(redirect) {
			http.Error(w, "native mode requires a loopback redirect", http.StatusBadRequest)
			return
		}
	} else if !h.redirectAllowed(redirect) {
		redirect = h.defaultRedirect()
	}

	payload, _ := json.Marshal(statePayload{State: state, Redirect: redirect, Native: native})
	if native {
		// 原生客户端：state 必须存服务端，不能依赖登录子窗口的 cookie——
		// Tauri 子窗口是独立 WebView，回写的 cookie 在回调时拿不到
		// （2026-08-24 真机实测：missing oauth state）。
		// 安全性由「state 不可猜 + 单次使用 + 回调必须回环」共同保证。
		if err := h.Store.PutState(r.Context(), state, payload, stateTTL); err != nil {
			h.Log.Error("put oauth state failed", zap.Error(err))
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    base64.RawURLEncoding.EncodeToString(payload),
		Path:     "/auth",
		Domain:   h.Cookie.Domain,
		MaxAge:   int(stateTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.Cookie.Secure,
		SameSite: h.Cookie.SameSite,
	})
	http.Redirect(w, r, h.Casdoor.AuthorizeURL(h.redirectURI(), state), http.StatusFound)
}

// callback 校验 state → 换令牌 → 取角色 → 建会话 → 下发 cookie → 跳回前端。
func (h *Handler) callback(w http.ResponseWriter, r *http.Request) {
	queryState := r.URL.Query().Get("state")
	var sp statePayload
	if c, cerr := r.Cookie(stateCookieName); cerr == nil {
		// 浏览器流程：state 在 httpOnly cookie 里，天然与本浏览器绑定。
		rawPayload, derr := base64.RawURLEncoding.DecodeString(c.Value)
		if derr != nil || json.Unmarshal(rawPayload, &sp) != nil {
			http.Error(w, "bad oauth state", http.StatusBadRequest)
			return
		}
	} else if queryState != "" {
		// 原生流程：回落到服务端 state（取出即删，单次使用）。
		raw, serr := h.Store.TakeState(r.Context(), queryState)
		if serr != nil {
			http.Error(w, "missing oauth state", http.StatusBadRequest)
			return
		}
		if json.Unmarshal(raw, &sp) != nil {
			http.Error(w, "bad oauth state", http.StatusBadRequest)
			return
		}
	} else {
		http.Error(w, "missing oauth state", http.StatusBadRequest)
		return
	}
	// 恒时比较，避免 state 被逐字符试探。
	if subtle.ConstantTimeCompare([]byte(sp.State), []byte(queryState)) != 1 {
		http.Error(w, "oauth state mismatch", http.StatusBadRequest)
		return
	}
	h.clearCookie(w, stateCookieName, "/auth")

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	tokens, err := h.Casdoor.Exchange(r.Context(), code, h.redirectURI())
	if err != nil {
		h.Log.Warn("code exchange failed", zap.Error(err))
		http.Error(w, "login failed", http.StatusUnauthorized)
		return
	}

	sess, err := h.buildSession(r, tokens)
	if err != nil {
		h.Log.Warn("session build failed", zap.Error(err))
		http.Error(w, "login failed", http.StatusUnauthorized)
		return
	}
	if err := h.Store.Create(r.Context(), sess); err != nil {
		h.Log.Error("session create failed", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if sp.Native {
		// 桌面端：会话 id 经回环回调交回原生客户端，不下发 cookie（它也收不到）。
		// 参数名沿用 code/state 是刻意的——Tauri 侧 Rust 拦截器就认这两个 key，
		// 于是桌面端切换不需要动 Rust、不需要重建原生层。
		target, err := url.Parse(sp.Redirect)
		if err != nil {
			http.Error(w, "bad native redirect", http.StatusBadRequest)
			return
		}
		q := target.Query()
		q.Set("code", sess.ID)
		q.Set("state", sp.State)
		target.RawQuery = q.Encode()
		http.Redirect(w, r, target.String(), http.StatusFound)
		return
	}
	h.setSessionCookie(w, sess.ID)
	http.Redirect(w, r, sp.Redirect, http.StatusFound)
}

// buildSession 用刚拿到的令牌构造会话：验签取身份，再补角色。
func (h *Handler) buildSession(r *http.Request, tokens *Tokens) (*session.Session, error) {
	now := h.now()
	claims, err := h.Verifier.Verify(tokens.AccessToken, now)
	if err != nil {
		return nil, err
	}
	id, err := session.NewID()
	if err != nil {
		return nil, err
	}
	roles := claims.RoleNames()
	// 本部署的 Casdoor 不把 roles 嵌进 claims（实测），登录时补一次即可——
	// 之后整段会话都不再回源，比每请求回源省得多。
	if len(roles) == 0 && h.Roles != nil {
		if names, rerr := h.Roles.Roles(r.Context(), claims.Owner, claims.Name); rerr == nil {
			roles = names
		} else {
			h.Log.Warn("role fetch failed at login", zap.Error(rerr))
		}
	}
	return &session.Session{
		ID:           id,
		Sub:          claims.UserID(),
		Owner:        claims.Owner,
		Name:         claims.Name,
		Roles:        roles,
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		AccessExpiry: tokens.ExpiresAt(now),
		CreatedAt:    now,
		LastSeenAt:   now,
		UserAgent:    r.UserAgent(),
	}, nil
}

// logout 删会话并清 cookie。撤权的正式手段就是这一下（即时生效）。
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if id := h.sessionIDFrom(r); id != "" {
		if err := h.Store.Delete(r.Context(), id); err != nil {
			h.Log.Warn("session delete failed", zap.Error(err))
		}
	}
	h.clearCookie(w, h.Cookie.Name, h.Cookie.Path)
	w.WriteHeader(http.StatusNoContent)
}

// me 返回前端渲染所需的最小身份信息——**不含任何 token**。
func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	id := h.sessionIDFrom(r)
	if id == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": false})
		return
	}
	sess, err := h.Store.Get(r.Context(), id)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": false})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"authenticated": true,
		"name":          sess.Name,
		"owner":         sess.Owner,
		"roles":         sess.Roles,
		"createdAt":     sess.CreatedAt,
	})
}

// sessionIDFrom 取会话标识：cookie（浏览器）或会话头（原生客户端）。
//
// 必须两者都认——桌面端根本没有 cookie，只读 cookie 会让它登录成功后
// /auth/me 恒返回未登录、/auth/logout 也删不掉会话（实测踩过）。
func (h *Handler) sessionIDFrom(r *http.Request) string {
	if c, err := r.Cookie(h.Cookie.Name); err == nil && c.Value != "" {
		return c.Value
	}
	name := h.SessionHeader
	if name == "" {
		name = defaultSessionHeader
	}
	return strings.TrimSpace(r.Header.Get(name))
}

func (h *Handler) setSessionCookie(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.Cookie.Name,
		Value:    id,
		Path:     h.Cookie.Path,
		Domain:   h.Cookie.Domain,
		HttpOnly: true,
		Secure:   h.Cookie.Secure,
		SameSite: h.Cookie.SameSite,
	})
}

func (h *Handler) clearCookie(w http.ResponseWriter, name, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		Domain:   h.Cookie.Domain,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.Cookie.Secure,
		SameSite: h.Cookie.SameSite,
	})
}

// redirectAllowed 防开放重定向：只允许相对路径或白名单来源。
func (h *Handler) redirectAllowed(target string) bool {
	if target == "" {
		return false
	}
	if strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "//") {
		return true
	}
	for _, allowed := range h.AllowedRedirects {
		if strings.HasPrefix(target, strings.TrimSuffix(allowed, "/")+"/") || target == allowed {
			return true
		}
	}
	return false
}

func (h *Handler) defaultRedirect() string {
	if len(h.AllowedRedirects) > 0 {
		return h.AllowedRedirects[0]
	}
	return "/"
}
