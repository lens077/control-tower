package data

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lens077/control-tower/constants"
	"github.com/lens077/control-tower/services/config/internal/biz"
	conf "github.com/lens077/control-tower/services/config/internal/conf/v1"
	"github.com/lens077/control-tower/services/config/internal/iam"
	"github.com/lens077/go-connect-kit/dbutil"
	"github.com/redis/go-redis/v9"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Module 导出给 FX 的 Provider
var Module = fx.Module("data",
	fx.Provide(
		NewData,
		NewPostgresPool,
		NewRedisClient,
		NewConfigRepo,
		NewWatcher,
		NewMachineTokenRepo,
		// 同一 repo 同时喂给 biz（管理面）与 iam（数据面校验）。
		func(r *MachineTokenRepo) biz.MachineTokenRepo { return r },
		func(r *MachineTokenRepo) iam.TokenStore { return r },
	),
)

type contextTxKey struct{}

// Data 包含所有数据源的客户端
type Data struct {
	db           *pgxpool.Pool
	pgx          *pgxpool.Pool
	rdb          *redis.Client
	dbErrHandler *dbutil.Handler
	log          *zap.Logger
}

// NewData 是 Data 的构造函数
func NewData(db *pgxpool.Pool, rdb *redis.Client, logger *zap.Logger) *Data {
	return &Data{
		db:  db,
		pgx: db,
		rdb: rdb,
		log: logger,
		dbErrHandler: dbutil.NewHandler(
			// dbutil.WithErrorMapping("23505", biz.ErrAlreadyExists),
			// dbutil.WithErrorMapping("23503", biz.ErrNotFound),
			dbutil.WithLogging(true),
			dbutil.WithLogger(func(err error, pgErr *pgconn.PgError) {
				if pgErr != nil {
					logger.Warn("database error",
						zap.String("code", pgErr.Code),
						zap.String("message", pgErr.Message),
						zap.String("detail", pgErr.Detail),
					)
				}
			}),
		),
	}
}

// DB 从上下文中获取事务或返回默认DB
// 通过 data.DB(ctx) 自动获取事务或普通连接
// example: db := p.data.DB(ctx)
// func (d *Data) DB(ctx context.Context) *models.Queries {
// 	if tx, ok := ctx.Value(contextTxKey{}).(pgx.Tx); ok {
// 		// 如果上下文中有事务，使用事务版 Queries
// 		return models.New(tx)
// 	}
// 	// 无事务时使用普通连接
// 	return d.db
// }

// WithTx 将事务存入上下文
func (d *Data) WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, contextTxKey{}, tx)
}

// ExecTx 支持嵌套事务检测
func (d *Data) ExecTx(ctx context.Context, fn func(context.Context) error) error {
	if _, ok := ctx.Value(contextTxKey{}).(pgx.Tx); ok {
		d.log.Debug("reuse existing transaction")
		return fn(ctx)
	}

	d.log.Info("begin transaction")
	tx, err := d.pgx.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx failed: %w", err)
	}

	txCtx := d.WithTx(ctx, tx)

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()

	if err := fn(txCtx); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			return fmt.Errorf("%w (rollback err: %v)", err, rbErr)
		}
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit failed: %w", err)
	}
	d.log.Info("transaction committed")
	return nil
}

