// Package configadmin 是 Config Center 的**管理面**客户端：读、写、列举 key。
//
// 与 sdk/configsource 的分工：configsource 是业务进程启动时拉 Bootstrap 的只读数据面，
// 只认 service token；configadmin 面向运维与 CI，要能 PutKey，因此支持两种凭据：
//
//   - operator machine token（`x-config-center-service-token`，role=operator）：
//     只能在自身 environment 内读写，见 docs/design/machine-token.md。
//   - 管理员 Casdoor JWT（`Authorization: Bearer`）：全量权限。
//
// 服务端按 format 校验语法，所以写之前本地不做二次解析——避免两套校验各说各话。
package configadmin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"connectrpc.com/connect"
	configv1 "github.com/lens077/control-tower/api/config/v1"
	"github.com/lens077/control-tower/api/config/v1/configv1connect"
	"github.com/lens077/control-tower/constants"
)

// Credential 是调用方身份。两者都给时用 machine token（更窄的那个）。
type Credential struct {
	// MachineToken 走 x-config-center-service-token，需 role=operator 才能写。
	MachineToken string
	// BearerToken 是管理员 Casdoor JWT，走 Authorization: Bearer。
	BearerToken string
}

func (c Credential) empty() bool {
	return strings.TrimSpace(c.MachineToken) == "" && strings.TrimSpace(c.BearerToken) == ""
}

// Client 绑定一个 Config Center 端点与一份凭据。零值不可用，走 New。
type Client struct {
	rpc        configv1connect.ConfigServiceClient
	credential Credential
	clientName string
}

// Option 调整传输细节。
type Option func(*options)

type options struct {
	httpClient connect.HTTPClient
	clientName string
}

// WithHTTPClient 替换传输（测试注入、代理、自定义 TLS）。
func WithHTTPClient(client connect.HTTPClient) Option {
	return func(o *options) { o.httpClient = client }
}

// WithClientName 覆盖 x-config-center-client-name，用于在 /connections 与审计里认人。
func WithClientName(name string) Option {
	return func(o *options) { o.clientName = name }
}

// New 构造客户端。endpoint 形如 https://config-api.apikv.com。
func New(endpoint string, credential Credential, opts ...Option) (*Client, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, errors.New("endpoint is required")
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		return nil, fmt.Errorf("endpoint %q must start with http:// or https://", endpoint)
	}
	if credential.empty() {
		return nil, errors.New("a machine token or a bearer token is required")
	}

	resolved := options{
		// WatchKeys 不在本包范围内，所以可以给一个有限超时；长流走 configsource。
		httpClient: &http.Client{Timeout: 30 * time.Second},
		clientName: "configctl",
	}
	for _, opt := range opts {
		opt(&resolved)
	}
	return &Client{
		rpc:        configv1connect.NewConfigServiceClient(resolved.httpClient, strings.TrimSuffix(endpoint, "/")),
		credential: credential,
		clientName: resolved.clientName,
	}, nil
}

// Selector 定位唯一一个 key。
type Selector struct {
	Namespace   string
	Environment string
	Key         string
}

func (s Selector) String() string {
	return fmt.Sprintf("%s/%s/%s", s.Namespace, s.Environment, s.Key)
}

func (s Selector) validate() error {
	if strings.TrimSpace(s.Namespace) == "" {
		return errors.New("namespace is required")
	}
	if strings.TrimSpace(s.Environment) == "" {
		return errors.New("environment is required")
	}
	if strings.TrimSpace(s.Key) == "" {
		return errors.New("key is required")
	}
	return nil
}

// PutRequest 是一次写入。Format 为零值时由服务端按 UNSPECIFIED 处理，
// 所以调用方应显式给出（CLI 会按文件后缀推断）。
type PutRequest struct {
	Selector
	Format      configv1.ConfigFormat
	Value       string
	Comment     string
	Description string
	IsSecret    bool
}

// Entry 是写入或读取后的条目快照。
type Entry struct {
	Selector
	Format      configv1.ConfigFormat
	Value       string
	Version     int32
	IsSecret    bool
	Description string
	UpdatedBy   string
}

func entryFrom(message *configv1.ConfigEntry) Entry {
	return Entry{
		Selector: Selector{
			Namespace:   message.GetNamespace(),
			Environment: message.GetEnvironment(),
			Key:         message.GetKey(),
		},
		Format:      message.GetFormat(),
		Value:       message.GetValue(),
		Version:     message.GetVersion(),
		IsSecret:    message.GetIsSecret(),
		Description: message.GetDescription(),
		UpdatedBy:   message.GetUpdatedBy(),
	}
}

// ErrNotFound 表示 key 不存在。Put 的前后对比与 CLI 的「新建还是更新」都靠它区分。
var ErrNotFound = errors.New("config key not found")

// Get 读取 key 当前值。key 不存在时返回 ErrNotFound。
func (c *Client) Get(ctx context.Context, selector Selector) (Entry, error) {
	if err := selector.validate(); err != nil {
		return Entry{}, err
	}
	response, err := c.rpc.GetKey(ctx, newRequest(c, &configv1.GetKeyRequest{
		Namespace:   selector.Namespace,
		Environment: selector.Environment,
		Key:         selector.Key,
	}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return Entry{}, fmt.Errorf("%s: %w", selector, ErrNotFound)
		}
		return Entry{}, fmt.Errorf("get %s: %w", selector, err)
	}
	return entryFrom(response.Msg.GetEntry()), nil
}

