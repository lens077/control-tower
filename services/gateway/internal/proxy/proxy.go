// Package proxy 实现转发核心：httputil.ReverseProxy + h2c 出站 + 选点反馈。
//
// 设计要点（docs/design/architecture.md 不变式）：
//   - 端到端 Connect 直通：无转码、无请求体缓存、默认无重试；
//   - 可信身份头注入在 Rewrite 内执行（晚于标准库 hop-by-hop 删除，防
//     Connection: x-md-global-* 剥头），同处调用 SetXForwarded；
//   - 出站 h2c：http2.Transport 仅设 AllowHTTP 不会走明文——必须覆写
//     DialTLSContext 返回明文连接（终裁 §三-1；旧网关 client/node.go 同法）；
//     ReadIdleTimeout/PingTimeout 剔除后端滚动产生的死连接；
//   - 路由级总超时在这里施加（context），流式路由（当前为零）按 decisions.md 豁免；
//   - Authorization 头保持透传（后端可能有纵深校验，P3 逐服务确认前不动）。
//
// T1 可信身份边界（palcommerce/docs/shop/T1-implementation-plan.md WP-2）：
//   - 断言签发在 ServeHTTP（转发前）完成、失败 fail-closed；Rewrite 只负责落头；
//   - X-Md-Request-Id 与断言 jti 分离（契约 I3）：前者整请求不变，后者每次签发新生成。
package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/lens077/control-tower/services/gateway/internal/assertion"
	"github.com/lens077/control-tower/services/gateway/internal/gwctx"
	"github.com/lens077/control-tower/services/gateway/internal/gwerrors"
	"github.com/lens077/control-tower/services/gateway/internal/identity"
	"github.com/lens077/control-tower/services/gateway/internal/resolver"

	"go.uber.org/zap"
	"golang.org/x/net/http2"
)

// 目标 scheme 前缀。
const (
	discoveryPrefix = "discovery:///"
	directPrefix    = "direct://"
)

// NewH2CTransport 构造出站明文 HTTP/2 transport。
func NewH2CTransport() *http2.Transport {
	return &http2.Transport{
		AllowHTTP: true,
		// 关键：返回明文连接，h2c 才真正生效（仅 AllowHTTP 仍会 TLS 拨号）。
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, network, addr)
		},
		// 后端滚动后旧连接可能半死：定期 ping，超时即弃连接。
		ReadIdleTimeout: 20 * time.Second,
		PingTimeout:     5 * time.Second,
	}
}

