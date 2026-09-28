package httpmw

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRejectAmbiguousRequestBoundaryRejectsHTTP1TransferEncoding(t *testing.T) {
	handler := RejectAmbiguousRequestBoundary(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "http://gateway.test/", nil)
	req.ProtoMajor = 1
	req.ProtoMinor = 1
	req.TransferEncoding = []string{"chunked"}
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want %d", rec.Code, http.StatusBadRequest)
	}
	if got := rec.Header().Get("Connection"); got != "close" {
		t.Fatalf("Connection=%q, want close", got)
	}
	if got := rec.Header().Get("X-Error-Reason"); got != "AMBIGUOUS_REQUEST_BOUNDARY" {
		t.Fatalf("reason=%q, want AMBIGUOUS_REQUEST_BOUNDARY", got)
	}
}

func TestRejectAmbiguousRequestBoundaryAllowsHTTP2(t *testing.T) {
	called := false
	handler := RejectAmbiguousRequestBoundary(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "https://gateway.test/", nil)
	req.ProtoMajor = 2
	req.ProtoMinor = 0
	req.TransferEncoding = nil
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent || !called {
		t.Fatalf("status=%d called=%v, want 204 and handler call", rec.Code, called)
	}
}
