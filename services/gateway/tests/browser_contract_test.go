package tests

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lens077/control-tower/services/gateway/internal/app"
	"github.com/lens077/control-tower/services/gateway/internal/bff"
	"github.com/lens077/control-tower/services/gateway/internal/session"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// 跨仓可选契约验收：真实 Firefox → admin Vite 同源代理 → 网关/BFF → h2c 探针。
// 不操作线上数据，不 mock 浏览器健康响应。普通 Go 验证不依赖 sibling Node 工具链。
func TestServiceHealthFirefoxContract(t *testing.T) {
	ecommerce := os.Getenv("E2E_ECOMMERCE_DIR")
	if ecommerce == "" {
		t.Skip("set E2E_ECOMMERCE_DIR to run the cross-repository Firefox contract test")
	}
	root, err := filepath.Abs(ecommerce)
	if err != nil {
		t.Fatal(err)
	}
	store := session.NewMemoryStore(session.DefaultTTL())
	now := time.Now()
	sid, err := session.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(t.Context(), &session.Session{
		ID: sid, Sub: "test-operator", Owner: "lens", Name: "test-operator", Roles: []string{"admin"},
		AccessToken: "local-test-only", AccessExpiry: now.Add(time.Hour), CreatedAt: now, LastSeenAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	e := setup(t, func(d *app.Deps) {
		d.Transport = nil
		d.Sessions = store
		d.SessionCookie = "ct-test-session"
		d.BFF = &bff.Handler{Store: store, Cookie: bff.CookieConfig{Name: d.SessionCookie, Path: "/", SameSite: http.SameSiteLaxMode}, Log: zap.NewNop()}
	})
	targets := map[string]string{}
	for _, svc := range []struct {
		name, body string
		status     int
	}{
		{"order", `{"healthy":true}`, 200},
		{"payment", `{"healthy":false,"details":{"database":"private dependency failure"}}`, 503},
	} {
		srv := httptest.NewServer(h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ProtoMajor != 2 || r.URL.Path != "/healthz" {
				t.Error("wrong real probe protocol/path")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(svc.status)
			_, _ = io.WriteString(w, svc.body)
		}), &http2.Server{}))
		t.Cleanup(srv.Close)
		targets[svc.name] = "direct://" + strings.TrimPrefix(srv.URL, "http://")
	}
	setHealthRoutes(t, e, targets)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "e2e/monitor.gateway.mjs")
	// 给 Node 的 finally 留时间回收它自己的 Firefox 与 Vite 进程组。
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	cmd.Dir = filepath.Join(root, "frontend")
	cmd.Env = append(os.Environ(), "HEALTH_E2E_GATEWAY="+e.gw.URL, "HEALTH_E2E_SESSION="+sid)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Firefox contract failed: %v\n%s", err, output)
	}
	t.Log(string(output))
}
