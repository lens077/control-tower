package data

import (
	"context"
	"embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"go.uber.org/zap"
)

// migrationsFS 内嵌 goose 迁移。
// 00001 是存量 schema 快照（全程 IF NOT EXISTS，存量库重放为 no-op），
// 因此不需要「手工插基线记录」的接管手顺；版本表用 goose 默认 public.goose_db_version
// （config schema 由 00001 自己创建，版本表不能依赖它先存在）。
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// runMigrations 在连接池就绪后、服务开始收流量前执行 goose Up。
func runMigrations(ctx context.Context, connCfg *pgx.ConnConfig, logger *zap.Logger) error {
	db := stdlib.OpenDB(*connCfg)
	defer db.Close()

	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(gooseZapLogger{logger.Named("goose")})
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("goose dialect: %w", err)
	}
	mctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := goose.UpContext(mctx, db, "migrations"); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

type gooseZapLogger struct{ log *zap.Logger }

func (l gooseZapLogger) Fatalf(format string, v ...any) { l.log.Fatal(fmt.Sprintf(format, v...)) }
func (l gooseZapLogger) Printf(format string, v ...any) { l.log.Info(fmt.Sprintf(format, v...)) }
