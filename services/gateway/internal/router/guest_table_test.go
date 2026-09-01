package router

import (
	"strings"
	"testing"

	confv1 "github.com/lens077/control-tower/services/gateway/internal/conf/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

func guestCfg(anonymous, guest []string) *confv1.RouteConfig {
	return &confv1.RouteConfig{
		Version: "v2",
		Routes: []*confv1.Route{{
			Package: "cart",
			Target:  "discovery:///cart-service",
			Timeout: durationpb.New(5_000_000_000),
		}},
		Anonymous: anonymous,
		Guest:     guest,
	}
}

func TestIsGuest(t *testing.T) {
	tbl, err := Build(guestCfg(nil, []string{"/cart.v1.CartService/GetCart"}))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !tbl.IsGuest("/cart.v1.CartService/GetCart") {
		t.Error("访客清单里的路径应判为 guest")
	}
	if tbl.IsGuest("/order.v1.OrderService/CreateOrder") {
		t.Error("不在清单里的路径不应判为 guest")
	}
}

// anonymous 与 guest 语义互斥（前者完全无身份，后者有访客身份）。
// 同一路径同时出现在两个清单必然有一半配置静默失效——必须报错而不是猜。
func TestBuild_RejectsPathInBothLists(t *testing.T) {
	p := "/cart.v1.CartService/GetCart"
	_, err := Build(guestCfg([]string{p}, []string{p}))
	if err == nil {
		t.Fatal("同一路径同时在 anonymous 与 guest 清单里，Build 必须报错")
	}
	if !strings.Contains(err.Error(), "both anonymous and guest") {
		t.Errorf("错误信息应指明冲突原因，得到: %v", err)
	}
}

// 访客清单为空时不应影响既有行为。
func TestBuild_EmptyGuestList(t *testing.T) {
	tbl, err := Build(guestCfg([]string{"/user.v1.UserService/SignIn"}, nil))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if tbl.IsGuest("/user.v1.UserService/SignIn") {
		t.Error("空访客清单不该把任何路径判为 guest")
	}
	if !tbl.IsAnonymous("/user.v1.UserService/SignIn") {
		t.Error("匿名清单行为不应受访客特性影响")
	}
}
