// control-tower gateway：瘦身 Connect 反代 + 鉴权（BFF-ready）。
// 链路与不变式见 docs/design/architecture.md；鉴权语义见 docs/design/auth.md。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"strconv"
	"strings"
	"time"

	capi "github.com/hashicorp/consul/api"
	"github.com/lens077/control-tower/services/gateway/internal/app"
	"github.com/lens077/control-tower/services/gateway/internal/authn"
	"github.com/lens077/control-tower/services/gateway/internal/authz"
	"github.com/lens077/control-tower/services/gateway/internal/gwerrors"
	"github.com/lens077/control-tower/services/gateway/internal/httpmw"
	"github.com/lens077/control-tower/services/gateway/internal/loader"
	"github.com/lens077/control-tower/services/gateway/internal/observability"
	"github.com/lens077/control-tower/services/gateway/internal/resolver"

	"go.uber.org/fx"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// Version 由 -ldflags 注入。
var Version = "dev"

var (
	httpAddr  = flag.String("httpAddr", envOr("HTTP_PORT", ":8080"), "业务端口（HTTP/1.1+h2c）")
	adminAddr = flag.String("adminAddr", envOr("ADMIN_PORT", ":9090"), "内部管理端口（pprof，默认关闭）")
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	flag.Parse()
	app := fx.New(
		fx.Provide(newLogger),
		fx.Invoke(run),
		fx.StopTimeout(10*time.Second),
		fx.NopLogger,
	)
	// 不用 app.Run()：NopLogger 下它会把启动错误吞成静默 exit 1（实测踩过——
	// CrashLoopBackOff 且 kubectl logs 全空）。显式 Start 并把错误打到 stderr。
	startCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	if err := app.Start(startCtx); err != nil {
		cancel()
		fmt.Fprintln(os.Stderr, "gateway start failed:", err)
		os.Exit(1)
	}
	cancel()
	<-app.Done()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if err := app.Stop(stopCtx); err != nil {
		fmt.Fprintln(os.Stderr, "gateway stop failed:", err)
		os.Exit(1)
	}
}

func newLogger() (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()
	if lvl, err := zapcore.ParseLevel(envOr("LOG_LEVEL", "info")); err == nil {
		cfg.Level = zap.NewAtomicLevelAt(lvl)
	}
	return cfg.Build()
}

