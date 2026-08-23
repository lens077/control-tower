package data

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lens077/control-tower/services/config/internal/biz"
	"github.com/lens077/control-tower/services/config/internal/data/models"
	"go.uber.org/zap"
)

var _ biz.ConfigRepo = (*configRepo)(nil)

type configRepo struct {
	db      *pgxpool.Pool
	queries *models.Queries
	log     *zap.Logger
}

func NewConfigRepo(data *Data, logger *zap.Logger) biz.ConfigRepo {
	return &configRepo{
		db:      data.db,
		queries: models.New(data.db),
		log:     logger.Named("configRepo"),
	}
}

func toEntry(m models.ConfigEntry) *biz.ConfigEntry {
	return &biz.ConfigEntry{
		ID:          m.ID,
		Namespace:   m.Namespace,
		Environment: m.Environment,
		Key:         m.Key,
		Format:      biz.ConfigFormat(m.Format),
		Value:       m.Value,
		Version:     m.Version,
		IsSecret:    m.IsSecret,
		Description: m.Description,
		UpdatedBy:   m.UpdatedBy,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

func toRevision(m models.ConfigRevision) *biz.ConfigRevision {
	return &biz.ConfigRevision{
		ID:        m.ID,
		EntryID:   m.EntryID,
		Version:   m.Version,
		Format:    biz.ConfigFormat(m.Format),
		Value:     m.Value,
		Comment:   m.Comment,
		Author:    m.Author,
		CreatedAt: m.CreatedAt,
	}
}

// ListNamespaces 把 (namespace, environment) 维度的行聚合成每个 namespace 一条记录。
// 查询已按 namespace, environment 排序,故顺序聚合即可,无需再排序。
func (r *configRepo) ListNamespaces(ctx context.Context) ([]*biz.NamespaceInfo, error) {
	rows, err := r.queries.ListNamespaceEnvironments(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*biz.NamespaceInfo, 0, len(rows))
	for _, row := range rows {
		if n := len(out); n > 0 && out[n-1].Namespace == row.Namespace {
			cur := out[n-1]
			cur.Environments = append(cur.Environments, row.Environment)
			cur.KeyCount += row.KeyCount
			continue
		}
		out = append(out, &biz.NamespaceInfo{
			Namespace:    row.Namespace,
			Environments: []string{row.Environment},
			KeyCount:     row.KeyCount,
		})
	}
	return out, nil
}

func (r *configRepo) ListEntries(ctx context.Context, namespace, environment, keyPrefix string) ([]*biz.ConfigEntry, error) {
	prefix := keyPrefix
	rows, err := r.queries.ListEntries(ctx, models.ListEntriesParams{
		Namespace:   namespace,
		Environment: environment,
		KeyPrefix:   &prefix, // 空串 -> LIKE '%' 匹配全部;必须为非 nil 指针
	})
	if err != nil {
		return nil, err
	}
	out := make([]*biz.ConfigEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, &biz.ConfigEntry{
			ID:          row.ID,
			Namespace:   row.Namespace,
			Environment: row.Environment,
			Key:         row.Key,
			Format:      biz.ConfigFormat(row.Format),
			// ListKeys 仅返回元数据,不含 value
			Version:     row.Version,
			IsSecret:    row.IsSecret,
			Description: row.Description,
			UpdatedBy:   row.UpdatedBy,
			CreatedAt:   row.CreatedAt,
			UpdatedAt:   row.UpdatedAt,
		})
	}
	return out, nil
}

func (r *configRepo) GetEntry(ctx context.Context, namespace, environment, key string) (*biz.ConfigEntry, error) {
	m, err := r.queries.GetEntry(ctx, models.GetEntryParams{
		Namespace:   namespace,
		Environment: environment,
		Key:         key,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, biz.ErrKeyNotFound
		}
		return nil, err
	}
	return toEntry(m), nil
}

// PutEntry 在单事务内 upsert 配置项(version+1)并追加一条 revision。
func (r *configRepo) PutEntry(ctx context.Context, in biz.PutParams) (*biz.ConfigEntry, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := models.New(tx)

	existing, err := q.GetEntryForUpdate(ctx, models.GetEntryForUpdateParams{
		Namespace:   in.Namespace,
		Environment: in.Environment,
		Key:         in.Key,
	})

	var saved models.ConfigEntry
	switch {
	case err == nil:
		// 已存在 -> 版本自增并更新
		newVersion := existing.Version + 1
		saved, err = q.UpdateEntry(ctx, models.UpdateEntryParams{
			ID:          existing.ID,
			Value:       in.Value,
			Format:      string(in.Format),
			Version:     newVersion,
			IsSecret:    in.IsSecret,
			Description: in.Description,
			UpdatedBy:   in.Author,
		})
		if err != nil {
			return nil, fmt.Errorf("update entry: %w", err)
		}
	case errors.Is(err, pgx.ErrNoRows):
		// 不存在 -> 新建(version=1)
		saved, err = q.InsertEntry(ctx, models.InsertEntryParams{
			Namespace:   in.Namespace,
			Environment: in.Environment,
			Key:         in.Key,
			Format:      string(in.Format),
			Value:       in.Value,
			IsSecret:    in.IsSecret,
			Description: in.Description,
			UpdatedBy:   in.Author,
		})
		if err != nil {
			return nil, fmt.Errorf("insert entry: %w", err)
		}
	default:
		return nil, fmt.Errorf("lock entry: %w", err)
	}

	// 追加历史版本
	if _, err = q.InsertRevision(ctx, models.InsertRevisionParams{
		EntryID: saved.ID,
		Version: saved.Version,
		Format:  saved.Format,
		Value:   saved.Value,
		Comment: in.Comment,
		Author:  in.Author,
	}); err != nil {
		return nil, fmt.Errorf("insert revision: %w", err)
	}

	// 在事务内发通知:提交才投递,回滚不会误发(见 watcher.go 的 notify)
	if err = notify(ctx, tx, changePayload{
		Namespace:   saved.Namespace,
		Environment: saved.Environment,
		Key:         saved.Key,
		Version:     saved.Version,
	}); err != nil {
		return nil, err
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return toEntry(saved), nil
}

// DeleteEntry 删除配置项(revision 由外键级联删除)。
// 单条 DELETE 本可以不开事务,这里开是为了让变更通知与删除同生共死。
func (r *configRepo) DeleteEntry(ctx context.Context, namespace, environment, key string) (bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	n, err := models.New(tx).DeleteEntry(ctx, models.DeleteEntryParams{
		Namespace:   namespace,
		Environment: environment,
		Key:         key,
	})
	if err != nil {
		return false, err
	}
	if n == 0 {
		// 没删到任何行就没有变更,不发通知
		return false, nil
	}

	if err = notify(ctx, tx, changePayload{
		Namespace:   namespace,
		Environment: environment,
		Key:         key,
		Deleted:     true,
	}); err != nil {
		return false, err
	}

	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return true, nil
}

func (r *configRepo) ListRevisions(ctx context.Context, namespace, environment, key string) ([]*biz.ConfigRevision, error) {
	entry, err := r.GetEntry(ctx, namespace, environment, key)
	if err != nil {
		return nil, err
	}
	rows, err := r.queries.ListRevisions(ctx, entry.ID)
	if err != nil {
		return nil, err
	}
	out := make([]*biz.ConfigRevision, 0, len(rows))
	for _, m := range rows {
		rev := toRevision(m)
		// revision 表不存 is_secret,标记从 entry 带过来,让上层能按同一规则脱敏
		rev.IsSecret = entry.IsSecret
		out = append(out, rev)
	}
	return out, nil
}

func (r *configRepo) GetRevision(ctx context.Context, namespace, environment, key string, version int32) (*biz.ConfigRevision, error) {
	entry, err := r.GetEntry(ctx, namespace, environment, key)
	if err != nil {
		return nil, err
	}
	m, err := r.queries.GetRevisionByVersion(ctx, models.GetRevisionByVersionParams{
		EntryID: entry.ID,
		Version: version,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, biz.ErrRevisionNotFound
		}
		return nil, err
	}
	rev := toRevision(m)
	rev.IsSecret = entry.IsSecret
	return rev, nil
}
