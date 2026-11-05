package server

import (
	"connectrpc.com/connect/v2"
	"connectrpc.com/otelconnect"
	"connectrpc.com/validate"
	confv1 "github.com/lens077/control-tower/services/config/internal/conf/v1"
	"github.com/lens077/control-tower/services/config/internal/iam"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"net/http"
)

var MiddlewareModule = fx.Module("server.middleware",
	fx.Provide(
		// 提供拦截器实例
		NewLoggingInterceptor,

		// 组装成 connect v2 服务端拦截器切片
		NewConnectOptions,
	),
)

func NewConnectOptions(
	logger *zap.Logger,
	logging connect.ServerInterceptor,
	observability *confv1.Observability,
) []connect.ServerInterceptor {
	var interceptors []connect.ServerInterceptor

	// 请求体校验始终启用,放在最外层:坏消息在记日志/开 span 之前就被打回。
	interceptors = append(interceptors, validate.NewServerInterceptor())

	// 只有当 observability 启用时才添加 otel 拦截器
	if observability != nil && observability.Enable {
		// v1 时代用 WithoutServerPeerAttributes 去掉 net.peer.name / net.peer.port:
		// net.peer.port 是客户端临时端口,取值无上界,与 le 桶相乘后直方图序列只会涨
		// —— config-center 尤其受害(WatchKeys 长连接每次重连都换端口)。实测(2026-08):
		// VM 4862 条序列里 rpc_server_* 五个直方图占 4076 条。connect v2 中 server span
		// 默认就不带这两个属性(选项反转为 WithServerPeerAttributes 选择开启),无需再传。
		// 代价仍是丢掉「按调用方 IP 拆分」:进程只从网关入站,真正的调用方身份在
		// IAM 的 service token 里,不在 TCP 四元组里。
		otelInterceptor, err := otelconnect.NewServerInterceptor()
		if err != nil {
			logger.Fatal("failed to init otel interceptor", zap.Error(err))
		}
		interceptors = append(interceptors, otelInterceptor)
	}

	// 添加日志拦截器
	interceptors = append(interceptors, logging)

	return interceptors
}

// NewIAMMiddleware authorizes the complete HTTP request before Connect decodes
// it, so the same rule protects unary and streaming procedures.
func NewIAMMiddleware(authorizer *iam.Authorizer) func(http.Handler) http.Handler {
	return authorizer.HTTP
}
