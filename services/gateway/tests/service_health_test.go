package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	capi "github.com/hashicorp/consul/api"
	"github.com/lens077/control-tower/services/gateway/internal/app"
	"github.com/lens077/control-tower/services/gateway/internal/authn"
	"github.com/lens077/control-tower/services/gateway/internal/loader"
	"github.com/lens077/control-tower/services/gateway/internal/resolver"
	"github.com/lens077/control-tower/services/gateway/internal/session"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// JSON shape is asserted through the public endpoint, not the checker's implementation.
type healthSnapshot struct {
	CheckedAt string `json:"checked_at"`
	Services  []struct {
		Name      string `json:"name"`
		Status    string `json:"status"`
		LatencyMS *int64 `json:"latency_ms"`
		CheckedAt string `json:"checked_at"`
		Reason    string `json:"reason"`
	} `json:"services"`
}

func setHealthRoutes(t *testing.T, e *env, targets map[string]string) {
	t.Helper()
	cfg := "version: v2\nroutes:\n"
	for name, target := range targets {
		cfg += fmt.Sprintf("  - package: %s\n    target: %s\n    timeout: 2s\n", name, target)
	}
	cfg += "cors:\n  allow_origins: [\"http://localhost:3000\"]\n  allow_methods: [GET, POST, OPTIONS]\n"
	if err := e.state.Apply(loader.KeyRoutes, []byte(cfg)); err != nil {
		t.Fatal(err)
	}
}

func getHealth(t *testing.T, e *env) (healthSnapshot, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.gw.URL+"/admin/health/services", nil)
	req.Header.Set("Authorization", "Bearer "+e.token(t, func(c *authn.Claims) { c.Roles = []authn.Role{{Name: "admin"}} }))
	resp, err := e.gw.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("snapshot request=%d body=%s", resp.StatusCode, body)
	}
	var result healthSnapshot
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	return result, string(body)
}

func TestServiceHealthPartialFailureDoesNotLeakDetails(t *testing.T) {
	e := setup(t)
	targets := map[string]string{}
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"order", 503, `{"healthy":false,"details":{"database":"secret-database.internal:5432 password=private"}}`},
		{"product", 200, `{"healthy":true}`},
		{"search", 200, `{"unexpected":"do not treat an arbitrary 200 as healthy"}`},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/healthz" {
				t.Errorf("unexpected path %q", r.URL.Path)
			}
			if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
				t.Error("probe forwarded user credentials")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		}))
		t.Cleanup(srv.Close)
		targets[tc.name] = "direct://" + strings.TrimPrefix(srv.URL, "http://")
	}
	setHealthRoutes(t, e, targets)
	got, body := getHealth(t, e)
	if len(got.Services) != 3 {
		t.Fatalf("want 3 services: %s", body)
	}
	for i, want := range []string{"degraded", "healthy", "unknown"} {
		if got.Services[i].Status != want {
			t.Errorf("%s status=%s want=%s", got.Services[i].Name, got.Services[i].Status, want)
		}
	}
	if got.Services[0].Reason != "dependency_unhealthy" || got.Services[2].Reason != "invalid_response" {
		t.Errorf("unexpected reasons: %s", body)
	}
	if strings.Contains(body, "secret-database") || strings.Contains(body, "password") || strings.Contains(body, "http://") {
		t.Error("raw probe error leaked")
	}
}

