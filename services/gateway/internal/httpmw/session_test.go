package httpmw

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lens077/control-tower/services/gateway/internal/gwctx"
	"github.com/lens077/control-tower/services/gateway/internal/gwerrors"
	"github.com/lens077/control-tower/services/gateway/internal/identity"
	"github.com/lens077/control-tower/services/gateway/internal/session"
)

const (
	testCookie = "__Secure-ct_session"
	testHeader = "X-CT-Session"
	goodOrigin = "https://shop.apikv.com"
)

// withSessions 给既有 fixture 装上会话轨。
func withSessions(t *testing.T, f *fixture) session.Store {
	t.Helper()
	// Auth fixture 与 token 都钉在 testNow；Store 也必须用同一时钟。
	// 用真实 time.Now 会在 testNow + DefaultTTL().Absolute（7 天）之后把全部 fixture
	// 判成过期，形成一个与代码无关的日历炸弹。
	store := session.NewMemoryStoreWithClock(session.DefaultTTL(), func() time.Time { return testNow })
	f.deps.Sessions = store
	f.deps.SessionCookie = testCookie
	f.deps.SessionHeader = testHeader
	f.deps.OriginAllowed = func(o string) bool { return o == goodOrigin }
	return store
}

func liveSession(t *testing.T, store session.Store, roles []string) *session.Session {
	t.Helper()
	s := &session.Session{
		ID: "sid-1", Sub: "u-alice", Owner: "lens", Name: "alice", Roles: roles,
		AccessToken: "at", RefreshToken: "rt",
		AccessExpiry: testNow.Add(10 * time.Minute),
		CreatedAt:    testNow, LastSeenAt: testNow,
	}
	if err := store.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	return s
}

