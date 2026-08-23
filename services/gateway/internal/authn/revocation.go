package authn

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// RevocationEntry 是撤销名单的一条记录。expires_at 由 config 服务按
// 「撤销时刻 + 最大 token TTL + leeway」派生（禁止手填过短，否则被撤 token 会复活）；
// 网关侧仍防御性剔除已过期条目。
type RevocationEntry struct {
	// Sub 匹配 claims.sub。与 Jti 二选一必填。
	Sub string `yaml:"sub"`
	// Jti 精确匹配单枚 token（泄露场景）。
	Jti string `yaml:"jti"`
	// All 为 true 时无视签发时间拒绝该 sub 的一切 token（封禁兜底）。
	All bool `yaml:"all"`
	// IssuedBefore：早于该时刻签发的 token 一律拒绝（改角色/强制换发场景）。
	IssuedBefore time.Time `yaml:"issued_before"`
	// ExpiresAt：条目自身的过期时刻，过期后自动失效并被剔除。
	ExpiresAt time.Time `yaml:"expires_at"`
	// Reason 记录撤销原因，回显在拒绝响应的 X-Error-Reason 细节里。
	Reason string `yaml:"reason"`
}

// RevocationTable 是构建后只读的撤销名单；热更新按整表原子替换。
type RevocationTable struct {
	bySub map[string][]RevocationEntry
	byJti map[string]RevocationEntry
}

type revocationFile struct {
	Revocations []RevocationEntry `yaml:"revocations"`
}

// ParseRevocations 解析 auth/revocations.yaml。已过期条目直接剔除；
// 既无 sub 也无 jti 的条目视为配置错误（静默忽略会让撤销「看似生效实则没生效」）。
func ParseRevocations(raw []byte, now time.Time) (*RevocationTable, error) {
	var f revocationFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("authn: parse revocations: %w", err)
	}
	t := &RevocationTable{
		bySub: make(map[string][]RevocationEntry),
		byJti: make(map[string]RevocationEntry),
	}
	for i, e := range f.Revocations {
		if e.Sub == "" && e.Jti == "" {
			return nil, fmt.Errorf("authn: revocations[%d] needs sub or jti", i)
		}
		if !e.ExpiresAt.IsZero() && !e.ExpiresAt.After(now) {
			continue // 已过期
		}
		if e.Jti != "" {
			t.byJti[e.Jti] = e
			continue
		}
		t.bySub[e.Sub] = append(t.bySub[e.Sub], e)
	}
	return t, nil
}

// EmptyRevocations 返回空表（启动时名单键尚未就绪的安全默认）。
func EmptyRevocations() *RevocationTable {
	return &RevocationTable{
		bySub: map[string][]RevocationEntry{},
		byJti: map[string]RevocationEntry{},
	}
}

// Revoked 判定该 claims 是否被撤销，返回命中的原因。
func (t *RevocationTable) Revoked(c *Claims, now time.Time) (string, bool) {
	if c.ID != "" {
		if e, ok := t.byJti[c.ID]; ok && entryAlive(e, now) {
			return reasonOf(e), true
		}
	}
	sub := c.Subject
	for _, e := range t.bySub[sub] {
		if !entryAlive(e, now) {
			continue
		}
		if e.All {
			return reasonOf(e), true
		}
		if !e.IssuedBefore.IsZero() && c.IssuedAt != nil && c.IssuedAt.Time.Before(e.IssuedBefore) {
			return reasonOf(e), true
		}
	}
	return "", false
}

// Len 返回有效条目数（供新鲜度指标使用）。
func (t *RevocationTable) Len() int {
	n := len(t.byJti)
	for _, es := range t.bySub {
		n += len(es)
	}
	return n
}

func entryAlive(e RevocationEntry, now time.Time) bool {
	return e.ExpiresAt.IsZero() || e.ExpiresAt.After(now)
}

func reasonOf(e RevocationEntry) string {
	if e.Reason != "" {
		return e.Reason
	}
	return "TOKEN_REVOKED"
}
