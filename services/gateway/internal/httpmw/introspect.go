package httpmw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lens077/control-tower/services/gateway/internal/authn"
)

// Introspector 对高危路由做实时在线校验（fail-close：任何错误都拒绝）。
type Introspector interface {
	// Check 校验 token 当前有效性；返回非 nil 即拒绝。
	Check(ctx context.Context, rawToken string, claims *authn.Claims) error
}

// ErrIntrospectDisabled 表示未配置在线校验凭据。
// 高危路由在未配置时必须拒绝（fail-close），而不是静默放行。
var ErrIntrospectDisabled = errors.New("httpmw: online check required but introspector not configured")

// Disabled 是未配置凭据时的占位实现：恒拒绝。
type Disabled struct{}

// Check 实现 Introspector。
func (Disabled) Check(context.Context, string, *authn.Claims) error {
	return ErrIntrospectDisabled
}

// CasdoorIntrospector 调 Casdoor OAuth introspection（RFC 7662）。
// 约束（docs/design/auth.md）：超时 ≤2s、禁止重试、错误 fail-close。
// 凭据经环境/Secret 注入；端点可用性在 P3 用真实 Casdoor 验证。
type CasdoorIntrospector struct {
	// Endpoint 形如 https://casdoor.apikv.com/api/login/oauth/introspect。
	Endpoint     string
	ClientID     string
	ClientSecret string
	// HTTPClient 可注入测试替身；nil 用带 2s 超时的默认 client。
	HTTPClient *http.Client
}

// Check 实现 Introspector。
func (c *CasdoorIntrospector) Check(ctx context.Context, rawToken string, _ *authn.Claims) error {
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	form := url.Values{
		"token":           {rawToken},
		"token_type_hint": {"access_token"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("httpmw: introspect request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.ClientID, c.ClientSecret)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("httpmw: introspect call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("httpmw: introspect status %d", resp.StatusCode)
	}
	var body struct {
		Active bool `json:"active"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("httpmw: introspect decode: %w", err)
	}
	if !body.Active {
		return errors.New("httpmw: token inactive")
	}
	return nil
}
