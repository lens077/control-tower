// Package httpmw 组装网关的 HTTP 中间件：恢复、访问日志、CORS 热切换与鉴权流水线。
// 顺序契约见 docs/design/architecture.md 的请求链路。
package httpmw

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/lens077/control-tower/services/gateway/internal/authn"
	"github.com/lens077/control-tower/services/gateway/internal/authz"
	"github.com/lens077/control-tower/services/gateway/internal/gwctx"
	"github.com/lens077/control-tower/services/gateway/internal/guest"
	"github.com/lens077/control-tower/services/gateway/internal/gwerrors"
	"github.com/lens077/control-tower/services/gateway/internal/identity"
	"github.com/lens077/control-tower/services/gateway/internal/router"
	"github.com/lens077/control-tower/services/gateway/internal/session"
)

// AuthDeps 是鉴权流水线的依赖。
type AuthDeps struct {
	// Table 返回当前路由表（热更新经 atomic 替换；nil=尚未加载）。
	Table func() *router.Table
	// Verifier 做 JWT 验签 + 撤销查表。
	Verifier *authn.Verifier
	// Enforcer 做 Casbin 判定。
	Enforcer *authz.Enforcer
	// Introspect 做高危路由在线校验（fail-close）。
	Introspect Introspector
	// Roles 是角色回退源（claims 无 roles 时启用；nil=关闭）。
	Roles authn.RoleSource
	// Errors 写统一错误响应。
	Errors *gwerrors.Writer
	// Now 便于测试注入时钟；nil 用 time.Now。
	Now func() time.Time

	// ── 以下为 BFF 会话轨（ADR-0002）。Sessions 为 nil 时整轨关闭，
	// 行为退化为纯 legacy bearer，便于分阶段上线与回滚。
	//
	// 三轨识别顺序：cookie → session header → legacy bearer JWT。
	Sessions session.Store
	// SessionCookie 是会话 cookie 名。
	SessionCookie string
	// SessionHeader 是桌面端携带 session id 的头名。
	SessionHeader string
	// Refresher 在 access token 临近过期时服务端续期；
	// 续期失败即视为 IdP 否定该用户 → 删会话（账户禁用可由此传导）。
	Refresher SessionRefresher
	// OriginAllowed 判定 Origin 是否可信。cookie 轨的状态变更请求必须通过它——
	// 这是 CSRF 的第一道防线（第二道是 Connect 协议头天然触发预检，见 ADR-0002）。
	OriginAllowed func(origin string) bool

	// ── 匿名购物访客轨（B 级 RPC）。GuestCookie 为 nil 时整轨关闭：
	// 访客清单里的路径会退化成「需要登录」，与本特性上线前行为一致。
	// 设计见 ecommerce docs/design/platform/anonymous-shopping.md。
	GuestCookie *guest.CookieConfig
}

// SessionRefresher 抽象「用 refresh token 换新令牌」，避免 httpmw 依赖 bff 包。
type SessionRefresher interface {
	Refresh(ctx context.Context, refreshToken string) (access, refresh string, expiry time.Time, err error)
}

// refreshSkew 是提前续期窗：access token 剩余不足该时长即续。
const refreshSkew = 60 * time.Second

