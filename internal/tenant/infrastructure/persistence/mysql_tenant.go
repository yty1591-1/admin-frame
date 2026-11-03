package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// MySQLRepository 是租户仓储的 MySQL 实现。
//
// 表结构见 migrations/000008_create_tenant.up.sql；状态迁移用
// `WHERE version = ?` 做 CAS，并发下第二次写入必然影响 0 行。
type MySQLRepository struct {
	db *sql.DB
}

// NewMySQLRepository 构造 MySQL 仓储；db 为 nil 返回错误（装配期即失败）。
func NewMySQLRepository(db *sql.DB) (*MySQLRepository, error) {
	if db == nil {
		return nil, errors.New("tenant repository requires a database handle")
	}
	return &MySQLRepository{db: db}, nil
}

// tenantColumns 固定列顺序，避免 SELECT * 与扫描顺序漂移。
const tenantColumns = "id, name, status, status_reason, version, suspended_at, terminated_at, created_at, updated_at"

// Create 插入租户；主键冲突返回 ErrAlreadyExists 而不是驱动错误。
func (r *MySQLRepository) Create(ctx context.Context, tenant domain.Tenant) error {
	if err := tenant.Validate(); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO tenant (`+tenantColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tenant.ID, tenant.Name, string(tenant.Status), tenant.StatusReason, tenant.Version,
		nullTime(tenant.SuspendedAt), nullTime(tenant.TerminatedAt), tenant.CreatedAt, tenant.UpdatedAt)
	if err != nil {
		if isDuplicateKey(err) {
			return fmt.Errorf("%w: %s", domain.ErrAlreadyExists, tenant.ID)
		}
		return fmt.Errorf("tenant: create %s: %w", tenant.ID, err)
	}
	return nil
}

// Get 按 ID 读取租户。
func (r *MySQLRepository) Get(ctx context.Context, id string) (domain.Tenant, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+tenantColumns+` FROM tenant WHERE id = ?`, strings.TrimSpace(id))
	tenant, err := scanTenant(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Tenant{}, domain.ErrNotFound
		}
		return domain.Tenant{}, fmt.Errorf("tenant: get %s: %w", id, err)
	}
	return tenant, nil
}

// List 按状态 / 名称过滤并分页；总数与当页数据分别查询。
func (r *MySQLRepository) List(ctx context.Context, filter domain.Filter) ([]domain.Tenant, int, error) {
	filter = filter.Normalize()

	where := make([]string, 0, 2)
	args := make([]any, 0, 4)
	if filter.Status != "" {
		where = append(where, "status = ?")
		args = append(args, string(filter.Status))
	}
	if filter.Keyword != "" {
		where = append(where, "name LIKE ?")
		args = append(args, "%"+escapeLike(filter.Keyword)+"%")
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tenant`+clause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("tenant: count: %w", err)
	}

	query := `SELECT ` + tenantColumns + ` FROM tenant` + clause + ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	rows, err := r.db.QueryContext(ctx, query, append(args, filter.Limit, filter.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("tenant: list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]domain.Tenant, 0, filter.Limit)
	for rows.Next() {
		tenant, scanErr := scanTenant(rows)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("tenant: scan: %w", scanErr)
		}
		items = append(items, tenant)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("tenant: iterate: %w", err)
	}
	return items, total, nil
}

// UpdateStatus 以 `version = tenant.Version - 1` 做 CAS 写入。
func (r *MySQLRepository) UpdateStatus(ctx context.Context, tenant domain.Tenant) error {
	if err := tenant.Validate(); err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx,
		`UPDATE tenant
		    SET status = ?, status_reason = ?, suspended_at = ?, terminated_at = ?,
		        version = ?, updated_at = ?
		  WHERE id = ? AND version = ?`,
		string(tenant.Status), tenant.StatusReason, nullTime(tenant.SuspendedAt), nullTime(tenant.TerminatedAt),
		tenant.Version, tenant.UpdatedAt, tenant.ID, tenant.Version-1)
	if err != nil {
		return fmt.Errorf("tenant: update status %s: %w", tenant.ID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("tenant: update status %s: %w", tenant.ID, err)
	}
	if affected == 1 {
		return nil
	}

	// 0 行有两种原因：租户不存在，或版本已被并发改动。二者语义不同，必须区分。
	var exists int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tenant WHERE id = ?`, tenant.ID).Scan(&exists); err != nil {
		return fmt.Errorf("tenant: verify %s: %w", tenant.ID, err)
	}
	if exists == 0 {
		return domain.ErrNotFound
	}
	return fmt.Errorf("%w: tenant %s version %d", domain.ErrVersionConflict, tenant.ID, tenant.Version-1)
}

// rowScanner 抽象 *sql.Row 与 *sql.Rows 的共同能力。
type rowScanner interface {
	Scan(dest ...any) error
}

// scanTenant 按 tenantColumns 的顺序扫描一行。
func scanTenant(scanner rowScanner) (domain.Tenant, error) {
	var (
		tenant       domain.Tenant
		status       string
		suspendedAt  sql.NullTime
		terminatedAt sql.NullTime
	)
	if err := scanner.Scan(
		&tenant.ID, &tenant.Name, &status, &tenant.StatusReason, &tenant.Version,
		&suspendedAt, &terminatedAt, &tenant.CreatedAt, &tenant.UpdatedAt,
	); err != nil {
		return domain.Tenant{}, err
	}
	tenant.Status = domain.Status(status)
	if suspendedAt.Valid {
		moment := suspendedAt.Time
		tenant.SuspendedAt = &moment
	}
	if terminatedAt.Valid {
		moment := terminatedAt.Time
		tenant.TerminatedAt = &moment
	}
	return tenant, nil
}

// nullTime 把可空时间转成驱动可写的值。
func nullTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

// escapeLike 转义 LIKE 通配符，避免名称里的 % / _ 变成模糊匹配。
func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)
	return replacer.Replace(value)
}

// isDuplicateKey 判断是否为 MySQL 主键 / 唯一键冲突（1062）。
func isDuplicateKey(err error) bool {
	return strings.Contains(err.Error(), "Error 1062") || strings.Contains(err.Error(), "Duplicate entry")
}
