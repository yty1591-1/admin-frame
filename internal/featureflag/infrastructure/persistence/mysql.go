// Package persistence 是 featureflag 域的 MySQL 持久化（ENT-11）。
//
// 表结构见 migrations/000020_create_feature_flag.up.sql。本包自带
// execer / queryer / 判重助手副本：与租户域持久化包互不依赖，
// 避免为几个小助手建立跨域内部 import（分层门禁红线）。
package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/featureflag/domain"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// MySQLRepository 是 flag 的 MySQL 仓储。
type MySQLRepository struct {
	db *sql.DB
}

// NewMySQLRepository 构造仓储；db 为 nil 返回错误（装配期即失败）。
func NewMySQLRepository(sqlDB *sql.DB) (*MySQLRepository, error) {
	if sqlDB == nil {
		return nil, errors.New("featureflag repository requires a database handle")
	}
	return &MySQLRepository{db: sqlDB}, nil
}

// flagColumns 与 scanFlag 一一对齐。
const flagColumns = "`key`, description, enabled, default_enabled, rules_json, permanent, expires_at, version, created_by, created_at, updated_at"

// execer / queryer 让仓储在事务内复用调用方的 *sql.Tx（pkg/db.TxFromContext）。
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (r *MySQLRepository) exec(ctx context.Context) execer {
	if tx, ok := db.TxFromContext(ctx); ok {
		return tx
	}
	return r.db
}

func (r *MySQLRepository) query(ctx context.Context) queryer {
	if tx, ok := db.TxFromContext(ctx); ok {
		return tx
	}
	return r.db
}

// Create 写入新 flag；key 冲突返回 domain.ErrDuplicateKey。
func (r *MySQLRepository) Create(ctx context.Context, f domain.Flag) error {
	rules, err := f.MarshalRules()
	if err != nil {
		return err
	}
	_, err = r.exec(ctx).ExecContext(ctx,
		`INSERT INTO feature_flag (`+flagColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.Key, f.Description, f.Status == domain.StatusEnabled, f.DefaultEnabled,
		rules, f.Permanent, nullTime(f.ExpiresAt), f.Version, f.CreatedBy,
		f.CreatedAt.UTC(), f.UpdatedAt.UTC())
	if err != nil {
		if isDuplicateEntry(err) {
			return domain.ErrDuplicateKey
		}
		return fmt.Errorf("featureflag: create flag: %w", err)
	}
	return nil
}

// Get 按 key 读取；不存在返回 domain.ErrNotFound。
func (r *MySQLRepository) Get(ctx context.Context, key string) (domain.Flag, error) {
	row := r.query(ctx).QueryRowContext(ctx,
		`SELECT `+flagColumns+` FROM feature_flag WHERE `+"`key`"+` = ?`, strings.TrimSpace(key))
	f, err := scanFlag(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Flag{}, domain.ErrNotFound
		}
		return domain.Flag{}, err
	}
	return f, nil
}

