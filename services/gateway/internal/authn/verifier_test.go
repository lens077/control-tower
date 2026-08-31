package authn

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer = "https://casdoor.apikv.com"
	testAud    = "client-consumer"
)

var now = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

type tokenOpt func(*Claims)

func mintKey(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	return key, pubPEM
}

func mintToken(t *testing.T, key *rsa.PrivateKey, opts ...tokenOpt) string {
	t.Helper()
	claims := &Claims{
		Owner:     "lens",
		Name:      "alice",
		TokenType: TokenTypeAccess,
		Roles:     []Role{{Owner: "lens", Name: "customer"}},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Audience:  jwt.ClaimStrings{testAud},
			Subject:   "u-alice",
			ID:        "jti-1",
			IssuedAt:  jwt.NewNumericDate(now.Add(-time.Minute)),
			ExpiresAt: jwt.NewNumericDate(now.Add(14 * time.Minute)),
		},
	}
	for _, o := range opts {
		o(claims)
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newVerifier(t *testing.T, pubPEM []byte) *Verifier {
	t.Helper()
	v, err := New(testIssuer, []string{testAud})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.SetPublicKeyPEM(pubPEM); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVerifyValidAccessToken(t *testing.T) {
	key, pub := mintKey(t)
	v := newVerifier(t, pub)
	c, err := v.Verify(mintToken(t, key), now)
	if err != nil {
		t.Fatal(err)
	}
	if c.UserID() != "u-alice" || len(c.RoleNames()) != 1 || c.RoleNames()[0] != "customer" {
		t.Fatalf("claims=%+v", c)
	}
}

// P0-C 核心：refresh token 与 access token 同钥签发，tokenType 必须拦住它。
func TestRefreshTokenRejected(t *testing.T) {
	key, pub := mintKey(t)
	v := newVerifier(t, pub)
	tok := mintToken(t, key, func(c *Claims) {
		c.TokenType = "refresh-token"
		c.ExpiresAt = jwt.NewNumericDate(now.Add(168 * time.Hour)) // 长效
	})
	if _, err := v.Verify(tok, now); !errors.Is(err, ErrNotAccess) {
		t.Fatalf("want ErrNotAccess, got %v", err)
	}
}

func TestWrongIssuerRejected(t *testing.T) {
	key, pub := mintKey(t)
	v := newVerifier(t, pub)
	tok := mintToken(t, key, func(c *Claims) { c.Issuer = "https://evil.example" })
	if _, err := v.Verify(tok, now); !errors.Is(err, ErrWrongIssuer) {
		t.Fatalf("want ErrWrongIssuer, got %v", err)
	}
}

func TestWrongAudienceRejected(t *testing.T) {
	key, pub := mintKey(t)
	v := newVerifier(t, pub)
	tok := mintToken(t, key, func(c *Claims) { c.Audience = jwt.ClaimStrings{"other-app"} })
	if _, err := v.Verify(tok, now); !errors.Is(err, ErrWrongAud) {
		t.Fatalf("want ErrWrongAud, got %v", err)
	}
}

func TestExpiredRejectedButLeewayHolds(t *testing.T) {
	key, pub := mintKey(t)
	v := newVerifier(t, pub)

	// 过期 30s：在 60s leeway 内，放行（时钟偏差容忍）。
	within := mintToken(t, key, func(c *Claims) { c.ExpiresAt = jwt.NewNumericDate(now.Add(-30 * time.Second)) })
	if _, err := v.Verify(within, now); err != nil {
		t.Fatalf("within leeway should pass: %v", err)
	}
	// 过期 2 分钟：拒绝。
	beyond := mintToken(t, key, func(c *Claims) { c.ExpiresAt = jwt.NewNumericDate(now.Add(-2 * time.Minute)) })
	if _, err := v.Verify(beyond, now); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("want ErrTokenInvalid, got %v", err)
	}
	// nbf 在未来 30s：leeway 内放行（登录死循环的历史教训）。
	nbf := mintToken(t, key, func(c *Claims) { c.NotBefore = jwt.NewNumericDate(now.Add(30 * time.Second)) })
	if _, err := v.Verify(nbf, now); err != nil {
		t.Fatalf("nbf within leeway should pass: %v", err)
	}
}

func TestHS256Rejected(t *testing.T) {
	_, pub := mintKey(t)
	v := newVerifier(t, pub)
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": testIssuer, "aud": testAud, "sub": "u", "exp": now.Add(time.Hour).Unix(),
	}).SignedString([]byte("hmac-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(tok, now); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("want ErrTokenInvalid, got %v", err)
	}
}

