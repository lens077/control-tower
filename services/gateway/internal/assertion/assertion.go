// Package assertion 实现网关侧内部断言签发（T1 可信身份边界，WP-2）。
//
// 需求权威：palcommerce/docs/shop/trusted-identity-contract.md（冻结件）；
// 共享契约：同目录 T1-implementation-plan.md §1.1–§1.4/§1.7，claim 名与取值逐字一致。
//
// 边界：网关只签发，shop 只验证（JWKS 分发在 WP-3）。算法固定 EdDSA（Ed25519），
// 私钥是 PEM PKCS8 文件（K8s Secret 挂载），TTL 上限 300s，jti 每次签发新生成。
package assertion

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/lens077/control-tower/services/gateway/internal/authn"
)

// 断言载体头名（契约 §4.1，与 shop 侧逐字一致）。
const (
	HeaderName      = "X-Internal-Assertion"
	HeaderRequestID = "X-Md-Request-Id"
)

// 默认值与上限（§1.7：TTL 可配默认 300s；契约 §4.2：TTL ≤ 300 秒，超出 shop 直接拒绝，
// 所以网关侧在装配时 fail-fast 而不是签一枚注定被拒的断言）。
const (
	DefaultIssuer   = "palcommerce-gateway"
	DefaultAudience = "shop"
	DefaultTTL      = 300 * time.Second
	MaxTTL          = 300 * time.Second
)

// Config 是签发器的装配输入（env 键见 §1.7）。
type Config struct {
	// Packages 是断言生效的 proto 一级包名（如 shop）；空 = 不启用任何包。
	Packages []string
	// Issuer/Audience 默认 palcommerce-gateway / shop。
	Issuer   string
	Audience string
	// TTL 非正数取 DefaultTTL；超过 MaxTTL 直接报错（契约上限）。
	TTL time.Duration
	// KeyFile 是 PEM PKCS8 Ed25519 私钥文件路径（Secret 挂载）。
	KeyFile string
	// KeyID 是 JWT kid，必须与 JWKS 发布一致（WP-3）。
	KeyID string
	// RolePermissions 是 ASSERTION_ROLE_PERMISSIONS 的原文，解析规则见 ParseRolePermissions。
	RolePermissions string
}

// Identity 是签发断言所依据的已验证身份。
type Identity struct {
	// Subject 是用户 UUID；访客/匿名请求为空串（契约：访客 sub 为空）。
	Subject string
	// Roles 是角色名快照（进断言供审计）。
	Roles []string
	// Anonymous 为 true 时按访客签发：roles 空、sub 空、permissions 取 anonymous 集合。
	Anonymous bool
}

// IdentityFromClaims 把网关已验签的 claims 折叠成签发输入。
// claims 为 nil（匿名清单 / 可选认证未识别出 / 访客轨）一律按匿名签发——
// 三种形态在 proxy 里都拿不到 claims，语义统一为「未登录」。
func IdentityFromClaims(c *authn.Claims) Identity {
	if c == nil {
		return Identity{Anonymous: true}
	}
	return Identity{Subject: c.UserID(), Roles: c.RoleNames()}
}

// Issuer 是断言签发器。构建后只读，可并发使用。
type Issuer struct {
	key      ed25519.PrivateKey
	keyID    string
	issuer   string
	audience string
	ttl      time.Duration
	packages map[string]struct{}
	perms    map[string][]string
}

