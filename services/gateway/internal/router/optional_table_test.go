package router

import (
	"strings"
	"testing"
)

func optionalCfgTest(t *testing.T, anonymous, guest, optional []string) (*Table, error) {
	t.Helper()
	cfg := guestCfg(anonymous, guest)
	cfg.OptionalAuth = optional
	return Build(cfg)
}

func TestIsOptionalAuth(t *testing.T) {
	tbl, err := optionalCfgTest(t, nil, nil, []string{"/behavior.v1.BehaviorService/Track"})
	if err != nil {
		t.Fatal(err)
	}
	if !tbl.IsOptionalAuth("/behavior.v1.BehaviorService/Track") {
		t.Fatal("Track 在 optional_auth 清单里，应判为可选认证")
	}
	if tbl.IsAnonymous("/behavior.v1.BehaviorService/Track") || tbl.IsGuest("/behavior.v1.BehaviorService/Track") {
		t.Fatal("可选认证路径不能同时被判成匿名或访客")
	}
}

// 三类清单两两互斥：重叠时鉴权中间件只会按先判定的那类处理，另一边配置静默失效。
func TestOptionalAuthRejectsOverlap(t *testing.T) {
	const p = "/behavior.v1.BehaviorService/Track"
	cases := []struct {
		name      string
		anonymous []string
		guest     []string
		want      string
	}{
		{"与 anonymous 重叠", []string{p}, nil, "both anonymous and optional_auth"},
		{"与 guest 重叠", nil, []string{p}, "both guest and optional_auth"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := optionalCfgTest(t, c.anonymous, c.guest, []string{p})
			if err == nil {
				t.Fatal("重叠配置必须在 Build 时报错")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err=%v，期望包含 %q", err, c.want)
			}
		})
	}
}
