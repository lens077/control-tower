package assertion

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	"github.com/lens077/control-tower/services/gateway/internal/authn"
)

const testRolePerms = "customer:catalog.read,wallet.read,order.read,order.create,topup.create,topup.read;admin:+topup.manual,+topup.refund;anonymous:catalog.read"

// writeTestKey 生成 Ed25519 密钥对，私钥按 PKCS8 PEM 落盘。
func writeTestKey(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "signing-key.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(
		&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0400))
	return path
}

func testConfig(t *testing.T, mutate func(*Config)) *Issuer {
	t.Helper()
	cfg := Config{
		Packages:        []string{"shop"},
		KeyFile:         writeTestKey(t),
		KeyID:           "test-kid-1",
		RolePermissions: testRolePerms,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	iss, err := Load(cfg)
	require.NoError(t, err)
	return iss
}

// parsedClaims 是测试侧的断言 claim 反序列化目标（签发侧用 MapClaims 显式填充，
// 这里嵌 RegisteredClaims 只为满足 jwt/v5 的 Claims 接口；omitempty 只影响序列化）。
type parsedClaims struct {
	Permissions []string `json:"permissions"`
	Roles       []string `json:"roles"`
	Anonymous   bool     `json:"anonymous"`
	jwt.RegisteredClaims
}

// parse 用签发器私钥对应的公钥验签（模拟 shop 持 JWKS 公钥的角色），逐字段断言。
func parse(t *testing.T, iss *Issuer, token string) *parsedClaims {
	t.Helper()
	pub := iss.key.Public().(ed25519.PublicKey)
	parser := jwt.NewParser(jwt.WithValidMethods([]string{"EdDSA"}))
	var claims parsedClaims
	tok, err := parser.ParseWithClaims(token, &claims, func(*jwt.Token) (any, error) { return pub, nil })
	require.NoError(t, err, "签名与 claim 必须有效")
	require.True(t, tok.Valid)
	return &claims
}

func mustECDSAPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "ec.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(
		&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0400))
	return path
}

func TestLoadRejectsBadKeyFile(t *testing.T) {
	t.Run("文件不存在", func(t *testing.T) {
		_, err := Load(Config{Packages: []string{"shop"}, KeyFile: "/nonexistent", KeyID: "k",
			RolePermissions: testRolePerms})
		require.ErrorContains(t, err, "读取签名私钥")
	})

	t.Run("非 PEM", func(t *testing.T) {
		bad := filepath.Join(t.TempDir(), "bad.pem")
		require.NoError(t, os.WriteFile(bad, []byte("not a pem"), 0400))
		_, err := Load(Config{Packages: []string{"shop"}, KeyFile: bad, KeyID: "k",
			RolePermissions: testRolePerms})
		require.ErrorContains(t, err, "不是有效 PEM")
	})

	t.Run("PKCS8 里不是 Ed25519", func(t *testing.T) {
		_, err := Load(Config{Packages: []string{"shop"}, KeyFile: mustECDSAPEM(t), KeyID: "k",
			RolePermissions: testRolePerms})
		require.ErrorContains(t, err, "不是 Ed25519")
	})
}

func TestLoadRejectsMissingKeyIDAndTTLCap(t *testing.T) {
	keyFile := writeTestKey(t)

	t.Run("缺 kid", func(t *testing.T) {
		_, err := Load(Config{Packages: []string{"shop"}, KeyFile: keyFile, RolePermissions: testRolePerms})
		require.ErrorContains(t, err, "ASSERTION_KEY_ID")
	})

	t.Run("TTL 超上限", func(t *testing.T) {
		_, err := Load(Config{Packages: []string{"shop"}, KeyFile: keyFile, KeyID: "k",
			TTL: 301 * time.Second, RolePermissions: testRolePerms})
		require.ErrorContains(t, err, "超过契约上限")
	})

	t.Run("空映射", func(t *testing.T) {
		_, err := Load(Config{Packages: []string{"shop"}, KeyFile: keyFile, KeyID: "k"})
		require.ErrorContains(t, err, "ASSERTION_ROLE_PERMISSIONS")
	})
}

