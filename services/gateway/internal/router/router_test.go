package router

import (
	"strings"
	"testing"
	"time"

	confv1 "github.com/lens077/control-tower/services/gateway/internal/conf/v1"

	"google.golang.org/protobuf/types/known/durationpb"
)

func testConfig() *confv1.RouteConfig {
	return &confv1.RouteConfig{
		Version: "v2",
		Routes: []*confv1.Route{
			{Package: "user", Target: "discovery:///user-identity", Timeout: durationpb.New(4 * time.Second)},
			{Package: "telemetry", Target: "discovery:///behavior-service", Timeout: durationpb.New(2 * time.Second)},
		},
		Anonymous: []string{"/user.v1.UserService/SignIn"},
		Auth: &confv1.Auth{
			OnlineCheckProcedures: []string{"/payment.v1.PaymentService/Refund"},
		},
	}
}

func TestResolve(t *testing.T) {
	tbl, err := Build(testConfig())
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		path    string
		wantPkg string
		wantOK  bool
	}{
		{"/user.v1.UserService/SignIn", "user", true},
		{"/user.v1.UserService/GetProfile", "user", true},
		{"/telemetry.v1.TelemetryService/CollectWebVitals", "telemetry", true},
		// 首段无「.」：不是 Connect 路由，不得误命中 user。
		{"/username/x", "", false},
		{"/user/x", "", false},
		{"/user", "", false},
		// 未知包。
		{"/order.v1.OrderService/CreateOrder", "", false},
		// 以「.」开头的畸形首段。
		{"/.v1.Svc/M", "", false},
		// 空路径与根路径。
		{"", "", false},
		{"/", "", false},
	}
	for _, c := range cases {
		r, ok := tbl.Resolve(c.path)
		if ok != c.wantOK {
			t.Errorf("Resolve(%q) ok=%v want %v", c.path, ok, c.wantOK)
			continue
		}
		if ok && r.Package != c.wantPkg {
			t.Errorf("Resolve(%q) pkg=%q want %q", c.path, r.Package, c.wantPkg)
		}
	}
}

func TestResolveTimeout(t *testing.T) {
	tbl, err := Build(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	r, ok := tbl.Resolve("/user.v1.UserService/SignIn")
	if !ok || r.Timeout != 4*time.Second {
		t.Fatalf("timeout=%v ok=%v", r.Timeout, ok)
	}
}

func TestOverlongPathRejected(t *testing.T) {
	tbl, err := Build(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	long := "/user.v1.UserService/" + strings.Repeat("a", MaxPathLen)
	if _, ok := tbl.Resolve(long); ok {
		t.Fatal("overlong path must miss")
	}
}

func TestAnonymousAndOnline(t *testing.T) {
	tbl, err := Build(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !tbl.IsAnonymous("/user.v1.UserService/SignIn") {
		t.Fatal("SignIn should be anonymous")
	}
	if tbl.IsAnonymous("/user.v1.UserService/GetProfile") {
		t.Fatal("GetProfile should not be anonymous")
	}
	if !tbl.NeedsOnlineCheck("/payment.v1.PaymentService/Refund") {
		t.Fatal("Refund should need online check")
	}
}

func TestDuplicatePackageRejected(t *testing.T) {
	cfg := testConfig()
	cfg.Routes = append(cfg.Routes, &confv1.Route{
		Package: "user", Target: "discovery:///other", Timeout: durationpb.New(time.Second),
	})
	if _, err := Build(cfg); err == nil {
		t.Fatal("duplicate package must fail Build")
	}
}
