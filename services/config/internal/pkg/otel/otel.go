package otel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"runtime"
	"time"

	confv1 "github.com/lens077/control-tower/services/config/internal/conf/v1"
	"github.com/lens077/go-connect-kit/meta"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

var (
	// Module 提供 Fx 模块
	Module = fx.Module("otel",
		fx.Provide(
			// 提供 OpenTelemetry 设置函数
			func(info meta.AppInfo, cfg *confv1.Observability, logger *zap.Logger) (func(context.Context) error, error) {
				return SetupOTelSDK(context.Background(), info, cfg, logger)
			},
		),
	)
)

type TraceOption func(opts *traceOptions)
type traceOptions struct {
	logger   *zap.Logger
	endpoint string
	tls      otlptracehttp.Option
}
type MetricOption func(opts *metricOptions)
type metricOptions struct {
	logger   *zap.Logger
	endpoint string
	tls      otlpmetrichttp.Option
}

type LogOption func(opts *logOptions)
type logOptions struct {
	logger   *zap.Logger
	endpoint string
	tls      otlploghttp.Option
}

// SetupOTelSDK bootstraps the OpenTelemetry pipeline.
func SetupOTelSDK(ctx context.Context, info meta.AppInfo, cfg *confv1.Observability, logger *zap.Logger) (func(context.Context) error, error) {
	if cfg == nil || !cfg.Enable {
		logger.Info("Observability is disabled, skipping OpenTelemetry setup")
		return func(ctx context.Context) error { return nil }, nil
	}

	// 存储所有子组件停止方法的切片
	var shutdownFuncs []func(context.Context) error
	var err error

	// 返回给外部的关闭otel
	shutdown := func(ctx context.Context) error {
		var err error
		for _, fn := range shutdownFuncs {
			err = errors.Join(err, fn(ctx)) // 合并所有组件的错误
		}
		shutdownFuncs = nil
		return err
	}

	handleErr := func(inErr error) {
		err = errors.Join(inErr, shutdown(ctx))
	}

	prop := newPropagator()
	otel.SetTextMapPropagator(prop)

	res, err := newResource(info)
	if err != nil {
		handleErr(err)
		return shutdown, err
	}

	var traceTlsOpt otlptracehttp.Option
	var metricTlsOpt otlpmetrichttp.Option
	var logTlsOpt otlploghttp.Option
	if cfg.Trace.Tls.Enable {
		tOpts := &traceOptions{logger: logger}
		// 假设从配置文件读取 CA 内容或跳过验证
		WithTraceTLS(cfg.Trace.Tls.InsecureSkipVerify, []byte(cfg.Trace.Tls.CaPem))(tOpts)
		traceTlsOpt = tOpts.tls
	}

	if cfg.Metric.Tls.Enable {
		tOpts := &metricOptions{logger: logger}
		// 假设从配置文件读取 CA 内容或跳过验证
		WithMetricTLS(cfg.Metric.Tls.InsecureSkipVerify, []byte(cfg.Metric.Tls.CaPem))(tOpts)
		metricTlsOpt = tOpts.tls
	}

	if cfg.Log.Tls.Enable {
		tOpts := &logOptions{logger: logger}
		// 假设从配置文件读取 CA 内容或跳过验证
		WithLogTLS(cfg.Log.Tls.InsecureSkipVerify, []byte(cfg.Log.Tls.CaPem))(tOpts)
		logTlsOpt = tOpts.tls
	}

	// 注入Trace
	tracerProvider, err := newTracerProvider(res, cfg.Trace.Endpoint, traceTlsOpt)
	if err != nil {
		handleErr(err)
		return shutdown, err
	}
	shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown)
	otel.SetTracerProvider(tracerProvider)

	metricProvider, err := newMeterProvider(res, cfg.Metric.Endpoint, metricTlsOpt)
	if err != nil {
		handleErr(err)
		return shutdown, err
	}
	shutdownFuncs = append(shutdownFuncs, metricProvider.Shutdown)
	otel.SetMeterProvider(metricProvider)

	loggerProvider, err := newLoggerProvider(res, cfg.Log.Endpoint, logTlsOpt)
	if err != nil {
		handleErr(err)
		return shutdown, err
	}
	shutdownFuncs = append(shutdownFuncs, loggerProvider.Shutdown)
	global.SetLoggerProvider(loggerProvider)

	return shutdown, err
}

