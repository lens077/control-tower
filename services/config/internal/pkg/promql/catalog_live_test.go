package promql

import (
	"context"
	"os"
	"testing"
	"time"

	confv1 "github.com/lens077/control-tower/services/config/internal/conf/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"
)

// 联机冒烟测试:把目录里每条查询都对真实的 VictoriaMetrics 打一遍,
// 确认它们确实取得到数据。
//
// 为什么需要这个:catalog_test.go 只能验证查询语句「长得对」,验证不了
// 「指标真的存在」。而这条链路上最容易出的事恰恰是后者 —— 有人调整了
// otel collector 的 scraper、改了服务名、或者升级 SDK 后指标改名,
// 结果是页面上某张图默默变空,没有任何报错,也没有测试会红。
//
// 默认跳过(需要能连到集群),显式打开:
//
//	CONFIG_CENTER_VM_ENDPOINT=http://vm.app.com go test ./internal/pkg/promql -run Live -v
//
// 窗口取 24h 而不是 1h:开发环境经常几小时没有流量,窗口太短会把
// 「最近很闲」误判成「查询坏了」。
func TestLive_目录里每条查询都取得到数据(t *testing.T) {
	endpoint := os.Getenv("CONFIG_CENTER_VM_ENDPOINT")
	if endpoint == "" || testing.Short() {
		t.Skip("设置 CONFIG_CENTER_VM_ENDPOINT 后运行")
	}

	serviceName := os.Getenv("CONFIG_CENTER_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "config-service"
	}

	client, err := New(&confv1.Observability{
		Enable: true,
		MetricQuery: &confv1.Observability_MetricQuery{
			Endpoint: endpoint,
			Timeout:  durationpb.New(15 * time.Second),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, client)

	c := NewCatalog(serviceName)
	groups := map[string][]Query{
		"PROCESS_CPU":        c.ProcessCPU(),
		"PROCESS_MEMORY":     c.ProcessMemory(),
		"PROCESS_GOROUTINES": c.ProcessGoroutines(),
		"PROCESS_NETWORK":    c.ProcessNetwork(),
		"HOST_CPU":           c.HostCPU(),
		"HOST_MEMORY":        c.HostMemory(),
		"HOST_DISK":          c.HostDisk(),
		"HOST_NETWORK":       c.HostNetwork(),
		"API_LATENCY":        c.APILatency(),
		"API_THROUGHPUT":     c.APIThroughput(),
		"API_ERROR_RATE":     c.APIErrorRate(),
		"DB_LATENCY":         c.DBLatency(),
		"DB_POOL":            c.DBPool(),
	}

	end := time.Now()
	start := end.Add(-24 * time.Hour)

	for name, queries := range groups {
		t.Run(name, func(t *testing.T) {
			lines, points := 0, 0
			for _, q := range queries {
				series, err := client.QueryRange(context.Background(), q.Expr, start, end, 5*time.Minute)
				require.NoError(t, err, "查询语句本身必须合法:%s", q.Expr)
				for _, s := range series {
					lines++
					points += len(s.Points)
				}
			}
			// 一条线都没有,基本只有两个原因:指标名不存在,或者过滤条件写错了。
			// 两者的表现都是页面上一张空图,所以在这里必须失败。
			require.Positive(t, lines, "取不到任何序列 —— 指标可能已改名或采集端已关闭")
			t.Logf("%s: %d 条线 / %d 个点", name, lines, points)
		})
	}
}
