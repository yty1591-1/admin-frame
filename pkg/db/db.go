package db

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"time"
)

// DB 包装 *sql.DB，统一连接池装配、关闭与就绪探测。
//
// 并发安全：底层 *sql.DB 可并发使用；Close 至多生效一次。
type DB struct {
	sql    *sql.DB
	driver string
	closed atomic.Bool
}

// Open 打开驱动、应用连接池参数，并（默认）Ping 验证连通性。
//
// 调用方须 blank-import 对应驱动；失败时错误消息仅含脱敏 DSN。
func Open(ctx context.Context, cfg Config) (*DB, error) {
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	sqlDB, err := sql.Open(cfg.Driver, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("db: open %s (%s): %w", cfg.Driver, RedactDSN(cfg.DSN), err)
	}

	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	d := &DB{sql: sqlDB, driver: cfg.Driver}
	if !cfg.SkipPing {
		pingCtx := ctx
		var cancel context.CancelFunc
		if cfg.PingTimeout > 0 {
			pingCtx, cancel = context.WithTimeout(ctx, cfg.PingTimeout)
			defer cancel()
		}
		if err := d.Ping(pingCtx); err != nil {
			_ = sqlDB.Close()
			return nil, err
		}
	}
	return d, nil
}

// SQL 返回底层 *sql.DB；调用方勿 Close（应通过 DB.Close）。
func (d *DB) SQL() *sql.DB {
	if d == nil {
		return nil
	}
	return d.sql
}

// Driver 返回打开时使用的驱动名。
func (d *DB) Driver() string {
	if d == nil {
		return ""
	}
	return d.driver
}

// Ping 探测连接可用性；须传入带 deadline 的 context（生产就绪检查）。
func (d *DB) Ping(ctx context.Context) error {
	if d == nil || d.sql == nil || d.closed.Load() {
		return ErrClosed
	}
	if err := d.sql.PingContext(ctx); err != nil {
		return fmt.Errorf("db: ping: %w", err)
	}
	return nil
}

// Checker 返回无参就绪检查函数，可直接交给 pkg/health.Handler.Register。
// 内部使用 2s 超时，避免探针阻塞。
func (d *DB) Checker() func() error {
	return func() error {
		if d == nil || d.sql == nil || d.closed.Load() {
			return ErrClosed
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return d.Ping(ctx)
	}
}

// Stats 返回连接池统计快照（监控 / 排障）。
func (d *DB) Stats() sql.DBStats {
	if d == nil || d.sql == nil {
		return sql.DBStats{}
	}
	return d.sql.Stats()
}

// Close 关闭连接池；可重复调用。
func (d *DB) Close() error {
	if d == nil || d.sql == nil {
		return nil
	}
	if !d.closed.CompareAndSwap(false, true) {
		return nil
	}
	if err := d.sql.Close(); err != nil {
		return fmt.Errorf("db: close: %w", err)
	}
	return nil
}
