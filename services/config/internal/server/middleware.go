package server

import (
	"net/http"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	confv1 "github.com/lens077/control-tower/services/config/internal/conf/v1"
	"github.com/lens077/control-tower/services/config/internal/iam"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

var MiddlewareModule = fx.Module("server.middleware",
	fx.Provide(
		// 提供拦截器实例
		NewLoggingInterceptor,

		// 组装成一个拦截器切片，或者直接返回 Connect Option
		NewConnectOptions,
	),
)

func NewConnectOptions(
	logger *zap.Logger,
	logging *LoggingInterceptor,
	observability *confv1.Observability,
) []connect.HandlerOption {
	var interceptors []connect.Interceptor

	// 只有当 observability 启用时才添加 otel 拦截器
	if observability != nil && observability.Enable {
		// WithoutServerPeerAttributes 去掉 net.peer.name / net.peer.port 两个属性。
		//
		// net.peer.port 是客户端的临时端口,取值无上界:每来一条新连接就是一个新值,
		// 与 le 桶相乘后每个直方图会持续长出新序列,而已写入的旧序列不会自己消失。
		// config-center 尤其受害 —— WatchKeys 是长连接,客户端每次重连都换端口。
		//
		// 实测(2026-08):VM 总共 4862 条序列,rpc_server_* 五个直方图占了 4076 条,
		// net_peer_port 已有 39 个取值,且只会涨。
		//
		// 代价是丢掉「按调用方 IP 拆分」的能力。这在这里不是损失:进程只从网关入站,
		// 真正的调用方身份在 IAM 的 service token 里,不在 TCP 四元组里。
		otelInterceptor, err := otelconnect.NewInterceptor(otelconnect.WithoutServerPeerAttributes())
		if err != nil {
			logger.Fatal("failed to init otel interceptor", zap.Error(err))
		}
		interceptors = append(interceptors, otelInterceptor)
	}

	// 添加日志拦截器
	interceptors = append(interceptors, logging)

	return []connect.HandlerOption{
		connect.WithInterceptors(interceptors...),
	}
}

// NewIAMMiddleware authorizes the complete HTTP request before Connect decodes
// it, so the same rule protects unary and streaming procedures.
func NewIAMMiddleware(authorizer *iam.Authorizer) func(http.Handler) http.Handler {
	return authorizer.HTTP
}
