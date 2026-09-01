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
