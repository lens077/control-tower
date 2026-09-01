package identity

import (
	"net/http"
	"testing"
)

// 访客注入只该给出 user-id + 匿名标记。
func TestInjectGuest_SetsIDAndFlag(t *testing.T) {
	h := http.Header{}
	InjectGuest(h, "guest-123")

	if got := h.Get(HeaderUserID); got != "guest-123" {
		t.Errorf("%s = %q, 期望 guest-123", HeaderUserID, got)
	}
	if got := h.Get(HeaderAnonymous); got != "true" {
		t.Errorf("%s = %q, 期望 true——下游靠它区分访客与登录用户", HeaderAnonymous, got)
	}
}

// 这条是越权防线：若上一跳残留了 role/name/owner，访客会被下游当成有角色的用户。
// RBAC 是按角色判的，漏一个 Del 就是提权。
func TestInjectGuest_ClearsLoggedInAttributes(t *testing.T) {
	h := http.Header{}
	h.Set(HeaderName, "alice")
	h.Set(HeaderRole, "admin")
	h.Set(HeaderOwner, "merchant-a")

	InjectGuest(h, "guest-123")

	for _, k := range []string{HeaderName, HeaderRole, HeaderOwner} {
		if got := h.Get(k); got != "" {
			t.Errorf("%s 未被清除，残值 %q——访客可能被误判为有角色的用户", k, got)
		}
	}
}

// 访客标记必须落在 x-md- 命名空间内，才会被 Strip 无条件剥离；
// 否则客户端可以自带 x-md-global-anonymous=false 把自己伪装成登录用户。
func TestHeaderAnonymous_IsStrippable(t *testing.T) {
	h := http.Header{}
	h.Set(HeaderAnonymous, "true")
	Strip(h)
	if got := h.Get(HeaderAnonymous); got != "" {
		t.Fatalf("%s 未被 Strip 剥离——客户端可伪造匿名标记", HeaderAnonymous)
	}
}
