package data

import (
	"context"
	"errors"

	"github.com/lens077/control-tower/services/config/internal/biz"
	"github.com/lens077/control-tower/services/config/internal/data/models"
	"github.com/lens077/control-tower/services/config/internal/iam"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// MachineTokenRepo 是 machine token 的存储实现：
// 同时满足 biz.MachineTokenRepo（管理面）与 iam.TokenStore（数据面校验）。
type MachineTokenRepo struct {
	q   *models.Queries
	log *zap.Logger
}

// NewMachineTokenRepo 构造 repo。
func NewMachineTokenRepo(d *Data, logger *zap.Logger) *MachineTokenRepo {
	return &MachineTokenRepo{q: models.New(d.pgx), log: logger.Named("machine-token-repo")}
}

var (
	_ biz.MachineTokenRepo = (*MachineTokenRepo)(nil)
	_ iam.TokenStore       = (*MachineTokenRepo)(nil)
)

// ── biz.MachineTokenRepo ────────────────────────────────────────────────

// Insert 落库一条新 token（只存哈希）。
func (r *MachineTokenRepo) Insert(ctx context.Context, t biz.MachineToken) (biz.MachineToken, error) {
	id, err := uuid.Parse(t.ID)
	if err != nil {
		return biz.MachineToken{}, err
	}
	row, err := r.q.InsertMachineToken(ctx, models.InsertMachineTokenParams{
		ID:                id,
		ServiceName:       t.Service,
		Environment:       t.Environment,
		TokenHash:         t.TokenHash,
		AllowedNamespaces: t.AllowedNamespaces,
		Note:              t.Note,
	})
	if err != nil {
		return biz.MachineToken{}, err
	}
	return toBizToken(row), nil
}

// List 按可选过滤条件列出。
func (r *MachineTokenRepo) List(ctx context.Context, service, environment string) ([]biz.MachineToken, error) {
	rows, err := r.q.ListMachineTokens(ctx, models.ListMachineTokensParams{
		ServiceName: strPtrOrNil(service),
		Environment: strPtrOrNil(environment),
	})
	if err != nil {
		return nil, err
	}
	out := make([]biz.MachineToken, 0, len(rows))
	for _, row := range rows {
		out = append(out, toBizToken(row))
	}
	return out, nil
}

// Revoke 吊销；不存在或已吊销返回 biz.ErrTokenNotFound。
func (r *MachineTokenRepo) Revoke(ctx context.Context, tokenID string) (biz.MachineToken, error) {
	id, err := uuid.Parse(tokenID)
	if err != nil {
		return biz.MachineToken{}, biz.ErrTokenNotFound
	}
	row, err := r.q.RevokeMachineToken(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.MachineToken{}, biz.ErrTokenNotFound
	}
	if err != nil {
		return biz.MachineToken{}, err
	}
	return toBizToken(row), nil
}

// ── iam.TokenStore ──────────────────────────────────────────────────────

// LookupActiveByHash 数据面校验查表。
func (r *MachineTokenRepo) LookupActiveByHash(ctx context.Context, hash []byte) (iam.MachineScope, bool, error) {
	row, err := r.q.GetActiveMachineTokenByHash(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return iam.MachineScope{}, false, nil
	}
	if err != nil {
		return iam.MachineScope{}, false, err
	}
	return iam.MachineScope{
		TokenID:     row.ID.String(),
		Service:     row.ServiceName,
		Environment: row.Environment,
		Namespaces:  row.AllowedNamespaces,
	}, true, nil
}

// IsActive 心跳复验（吊销断流）。
func (r *MachineTokenRepo) IsActive(ctx context.Context, tokenID string) (bool, error) {
	id, err := uuid.Parse(tokenID)
	if err != nil {
		return false, err
	}
	return r.q.IsMachineTokenActive(ctx, id)
}

// TouchLastUsed 尽力而为地记录使用时刻，失败只打日志。
func (r *MachineTokenRepo) TouchLastUsed(ctx context.Context, tokenID string) {
	id, err := uuid.Parse(tokenID)
	if err != nil {
		return
	}
	if err := r.q.TouchMachineTokenLastUsed(ctx, id); err != nil {
		r.log.Warn("touch machine token last_used failed", zap.Error(err))
	}
}

func toBizToken(row models.ConfigMachineToken) biz.MachineToken {
	t := biz.MachineToken{
		ID:                row.ID.String(),
		Service:           row.ServiceName,
		Environment:       row.Environment,
		TokenHash:         row.TokenHash,
		AllowedNamespaces: row.AllowedNamespaces,
		Note:              row.Note,
		Disabled:          row.Disabled,
		CreatedAt:         row.CreatedAt,
	}
	if row.RevokedAt.Valid {
		ts := row.RevokedAt.Time
		t.RevokedAt = &ts
	}
	if row.LastUsedAt.Valid {
		ts := row.LastUsedAt.Time
		t.LastUsedAt = &ts
	}
	return t
}

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
