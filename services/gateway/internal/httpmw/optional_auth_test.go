package httpmw

import (
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lens077/control-tower/services/gateway/internal/authn"
	confv1 "github.com/lens077/control-tower/services/gateway/internal/conf/v1"
	"github.com/lens077/control-tower/services/gateway/internal/gwctx"
	"github.com/lens077/control-tower/services/gateway/internal/router"
	"google.golang.org/protobuf/types/known/durationpb"
)

// optionalPath 不在任何 Casbin 策略里：可选认证轨不进 RBAC，
// 用一条没有策略的路径能证明「放行不是因为角色碰巧有权限」。
const optionalPath = "/behavior.v1.BehaviorService/Track"

// withOptionalRoute 把 fixture 的路由表换成含可选认证清单的版本。
func withOptionalRoute(t *testing.T, f *fixture) {
	t.Helper()
	tbl, err := router.Build(&confv1.RouteConfig{
		Version: "v2",
		Routes: []*confv1.Route{
			{Package: "behavior", Target: "discovery:///behavior-service", Timeout: durationpb.New(time.Second)},
			{Package: "user", Target: "discovery:///user-identity", Timeout: durationpb.New(time.Second)},
		},
		OptionalAuth: []string{optionalPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.deps.Table = func() *router.Table { return tbl }
}

// mustPassAs 断言请求被放行，并且下游看到的身份是 wantUser（空串表示匿名）。
func (f *fixture) mustPassAs(t *testing.T, code int, wantUser string) {
	t.Helper()
	if code != http.StatusOK {
		t.Fatalf("可选认证路径不应拒绝请求，status=%d", code)
	}
	c := gwctx.Claims(f.captured.Context())
	switch {
	case wantUser == "" && c != nil:
		t.Fatalf("应按匿名放行，却挂上了身份 %q", c.UserID())
	case wantUser != "" && (c == nil || c.UserID() != wantUser):
		t.Fatalf("应识别为 %q，claims=%+v", wantUser, c)
	}
}

func TestOptionalAuthWithoutCredentialsIsAnonymous(t *testing.T) {
	f := newFixture(t)
	withOptionalRoute(t, f)

	rec := f.do(t, optionalPath, "", func(r *http.Request) {
		r.Header.Set("x-md-global-user-id", "forged")
	})

	f.mustPassAs(t, rec.Code, "")
	if f.captured.Header.Get("x-md-global-user-id") != "" {
		t.Fatal("伪造的身份头必须被剥掉")
	}
}

func TestOptionalAuthValidBearerIsIdentifiedWithoutRBAC(t *testing.T) {
	f := newFixture(t)
	withOptionalRoute(t, f)
	// 角色在策略里没有任何权限：可选认证轨若误走 RBAC，这里会是 403。
	tok := f.token(t, func(c *authn.Claims) { c.Roles = []authn.Role{{Name: "nobody"}} })

	rec := f.do(t, optionalPath, tok)

	f.mustPassAs(t, rec.Code, "u-alice")
}

func TestOptionalAuthInvalidBearerFallsBackToAnonymous(t *testing.T) {
	f := newFixture(t)
	withOptionalRoute(t, f)
	expired := f.token(t, func(c *authn.Claims) {
		c.IssuedAt = jwt.NewNumericDate(testNow.Add(-time.Hour))
		c.ExpiresAt = jwt.NewNumericDate(testNow.Add(-time.Minute))
	})

	rec := f.do(t, optionalPath, expired)

	f.mustPassAs(t, rec.Code, "")
}

func TestOptionalAuthValidCookieSessionIsIdentified(t *testing.T) {
	f := newFixture(t)
	withOptionalRoute(t, f)
	store := withSessions(t, f)
	liveSession(t, store, nil)

	rec := f.doSession(t, optionalPath, "sid-1", true, goodOrigin, http.MethodPost)

	f.mustPassAs(t, rec.Code, "u-alice")
}

// cookie 是环境凭据：第三方站点借用户 cookie 调 Track，只能记成匿名，不能记到该用户头上。
func TestOptionalAuthCookieFromUntrustedOriginIsAnonymous(t *testing.T) {
	f := newFixture(t)
	withOptionalRoute(t, f)
	store := withSessions(t, f)
	liveSession(t, store, nil)

	rec := f.doSession(t, optionalPath, "sid-1", true, "https://evil.example", http.MethodPost)

	f.mustPassAs(t, rec.Code, "")
}

// 会话过期或被删不能变成 401：埋点是旁路，拒绝只会丢数据。
func TestOptionalAuthUnknownSessionFallsBackToAnonymous(t *testing.T) {
	f := newFixture(t)
	withOptionalRoute(t, f)
	withSessions(t, f)

	rec := f.doSession(t, optionalPath, "sid-gone", true, goodOrigin, http.MethodPost)

	f.mustPassAs(t, rec.Code, "")
}

// 会话头（桌面端）不是环境凭据，不需要 Origin。
func TestOptionalAuthSessionHeaderIsIdentifiedWithoutOrigin(t *testing.T) {
	f := newFixture(t)
	withOptionalRoute(t, f)
	store := withSessions(t, f)
	liveSession(t, store, nil)

	rec := f.doSession(t, optionalPath, "sid-1", false, "", http.MethodPost)

	f.mustPassAs(t, rec.Code, "u-alice")
}
