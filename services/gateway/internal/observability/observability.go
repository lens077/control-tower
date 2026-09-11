// Package observability 将网关部署参数映射到 go-connect-kit/otel。
//
// 网关只启用 trace 与 metric；日志管道和运行时指标保持关闭。Endpoint 为空时走 no-op。
package observability

import (
	"context"
	"time"

	"github.com/lens077/go-connect-kit/meta"
	kitotel "github.com/lens077/go-connect-kit/otel"
	"go.uber.org/zap"
)

// Config 保存网关自己负责的 OpenTelemetry 部署参数。
type Config struct {
	ServiceName    string
	ServiceVersion string
	Environment    string
	// Endpoint 形如 host:4318；空值表示禁用遥测。
	Endpoint string
	Insecure bool
	// SampleRatio 是 ParentBased 内的根 span 采样率；nil 使用 kit 默认值，显式 0 表示不采样。
	SampleRatio *float64
}

// Setup 将 trace 与 metric 的构造、全局安装和关闭委托给 go-connect-kit。
func Setup(ctx context.Context, cfg Config, logger *zap.Logger) (func(context.Context) error, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	options := kitotel.Options{}
	if cfg.Endpoint != "" {
		tls := kitotel.TLSOptions{Enabled: !cfg.Insecure}
		options.Trace = &kitotel.TraceOptions{
			Endpoint:    cfg.Endpoint,
			SampleRatio: cfg.SampleRatio,
			TLS:         tls,
		}
		options.Metric = &kitotel.MetricOptions{
			Endpoint:       cfg.Endpoint,
			ExportInterval: 30 * time.Second,
			TLS:            tls,
		}
	}

	return kitotel.SetupOTelSDK(ctx, meta.AppInfo{
		Name:        cfg.ServiceName,
		Version:     cfg.ServiceVersion,
		Environment: cfg.Environment,
	}, options, logger)
}
