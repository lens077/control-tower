package promql

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 目录里每条查询都必须带上 service_name 过滤 —— 否则在一个多服务共用
// VictoriaMetrics 的集群里,配置中心的页面会把别的服务的数据也画进来。
// 主机指标是例外:它本来就是整台机器的口径,按服务过滤反而查不到东西。
func TestCatalog_进程与应用类查询都带服务名过滤(t *testing.T) {
	c := NewCatalog("config-service")

	groups := map[string][]Query{
		"ProcessCPU":        c.ProcessCPU(),
		"ProcessMemory":     c.ProcessMemory(),
		"ProcessGoroutines": c.ProcessGoroutines(),
		"ProcessNetwork":    c.ProcessNetwork(),
		"APILatency":        c.APILatency(),
		"APIThroughput":     c.APIThroughput(),
		"APIErrorRate":      c.APIErrorRate(),
		"DBLatency":         c.DBLatency(),
		"DBPool":            c.DBPool(),
	}

	for name, queries := range groups {
		t.Run(name, func(t *testing.T) {
			require.NotEmpty(t, queries)
			for _, q := range queries {
				assert.Contains(t, q.Expr,
					`service_name="config-service",service_namespace="config-center"`,
					"少了配置中心资源过滤会把同集群其他服务的数据混进来")
			}
		})
	}
}

func TestCatalog_主机只消费共享规则并保持原单位(t *testing.T) {
	c := NewCatalog("config-service")
	for _, tc := range []struct {
		name    string
		queries []Query
		records []string
		signal  string
		percent bool
	}{
		{"CPU", c.HostCPU(), []string{"host:cpu_busy_ratio", "host:cpu_iowait_ratio"}, "cpu", true},
		{"Memory", c.HostMemory(), []string{"host:memory_used_ratio"}, "memory", true},
		{"Disk", c.HostDisk(), []string{"host:filesystem_used_ratio"}, "filesystem", true},
		{"Network", c.HostNetwork(), []string{"host:network_io_bytes_per_second", "host:network_io_bytes_per_second"}, "network", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Len(t, tc.queries, len(tc.records))
			for i, q := range tc.queries {
				assert.Contains(t, q.Expr, tc.records[i])
				assert.Equal(t, "host", q.LabelKey)
				assert.Contains(t, q.Expr, `host:signal_present{signal="`+tc.signal+`"} == 1`)
				assert.Contains(t, q.Expr, `time() - timestamp(host:signal_present{signal="`+tc.signal+`"}) < 180`)
				assert.Contains(t, q.Expr, `time() - host:last_seen_timestamp_seconds < 180`)
				assert.Contains(t, q.Expr, `time() - host:rules_evaluation_timestamp_seconds < 180`)
				assert.Contains(t, q.Expr, "host:last_seen_timestamp_seconds")
				assert.Contains(t, q.Expr, "host:rules_evaluation_timestamp_seconds")
				assert.Contains(t, q.Expr, "timestamp("+tc.records[i])
				assert.Contains(t, q.Expr, "host:expected_info")
				if tc.percent {
					assert.Equal(t, 1, strings.Count(q.Expr, "* 100"), "比率只换算一次百分比")
				} else {
					assert.NotContains(t, q.Expr, "* 100")
				}
				for _, raw := range []string{"system_cpu_", "system_memory_", "system_filesystem_", "system_network_", "node_cpu_", "node_memory_", "k8s_node_name", "service_name", "rate(", "or ", "vector(0)"} {
					assert.NotContains(t, q.Expr, raw, "不回退原始公式、不补零、不重复 rate")
				}
			}
		})
	}
	assert.Contains(t, c.HostDisk()[0].Expr, `mountpoint="/"`)
	assert.Contains(t, c.HostNetwork()[0].Expr, `direction="receive"`)
	if len(c.HostNetwork()) > 1 {
		assert.Contains(t, c.HostNetwork()[1].Expr, `direction="transmit"`)
	}
}

// 分子必须用「错误码标签存在且非空」来选。otelconnect 只在出错时才打这个标签,
// 写成 code!="ok" 会把成功的请求(压根没有这个标签)也算进分子,错误率恒等于 100%。
func TestCatalog_错误率分子只选带错误码的(t *testing.T) {
	expr := NewCatalog("x").APIErrorRate()[0].Expr
	assert.Contains(t, expr, `rpc_connect_rpc_error_code!=""`)
	assert.NotContains(t, expr, `rpc_connect_rpc_error_code!="ok"`)
}

// 一次错误都没有时,分子匹配不到任何序列(是空集合),除法结果也是空,
// 图上一片空白。而「完全健康」正是最常见的状态 —— 实测过:健康服务的
// 错误率图确实什么都不显示,看起来像坏了。补 0 之后空白只剩一个含义:
// 窗口内根本没有请求。
func TestCatalog_错误率在无错误时补零(t *testing.T) {
	expr := NewCatalog("x").APIErrorRate()[0].Expr
	assert.Contains(t, expr, "or on() vector(0)")
}

// DB 延迟是秒、API 延迟是毫秒。两张图并排放着必须统一量纲,
// 否则读者会把 0.05 秒看成 0.05 毫秒。
func TestCatalog_DB延迟换算成毫秒(t *testing.T) {
	for _, q := range NewCatalog("x").DBLatency() {
		assert.Contains(t, q.Expr, "* 1000", "otelpgx 的单位是秒,必须换算")
	}
}

func TestCatalog_延迟组给出三个分位数(t *testing.T) {
	for name, queries := range map[string][]Query{
		"API": NewCatalog("x").APILatency(),
		"DB":  NewCatalog("x").DBLatency(),
	} {
		t.Run(name, func(t *testing.T) {
			require.Len(t, queries, 3)
			labels := []string{queries[0].FixedLabel, queries[1].FixedLabel, queries[2].FixedLabel}
			assert.ElementsMatch(t, []string{"P50", "P95", "P99"}, labels)
		})
	}
}

// 服务名来自配置,理论上不该带引号;真带了要剥掉而不是拼出一句非法 PromQL。
// 拼坏了的表现是 VM 返回 400,而页面只显示「查询失败」,查半天查不到原因。
func TestCatalog_服务名里的引号被剥掉(t *testing.T) {
	expr := NewCatalog(`bad"name\`).ProcessCPU()[0].Expr
	assert.Equal(t, `service_name="badname",service_namespace="config-center"`, extractSelector(t, expr))
}

// 每条查询都要么指定 LabelKey、要么给 FixedLabel,不能两者都空 ——
// 那样图例会是空的,前端只能渲染出一个没有名字的色块。
func TestCatalog_每条查询都有图例来源(t *testing.T) {
	c := NewCatalog("x")
	all := [][]Query{
		c.ProcessCPU(), c.ProcessMemory(), c.ProcessGoroutines(), c.ProcessNetwork(),
		c.HostCPU(), c.HostMemory(), c.HostDisk(), c.HostNetwork(),
		c.APILatency(), c.APIThroughput(), c.APIErrorRate(),
		c.DBLatency(), c.DBPool(),
	}
	for _, group := range all {
		for _, q := range group {
			assert.True(t, q.LabelKey != "" || q.FixedLabel != "",
				"查询 %q 既没有 LabelKey 也没有 FixedLabel", q.Expr)
		}
	}
}

func extractSelector(t *testing.T, expr string) string {
	t.Helper()
	start := strings.Index(expr, "service_name=")
	require.GreaterOrEqual(t, start, 0)
	rest := expr[start:]
	end := strings.Index(rest, "}")
	require.Greater(t, end, 0)
	return rest[:end]
}
