// Package guest 管理匿名购物的访客身份（B 级 RPC）。
//
// 设计依据：ecommerce docs/design/platform/anonymous-shopping.md。
//
// 身份模型刻意与 BFF 会话同构：**128 位以上随机不透明 ID**，不做 HMAC 签名。
// 理由是签名在这里买不到额外安全性——访客 ID 唯一能解锁的资源是它自己的购物车，
// 既无 PII 也无资金，而 32 字节 CSPRNG 的不可猜性已经等价于会话 id 的强度模型。
// 引入签名反而要新增密钥管理与轮换面。若将来访客身份要承载更多资源，再回头加签。
//
// 边界：本包只负责「发/读 cookie」与「生成 ID」，不决定哪些路径需要访客身份——
// 那是路由表（router.Table.IsGuest）的职责。
package guest

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"time"
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

// NewID 生成 32 字节随机访客 id（与 session.NewID 同强度）。
func NewID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// FromRequest 读取请求里的访客 id；没有或为空返回空串。
func (c CookieConfig) FromRequest(r *http.Request) string {
	ck, err := r.Cookie(c.Name)
	if err != nil || ck.Value == "" {
		return ""
	}
	return ck.Value
}

// Issue 把访客 id 写入响应 cookie。
//
// HttpOnly 恒为 true：访客 id 是身份凭据，没有任何前端脚本需要读它——
// 购物车数据一律经后端返回，JS 拿到这个值只会扩大 XSS 的战果。
func (c CookieConfig) Issue(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     c.Name,
		Value:    id,
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