func TestServiceHealthSnapshotCacheInvalidatesOnRouteUpdate(t *testing.T) {
	e := setup(t)
	var unhealthy atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"healthy":%t}`, !unhealthy.Load())
	}))
	defer srv.Close()
	target := "direct://" + strings.TrimPrefix(srv.URL, "http://")
	setHealthRoutes(t, e, map[string]string{"behavior": target, "telemetry": target})
	first, _ := getHealth(t, e)
	if len(first.Services) != 1 || first.Services[0].Name != "behavior" {
		t.Fatalf("shared behavior target must not be probed twice: %+v", first)
	}
	unhealthy.Store(true)
	cached, _ := getHealth(t, e)
	if cached.CheckedAt != first.CheckedAt || cached.Services[0].Status != "healthy" {
		t.Fatal("rapid refresh should reuse the same timestamped snapshot")
	}
	setHealthRoutes(t, e, map[string]string{"order": target})
	updated, _ := getHealth(t, e)
	if len(updated.Services) != 1 || updated.Services[0].Name != "order" || updated.Services[0].Status != "degraded" {
		t.Fatalf("route reload did not invalidate snapshot: %+v", updated)
	}
}

func TestServiceHealthSessionRevocationAndH2C(t *testing.T) {
	store := session.NewMemoryStore(session.DefaultTTL())
	now := time.Now()
	if err := store.Create(t.Context(), &session.Session{
		ID: "monitor-admin", Sub: "operator", Owner: "lens", Name: "operator", Roles: []string{"admin"},
		AccessToken: "private-access-token", AccessExpiry: now.Add(time.Hour), CreatedAt: now, LastSeenAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	e := setup(t, func(d *app.Deps) {
		d.Sessions = store
		d.SessionCookie = "ct-session"
		d.SessionHeader = "X-CT-Session"
		d.Transport = nil
	})
	srv := httptest.NewServer(h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Error("production health probes must use h2c")
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("session credentials reached backend")
		}
		_, _ = io.WriteString(w, `{"healthy":true}`)
	}), &http2.Server{}))
	defer srv.Close()
	setHealthRoutes(t, e, map[string]string{"order": "direct://" + strings.TrimPrefix(srv.URL, "http://")})
	request := func(method, path string, want int) {
		t.Helper()
		req, _ := http.NewRequest(method, e.gw.URL+path, nil)
		req.AddCookie(&http.Cookie{Name: "ct-session", Value: "monitor-admin"})
		resp, err := e.gw.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s status=%d want=%d body=%s", method, path, resp.StatusCode, want, body)
		}
		if want == 200 && !strings.Contains(string(body), `"status":"healthy"`) {
			t.Errorf("h2c probe not healthy: %s", body)
		}
	}
	request("GET", "/admin/health/services", 200)
	request("POST", "/admin/health/services", 405)
	request("GET", "/admin/health/services?target=arbitrary", 400)
	request("GET", "/admin/health%2fservices", 404)
	if err := store.Delete(t.Context(), "monitor-admin"); err != nil {
		t.Fatal(err)
	}
	request("GET", "/admin/health/services", 401)
}

func TestServiceHealthProbeBoundsAndRedirects(t *testing.T) {
	e := setup(t)
	var redirected atomic.Int32
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected.Add(1)
		_, _ = io.WriteString(w, `{"healthy":true}`)
	}))
	defer trap.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, trap.URL, 302) }))
	defer redirect.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	oversized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"healthy":true,"padding":"`+strings.Repeat("x", 70<<10)+`"}`)
	}))
	defer oversized.Close()
	setHealthRoutes(t, e, map[string]string{
		"order":   "direct://" + strings.TrimPrefix(redirect.URL, "http://"),
		"payment": "direct://" + strings.TrimPrefix(slow.URL, "http://"),
		"search":  "direct://" + strings.TrimPrefix(oversized.URL, "http://"),
		"user":    "discovery:///unregistered-service",
	})
	start := time.Now()
	got, body := getHealth(t, e)
	if time.Since(start) > 4*time.Second {
		t.Error("single slow probe blocked the snapshot")
	}
	if len(got.Services) != 4 {
		t.Fatalf("missing services: %s", body)
	}
	for i, reason := range []string{"probe_failed", "timeout", "invalid_response", "no_instance"} {
		if got.Services[i].Reason != reason {
			t.Errorf("%s reason=%s want=%s", got.Services[i].Name, got.Services[i].Reason, reason)
		}
	}
	if redirected.Load() != 0 {
		t.Error("health followed redirect to unapproved host")
	}
	if got.Services[3].LatencyMS != nil {
		t.Error("no-instance is not a zero-ms successful probe")
	}
	if strings.Contains(body, "unregistered-service") || strings.Contains(body, "127.0.0.1") {
		t.Error("raw discovery/transport error leaked")
	}
}

func TestServiceHealthCallerCancellationReleasesProbe(t *testing.T) {
	e := setup(t)
	started, canceled := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(canceled)
	}))
	defer srv.Close()
	setHealthRoutes(t, e, map[string]string{"order": "direct://" + strings.TrimPrefix(srv.URL, "http://")})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.gw.URL+"/admin/health/services", nil)
	req.Header.Set("Authorization", "Bearer "+e.token(t, func(c *authn.Claims) { c.Roles = []authn.Role{{Name: "admin"}} }))
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		resp, err := e.gw.Client().Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("probe did not start")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("browser cancellation did not cancel backend probe")
	}
	<-finished
}

