package httpmw

import (
	"net/http"
	"runtime/debug"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	connectcors "connectrpc.com/cors"
	confv1 "github.com/lens077/control-tower/services/gateway/internal/conf/v1"
	"github.com/lens077/control-tower/services/gateway/internal/gwctx"
	"github.com/lens077/control-tower/services/gateway/internal/gwerrors"

	"github.com/rs/cors"
	"go.uber.org/zap"
)

// Recover 兜底 panic：记栈、回 500。
func Recover(log *zap.Logger, ew *gwerrors.Writer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic in gateway pipeline",
						zap.Any("panic", rec),
						zap.String("path", r.URL.Path),
						zap.ByteString("stack", debug.Stack()),
					)
					ew.Write(w, r, connect.CodeInternal, "GATEWAY_PANIC", "internal gateway error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder 捕获状态码与字节数供访问日志使用。
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

// Flush 透传（流式响应依赖）。
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// AccessLog 记录结构化访问日志；5xx 一律按错误级别记（旧网关把 5xx 记成功的缺陷不保留）。
func AccessLog(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ctx, up := gwctx.WithUpstream(r.Context())
			rec := &statusRecorder{ResponseWriter: w}

			next.ServeHTTP(rec, r.WithContext(ctx))

			fields := []zap.Field{
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", rec.status),
				zap.Int64("bytes", rec.bytes),
				zap.Duration("duration", time.Since(start)),
				zap.String("upstream", up.Addr),
			}
			if reason := rec.Header().Get(gwerrors.HeaderReason); reason != "" {
				fields = append(fields, zap.String("reason", reason))
			}
			if rec.status >= 500 {
				log.Error("access", fields...)
				return
			}
			log.Info("access", fields...)
		})
	}
}

// CorsSwapper 支持 CORS 策略热切换（策略在 routes.yaml 里，随路由表一起下发）。
type CorsSwapper struct {
	c atomic.Pointer[cors.Cors]
}

// NewCorsSwapper 构造空的切换器；策略到位前 CORS 中间件直通。
func NewCorsSwapper() *CorsSwapper {
	return &CorsSwapper{}
}

// ValidateCors 做 CORS 配置的纯校验（供加载器在整份配置生效前预检，保证原子性）。
func ValidateCors(cfg *confv1.Cors) error {
	if cfg.GetAllowCredentials() {
		for _, o := range cfg.GetAllowOrigins() {
			if o == "*" {
				return errWildcardWithCredentials
			}
		}
	}
	return nil
}

// Update 应用新的 CORS 配置。allow_credentials=true 且 origins 含 "*" 视为配置错误。
func (s *CorsSwapper) Update(cfg *confv1.Cors) error {
	if err := ValidateCors(cfg); err != nil {
		return err
	}
	// Connect 协议要求的响应头暴露清单来自 connectrpc.com/cors。
	c := cors.New(cors.Options{
		AllowedOrigins:   cfg.GetAllowOrigins(),
		AllowedMethods:   cfg.GetAllowMethods(),
		AllowedHeaders:   cfg.GetAllowHeaders(),
		ExposedHeaders:   append(connectcors.ExposedHeaders(), gwerrors.HeaderReason),
		AllowCredentials: cfg.GetAllowCredentials(),
		MaxAge:           7200,
		// 浏览器 Private Network Access 预检（内网站点访问，沿旧网关行为）。
		AllowPrivateNetwork: true,
	})
	s.c.Store(c)
	return nil
}

type corsConfigError string

func (e corsConfigError) Error() string { return string(e) }

const errWildcardWithCredentials = corsConfigError("httpmw: allow_credentials=true 时 allow_origins 禁止通配符")

// Middleware 返回按当前策略工作的 CORS 中间件；策略未加载时直通。
func (s *CorsSwapper) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := s.c.Load()
			if c == nil {
				next.ServeHTTP(w, r)
				return
			}
			c.Handler(next).ServeHTTP(w, r)
		})
	}
}

// Chain 按序组合中间件（第一个参数最外层）。
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
