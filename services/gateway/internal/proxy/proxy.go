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

	"connectrpc.com/connect"
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

// Proxy 是网关转发器。
type Proxy struct {
	rp *httputil.ReverseProxy
	ew *gwerrors.Writer
}

// New 构造 Proxy。inner 为出站 RoundTripper（生产传 NewH2CTransport()，测试可注入）。
func New(res resolver.Resolver, ew *gwerrors.Writer, log *zap.Logger, inner http.RoundTripper) *Proxy {
	if inner == nil {
		inner = NewH2CTransport()
	}
	p := &Proxy{ew: ew}
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// XFF：追加边缘链路（仅信任 RemoteAddr 语义由 SetXForwarded 保证）。
			pr.SetXForwarded()
			// 可信身份注入（入站 x-md-* 已在链路早段剥离）。
			if c := gwctx.Claims(pr.In.Context()); c != nil {
				identity.Inject(pr.Out.Header, c)
			}
			// scheme/host 由 pickingTransport 决定；这里先占位为入站 host。
			pr.Out.URL.Scheme = "http"
			if pr.Out.URL.Host == "" {
				pr.Out.URL.Host = pr.In.Host
			}
		},
		Transport: &pickingTransport{res: res, inner: inner},
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

	req.URL.Host = addr
	if u := gwctx.UpstreamOf(req.Context()); u != nil {
		u.Addr = addr
	}
	resp, err := t.inner.RoundTrip(req)
	if done != nil {
		// 仅按 transport 层结果反馈健康；后端业务态 5xx 不冷却节点。
		done(err)
	}
	return resp, err
}
