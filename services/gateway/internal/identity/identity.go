// Package identity 管理可信身份头：入站无条件剥离、鉴权后注入。
//
// 安全边界（沿旧网关，下游 10 服务裸信这些头、自己不验签）：
//   - 任何以 x-md- 为前缀的入站头都必须在进入路由/鉴权前删除，否则客户端可伪造身份；
//   - 注入动作发生在 ReverseProxy.Rewrite 内（晚于标准库的 hop-by-hop 头删除，
//     客户端无法用 Connection: x-md-global-* 触发标准库替我们删掉可信头）。
package identity

import (
	"net/http"
	"strings"

	"github.com/lens077/control-tower/services/gateway/internal/authn"
)

// 头名与旧网关逐字一致（.service-matrix.yaml 与 10 个后端服务依赖该契约）。
const (
	HeaderUserID = "x-md-global-user-id"
	HeaderName   = "x-md-global-name"
	HeaderRole   = "x-md-global-role"
	HeaderOwner  = "x-md-global-owner"

	// prefix 覆盖全部保留身份头命名空间。
	prefix = "x-md-"
)

// Strip 删除全部 x-md-* 前缀的入站头（大小写不敏感）。匿名与非匿名路径都必须先剥离。
func Strip(h http.Header) {
	for name := range h {
		if strings.HasPrefix(strings.ToLower(name), prefix) {
			delete(h, name)
		}
	}
}

// Inject 按已验签的 claims 注入可信身份头。
// 多角色以逗号拼接（消费方按 CSV 解析；旧网关单角色行为是其缺陷，不保留）。
func Inject(h http.Header, c *authn.Claims) {
	h.Set(HeaderUserID, c.UserID())
	h.Set(HeaderName, c.Name)
	h.Set(HeaderOwner, c.Owner)
	if roles := c.RoleNames(); len(roles) > 0 {
		h.Set(HeaderRole, strings.Join(roles, ","))
	} else {
		h.Del(HeaderRole)
	}
}
