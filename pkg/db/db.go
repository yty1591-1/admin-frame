package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-sql-driver/mysql"
)

// MySQLDefaultCollation 与 migrations DDL 使用的表级 collation 保持一致。
//
// 用户变量（@var）与 CAST 结果继承连接 collation 且同为 implicit 级别，与
// utf8mb4_0900_ai_ci 列比较时会报 1267 Illegal mix of collations（000005 种子迁移
// 即依赖此类比较），因此需要在连接层对齐。
const MySQLDefaultCollation = "utf8mb4_0900_ai_ci"

// MySQLDefaultCharset 是 MySQLDefaultCollation 对应的字符集。
//
// 驱动只有在 DSN 同时给出 charset 与 collation 时才执行
// SET NAMES <charset> COLLATE <collation>；只写 collation 会被忽略，
// 连接仍沿用「默认库 / 服务端」的 collation，这也是 1267 的真正来源。
const MySQLDefaultCharset = "utf8mb4"

// NormalizeDSN 在 MySQL DSN 未显式指定 collation 时补默认值；其他驱动原样返回。
func NormalizeDSN(driver, dsn string) (string, error) {
	switch driver {
	case "mysql", "mariadb":
		parsed, err := mysql.ParseDSN(dsn)
		if err != nil {
			return "", err
		}
		applyDefault := parsed.Collation == ""
		if applyDefault {
			parsed.Collation = MySQLDefaultCollation
		}
		out := parsed.FormatDSN()
		if applyDefault {
			out = ensureCharsetParam(out, MySQLDefaultCharset)
		}
		return out, nil
	default:
		return dsn, nil
	}
}

// ForceMySQLCollation 无条件覆盖 MySQL DSN 的连接 collation；其他驱动原样返回。
//
// 迁移脚本依赖用户变量与 CAST 结果和 utf8mb4_0900_ai_ci 列比较，历史 DSN 模板中
// 常见的 collation=utf8mb4_general_ci 会让 000005 等种子迁移报 1267；
// 该函数由迁移入口（cmd/migrate、测试基座）调用，业务连接仍走 NormalizeDSN 以免覆盖显式配置。
func ForceMySQLCollation(driver, dsn, collation string) (string, error) {
	switch driver {
	case "mysql", "mariadb":
		parsed, err := mysql.ParseDSN(dsn)
		if err != nil {
			return "", err
		}
		parsed.Collation = collation
		// 仅设置 collation 不会生效：驱动需要 charset 才会下发 SET NAMES ... COLLATE ...。
		return ensureCharsetParam(parsed.FormatDSN(), MySQLDefaultCharset), nil
	default:
		return dsn, nil
	}
}

// ensureCharsetParam 在 DSN 查询串缺少 charset 参数时追加，已有则原样返回。
func ensureCharsetParam(dsn, charset string) string {
	query := dsn
	if idx := strings.LastIndexByte(dsn, '?'); idx >= 0 {
		query = dsn[idx+1:]
	}
	for _, pair := range strings.Split(query, "&") {
		if strings.HasPrefix(pair, "charset=") {
			return dsn
		}
	}
	if strings.Contains(dsn, "?") {
		return dsn + "&charset=" + charset
	}
	return dsn + "?charset=" + charset
}

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

	dsn, err := NormalizeDSN(cfg.Driver, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("db: parse %s DSN (%s): %w", cfg.Driver, RedactDSN(cfg.DSN), err)
	}
	sqlDB, err := sql.Open(cfg.Driver, dsn)
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
