// Package promql 是查询 VictoriaMetrics 的最小客户端。
//
// 只实现 /api/v1/query_range 一个端点,因为控制台只画曲线。不引入
// prometheus/client_golang 的 api 包:那会把整个 Prometheus 客户端库
// 及其依赖拖进来,而这里需要的只是「发个带参数的 GET、解一层 JSON」。
//
// 安全边界:查询语句一律由调用方(catalog.go)在编译期给定,本包不做
// 字符串拼接之外的事,也不接受来自请求的 PromQL。原因见 system.proto 里
// QueryMetrics 的注释。
package promql

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	confv1 "github.com/lens077/control-tower/services/config/internal/conf/v1"
)

const defaultTimeout = 5 * time.Second

// 单次查询返回的数据点上限。超过就拒绝,而不是截断 ——
// 截断出来的曲线看着完整,实则少了一截,这种错没人看得出来。
const maxPointsPerQuery = 2000

// Client 查询 VictoriaMetrics。零值不可用,必须经 New 构造。
type Client struct {
	base    string
	http    *http.Client
	timeout time.Duration
}

// Sample 是一个数据点。
type Sample struct {
	TimestampMS int64
	Value       float64
}

// Series 是一条曲线:一组标签 + 一串点。
type Series struct {
	Labels map[string]string
	Points []Sample
}

// New 构造客户端。cfg 为 nil 或 endpoint 为空时返回 (nil, nil) ——
// 表示「没配置指标查询端」,这是合法状态而不是错误,调用方据此关掉历史曲线。
func New(cfg *confv1.Observability) (*Client, error) {
	if cfg == nil || !cfg.GetEnable() {
		return nil, nil
	}
	q := cfg.GetMetricQuery()
	if q == nil || strings.TrimSpace(q.GetEndpoint()) == "" {
		return nil, nil
	}

	base := strings.TrimSuffix(strings.TrimSpace(q.GetEndpoint()), "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("observability.metric_query.endpoint 必须是带 scheme 的完整地址,当前为 %q", base)
	}

	timeout := q.GetTimeout().AsDuration()
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if tlsCfg := q.GetTls(); tlsCfg.GetEnable() {
		conf := &tls.Config{
			MinVersion: tls.VersionTLS12,
			// nolint:gosec // 仅在显式配置时生效,用于自签 CA 的开发集群
			InsecureSkipVerify: tlsCfg.GetInsecureSkipVerify(),
		}
		if pem := tlsCfg.GetCaPem(); pem != "" {
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM([]byte(pem)) {
				return nil, errors.New("observability.metric_query.tls.ca_pem 不是合法的 PEM 证书")
			}
			conf.RootCAs = pool
		}
		transport.TLSClientConfig = conf
	}

	return &Client{
		base:    base,
		timeout: timeout,
		http:    &http.Client{Transport: transport, Timeout: timeout},
	}, nil
}

// QueryRange 执行一次区间查询。
//
// step 决定点的疏密。调用方负责保证 (end-start)/step 不至于离谱,
// 这里再兜一道底:超过 maxPointsPerQuery 直接报错,免得一次请求把
// 几十万个点塞进响应体。
func (c *Client) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]Series, error) {
	if step <= 0 {
		return nil, errors.New("step 必须为正")
	}
	if points := int(end.Sub(start) / step); points > maxPointsPerQuery {
		return nil, fmt.Errorf("查询会产生 %d 个点,超过上限 %d;请增大 step 或缩短窗口", points, maxPointsPerQuery)
	}

	params := url.Values{}
	params.Set("query", query)
	// 用秒而不是 RFC3339:VM 两种都收,但秒的形式在日志里更容易和
	// 手工 curl 的复现命令对上。
	params.Set("start", strconv.FormatInt(start.Unix(), 10))
	params.Set("end", strconv.FormatInt(end.Unix(), 10))
	params.Set("step", strconv.FormatInt(int64(step.Seconds()), 10))

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// 用 POST 而不是 GET:PromQL 语句可能很长(直方图分位数那几条尤其),
	// 而部分代理对 URL 长度有限制,超了会返回 414 而不是转发。
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/api/v1/query_range", strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build query request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query victoriametrics: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 限制读取量:VM 正常时不会返回这么多,但一个配错的 endpoint
	// 指到某个会流式输出的服务上,不设限会把内存吃光。
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("read query response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("victoriametrics 返回 %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	return parseRangeResponse(body)
}

// rangeResponse 对应 Prometheus 的 query_range 响应。
// 只解需要的字段 —— 多余的字段留在 JSON 里不解,少一处将来会不同步的定义。
type rangeResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			// [秒级时间戳(number), 值(string)] —— 值是字符串,
			// 因为 Prometheus 要表达 NaN/Inf,而 JSON 的 number 表达不了。
			Values [][2]json.RawMessage `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

func parseRangeResponse(body []byte) ([]Series, error) {
	var payload rangeResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode query response: %w", err)
	}
	if payload.Status != "success" {
		return nil, fmt.Errorf("victoriametrics 查询失败: %s", truncate(payload.Error, 200))
	}
	if payload.Data.ResultType != "matrix" {
		return nil, fmt.Errorf("期望 matrix 结果,得到 %q", payload.Data.ResultType)
	}

	series := make([]Series, 0, len(payload.Data.Result))
	for _, raw := range payload.Data.Result {
		points := make([]Sample, 0, len(raw.Values))
		for _, pair := range raw.Values {
			var ts float64
			if err := json.Unmarshal(pair[0], &ts); err != nil {
				continue
			}
			var literal string
			if err := json.Unmarshal(pair[1], &literal); err != nil {
				continue
			}
			value, err := strconv.ParseFloat(literal, 64)
			if err != nil {
				continue
			}
			// 必须显式挡 NaN / Inf。strconv.ParseFloat 会「成功」解析出这两者
			// (它认识 "NaN" "+Inf" 这些字面量),所以靠 err 判断挡不住。
			//
			// 直方图分位数在窗口内无样本时就是 NaN,除零则得 ±Inf。跳过它们让
			// 曲线在那一段断开 —— 这是真实情况;记成 0 会在图上画出一条并不存在
			// 的谷底,而 JSON 里的 NaN 更会直接让前端的图表库崩掉。
			if math.IsNaN(value) || math.IsInf(value, 0) {
				continue
			}
			points = append(points, Sample{TimestampMS: int64(ts * 1000), Value: value})
		}
		if len(points) == 0 {
			continue
		}
		series = append(series, Series{Labels: raw.Metric, Points: points})
	}
	return series, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