// NewPostgresPool 创建pg数据库连接池
func NewPostgresPool(lc fx.Lifecycle, cfg *conf.Bootstrap, logger *zap.Logger) (*pgxpool.Pool, error) {
	dbCfg := cfg.Data.Database.Postgres // 从 Config 中获取 Data 配置

	// 使用 ParseConfig 生成带有内部安全凭证的空模板
	// 传入空字符串即可，它会生成一套标准的默认连接池参数
	pgConf, err := pgxpool.ParseConfig("")
	if err != nil {
		return nil, fmt.Errorf("parse base pool config failed: %w", err)
	}

	// PostgreSQL 启动参数
	if pgConf.ConnConfig.RuntimeParams == nil {
		pgConf.ConnConfig.RuntimeParams = make(map[string]string)
	}
	// 时区配置（例如 "Asia/Shanghai" 或 "UTC"）
	pgConf.ConnConfig.RuntimeParams["timezone"] = dbCfg.Timezone

	// 将 Consul/Viper 读取到的具体配置灌入模板中
	pgConf.ConnConfig.Host = dbCfg.Host
	pgConf.ConnConfig.Port = uint16(dbCfg.Port)
	pgConf.ConnConfig.Database = dbCfg.DbName
	pgConf.ConnConfig.User = dbCfg.User
	pgConf.ConnConfig.Password = dbCfg.Password

	// 配置连接池熔断与超时属性
	pgConf.MaxConnLifetime = dbCfg.Pool.MaxConnLifetime.AsDuration()
	pgConf.MaxConnIdleTime = dbCfg.Pool.MaxConnIdleTime.AsDuration()
	pgConf.PingTimeout = dbCfg.Pool.PingTimeout.AsDuration()
	pgConf.MaxConns = int32(dbCfg.Pool.MaxConns)
	pgConf.MinConns = int32(dbCfg.Pool.MinConns)

	// SSL 证书校验逻辑
	switch dbCfg.Tls.SslMode {
	case constants.SslModeVerifyCa:
		if dbCfg.Tls.CaPem != "" {
			caCertPool := x509.NewCertPool()
			if ok := caCertPool.AppendCertsFromPEM([]byte(dbCfg.Tls.CaPem)); !ok {
				return nil, fmt.Errorf("failed to parse CA PEM")
			}

			logger.Info("setting up ssl mode: verify-ca config")
			pgConf.ConnConfig.TLSConfig = &tls.Config{
				RootCAs:            caCertPool,
				InsecureSkipVerify: true, // 必须为 true，以此绕过 Go 对 ServerName 的强制检查
				// Go 语言的标准库 crypto/tls 不支持这种“只验 CA 不验域名”的中间态
				// 手动实现“只验CA、不验域名”
				VerifyPeerCertificate: func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
					opts := x509.VerifyOptions{
						Roots:         caCertPool,
						CurrentTime:   time.Now(),
						Intermediates: x509.NewCertPool(),
					}
					// 拿到服务器传过来的第一个证书
					cert, err := x509.ParseCertificate(rawCerts[0])
					if err != nil {
						return err
					}
					// 把后续的证书当作证书链辅助塞进去
					for _, rawCert := range rawCerts[1:] {
						if c, err := x509.ParseCertificate(rawCert); err == nil {
							opts.Intermediates.AddCert(c)
						}
					}
					// 校验合法性（注意：此时不校验 DNS / IP）
					_, err = cert.Verify(opts)
					return err
				},
			}
		}
	case constants.SslModeVerifyFull:
		if dbCfg.Tls.CaPem != "" {
			caCertPool := x509.NewCertPool()
			if ok := caCertPool.AppendCertsFromPEM([]byte(dbCfg.Tls.CaPem)); !ok {
				return nil, fmt.Errorf("failed to parse CA PEM")
			}

			logger.Info("setting up TLS config")
			pgConf.ConnConfig.TLSConfig = &tls.Config{
				RootCAs:            caCertPool,
				InsecureSkipVerify: false,      // 必须为 false，严格校验证书
				ServerName:         dbCfg.Host, // 校验证书里的域名是否为该 Host
			}
		}
	}

	// 链路追踪配置
	pgConf.ConnConfig.Tracer = otelpgx.NewTracer()

	pool, err := pgxpool.NewWithConfig(context.Background(), pgConf)
	if err != nil {
		return nil, fmt.Errorf("connect to database failed: %v", err)
	}

	// 记录数据库统计信息
	if err := otelpgx.RecordStats(pool); err != nil {
		return nil, fmt.Errorf("unable to record database stats: %w", err)
	}

	// 测试连接
	ctx, cancel := context.WithTimeout(context.Background(), dbCfg.Pool.PingTimeout.AsDuration())
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("database ping failed: %v", err)
	}

	logger.Info(fmt.Sprintf("database connected successfully to %s", dbCfg.Host))

	// goose 迁移：池就绪后、收流量前执行（00001 对存量库是 no-op，见 migrate.go）。
	if err := runMigrations(context.Background(), pgConf.ConnConfig, logger); err != nil {
		pool.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			logger.Info("closing database connection...")
			pool.Close()
			return nil
		},
	})

	return pool, nil
}

// NewRedisClient 创建 Redis 客户端
func NewRedisClient(lc fx.Lifecycle, cfg *conf.Bootstrap, logger *zap.Logger) (*redis.Client, error) {
	redisCfg := cfg.Data.Cache.Redis

	// 基础配置
	opts := &redis.Options{
		Addr:         fmt.Sprintf("%s:%d", redisCfg.Host, redisCfg.Port),
		Username:     redisCfg.Username,
		Password:     redisCfg.Password,
		DB:           int(redisCfg.Db),
		DialTimeout:  redisCfg.DialTimeout.AsDuration(),
		ReadTimeout:  redisCfg.ReadTimeout.AsDuration(),
		WriteTimeout: redisCfg.WriteTimeout.AsDuration(),
		PoolSize:     int(redisCfg.PoolSize),
		MinIdleConns: int(redisCfg.MinIdleConns),
	}

	// TLS 适配
	if redisCfg.Tls != nil && redisCfg.Tls.Enable {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: redisCfg.Tls.InsecureSkipVerify,
		}

		// 处理 CA 证书字符串
		if redisCfg.Tls.CaPem != "" {
			caCertPool := x509.NewCertPool()
			// 注意：这里直接使用字符串解析，不需要 os.ReadFile
			if ok := caCertPool.AppendCertsFromPEM([]byte(redisCfg.Tls.CaPem)); !ok {
				return nil, fmt.Errorf("failed to parse redis CA certificate: invalid PEM format")
			}
			tlsConfig.RootCAs = caCertPool

			// 如果你的证书中限制了访问域名（SANs），需要匹配 Addr 中的 Host
			// 你的证书里包含：dragonfly.sumery.com
			// tlsConfig.ServerName = "dragonfly.sumery.com"
		}

		opts.TLSConfig = tlsConfig
		logger.Info("tls connection initialized with CA string")
	}

	rdb := redis.NewClient(opts)

	// 测试连接
	ctx, cancel := context.WithTimeout(context.Background(), redisCfg.DialTimeout.AsDuration())
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		// 记录带上下文的错误日志
		logger.Error("redis ping failed",
			zap.String("addr", redisCfg.Host),
			zap.Error(err),
		)

		// 关闭连接
		if errClose := rdb.Close(); errClose != nil {
			logger.Error("failed to close redis connection after ping failure",
				zap.String("addr", redisCfg.Host),
				zap.Error(errClose),
			)
		}

		// 返回错误给调用方（让 Fx 知道初始化失败）
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}
	logger.Info("redis connected successfully",
		zap.String("addr", redisCfg.Host),
	)

	lc.Append(fx.Hook{
		// 应用停止时释放资源
		OnStop: func(ctx context.Context) error {
			logger.Info("closing redis connection...")
			return rdb.Close()
		},
	})

	return rdb, nil
}

// CheckDatabase 检查数据库连通性
func (d *Data) CheckDatabase(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := d.db.Ping(ctx); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}
	return nil
}

// CheckCache 检查缓存连通性
func (d *Data) CheckCache(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := d.rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("cache ping failed: %w", err)
	}
	return nil
}