// List 按 state 过滤列出（key 字典序，稳定分页）。
func (r *MySQLRepository) List(ctx context.Context, state string, limit, offset int) ([]domain.Flag, int, error) {
	where := ""
	args := []any{}
	if state != "" {
		// expired 在库内与 disabled 同值（治理留痕靠审计），查询按
		// state=disabled 处理并排除 permanent？不——expired 行 status
		// 与 disabled 不可分，因此 List 的 expired 过滤在应用层不做：
		// 库内仅存 enabled 布尔。这里把 state 归一为 enabled / 全部。
		if state == string(domain.StatusEnabled) {
			where = " WHERE enabled = 1"
		} else if state == string(domain.StatusDisabled) || state == string(domain.StatusExpired) {
			where = " WHERE enabled = 0"
		}
	}
	var total int
	if err := r.query(ctx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM feature_flag`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("featureflag: count flags: %w", err)
	}
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := r.query(ctx).QueryContext(ctx,
		`SELECT `+flagColumns+` FROM feature_flag`+where+
			` ORDER BY `+"`key`"+` LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("featureflag: list flags: %w", err)
	}
	defer func() { _ = rows.Close() }()
	flags := make([]domain.Flag, 0, limit)
	for rows.Next() {
		f, scanErr := scanFlag(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		flags = append(flags, f)
	}
	return flags, total, rows.Err()
}

// Update 以 CAS 更新（expectedVersion 不匹配返回 domain.ErrVersionConflict）。
func (r *MySQLRepository) Update(ctx context.Context, f domain.Flag, expectedVersion int64) error {
	rules, err := f.MarshalRules()
	if err != nil {
		return err
	}
	status := domain.StatusDisabled
	if f.Status == domain.StatusEnabled {
		status = domain.StatusEnabled
	}
	res, err := r.exec(ctx).ExecContext(ctx,
		`UPDATE feature_flag SET description = ?, enabled = ?, default_enabled = ?, rules_json = ?,
		 permanent = ?, expires_at = ?, version = ?, updated_at = ?
		 WHERE `+"`key`"+` = ? AND version = ?`,
		f.Description, status == domain.StatusEnabled, f.DefaultEnabled, rules,
		f.Permanent, nullTime(f.ExpiresAt), f.Version, f.UpdatedAt.UTC(),
		f.Key, expectedVersion)
	if err != nil {
		return fmt.Errorf("featureflag: update flag: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("featureflag: update flag affected: %w", err)
	}
	if affected == 0 {
		// 区分不存在与版本冲突。
		if _, err := r.Get(ctx, f.Key); err != nil {
			return domain.ErrNotFound
		}
		return domain.ErrVersionConflict
	}
	return nil
}

// Delete 物理删除；不存在返回 domain.ErrNotFound。
func (r *MySQLRepository) Delete(ctx context.Context, key string) error {
	res, err := r.exec(ctx).ExecContext(ctx,
		`DELETE FROM feature_flag WHERE `+"`key`"+` = ?`, strings.TrimSpace(key))
	if err != nil {
		return fmt.Errorf("featureflag: delete flag: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("featureflag: delete flag affected: %w", err)
	}
	if affected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ListExpired 列出到期时刻仍为 enabled 的非 permanent flag（到期任务数据源）。
func (r *MySQLRepository) ListExpired(ctx context.Context, now time.Time, limit int) ([]domain.Flag, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.query(ctx).QueryContext(ctx,
		`SELECT `+flagColumns+` FROM feature_flag
		 WHERE enabled = 1 AND permanent = 0 AND expires_at IS NOT NULL AND expires_at <= ?
		 LIMIT ?`, now.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("featureflag: list expired flags: %w", err)
	}
	defer func() { _ = rows.Close() }()
	flags := make([]domain.Flag, 0, limit)
	for rows.Next() {
		f, scanErr := scanFlag(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		flags = append(flags, f)
	}
	return flags, rows.Err()
}

// scanner 抽象 *sql.Row 与 *sql.Rows 的 Scan。
type scanner interface{ Scan(dest ...any) error }

// scanFlag 行 → 聚合（rules_json 反序列化时再校验）。
func scanFlag(s scanner) (domain.Flag, error) {
	var (
		f         domain.Flag
		enabled   bool
		def       bool
		permanent bool
		rulesRaw  []byte
		expiresAt sql.NullTime
		createdAt time.Time
		updatedAt time.Time
	)
	if err := s.Scan(&f.Key, &f.Description, &enabled, &def, &rulesRaw, &permanent,
		&expiresAt, &f.Version, &f.CreatedBy, &createdAt, &updatedAt); err != nil {
		return domain.Flag{}, err
	}
	f.Status = domain.StatusDisabled
	if enabled {
		f.Status = domain.StatusEnabled
	}
	f.DefaultEnabled = def
	f.Permanent = permanent
	if expiresAt.Valid {
		f.ExpiresAt = expiresAt.Time
	}
	f.CreatedAt = createdAt
	f.UpdatedAt = updatedAt
	if err := f.UnmarshalRules(rulesRaw); err != nil {
		return domain.Flag{}, err
	}
	if err := f.Validate(); err != nil {
		return domain.Flag{}, fmt.Errorf("featureflag: loaded flag %q invalid: %w", f.Key, err)
	}
	return f, nil
}

// nullTime 零值转 NULL。
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

// isDuplicateEntry 判定 MySQL 1062 唯一冲突（自含副本，避免跨域依赖）。
func isDuplicateEntry(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "Error 1062") ||
		strings.Contains(err.Error(), "Duplicate entry")
}
