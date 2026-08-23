// control-tower gateway：瘦身 Connect 反代 + 鉴权（BFF-ready）。
// P1 空壳：fx 起停 + h2c server + healthz/readyz。
// P3 将接入：路由/resolver/代理核心、authn/authz、可观测性与真实就绪条件。
package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"time"

	"go.uber.org/fx"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

var httpAddr = flag.String("httpAddr", envOr("HTTP_PORT", ":8080"), "HTTP/1.1+h2c 监听地址")

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	flag.Parse()
	app := fx.New(
		fx.Provide(newLogger, newServer),
		fx.Invoke(run),
		fx.StopTimeout(8*time.Second),
		fx.NopLogger,
	)
	// Run 处理 SIGINT/SIGTERM，并在 StopTimeout 内执行全部 OnStop 钩子。
	app.Run()
}

func newLogger() (*zap.Logger, error) {
	return zap.NewProduction()
}

func newServer() *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// P1 占位：P3 起 readyz 的就绪条件 = 路由表 + JWT 公钥 + Casbin 模型/策略全部加载成功。
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return &http.Server{
		Addr: *httpAddr,
		// TLS 由 Cilium Gateway 终止，网关只听明文 HTTP/1.1 + h2c。
		Handler:           h2c.NewHandler(mux, &http2.Server{}),
		ReadHeaderTimeout: 10 * time.Second,
		// WriteTimeout 保持 0：超时统一走路由级 context（WatchKeys 掐流教训，见方案 §3.1）。
	}
}

func run(lc fx.Lifecycle, srv *http.Server, log *zap.Logger) {
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Fatal("gateway http server exited", zap.Error(err))
				}
			}()
			log.Info("gateway listening", zap.String("addr", srv.Addr))
			return nil
		},
		OnStop: func(ctx context.Context) error {
			log.Info("gateway shutting down")
			return srv.Shutdown(ctx)
		},
	})
}
