package identity

import (
	"net/http"
	"testing"

	"github.com/lens077/control-tower/services/gateway/internal/authn"

	"github.com/golang-jwt/jwt/v5"
)

func TestStripRemovesAllVariants(t *testing.T) {
	h := http.Header{}
	h.Set("x-md-global-user-id", "forged")
	h.Set("X-Md-Global-Role", "admin")
	h.Set("X-MD-GLOBAL-OWNER", "evil")
	h.Set("x-md-anything", "junk")
	h.Set("Authorization", "Bearer keep")

	Strip(h)

	for name := range h {
		if name != "Authorization" {
			t.Fatalf("header %q should have been stripped", name)
		}
	}
	if h.Get("Authorization") != "Bearer keep" {
		t.Fatal("non-identity headers must survive")
	}
}

func TestInject(t *testing.T) {
	c := &authn.Claims{
		Owner: "lens",
		Name:  "alice",
		Roles: []authn.Role{{Name: "customer"}, {Name: "vip"}},
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "u-alice",
		},
	}
	h := http.Header{}
	Inject(h, c)

	if h.Get(HeaderUserID) != "u-alice" || h.Get(HeaderName) != "alice" || h.Get(HeaderOwner) != "lens" {
		t.Fatalf("identity headers wrong: %v", h)
	}
	if h.Get(HeaderRole) != "customer,vip" {
		t.Fatalf("role header=%q want customer,vip", h.Get(HeaderRole))
	}
}

func TestInjectNoRoles(t *testing.T) {
	h := http.Header{}
	h.Set(HeaderRole, "stale")
	Inject(h, &authn.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "u"}})
	if _, exists := h[http.CanonicalHeaderKey(HeaderRole)]; exists {
		t.Fatal("empty roles must clear role header")
	}
}
