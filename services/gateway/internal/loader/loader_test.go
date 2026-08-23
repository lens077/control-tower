package loader

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lens077/control-tower/routes"
	"github.com/lens077/control-tower/services/gateway/internal/authn"
	"github.com/lens077/control-tower/services/gateway/internal/authz"
	"github.com/lens077/control-tower/services/gateway/internal/httpmw"

	"go.uber.org/zap"
)

const miniModel = `
[request_definition]
r = sub, obj, act
[policy_definition]
p = sub, obj, act, eft
[role_definition]
g = _, _
[policy_effect]
e = some(where (p.eft == allow)) && !some(where (p.eft == deny))
[matchers]
m = g(r.sub, p.sub) && keyMatch2(r.obj, p.obj) && regexMatch(r.act, p.act)
`

const miniPolicies = "p, consumer, /user.v1.UserService/*, POST, allow\n"

func newState(t *testing.T) *State {
	t.Helper()
	v, err := authn.New("https://casdoor.apikv.com", []string{"client"})
	if err != nil {
		t.Fatal(err)
	}
	return NewState(v, authz.New(), httpmw.NewCorsSwapper(), zap.NewNop())
}

func pubPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

// 仓内路由模板必须能通过完整解析+校验链——模板漂移在这里被 CI 抓住。
func TestEmbeddedTemplatesAreValid(t *testing.T) {
	s := newState(t)
	for _, env := range routes.Envs() {
		data, err := routes.Env(env)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Apply(KeyRoutes, data); err != nil {
			t.Fatalf("template %s invalid: %v", env, err)
		}
		tbl := s.Table()
		if tbl == nil {
			t.Fatal("table not stored")
		}
		// 抽查关键路由与匿名清单。
		if r, ok := tbl.Resolve("/user.v1.UserService/SignIn"); !ok || r.Target != "discovery:///user-identity" {
			t.Fatalf("user route wrong: %+v ok=%v", r, ok)
		}
		if r, ok := tbl.Resolve("/telemetry.v1.TelemetryService/CollectWebVitals"); !ok || r.Target != "discovery:///behavior-service" {
			t.Fatalf("telemetry route wrong: %+v ok=%v", r, ok)
		}
		if _, ok := tbl.Resolve("/config.v1.ConfigService/GetKey"); ok {
			t.Fatal("/config* route must be gone (Q14a)")
		}
		if !tbl.IsAnonymous("/payment.v1.PaymentService/HandlePaymentNotify") {
			t.Fatal("payment notify must be anonymous (verbatim migration)")
		}
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	s := newState(t)
	bad := []byte(`
version: v2
routes:
  - package: user
    target: discovery:///user-identity
    timeout: 4s
typo_field: oops
cors:
  allow_origins: ["http://localhost:3000"]
`)
	if err := s.Apply(KeyRoutes, bad); err == nil {
		t.Fatal("unknown field must be rejected (DiscardUnknown=false)")
	}
}

func TestInvalidUpdateKeepsLastKnownGood(t *testing.T) {
	s := newState(t)
	good, _ := routes.Env("dev")
	if err := s.Apply(KeyRoutes, good); err != nil {
		t.Fatal(err)
	}
	before := s.Table()

	// timeout 超上限（>120s）触发 protovalidate 拒绝。
	bad := []byte(`
version: v2
routes:
  - package: user
    target: discovery:///user-identity
    timeout: 600s
cors:
  allow_origins: ["http://localhost:3000"]
`)
	if err := s.Apply(KeyRoutes, bad); err == nil {
		t.Fatal("invalid timeout must be rejected")
	}
	if s.Table() != before {
		t.Fatal("table must keep last-known-good")
	}
}

func TestCorsAtomicity(t *testing.T) {
	s := newState(t)
	good, _ := routes.Env("dev")
	if err := s.Apply(KeyRoutes, good); err != nil {
		t.Fatal(err)
	}
	before := s.Table()

	// 路由合法但 CORS 非法（credentials+通配）：整份拒绝，表不替换。
	bad := []byte(`
version: v2
routes:
  - package: user
    target: discovery:///user-identity
    timeout: 4s
cors:
  allow_credentials: true
  allow_origins: ["*"]
`)
	if err := s.Apply(KeyRoutes, bad); err == nil {
		t.Fatal("wildcard+credentials must reject whole update")
	}
	if s.Table() != before {
		t.Fatal("table must not swap when cors invalid")
	}
}

func TestReadyRequiresAllThree(t *testing.T) {
	s := newState(t)
	if s.Ready() {
		t.Fatal("empty state must not be ready")
	}
	good, _ := routes.Env("dev")
	if err := s.Apply(KeyRoutes, good); err != nil {
		t.Fatal(err)
	}
	if s.Ready() {
		t.Fatal("routes alone must not be ready")
	}
	if err := s.Apply(KeyPublicPEM, pubPEM(t)); err != nil {
		t.Fatal(err)
	}
	if s.Ready() {
		t.Fatal("routes+key must not be ready without policies")
	}
	if err := s.Apply(KeyModel, []byte(miniModel)); err != nil {
		t.Fatal(err)
	}
	if err := s.Apply(KeyPolicies, []byte(miniPolicies)); err != nil {
		t.Fatal(err)
	}
	if !s.Ready() {
		t.Fatal("all three loaded must be ready")
	}
}

func TestRevocationsApplyAndAge(t *testing.T) {
	s := newState(t)
	if age := s.RevocationAge(time.Now()); age < time.Hour {
		t.Fatal("unloaded revocations must report huge age")
	}
	if err := s.Apply(KeyRevocations, []byte("revocations:\n  - sub: u1\n    all: true\n")); err != nil {
		t.Fatal(err)
	}
	if age := s.RevocationAge(time.Now()); age > time.Minute {
		t.Fatalf("age=%v", age)
	}
	if s.verifier.Revocations().Len() != 1 {
		t.Fatal("revocation table must be applied")
	}
}

func TestRunFileDir(t *testing.T) {
	dir := t.TempDir()
	good, _ := routes.Env("dev")
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("routes.yaml", good)
	write("public.pem", pubPEM(t))
	write("policies.csv", []byte(miniPolicies))
	write("model.conf", []byte(miniModel))
	// revocations.yaml 缺省：容忍为空表。

	s := newState(t)
	if err := RunFileDir(dir, s); err != nil {
		t.Fatal(err)
	}
	if !s.Ready() {
		t.Fatal("file mode must reach ready")
	}
}