// Put 创建或更新 key，返回写入后的条目（含新版本号）。
func (c *Client) Put(ctx context.Context, request PutRequest) (Entry, error) {
	if err := request.validate(); err != nil {
		return Entry{}, err
	}
	response, err := c.rpc.PutKey(ctx, newRequest(c, &configv1.PutKeyRequest{
		Namespace:   request.Namespace,
		Environment: request.Environment,
		Key:         request.Key,
		Format:      request.Format,
		Value:       request.Value,
		Comment:     request.Comment,
		Description: request.Description,
		IsSecret:    request.IsSecret,
	}))
	if err != nil {
		return Entry{}, fmt.Errorf("put %s: %w", request.Selector, err)
	}
	return entryFrom(response.Msg.GetEntry()), nil
}

func (r PutRequest) validate() error {
	if err := r.Selector.validate(); err != nil {
		return err
	}
	if r.Value == "" {
		// 空值写入几乎总是「文件路径写错了」，不是刻意为之。
		return fmt.Errorf("value for %s is empty", r.Selector)
	}
	if _, ok := configv1.ConfigFormat_name[int32(r.Format)]; !ok {
		return fmt.Errorf("unknown config format %d", r.Format)
	}
	return nil
}

// ListKeys 列出 namespace×environment 下的 key 元数据（不含 value）。
func (c *Client) ListKeys(ctx context.Context, namespace, environment, keyPrefix string) ([]Entry, error) {
	if strings.TrimSpace(namespace) == "" || strings.TrimSpace(environment) == "" {
		return nil, errors.New("namespace and environment are required")
	}
	response, err := c.rpc.ListKeys(ctx, newRequest(c, &configv1.ListKeysRequest{
		Namespace:   namespace,
		Environment: environment,
		KeyPrefix:   keyPrefix,
	}))
	if err != nil {
		return nil, fmt.Errorf("list keys in %s/%s: %w", namespace, environment, err)
	}
	entries := make([]Entry, 0, len(response.Msg.GetEntries()))
	for _, item := range response.Msg.GetEntries() {
		entries = append(entries, entryFrom(item))
	}
	return entries, nil
}

// Namespace 是一个 namespace 及其下已有配置的 environment。
type Namespace struct {
	Name         string
	Environments []string
}

// ListNamespaces 列出已有 namespace，用于写之前确认拼写。
func (c *Client) ListNamespaces(ctx context.Context) ([]Namespace, error) {
	response, err := c.rpc.ListNamespaces(ctx, newRequest(c, &configv1.ListNamespacesRequest{}))
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}
	namespaces := make([]Namespace, 0, len(response.Msg.GetNamespaces()))
	for _, item := range response.Msg.GetNamespaces() {
		namespaces = append(namespaces, Namespace{Name: item.GetNamespace(), Environments: item.GetEnvironments()})
	}
	return namespaces, nil
}

// newRequest 统一贴凭据与客户端标识。写成自由函数是因为 Go 的方法不能带类型参数。
func newRequest[T any](c *Client, message *T) *connect.Request[T] {
	request := connect.NewRequest(message)
	if token := strings.TrimSpace(c.credential.MachineToken); token != "" {
		request.Header().Set(constants.ServiceTokenHeader, token)
	} else if bearer := strings.TrimSpace(c.credential.BearerToken); bearer != "" {
		request.Header().Set("Authorization", "Bearer "+bearer)
	}
	request.Header().Set(constants.ClientNameHeader, c.clientName)
	return request
}

// ParseFormat 把 CLI 的 --format 值映射到枚举。
func ParseFormat(name string) (configv1.ConfigFormat, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "yaml", "yml":
		return configv1.ConfigFormat_CONFIG_FORMAT_YAML, nil
	case "toml":
		return configv1.ConfigFormat_CONFIG_FORMAT_TOML, nil
	case "json":
		return configv1.ConfigFormat_CONFIG_FORMAT_JSON, nil
	case "plaintext", "text", "txt":
		return configv1.ConfigFormat_CONFIG_FORMAT_PLAINTEXT, nil
	default:
		return configv1.ConfigFormat_CONFIG_FORMAT_UNSPECIFIED, fmt.Errorf("unknown format %q (want yaml|toml|json|plaintext)", name)
	}
}

// FormatName 是 ParseFormat 的逆向，给输出用。
func FormatName(format configv1.ConfigFormat) string {
	switch format {
	case configv1.ConfigFormat_CONFIG_FORMAT_YAML:
		return "yaml"
	case configv1.ConfigFormat_CONFIG_FORMAT_TOML:
		return "toml"
	case configv1.ConfigFormat_CONFIG_FORMAT_JSON:
		return "json"
	case configv1.ConfigFormat_CONFIG_FORMAT_PLAINTEXT:
		return "plaintext"
	default:
		return "unspecified"
	}
}

// FormatForPath 按文件后缀推断格式，未知后缀返回 false 交给调用方显式指定，
// 而不是悄悄按 plaintext 写进去——格式错了服务端的语法校验就白做了。
func FormatForPath(filePath string) (configv1.ConfigFormat, bool) {
	format, err := ParseFormat(strings.TrimPrefix(path.Ext(filePath), "."))
	if err != nil {
		return configv1.ConfigFormat_CONFIG_FORMAT_UNSPECIFIED, false
	}
	return format, true
}
