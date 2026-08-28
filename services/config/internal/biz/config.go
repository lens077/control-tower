package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pelletier/go-toml/v2"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// ConfigFormat 配置内容格式(与 proto ConfigFormat 对应,以字符串持久化)
type ConfigFormat string

const (
	FormatYAML      ConfigFormat = "yaml"
	FormatTOML      ConfigFormat = "toml"
	FormatJSON      ConfigFormat = "json"
	FormatPlaintext ConfigFormat = "plaintext"
)

var (
	ErrKeyNotFound      = errors.New("config key not found")
	ErrRevisionNotFound = errors.New("config revision not found")
	ErrInvalidFormat    = errors.New("invalid config format")
)

// ConfigEntry 配置项(键 + 当前值 + 元数据)
type ConfigEntry struct {
	ID          int64
	Namespace   string
	Environment string
	Key         string
	Format      ConfigFormat
	Value       string
	Version     int32
	IsSecret    bool
	Description string
	UpdatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NamespaceInfo namespace 概览:该 namespace 下确实存在配置的环境与 key 总数。
// 供前端下拉选择,避免手输 namespace/environment。
type NamespaceInfo struct {
	Namespace    string
	Environments []string
	KeyCount     int32
}

// ConfigRevision 版本历史记录
type ConfigRevision struct {
	ID      int64
	EntryID int64
	Version int32
	Format  ConfigFormat
	Value   string
	// IsSecret 取自所属 entry 的当前值(revision 表本身不存这一列)。
	// 它只用于决定对外是否脱敏 —— 领域内部(如 Rollback)读到的始终是真值。
	IsSecret  bool
	Comment   string
	Author    string
	CreatedAt time.Time
}

// PutParams 创建/更新配置项的入参
type PutParams struct {
	Namespace   string
	Environment string
	Key         string
	Format      ConfigFormat
	Value       string
	Comment     string
	Description string
	IsSecret    bool
	Author      string
}

// ConfigRepo 数据访问接口(由 data 层实现)
type ConfigRepo interface {
	ListNamespaces(ctx context.Context) ([]*NamespaceInfo, error)
	ListEntries(ctx context.Context, namespace, environment, keyPrefix string) ([]*ConfigEntry, error)
	GetEntry(ctx context.Context, namespace, environment, key string) (*ConfigEntry, error)
	// PutEntry 在单事务内 upsert 配置项(version+1)并追加一条 revision,返回更新后的条目。
	PutEntry(ctx context.Context, in PutParams) (*ConfigEntry, error)
	DeleteEntry(ctx context.Context, namespace, environment, key string) (bool, error)
	ListRevisions(ctx context.Context, namespace, environment, key string) ([]*ConfigRevision, error)
	GetRevision(ctx context.Context, namespace, environment, key string, version int32) (*ConfigRevision, error)
}

// ChangeEvent 一次配置变更。只带定位信息,值由订阅方回查
// —— 通知通道里流完整 value 既受长度限制,也没必要让密文多走一条路径。
type ChangeEvent struct {
	Namespace   string
	Environment string
	Key         string
	Version     int32
	Deleted     bool
}

// ConfigWatcher 变更订阅(由 data 层实现)。
//
// 返回的 channel 被**关闭**表示下发链路已断(例如与数据库的监听连接掉线),
// 订阅方应结束当前流、让客户端重连重取快照。刻意不做「继续返回一条永远
// 收不到事件的 channel」:那会让调用方以为自己还在监听,是最难查的一类故障。
type ConfigWatcher interface {
	Subscribe(namespace, environment string, keys []string) (<-chan ChangeEvent, func())
}

// ConfigUseCase 配置中心用例
type ConfigUseCase struct {
	repo      ConfigRepo
	watcher   ConfigWatcher
	validator ContentValidator
	log       *zap.Logger
}

func NewConfigUseCase(repo ConfigRepo, watcher ConfigWatcher, validator ContentValidator, logger *zap.Logger) *ConfigUseCase {
	return &ConfigUseCase{
		repo:      repo,
		watcher:   watcher,
		validator: validator,
		log:       logger.Named("ConfigUseCase"),
	}
}

// WatchKeys 订阅 namespace+environment 下若干 key 的变更;keys 为空表示订阅全部。
func (uc *ConfigUseCase) WatchKeys(namespace, environment string, keys []string) (<-chan ChangeEvent, func()) {
	return uc.watcher.Subscribe(namespace, environment, keys)
}

func (uc *ConfigUseCase) ListNamespaces(ctx context.Context) ([]*NamespaceInfo, error) {
	return uc.repo.ListNamespaces(ctx)
}

func (uc *ConfigUseCase) ListKeys(ctx context.Context, namespace, environment, keyPrefix string) ([]*ConfigEntry, error) {
	return uc.repo.ListEntries(ctx, namespace, environment, keyPrefix)
}

func (uc *ConfigUseCase) GetKey(ctx context.Context, namespace, environment, key string) (*ConfigEntry, error) {
	return uc.repo.GetEntry(ctx, namespace, environment, key)
}

// PutKey 保存前依次做语法和已登记的结构校验，再写入。
func (uc *ConfigUseCase) PutKey(ctx context.Context, in PutParams) (*ConfigEntry, error) {
	if err := ValidateFormat(in.Format, in.Value); err != nil {
		return nil, err
	}
	if uc.validator != nil {
		target := ContentTarget{Namespace: in.Namespace, Environment: in.Environment, Key: in.Key}
		if err := uc.validator.Validate(target, in.Format, in.Value); err != nil {
			return nil, fmt.Errorf("%w: schema: %s", ErrInvalidFormat, err)
		}
	}
	return uc.repo.PutEntry(ctx, in)
}

func (uc *ConfigUseCase) DeleteKey(ctx context.Context, namespace, environment, key string) (bool, error) {
	return uc.repo.DeleteEntry(ctx, namespace, environment, key)
}

func (uc *ConfigUseCase) ListRevisions(ctx context.Context, namespace, environment, key string) ([]*ConfigRevision, error) {
	return uc.repo.ListRevisions(ctx, namespace, environment, key)
}

func (uc *ConfigUseCase) GetRevision(ctx context.Context, namespace, environment, key string, version int32) (*ConfigRevision, error) {
	return uc.repo.GetRevision(ctx, namespace, environment, key, version)
}

// Rollback 用指定历史版本的值写为新版本。
func (uc *ConfigUseCase) Rollback(ctx context.Context, namespace, environment, key string, version int32, comment, author string) (*ConfigEntry, error) {
	rev, err := uc.repo.GetRevision(ctx, namespace, environment, key, version)
	if err != nil {
		return nil, err
	}
	if comment == "" {
		comment = fmt.Sprintf("rollback to v%d", version)
	}
	return uc.PutKey(ctx, PutParams{
		Namespace:   namespace,
		Environment: environment,
		Key:         key,
		Format:      rev.Format,
		Value:       rev.Value,
		Comment:     comment,
		Author:      author,
	})
}

// ValidateFormat 按 format 解析 value,校验语法合法性。plaintext 不校验。
func ValidateFormat(format ConfigFormat, value string) error {
	if value == "" {
		return nil
	}
	var v any
	switch format {
	case FormatYAML:
		if err := yaml.Unmarshal([]byte(value), &v); err != nil {
			return fmt.Errorf("%w: yaml: %v", ErrInvalidFormat, err)
		}
	case FormatTOML:
		if err := toml.Unmarshal([]byte(value), &v); err != nil {
			return fmt.Errorf("%w: toml: %v", ErrInvalidFormat, err)
		}
	case FormatJSON:
		if err := json.Unmarshal([]byte(value), &v); err != nil {
			return fmt.Errorf("%w: json: %v", ErrInvalidFormat, err)
		}
	case FormatPlaintext:
		return nil
	default:
		return fmt.Errorf("%w: unknown format %q", ErrInvalidFormat, format)
	}
	return nil
}