// Auth 构造鉴权流水线中间件：路径卫生 → 身份头剥离 → 路由匹配 → 匿名判定 →
// JWT/撤销 → 在线校验 → Casbin → 挂载 ctx。
func Auth(d AuthDeps) func(http.Handler) http.Handler {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 路径卫生：转义走私（%2F 等）直接拒绝；长度上限在路由层。
			if r.URL.RawPath != "" && r.URL.RawPath != r.URL.Path {
				d.Errors.Write(w, r, connect.CodeNotFound, "PATH_ESCAPED", "escaped path segments are not accepted")
				return
			}

			// 无条件剥离入站身份头（匿名与非匿名都要剥）。
			identity.Strip(r.Header)

			t := d.Table()
			if t == nil {
				d.Errors.Write(w, r, connect.CodeUnavailable, "GATEWAY_NOT_READY", "route table not loaded yet")
				return
			}
			path := r.URL.Path
			route, ok := t.Resolve(path)
			if !ok {
				d.Errors.Write(w, r, connect.CodeNotFound, "ROUTE_NOT_FOUND", "no route for this procedure")
				return
			}
			ctx := gwctx.WithRoute(r.Context(), route)

			// 访客轨（B 级）：不验 JWT、不进 RBAC，但保证有一个稳定身份。
			// 顺序上先于登录判定：已登录用户访问 B 级路径时，下面的 authenticate
			// 分支不会执行，但 proxy 注入处以 claims 优先，故不会被降级成访客。
			//
			// GuestCookie 为 nil = 整轨关闭，此时 guest 清单里的路径落回下面的
			// 登录判定（与本特性上线前行为一致，便于回滚）。
			if d.GuestCookie != nil && t.IsGuest(path) {
				gid := d.GuestCookie.FromRequest(r)
				if gid == "" {
					newID, err := guest.NewID()
					if err != nil {
						// CSPRNG 失败极罕见，但绝不能退化成「用可预测值当身份」。
						d.Errors.Write(w, r, connect.CodeInternal, "GUEST_ID_FAILED", "failed to issue guest identity")
						return
					}
					gid = newID
					d.GuestCookie.Issue(w, gid)
				}
				next.ServeHTTP(w, r.WithContext(gwctx.WithGuestID(ctx, gid)))
				return
			}

			if !t.IsAnonymous(path) {
				res, ok := d.authenticate(w, r, ctx, now())
				if !ok {
					return // 错误响应已由 authenticate 写出
				}
				claims := res.claims

				// CSRF 第一道防线：cookie 是「环境凭据」，浏览器会自动附带，
				// 因此 cookie 轨的状态变更请求必须校验 Origin。
				// header/bearer 轨不是环境凭据，攻击者拿不到，无需此校验。
				if res.viaCookie && !safeMethod(r.Method) && d.OriginAllowed != nil {
					if !d.OriginAllowed(r.Header.Get("Origin")) {
						d.Errors.Write(w, r, connect.CodePermissionDenied, "CSRF_ORIGIN_REJECTED", "origin not allowed for state-changing request")
						return
					}
				}

				if t.NeedsOnlineCheck(path) {
					if err := d.Introspect.Check(ctx, res.accessToken, claims); err != nil {
						// fail-close：在线校验任何错误都拒绝（只收窄授权）。
						d.Errors.Write(w, r, connect.CodePermissionDenied, "ONLINE_CHECK_FAILED", "online verification failed")
						return
					}
				}
				allowed, err := d.Enforcer.Allowed(claims.RoleNames(), path, r.Method)
				if err != nil {
					if errors.Is(err, authz.ErrNotReady) {
						d.Errors.Write(w, r, connect.CodeUnavailable, "AUTHZ_NOT_READY", "authorization policies not loaded yet")
						return
					}
					d.Errors.Write(w, r, connect.CodeInternal, "AUTHZ_ERROR", "authorization evaluation failed")
					return
				}
				if !allowed {
					d.Errors.Write(w, r, connect.CodePermissionDenied, "RBAC_DENIED", "role not permitted for this procedure")
					return
				}
				ctx = gwctx.WithClaims(ctx, claims)
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// authResult 是三轨认证的统一产物。
type authResult struct {
	claims      *authn.Claims
	accessToken string // 供高危路由 introspect
	viaCookie   bool   // 仅 cookie 轨需要 CSRF 校验
}

// authenticate 按 cookie → session header → legacy bearer 顺序识别身份。
// 返回 ok=false 时错误响应已写出。
func (d AuthDeps) authenticate(w http.ResponseWriter, r *http.Request, ctx context.Context, now time.Time) (authResult, bool) {
	// ── 轨一/轨二：BFF 会话（Sessions 为 nil 时整轨关闭，退化为纯 legacy）
	if d.Sessions != nil {
		if id, viaCookie := d.sessionID(r); id != "" {
			sess, err := d.Sessions.Get(ctx, id)
			if err != nil {
				d.Errors.Write(w, r, connect.CodeUnauthenticated, "SESSION_INVALID", "session not found or expired")
				return authResult{}, false
			}
			sess, err = d.ensureFresh(ctx, sess, now)
			if err != nil {
				// 续期被 IdP 拒 = 账户禁用/会话已撤，会话已在 ensureFresh 里删除。
				d.Errors.Write(w, r, connect.CodeUnauthenticated, "SESSION_REVOKED", "session rejected by identity provider")
				return authResult{}, false
			}
			return authResult{claims: claimsFromSession(sess), accessToken: sess.AccessToken, viaCookie: viaCookie}, true
		}
	}

	// ── 轨三：legacy bearer JWT。桌面端切换完成后按 bff-migration.md P4 拆除。
	token := bearerToken(r)
	if token == "" {
		d.Errors.Write(w, r, connect.CodeUnauthenticated, "JWT_MISSING", "missing session cookie or bearer token")
		return authResult{}, false
	}
	claims, err := d.Verifier.Verify(token, now)
	if err != nil {
		code, reason := classifyAuthnErr(err)
		d.Errors.Write(w, r, code, reason, "authentication failed")
		return authResult{}, false
	}
	// 角色回退源：仅 legacy 轨需要（会话轨在登录时取一次，热路径零回源）。
	if len(claims.RoleNames()) == 0 && d.Roles != nil {
		if names, rerr := d.Roles.Roles(ctx, claims.Owner, claims.Name); rerr == nil {
			for _, n := range names {
				claims.Roles = append(claims.Roles, authn.Role{Owner: claims.Owner, Name: n})
			}
		}
	}
	return authResult{claims: claims, accessToken: token}, true
}

// sessionID 取会话标识；第二个返回值标明是否来自 cookie（决定要不要查 Origin）。
func (d AuthDeps) sessionID(r *http.Request) (string, bool) {
	if d.SessionCookie != "" {
		if c, err := r.Cookie(d.SessionCookie); err == nil && c.Value != "" {
			return c.Value, true
		}
	}
	if d.SessionHeader != "" {
		if v := strings.TrimSpace(r.Header.Get(d.SessionHeader)); v != "" {
			return v, false
		}
	}
	return "", false
}

// ensureFresh 在 access token 临近过期时服务端续期，并顺带刷新角色。
// 续期失败即删会话——这条把「IdP 侧禁用账户」传导成网关侧登出。
func (d AuthDeps) ensureFresh(ctx context.Context, sess *session.Session, now time.Time) (*session.Session, error) {
	if d.Refresher == nil || sess.RefreshToken == "" {
		return sess, nil
	}
	if now.Add(refreshSkew).Before(sess.AccessExpiry) {
		return sess, nil // 仍新鲜
	}
	access, refresh, expiry, err := d.Refresher.Refresh(ctx, sess.RefreshToken)
	if err != nil {
		_ = d.Sessions.Delete(ctx, sess.ID)
		return nil, err
	}
	sess.AccessToken = access
	if refresh != "" {
		sess.RefreshToken = refresh
	}
	sess.AccessExpiry = expiry
	// 降权最迟在一个续期周期后生效；要即时请直接删会话。
	if d.Roles != nil {
		if names, rerr := d.Roles.Roles(ctx, sess.Owner, sess.Name); rerr == nil {
			sess.Roles = names
		}
	}
	_ = d.Sessions.Save(ctx, sess)
	return sess, nil
}

// claimsFromSession 把会话映射为下游统一的 claims，使身份头注入与 Casbin 同口径。
func claimsFromSession(s *session.Session) *authn.Claims {
	roles := make([]authn.Role, 0, len(s.Roles))
	for _, n := range s.Roles {
		roles = append(roles, authn.Role{Owner: s.Owner, Name: n})
	}
	c := &authn.Claims{Owner: s.Owner, Name: s.Name, Roles: roles}
	c.Subject = s.Sub
	return c
}

func safeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return h[len(prefix):]
	}
	return ""
}

func classifyAuthnErr(err error) (connect.Code, string) {
	switch {
	case errors.Is(err, authn.ErrRevoked):
		return connect.CodeUnauthenticated, "TOKEN_REVOKED"
	case errors.Is(err, authn.ErrNotAccess):
		return connect.CodeUnauthenticated, "NOT_ACCESS_TOKEN"
	case errors.Is(err, authn.ErrWrongIssuer):
		return connect.CodeUnauthenticated, "WRONG_ISSUER"
	case errors.Is(err, authn.ErrWrongAud):
		return connect.CodeUnauthenticated, "WRONG_AUDIENCE"
	case errors.Is(err, authn.ErrAccountGone):
		return connect.CodeUnauthenticated, "ACCOUNT_DISABLED"
	case errors.Is(err, authn.ErrNoKey):
		return connect.CodeUnavailable, "AUTH_NOT_READY"
	default:
		return connect.CodeUnauthenticated, "TOKEN_INVALID"
	}
}
