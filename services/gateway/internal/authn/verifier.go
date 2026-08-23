package authn

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// 校验失败的分类错误，供上层映射 X-Error-Reason。
var (
	ErrNoKey        = errors.New("authn: public key not loaded")
	ErrTokenInvalid = errors.New("authn: token invalid")
	ErrWrongIssuer  = errors.New("authn: wrong issuer")
	ErrWrongAud     = errors.New("authn: audience not allowed")
	ErrNotAccess    = errors.New("authn: not an access token")
	ErrAccountGone  = errors.New("authn: account deleted or forbidden")
	ErrRevoked      = errors.New("authn: token revoked")
)

// Leeway 是时钟偏差容忍度。Casdoor 与网关的亚秒时差曾把刚签发的 token 判为
// 「not valid yet」，造成前端 401 → 清 token → 跳登录的死循环；60 秒容差是既有共识，必须保留。
const Leeway = 60 * time.Second

// Verifier 做本地 RS256 验签 + 信任域绑定 + 撤销查表。公钥与撤销名单均可热更新（原子替换）。
type Verifier struct {
	issuer    string
	audiences map[string]struct{}

	key         atomic.Pointer[rsa.PublicKey]
	revocations atomic.Pointer[RevocationTable]
}

// New 构造 Verifier。issuer 必填；audiences 是 aud 白名单（至少一个 client id）。
func New(issuer string, audiences []string) (*Verifier, error) {
	if issuer == "" {
		return nil, fmt.Errorf("authn: issuer required")
	}
	if len(audiences) == 0 {
		return nil, fmt.Errorf("authn: at least one audience required")
	}
	v := &Verifier{
		issuer:    issuer,
		audiences: make(map[string]struct{}, len(audiences)),
	}
	for _, a := range audiences {
		v.audiences[a] = struct{}{}
	}
	v.revocations.Store(EmptyRevocations())
	return v, nil
}

// SetPublicKeyPEM 热更新验签公钥（X.509 证书或 PEM 公钥）。
// 解析失败返回错误且保留旧钥（last-known-good）。
func (v *Verifier) SetPublicKeyPEM(pemBytes []byte) error {
	key, err := jwt.ParseRSAPublicKeyFromPEM(pemBytes)
	if err != nil {
		return fmt.Errorf("authn: parse public key: %w", err)
	}
	v.key.Store(key)
	return nil
}

// SetRevocations 热更新撤销名单（整表原子替换）。
func (v *Verifier) SetRevocations(t *RevocationTable) {
	if t == nil {
		t = EmptyRevocations()
	}
	v.revocations.Store(t)
}

// Revocations 返回当前撤销名单（供指标与测试使用）。
func (v *Verifier) Revocations() *RevocationTable {
	return v.revocations.Load()
}

// Verify 校验 token 并返回 claims。全部条件按 docs/design/auth.md：
// RS256 签名、iss、aud 白名单、tokenType=access-token、sub/exp 必填、60s leeway、
// 账户未注销未禁用、未被撤销。
func (v *Verifier) Verify(tokenString string, now time.Time) (*Claims, error) {
	key := v.key.Load()
	if key == nil {
		return nil, ErrNoKey
	}

	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenString, claims,
		func(t *jwt.Token) (any, error) { return key, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithLeeway(Leeway),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTokenInvalid, err)
	}

	// 信任域绑定（终裁 P0-C）。
	if claims.Issuer != v.issuer {
		return nil, fmt.Errorf("%w: got %q", ErrWrongIssuer, claims.Issuer)
	}
	if !v.audienceAllowed(claims.Audience) {
		return nil, fmt.Errorf("%w: got %v", ErrWrongAud, []string(claims.Audience))
	}
	if claims.TokenType != TokenTypeAccess {
		return nil, fmt.Errorf("%w: tokenType=%q", ErrNotAccess, claims.TokenType)
	}
	if claims.Subject == "" {
		return nil, fmt.Errorf("%w: empty sub", ErrTokenInvalid)
	}
	if claims.IsDeleted || claims.IsForbidden {
		return nil, ErrAccountGone
	}

	// 撤销查表（终裁 P0-D：秒级生效，热路径纯内存）。
	if reason, revoked := v.revocations.Load().Revoked(claims, now); revoked {
		return nil, fmt.Errorf("%w: %s", ErrRevoked, reason)
	}
	return claims, nil
}

func (v *Verifier) audienceAllowed(aud jwt.ClaimStrings) bool {
	for _, a := range aud {
		if _, ok := v.audiences[a]; ok {
			return true
		}
	}
	return false
}
