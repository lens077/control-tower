// Package health 从网关当前路由探测服务健康，仅供受保护的管理员 HTTP 端点使用。
// 返回单次路由采样，不代表全副本健康；不向客户端返回内部地址、凭据或依赖错误原文。
package health

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lens077/control-tower/services/gateway/internal/resolver"
	"github.com/lens077/control-tower/services/gateway/internal/router"
)

// Path 是固定本地路由，不进入 RPC 匿名清单和包路由配置。
const Path = "/admin/health/services"

const (
	perProbeTimeout = 2 * time.Second
	totalTimeout    = 5 * time.Second
	maxServices     = 64
	parallelProbes  = 4
	maxBodyBytes    = 64 << 10
)

type serviceStatus struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	LatencyMS *int64    `json:"latency_ms"`
	CheckedAt time.Time `json:"checked_at"`
	Reason    string    `json:"reason,omitempty"`
}

type snapshot struct {
	CheckedAt time.Time       `json:"checked_at"`
	Services  []serviceStatus `json:"services"`
}

// Checker 对已配置路由并行探测 /healthz；目标只能来自路由表或可信 Resolver。
type Checker struct {
	table       func() *router.Table
	resolver    resolver.Resolver
	client      *http.Client
	mu          sync.Mutex
	busy        chan struct{}
	cached      snapshot
	cachedTable *router.Table
	expires     time.Time
}

// New 使用与网关反向代理相同的 transport（生产为 h2c），且禁止重定向。
func New(table func() *router.Table, res resolver.Resolver, transport http.RoundTripper) *Checker {
	return &Checker{
		table: table, resolver: res,
		client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

func (c *Checker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), totalTimeout)
	defer cancel()
	table := c.table()
	result := snapshot{CheckedAt: time.Now().UTC(), Services: []serviceStatus{}}
	status := http.StatusOK
	if table == nil {
		status = http.StatusServiceUnavailable
	} else {
		routes := businessRoutes(table)
		if len(routes) == 0 || len(routes) > maxServices {
			status = http.StatusServiceUnavailable
		} else {
			result = c.load(ctx, table, routes)
			if ctx.Err() != nil {
				status = http.StatusServiceUnavailable
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}

// 同一时刻只跑一轮探测；其他 HTTP 请求可独立取消等待。完整快照按路由表缓存 5 秒。
func (c *Checker) load(ctx context.Context, table *router.Table, routes []router.Route) snapshot {
	for {
		c.mu.Lock()
		if c.cachedTable == table && time.Now().Before(c.expires) {
			result := c.cached
			c.mu.Unlock()
			return result
		}
		if c.busy != nil {
			busy := c.busy
			c.mu.Unlock()
			select {
			case <-busy:
				continue
			case <-ctx.Done():
				return snapshot{CheckedAt: time.Now().UTC(), Services: []serviceStatus{}}
			}
		}
		c.busy = make(chan struct{})
		c.mu.Unlock()
		result := c.collect(ctx, routes)
		c.mu.Lock()
		if ctx.Err() == nil {
			c.cached, c.cachedTable = result, table
			c.expires = time.Now().Add(5 * time.Second)
		}
		close(c.busy)
		c.busy = nil
		c.mu.Unlock()
		return result
	}
}

func businessRoutes(table *router.Table) []router.Route {
	routes := table.Routes()
	var behaviorTarget string
	for _, route := range routes {
		if route.Package == "behavior" {
			behaviorTarget = route.Target
		}
	}
	out := make([]router.Route, 0, len(routes))
	for _, route := range routes {
		if route.Package == "telemetry" && route.Target == behaviorTarget {
			continue
		}
		out = append(out, route)
	}
	return out
}

func (c *Checker) collect(ctx context.Context, routes []router.Route) snapshot {
	items := make([]serviceStatus, len(routes))
	jobs := make(chan int, len(routes))
	for i, route := range routes {
		items[i] = serviceStatus{Name: route.Package, Status: "unknown", Reason: "not_configured", CheckedAt: time.Now().UTC()}
		jobs <- i
	}
	close(jobs)
	var wg sync.WaitGroup
	for range min(parallelProbes, len(routes)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				items[i] = c.check(ctx, routes[i])
			}
		}()
	}
	wg.Wait()
	return snapshot{CheckedAt: time.Now().UTC(), Services: items}
}

func (c *Checker) check(parent context.Context, route router.Route) serviceStatus {
	item := serviceStatus{Name: route.Package, Status: "unknown", Reason: "not_configured"}
	addr, done, reason := c.target(route)
	// 与代理相同：只按连接层结果反馈，依赖不健康不冷却节点。
	var transportErr error
	if done != nil {
		defer func() { done(transportErr) }()
	}
	if reason != "" {
		transportErr = errors.New(reason)
		if reason == "no_instance" {
			item.Status = "unavailable"
		}
		item.Reason, item.CheckedAt = reason, time.Now().UTC()
		return item
	}
	ctx, cancel := context.WithTimeout(parent, perProbeTimeout)
	defer cancel()
	start := time.Now()
	item.Status, item.Reason, transportErr = c.probe(ctx, addr)
	if parent.Err() != nil {
		transportErr = nil
	}
	latency := time.Since(start).Milliseconds()
	item.LatencyMS, item.CheckedAt = &latency, time.Now().UTC()
	return item
}

func (c *Checker) target(route router.Route) (string, resolver.Done, string) {
	var addr string
	var done resolver.Done
	switch {
	case strings.HasPrefix(route.Target, "discovery:///"):
		if c.resolver == nil {
			return "", nil, "no_instance"
		}
		inst, feedback, err := c.resolver.Pick(strings.TrimPrefix(route.Target, "discovery:///"))
		done = feedback
		if err != nil {
			return "", done, "no_instance"
		}
		addr = inst.Addr
	case strings.HasPrefix(route.Target, "direct://"):
		addr = strings.TrimPrefix(route.Target, "direct://")
	default:
		return "", nil, "not_configured"
	}
	host, port, err := net.SplitHostPort(addr)
	n, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || host == "" || n < 1 || n > 65535 || strings.ContainsAny(addr, "/?#@%\\ \t\r\n") {
		return "", done, "not_configured"
	}
	return addr, done, ""
}

func (c *Checker) probe(ctx context.Context, addr string) (string, string, error) {
	u := url.URL{Scheme: "http", Host: addr, Path: "/healthz"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "unknown", "not_configured", nil
	}
	resp, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "unavailable", "timeout", err
		}
		return "unavailable", "probe_failed", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
		return "unavailable", "probe_failed", nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if ctx.Err() != nil {
		return "unavailable", "timeout", nil
	}
	if err != nil || len(body) > maxBodyBytes {
		return "unknown", "invalid_response", nil
	}
	var health struct {
		Healthy *bool `json:"healthy"`
	}
	if err := json.Unmarshal(body, &health); err != nil || health.Healthy == nil {
		return "unknown", "invalid_response", nil
	}
	if !*health.Healthy {
		return "degraded", "dependency_unhealthy", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "unknown", "invalid_response", nil
	}
	return "healthy", "", nil
}
