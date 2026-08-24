// Package app 组装网关的完整请求处理器（main 与集成测试共用，防两处漂移）。
package app

import (
	"net/http"

	"github.com/lens077/control-tower/services/gateway/internal/authn"
	"github.com/lens077/control-tower/services/gateway/internal/bff"
	"github.com/lens077/control-tower/services/gateway/internal/gwerrors"
	"github.com/lens077/control-tower/services/gateway/internal/httpmw"
	"github.com/lens077/control-tower/services/gateway/internal/loader"
	"github.com/lens077/control-tower/services/gateway/internal/proxy"
	"github.com/lens077/control-tower/services/gateway/internal/resolver"
	"github.com/lens077/control-tower/services/gateway/internal/session"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"
)

// Deps 是装配输入。
type Deps struct {
	State      *loader.State
	Cors       *httpmw.CorsSwapper
	Introspect httpmw.Introspector
	// Roles 角色回退源（claims 无 roles 时启用；nil=关闭）。
	Roles    authn.RoleSource
	Resolver resolver.Resolver
	Errors   *gwerrors.Writer
	Log      *zap.Logger
	// Transport 出站 RoundTripper；nil=生产 h2c（经 otelhttp 包装）。
	Transport http.RoundTripper

	// ── BFF 会话轨（ADR-0002）。三者同时为零值时整轨关闭，
	// 行为与切换前完全一致（bff-migration.md 的 P1「零客户端影响」靠这个成立）。
	Sessions      session.Store
	BFF           *bff.Handler
	SessionCookie string
	SessionHeader string
	Refresher     httpmw.SessionRefresher
}

// BuildHandler 构造业务端口的完整处理器：
// healthz/readyz 先于包路由注册；其余路径走
// recover → otel → accesslog → cors → auth → proxy。
func BuildHandler(d Deps) http.Handler {
	transport := d.Transport
	if transport == nil {
		transport = otelhttp.NewTransport(proxy.NewH2CTransport())
	}
	p := proxy.New(d.Resolver, d.Errors, d.Log, transport)

	chain := httpmw.Chain(p,
		httpmw.Recover(d.Log, d.Errors),
		func(next http.Handler) http.Handler {
			return otelhttp.NewHandler(next, "gateway", otelhttp.WithSpanNameFormatter(
				func(_ string, r *http.Request) string { return r.URL.Path },
			))
		},
		httpmw.AccessLog(d.Log),
		d.Cors.Middleware(),
		httpmw.Auth(httpmw.AuthDeps{
			Table:         d.State.Table,
			Verifier:      d.State.Verifier(),
			Enforcer:      d.State.Enforcer(),
			Introspect:    d.Introspect,
			Roles:         d.Roles,
			Errors:        d.Errors,
			Sessions:      d.Sessions,
			SessionCookie: d.SessionCookie,
			SessionHeader: d.SessionHeader,
			Refresher:     d.Refresher,
			OriginAllowed: d.Cors.OriginAllowed,
		}),
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ready := d.State.Ready() && d.Resolver.Ready()
		// 会话存储是鉴权单点（ADR-0002 的取舍）：不可达即摘流量。
		if ready && d.Sessions != nil {
			if err := d.Sessions.Ping(r.Context()); err != nil {
				ready = false
			}
		}
		if ready {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("loading"))
	})
	// BFF 端点与 healthz/readyz 同列本地路由，先于包路由注册，永不被代理。
	//
	// 必须套 Recover/AccessLog/Cors，但**不能套 Auth**（登录入口本就该匿名可达）：
	//   - AccessLog：这些是安全关键端点，不进日志等于排障时两眼一抹黑
	//     （实测踩过：桌面端登录失败，网关侧查不到任何记录）；
	//   - Cors：生产上前端在 shop.apikv.com、网关在 gateway.apikv.com，
	//     跨源调 /auth/me 若无 CORS 头会被浏览器直接挡掉。
	if d.BFF != nil {
		mux.Handle("/auth/", httpmw.Chain(d.BFF.Handler(),
			httpmw.Recover(d.Log, d.Errors),
			httpmw.AccessLog(d.Log),
			d.Cors.Middleware(),
		))
	}
	mux.Handle("/", chain)
	return mux
}
