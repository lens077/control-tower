package guest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// testCfg 是带签名密钥的配置。密钥缺省为空是刻意的 fail-closed 设计，
// 因此每个用例都必须显式给一把。
func testCfg() CookieConfig {
	c := DefaultCookieConfig()
	c.Key = []byte("unit-test-signing-key")
	return c
}

// 一个合法的 UUID，代表「本网关签发过的访客 ID」。
const sampleID = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

// 访客 id 必须每次不同且足够长——它是身份凭据，可预测即可冒用他人购物车。
func TestNewID_UniqueAndLongEnough(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		id, err := NewID()
		if err != nil {
			t.Fatalf("NewID: %v", err)
		}
		// 必须是合法 UUID：cart 服务会 uuid.Parse 后写进 UUID 列，
		// 形态不对会让访客加购在数据层直接失败。
		if _, err := uuid.Parse(id); err != nil {
			t.Fatalf("id 不是合法 UUID(%q): %v——cart 侧 uuid.Parse 会失败", id, err)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("NewID 产生重复值 %q——CSPRNG 装配错误", id)
		}
		seen[id] = struct{}{}
	}
}

// Issue 写出的 cookie 必须 HttpOnly：访客 id 是凭据，JS 没有任何理由读它。
func TestIssue_IsHttpOnly(t *testing.T) {
	c := testCfg()
	w := httptest.NewRecorder()
	c.Issue(w, sampleID)

	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("期望 1 个 cookie，得到 %d", len(cookies))
	}
	got := cookies[0]
	if !got.HttpOnly {
		t.Error("访客 cookie 必须 HttpOnly——否则 XSS 可直接窃取身份")
	}
	// 写出的是「<id>.<签名>」，不是裸 id。
	if !strings.HasPrefix(got.Value, sampleID+".") || got.Value == sampleID {
		t.Errorf("cookie 值应为已签名形态，得到 %q", got.Value)
	}
	if got.MaxAge <= 0 {
		t.Errorf("MaxAge 应为正数(持久 cookie)，得到 %d", got.MaxAge)
	}
}

// 读回自己写出的 cookie —— 发与读必须对称，否则每次请求都会签发新身份，
// 购物车永远是空的（这类 bug 不会报错，只会「功能看起来没生效」）。
func TestIssueThenFromRequest_RoundTrip(t *testing.T) {
	c := testCfg()
	w := httptest.NewRecorder()
	c.Issue(w, sampleID)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, ck := range w.Result().Cookies() {
		r.AddCookie(ck)
	}

	if got := c.FromRequest(r); got != sampleID {
		t.Errorf("FromRequest = %q, 期望 %s", got, sampleID)
	}
}

func TestFromRequest_NoCookie(t *testing.T) {
	c := testCfg()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := c.FromRequest(r); got != "" {
		t.Errorf("无 cookie 时应返回空串，得到 %q", got)
	}
}

// 安全回归：客户端自带的访客 cookie 一律不认。
//
// 背景（2026-09-08 审计）：访客 ID 与真实用户 ID 共用 UUID 取值空间，且同写进
// cart_item.user_id。若网关原样采纳 cookie 值，任何人把它改成受害者的用户 UUID
// 就能读写受害者的购物车——横向越权。签名把「本网关签发过」这件事变成可验证的。
func TestFromRequest_RejectsClientSuppliedValues(t *testing.T) {
	c := testCfg()
	// 受害者的真实用户 UUID：形态完全合法，只是不是我们签发的。
	const victimUserID = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"

	cases := []struct {
		name  string
		value string
	}{
		{"裸 UUID（伪造成他人用户 ID）", victimUserID},
		{"无签名的历史 cookie", sampleID},
		{"签名被篡改", sampleID + ".AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		{"拿别人的签名配自己的 id", victimUserID + "." + strings.SplitN(signOf(t, c, sampleID), ".", 2)[1]},
		{"根本不是 UUID", "not-a-uuid-at-all"},
		{"空值", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.AddCookie(&http.Cookie{Name: c.Name, Value: tc.value})
			if got := c.FromRequest(r); got != "" {
				t.Errorf("必须拒绝，却采纳了 %q", got)
			}
		})
	}
}

// 未配置密钥时 fail closed：宁可让访客身份失效，也不接受未经验证的取值。
func TestFromRequest_NoKeyRejectsEverything(t *testing.T) {
	signed := signOf(t, testCfg(), sampleID)
	c := DefaultCookieConfig() // 没有 Key
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: c.Name, Value: signed})
	if got := c.FromRequest(r); got != "" {
		t.Errorf("无密钥时必须一律拒绝，得到 %q", got)
	}
}

// 换一把密钥就认不出对方签发的身份——提醒同一部署的所有副本必须共用同一把。
func TestFromRequest_RejectsOtherKeysSignature(t *testing.T) {
	a := testCfg()
	b := testCfg()
	b.Key = []byte("a-different-key")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: a.Name, Value: signOf(t, a, sampleID)})
	if got := b.FromRequest(r); got != "" {
		t.Errorf("不同密钥签发的 cookie 必须拒绝，得到 %q", got)
	}
}

// signOf 借 Issue 拿到已签名的 cookie 值，避免测试直接依赖内部 sign。
func signOf(t *testing.T, c CookieConfig, id string) string {
	t.Helper()
	w := httptest.NewRecorder()
	c.Issue(w, id)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("期望 1 个 cookie，得到 %d", len(cookies))
	}
	return cookies[0].Value
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