func TestSignClaimsComplete(t *testing.T) {
	iss := testConfig(t, nil)
	token, err := iss.Issue(IdentityFromClaims(&authn.Claims{
		Roles:            []authn.Role{{Name: "customer"}, {Name: "admin"}},
		RegisteredClaims: jwt.RegisteredClaims{Subject: "0f0e6d3e-1111-2222-3333-444455556666"},
	}))
	require.NoError(t, err)
	c := parse(t, iss, token)

	require.Equal(t, "palcommerce-gateway", c.Issuer)
	require.Equal(t, jwt.ClaimStrings{"shop"}, c.Audience)
	require.Equal(t, "0f0e6d3e-1111-2222-3333-444455556666", c.Subject)
	require.False(t, c.Anonymous)

	// kid 进 JWT header，shop 按它取 JWKS 公钥。
	tok, _, err := jwt.NewParser().ParseUnverified(token, jwt.MapClaims{})
	require.NoError(t, err)
	require.Equal(t, "test-kid-1", tok.Header["kid"])
	require.Equal(t, "EdDSA", tok.Header["alg"])

	// 多角色取并集：customer + admin = customer 全部 + topup.manual + topup.refund。
	require.ElementsMatch(t, []string{
		"catalog.read", "wallet.read", "order.read", "order.create",
		"topup.create", "topup.read", "topup.manual", "topup.refund",
	}, c.Permissions)
	require.ElementsMatch(t, []string{"customer", "admin"}, c.Roles)

	// TTL：exp-iat == 配置值（默认 300s），iat 在近期。
	require.Equal(t, int64(300), c.ExpiresAt.Unix()-c.IssuedAt.Unix())
	require.InDelta(t, time.Now().Unix(), c.IssuedAt.Unix(), 5)

	// jti 必须是合法 UUID。
	require.Len(t, c.ID, 36)
	require.Contains(t, c.ID, "-")
}

func TestJtiDiffersPerCall(t *testing.T) {
	iss := testConfig(t, nil)
	id := Identity{Subject: "u1", Roles: []string{"customer"}}
	t1, err := iss.Issue(id)
	require.NoError(t, err)
	t2, err := iss.Issue(id)
	require.NoError(t, err)
	c1, c2 := parse(t, iss, t1), parse(t, iss, t2)
	require.NotEqual(t, c1.ID, c2.ID, "每次签发必须生成新 jti（契约 I3）")
}

func TestTTLOverridableAndCapped(t *testing.T) {
	iss := testConfig(t, func(c *Config) { c.TTL = 120 * time.Second })
	require.Equal(t, 120*time.Second, iss.TTL())
	token, err := iss.Issue(Identity{Subject: "u1", Roles: []string{"customer"}})
	require.NoError(t, err)
	c := parse(t, iss, token)
	require.Equal(t, int64(120), c.ExpiresAt.Unix()-c.IssuedAt.Unix())
}

func TestAnonymousIdentity(t *testing.T) {
	iss := testConfig(t, nil)

	t.Run("nil claims（匿名清单）", func(t *testing.T) {
		token, err := iss.Issue(IdentityFromClaims(nil))
		require.NoError(t, err)
		c := parse(t, iss, token)
		require.True(t, c.Anonymous)
		require.Empty(t, c.Subject)
		require.Empty(t, c.Roles)
		require.Equal(t, []string{"catalog.read"}, c.Permissions)
	})

	t.Run("访客轨请求（有 guest id 但不是注册用户）", func(t *testing.T) {
		token, err := iss.Issue(Identity{Anonymous: true})
		require.NoError(t, err)
		c := parse(t, iss, token)
		require.True(t, c.Anonymous)
		require.Equal(t, "", c.Subject)
	})
}

func TestUnknownRoleYieldsNoPermissions(t *testing.T) {
	iss := testConfig(t, nil)
	token, err := iss.Issue(Identity{Subject: "u1", Roles: []string{"not-a-role"}})
	require.NoError(t, err)
	c := parse(t, iss, token)
	require.Empty(t, c.Permissions)
	require.Equal(t, []string{"not-a-role"}, c.Roles)
}

func TestParseRolePermissions(t *testing.T) {
	t.Run("完整映射与 + 继承", func(t *testing.T) {
		m, err := ParseRolePermissions(testRolePerms)
		require.NoError(t, err)
		require.Equal(t, []string{"catalog.read", "wallet.read", "order.read", "order.create",
			"topup.create", "topup.read"}, m["customer"])
		// admin = customer 全部 + 两项追加（§1.7 语义等价即可，这里按继承实现）。
		require.Equal(t, []string{"catalog.read", "wallet.read", "order.read", "order.create",
			"topup.create", "topup.read", "topup.manual", "topup.refund"}, m["admin"])
		require.Equal(t, []string{"catalog.read"}, m["anonymous"])
	})

	t.Run("去重", func(t *testing.T) {
		m, err := ParseRolePermissions("a:x,x,+y;b:y")
		require.NoError(t, err)
		require.Equal(t, []string{"x", "y"}, m["a"])
	})

	t.Run("空白容忍", func(t *testing.T) {
		m, err := ParseRolePermissions(" customer : a , b ; admin : +c ; anonymous : d ")
		require.NoError(t, err)
		require.Equal(t, []string{"a", "b"}, m["customer"])
		require.Equal(t, []string{"a", "b", "c"}, m["admin"])
	})

	t.Run("错误形态", func(t *testing.T) {
		for _, bad := range []string{
			"customer-catalog.read", // 缺 ':'
			":a",                    // 空角色
			"customer:",             // 无动作
			"customer:a;customer:b", // 角色重复
		} {
			_, err := ParseRolePermissions(bad)
			require.Error(t, err, "input=%q", bad)
		}
	})
}

func TestEnabled(t *testing.T) {
	iss := testConfig(t, nil)
	require.True(t, iss.Enabled("shop"))
	require.False(t, iss.Enabled("user"))
}
