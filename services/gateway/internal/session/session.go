// Package session 管理 BFF 的服务端会话（ADR-0002）。
//
// 浏览器只持有一枚不透明 session id（httpOnly cookie），桌面端以 header 携带同一枚 id；
// access/refresh token 与角色都保管在服务端，绝不出网关。
//
// 撤权语义：删会话即时生效——这是本方案取代「撤销名单 + 短 TTL」的核心理由。
// 因此**故意不做会话查询的本地缓存**：任何缓存都会把「即时」打回「缓存 TTL 级」。
package session

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"
)

// ErrNotFound 表示会话不存在或已过期（调用方一律按未认证处理）。
var ErrNotFound = errors.New("session: not found")

// Session 是服务端会话。JSON 标签用于存储序列化。
type Session struct {
	ID    string `json:"id"`
	Sub   string `json:"sub"`
	Owner string `json:"owner"`
	Name  string `json:"name"`
	// Roles 在登录与每次续期时刷新，消除每请求回源 Casdoor。
	Roles []string `json:"roles"`
	// AccessToken/RefreshToken 仅供网关服务端续期与吊销联动使用。
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	AccessExpiry time.Time `json:"access_expiry"`
	CreatedAt    time.Time `json:"created_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
	// UserAgent 仅存摘要用途的原文，供会话清单展示。
	UserAgent string `json:"user_agent,omitempty"`
}

// TTL 是会话的两条生命线：空闲上限与绝对上限。
type TTL struct {
	// Idle 是「多久没活动就失效」，每次访问续期。
	Idle time.Duration
	// Absolute 是「从创建起最长活多久」，不因活动而延长。
	Absolute time.Duration
}

// DefaultTTL 是缺省取值。
func DefaultTTL() TTL {
	return TTL{Idle: 12 * time.Hour, Absolute: 7 * 24 * time.Hour}
}

// Expired 判定会话是否已越过绝对上限。
func (s *Session) Expired(ttl TTL, now time.Time) bool {
	return now.Sub(s.CreatedAt) >= ttl.Absolute
}

// Store 是会话存储抽象。生产用 Dragonfly，测试与无依赖的本地开发用内存实现。
type Store interface {
	// Create 落库新会话。
	Create(ctx context.Context, s *Session) error
	// Get 取会话；不存在或已过期返回 ErrNotFound。命中时顺带续期空闲 TTL。
	Get(ctx context.Context, id string) (*Session, error)
	// Save 覆盖写回（续期 token、刷新角色后调用）。
	Save(ctx context.Context, s *Session) error
	// Delete 删除单个会话——**撤权的正式手段**。
	Delete(ctx context.Context, id string) error
	// ListByUser 列出某用户的活跃会话（会话清单/设备管理能力）。
	ListByUser(ctx context.Context, sub string) ([]*Session, error)
	// DeleteByUser 踢掉某用户的全部会话，返回删除条数（封禁用）。
	DeleteByUser(ctx context.Context, sub string) (int, error)
	// Ping 供 readyz 判定存储可达；不可达即摘流量（fail-closed，见 ADR-0002）。
	Ping(ctx context.Context) error

	// PutState 暂存 OAuth state（短 TTL）。
	// 浏览器流程把 state 放 httpOnly cookie 即可；**原生客户端流程必须存服务端**——
	// Tauri 的登录子窗口是独立 WebView，cookie 存储行为不由我们掌控，
	// 依赖它会得到「missing oauth state」（2026-08-24 真机实测踩过）。
	PutState(ctx context.Context, state string, payload []byte, ttl time.Duration) error
	// TakeState 取出并**立即删除** state（单次使用，防重放）。不存在返回 ErrNotFound。
	TakeState(ctx context.Context, state string) ([]byte, error)
}

// NewID 生成 32 字节随机 session id。
func NewID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
