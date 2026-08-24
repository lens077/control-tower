// Package bff 实现 BFF 面：网关自己完成 OAuth 流程，浏览器只拿不透明 session id（ADR-0002）。
package bff

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Casdoor 端点路径。取自前端既有 PKCE 实现（该部署实测可用）：
// refresh **不走**标准 token 端点，是 Casdoor 特有的独立路径。
const (
	pathAuthorize = "/login/oauth/authorize"
	pathToken     = "/api/login/oauth/access_token"
	pathRefresh   = "/api/login/oauth/refresh_token"
	pathLogout    = "/api/logout"
	oauthScope    = "openid profile email"
)

// Tokens 是 Casdoor 令牌端点的响应子集。
type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// ExpiresAt 换算绝对过期时刻；缺省给 5 分钟保底（宁可多续几次，不拿过期令牌）。
func (t *Tokens) ExpiresAt(now time.Time) time.Time {
	secs := t.ExpiresIn
	if secs <= 0 {
		secs = 300
	}
	return now.Add(time.Duration(secs) * time.Second)
}

// CasdoorClient 是机密客户端——与前端的公开客户端不同，它持 client secret。
type CasdoorClient struct {
	BaseURL      string
	ClientID     string
	ClientSecret string
	HTTP         *http.Client
}

// NewCasdoorClient 构造客户端。
func NewCasdoorClient(baseURL, clientID, clientSecret string) *CasdoorClient {
	return &CasdoorClient{
		BaseURL:      strings.TrimSuffix(baseURL, "/"),
		ClientID:     clientID,
		ClientSecret: clientSecret,
		HTTP:         &http.Client{Timeout: 8 * time.Second},
	}
}

// AuthorizeURL 构造授权跳转地址。
func (c *CasdoorClient) AuthorizeURL(redirectURI, state string) string {
	q := url.Values{
		"client_id":     {c.ClientID},
		"response_type": {"code"},
		"redirect_uri":  {redirectURI},
		"scope":         {oauthScope},
		"state":         {state},
	}
	return c.BaseURL + pathAuthorize + "?" + q.Encode()
}

// LogoutURL 是 Casdoor 侧结束会话的地址（可选联动）。
func (c *CasdoorClient) LogoutURL() string { return c.BaseURL + pathLogout }

// Exchange 用授权码换令牌（机密客户端，带 client_secret）。
func (c *CasdoorClient) Exchange(ctx context.Context, code, redirectURI string) (*Tokens, error) {
	return c.post(ctx, pathToken, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {c.ClientID},
		"client_secret": {c.ClientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	})
}

// Refresh 续期。失败即视为 IdP 侧已否定该用户（账户禁用/会话撤销），调用方应删会话。
func (c *CasdoorClient) Refresh(ctx context.Context, refreshToken string) (*Tokens, error) {
	return c.post(ctx, pathRefresh, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {c.ClientID},
		"client_secret": {c.ClientSecret},
		"refresh_token": {refreshToken},
		"scope":         {oauthScope},
	})
}

// Refresher 适配 httpmw 的 SessionRefresher 接口（结构化满足，无需互相 import）。
type Refresher struct {
	Client *CasdoorClient
	Now    func() time.Time
}

// Refresh 实现续期。
func (r Refresher) Refresh(ctx context.Context, refreshToken string) (string, string, time.Time, error) {
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	t, err := r.Client.Refresh(ctx, refreshToken)
	if err != nil {
		return "", "", time.Time{}, err
	}
	return t.AccessToken, t.RefreshToken, t.ExpiresAt(now()), nil
}

func (c *CasdoorClient) post(ctx context.Context, path string, form url.Values) (*Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bff: casdoor %s: %w", path, err)
	}
	defer resp.Body.Close()

	var t Tokens
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return nil, fmt.Errorf("bff: casdoor %s decode: %w", path, err)
	}
	if t.Error != "" {
		msg := t.ErrorDesc
		if msg == "" {
			msg = t.Error
		}
		return nil, fmt.Errorf("bff: casdoor %s: %s", path, msg)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bff: casdoor %s status %d", path, resp.StatusCode)
	}
	if t.AccessToken == "" {
		return nil, fmt.Errorf("bff: casdoor %s: empty access_token", path)
	}
	return &t, nil
}
