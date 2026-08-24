package bff

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
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
	Log              *zap.Logger
	Now              func() time.Time
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
	if !h.redirectAllowed(redirect) {
		redirect = h.defaultRedirect()
	}

	payload, _ := json.Marshal(statePayload{State: state, Redirect: redirect})
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
	c, err := r.Cookie(stateCookieName)
	if err != nil {
		http.Error(w, "missing oauth state", http.StatusBadRequest)
		return
	}
	rawPayload, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		http.Error(w, "bad oauth state", http.StatusBadRequest)
		return
	}
	var sp statePayload
	if err := json.Unmarshal(rawPayload, &sp); err != nil {
		http.Error(w, "bad oauth state", http.StatusBadRequest)
		return
	}
	// 恒时比较，避免 state 被逐字符试探。
	if subtle.ConstantTimeCompare([]byte(sp.State), []byte(r.URL.Query().Get("state"))) != 1 {
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

func (h *Handler) sessionIDFrom(r *http.Request) string {
	if c, err := r.Cookie(h.Cookie.Name); err == nil && c.Value != "" {
		return c.Value
	}
	return ""
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
