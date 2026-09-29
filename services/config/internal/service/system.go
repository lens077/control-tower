package service

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/lens077/control-tower/api/system/v1"
	"github.com/lens077/control-tower/api/system/v1/systemv1connect"
	"github.com/lens077/control-tower/services/config/internal/data"
	"github.com/lens077/control-tower/services/config/internal/pkg/promql"
	"github.com/lens077/control-tower/services/config/internal/pkg/sysstat"
	"github.com/lens077/go-connect-kit/meta"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ systemv1connect.SystemServiceHandler = (*SystemService)(nil)

// defaultStep 是请求没给 step_seconds 时用的步长。
// 与 sysstat 的采样周期和 collector 的 collection_interval 一致(都是 10s),
// 更细只会得到插值出来的假点。
const defaultStep = 10 * time.Second

type SystemService struct {
	sampler *sysstat.Sampler
	metrics *promql.Client // 可为 nil:未配置指标查询端时历史曲线整体关闭
	catalog *promql.Catalog
	deps    *data.Data
	info    meta.AppInfo
	log     *zap.Logger
}

func NewSystemService(
	sampler *sysstat.Sampler,
	metrics *promql.Client,
	deps *data.Data,
	info meta.AppInfo,
	logger *zap.Logger,
) systemv1connect.SystemServiceHandler {
	return &SystemService{
		sampler: sampler,
		metrics: metrics,
		catalog: promql.NewCatalog(info.Name),
		deps:    deps,
		info:    info,
		log:     logger.Named("SystemService"),
	}
}

func (s *SystemService) GetSystemStatus(
	ctx context.Context,
	_ *connect.Request[v1.GetSystemStatusRequest],
) (*connect.Response[v1.GetSystemStatusResponse], error) {
	snap := s.sampler.Snapshot()

	return connect.NewResponse(&v1.GetSystemStatusResponse{
		Process: &v1.ProcessStatus{
			CpuPercent:       snap.CPUPercent,
			CpuLimitCores:    snap.CPULimitCores,
			MemoryRssBytes:   int64(snap.MemoryRSSBytes),
			MemoryLimitBytes: int64(snap.MemoryLimitBytes),
			GoHeapBytes:      int64(snap.GoHeapInUseBytes),
			Goroutines:       snap.Goroutines,
			GcCount:          snap.GCCount,
			DiskPath:         snap.DiskPath,
			DiskUsedBytes:    int64(snap.DiskUsedBytes),
			DiskTotalBytes:   int64(snap.DiskTotalBytes),
			NetRxBytesPerSec: snap.NetRxBytesPerSec,
			NetTxBytesPerSec: snap.NetTxBytesPerSec,
			LimitsFromCgroup: snap.LimitsFromCgroup,
			SampledAt:        timestamppb.New(snap.SampledAt),
			Uptime:           durationpb.New(snap.Uptime),
			Degraded:         snap.Degraded,
		},
		Dependencies: s.dependencies(ctx),
		Build: &v1.BuildInfo{
			ServiceName:  s.info.Name,
			Version:      s.info.Version, // API 契约版本
			Environment:  s.info.Environment,
			GoVersion:    runtime.Version(),
			BuildVersion: meta.Version, // 构建制品版本,ldflags 注入
		},
		MetricsBackendAvailable: s.metrics != nil,
	}), nil
}

// dependencies 复用 server 那边健康检查用的同一组探测。
//
// 刻意不引 server 包(会形成 service → server 的反向依赖),而是直接调
// data 层暴露的检查方法 —— 两处调的是同一个函数,不存在「探针说健康、
// 页面说挂了」的可能。
func (s *SystemService) dependencies(ctx context.Context) []*v1.DependencyStatus {
	checks := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"postgres", s.deps.CheckDatabase},
		{"redis", s.deps.CheckCache},
	}

	result := make([]*v1.DependencyStatus, 0, len(checks))
	for _, c := range checks {
		status := &v1.DependencyStatus{Name: c.name, Healthy: true}
		if err := c.fn(ctx); err != nil {
			status.Healthy = false
			status.Detail = err.Error()
		}
		result = append(result, status)
	}
	return result
}

