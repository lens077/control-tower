package httpmw

import (
	"net/http"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/lens077/control-tower/services/gateway/internal/gwctx"
	"github.com/lens077/control-tower/services/gateway/internal/identity"
)

// AdminRead 保护网关固定的只读诊断端点，复用业务认证但不放宽 POST-only RPC 策略。
// 只能在 app 的精确本地路径上挂载，不能用于反向代理或可写接口。
func AdminRead(d AuthDeps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			identity.Strip(r.Header)
			if r.URL.RawPath != "" && r.URL.RawPath != r.URL.Path {
				d.Errors.Write(w, r, connect.CodeNotFound, "PATH_ESCAPED", "escaped path segments are not accepted")
				return
			}
			now := time.Now()
			if d.Now != nil {
				now = d.Now()
			}
			res, ok := d.authenticate(w, r, r.Context(), now)
			if !ok {
				return
			}
			if !slices.Contains(res.claims.RoleNames(), "admin") {
				d.Errors.Write(w, r, connect.CodePermissionDenied, "ADMIN_REQUIRED", "administrator role required")
				return
			}
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", http.MethodGet)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusMethodNotAllowed)
				_, _ = w.Write([]byte(`{"code":"unimplemented","message":"only GET is supported"}`))
				return
			}
			if r.URL.RawQuery != "" {
				d.Errors.Write(w, r, connect.CodeInvalidArgument, "QUERY_NOT_ALLOWED", "this endpoint accepts no query parameters")
				return
			}
			next.ServeHTTP(w, r.WithContext(gwctx.WithClaims(r.Context(), res.claims)))
		})
	}
}
