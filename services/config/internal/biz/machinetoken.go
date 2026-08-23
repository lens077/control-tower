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
}

// MachineTokenRepo 是存储接口（data 层实现）。
type MachineTokenRepo interface {
	Insert(ctx context.Context, t MachineToken) (MachineToken, error)
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
// allowedNamespaces 为空时默认仅 service 自身。
func (uc *MachineTokenUseCase) Issue(ctx context.Context, service, environment string, allowedNamespaces []string, note, actor string) (string, MachineToken, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", MachineToken{}, fmt.Errorf("generate token: %w", err)
	}
	plaintext := tokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(plaintext))

	if len(allowedNamespaces) == 0 {
		allowedNamespaces = []string{service}
	}
	t, err := uc.repo.Insert(ctx, MachineToken{
		ID:                uuid.New().String(),
		Service:           service,
		Environment:       environment,
		TokenHash:         sum[:],
		AllowedNamespaces: allowedNamespaces,
		Note:              note,
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
		zap.String("actor", actor),
	)
	return plaintext, t, nil
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
		zap.String("actor", actor),
	)
	return t, nil
}
