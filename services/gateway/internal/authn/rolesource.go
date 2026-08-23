package authn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// RoleSource 在 access token claims 不携带角色时按需补齐。
// 背景（P3 真 token 实测）：本部署的 Casdoor JWT 不嵌 roles——这正是旧网关
// 每请求回源 get-user 的原因；docs/design/auth.md 的回退分支在此落地。
type RoleSource interface {
	// Roles 返回用户角色名集合；错误语义见实现。
	Roles(ctx context.Context, owner, name string) ([]string, error)
}

// CasdoorRoleSource 调 Casdoor get-user（与旧网关同一无鉴权 GET 端点）+ 进程内 TTL 缓存。
//
// 容错语义：
//   - 缓存新鲜 → 直接返回（含空角色的负缓存，防对不存在角色的用户反复回源）；
//   - 回源失败且有过期缓存 → 返回过期值并计数（Casdoor 抖动不放大为全站 403）；
//   - 回源失败且无缓存 → 返回错误（上层按无角色处理 → Casbin 自然拒绝，收窄不放大）。
//
// 撤权时效不依赖本缓存：撤销名单独立生效（秒级）；角色降级的时效 = min(缓存 TTL, token TTL)。
type CasdoorRoleSource struct {
	base   string
	client *http.Client
	ttl    time.Duration

	mu    sync.RWMutex
	cache map[string]roleEntry
	sf    singleflight.Group

	// StaleServed 供观测：过期缓存兜底次数。
	staleServed int64
}

type roleEntry struct {
	roles []string
	exp   time.Time
}

// NewCasdoorRoleSource 构造角色源。ttl 建议 5 分钟（旧网关 L1 口径）。
func NewCasdoorRoleSource(baseURL string, ttl time.Duration) *CasdoorRoleSource {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &CasdoorRoleSource{
		base:   baseURL,
		client: &http.Client{Timeout: 3 * time.Second},
		ttl:    ttl,
		cache:  map[string]roleEntry{},
	}
}

// Roles 实现 RoleSource。
func (s *CasdoorRoleSource) Roles(ctx context.Context, owner, name string) ([]string, error) {
	key := owner + "/" + name

	s.mu.RLock()
	e, ok := s.cache[key]
	s.mu.RUnlock()
	now := time.Now()
	if ok && now.Before(e.exp) {
		return e.roles, nil
	}

	v, err, _ := s.sf.Do(key, func() (any, error) {
		roles, ferr := s.fetch(ctx, key)
		if ferr != nil {
			return nil, ferr
		}
		s.mu.Lock()
		s.cache[key] = roleEntry{roles: roles, exp: time.Now().Add(s.ttl)}
		s.mu.Unlock()
		return roles, nil
	})
	if err != nil {
		if ok { // 过期缓存兜底
			s.mu.Lock()
			s.staleServed++
			s.mu.Unlock()
			return e.roles, nil
		}
		return nil, err
	}
	return v.([]string), nil
}

// StaleServedCount 供指标/测试。
func (s *CasdoorRoleSource) StaleServedCount() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.staleServed
}

func (s *CasdoorRoleSource) fetch(ctx context.Context, id string) ([]string, error) {
	u := s.base + "/api/get-user?id=" + url.QueryEscape(id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("authn: get-user: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("authn: get-user status %d", resp.StatusCode)
	}

	// Casdoor 两种响应形态：{"status":"ok","data":{...,"roles":[...]}} 或直接用户对象。
	var body struct {
		Status string `json:"status"`
		Data   *struct {
			Roles []Role `json:"roles"`
		} `json:"data"`
		Roles []Role `json:"roles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("authn: get-user decode: %w", err)
	}
	src := body.Roles
	if body.Data != nil {
		src = body.Data.Roles
	}
	out := make([]string, 0, len(src))
	for _, r := range src {
		if r.Name != "" {
			out = append(out, r.Name)
		}
	}
	return out, nil
}
