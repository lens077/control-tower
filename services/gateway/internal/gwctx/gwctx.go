// Package gwctx 定义请求上下文的传递键：路由决策与已验签身份沿链路下行，
// 转发落点（实例地址）由 transport 回填供访问日志使用。
package gwctx

import (
	"context"

	"github.com/lens077/control-tower/services/gateway/internal/authn"
	"github.com/lens077/control-tower/services/gateway/internal/router"
)

type routeKey struct{}
type claimsKey struct{}
type guestKey struct{}
type upstreamKey struct{}
type assertionKey struct{}
type requestIDKey struct{}

// Upstream 是 transport 回填的转发落点信息（ctx 值向下传递、内容可变的持有器）。
type Upstream struct {
	// Addr 是最终转发到的实例地址（host:port）。
	Addr string
}

// WithRoute 挂载路由决策。
func WithRoute(ctx context.Context, r router.Route) context.Context {
	return context.WithValue(ctx, routeKey{}, r)
}

// Route 取出路由决策；ok=false 表示链路装配错误（proxy 之前必须已挂载）。
func Route(ctx context.Context) (router.Route, bool) {
	r, ok := ctx.Value(routeKey{}).(router.Route)
	return r, ok
}

// WithClaims 挂载已验签身份（匿名路径不挂载）。
func WithClaims(ctx context.Context, c *authn.Claims) context.Context {
	return context.WithValue(ctx, claimsKey{}, c)
}

// Claims 取出身份；匿名请求返回 nil。
func Claims(ctx context.Context) *authn.Claims {
	c, _ := ctx.Value(claimsKey{}).(*authn.Claims)
	return c
}

// WithGuestID 挂载访客身份（仅 B 级路径；与 Claims 互斥——已登录就不该再有访客身份）。
func WithGuestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, guestKey{}, id)
}

// GuestID 取出访客 id；非访客请求返回空串。
func GuestID(ctx context.Context) string {
	id, _ := ctx.Value(guestKey{}).(string)
	return id
}

// WithUpstream 挂载空的落点持有器（accesslog 中间件负责挂载并读取）。
func WithUpstream(ctx context.Context) (context.Context, *Upstream) {
	u := &Upstream{}
	return context.WithValue(ctx, upstreamKey{}, u), u
}

// UpstreamOf 取出落点持有器；无则返回 nil。
func UpstreamOf(ctx context.Context) *Upstream {
	u, _ := ctx.Value(upstreamKey{}).(*Upstream)
	return u
}

// WithAssertionToken 挂载已签发的内部断言（T1 可信身份边界，proxy 在 Rewrite 落头）。
func WithAssertionToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, assertionKey{}, token)
}

// AssertionToken 取出断言；未启用断言的请求返回空串。
func AssertionToken(ctx context.Context) string {
	tok, _ := ctx.Value(assertionKey{}).(string)
	return tok
}

// WithRequestID 挂载链路 request id（X-Md-Request-Id）。与断言 jti 分离（契约 I3）：
// 同一次业务请求（含响应链路）保持不变，jti 每次上游调用重新生成。
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID 取出链路 request id；未挂载返回空串。
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}
