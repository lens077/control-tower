package gwerrors

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
)

// Connect unary POST：走 connect.ErrorWriter，响应体必须是规范错误 JSON 且 details 非空。
func TestWriteConnectUnary(t *testing.T) {
	w := NewWriter()
	req := httptest.NewRequest(http.MethodPost, "/order.v1.OrderService/CreateOrder", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	w.Write(rec, req, connect.CodeNotFound, "ROUTE_NOT_FOUND", "no route for package order")

	if rec.Header().Get(HeaderReason) != "ROUTE_NOT_FOUND" {
		t.Fatalf("missing %s header", HeaderReason)
	}
	if rec.Code != 404 {
		t.Fatalf("status=%d want 404", rec.Code)
	}
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details []struct {
			Type string `json:"type"`
		} `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not json: %v: %s", err, rec.Body.String())
	}
	if body.Code != "not_found" {
		t.Fatalf("code=%q want not_found", body.Code)
	}
	if len(body.Details) == 0 || body.Details[0].Type == "" {
		t.Fatalf("details must be non-empty with valid type: %s", rec.Body.String())
	}
}

// 非 RPC 请求（浏览器 GET）：回退 JSON，仍是 Connect 风格错误体 + 正确状态码。
func TestWriteFallback(t *testing.T) {
	w := NewWriter()
	req := httptest.NewRequest(http.MethodGet, "/nonsense", nil)
	rec := httptest.NewRecorder()

	w.Write(rec, req, connect.CodeUnauthenticated, "JWT_MISSING", "missing bearer token")

	if rec.Code != 401 {
		t.Fatalf("status=%d want 401", rec.Code)
	}
	var body struct {
		Code    string `json:"code"`
		Details []struct {
			Type string `json:"type"`
		} `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("fallback body not json: %v", err)
	}
	if body.Code != "unauthenticated" || len(body.Details) == 0 {
		t.Fatalf("unexpected fallback body: %s", rec.Body.String())
	}
	if body.Details[0].Type != "google.rpc.ErrorInfo" {
		t.Fatalf("detail type=%q", body.Details[0].Type)
	}
}

func TestHTTPStatusMapping(t *testing.T) {
	cases := map[connect.Code]int{
		connect.CodeNotFound:          404,
		connect.CodeUnauthenticated:   401,
		connect.CodePermissionDenied:  403,
		connect.CodeUnavailable:       503,
		connect.CodeDeadlineExceeded:  504,
		connect.CodeResourceExhausted: 429,
	}
	for code, want := range cases {
		if got := httpStatus(code); got != want {
			t.Errorf("httpStatus(%v)=%d want %d", code, got, want)
		}
	}
}
