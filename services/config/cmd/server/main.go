// control-tower config：配置中心服务（config-center 演进版）。
// P1 空壳：fx 起停 + h2c server + healthz/readyz。
// P2 将平移 config-center 的 internal/{biz,data,service,server,iam,presence,pkg}
// 与 web/，并落地 per-service machine token 与 goose migrations（wire 冻结见方案 §4）。
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

var httpAddr = flag.String("httpAddr", envOr("HTTP_PORT", ":30010"), "HTTP/1.1+h2c 监听地址")

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
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return &http.Server{
		Addr:              *httpAddr,
		Handler:           h2c.NewHandler(mux, &http2.Server{}),
		ReadHeaderTimeout: 10 * time.Second,
		// WriteTimeout 必须保持 0：WriteTimeout 会掐断 WatchKeys 长流（历史事故，见 04 报告 §3.3）。
	}
}

func run(lc fx.Lifecycle, srv *http.Server, log *zap.Logger) {
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Fatal("config http server exited", zap.Error(err))
				}
			}()
			log.Info("config listening", zap.String("addr", srv.Addr))
			return nil
		},
		OnStop: func(ctx context.Context) error {
			log.Info("config shutting down")
			return srv.Shutdown(ctx)
		},
	})
}