// Load 读取私钥并解析角色映射，装配签发器。任何配置错误都在启动时报错（快速失败纪律）。
func Load(cfg Config) (*Issuer, error) {
	if len(cfg.Packages) == 0 {
		return nil, errors.New("assertion: Packages 为空（ASSERTION_ENABLED_PACKAGES），不应装配签发器")
	}
	if cfg.KeyFile == "" {
		return nil, errors.New("assertion: ASSERTION_SIGNING_KEY_FILE 必须设置")
	}
	if cfg.KeyID == "" {
		return nil, errors.New("assertion: ASSERTION_KEY_ID 必须设置（kid 须与 JWKS 发布一致）")
	}
	issuer := cfg.Issuer
	if issuer == "" {
		issuer = DefaultIssuer
	}
	audience := cfg.Audience
	if audience == "" {
		audience = DefaultAudience
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if ttl > MaxTTL {
		return nil, fmt.Errorf("assertion: TTL %s 超过契约上限 %s（shop 会直接拒绝）", ttl, MaxTTL)
	}
	perms, err := ParseRolePermissions(cfg.RolePermissions)
	if err != nil {
		return nil, err
	}
	if len(perms) == 0 {
		return nil, errors.New("assertion: ASSERTION_ROLE_PERMISSIONS 至少要有一条角色映射（含 anonymous，见契约 §1.4）")
	}
	key, err := loadPrivateKey(cfg.KeyFile)
	if err != nil {
		return nil, err
	}
	pkgs := make(map[string]struct{}, len(cfg.Packages))
	for _, p := range cfg.Packages {
		pkgs[p] = struct{}{}
	}
	return &Issuer{
		key:      key,
		keyID:    cfg.KeyID,
		issuer:   issuer,
		audience: audience,
		ttl:      ttl,
		packages: pkgs,
		perms:    perms,
	}, nil
}

// loadPrivateKey 读取 PEM PKCS8 私钥并校验算法类型。
// 禁止 HMAC（契约 §4.3），所以非 Ed25519 的 PKCS8 私钥（RSA/ECDSA）一律拒绝。
func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("assertion: 读取签名私钥 %s: %w", path, err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("assertion: 签名私钥 %s 不是有效 PEM", path)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("assertion: 解析 PKCS8 私钥: %w", err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("assertion: 私钥类型 %T 不是 Ed25519（契约固定 EdDSA）", parsed)
	}
	return key, nil
}

// Enabled 判定包是否在断言生效集合内。
func (i *Issuer) Enabled(pkg string) bool {
	_, ok := i.packages[pkg]
	return ok
}

// KeyID 返回 kid（供启动日志确认与 JWKS 发布一致）。
func (i *Issuer) KeyID() string { return i.keyID }

// TTL 返回签发有效期。
func (i *Issuer) TTL() time.Duration { return i.ttl }

// Issue 签发一枚 compact JWT。
//   - permissions = 用户每个角色映射集合的并集；匿名身份只取 anonymous 集合；
//   - jti 每次调用新生成（契约 I3：每次网关发起的调用都是新断言）；
//   - kid 进 JWT header，shop 按它取 JWKS 验签公钥。
//
// claims 用 MapClaims 显式逐项填充而不是嵌入 jwt.RegisteredClaims：
// 契约要求 sub 必填且访客为**空串**，RegisteredClaims 的 omitempty 会把空 sub
// 整个丢掉，shop 按「缺 claim」拒绝。
func (i *Issuer) Issue(id Identity) (string, error) {
	var (
		perms  []string
		roles  []string
		subj   string
		active bool
	)
	if id.Anonymous {
		perms = i.permSet("anonymous")
		roles = []string{}
	} else {
		subj = id.Subject
		roles = appendEmpty(id.Roles)
		active = true
		seen := make(map[string]struct{})
		perms = []string{}
		for _, r := range id.Roles {
			for _, a := range i.permSet(r) {
				if _, dup := seen[a]; dup {
					continue
				}
				seen[a] = struct{}{}
				perms = append(perms, a)
			}
		}
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":         i.issuer,
		"sub":         subj,
		"aud":         i.audience,
		"permissions": perms,
		"roles":       roles,
		"anonymous":   !active,
		"iat":         now.Unix(),
		"exp":         now.Add(i.ttl).Unix(),
		"jti":         uuid.NewString(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tok.Header["kid"] = i.keyID
	signed, err := tok.SignedString(i.key)
	if err != nil {
		return "", fmt.Errorf("assertion: 签发失败: %w", err)
	}
	return signed, nil
}

// permSet 取某角色的映射集合（缺失角色返回空 slice，不报错——未知角色只是没有权限，
// 授权裁决在 shop 的动作级 RBAC，网关不越位）。
func (i *Issuer) permSet(role string) []string {
	out := i.perms[role]
	if out == nil {
		return []string{}
	}
	return out
}

func appendEmpty(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// ParseRolePermissions 解析 ASSERTION_ROLE_PERMISSIONS（§1.4/§1.7）：
//
//	customer:catalog.read,wallet.read,...;admin:+topup.manual,+topup.refund;anonymous:catalog.read
//
// 「+」前缀 = 在**前一个角色**的集合上追加（admin 继承 customer 全部）。
// 解析成 map[role]→有序去重集合；签发时对用户每个角色取并集。
func ParseRolePermissions(s string) (map[string][]string, error) {
	out := make(map[string][]string)
	var prev []string
	for _, seg := range strings.Split(s, ";") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		role, actions, found := strings.Cut(seg, ":")
		if !found {
			return nil, fmt.Errorf("assertion: ASSERTION_ROLE_PERMISSIONS 片段 %q 缺少角色分隔符 ':'", seg)
		}
		role = strings.TrimSpace(role)
		if role == "" {
			return nil, fmt.Errorf("assertion: ASSERTION_ROLE_PERMISSIONS 片段 %q 角色名为空", seg)
		}
		if _, dup := out[role]; dup {
			return nil, fmt.Errorf("assertion: 角色 %q 在 ASSERTION_ROLE_PERMISSIONS 里出现两次", role)
		}
		// 任一动作带「+」才继承前一角色集合；纯新增写法（无 +）以本条为准。
		set := make(map[string]struct{})
		var list []string
		plus := false
		for _, a := range strings.Split(actions, ",") {
			if strings.HasPrefix(strings.TrimSpace(a), "+") {
				plus = true
				break
			}
		}
		if plus {
			for _, a := range prev {
				if _, dup := set[a]; !dup {
					set[a] = struct{}{}
					list = append(list, a)
				}
			}
		}
		for _, a := range strings.Split(actions, ",") {
			a = strings.TrimSpace(a)
			a = strings.TrimPrefix(a, "+")
			if a == "" {
				continue
			}
			if _, dup := set[a]; dup {
				continue
			}
			set[a] = struct{}{}
			list = append(list, a)
		}
		if len(list) == 0 {
			return nil, fmt.Errorf("assertion: 角色 %q 没有任何动作（片段 %q）", role, seg)
		}
		out[role] = list
		prev = list
	}
	return out, nil
}