func WithTraceTLS(insecureSkipVerify bool, caPem []byte) TraceOption {
	return func(o *traceOptions) {
		tlsConf := &tls.Config{InsecureSkipVerify: insecureSkipVerify}

		if !insecureSkipVerify && len(caPem) > 0 {
			caCertPool := x509.NewCertPool()
			if ok := caCertPool.AppendCertsFromPEM(caPem); ok {
				tlsConf.RootCAs = caCertPool
			} else {
				o.logger.Error("failed to append ca cert")
			}
		}
		o.tls = otlptracehttp.WithTLSClientConfig(tlsConf)
	}
}

func WithMetricTLS(insecureSkipVerify bool, caPem []byte) MetricOption {
	return func(o *metricOptions) {
		tlsConf := &tls.Config{InsecureSkipVerify: insecureSkipVerify}

		if !insecureSkipVerify && len(caPem) > 0 {
			caCertPool := x509.NewCertPool()
			if ok := caCertPool.AppendCertsFromPEM(caPem); ok {
				tlsConf.RootCAs = caCertPool
			} else {
				o.logger.Error("failed to append ca cert")
			}
		}
		o.tls = otlpmetrichttp.WithTLSClientConfig(tlsConf)
	}
}

func WithLogTLS(insecureSkipVerify bool, caPem []byte) LogOption {
	return func(o *logOptions) {
		tlsConf := &tls.Config{InsecureSkipVerify: insecureSkipVerify}

		if !insecureSkipVerify && len(caPem) > 0 {
			caCertPool := x509.NewCertPool()
			if ok := caCertPool.AppendCertsFromPEM(caPem); ok {
				tlsConf.RootCAs = caCertPool
			} else {
				o.logger.Error("failed to append ca cert")
			}
		}
		o.tls = otlploghttp.WithTLSClientConfig(tlsConf)
	}
}

func newResource(info meta.AppInfo) (*resource.Resource, error) {
	return resource.NewWithAttributes(
		semconv.SchemaURL,                                             // URL
		semconv.ServiceName(info.Name),                                // 应用名称
		semconv.ServiceNamespace("config-center"),                     // 与电商业务服务隔离
		semconv.ServiceVersion(info.Version),                          // 应用版本
		semconv.TelemetrySDKVersion(otel.Version()),                   // otel 的版本
		semconv.DeploymentEnvironmentNameKey.String(info.Environment), // 部署环境
		semconv.TelemetrySDKLanguageGo,                                // 使用 otel 的语言
		attribute.String("GolangVersion", runtime.Version()),          // Golang 版本
	), nil
}

func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

func newTracerProvider(res *resource.Resource, endpoint string, tlsOpt otlptracehttp.Option) (*trace.TracerProvider, error) {
	ctx := context.Background()

	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(endpoint),
	}
	if tlsOpt != nil {
		opts = append(opts, tlsOpt)
	} else {
		// 如果没有 TLS 配置，必须显式指定 Insecure
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	traceExporter, err := otlptracehttp.New(
		ctx,
		opts...,
	)
	if err != nil {
		return nil, err
	}

	bsp := trace.NewBatchSpanProcessor(traceExporter)
	tracerProvider := trace.NewTracerProvider(
		trace.WithSampler(trace.AlwaysSample()),
		trace.WithResource(res),
		trace.WithSpanProcessor(bsp),
	)
	return tracerProvider, nil
}

// Push 模式
// 主动将指标推向 OTLP Collector
func newMeterProvider(res *resource.Resource, endpoint string, tlsOpt otlpmetrichttp.Option) (*metric.MeterProvider, error) {
	ctx := context.Background()
	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpoint(endpoint),
	}
	if tlsOpt != nil {
		opts = append(opts, tlsOpt)
	} else {
		// 如果没有 TLS 配置，必须显式指定 Insecure
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}

	metricExporter, err := otlpmetrichttp.New(
		ctx,
		opts...,
	)
	if err != nil {
		return nil, err
	}

	meterProvider := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metric.NewPeriodicReader(metricExporter,
			metric.WithInterval(30*time.Second))),
	)
	return meterProvider, nil
}

func newLoggerProvider(res *resource.Resource, endpoint string, tlsOpt otlploghttp.Option) (*log.LoggerProvider, error) {
	ctx := context.Background()
	opts := []otlploghttp.Option{
		otlploghttp.WithEndpoint(endpoint),
	}
	if tlsOpt != nil {
		opts = append(opts, tlsOpt)
	} else {
		// 如果没有 TLS 配置，必须显式指定 Insecure
		opts = append(opts, otlploghttp.WithInsecure())
	}

	logExporter, err := otlploghttp.New(
		ctx,
		opts...,
	)
	if err != nil {
		return nil, err
	}

	loggerProvider := log.NewLoggerProvider(
		log.WithResource(res),
		log.WithProcessor(log.NewBatchProcessor(logExporter)),
	)
	return loggerProvider, nil
}
