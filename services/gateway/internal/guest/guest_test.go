package guest

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 访客 id 必须每次不同且足够长——它是身份凭据，可预测即可冒用他人购物车。
func TestNewID_UniqueAndLongEnough(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		id, err := NewID()
		if err != nil {
			t.Fatalf("NewID: %v", err)
		}
		// 32 字节 base64url 无填充 = 43 字符
		if len(id) < 43 {
			t.Fatalf("id 太短(%d)，随机性不足以抗猜测: %q", len(id), id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("NewID 产生重复值 %q——CSPRNG 装配错误", id)
		}
		seen[id] = struct{}{}
	}
}

// Issue 写出的 cookie 必须 HttpOnly：访客 id 是凭据，JS 没有任何理由读它。
func TestIssue_IsHttpOnly(t *testing.T) {
	c := DefaultCookieConfig()
	w := httptest.NewRecorder()
	c.Issue(w, "abc")

	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("期望 1 个 cookie，得到 %d", len(cookies))
	}
	got := cookies[0]
	if !got.HttpOnly {
		t.Error("访客 cookie 必须 HttpOnly——否则 XSS 可直接窃取身份")
	}
	if got.Value != "abc" {
		t.Errorf("cookie 值 = %q, 期望 abc", got.Value)
	}
	if got.MaxAge <= 0 {
		t.Errorf("MaxAge 应为正数(持久 cookie)，得到 %d", got.MaxAge)
	}
}

// 读回自己写出的 cookie —— 发与读必须对称，否则每次请求都会签发新身份，
// 购物车永远是空的（这类 bug 不会报错，只会「功能看起来没生效」）。
func TestIssueThenFromRequest_RoundTrip(t *testing.T) {
	c := DefaultCookieConfig()
	w := httptest.NewRecorder()
	c.Issue(w, "round-trip-id")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, ck := range w.Result().Cookies() {
		r.AddCookie(ck)
	}

	if got := c.FromRequest(r); got != "round-trip-id" {
		t.Errorf("FromRequest = %q, 期望 round-trip-id", got)
	}
}

func TestFromRequest_NoCookie(t *testing.T) {
	c := DefaultCookieConfig()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := c.FromRequest(r); got != "" {
		t.Errorf("无 cookie 时应返回空串，得到 %q", got)
	}
}

// Clear 必须让浏览器立即删除 cookie（MaxAge<0），否则登录后访客身份仍在，
// 会反复触发购物车合并。
func TestClear_ExpiresCookie(t *testing.T) {
	c := DefaultCookieConfig()
	w := httptest.NewRecorder()
	c.Clear(w)

	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("期望 1 个 cookie，得到 %d", len(cookies))
	}
	if cookies[0].MaxAge >= 0 {
		t.Errorf("Clear 应设 MaxAge<0，得到 %d", cookies[0].MaxAge)
	}
}
