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
		seen := map[string]string{}
		for _, e := range p.Routes {
			if e.Package == "" || e.DiscoveryTarget() == "" {
				t.Fatalf("%s: bad entry %+v", env, e)
			}
			seen[e.Package] = e.DiscoveryTarget()
		}
		// 关键拓扑抽查（与 .service-matrix.yaml 的核对由 ecommerce structcheck 承担）。
		if seen["user"] != "user-identity" || seen["telemetry"] != "behavior-service" {
			t.Fatalf("%s: unexpected topology %v", env, seen)
		}
		if _, ok := seen["config"]; ok {
			t.Fatalf("%s: /config* route must not exist", env)
		}
	}
}
