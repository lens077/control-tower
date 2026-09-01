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

	// HeaderAnonymous 标记「本请求的 user-id 属于访客而非注册用户」。
	// ⚠️ 下游服务判定「必须登录」的 RPC 时**必须**检查这个头——访客也有 user-id，
	// 只看它非空会把访客放进下单/支付链路。
	// 它同属 x-md- 前缀，故与其他身份头一样被 Strip 无条件剥离，客户端伪造不了。
	HeaderAnonymous = "x-md-global-anonymous"

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

// InjectGuest 注入访客身份：只有 user-id 与匿名标记，没有名字/角色/租户。
// guestID 由网关签发（128 位随机不透明 ID，与会话 id 同一强度模型）。
//
// 显式清掉 name/role/owner：这些字段对访客无意义，留下残值会让下游把访客
// 误判成「有角色的用户」——RBAC 是按角色判的，漏一个 Del 就是越权。
func InjectGuest(h http.Header, guestID string) {
	h.Set(HeaderUserID, guestID)
	h.Set(HeaderAnonymous, "true")
	h.Del(HeaderName)
	h.Del(HeaderRole)
	h.Del(HeaderOwner)
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