// NewTLSRoundTripper 构造 mTLS 出站 transport（T1：网关 → shop 的 direct 拨号）。
// 与 NewH2CTransport 的明文 h2c 不同，这里走真 TLS 拨号，HTTP/2 由 ALPN 协商
// （ForceAttemptHTTP2）；tls.Config 由调用方按 SHOP_UPSTREAM_TLS_* env 组装。
// 只会挂给声明了 TLS 的 upstream（见 Options.UpstreamTLS），其他 upstream 不受影响。
func NewTLSRoundTripper(cfg *tls.Config) http.RoundTripper {
	return &http.Transport{
		TLSClientConfig:   cfg,
		ForceAttemptHTTP2: true,
		DialContext:       (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
	}
}

// Options 是 Proxy 的可选装配项。零值 = 行为与 T1 之前完全一致。
type Options struct {
	// Assertion 非 nil 时，对 Enabled 包的请求在转发前签发内部断言（T1）。
	Assertion *assertion.Issuer
	// UpstreamTLS 把「包名 → TLS 出站 transport」挂给 pickingTransport：
	// 命中包的请求改走该 transport（mTLS 拨号），其余 upstream 走默认 inner。
	UpstreamTLS map[string]http.RoundTripper
}

// Option 是函数式装配项。
type Option func(*Options)

// WithAssertion 启用断言签发。
func WithAssertion(a *assertion.Issuer) Option {
	return func(o *Options) { o.Assertion = a }
}

// WithUpstreamTLS 启用指定包的 TLS 拨号。
func WithUpstreamTLS(m map[string]http.RoundTripper) Option {
	return func(o *Options) { o.UpstreamTLS = m }
}

// tlsPackages 抽出启用 TLS 的包名集合（供 Rewrite 判 scheme）。
func (o Options) tlsPackages() map[string]struct{} {
	if len(o.UpstreamTLS) == 0 {
		return nil
	}
	pkgs := make(map[string]struct{}, len(o.UpstreamTLS))
	for pkg := range o.UpstreamTLS {
		pkgs[pkg] = struct{}{}
	}
	return pkgs
}

// Proxy 是网关转发器。
type Proxy struct {
	rp *httputil.ReverseProxy
	ew *gwerrors.Writer
	// assertion 为 nil = 断言整体关闭（默认，行为与 T1 之前一致）。
	assertion *assertion.Issuer
	// tlsPkgs 是启用 TLS 拨号的包名集合（nil = 无）。
	tlsPkgs map[string]struct{}
}

// New 构造 Proxy。inner 为出站 RoundTripper（生产传 NewH2CTransport()，测试可注入）。
func New(res resolver.Resolver, ew *gwerrors.Writer, log *zap.Logger, inner http.RoundTripper, opts ...Option) *Proxy {
	if inner == nil {
		inner = NewH2CTransport()
	}
	var o Options
	for _, f := range opts {
		f(&o)
	}
	p := &Proxy{ew: ew, assertion: o.Assertion, tlsPkgs: o.tlsPackages()}
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// XFF：追加边缘链路（仅信任 RemoteAddr 语义由 SetXForwarded 保证）。
			pr.SetXForwarded()
			// Authorization 不透传：实测后端 10 服务零消费（P3 确认），
			// 凭据不进内网；将来有服务要消费原始 JWT 时再按 decisions.md 触发条款恢复。
			pr.Out.Header.Del("Authorization")
			// 可信身份注入（入站 x-md-* 已在链路早段剥离）。
			// 登录态优先：已验签 claims 存在时不看访客身份，两者不会同时注入。
			if c := gwctx.Claims(pr.In.Context()); c != nil {
				identity.Inject(pr.Out.Header, c)
			} else if gid := gwctx.GuestID(pr.In.Context()); gid != "" {
				identity.InjectGuest(pr.Out.Header, gid)
			}
			// T1：内部断言 + 链路 request id（紧随 x-md-* 注入，契约 §4.1）。
			// 断言已在 ServeHTTP 签好（签发失败在那里 fail-closed），这里只落头。
			// 入站 x-md-* 头已被 identity.Strip 无条件剥离，客户端伪造不了 X-Md-Request-Id。
			if tok := gwctx.AssertionToken(pr.In.Context()); tok != "" {
				pr.Out.Header.Set(assertion.HeaderName, tok)
				if rid := gwctx.RequestID(pr.In.Context()); rid != "" {
					pr.Out.Header.Set(assertion.HeaderRequestID, rid)
				}
			}
			// scheme/host 由 pickingTransport 决定；这里先占位为入站 host。
			pr.Out.URL.Scheme = "http"
			// T1：TLS upstream 的 scheme 必须是 https，transport 才会走 TLS 拨号。
			if route, ok := gwctx.Route(pr.In.Context()); ok {
				if _, isTLS := p.tlsPkgs[route.Package]; isTLS {
					pr.Out.URL.Scheme = "https"
				}
			}
			if pr.Out.URL.Host == "" {
				pr.Out.URL.Host = pr.In.Host
			}
		},
		Transport: &pickingTransport{res: res, inner: inner, tls: o.UpstreamTLS},
		// 网关是唯一 CORS 权威：剥除上游自带的 CORS 头。
		// 实测坑：后端服务为「dev 直连」场景各自带 CORS，经网关转发后
		// Access-Control-Allow-Origin 出现两份，浏览器整单拒收（预检不转发所以只有实际请求炸）。
		ModifyResponse: func(resp *http.Response) error {
			for _, h := range []string{
				"Access-Control-Allow-Origin",
				"Access-Control-Allow-Credentials",
				"Access-Control-Allow-Methods",
				"Access-Control-Allow-Headers",
				"Access-Control-Expose-Headers",
				"Access-Control-Max-Age",
			} {
				resp.Header.Del(h)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			p.writeError(w, r, err, log)
		},
	}
	return p
}

