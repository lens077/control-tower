package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	confv1 "github.com/lens077/control-tower/services/config/internal/conf/v1"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/durationpb"

	v1 "github.com/lens077/control-tower/api/system/v1"
	"github.com/lens077/control-tower/services/config/internal/pkg/promql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var expectedHostNames = []string{"k1", "k2", "k3", "node0", "node1", "node2", "node3", "node4"}

// 真实 promql.Client + 本地 HTTP fake,证明 metadata 从未进入利用率数值。
func testHostService(t *testing.T, reply func(query string, end int64) ([]map[string]any, int)) *SystemService {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		end, err := strconv.ParseInt(r.Form.Get("end"), 10, 64)
		require.NoError(t, err)
		result, status := reply(r.Form.Get("query"), end)
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"status": "success", "data": map[string]any{"resultType": "matrix", "result": result},
		}))
	}))
	t.Cleanup(server.Close)
	client, err := promql.New(&confv1.Observability{MetricQuery: &confv1.Observability_MetricQuery{Endpoint: server.URL}})
	require.NoError(t, err)
	return &SystemService{metrics: client, catalog: promql.NewCatalog("config-service"), log: zap.NewNop()}
}

func hostMatrix(host string, ts int64, value float64) map[string]any {
	kind := "cloud"
	if strings.HasPrefix(host, "k") {
		kind = "kubernetes"
	}
	return map[string]any{
		"metric": map[string]string{"host": host, "host_kind": kind},
		"values": [][]any{{ts, fmt.Sprint(value)}},
	}
}

func inventoryMatrix(ts int64) []map[string]any {
	var result []map[string]any
	for _, host := range expectedHostNames {
		result = append(result, hostMatrix(host, ts, 1))
	}
	// 变更 disk_alert_owner 等清单标签可能留下两条同 host 历史,不能重复图例。
	return append(result, hostMatrix("node4", ts-30, 1))
}

func TestQueryMetrics_预期主机整窗缺失保留空点而非零(t *testing.T) {
	inventoryCalls := 0
	svc := testHostService(t, func(query string, end int64) ([]map[string]any, int) {
		if strings.HasPrefix(query, "host:expected_info") {
			inventoryCalls++
			return inventoryMatrix(end), 0
		}
		return []map[string]any{hostMatrix("k1", end, 0), hostMatrix("k2", end, 25)}, 0
	})
	response, err := svc.QueryMetrics(context.Background(), connect.NewRequest(&v1.QueryMetricsRequest{
		Series: []v1.MetricSeries{v1.MetricSeries_METRIC_SERIES_HOST_MEMORY, v1.MetricSeries_METRIC_SERIES_HOST_DISK},
		Window: durationpb.New(time.Hour), StepSeconds: 30,
	}))
	require.NoError(t, err)
	require.Len(t, response.Msg.Results, 2)
	assert.Equal(t, 1, inventoryCalls, "一个 API 请求只取一次主机清单")
	for _, result := range response.Msg.Results {
		require.Len(t, result.Lines, 8)
		for i, line := range result.Lines {
			assert.Equal(t, expectedHostNames[i], line.Label)
			assert.Equal(t, v1.MetricUnit_METRIC_UNIT_PERCENT, line.Unit)
			if i < 2 {
				require.Len(t, line.Points, 1)
			} else {
				assert.Empty(t, line.Points, "清单的常数1不是利用率;也不能为缺失补0")
			}
		}
		assert.Zero(t, result.Lines[0].Points[0].Value, "真实的零不能被当成缺失")
	}
}

func TestQueryMetrics_全缺时仍列出八台预期主机(t *testing.T) {
	svc := testHostService(t, func(query string, end int64) ([]map[string]any, int) {
		if strings.HasPrefix(query, "host:expected_info") {
			return inventoryMatrix(end), 0
		}
		return nil, 0 // 规则缺失、signal_present=0 或 freshness 失败都只能返回空
	})
	response, err := svc.QueryMetrics(context.Background(), connect.NewRequest(&v1.QueryMetricsRequest{
		Series: []v1.MetricSeries{v1.MetricSeries_METRIC_SERIES_HOST_NETWORK},
		Window: durationpb.New(time.Hour), StepSeconds: 30,
	}))
	require.NoError(t, err)
	require.Len(t, response.Msg.Results[0].Lines, 16)
	for _, line := range response.Msg.Results[0].Lines {
		assert.Empty(t, line.Points)
		assert.Equal(t, v1.MetricUnit_METRIC_UNIT_BYTES_PER_SECOND, line.Unit)
	}
}

