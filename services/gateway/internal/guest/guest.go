// Package guest 管理匿名购物的访客身份（B 级 RPC）。
//
// 设计依据：ecommerce docs/design/platform/anonymous-shopping.md。
//
// 身份模型与 BFF 会话同构：**128 位以上随机不透明 ID**，并额外带 HMAC 签名。
//
// ⚠️ 签名是后补的，理由值得记下来。最初的判断是「签名买不到额外安全性——访客 ID
// 唯一能解锁的资源是它自己的购物车」。这个推理有一个隐含前提：访客 ID 只能指向
// 访客自己。而它不成立，因为访客 ID 刻意采用了 UUID 形态（见 NewID 的注释：
// 为了让下游 cart 服务零改动地 uuid.Parse 后写进 UUID 列），于是它与**真实用户
// ID 共用同一个取值空间**。两个各自合理的决定叠在一起，结果是：任何人把
// cookie 改成受害者的用户 UUID，网关就会把「受害者」当作访客身份注入下游，
// 从而读写受害者的购物车（横向越权）。
//
// 因此现在的规则是：cookie 值必须是「<uuid>.<HMAC>」，只有本网关签发过的
// ID 才被接受，未签名或签名不符的一律当作没有并重新签发。
//
// 边界：本包只负责「发/读 cookie」与「生成 ID」，不决定哪些路径需要访客身份——
// 那是路由表（router.Table.IsGuest）的职责。
package guest

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// TTL 是访客 cookie 的有效期。30 天与「购物车留存」的产品预期一致：
// 比它短会让用户隔周回来发现车空了，比它长则徒增无主购物车的清理压力。
const TTL = 30 * 24 * time.Hour

// CookieConfig 与 bff.CookieConfig 同形，但刻意不复用那个类型：
// 会话 cookie 与访客 cookie 的安全属性可以不同（例如将来访客放宽 SameSite
// 以支持跨站商品页嵌入），共用一个类型会让两者被迫同步演进。
type CookieConfig struct {
	Name     string
	Domain   string
	Path     string
	Secure   bool
	SameSite http.SameSite

	// Key 是访客 cookie 的签名密钥。**必须非空**，且同一部署的所有副本必须一致，
	// 否则副本之间会互相不认对方签发的访客身份（表现为购物车随机丢失）。
	// 为空时 FromRequest 一律返回空串（fail closed）——宁可让访客身份失效，
	// 也不接受未经验证的客户端取值。
	Key []byte
}

// DefaultCookieConfig 返回生产缺省值。
func DefaultCookieConfig() CookieConfig {
	return CookieConfig{
		Name:     "__Secure-ct_guest",
		Path:     "/",
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
}

// NewID 生成访客 id，形态是 **UUID v4**。
//
// 刻意用 UUID 而非 session.NewID 那样的 base64 随机串：下游 cart 服务把
// x-md-global-user-id 直接 uuid.Parse 后写进 cart_item.user_id（UUID NOT NULL 列）。
// 用 UUID 形态意味着**数据库零改动、cart 的解析逻辑零改动**——访客 ID 天然落得进去。
// v4 是 122 位 CSPRNG 随机，不可猜性对「只解锁自己购物车」这个用途绰绰有余。
//
// ⚠️ 因此该列里会同时存在真实用户 ID 与访客 ID，两者靠 x-md-global-anonymous
// 头区分，而不是靠值的形状——不要试图从 UUID 本身反推身份类型。
func NewID() (string, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// sign 返回 cookie 的携带形态「<id>.<base64url(HMAC-SHA256(key, id))>」。
func (c CookieConfig) sign(id string) string {
	mac := hmac.New(sha256.New, c.Key)
	mac.Write([]byte(id))
	return id + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// FromRequest 读取并**验证**请求里的访客 id。
//
// 任何一项不满足都返回空串（调用方据此重新签发一个全新访客身份）：
// cookie 不存在、形态不对、id 不是合法 UUID、签名不符、密钥未配置。
//
// 返回值是裸 id（不含签名），因为下游拿到的 x-md-global-user-id 必须能直接
// uuid.Parse。签名只用于证明「这个 id 是本网关签发的」，不出网关。
func (c CookieConfig) FromRequest(r *http.Request) string {
	if len(c.Key) == 0 {
		return "" // 未配置密钥：无法验证，一律不认。
	}
	ck, err := r.Cookie(c.Name)
	if err != nil || ck.Value == "" {
		return ""
	}
	// 用最后一个「.」分隔：UUID 本身不含「.」，但这样对将来的 id 形态更宽容。
	dot := strings.LastIndexByte(ck.Value, '.')
	if dot <= 0 {
		return "" // 无签名的历史 cookie 落在这里，按「没有」处理。
	}
	id := ck.Value[:dot]
	if _, perr := uuid.Parse(id); perr != nil {
		return ""
	}
	// 恒时比较，避免签名被逐字节试探。
	if !hmac.Equal([]byte(ck.Value), []byte(c.sign(id))) {
		return ""
	}
	return id
}

// Issue 把访客 id 写入响应 cookie。
//
// HttpOnly 恒为 true：访客 id 是身份凭据，没有任何前端脚本需要读它——
// 购物车数据一律经后端返回，JS 拿到这个值只会扩大 XSS 的战果。
func (c CookieConfig) Issue(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     c.Name,
		Value:    c.sign(id),
		Path:     c.Path,
		Domain:   c.Domain,
		Secure:   c.Secure,
		HttpOnly: true,
		SameSite: c.SameSite,
		Expires:  time.Now().Add(TTL),
		MaxAge:   int(TTL.Seconds()),
	})
}

// Clear 删除访客 cookie。登录成功后必须调用：同一浏览器同时持有登录会话与访客
// 身份会让「合并购物车」反复触发，也让排障时分不清请求到底算谁的。
func (c CookieConfig) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     c.Name,
		Value:    "",
		Path:     c.Path,
		Domain:   c.Domain,
		Secure:   c.Secure,
		HttpOnly: true,
		SameSite: c.SameSite,
		MaxAge:   -1,
	})
}