// ServeHTTP 施加路由级总超时后转发。
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route, ok := gwctx.Route(r.Context())
	if !ok {
		p.ew.Write(w, r, connect.CodeInternal, "ROUTE_MISSING_IN_CONTEXT", "gateway pipeline bug: route not resolved")
		return
	}
	if route.Timeout > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), route.Timeout)
		defer cancel()
		r = r.WithContext(ctx)
	}
	// T1：断言签发 + request id，仅对启用包生效。
	//
	// 签发点说明（契约 I3）：本网关无 per-attempt 重试（decisions.md 砍掉两层重试，
	// ReverseProxy 也没有按 attempt 的钩子），每次上游调用恰好触发一次签发，
	// jti 每请求一个新 UUID；X-Md-Request-Id 在同一请求内保持不变。
	// 若将来引入 per-attempt 重试，签发必须下沉到每次 RoundTrip 之前换新 jti，
	// 否则合法重试会被 shop 判成重放。
	if p.assertion != nil && p.assertion.Enabled(route.Package) {
		token, err := p.assertion.Issue(assertion.IdentityFromClaims(gwctx.Claims(r.Context())))
		if err != nil {
			// fail-closed：断言启用即 shop 要求断言，签不出来绝不裸转发。
			p.ew.Write(w, r, connect.CodeInternal, "ASSERTION_ISSUE_FAILED", "failed to issue internal assertion")
			return
		}
		ctx := gwctx.WithRequestID(r.Context(), uuid.NewString())
		r = r.WithContext(gwctx.WithAssertionToken(ctx, token))
	}
	p.rp.ServeHTTP(w, r)
}

func (p *Proxy) writeError(w http.ResponseWriter, r *http.Request, err error, log *zap.Logger) {
	// 客户端已断开：无处可写。
	if errors.Is(r.Context().Err(), context.Canceled) {
		return
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		p.ew.Write(w, r, connect.CodeDeadlineExceeded, "UPSTREAM_TIMEOUT", "upstream did not answer within route timeout")
	case errors.Is(err, resolver.ErrNoInstance):
		p.ew.Write(w, r, connect.CodeUnavailable, "NO_HEALTHY_UPSTREAM", "no healthy upstream instance")
	case errors.Is(err, resolver.ErrUnknownSvc):
		p.ew.Write(w, r, connect.CodeUnavailable, "UPSTREAM_NOT_WATCHED", "upstream service not watched")
	case errors.Is(err, errBadTarget):
		p.ew.Write(w, r, connect.CodeInternal, "BAD_ROUTE_TARGET", "route target malformed")
	default:
		log.Warn("proxy transport error", zap.Error(err), zap.String("path", r.URL.Path))
		p.ew.Write(w, r, connect.CodeUnavailable, "UPSTREAM_ERROR", "upstream transport error")
	}
}

var errBadTarget = errors.New("proxy: malformed route target")

// pickingTransport 在 RoundTrip 边界完成选点、落点回填与 exactly-once 反馈。
type pickingTransport struct {
	res   resolver.Resolver
	inner http.RoundTripper
	// tls 按包名覆盖 inner（T1：shop upstream 走 mTLS）；nil/缺包走 inner。
	tls map[string]http.RoundTripper
}

func (t *pickingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	route, ok := gwctx.Route(req.Context())
	if !ok {
		return nil, errBadTarget
	}
	var (
		addr string
		done resolver.Done
	)
	switch {
	case strings.HasPrefix(route.Target, discoveryPrefix):
		service := route.Target[len(discoveryPrefix):]
		inst, d, err := t.res.Pick(service)
		if err != nil {
			return nil, err
		}
		addr, done = inst.Addr, d
	case strings.HasPrefix(route.Target, directPrefix):
		addr = route.Target[len(directPrefix):]
	default:
		return nil, errBadTarget
	}

	inner := t.inner
	if rt, ok := t.tls[route.Package]; ok && rt != nil {
		inner = rt
	}

	req.URL.Host = addr
	if u := gwctx.UpstreamOf(req.Context()); u != nil {
		u.Addr = addr
	}
	resp, err := inner.RoundTrip(req)
	if done != nil {
		// 仅按 transport 层结果反馈健康；后端业务态 5xx 不冷却节点。
		done(err)
	}
	return resp, err
}