func TestQueryMetrics_部分查询与清单失败不被已有曲线掩盖(t *testing.T) {
	for _, failure := range []string{"inventory", "iowait"} {
		t.Run(failure, func(t *testing.T) {
			svc := testHostService(t, func(query string, end int64) ([]map[string]any, int) {
				if strings.HasPrefix(query, "host:expected_info") {
					if failure == "inventory" {
						return nil, http.StatusServiceUnavailable
					}
					return inventoryMatrix(end), 0
				}
				if failure == "iowait" && strings.Contains(query, "host:cpu_iowait_ratio") {
					return nil, http.StatusServiceUnavailable
				}
				return []map[string]any{hostMatrix("k1", end, 20)}, 0
			})
			response, err := svc.QueryMetrics(context.Background(), connect.NewRequest(&v1.QueryMetricsRequest{
				Series: []v1.MetricSeries{v1.MetricSeries_METRIC_SERIES_HOST_CPU},
				Window: durationpb.New(time.Hour), StepSeconds: 30,
			}))
			require.NoError(t, err)
			result := response.Msg.Results[0]
			assert.NotEmpty(t, result.Error)
			assert.NotEmpty(t, result.Lines)
		})
	}
}

func TestQueryMetrics_保留合法历史但提示尾部记录已过旧(t *testing.T) {
	svc := testHostService(t, func(query string, end int64) ([]map[string]any, int) {
		if strings.HasPrefix(query, "host:expected_info") {
			return inventoryMatrix(end), 0
		}
		return []map[string]any{hostMatrix("k1", end-600, 15)}, 0
	})
	response, err := svc.QueryMetrics(context.Background(), connect.NewRequest(&v1.QueryMetricsRequest{
		Series: []v1.MetricSeries{v1.MetricSeries_METRIC_SERIES_HOST_MEMORY},
		Window: durationpb.New(time.Hour), StepSeconds: 30,
	}))
	require.NoError(t, err)
	result := response.Msg.Results[0]
	assert.Contains(t, result.Error, "180s")
	assert.Contains(t, result.Error, "k1")
	require.NotEmpty(t, result.Lines[0].Points, "历史点不可用当前时间过滤掉")
	assert.Equal(t, 15.0, result.Lines[0].Points[0].Value)
}

func TestHostLines_图例区分主机忙碌等待与收发(t *testing.T) {
	catalog := promql.NewCatalog("config-service")
	for _, tc := range []struct {
		name    string
		queries []promql.Query
		labels  []string
		unit    v1.MetricUnit
	}{
		{"CPU", catalog.HostCPU(), []string{"k1", "k1 / iowait"}, v1.MetricUnit_METRIC_UNIT_PERCENT},
		{"Network", catalog.HostNetwork(), []string{"k1 / receive", "k1 / transmit"}, v1.MetricUnit_METRIC_UNIT_BYTES_PER_SECOND},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Len(t, tc.queries, len(tc.labels))
			for i, q := range tc.queries {
				lines := toLines([]promql.Series{{
					Labels: map[string]string{"host": "k1", "host_kind": "kubernetes"},
					Points: []promql.Sample{{TimestampMS: 1000, Value: 0}},
				}}, q, tc.unit)
				require.Len(t, lines, 1)
				assert.Equal(t, tc.labels[i], lines[0].Label)
				assert.Equal(t, tc.unit, lines[0].Unit)
				require.Len(t, lines[0].Points, 1, "真实的零仍然是数据点")
				assert.Zero(t, lines[0].Points[0].Value)
			}
		})
	}
}