func TestServiceHealthConcurrentReadersShareBoundedProbes(t *testing.T) {
	e := setup(t)
	var calls, active, peak atomic.Int32
	fourStarted, release := make(chan struct{}), make(chan struct{})
	var startedOnce, releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); old < n && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		if n >= 4 {
			startedOnce.Do(func() { close(fourStarted) })
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, `{"healthy":true}`)
	}))
	t.Cleanup(srv.Close)
	targets := map[string]string{}
	for _, name := range []string{"user", "search", "order", "payment", "cart", "product"} {
		targets[name] = "direct://" + strings.TrimPrefix(srv.URL, "http://")
	}
	setHealthRoutes(t, e, targets)
	token := e.token(t, func(c *authn.Claims) { c.Roles = []authn.Role{{Name: "admin"}} })
	done := make(chan string, 8)
	for range 8 {
		go func() {
			req, _ := http.NewRequest("GET", e.gw.URL+"/admin/health/services", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := e.gw.Client().Do(req)
			if err != nil {
				done <- err.Error()
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				done <- fmt.Sprintf("status=%d", resp.StatusCode)
				return
			}
			var result healthSnapshot
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				done <- err.Error()
				return
			}
			done <- result.CheckedAt
		}()
	}
	select {
	case <-fourStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("probes did not start concurrently")
	}
	releaseOnce.Do(func() { close(release) })
	first := <-done
	if _, err := time.Parse(time.RFC3339Nano, first); err != nil {
		t.Fatalf("first response: %s", first)
	}
	for range 7 {
		if next := <-done; next != first {
			t.Errorf("concurrent readers got different snapshots: %s / %s", first, next)
		}
	}
	if calls.Load() != 6 || peak.Load() > 4 {
		t.Fatalf("admin polling amplified backend traffic: probes=%d peak=%d", calls.Load(), peak.Load())
	}
}

func TestServiceHealthUsesRealDiscoverySnapshot(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("probe path=%s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"healthy":true}`)
	}))
	t.Cleanup(backend.Close)
	addr := backend.Listener.Addr().(*net.TCPAddr)
	directory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("index") == "1" {
			<-r.Context().Done()
			return
		}
		w.Header().Set("X-Consul-Index", "1")
		_ = json.NewEncoder(w).Encode([]map[string]any{{"Service": map[string]any{"ID": "order-one", "Address": "127.0.0.1", "Port": addr.Port}}})
	}))
	t.Cleanup(directory.Close)
	client, err := capi.NewClient(&capi.Config{Address: directory.URL})
	if err != nil {
		t.Fatal(err)
	}
	watching := resolver.NewWatching(resolver.NewConsulLister(client), []string{"order-service"})
	t.Cleanup(watching.Close)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(2 * time.Second)
	for !watching.Ready() {
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatal("real Resolver did not load directory snapshot")
		}
	}
	e := setup(t, func(d *app.Deps) { d.Resolver = watching })
	setHealthRoutes(t, e, map[string]string{"order": "discovery:///order-service"})
	got, body := getHealth(t, e)
	if len(got.Services) != 1 || got.Services[0].Status != "healthy" {
		t.Fatalf("discovery health=%s", body)
	}
	if strings.Contains(body, "order-service") || strings.Contains(body, backend.URL) {
		t.Error("discovery address leaked")
	}
}

func TestServiceHealthTotalDeadlineDoesNotCacheIncompleteSnapshot(t *testing.T) {
	e := setup(t)
	var restored atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !restored.Load() {
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, `{"healthy":true}`)
	}))
	defer srv.Close()
	targets := map[string]string{}
	for i := 0; i < 10; i++ {
		targets[fmt.Sprintf("svc%d", i)] = "direct://" + strings.TrimPrefix(srv.URL, "http://")
	}
	setHealthRoutes(t, e, targets)
	req, _ := http.NewRequest("GET", e.gw.URL+"/admin/health/services", nil)
	req.Header.Set("Authorization", "Bearer "+e.token(t, func(c *authn.Claims) { c.Roles = []authn.Role{{Name: "admin"}} }))
	start := time.Now()
	resp, err := e.gw.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 503 || time.Since(start) > 6500*time.Millisecond {
		t.Fatalf("total deadline not enforced: status=%d time=%s", resp.StatusCode, time.Since(start))
	}
	restored.Store(true)
	fresh, body := getHealth(t, e)
	for _, svc := range fresh.Services {
		if svc.Status != "healthy" {
			t.Fatalf("incomplete snapshot reused after recovery: %s", body)
		}
	}
}

func TestServiceHealthRequiresVerifiedAdmin(t *testing.T) {
	e := setup(t)
	for _, tc := range []struct {
		name  string
		roles []authn.Role
		token bool
		want  int
	}{
		{"anonymous forged admin header", nil, false, http.StatusUnauthorized},
		{"customer", []authn.Role{{Name: "customer"}}, true, http.StatusForbidden},
		{"merchant", []authn.Role{{Name: "merchant"}}, true, http.StatusForbidden},
		{"admin", []authn.Role{{Name: "admin"}}, true, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, e.gw.URL+"/admin/health/services", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("x-md-global-role", "admin")
			if tc.token {
				req.Header.Set("Authorization", "Bearer "+e.token(t, func(c *authn.Claims) { c.Roles = tc.roles }))
			}
			resp, err := e.gw.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.want {
				t.Fatalf("status=%d want=%d body=%s", resp.StatusCode, tc.want, body)
			}
			if resp.Header.Get("Cache-Control") != "no-store" {
				t.Error("admin health must never enter browser caches")
			}
			if tc.want != http.StatusOK && strings.Contains(string(body), "\"services\"") {
				t.Error("denied request leaked snapshot")
			}
		})
	}
}
