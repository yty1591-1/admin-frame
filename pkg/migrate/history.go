package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Record 是迁移历史表中的一行（已应用迁移）。
type Record struct {
	// Version 迁移版本号。
	Version int64
	// Description 迁移描述。
	Description string
	// Checksum 应用时记录的 up SQL SHA-256。
	Checksum string
	// AppliedAt 执行时间（UTC）；读取时会兼容驱动返回的 time.Time / 文本。
	AppliedAt time.Time
	// ExecutionMS 执行耗时（毫秒）。
	ExecutionMS int64
	// Dirty 为 true 表示该迁移未成功完成，须人工确认。
	Dirty bool
}

// execer 抽象 *sql.DB 与 *sql.Tx 的执行面。
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// historyTable 返回按方言引用后的历史表名（含可选 schema）。
func (r *Runner) historyTable() string {
	if r.cfg.Schema != "" {
		return r.dialect.quoteIdent(r.cfg.Schema) + "." + r.dialect.quoteIdent(r.cfg.Table)
	}
	return r.dialect.quoteIdent(r.cfg.Table)
}

func (r *Runner) createHistorySQL() string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
    version BIGINT PRIMARY KEY,
    description VARCHAR(255) NOT NULL,
    checksum VARCHAR(64) NOT NULL,
    applied_at TIMESTAMP NOT NULL,
    execution_ms BIGINT NOT NULL,
    dirty BOOLEAN NOT NULL
)`, r.historyTable())
}

func (r *Runner) selectHistorySQL() string {
	return fmt.Sprintf(
		"SELECT version, description, checksum, applied_at, execution_ms, dirty FROM %s ORDER BY version",
		r.historyTable(),
	)
}

func (r *Runner) insertHistorySQL() string {
	return fmt.Sprintf(
		"INSERT INTO %s (version, description, checksum, applied_at, execution_ms, dirty) VALUES (%s)",
		r.historyTable(), strings.Join(r.dialect.placeholders(6), ", "),
	)
}

func (r *Runner) markDirtySQL() string {
	return fmt.Sprintf(
		"UPDATE %s SET dirty = %s WHERE version = %s",
		r.historyTable(), r.dialect.placeholder(1), r.dialect.placeholder(2),
	)
}

func (r *Runner) finishApplySQL() string {
	return fmt.Sprintf(
		"UPDATE %s SET dirty = %s, execution_ms = %s WHERE version = %s",
		r.historyTable(),
		r.dialect.placeholder(1), r.dialect.placeholder(2), r.dialect.placeholder(3),
	)
}

func (r *Runner) deleteHistorySQL() string {
	return fmt.Sprintf("DELETE FROM %s WHERE version = %s", r.historyTable(), r.dialect.placeholder(1))
}

// ensureHistory 幂等创建迁移历史表；并发 CREATE TABLE IF NOT EXISTS 由数据库保证安全。
func (r *Runner) ensureHistory(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, r.createHistorySQL()); err != nil {
		return fmt.Errorf("migrate: ensure history table %s: %w", r.historyTable(), err)
	}
	return nil
}

// loadRecords 读取全部历史记录（按 version 升序）。
func (r *Runner) loadRecords(ctx context.Context) ([]Record, error) {
	rows, err := r.db.QueryContext(ctx, r.selectHistorySQL())
	if err != nil {
		return nil, fmt.Errorf("migrate: load history %s: %w", r.historyTable(), err)
	}
	defer func() {
		// 忽略：只读查询遍历结束后关闭，关闭错误不影响已读取的数据。
		_ = rows.Close()
	}()

	var records []Record
	for rows.Next() {
		var (
			rec       Record
			appliedAt any
		)
		if err := rows.Scan(&rec.Version, &rec.Description, &rec.Checksum, &appliedAt, &rec.ExecutionMS, &rec.Dirty); err != nil {
			return nil, fmt.Errorf("migrate: scan history: %w", err)
		}
		ts, err := parseDBTime(appliedAt)
		if err != nil {
			return nil, fmt.Errorf("migrate: parse applied_at for version %06d: %w", rec.Version, err)
		}
		rec.AppliedAt = ts
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migrate: iterate history: %w", err)
	}
	return records, nil
}

// timeLayouts 覆盖 SQLite 文本时间与 MySQL DATETIME 的常见返回格式。
var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05.999999999",
}

// parseDBTime 将驱动返回的时间值统一为 time.Time。
func parseDBTime(v any) (time.Time, error) {
	switch t := v.(type) {
	case time.Time:
		return t, nil
	case string:
		return parseTimeString(t)
	case []byte:
		return parseTimeString(string(t))
	case nil:
		return time.Time{}, fmt.Errorf("nil timestamp")
	default:
		return time.Time{}, fmt.Errorf("unsupported timestamp type %T", v)
	}
}

func parseTimeString(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp %q", s)
}