// run 按依赖顺序完成装配：观测 → 鉴权组件 → 动态配置加载 → resolver → 服务器。
func run(lc fx.Lifecycle, log *zap.Logger) error {
	ctx := context.Background()

	// ── 可观测性（OTEL_EXPORTER_OTLP_ENDPOINT 未设置时 no-op）。
	ratio, _ := strconv.ParseFloat(envOr("OTEL_TRACES_SAMPLER_RATIO", "1"), 64)
	otelShutdown, err := observability.Setup(ctx, observability.Config{
		ServiceName:    "control-tower-gateway",
		ServiceVersion: Version,
		Environment:    envOr("DEPLOYMENT_MODE", "dev"),
		Endpoint:       os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		Insecure:       envOr("OTEL_EXPORTER_OTLP_INSECURE", "true") == "true",
		SampleRatio:    ratio,
	})
	if err != nil {
		return fmt.Errorf("otel setup: %w", err)
	}

	// ── 鉴权组件。issuer=Casdoor origin；audiences=各前端 client id（CSV）。
	issuer := envOr("JWT_ISSUER", envOr("CASDOOR_URL", ""))
	audiences := splitCSV(os.Getenv("JWT_AUDIENCES"))
	if issuer == "" || len(audiences) == 0 {
		return errors.New("JWT_ISSUER/CASDOOR_URL 与 JWT_AUDIENCES 必须设置（信任域绑定，见 docs/design/auth.md）")
	}
	verifier, err := authn.New(issuer, audiences)
	if err != nil {
		return err
	}
	enforcer := authz.New()
	corsSwap := httpmw.NewCorsSwapper()
	state := loader.NewState(verifier, enforcer, corsSwap, log)

	// 高危路由在线校验：凭据齐备才启用，否则 fail-close 占位。
	var introspect httpmw.Introspector = httpmw.Disabled{}
	if id, secret := os.Getenv("CASDOOR_CLIENT_ID"), os.Getenv("CASDOOR_CLIENT_SECRET"); id != "" && secret != "" {
		introspect = &httpmw.CasdoorIntrospector{
			Endpoint:     strings.TrimSuffix(envOr("CASDOOR_URL", issuer), "/") + "/api/login/oauth/introspect",
			ClientID:     id,
			ClientSecret: secret,
		}
	}

	// 角色回退源（P3 真 token 实测：本部署 Casdoor JWT 不嵌 roles，见 docs/design/auth.md 回退分支）。
	var roleSource authn.RoleSource
	if cu := strings.TrimSuffix(envOr("CASDOOR_URL", issuer), "/"); cu != "" {
		roleSource = authn.NewCasdoorRoleSource(cu, 5*time.Minute)
	}

	// ── 动态配置：生产走 selector+SDK；CONFIG_SOURCE=file 是显式本地/测试模式（非生产回退）。
	loaderCtx, cancelLoader := context.WithCancel(context.Background())
	switch envOr("CONFIG_SOURCE", "config_center") {
	case "file":
		dir := os.Getenv("CONFIG_DIR")
		if dir == "" {
			cancelLoader()
			return errors.New("CONFIG_SOURCE=file 需要 CONFIG_DIR")
		}
		if err := loader.RunFileDir(dir, state); err != nil {
			cancelLoader()
			return err
		}
		log.Warn("file config mode: 本地/测试专用，无热更新")
	default:
		selector := os.Getenv("CONFIG_SOURCE_FILE")
		if selector == "" {
			cancelLoader()
			return errors.New("CONFIG_SOURCE_FILE 必须指向 selector YAML（type=config_center）")
		}
		if err := loader.RunConfigCenter(loaderCtx, selector, state); err != nil {
			cancelLoader()
			return fmt.Errorf("动态配置启动加载失败（快速失败纪律）: %w", err)
		}
	}

	// ── resolver：按启动路由表的 discovery 目标建 Consul Watch。
	// 已知边界：路由热更新新增的后端服务需滚动重启才被 Watch（decisions.md 运行细节约定）。
	services := discoveryServices(state)
	var res resolver.Resolver
	var watching *resolver.Watching
	if len(services) > 0 {
		consulCfg := capi.DefaultConfig() // 读取 CONSUL_HTTP_ADDR/CONSUL_HTTP_TOKEN 标准环境变量
		if addr := os.Getenv("CONSUL_ADDR"); addr != "" && os.Getenv("CONSUL_HTTP_ADDR") == "" {
			consulCfg.Address = strings.TrimPrefix(addr, "consul://") // 兼容旧 CONSUL_ADDR 写法
		}
		client, cerr := capi.NewClient(consulCfg)
		if cerr != nil {
			cancelLoader()
			return cerr
		}
		watching = resolver.NewWatching(resolver.NewConsulLister(client), services)
		watching.OnEmptyResult = func(service string, streak int64) {
			// Consul ACL 缺 token 会 200 空列表：必须响亮告警（历史坑）。
			log.Error("consul returned empty instance list; check ACL token and registration",
				zap.String("service", service), zap.Int64("streak", streak))
		}
		res = watching
	} else {
		res = noResolver{}
	}

	// ── 请求链装配（healthz/readyz 先于包路由，见 internal/app）。
	handler := app.BuildHandler(app.Deps{
		State:      state,
		Cors:       corsSwap,
		Introspect: introspect,
		Roles:      roleSource,
		Resolver:   res,
		Errors:     gwerrors.NewWriter(),
		Log:        log,
	})

	srv := &http.Server{
		Addr:              *httpAddr,
		Handler:           h2c.NewHandler(handler, &http2.Server{}),
		ReadHeaderTimeout: 10 * time.Second,
		// WriteTimeout 保持 0：超时统一走路由级 context（流式豁免见 decisions.md）。
	}

	// 内部管理端口：pprof 默认关闭（PPROF_ENABLED=true 才监听）。
	var admin *http.Server
	if envOr("PPROF_ENABLED", "false") == "true" {
		amux := http.NewServeMux()
		amux.HandleFunc("/debug/pprof/", pprof.Index)
		amux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		amux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		amux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		admin = &http.Server{Addr: *adminAddr, Handler: amux, ReadHeaderTimeout: 5 * time.Second}
	}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Fatal("gateway server exited", zap.Error(err))
				}
			}()
			if admin != nil {
				go func() {
					if err := admin.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
						log.Error("admin server exited", zap.Error(err))
					}
				}()
			}
			log.Info("gateway listening",
				zap.String("addr", srv.Addr),
				zap.String("version", Version),
				zap.Strings("discovery_services", services),
			)
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			cancelLoader()
			err := srv.Shutdown(stopCtx)
			if admin != nil {
				_ = admin.Shutdown(stopCtx)
			}
			if watching != nil {
				watching.Close()
			}
			if oerr := otelShutdown(stopCtx); oerr != nil && err == nil {
				err = oerr
			}
			log.Info("gateway stopped")
			return err
		},
	})
	return nil
}

// discoveryServices 从启动路由表提取 discovery 目标的注册名集合。
func discoveryServices(s *loader.State) []string {
	t := s.Table()
	if t == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, svc := range t.DiscoveryTargets() {
		if _, ok := seen[svc]; ok {
			continue
		}
		seen[svc] = struct{}{}
		out = append(out, svc)
	}
	return out
}

// noResolver 用于纯 direct:// 路由表（本地测试）。
type noResolver struct{}

func (noResolver) Pick(string) (resolver.Instance, resolver.Done, error) {
	return resolver.Instance{}, nil, resolver.ErrUnknownSvc
}
func (noResolver) Ready() bool { return true }

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
