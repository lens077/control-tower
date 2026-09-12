package biz

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ErrTokenNotFound 表示 token 不存在或已吊销。
var ErrTokenNotFound = errors.New("machine token not found or already revoked")

// ErrInvalidTokenRole 表示 role 不在 {service, operator}。
var ErrInvalidTokenRole = errors.New("machine token role must be service or operator")

// machine token 角色（对应 config.machine_token.role 列）。
const (
	// RoleService 数据面只读凭据：GetKey/WatchKeys，namespace 白名单。
	RoleService = "service"
	// RoleOperator 管理面服务账号：自身 environment 内读写配置、签发/吊销 service token。
	RoleOperator = "operator"
)

// NamespaceWildcard 在 AllowedNamespaces 里表示任意 namespace（environment 仍须相等）。
const NamespaceWildcard = "*"

// MachineToken 是数据面凭据实体（设计：docs/design/machine-token.md）。
type MachineToken struct {
	ID                string
	Service           string
	Environment       string
	TokenHash         []byte
	AllowedNamespaces []string
	Note              string
	Disabled          bool
	CreatedAt         time.Time
	RevokedAt         *time.Time
	LastUsedAt        *time.Time
	// Role 为 RoleService 或 RoleOperator；空值按 RoleService 处理。
	Role string
}

// IsOperator 报告该 token 是否为 operator 角色。
func (t MachineToken) IsOperator() bool { return t.Role == RoleOperator }

// MachineTokenRepo 是存储接口（data 层实现）。
type MachineTokenRepo interface {
	Insert(ctx context.Context, t MachineToken) (MachineToken, error)
	// Get 按 id 查元数据（含已吊销）；不存在返回 ErrTokenNotFound。
	Get(ctx context.Context, tokenID string) (MachineToken, error)
	List(ctx context.Context, service, environment string) ([]MachineToken, error)
	Revoke(ctx context.Context, tokenID string) (MachineToken, error)
}

// MachineTokenUseCase 承载签发/列举/吊销。
type MachineTokenUseCase struct {
	repo MachineTokenRepo
	log  *zap.Logger
}

// NewMachineTokenUseCase 构造 usecase。
func NewMachineTokenUseCase(repo MachineTokenRepo, logger *zap.Logger) *MachineTokenUseCase {
	return &MachineTokenUseCase{repo: repo, log: logger.Named("machine-token")}
}

// tokenPrefix 便于日志/排障里辨认凭据类别（明文本身绝不落日志）。
const tokenPrefix = "ct_"

// Issue 签发新 token：返回的第一个值是明文（仅此一次），第二个是元数据。
// role 为空按 RoleService。allowedNamespaces 为空时：service 默认仅自身 namespace，
// operator 默认 "*"（任意 namespace）。
func (uc *MachineTokenUseCase) Issue(ctx context.Context, service, environment string, allowedNamespaces []string, note, actor, role string) (string, MachineToken, error) {
	switch role {
	case "":
		role = RoleService
	case RoleService, RoleOperator:
	default:
		return "", MachineToken{}, fmt.Errorf("%w: %q", ErrInvalidTokenRole, role)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", MachineToken{}, fmt.Errorf("generate token: %w", err)
	}
	plaintext := tokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(plaintext))

	if len(allowedNamespaces) == 0 {
		if role == RoleOperator {
			allowedNamespaces = []string{NamespaceWildcard}
		} else {
			allowedNamespaces = []string{service}
		}
	}
	t, err := uc.repo.Insert(ctx, MachineToken{
		ID:                uuid.New().String(),
		Service:           service,
		Environment:       environment,
		TokenHash:         sum[:],
		AllowedNamespaces: allowedNamespaces,
		Note:              note,
		Role:              role,
	})
	if err != nil {
		return "", MachineToken{}, err
	}
	// 审计：谁、给谁、什么范围；不含任何凭据材料。
	uc.log.Info("machine token issued",
		zap.String("id", t.ID),
		zap.String("service", service),
		zap.String("environment", environment),
		zap.Strings("namespaces", allowedNamespaces),
		zap.String("role", role),
		zap.String("actor", actor),
	)
	return plaintext, t, nil
}

// Get 按 id 查元数据（含已吊销）。
func (uc *MachineTokenUseCase) Get(ctx context.Context, tokenID string) (MachineToken, error) {
	return uc.repo.Get(ctx, tokenID)
}

// List 列举（不含哈希，data 层已只回元数据所需字段；哈希字段由 service 层丢弃）。
func (uc *MachineTokenUseCase) List(ctx context.Context, service, environment string) ([]MachineToken, error) {
	return uc.repo.List(ctx, service, environment)
}

// Revoke 吊销并审计。
func (uc *MachineTokenUseCase) Revoke(ctx context.Context, tokenID, actor string) (MachineToken, error) {
	t, err := uc.repo.Revoke(ctx, tokenID)
	if err != nil {
		return MachineToken{}, err
	}
	uc.log.Info("machine token revoked",
		zap.String("id", t.ID),
		zap.String("service", t.Service),
		zap.String("environment", t.Environment),
		zap.String("role", t.Role),
		zap.String("actor", actor),
	)
	return t, nil
}
