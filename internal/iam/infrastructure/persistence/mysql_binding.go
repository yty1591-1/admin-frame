package persistence

import (
	"context"
	"database/sql"
	"errors"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// bindingSelectColumns 是绑定表的显式列清单。
const bindingSelectColumns = `id, channel, subject_type, subject_id, merchant_id, role_id,
	expires_at, granted_by, created_at`

// SQLBindingRepository 是 MySQL 8.0 的绑定仓储实现。
//
// 幂等语义由唯一索引 uk_iam_binding 保证：重复绑定不新增行、不改变既有行，
// 因此重复调用不会产生「同一主体同一角色多条绑定」的脏数据。
type SQLBindingRepository struct {
	sql *sql.DB
}

// NewSQLBindingRepository 基于已打开的 *sql.DB 创建绑定仓储。
func NewSQLBindingRepository(sqlDB *sql.DB) *SQLBindingRepository {
	return &SQLBindingRepository{sql: sqlDB}
}

// exec 返回当前请求的执行器（UnitOfWork 事务内为 *sql.Tx）。
func (r *SQLBindingRepository) exec(ctx context.Context) db.Querier {
	return resolveExecutor(ctx, r.sql)
}

// Save 创建绑定；唯一键冲突时刷新有效期与授权人，保留原主键。
//
// 过期后重新绑定必须重新生效：只做 `role_id = role_id` 的空更新会让调用方
// 以为授权成功，实际快照仍为空。因此冲突分支更新 expires_at / granted_by，
// 主键与 created_at 保持不变，便于审计追溯首次授权时间。
func (r *SQLBindingRepository) Save(ctx context.Context, binding *domain.Binding) error {
	if binding == nil {
		return apperr.BadRequest("binding is required")
	}
	if r.sql == nil {
		return apperr.Unavailable("iam binding repository is not configured")
	}
	bindingID, err := parseID(binding.ID)
	if err != nil {
		return err
	}
	roleID, err := parseID(binding.RoleID)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx).ExecContext(ctx, `INSERT INTO iam_role_binding
		(id, channel, subject_type, subject_id, merchant_id, role_id, expires_at, granted_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE expires_at = VALUES(expires_at), granted_by = VALUES(granted_by)`,
		bindingID, string(binding.Channel), string(binding.SubjectType), binding.SubjectID,
		binding.MerchantID, roleID, nullTime(binding.ExpiresAt), binding.GrantedBy,
		binding.CreatedAt.UTC())
	return err
}

// Delete 解绑；不存在同样返回 nil（幂等）。
func (r *SQLBindingRepository) Delete(ctx context.Context, id string) error {
	if r.sql == nil {
		return apperr.Unavailable("iam binding repository is not configured")
	}
	bindingID, err := parseID(id)
	if err != nil {
		return err
	}
	if _, err := r.exec(ctx).ExecContext(ctx, `DELETE FROM iam_role_binding WHERE id = ?`, bindingID); err != nil {
		return err
	}
	return nil
}

// FindByID 按主键加载绑定；不存在返回 NotFound。
func (r *SQLBindingRepository) FindByID(ctx context.Context, id string) (*domain.Binding, error) {
	bindingID, err := parseID(id)
	if err != nil {
		return nil, err
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+bindingSelectColumns+` FROM iam_role_binding WHERE id = ?`, bindingID)
	binding, err := scanBinding(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.NotFound("binding not found")
	}
	if err != nil {
		return nil, err
	}
	return binding, nil
}

// ListBySubject 返回主体在 query.EffectiveTime() 时刻有效的绑定。
//
// 过期过滤放在 SQL 而不是内存，避免把全量绑定读出来再丢弃；
// 查询条件同时包含 channel / subject_type / subject_id / merchant_id 四列，
// 命中 idx_iam_binding_subject，且不会跨租户。
func (r *SQLBindingRepository) ListBySubject(ctx context.Context, query domain.SubjectQuery) ([]*domain.Binding, error) {
	if err := validateSubjectQuery(query); err != nil {
		return nil, err
	}
	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT `+bindingSelectColumns+` FROM iam_role_binding
		 WHERE channel = ? AND subject_type = ? AND subject_id = ? AND merchant_id = ?
		   AND (expires_at IS NULL OR expires_at > ?)
		 ORDER BY role_id, id`,
		string(query.Channel), string(query.SubjectType), query.SubjectID, query.MerchantID, query.EffectiveTime())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectBindings(rows)
}

// ListByRole 返回绑定到指定角色的全部记录（含已过期），用于删除前引用检查。
func (r *SQLBindingRepository) ListByRole(ctx context.Context, roleID string) ([]*domain.Binding, error) {
	parsedRoleID, err := parseID(roleID)
	if err != nil {
		return nil, err
	}
	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT `+bindingSelectColumns+` FROM iam_role_binding WHERE role_id = ? ORDER BY id`, parsedRoleID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectBindings(rows)
}

// collectBindings 遍历结果集并复制为绑定切片。
func collectBindings(rows *sql.Rows) ([]*domain.Binding, error) {
	out := make([]*domain.Binding, 0)
	for rows.Next() {
		binding, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// scanBinding 扫描一行绑定；expires_at 允许为 NULL。
func scanBinding(row scanRow) (*domain.Binding, error) {
	var (
		binding   domain.Binding
		expiresAt sql.NullTime
	)
	err := row.Scan(
		&binding.ID, &binding.Channel, &binding.SubjectType, &binding.SubjectID,
		&binding.MerchantID, &binding.RoleID, &expiresAt, &binding.GrantedBy, &binding.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if expiresAt.Valid {
		value := expiresAt.Time.UTC()
		binding.ExpiresAt = &value
	}
	binding.CreatedAt = binding.CreatedAt.UTC()
	return &binding, nil
}
