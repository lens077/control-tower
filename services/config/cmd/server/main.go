package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"time"

	"github.com/lens077/control-tower/constants"
	"github.com/lens077/control-tower/services/config/internal/biz"
	confv1 "github.com/lens077/control-tower/services/config/internal/conf/v1"
	"github.com/lens077/control-tower/services/config/internal/data"
	"github.com/lens077/control-tower/services/config/internal/iam"
	"github.com/lens077/control-tower/services/config/internal/pkg/config"
	logger "github.com/lens077/control-tower/services/config/internal/pkg/log"
	"github.com/lens077/control-tower/services/config/internal/pkg/otel"
	"github.com/lens077/control-tower/services/config/internal/pkg/promql"
	"github.com/lens077/control-tower/services/config/internal/pkg/registry"
	"github.com/lens077/control-tower/services/config/internal/pkg/sysstat"
	"github.com/lens077/control-tower/services/config/internal/presence"
	"github.com/lens077/control-tower/services/config/internal/schema"
	"github.com/lens077/control-tower/services/config/internal/server"
	"github.com/lens077/control-tower/services/config/internal/service"
	"github.com/lens077/go-connect-kit/env"
	"github.com/lens077/go-connect-kit/meta"

	"github.com/google/uuid"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

var (
	serviceName    = flag.String("serviceName", env.GetEnvString(constants.EnvServiceName, "config-service"), "应用名称, e.g.,config-service")
	serviceVersion = flag.String("serviceVersion", env.GetEnvString(constants.EnvServiceVersion, "v1"), "应用版本,e.g.,v1")
	deploymentMode = flag.String("deploymentMode", env.GetEnvString(constants.EnvDeploymentMode, "dev"), "标记应用部署的环境,e.g.,dev/prod/pre/uat")
)

func main() {
	flag.Parse()

	fxApp := NewApp(
		*serviceName,
		*deploymentMode,
		*serviceVersion,
	)

	// 启动应用
	if err := fxApp.Start(context.Background()); err != nil {
		zap.Error(err)
		os.Exit(1)
	}

	// 等待中断信号
	<-fxApp.Done()

	// 优雅关闭
	// 定制一个超时的 Context
	// 确保所有微服务的 OnStop 钩子（包括 Consul 注销、HTTP 关闭、OTel 刷盘）必须在定义的值内收尾
	stopCtx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	if err := fxApp.Stop(stopCtx); err != nil {
		zap.Error(err)
		os.Exit(1)
	}
}

// NewApp 创建并配置 FX 应用
func NewApp(serviceName, deploymentMode, serviceVersion string) *fx.App {
	host, err := meta.GetOutboundIP()
	if err != nil {
		zap.Error(err)
	}
	appInfo := meta.AppInfo{
		ID:          uuid.New().String(),
		Name:        serviceName,
		Version:     serviceVersion,
		Host:        host,
		Environment: deploymentMode,
	}

	return fx.New(
		// 基础模块
		logger.Module,     // 日志
		config.Module,     // 配置
		logger.FxLogger(), // Fx框架本身的日志控制器

		registry.Module, // 服务注册/发现

		// 可观测性 - 根据配置决定是否启用
		fx.Provide(func(conf *confv1.Bootstrap) *confv1.Observability {
			if conf.Observability == nil {
				return &confv1.Observability{Enable: false}
			}
			return conf.Observability
		}),
		otel.Module,

		// 进程资源自采样。必须排在 otel.Module 之后 —— 它会把采样结果注册成
		// OTel 可观测量表,而全局 MeterProvider 是在 otel.Module 里设置的,
		// 早于它注册的话量表会挂在 noop provider 上,表现是「代码在跑但 VM 里没数据」。
		sysstat.Module,
		// 反向查询 VictoriaMetrics(控制台的历史曲线)。返回 nil 表示没配置
		// 查询端,此时页面只显示即时值,这是合法形态而不是故障。
		fx.Provide(promql.New),

		// 注入业务模块（按依赖顺序）
		data.Module,
		iam.Module,
		schema.Module,
		biz.Module,
		presence.Module,
		service.Module,
		server.MiddlewareModule, // 中间件需要在服务模块之前
		server.Module,

		// 传递全局变量
		fx.Supply(appInfo),

		// 配置验证和初始化
		fx.Invoke(
			// 启动之前初始化 Consul 注册中心
			func(reg *registry.ConsulRegistry, logger *zap.Logger) {
				if reg != nil {
					logger.Info("consul service discovery component lifecycle successfully initialized")
				}
			},

			// 初始化并启动核心应用逻辑
			func(lc fx.Lifecycle, conf *confv1.Bootstrap, d *data.Data, logger *zap.Logger, srv *http.Server, otelShutdown func(context.Context) error) {
				lc.Append(fx.Hook{
					// 启动服务时的操作
					OnStart: func(ctx context.Context) error {
						logger.Info("performing startup health checks...")

						// 检查数据库
						if err := d.CheckDatabase(ctx); err != nil {
							return err
						}
						// 检查缓存
						if err := d.CheckCache(ctx); err != nil {
							return err
						}

						logger.Info("starting server",
							zap.String("addr", srv.Addr),
							zap.String("environment", deploymentMode),
						)
						go func() {
							// TLSConfig 非空 = server.tls.enable 为 true(见 internal/server 的
							// buildTLSConfig)。证书与私钥已经解析进 TLSConfig,所以这里传空串。
							// 集群默认走明文:TLS 由共享 Gateway 的 https listener 终止。
							var err error
							if srv.TLSConfig != nil {
								logger.Info("server tls enabled",
									zap.Bool("mtls", srv.TLSConfig.ClientCAs != nil))
								err = srv.ListenAndServeTLS("", "")
							} else {
								err = srv.ListenAndServe()
							}
							if err != nil && !errors.Is(err, http.ErrServerClosed) {
								logger.Fatal("failed to start server", zap.Error(err))
							}
						}()
						return nil
					},
					// 停止服务前的操作
					OnStop: func(ctx context.Context) error {
						logger.Info("stopping server...")
						// 关闭服务器
						if err := srv.Shutdown(ctx); err != nil {
							logger.Error("failed to shutdown server gracefully", zap.Error(err))
						}

						// 关闭transport 维护的空闲 TCP 连接
						if t, ok := http.DefaultTransport.(*http.Transport); ok {
							t.CloseIdleConnections()
						}

						// 关闭otel
						// 1. trace: 强制将内存中还没发出的 Span（链路数据）通过 HTTP 刷给 Collector
						// 2. metric: 它会触发最后一次指标收集，并确保数据推送到后端
						// 3. logging: 确保内存中的日志数据全部持久化
						if otelShutdown != nil {
							return otelShutdown(ctx) // 执行聚合后的停止逻辑
						}
						return nil
					},
				})
			},
		),
	)
}
