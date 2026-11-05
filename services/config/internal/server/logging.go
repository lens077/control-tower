package server

import (
	"connectrpc.com/connect/v2"
	"github.com/lens077/go-connect-kit/meta"
	"github.com/lens077/go-connect-kit/rpcobs"
	"go.uber.org/zap"
)

// NewLoggingInterceptor 装配 kit/rpcobs 的 RPC 可观测性拦截器(日志分级、span 属性、
// rpc.server.errors{error.reason} 计数)。此前 config 自带一份 v1 时代副本,与 kit
// 下沉的版本漂移;rpcobs 是 connect v2 的 ServerInterceptor,unary 与 streaming 一并
// 覆盖(WatchKeys 长连接此前完全不走日志)。config 的业务错误未声明 errinfo reason,
// ErrorInfo 附加不会触发,行为对客户端零变化。
func NewLoggingInterceptor(logger *zap.Logger, info meta.AppInfo) connect.ServerInterceptor {
	return rpcobs.New(logger, rpcobs.WithDomain(info.Name))
}
