package authz

import (
	"testing"
)

// modelText 与旧网关 model.conf 逐字一致（冻结键契约）。
const modelText = `
[request_definition]
r = sub, obj, act

[policy_definition]
p = sub, obj, act, eft

[role_definition]
g = _, _
g2 = _, _

[policy_effect]
e = some(where (p.eft == allow)) && !some(where (p.eft == deny))

[matchers]
m = g(r.sub, p.sub) && keyMatch2(r.obj, p.obj) && regexMatch(r.act, p.act)
`

const policiesCSV = `
# 逐字取自旧 policies.csv 的代表性行
p, customer, /user.v1.UserService/UserProfile, POST, allow
p, customer, /cart.v1.CartService/*, POST, allow
p, admin, /config.v1.ConfigService/*, POST, allow
p, customer, /order.v1.orderService/CreateOrder, POST, allow
p, merchant, /order.v1.orderService/CompleteOrder, POST, allow

# deny 压过 allow（policy_effect 语义）
p, customer, /cart.v1.CartService/AdminPurge, POST, deny

# 角色继承：vip 继承 customer
g, vip, customer
`

func newEnforcer(t *testing.T) *Enforcer {
	t.Helper()
	a := New()
	if err := a.SetPolicies(modelText, policiesCSV); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestNotReady(t *testing.T) {
	a := New()
	if a.Ready() {
		t.Fatal("empty enforcer must not be ready")
	}
	if _, err := a.Allowed([]string{"customer"}, "/cart.v1.CartService/AddItem", "POST"); err != ErrNotReady {
		t.Fatalf("want ErrNotReady, got %v", err)
	}
}

func TestExactAndWildcard(t *testing.T) {
	a := newEnforcer(t)

	cases := []struct {
		roles []string
		obj   string
		act   string
		want  bool
	}{
		{[]string{"customer"}, "/user.v1.UserService/UserProfile", "POST", true},
		{[]string{"customer"}, "/cart.v1.CartService/AddItem", "POST", true}, // 通配
		{[]string{"customer"}, "/config.v1.ConfigService/GetKey", "POST", false},
		{[]string{"admin"}, "/config.v1.ConfigService/GetKey", "POST", true},
		// 大小写敏感：orderService 是小写 o（历史坑，policies.csv 注释点名）。
		{[]string{"customer"}, "/order.v1.orderService/CreateOrder", "POST", true},
		{[]string{"customer"}, "/order.v1.OrderService/CreateOrder", "POST", false},
		// 方法不匹配。
		{[]string{"customer"}, "/cart.v1.CartService/AddItem", "DELETE", false},
	}
	for _, c := range cases {
		got, err := a.Allowed(c.roles, c.obj, c.act)
		if err != nil {
			t.Fatalf("Allowed(%v,%s,%s): %v", c.roles, c.obj, c.act, err)
		}
		if got != c.want {
			t.Errorf("Allowed(%v,%s,%s)=%v want %v", c.roles, c.obj, c.act, got, c.want)
		}
	}
}

func TestDenyOverridesAllow(t *testing.T) {
	a := newEnforcer(t)
	// /cart.v1.CartService/* 对 customer 是 allow，但 AdminPurge 有显式 deny。
	ok, err := a.Allowed([]string{"customer"}, "/cart.v1.CartService/AdminPurge", "POST")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("explicit deny must override wildcard allow")
	}
}

func TestRoleInheritance(t *testing.T) {
	a := newEnforcer(t)
	ok, err := a.Allowed([]string{"vip"}, "/cart.v1.CartService/AddItem", "POST")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("vip inherits customer via g")
	}
}

func TestMultiRoleAnyAllow(t *testing.T) {
	a := newEnforcer(t)
	// 第一个角色不放行、第二个放行 → 放行（多角色任一 allow 语义）。
	ok, err := a.Allowed([]string{"merchant", "admin"}, "/config.v1.ConfigService/GetKey", "POST")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("any-allow across roles must pass")
	}
	// 全部角色都不放行 → 拒绝。
	ok, err = a.Allowed([]string{"merchant", "customer"}, "/config.v1.ConfigService/GetKey", "POST")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("no role allows → deny")
	}
}

func TestInvalidPolicyKeepsOld(t *testing.T) {
	a := newEnforcer(t)
	if err := a.SetPolicies(modelText, "x, broken-line"); err == nil {
		t.Fatal("unknown ptype must fail")
	}
	// 旧策略仍然生效（last-known-good）。
	ok, err := a.Allowed([]string{"customer"}, "/cart.v1.CartService/AddItem", "POST")
	if err != nil || !ok {
		t.Fatalf("old enforcer must keep working: ok=%v err=%v", ok, err)
	}
}

func TestInvalidModelKeepsOld(t *testing.T) {
	a := newEnforcer(t)
	if err := a.SetPolicies("not a model", policiesCSV); err == nil {
		t.Fatal("bad model must fail")
	}
	if !a.Ready() {
		t.Fatal("must stay ready with old enforcer")
	}
}
