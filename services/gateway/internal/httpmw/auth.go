// Package httpmw 组装网关的 HTTP 中间件：恢复、访问日志、CORS 热切换与鉴权流水线。
// 顺序契约见 docs/design/architecture.md 的请求链路。
package httpmw

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/lens077/control-tower/services/gateway/internal/authn"
	"github.com/lens077/control-tower/services/gateway/internal/authz"
	"github.com/lens077/control-tower/services/gateway/internal/gwctx"
	"github.com/lens077/control-tower/services/gateway/internal/gwerrors"
	"github.com/lens077/control-tower/services/gateway/internal/identity"
	"github.com/lens077/control-tower/services/gateway/internal/router"
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
	// Errors 写统一错误响应。
	Errors *gwerrors.Writer
	// Now 便于测试注入时钟；nil 用 time.Now。
	Now func() time.Time
}

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

			if !t.IsAnonymous(path) {
				token := bearerToken(r)
				if token == "" {
					d.Errors.Write(w, r, connect.CodeUnauthenticated, "JWT_MISSING", "missing bearer token")
					return
				}
				claims, err := d.Verifier.Verify(token, now())
				if err != nil {
					code, reason := classifyAuthnErr(err)
					d.Errors.Write(w, r, code, reason, "authentication failed")
					return
				}
				if t.NeedsOnlineCheck(path) {
					if err := d.Introspect.Check(ctx, token, claims); err != nil {
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
