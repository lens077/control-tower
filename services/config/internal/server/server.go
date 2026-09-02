package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	connectcors "connectrpc.com/cors"
	"connectrpc.com/validate"
	"github.com/lens077/control-tower/api/config/v1/configv1connect"
	"github.com/lens077/control-tower/api/system/v1/systemv1connect"
	conf "github.com/lens077/control-tower/services/config/internal/conf/v1"
	"github.com/lens077/control-tower/services/config/internal/data"
	"github.com/lens077/control-tower/services/config/internal/iam"
	"github.com/lens077/control-tower/services/config/internal/presence"
	"github.com/lens077/go-connect-kit/meta"
	"github.com/rs/cors"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

var Module = fx.Module("server",
	fx.Provide(
		NewHTTPServer,
	),
)

// NewHTTPServer 构造函数已重构
func NewHTTPServer(
	lc fx.Lifecycle,
	cfg *conf.Bootstrap,
	configv1Service configv1connect.ConfigServiceHandler,
	systemv1Service systemv1connect.SystemServiceHandler,
	logger *zap.Logger,
	connectOptions []connect.HandlerOption,
	deps *data.Data, // 基础设施依赖
	authorizer *iam.Authorizer,
	info meta.AppInfo, // /healthz 回显 API 契约版本;构建版本直接读 meta.Version
) (*http.Server, error) {

	mux := http.NewServeMux()

	// 将 validate 拦截器添加到选项中
	combinedOptions := append(connectOptions, connect.WithInterceptors(validate.NewInterceptor()))

	// 注册 Connect 业务处理器
	configv1connectPath, configv1connectHandler := configv1connect.NewConfigServiceHandler(
		configv1Service,
		combinedOptions...,
	)
	mux.Handle(configv1connectPath, configv1connectHandler)

	// 系统信息(控制台的资源与指标页)。挂在同一个 mux 上,因此自动继承
	// IAM 中间件 —— 那里除 /healthz 外一律要求管理员 JWT,机器 service token
	// 只能走 GetKey/WatchKeys,所以这组接口默认就是管理员专属,不必额外配白名单。
	systemv1connectPath, systemv1connectHandler := systemv1connect.NewSystemServiceHandler(
		systemv1Service,
		combinedOptions...,
	)
	mux.Handle(systemv1connectPath, systemv1connectHandler)

	// 应用本身的健康检查
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		status := healthStatus(r.Context(), deps, info)
		w.Header().Set("Content-Type", "application/json")
		if !status.Healthy {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(status)
	})

	// 构建处理器链
	handlerChain := authorizer.HTTP(mux)

	tlsConf, err := buildTLSConfig(cfg.Server.GetTls())
	if err != nil {
		// 不降级成明文:以为加密了其实没有,比起不来危险得多
		return nil, fmt.Errorf("build server tls config: %w", err)
	}

	if tlsConf == nil {
		// 明文路径(默认):h2c 让同一个端口既接 HTTP/1.1 又接明文 HTTP/2。
		// 集群里 TLS 由共享 Gateway 的 https listener 终止,Pod 侧本就是明文。
		handlerChain = h2c.NewHandler(handlerChain, &http2.Server{})
	}
	// TLS 路径不包 h2c —— h2c 是「明文 HTTP/2」,与 TLS 互斥。
	// 走 TLS 时 HTTP/2 由 ALPN 协商,见 buildTLSConfig 里的 NextProtos。

	handlerChain = withCORS(handlerChain, cfg.Server.Cors.AllowedOrigins)

	server := &http.Server{
		Addr:        cfg.Server.Addr,
		Handler:     handlerChain,
		ReadTimeout: cfg.Server.Http.ReadTimeout.AsDuration(),
		IdleTimeout: cfg.Server.Http.IdleTimeout.AsDuration(),
		// 非 nil 时 main.go 走 ListenAndServeTLS。只有这一处真相源,
		// 免得「配置说开了、启动却没开」这类静默不一致
		TLSConfig: tlsConf,
	}

	// 注册 Fx 生命周期
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			logger.Info("http server starting",
				zap.String("addr", cfg.Server.Addr),
			)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			logger.Info("http server shutting down...")
			return server.Shutdown(ctx)
		},
	})

	return server, nil
}

// withCORS 为处理器添加跨域支持
func withCORS(h http.Handler, allowedOrigins []string) http.Handler {
	exposedHeaders := append(connectcors.ExposedHeaders(), presence.ModeHeader)
	allowedHeaders := append(connectcors.AllowedHeaders(), "Authorization")
	middleware := cors.New(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   connectcors.AllowedMethods(),
		AllowedHeaders:   allowedHeaders,
		ExposedHeaders:   exposedHeaders,
		AllowCredentials: true,
	})
	return middleware.Handler(h)
}

// buildTLSConfig 按配置构造服务端 TLS。返回 nil 表示不启用(明文,默认)。
//
// 校验失败一律返回 error 而不是退化成明文 —— 与本仓「不做失败自动降级」
// 的一贯态度一致:配置说要加密却跑成明文,比根本起不来危险得多,
// 因为它看起来一切正常。
func buildTLSConfig(c *conf.Server_Tls) (*tls.Config, error) {
	if c == nil || !c.GetEnable() {
		return nil, nil
	}

	if c.GetCertPem() == "" || c.GetKeyPem() == "" {
		return nil, errors.New("server.tls.enable 为 true 时 cert_pem 与 key_pem 必填")
	}
	cert, err := tls.X509KeyPair([]byte(c.GetCertPem()), []byte(c.GetKeyPem()))
	if err != nil {
		return nil, fmt.Errorf("解析服务端证书/私钥: %w", err)
	}

	conf := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		// ALPN 里带 h2:走 TLS 时 HTTP/2 靠它协商(替代明文路径的 h2c)。
		// 少了这行,connect 客户端会退回 HTTP/1.1,WatchKeys 那条长流仍能用
		// 但失去多路复用 —— 属于「能跑但悄悄退化」,所以显式写出来
		NextProtos: []string{"h2", "http/1.1"},
	}

	// 以下是 mTLS(双向认证)。当前未启用,留着是为了将来把「全局共享的
	// service token」换成「每服务一张证书」—— 那时调用方身份可以从
	// PeerCertificates[0].Subject.CommonName 取,不必再共用一个密码。
	if c.GetClientCaPem() != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(c.GetClientCaPem())) {
			return nil, errors.New("解析 client_ca_pem 失败:不是合法的 PEM 证书")
		}
		conf.ClientCAs = pool
	}
	if c.GetRequireClientCert() {
		if conf.ClientCAs == nil {
			return nil, errors.New("require_client_cert 为 true 时必须提供 client_ca_pem")
		}
		conf.ClientAuth = tls.RequireAndVerifyClientCert
	} else if conf.ClientCAs != nil {
		// 给了 CA 但不强制:校验带证书的连接,不带证书的照常放行。
		// 用于灰度 —— 先让部分调用方带上证书,验证通过后再翻 require
		conf.ClientAuth = tls.VerifyClientCertIfGiven
	}

	return conf, nil
}
