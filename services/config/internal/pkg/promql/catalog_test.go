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

func TestCatalog_主机类查询按节点拆分(t *testing.T) {
	c := NewCatalog("config-service")

	for name, queries := range map[string][]Query{
		"HostCPU":    c.HostCPU(),
		"HostMemory": c.HostMemory(),
		"HostDisk":   c.HostDisk(),
	} {
		t.Run(name, func(t *testing.T) {
			require.Len(t, queries, 1)
			assert.Contains(t, queries[0].Expr, "k8s_node_name",
				"不按节点拆的话多节点数据会叠在一起,看不出是哪台在抖")
			assert.Equal(t, "k8s_node_name", queries[0].LabelKey)
		})
	}
}

// 这是最容易写错的一条:CPU 使用率必须先按核聚合、再对核取平均。
// 直接 sum 会把 4 核机器的满载算成 400%(实测过:同一时刻 sum 给 116%、avg 给 28%)。
func TestCatalog_主机CPU先按核聚合再平均(t *testing.T) {
	expr := NewCatalog("x").HostCPU()[0].Expr
	assert.Contains(t, expr, "avg by (k8s_node_name)")
	assert.Contains(t, expr, "sum by (k8s_node_name, cpu)")
	assert.Contains(t, expr, `state!="idle"`)
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
