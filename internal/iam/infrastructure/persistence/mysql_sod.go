package persistence

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// sodGroupSelectColumns 是互斥组表的显式列清单（禁止 SELECT *，DATA-04）。
const sodGroupSelectColumns = `id, channel, merchant_id, code, name, description,
	max_active_roles, status, version, created_by, updated_by, created_at, updated_at, deleted_at`

// SQLSodGroupRepository 是 MySQL 8.0 的互斥角色组仓储实现。
//
// 组主体与组-角色关系在同一事务内写入，避免出现「组已生效、成员缺失」的中间态；
// 复合外键保证组与角色同端别、同商户。
type SQLSodGroupRepository struct {
	sql *sql.DB
}

// NewSQLSodGroupRepository 基于已打开的 *sql.DB 创建互斥组仓储。
func NewSQLSodGroupRepository(sqlDB *sql.DB) *SQLSodGroupRepository {
	return &SQLSodGroupRepository{sql: sqlDB}
}

// exec 返回当前请求的执行器（UnitOfWork 事务内为 *sql.Tx）。
func (r *SQLSodGroupRepository) exec(ctx context.Context) db.Querier {
	return resolveExecutor(ctx, r.sql)
}

// FindByID 按主键加载互斥组（含组内角色）。
func (r *SQLSodGroupRepository) FindByID(ctx context.Context, id string) (*domain.SodGroup, error) {
	if r.sql == nil {
		return nil, apperr.Unavailable("iam sod group repository is not configured")
	}
	groupID, err := parseID(id)
	if err != nil {
		return nil, err
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+sodGroupSelectColumns+` FROM iam_sod_group WHERE id = ?`, groupID)
	group, err := scanSodGroup(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.NotFound("sod group not found")
	}
	if err != nil {
		return nil, err
	}
	return group, r.attachRoles(ctx, r.exec(ctx), group, groupID)
}

// FindByCode 按端别 + 商户 + 编码加载互斥组；软删组仍返回，用于阻止 code 复用。
func (r *SQLSodGroupRepository) FindByCode(
	ctx context.Context, channel domain.Channel, merchantID, code string,
) (*domain.SodGroup, error) {
	if r.sql == nil {
		return nil, apperr.Unavailable("iam sod group repository is not configured")
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+sodGroupSelectColumns+` FROM iam_sod_group
		 WHERE channel = ? AND merchant_id = ? AND code = ?`,
		string(channel), merchantID, code)
	group, err := scanSodGroup(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.NotFound("sod group not found")
	}
	if err != nil {
		return nil, err
	}
	groupID, err := parseID(group.ID)
	if err != nil {
		return nil, err
	}
	return group, r.attachRoles(ctx, r.exec(ctx), group, groupID)
}

// Save 创建（expectedVersion=0）或更新互斥组，并同步组内角色。
func (r *SQLSodGroupRepository) Save(ctx context.Context, group *domain.SodGroup, expectedVersion int64) error {
	if group == nil {
		return apperr.BadRequest("sod group is required")
	}
	if r.sql == nil {
		return apperr.Unavailable("iam sod group repository is not configured")
	}
	return db.WithinUnitOfWork(ctx, r.sql, nil, func(ctx context.Context) error {
		if expectedVersion == 0 {
			return r.insert(ctx, group)
		}
		return r.update(ctx, group, expectedVersion)
	})
}

// insert 写入新互斥组；端内编码冲突映射为 40900。
func (r *SQLSodGroupRepository) insert(ctx context.Context, group *domain.SodGroup) error {
	tx := r.exec(ctx)
	groupID, err := parseID(group.ID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO iam_sod_group
		(id, channel, merchant_id, code, name, description, max_active_roles,
		 status, version, created_by, updated_by, created_at, updated_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		groupID, string(group.Channel), group.MerchantID, group.Code, group.Name, group.Description,
		group.MaxActiveRoles, string(group.Status), group.Version, group.CreatedBy, group.UpdatedBy,
		group.CreatedAt.UTC(), group.UpdatedAt.UTC(), nullTime(group.DeletedAt))
	if err := conflictOnDuplicate(err, "sod group code already exists in this channel and merchant"); err != nil {
		return err
	}
	return r.syncRoles(ctx, tx, group, groupID)
}

// update 以乐观锁更新互斥组；version 不匹配返回 40900。
func (r *SQLSodGroupRepository) update(ctx context.Context, group *domain.SodGroup, expectedVersion int64) error {
	tx := r.exec(ctx)
	groupID, err := parseID(group.ID)
	if err != nil {
		return err
	}
	var (
		currentVersion int64
		channel        string
		merchantID     string
		code           string
	)
	err = tx.QueryRowContext(ctx,
		`SELECT version, channel, merchant_id, code FROM iam_sod_group WHERE id = ? FOR UPDATE`, groupID).
		Scan(&currentVersion, &channel, &merchantID, &code)
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.NotFound("sod group not found")
	}
	if err != nil {
		return err
	}
	if currentVersion != expectedVersion {
		return apperr.Conflict("sod group version conflict")
	}
	if channel != string(group.Channel) || merchantID != group.MerchantID || code != group.Code {
		return apperr.BadRequest("sod group code, channel and merchant are immutable")
	}

	result, err := tx.ExecContext(ctx, `UPDATE iam_sod_group
		SET name = ?, description = ?, max_active_roles = ?, status = ?, version = ?,
		    updated_by = ?, updated_at = ?, deleted_at = ?
		WHERE id = ? AND version = ?`,
		group.Name, group.Description, group.MaxActiveRoles, string(group.Status), group.Version,
		group.UpdatedBy, group.UpdatedAt.UTC(), nullTime(group.DeletedAt), groupID, expectedVersion)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return apperr.Conflict("sod group version conflict")
	}
	return r.syncRoles(ctx, tx, group, groupID)
}

