package platform

import (
	"context"
	"fmt"

	// 注册 MySQL 与 PostgreSQL 的 database/sql 驱动（迁移与业务共用同一驱动集合）。
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"go.opentelemetry.io/otel/trace"

	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
	"github.com/admin-frame/admin-frame/pkg/db"
	"github.com/admin-frame/admin-frame/pkg/redis"
)

// sqlDriverName 把配置中的引擎名映射为已注册的 database/sql 驱动名。
//
// 配置面向运维使用 postgres，而 pgx stdlib 注册名是 pgx，必须在此显式转换；
// 未知引擎直接失败，避免静默回落到其它数据库。
func sqlDriverName(driver string) (string, error) {
	switch driver {
	case "mysql":
		return "mysql", nil
	case "postgres", "postgresql", "pgx":
		return "pgx", nil
	default:
		return "", fmt.Errorf("platform: unsupported db.driver %q", driver)
	}
}

// dbConfigFrom 把配置映射为 pkg/db 的连接参数（纯函数，便于单测覆盖字段搬运）。
//
// 池参数原样传递：0 交由 pkg/db.applyDefaults 填充，避免在两处维护默认值。
func dbConfigFrom(cfg platformconfig.DBConfig) (db.Config, error) {
	driver, err := sqlDriverName(cfg.Driver)
	if err != nil {
		return db.Config{}, err
	}
	return db.Config{
		Driver:          driver,
		DSN:             cfg.DSN,
		MaxOpenConns:    cfg.MaxOpenConns,
		MaxIdleConns:    cfg.MaxIdleConns,
		ConnMaxLifetime: cfg.ConnMaxLifetime,
	}, nil
}

// OpenDatabase 按配置打开进程数据库；driver=memory 返回 nil（业务使用内存实现）。
//
// 连接池参数取自 db.max_open_conns / max_idle_conns / conn_max_lifetime，未配置时由
// pkg/db 填充默认值；tp 非 nil 时为 SQL 调用创建 CLIENT span（调用方传本进程的
// TracerProvider，worker 等未接入 tracing 的进程传 nil）。
// 调用方须在使用完毕后 Close。
func OpenDatabase(ctx context.Context, cfg platformconfig.DBConfig, tp trace.TracerProvider) (*db.DB, error) {
	if cfg.Driver == "" || cfg.Driver == "memory" {
		return nil, nil
	}
	dbCfg, err := dbConfigFrom(cfg)
	if err != nil {
		return nil, err
	}
	database, err := db.OpenTraced(ctx, dbCfg, tp)
	if err != nil {
		return nil, fmt.Errorf("db: %w", err)
	}
	return database, nil
}

// OpenRedis 按配置打开共享 Redis；未配置 addr 时返回 nil（单实例 / 本地）。
//
// 连接失败即返回错误：IAM enforce 依赖 Redis 时不得静默降级为单层内存缓存。
func OpenRedis(ctx context.Context, cfg platformconfig.RedisConfig) (*redis.Client, error) {
	if !cfg.Enabled() {
		return nil, nil
	}
	client, err := redis.Open(ctx, redis.Config{
		Addr:      cfg.Addr,
		Username:  cfg.Username,
		Password:  cfg.Password,
		DB:        cfg.DB,
		KeyPrefix: cfg.KeyPrefix,
	})
	if err != nil {
		return nil, fmt.Errorf("redis: %w", err)
	}
	return client, nil
}
