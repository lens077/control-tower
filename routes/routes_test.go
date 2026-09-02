package routes

import "testing"

func TestParseAllEnvs(t *testing.T) {
	for _, env := range Envs() {
		p, err := Parse(env)
		if err != nil {
			t.Fatalf("%s: %v", env, err)
		}
		if len(p.Routes) == 0 || len(p.Anonymous) == 0 {
			t.Fatalf("%s: empty routes/anonymous", env)
		}
		// 每条 target 必须恰好是 discovery:/// 或 direct:// 之一。
		seen := map[string]string{}
		for _, e := range p.Routes {
			d, h := e.DiscoveryTarget(), e.DirectHost()
			if e.Package == "" || (d == "" && h == "") || (d != "" && h != "") {
				t.Fatalf("%s: bad entry %+v", env, e)
			}
			if d != "" {
				seen[e.Package] = d
			} else {
				seen[e.Package] = h
			}
		}
		// 关键拓扑抽查（与 .service-matrix.yaml 的核对由 ecommerce structcheck 承担）：
		// dev 已改 direct://（Consul 注册关闭），pre 仍是 discovery:///，两种形态都要认。
		userOK := seen["user"] == "user-identity" || seen["user"] == "ecommerce-user-service.ecommerce.svc"
		telemetryOK := seen["telemetry"] == "behavior-service" || seen["telemetry"] == "ecommerce-behavior-service.ecommerce.svc"
		if !userOK || !telemetryOK {
			t.Fatalf("%s: unexpected topology %v", env, seen)
		}
		if _, ok := seen["config"]; ok {
			t.Fatalf("%s: /config* route must not exist", env)
		}
	}
}

func TestDirectHost(t *testing.T) {
	cases := map[string]string{
		"direct://ecommerce-user-service.ecommerce.svc:30001": "ecommerce-user-service.ecommerce.svc",
		"direct://127.0.0.1:18080":                            "127.0.0.1",
		"direct://no-port":                                    "no-port",
		"discovery:///user-identity":                          "",
	}
	for target, want := range cases {
		if got := (Entry{Target: target}).DirectHost(); got != want {
			t.Errorf("DirectHost(%q) = %q, want %q", target, got, want)
		}
	}
}
