// Package observability 提供网关的 OTel 装配薄层。
//
// 修正旧网关两处口径缺陷（终裁 §四 / 勾选表 11-12 行）：
//   - 采样 ParentBased(TraceIDRatioBased)，与后端一致（旧网关 AlwaysSample 口径相反）；
//   - 指标经 OTLP 推送（与后端车队一致），不再依赖脆弱的 /metrics XFF 判断。
//
// OTEL_EXPORTER_OTLP_ENDPOINT 未设置时返回 no-op（本地/测试零依赖）。
// 与 services/config/internal/pkg/otel 的去重合并排在 P6（跨 internal 边界需要搬家）。
package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	// 必须与当前 otel SDK 的 resource.Default() schema 同版，否则 resource.Merge
	// 直接报 conflicting Schema URL（实测在集群里炸过：1.34 vs 1.43）。
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// Config 是 OTel 装配参数。
type Config struct {
	ServiceName    string
	ServiceVersion string
	Environment    string
	// Endpoint 形如 host:4318；空=禁用（no-op）。
	Endpoint string
	Insecure bool
	// SampleRatio 是根 span 采样率（ParentBased 内层）。
	SampleRatio float64
}

// Setup 安装全局 TracerProvider/MeterProvider，返回聚合 shutdown。
func Setup(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	if cfg.Endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}

	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.ServiceVersion),
		semconv.DeploymentEnvironmentName(cfg.Environment),
	))
	if err != nil {
		return nil, err
	}

	traceOpts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(cfg.Endpoint)}
	metricOpts := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		traceOpts = append(traceOpts, otlptracehttp.WithInsecure())
		metricOpts = append(metricOpts, otlpmetrichttp.WithInsecure())
	}

	traceExp, err := otlptracehttp.New(ctx, traceOpts...)
	if err != nil {
		return nil, err
	}
	metricExp, err := otlpmetrichttp.New(ctx, metricOpts...)
	if err != nil {
		return nil, err
	}

	ratio := cfg.SampleRatio
	if ratio <= 0 || ratio > 1 {
		ratio = 1
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		// 与后端口径一致：跟随父采样决策，根 span 按比例。
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
		sdktrace.WithBatcher(traceExp),
	)
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(30*time.Second))),
	)
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)

	return func(ctx context.Context) error {
		terr := tp.Shutdown(ctx)
		merr := mp.Shutdown(ctx)
		if terr != nil {
			return terr
		}
		return merr
	}, nil
}