// List 按条件分页返回未软删互斥组，按 code、id 排序保证分页稳定。
func (r *SQLSodGroupRepository) List(ctx context.Context, filter domain.SodGroupFilter) ([]*domain.SodGroup, error) {
	if r.sql == nil {
		return nil, apperr.Unavailable("iam sod group repository is not configured")
	}
	clause, args := sodGroupFilterClause(filter)
	args = append(args, normalizeLimit(filter.Limit), normalizeOffset(filter.Offset))

	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT `+sodGroupSelectColumns+` FROM iam_sod_group WHERE deleted_at IS NULL`+
			clause+` ORDER BY code, id LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]*domain.SodGroup, 0, normalizeLimit(filter.Limit))
	ids := make([]int64, 0, len(out))
	for rows.Next() {
		group, scanErr := scanSodGroup(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		group.RoleIDs = make([]string, 0)
		id, parseErr := parseID(group.ID)
		if parseErr != nil {
			return nil, parseErr
		}
		ids = append(ids, id)
		out = append(out, group)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := r.attachRolesForGroups(ctx, r.exec(ctx), out, ids); err != nil {
		return nil, err
	}
	return out, nil
}

// Count 返回满足条件的未删除互斥组数；与 List 共用过滤条件。
func (r *SQLSodGroupRepository) Count(ctx context.Context, filter domain.SodGroupFilter) (int, error) {
	if r.sql == nil {
		return 0, apperr.Unavailable("iam sod group repository is not configured")
	}
	clause, args := sodGroupFilterClause(filter)
	var total int
	err := r.exec(ctx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM iam_sod_group WHERE deleted_at IS NULL`+clause, args...).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total, nil
}

// ListByRole 返回包含指定角色的未软删互斥组，供绑定期互斥校验。
func (r *SQLSodGroupRepository) ListByRole(
	ctx context.Context, channel domain.Channel, merchantID, roleID string,
) ([]*domain.SodGroup, error) {
	if r.sql == nil {
		return nil, apperr.Unavailable("iam sod group repository is not configured")
	}
	parsedRoleID, err := parseID(roleID)
	if err != nil {
		return nil, err
	}
	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT `+sodGroupSelectColumns+`
		 FROM iam_sod_group g
		 JOIN iam_sod_group_role gr ON gr.group_id = g.id
		 WHERE g.deleted_at IS NULL
		   AND gr.channel = ? AND gr.merchant_id = ? AND gr.role_id = ?
		 ORDER BY g.code, g.id`,
		string(channel), merchantID, parsedRoleID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]*domain.SodGroup, 0)
	ids := make([]int64, 0)
	for rows.Next() {
		group, scanErr := scanSodGroup(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		group.RoleIDs = make([]string, 0)
		id, parseErr := parseID(group.ID)
		if parseErr != nil {
			return nil, parseErr
		}
		ids = append(ids, id)
		out = append(out, group)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := r.attachRolesForGroups(ctx, r.exec(ctx), out, ids); err != nil {
		return nil, err
	}
	return out, nil
}

// sodGroupFilterClause 生成 List / Count 共用的过滤子句。
func sodGroupFilterClause(filter domain.SodGroupFilter) (clause string, args []any) {
	var builder strings.Builder
	args = make([]any, 0, 5)
	if filter.Channel != "" {
		builder.WriteString(` AND channel = ?`)
		args = append(args, string(filter.Channel))
	}
	if filter.MerchantID != "" {
		builder.WriteString(` AND merchant_id = ?`)
		args = append(args, filter.MerchantID)
	}
	if filter.Status != "" {
		builder.WriteString(` AND status = ?`)
		args = append(args, string(filter.Status))
	}
	if filter.Keyword != "" {
		builder.WriteString(` AND (code LIKE ? OR name LIKE ?)`)
		like := "%" + filter.Keyword + "%"
		args = append(args, like, like)
	}
	return builder.String(), args
}

// attachRoles 加载单个组的角色 ID。
func (r *SQLSodGroupRepository) attachRoles(
	ctx context.Context, q queryer, group *domain.SodGroup, groupID int64,
) error {
	roleIDs, err := loadSodGroupRoles(ctx, q, groupID)
	if err != nil {
		return err
	}
	group.RoleIDs = roleIDs
	return nil
}

// attachRolesForGroups 一次查询填充多个组的角色 ID，避免列表 N+1。
func (r *SQLSodGroupRepository) attachRolesForGroups(
	ctx context.Context, q queryer, groups []*domain.SodGroup, groupIDs []int64,
) error {
	if len(groups) == 0 {
		return nil
	}
	byGroup, err := loadSodGroupRolesByGroups(ctx, q, groupIDs)
	if err != nil {
		return err
	}
	for index, group := range groups {
		if roleIDs, ok := byGroup[groupIDs[index]]; ok {
			group.RoleIDs = roleIDs
		}
	}
	return nil
}

// syncRoles 差量同步组内角色：删除不在新集合中的行，并 upsert 新集合。
//
// 不做全量 delete + insert：保留未变更行的 created_at，便于审计「何时加入该组」。
func (r *SQLSodGroupRepository) syncRoles(
	ctx context.Context, tx db.Querier, group *domain.SodGroup, groupID int64,
) error {
	roleIDs := group.SortedRoleIDs()
	if len(roleIDs) == 0 {
		_, err := tx.ExecContext(ctx, `DELETE FROM iam_sod_group_role WHERE group_id = ?`, groupID)
		return err
	}

	parsed := make([]int64, 0, len(roleIDs))
	for _, roleID := range roleIDs {
		value, err := parseID(roleID)
		if err != nil {
			return err
		}
		parsed = append(parsed, value)
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(parsed)), ",")
	deleteArgs := make([]any, 0, len(parsed)+1)
	deleteArgs = append(deleteArgs, groupID)
	for _, value := range parsed {
		deleteArgs = append(deleteArgs, value)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM iam_sod_group_role WHERE group_id = ? AND role_id NOT IN (`+placeholders+`)`,
		deleteArgs...); err != nil {
		return err
	}

	for _, value := range parsed {
		if _, err := tx.ExecContext(ctx, `INSERT INTO iam_sod_group_role
			(group_id, role_id, channel, merchant_id, granted_by, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
			AS new ON DUPLICATE KEY UPDATE granted_by = new.granted_by`,
			groupID, value, string(group.Channel), group.MerchantID,
			group.UpdatedBy, group.UpdatedAt.UTC()); err != nil {
			return err
		}
	}
	return nil
}

// loadSodGroupRoles 加载单个组的角色 ID。
func loadSodGroupRoles(ctx context.Context, q queryer, groupID int64) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT role_id FROM iam_sod_group_role WHERE group_id = ? ORDER BY role_id`, groupID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]string, 0)
	for rows.Next() {
		var roleID int64
		if err := rows.Scan(&roleID); err != nil {
			return nil, err
		}
		out = append(out, strconv.FormatInt(roleID, 10))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// loadSodGroupRolesByGroups 一次查询加载多个组的角色 ID。
func loadSodGroupRolesByGroups(
	ctx context.Context, q queryer, groupIDs []int64,
) (map[int64][]string, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(groupIDs)), ",")
	args := make([]any, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		args = append(args, groupID)
	}
	rows, err := q.QueryContext(ctx,
		`SELECT group_id, role_id FROM iam_sod_group_role
		 WHERE group_id IN (`+placeholders+`) ORDER BY group_id, role_id`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[int64][]string, len(groupIDs))
	for rows.Next() {
		var (
			groupID int64
			roleID  int64
		)
		if err := rows.Scan(&groupID, &roleID); err != nil {
			return nil, err
		}
		out[groupID] = append(out[groupID], strconv.FormatInt(roleID, 10))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// scanSodGroup 扫描一行互斥组；deleted_at 允许为 NULL。
func scanSodGroup(row scanRow) (*domain.SodGroup, error) {
	var (
		group     domain.SodGroup
		deletedAt sql.NullTime
	)
	if err := row.Scan(
		&group.ID, &group.Channel, &group.MerchantID, &group.Code, &group.Name, &group.Description,
		&group.MaxActiveRoles, &group.Status, &group.Version, &group.CreatedBy, &group.UpdatedBy,
		&group.CreatedAt, &group.UpdatedAt, &deletedAt,
	); err != nil {
		return nil, err
	}
	if deletedAt.Valid {
		value := deletedAt.Time.UTC()
		group.DeletedAt = &value
	}
	group.CreatedAt = group.CreatedAt.UTC()
	group.UpdatedAt = group.UpdatedAt.UTC()
	group.RoleIDs = make([]string, 0)
	return &group, nil
}