func (s *SystemService) QueryMetrics(
	ctx context.Context,
	req *connect.Request[v1.QueryMetricsRequest],
) (*connect.Response[v1.QueryMetricsResponse], error) {
	if s.metrics == nil {
		// 不报错:这是一个合法的部署形态(没接 VM)。前端据此隐藏曲线区,
		// 报错的话页面会弹一个红条,而其实什么都没坏。
		return connect.NewResponse(&v1.QueryMetricsResponse{MetricsBackendAvailable: false}), nil
	}

	step := time.Duration(req.Msg.GetStepSeconds()) * time.Second
	if step <= 0 {
		step = defaultStep
	}
	end := time.Now()
	start := end.Add(-req.Msg.GetWindow().AsDuration())

	// 一个请求只读一次清单。它与利用率查询分开,常数1不会流入图表。
	var expected []promql.Series
	var expectedErr error
	for _, series := range req.Msg.GetSeries() {
		queries, _, _ := s.plan(series)
		if len(queries) > 0 && queries[0].ExpectedHosts {
			expected, expectedErr = s.metrics.QueryRange(ctx, s.catalog.HostExpectedInfo().Expr, start, end, step)
			break
		}
	}

	results := make([]*v1.SeriesResult, 0, len(req.Msg.GetSeries()))
	for _, series := range req.Msg.GetSeries() {
		result := s.querySeries(ctx, series, start, end, step)
		queries, unit, _ := s.plan(series)
		if len(queries) > 0 && queries[0].ExpectedHosts {
			if expectedErr != nil {
				result.Error = joinMetricErrors(result.Error, "预期主机清单查询失败: "+expectedErr.Error())
			} else if len(expected) == 0 {
				result.Error = joinMetricErrors(result.Error, "共享规则缺少预期主机清单,无法确认覆盖范围")
			} else {
				for _, q := range queries {
					placeholders := toLines(expected, q, unit)
					for _, line := range placeholders {
						line.Points = nil
					}
					result.Lines = append(result.Lines, placeholders...)
				}
			}
			result.Lines = mergeHostLines(result.Lines)
			// step 可达5分钟,最后一个对齐点离 end 最多差一个 step;勿把它误判为过旧。
			var stale []string
			cutoff := end.Add(-step - promql.HostFreshnessSeconds*time.Second).UnixMilli()
			for _, line := range result.Lines {
				if n := len(line.Points); n > 0 && line.Points[n-1].TsMs < cutoff {
					stale = append(stale, line.Label)
				}
			}
			if len(stale) > 0 {
				result.Error = joinMetricErrors(result.Error, "最近记录缺失或超过 180s（另计查询步长）,仅保留历史: "+strings.Join(stale, ", "))
			}
		}
		results = append(results, result)
	}

	return connect.NewResponse(&v1.QueryMetricsResponse{
		Results:                 results,
		MetricsBackendAvailable: true,
	}), nil
}

// querySeries 查一组曲线。任何失败都收进 SeriesResult.error 返回,
// 不往上抛 —— 一组指标没数据不该让整页空白,这与 sysstat 的 Degraded 同一个取向。
func (s *SystemService) querySeries(
	ctx context.Context,
	series v1.MetricSeries,
	start, end time.Time,
	step time.Duration,
) *v1.SeriesResult {
	out := &v1.SeriesResult{Series: series}

	queries, unit, err := s.plan(series)
	if err != nil {
		out.Error = err.Error()
		return out
	}

	for _, q := range queries {
		found, err := s.metrics.QueryRange(ctx, q.Expr, start, end, step)
		if err != nil {
			// 记日志是因为这里的失败信息(超时、VM 5xx)对排查有用,
			// 而前端只会把 error 显示成一行灰字。
			s.log.Warn("query metrics failed",
				zap.String("series", series.String()),
				zap.String("expr", q.Expr),
				zap.Error(err))
			out.Error = joinMetricErrors(out.Error, err.Error())
			continue
		}
		out.Lines = append(out.Lines, toLines(found, q, unit)...)
	}

	// 部分查询失败时保留已有曲线和错误,不能用一条成功曲线掩盖另一条失败。
	return out
}

func joinMetricErrors(current, next string) string {
	if current == "" {
		return next
	}
	return current + "; " + next
}

// 清单标签变更或历史数据可能带来同名线。按图例合并去重,真实点覆盖空占位,
// 不对利用率求和,不把 metadata 的0/1当成数据;输出顺序保持稳定。
func mergeHostLines(lines []*v1.MetricLine) []*v1.MetricLine {
	byLabel := make(map[string]*v1.MetricLine)
	points := make(map[string]map[int64]*v1.MetricPoint)
	for _, line := range lines {
		if byLabel[line.Label] == nil {
			byLabel[line.Label] = &v1.MetricLine{Label: line.Label, Unit: line.Unit}
			points[line.Label] = make(map[int64]*v1.MetricPoint)
		}
		for _, point := range line.Points {
			if _, exists := points[line.Label][point.TsMs]; !exists {
				points[line.Label][point.TsMs] = point
			}
		}
	}
	result := make([]*v1.MetricLine, 0, len(byLabel))
	for label, line := range byLabel {
		for _, point := range points[label] {
			line.Points = append(line.Points, point)
		}
		sort.Slice(line.Points, func(i, j int) bool { return line.Points[i].TsMs < line.Points[j].TsMs })
		result = append(result, line)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Label < result[j].Label })
	return result
}

