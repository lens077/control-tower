package httpmw

import (
	"net/http"

	"github.com/lens077/control-tower/services/gateway/internal/gwerrors"
)

// RejectAmbiguousRequestBoundary prevents HTTP/1.1 chunked framing from crossing
// the gateway. The gateway's Connect endpoints send bounded request bodies, so
// rejecting Transfer-Encoding is safer than letting another hop reinterpret it.
// HTTP/2 has its own binary framing and is not subject to this check.
func RejectAmbiguousRequestBoundary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 1 && len(r.TransferEncoding) > 0 {
			w.Header().Set("Connection", "close")
			w.Header().Set(gwerrors.HeaderReason, "AMBIGUOUS_REQUEST_BOUNDARY")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}