// doSession 发一个带会话凭据的请求。cookie=true 走 cookie 轨，否则走 header 轨。
func (f *fixture) doSession(t *testing.T, path, id string, cookie bool, origin, method string) *httptest.ResponseRecorder {
	t.Helper()
	f.captured = nil
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Content-Type", "application/json")
	if cookie {
		req.AddCookie(&http.Cookie{Name: testCookie, Value: id})
	} else {
		req.Header.Set(testHeader, id)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	Auth(f.deps)(f.next).ServeHTTP(rec, req)
	return rec
}

// cookie 轨：认证通过，角色直接来自会话（热路径不回源）。
func TestSessionCookieAuthenticates(t *testing.T) {
	f := newFixture(t)
	store := withSessions(t, f)
	liveSession(t, store, []string{"customer"})
	// 若走了回退源就说明没用会话里的角色——这里故意让它爆。
	f.deps.Roles = roleSourceFunc(func(context.Context, string, string) ([]string, error) {
		t.Fatal("session track must not hit the role source on the hot path")
		return nil, nil
	})

	rec := f.doSession(t, "/user.v1.UserService/GetProfile", "sid-1", true, goodOrigin, http.MethodPost)
	if rec.Code != 200 {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}
	c := gwctx.Claims(f.captured.Context())
	if c == nil || c.UserID() != "u-alice" || len(c.RoleNames()) != 1 {
		t.Fatalf("claims=%+v", c)
	}
	if f.captured.Header.Get(identity.HeaderUserID) != "" {
		t.Fatal("上游身份头应由 proxy 层注入，不应在此出现")
	}
}

// header 轨（桌面端）：同一套会话，不需要 Origin。
func TestSessionHeaderAuthenticates(t *testing.T) {
	f := newFixture(t)
	store := withSessions(t, f)
	liveSession(t, store, []string{"customer"})

	rec := f.doSession(t, "/user.v1.UserService/GetProfile", "sid-1", false, "", http.MethodPost)
	if rec.Code != 200 {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}
}

// CSRF：cookie 是环境凭据，状态变更请求必须带可信 Origin。
func TestCookieTrackRejectsBadOrigin(t *testing.T) {
	f := newFixture(t)
	store := withSessions(t, f)
	liveSession(t, store, []string{"customer"})

	rec := f.doSession(t, "/user.v1.UserService/GetProfile", "sid-1", true, "https://evil.example", http.MethodPost)
	if rec.Code != 403 || reason(rec) != "CSRF_ORIGIN_REJECTED" {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}
	// 缺失 Origin 同样拒绝。
	rec = f.doSession(t, "/user.v1.UserService/GetProfile", "sid-1", true, "", http.MethodPost)
	if rec.Code != 403 || reason(rec) != "CSRF_ORIGIN_REJECTED" {
		t.Fatalf("missing origin: status=%d reason=%s", rec.Code, reason(rec))
	}
}

// header 轨不是环境凭据，坏 Origin 也不该被 CSRF 拦（攻击者拿不到该头）。
func TestHeaderTrackIgnoresOrigin(t *testing.T) {
	f := newFixture(t)
	store := withSessions(t, f)
	liveSession(t, store, []string{"customer"})

	rec := f.doSession(t, "/user.v1.UserService/GetProfile", "sid-1", false, "https://evil.example", http.MethodPost)
	if rec.Code != 200 {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}
}

// 删会话 = 撤权，立刻生效。
func TestDeletedSessionRejected(t *testing.T) {
	f := newFixture(t)
	store := withSessions(t, f)
	liveSession(t, store, []string{"customer"})
	if err := store.Delete(context.Background(), "sid-1"); err != nil {
		t.Fatal(err)
	}
	rec := f.doSession(t, "/user.v1.UserService/GetProfile", "sid-1", true, goodOrigin, http.MethodPost)
	if rec.Code != 401 || reason(rec) != "SESSION_INVALID" {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}
}

type refresherFunc func(ctx context.Context, rt string) (string, string, time.Time, error)

func (fn refresherFunc) Refresh(ctx context.Context, rt string) (string, string, time.Time, error) {
	return fn(ctx, rt)
}

// 临近过期 → 服务端续期，前端全程无感。
func TestServerSideRefresh(t *testing.T) {
	f := newFixture(t)
	store := withSessions(t, f)
	s := liveSession(t, store, []string{"customer"})
	s.AccessExpiry = testNow.Add(10 * time.Second) // 进入提前续期窗
	if err := store.Save(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	called := false
	f.deps.Refresher = refresherFunc(func(context.Context, string) (string, string, time.Time, error) {
		called = true
		return "new-at", "new-rt", testNow.Add(15 * time.Minute), nil
	})

	rec := f.doSession(t, "/user.v1.UserService/GetProfile", "sid-1", true, goodOrigin, http.MethodPost)
	if rec.Code != 200 || !called {
		t.Fatalf("status=%d refreshed=%v", rec.Code, called)
	}
	got, err := store.Get(context.Background(), "sid-1")
	if err != nil || got.AccessToken != "new-at" {
		t.Fatalf("session not updated: %+v err=%v", got, err)
	}
}

// 续期被 IdP 拒（账户禁用）→ 删会话 + 401，等于登出。
func TestRefreshRejectionRevokesSession(t *testing.T) {
	f := newFixture(t)
	store := withSessions(t, f)
	s := liveSession(t, store, []string{"customer"})
	s.AccessExpiry = testNow.Add(-time.Minute) // 已过期
	if err := store.Save(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	f.deps.Refresher = refresherFunc(func(context.Context, string) (string, string, time.Time, error) {
		return "", "", time.Time{}, errors.New("account disabled")
	})

	rec := f.doSession(t, "/user.v1.UserService/GetProfile", "sid-1", true, goodOrigin, http.MethodPost)
	if rec.Code != 401 || reason(rec) != "SESSION_REVOKED" {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}
	if _, err := store.Get(context.Background(), "sid-1"); err != session.ErrNotFound {
		t.Fatal("被 IdP 拒的会话必须删除")
	}
}

// 三轨共存：装了会话轨但请求只带 bearer 时，legacy 轨仍然工作（P1 零影响的核心保证）。
func TestLegacyBearerStillWorksAlongsideSessions(t *testing.T) {
	f := newFixture(t)
	withSessions(t, f)

	rec := f.do(t, "/user.v1.UserService/GetProfile", f.token(t))
	if rec.Code != 200 {
		t.Fatalf("legacy bearer must keep working: status=%d reason=%s", rec.Code, reason(rec))
	}
}

// 无任何凭据 → 401，且提示已覆盖两种形态。
func TestNoCredentials(t *testing.T) {
	f := newFixture(t)
	withSessions(t, f)
	rec := f.do(t, "/user.v1.UserService/GetProfile", "")
	if rec.Code != 401 || reason(rec) != "JWT_MISSING" {
		t.Fatalf("status=%d reason=%s", rec.Code, reason(rec))
	}
	_ = gwerrors.HeaderReason
}