func TestDeletedOrForbiddenRejected(t *testing.T) {
	key, pub := mintKey(t)
	v := newVerifier(t, pub)
	del := mintToken(t, key, func(c *Claims) { c.IsDeleted = true })
	if _, err := v.Verify(del, now); !errors.Is(err, ErrAccountGone) {
		t.Fatalf("want ErrAccountGone, got %v", err)
	}
	forbidden := mintToken(t, key, func(c *Claims) { c.IsForbidden = true })
	if _, err := v.Verify(forbidden, now); !errors.Is(err, ErrAccountGone) {
		t.Fatalf("want ErrAccountGone, got %v", err)
	}
}

func TestMissingSubRejected(t *testing.T) {
	key, pub := mintKey(t)
	v := newVerifier(t, pub)
	tok := mintToken(t, key, func(c *Claims) { c.Subject = "" })
	if _, err := v.Verify(tok, now); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("want ErrTokenInvalid, got %v", err)
	}
}

func TestBadPublicKeyKeepsOld(t *testing.T) {
	key, pub := mintKey(t)
	v := newVerifier(t, pub)
	if err := v.SetPublicKeyPEM([]byte("not a pem")); err == nil {
		t.Fatal("bad pem must error")
	}
	// 旧钥仍然可用（last-known-good）。
	if _, err := v.Verify(mintToken(t, key), now); err != nil {
		t.Fatalf("old key should keep working: %v", err)
	}
}

func TestRevocationScenarios(t *testing.T) {
	key, pub := mintKey(t)
	v := newVerifier(t, pub)

	yaml := []byte(`
revocations:
  # 场景一/二：sub + issued_before（改角色/封禁杀存量）
  - sub: u-alice
    issued_before: 2026-08-23T11:59:30Z
    expires_at: 2026-08-23T12:30:00Z
    reason: ROLE_CHANGED
  # 场景二兜底：无视 iat 的全拒
  - sub: u-mallory
    all: true
    expires_at: 2026-08-23T12:30:00Z
    reason: ACCOUNT_BANNED
  # 场景三：jti 级
  - jti: jti-leaked
    sub: ""
    expires_at: 2026-08-23T12:30:00Z
    reason: TOKEN_LEAKED
  # 已过期条目：应被剔除
  - sub: u-old
    all: true
    expires_at: 2026-08-23T11:00:00Z
`)
	table, err := ParseRevocations(yaml, now)
	if err != nil {
		t.Fatal(err)
	}
	v.SetRevocations(table)

	// alice 的旧 token（iat=11:59，早于 issued_before）被撤销。
	old := mintToken(t, key)
	if _, err := v.Verify(old, now); !errors.Is(err, ErrRevoked) {
		t.Fatalf("want ErrRevoked, got %v", err)
	}
	// alice 刷新后的新 token（iat 晚于 issued_before）放行——refresh 换新 claims 是设计机制。
	fresh := mintToken(t, key, func(c *Claims) {
		c.IssuedAt = jwt.NewNumericDate(now.Add(-10 * time.Second))
		c.ID = "jti-2"
	})
	if _, err := v.Verify(fresh, now); err != nil {
		t.Fatalf("fresh token should pass: %v", err)
	}
	// mallory 全拒：iat 再新也拒。
	mal := mintToken(t, key, func(c *Claims) {
		c.Subject = "u-mallory"
		c.ID = "jti-3"
		c.IssuedAt = jwt.NewNumericDate(now)
	})
	if _, err := v.Verify(mal, now); !errors.Is(err, ErrRevoked) {
		t.Fatalf("want ErrRevoked for banned sub, got %v", err)
	}
	// 泄露 jti：其他人无碍，该 token 必死。
	leaked := mintToken(t, key, func(c *Claims) {
		c.Subject = "u-bob"
		c.ID = "jti-leaked"
		c.IssuedAt = jwt.NewNumericDate(now)
	})
	if _, err := v.Verify(leaked, now); !errors.Is(err, ErrRevoked) {
		t.Fatalf("want ErrRevoked for leaked jti, got %v", err)
	}
	// 过期条目不生效。
	oldSub := mintToken(t, key, func(c *Claims) {
		c.Subject = "u-old"
		c.ID = "jti-4"
	})
	if _, err := v.Verify(oldSub, now); err != nil {
		t.Fatalf("expired entry must not revoke: %v", err)
	}
}

func TestParseRevocationsRejectsEmptySelector(t *testing.T) {
	if _, err := ParseRevocations([]byte("revocations:\n  - reason: X\n"), now); err == nil {
		t.Fatal("entry without sub/jti must fail parse")
	}
}