// plan 把枚举翻译成查询与量纲。新增 MetricSeries 时这里不加分支会直接报错,
// 而不是静默返回空 —— 后者的表现是「页面上少一张图」,没人会注意到。
func (s *SystemService) plan(series v1.MetricSeries) ([]promql.Query, v1.MetricUnit, error) {
	switch series {
	case v1.MetricSeries_METRIC_SERIES_PROCESS_CPU:
		return s.catalog.ProcessCPU(), v1.MetricUnit_METRIC_UNIT_PERCENT, nil
	case v1.MetricSeries_METRIC_SERIES_PROCESS_MEMORY:
		return s.catalog.ProcessMemory(), v1.MetricUnit_METRIC_UNIT_BYTES, nil
	case v1.MetricSeries_METRIC_SERIES_PROCESS_GOROUTINES:
		return s.catalog.ProcessGoroutines(), v1.MetricUnit_METRIC_UNIT_COUNT, nil
	case v1.MetricSeries_METRIC_SERIES_PROCESS_NETWORK:
		return s.catalog.ProcessNetwork(), v1.MetricUnit_METRIC_UNIT_BYTES_PER_SECOND, nil
	case v1.MetricSeries_METRIC_SERIES_HOST_CPU:
		return s.catalog.HostCPU(), v1.MetricUnit_METRIC_UNIT_PERCENT, nil
	case v1.MetricSeries_METRIC_SERIES_HOST_MEMORY:
		return s.catalog.HostMemory(), v1.MetricUnit_METRIC_UNIT_PERCENT, nil
	case v1.MetricSeries_METRIC_SERIES_HOST_DISK:
		return s.catalog.HostDisk(), v1.MetricUnit_METRIC_UNIT_PERCENT, nil
	case v1.MetricSeries_METRIC_SERIES_HOST_NETWORK:
		return s.catalog.HostNetwork(), v1.MetricUnit_METRIC_UNIT_BYTES_PER_SECOND, nil
	case v1.MetricSeries_METRIC_SERIES_API_LATENCY:
		return s.catalog.APILatency(), v1.MetricUnit_METRIC_UNIT_MILLISECONDS, nil
	case v1.MetricSeries_METRIC_SERIES_API_THROUGHPUT:
		return s.catalog.APIThroughput(), v1.MetricUnit_METRIC_UNIT_REQUESTS_PER_SECOND, nil
	case v1.MetricSeries_METRIC_SERIES_API_ERROR_RATE:
		return s.catalog.APIErrorRate(), v1.MetricUnit_METRIC_UNIT_PERCENT, nil
	case v1.MetricSeries_METRIC_SERIES_DB_LATENCY:
		return s.catalog.DBLatency(), v1.MetricUnit_METRIC_UNIT_MILLISECONDS, nil
	case v1.MetricSeries_METRIC_SERIES_DB_POOL:
		return s.catalog.DBPool(), v1.MetricUnit_METRIC_UNIT_COUNT, nil
	default:
		return nil, v1.MetricUnit_METRIC_UNIT_UNSPECIFIED,
			fmt.Errorf("未知的指标组 %s", series)
	}
}

// toLines 把 promql 结果转成前端要的形状,并决定每条线的图例名。
func toLines(found []promql.Series, q promql.Query, unit v1.MetricUnit) []*v1.MetricLine {
	lines := make([]*v1.MetricLine, 0, len(found))
	for _, s := range found {
		label := q.FixedLabel
		if q.LabelKey != "" {
			if value := s.Labels[q.LabelKey]; value != "" {
				label = value
			}
		}
		if label == "" {
			// 聚合掉全部标签的查询会走到这里(结果里没有任何标签可用)。
			// 给个占位而不是留空:图例为空的线在前端会渲染成一个孤零零的
			// 色块,看起来像渲染出了 bug。
			label = "value"
		}

		label += q.LabelSuffix

		points := make([]*v1.MetricPoint, 0, len(s.Points))
		for _, p := range s.Points {
			points = append(points, &v1.MetricPoint{TsMs: p.TimestampMS, Value: p.Value})
		}
		lines = append(lines, &v1.MetricLine{Label: label, Unit: unit, Points: points})
	}

	// 按图例名排序,保证同一张图每次刷新时线的顺序(以及配色)稳定。
	// 不排的话 VM 返回的顺序会变,用户会看到颜色在两次刷新之间互换。
	sort.Slice(lines, func(i, j int) bool { return lines[i].Label < lines[j].Label })
	return lines
}
