package promql

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	confv1 "github.com/lens077/control-tower/services/config/internal/conf/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"
)

func enabled(endpoint string) *confv1.Observability {
	return &confv1.Observability{
		Enable:      true,
		MetricQuery: &confv1.Observability_MetricQuery{Endpoint: endpoint},
	}
}

// 没配置查询端要返回 (nil, nil) 而不是错误:那是一个合法的部署形态,
// 调用方据此把历史曲线整体关掉。返回错误的话进程会起不来。
func TestNew_未配置时返回nil而非错误(t *testing.T) {
	cases := map[string]*confv1.Observability{
		"配置为 nil":         nil,
		"可观测性整体关闭":        {Enable: false, MetricQuery: &confv1.Observability_MetricQuery{Endpoint: "http://vm:8428"}},
		"没有 metric_query": {Enable: true},
		"endpoint 为空":     {Enable: true, MetricQuery: &confv1.Observability_MetricQuery{Endpoint: "  "}},
	}

	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			client, err := New(cfg)
			require.NoError(t, err)
			assert.Nil(t, client)
		})
	}
}

// 地址配错要在启动时就失败,而不是等到有人点开页面才发现查不到数据。
func TestNew_地址非法时报错(t *testing.T) {
	for _, endpoint := range []string{"vm:8428", "not a url", "/only/path"} {
		t.Run(endpoint, func(t *testing.T) {
			_, err := New(enabled(endpoint))
			require.Error(t, err, "缺少 scheme 的地址必须被拒绝")
		})
	}
}

func TestNew_默认超时(t *testing.T) {
	client, err := New(enabled("http://vm:8428"))
	require.NoError(t, err)
	assert.Equal(t, defaultTimeout, client.timeout)

	cfg := enabled("http://vm:8428")
	cfg.MetricQuery.Timeout = durationpb.New(2 * time.Second)
	client, err = New(cfg)
	require.NoError(t, err)
	assert.Equal(t, 2*time.Second, client.timeout)
}

func TestQueryRange_正常解析(t *testing.T) {
	var gotQuery, gotStep, gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotMethod = r.Method
		gotQuery = r.Form.Get("query")
		gotStep = r.Form.Get("step")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[
			{"metric":{"k8s_node_name":"node2"},"values":[[1754000000,"63.88"],[1754000010,"64.10"]]}
		]}}`))
	}))
	defer server.Close()

	client, err := New(enabled(server.URL))
	require.NoError(t, err)

	end := time.Unix(1754000010, 0)
	series, err := client.QueryRange(context.Background(), `up{job="x"}`, end.Add(-time.Minute), end, 10*time.Second)
	require.NoError(t, err)

	// 用 POST 而不是 GET —— 分位数那几条语句很长,GET 会撞上代理的 URL 长度限制
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, `up{job="x"}`, gotQuery, "查询语句必须原样送达,不能被转义破坏")
	assert.Equal(t, "10", gotStep)

	require.Len(t, series, 1)
	assert.Equal(t, "node2", series[0].Labels["k8s_node_name"])
	require.Len(t, series[0].Points, 2)
	// 时间戳要从秒换成毫秒,前端的图表库按毫秒吃
	assert.Equal(t, int64(1754000000000), series[0].Points[0].TimestampMS)
	assert.InDelta(t, 63.88, series[0].Points[0].Value, 0.001)
}

// NaN 出现在直方图分位数无样本时。跳过这些点让曲线断开,
// 而不是记成 0 —— 后者会在图上画出一条并不存在的谷底。
func TestQueryRange_跳过NaN而不是记成零(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[
			{"metric":{},"values":[[1754000000,"NaN"],[1754000010,"5"],[1754000020,"+Inf"]]}
		]}}`))
	}))
	defer server.Close()

	client, err := New(enabled(server.URL))
	require.NoError(t, err)
	series, err := client.QueryRange(context.Background(), "q", time.Unix(1754000000, 0), time.Unix(1754000020, 0), 10*time.Second)
	require.NoError(t, err)

	require.Len(t, series, 1)
	require.Len(t, series[0].Points, 1, "NaN 与 +Inf 都该被丢掉")
	assert.Equal(t, 5.0, series[0].Points[0].Value)
}

// 一条线上一个点都没有时整条线丢掉,前端就不会收到一个空图例。
func TestQueryRange_全是NaN的线被整条丢弃(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[
			{"metric":{},"values":[[1754000000,"NaN"]]}
		]}}`))
	}))
	defer server.Close()

	client, _ := New(enabled(server.URL))
	series, err := client.QueryRange(context.Background(), "q", time.Unix(1754000000, 0), time.Unix(1754000010, 0), 10*time.Second)
	require.NoError(t, err)
	assert.Empty(t, series)
}

func TestQueryRange_错误分支(t *testing.T) {
	t.Run("VM 返回 5xx", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
		}))
		defer server.Close()

		client, _ := New(enabled(server.URL))
		_, err := client.QueryRange(context.Background(), "q", time.Unix(0, 0), time.Unix(60, 0), 10*time.Second)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "500", "状态码要带进错误里,否则排查时只知道「失败了」")
	})

	t.Run("VM 报告查询语法错误", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":"error","error":"unexpected token"}`))
		}))
		defer server.Close()

		client, _ := New(enabled(server.URL))
		_, err := client.QueryRange(context.Background(), "q", time.Unix(0, 0), time.Unix(60, 0), 10*time.Second)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected token")
	})

	t.Run("返回的不是 matrix", func(t *testing.T) {
		// query 而不是 query_range 的响应会长这样。发现类型不对要报错,
		// 而不是当成空结果 —— 后者会把「代码调错了端点」伪装成「没数据」。
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
		}))
		defer server.Close()

		client, _ := New(enabled(server.URL))
		_, err := client.QueryRange(context.Background(), "q", time.Unix(0, 0), time.Unix(60, 0), 10*time.Second)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "matrix")
	})
}

// 点数上限是防止一次请求把几十万个点塞进响应体。
// 拒绝而不是截断:截断出来的曲线看着完整,实则少了一截。
func TestQueryRange_点数超限时拒绝(t *testing.T) {
	client, err := New(enabled("http://vm:8428"))
	require.NoError(t, err)

	end := time.Now()
	_, err = client.QueryRange(context.Background(), "q", end.Add(-24*time.Hour), end, time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "超过上限")
}

func TestQueryRange_step必须为正(t *testing.T) {
	client, _ := New(enabled("http://vm:8428"))
	_, err := client.QueryRange(context.Background(), "q", time.Unix(0, 0), time.Unix(60, 0), 0)
	require.Error(t, err)
}
