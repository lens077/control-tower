package httpmw

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	confv1 "github.com/lens077/control-tower/services/gateway/internal/conf/v1"
	"github.com/lens077/control-tower/services/gateway/internal/guest"
	"github.com/lens077/control-tower/services/gateway/internal/gwctx"
	"github.com/lens077/control-tower/services/gateway/internal/gwerrors"
	"github.com/lens077/control-tower/services/gateway/internal/router"

	"google.golang.org/protobuf/types/known/durationpb"
)

const guestPath = "/cart.v1.CartService/GetCart"

// 没有 cookie 时：网关签发一枚新访客身份并下发。
func TestGuestTrack_IssuesIdentityWhenAbsent(t *testing.T) {
	var captured *http.Request
	h, gc := guestPipeline(t, &captured)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, guestPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	gid := gwctx.GuestID(captured.Context())
	if gid == "" {
		t.Fatal("访客轨必须给出一个身份")
	}
	var issued *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == gc.Name {
			issued = c
		}
	}
	if issued == nil || issued.Value == gid {
		t.Fatalf("必须下发已签名的访客 cookie，得到 %+v", issued)
	}
}

// 安全回归：伪造的访客 cookie 不得成为下游身份。
//
// 攻击面（2026-09-08 审计）：访客 ID 与真实用户 ID 同为 UUID 且同写进
// cart_item.user_id。若网关原样采纳 cookie 值，把它改成受害者的用户 UUID
// 就能读写受害者的购物车。现在未签名/签名不符的值一律当作「没有身份」，
// 网关改为签发一枚全新的访客 ID。
func TestGuestTrack_RejectsForgedCookie(t *testing.T) {
	const victimUserID = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"

	for _, forged := range []string{victimUserID, "not-a-uuid-at-all", victimUserID + ".YWJj"} {
		t.Run(forged, func(t *testing.T) {
			var captured *http.Request
			h, gc := guestPipeline(t, &captured)

			req := httptest.NewRequest(http.MethodPost, guestPath, nil)
			req.AddCookie(&http.Cookie{Name: gc.Name, Value: forged})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			gid := gwctx.GuestID(captured.Context())
			if gid == victimUserID {
				t.Fatal("伪造值被当成了下游身份——横向越权")
			}
			if gid == forged {
				t.Fatalf("未经签名验证的值被采纳: %q", gid)
			}
			if gid == "" {
				t.Fatal("应当签发一枚全新访客身份，而不是没有身份")
			}
		})
	}
}

// 网关自己签发的 cookie 必须能读回同一个 id，否则每请求都换身份、购物车恒空。
func TestGuestTrack_AcceptsOwnCookie(t *testing.T) {
	var captured *http.Request
	h, gc := guestPipeline(t, &captured)

	// 第一次请求拿到签发的 cookie。
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, guestPath, nil))
	first := gwctx.GuestID(captured.Context())
	var issued *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == gc.Name {
			issued = c
		}
	}
	if issued == nil {
		t.Fatal("首次请求未下发访客 cookie")
	}

	// 第二次带上它，身份必须保持一致。
	req := httptest.NewRequest(http.MethodPost, guestPath, nil)
	req.AddCookie(issued)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)

	if got := gwctx.GuestID(captured.Context()); got != first {
		t.Fatalf("访客身份不稳定: 首次 %q，再次 %q", first, got)
	}
}

// guestPipeline 返回装好访客轨的处理器，并把穿过流水线的请求写回 out。
func guestPipeline(t *testing.T, out **http.Request) (http.Handler, guest.CookieConfig) {
	t.Helper()
	tbl, err := router.Build(&confv1.RouteConfig{
		Version: "v2",
		Routes: []*confv1.Route{
			{Package: "cart", Target: "discovery:///cart-service", Timeout: durationpb.New(time.Second)},
		},
		Guest: []string{guestPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	gc := guest.CookieConfig{Name: "ct_guest", Path: "/", Key: []byte("unit-test-signing-key")}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*out = r
		w.WriteHeader(http.StatusOK)
	})
	return Auth(AuthDeps{
		Table:       func() *router.Table { return tbl },
		Errors:      gwerrors.NewWriter(),
		GuestCookie: &gc,
		Now:         func() time.Time { return testNow },
	})(next), gc
}
