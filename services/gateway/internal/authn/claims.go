// Package authn 实现网关认证：RS256 JWT 本地验签（信任域绑定）+ 撤销名单查表。
// 设计依据：docs/design/auth.md。
package authn

import (
	"github.com/golang-jwt/jwt/v5"
)

// TokenTypeAccess 是 Casdoor access token 的 tokenType 取值。
// Casdoor 的 refresh token 与 access token 用同一把 RS256 钥签发，
// 因此必须显式校验 tokenType，否则长效 refresh token 可冒充 access token（终裁 P0-C）。
const TokenTypeAccess = "access-token"

// Role 是 Casdoor 嵌入 claims 的角色对象（形状以 P3 真实 token fixture 为准）。
type Role struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

// Claims 是网关消费的 Casdoor JWT claims 子集。
type Claims struct {
	Owner       string `json:"owner"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	TokenType   string `json:"tokenType"`
	IsDeleted   bool   `json:"isDeleted"`
	IsForbidden bool   `json:"isForbidden"`
	Roles       []Role `json:"roles"`
	jwt.RegisteredClaims
}

// RoleNames 返回全部角色名。授权语义：任一角色允许即放行（多角色显式定义，
// 不再沿用旧网关只取第一个角色的行为）。
func (c *Claims) RoleNames() []string {
	names := make([]string, 0, len(c.Roles))
	for _, r := range c.Roles {
		if r.Name != "" {
			names = append(names, r.Name)
		}
	}
	return names
}

// UserID 返回用户唯一标识（Casdoor 把用户 ID 放在标准 sub）。
func (c *Claims) UserID() string { return c.Subject }
