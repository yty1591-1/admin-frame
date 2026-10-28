package persistence

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// SQLRoleConstraintRepository 是 MySQL 8.0 的角色前置约束仓储实现。
//
// 前置关系是全量替换语义：调用方提交完整集合，仓储在同一事务内删除旧行并写入新行。
// 复合外键保证两个角色同端别、同商户，领域层保证不自引用。
type SQLRoleConstraintRepository struct {
	sql *sql.DB
}

// NewSQLRoleConstraintRepository 基于已打开的 *sql.DB 创建前置约束仓储。
func NewSQLRoleConstraintRepository(sqlDB *sql.DB) *SQLRoleConstraintRepository {
	return &SQLRoleConstraintRepository{sql: sqlDB}
}

// exec 返回当前请求的执行器（UnitOfWork 事务内为 *sql.Tx）。
func (r *SQLRoleConstraintRepository) exec(ctx context.Context) db.Querier {
	return resolveExecutor(ctx, r.sql)
}

// ListByRole 返回指定角色的前置约束；无约束时返回空集合而非 NotFound。
func (r *SQLRoleConstraintRepository) ListByRole(ctx context.Context, roleID string) (domain.RoleConstraint, error) {
	if r.sql == nil {
		return domain.RoleConstraint{}, apperr.Unavailable("iam role constraint repository is not configured")
	}
	parsed, err := parseID(roleID)
	if err != nil {
		return domain.RoleConstraint{}, err
	}
	required, err := loadPrerequisiteIDs(ctx, r.exec(ctx), parsed)
	if err != nil {
		return domain.RoleConstraint{}, err
	}
	return domain.RoleConstraint{RoleID: roleID, RequiredRoleIDs: required}, nil
}

// ListByRoles 批量返回前置约束，按入参顺序返回（无约束的角色返回空集合）。
func (r *SQLRoleConstraintRepository) ListByRoles(ctx context.Context, roleIDs []string) ([]domain.RoleConstraint, error) {
	out := make([]domain.RoleConstraint, 0, len(roleIDs))
	if len(roleIDs) == 0 {
		return out, nil
	}
	if r.sql == nil {
		return nil, apperr.Unavailable("iam role constraint repository is not configured")
	}

	parsed := make([]int64, len(roleIDs))
	placeholders := make([]string, len(roleIDs))
	args := make([]any, 0, len(roleIDs))
	for index, roleID := range roleIDs {
		value, err := parseID(roleID)
		if err != nil {
			return nil, err
		}
		parsed[index] = value
		placeholders[index] = "?"
		args = append(args, value)
	}

	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT role_id, required_role_id FROM iam_role_prerequisite
		 WHERE role_id IN (`+strings.Join(placeholders, ",")+`)
		 ORDER BY role_id, required_role_id`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	byRole := make(map[int64][]string, len(roleIDs))
	for rows.Next() {
		var (
			roleID     int64
			requiredID int64
		)
		if err := rows.Scan(&roleID, &requiredID); err != nil {
			return nil, err
		}
		byRole[roleID] = append(byRole[roleID], formatID(requiredID))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for index, roleID := range roleIDs {
		required := byRole[parsed[index]]
		if required == nil {
			required = make([]string, 0)
		}
		out = append(out, domain.RoleConstraint{RoleID: roleID, RequiredRoleIDs: required})
	}
	return out, nil
}

// Replace 全量替换角色的前置角色；租户字段取自 role，复合外键拒绝跨租户关系。
func (r *SQLRoleConstraintRepository) Replace(
	ctx context.Context, role *domain.Role, requiredRoleIDs []string, actor string,
) error {
	if role == nil {
		return apperr.BadRequest("role is required")
	}
	if strings.TrimSpace(actor) == "" {
		return apperr.BadRequest("actor is required")
	}
	constraint, err := domain.NewRoleConstraint(role.ID, requiredRoleIDs)
	if err != nil {
		return err
	}
	if r.sql == nil {
		return apperr.Unavailable("iam role constraint repository is not configured")
	}
	roleID, err := parseID(role.ID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()

	return db.WithinUnitOfWork(ctx, r.sql, nil, func(ctx context.Context) error {
		tx := resolveExecutor(ctx, r.sql)
		if _, err := tx.ExecContext(ctx, `DELETE FROM iam_role_prerequisite WHERE role_id = ?`, roleID); err != nil {
			return err
		}
		for _, requiredID := range constraint.Sorted() {
			parsed, err := parseID(requiredID)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO iam_role_prerequisite
				(role_id, required_role_id, channel, merchant_id, created_by, created_at)
				VALUES (?, ?, ?, ?, ?, ?)`,
				roleID, parsed, string(role.Channel), role.MerchantID, actor, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListDependents 返回把 roleID 作为前置角色的角色 ID，按 ID 排序。
func (r *SQLRoleConstraintRepository) ListDependents(ctx context.Context, roleID string) ([]string, error) {
	if r.sql == nil {
		return nil, apperr.Unavailable("iam role constraint repository is not configured")
	}
	parsed, err := parseID(roleID)
	if err != nil {
		return nil, err
	}
	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT role_id FROM iam_role_prerequisite
		 WHERE required_role_id = ? ORDER BY role_id`, parsed)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]string, 0)
	for rows.Next() {
		var dependentID int64
		if err := rows.Scan(&dependentID); err != nil {
			return nil, err
		}
		out = append(out, formatID(dependentID))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// loadPrerequisiteIDs 加载单个角色的前置角色 ID。
func loadPrerequisiteIDs(ctx context.Context, q queryer, roleID int64) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT required_role_id FROM iam_role_prerequisite WHERE role_id = ? ORDER BY required_role_id`,
		roleID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]string, 0)
	for rows.Next() {
		var requiredID int64
		if err := rows.Scan(&requiredID); err != nil {
			return nil, err
		}
		out = append(out, formatID(requiredID))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// formatID 把数据库 BIGINT 主键转回字符串 ID。
func formatID(value int64) string { return strconv.FormatInt(value, 10) }
