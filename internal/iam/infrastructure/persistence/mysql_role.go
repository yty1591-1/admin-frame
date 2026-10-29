package persistence

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// roleSelectColumns 是角色表的显式列清单（禁止 SELECT *，DATA-04）。
const roleSelectColumns = `id, channel, scope_type, merchant_id, parent_id, max_bindings, code, name, description,
	role_type, template_id, template_version, status, version, created_by, updated_by,
	created_at, updated_at, deleted_at`

// SQLRoleRepository 是 MySQL 8.0 的角色仓储实现。
//
// 依赖 DSN 设置 `parseTime=true&loc=UTC`（docs/migration-guide.md §4），
// 否则 DATETIME(3) 无法扫描为 time.Time。
//
// 事务边界：Save 在一个事务内写角色、权限与数据范围，避免出现
// 「角色已更新、授权未同步」的半成品状态；读方法不加事务，依赖单条查询一致性。
type SQLRoleRepository struct {
	sql *sql.DB
}

// NewSQLRoleRepository 基于已打开的 *sql.DB 创建角色仓储。
func NewSQLRoleRepository(sqlDB *sql.DB) *SQLRoleRepository {
	return &SQLRoleRepository{sql: sqlDB}
}

// FindByID 按主键加载角色（含权限与数据范围）。
func (r *SQLRoleRepository) FindByID(ctx context.Context, id string) (*domain.Role, error) {
	roleID, err := parseID(id)
	if err != nil {
		return nil, err
	}
	row := r.exec(ctx).QueryRowContext(ctx, `SELECT `+roleSelectColumns+` FROM iam_role WHERE id = ?`, roleID)
	role, err := scanRole(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.NotFound("role not found")
	}
	if err != nil {
		return nil, err
	}
	return role, r.attachRoleDetails(ctx, r.exec(ctx), role, roleID)
}

// FindByCode 按端别 + 商户 + 编码加载角色；软删角色仍返回，用于阻止 code 复用。
func (r *SQLRoleRepository) FindByCode(ctx context.Context, channel domain.Channel, merchantID, code string) (*domain.Role, error) {
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+roleSelectColumns+` FROM iam_role
		 WHERE channel = ? AND merchant_id = ? AND code = ?`,
		string(channel), merchantID, code)
	role, err := scanRole(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.NotFound("role not found")
	}
	if err != nil {
		return nil, err
	}
	roleID, err := parseID(role.ID)
	if err != nil {
		return nil, err
	}
	return role, r.attachRoleDetails(ctx, r.exec(ctx), role, roleID)
}

// Save 创建（expectedVersion=0）或更新角色，并同步权限与数据范围。
func (r *SQLRoleRepository) Save(ctx context.Context, role *domain.Role, expectedVersion int64) error {
	if role == nil {
		return apperr.BadRequest("role is required")
	}
	if r.sql == nil {
		return apperr.Unavailable("iam role repository is not configured")
	}
	// 已在用例事务中时复用该事务（UnitOfWork），否则自开一个：
	// 角色主体与权限 / 数据范围明细必须同批提交。
	return db.WithinUnitOfWork(ctx, r.sql, nil, func(ctx context.Context) error {
		if expectedVersion == 0 {
			return r.insert(ctx, role)
		}
		return r.update(ctx, role, expectedVersion)
	})
}

// insert 写入新角色；唯一键冲突映射为 40900。
func (r *SQLRoleRepository) insert(ctx context.Context, role *domain.Role) error {
	tx := r.exec(ctx)
	roleID, err := parseID(role.ID)
	if err != nil {
		return err
	}
	var templateID any = int64(0)
	if role.TemplateID != "" {
		parsed, parseErr := parseID(role.TemplateID)
		if parseErr != nil {
			return parseErr
		}
		templateID = parsed
	}
	parentID, err := nullableRoleID(role.ParentID)
	if err != nil {
		return err
	}
	var maxBindings any
	if role.MaxBindings != nil {
		maxBindings = *role.MaxBindings
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO iam_role
		(id, channel, scope_type, merchant_id, parent_id, max_bindings, code, name, description,
		 role_type, template_id, template_version, status, version,
		 created_by, updated_by, created_at, updated_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		roleID, string(role.Channel), string(role.Scope), role.MerchantID, parentID, maxBindings, role.Code,
		role.Name, role.Description, string(role.Type), templateID,
		role.TemplateVersion, string(role.Status), role.Version,
		role.CreatedBy, role.UpdatedBy, role.CreatedAt.UTC(), role.UpdatedAt.UTC(), nullTime(role.DeletedAt))
	if err := conflictOnDuplicate(err, "role code already exists in this channel and merchant"); err != nil {
		return err
	}
	return r.syncRoleDetails(ctx, tx, role, roleID)
}

// update 以乐观锁更新角色；version 不匹配返回 40900。
func (r *SQLRoleRepository) update(ctx context.Context, role *domain.Role, expectedVersion int64) error {
	tx := r.exec(ctx)
	roleID, err := parseID(role.ID)
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
		`SELECT version, channel, merchant_id, code FROM iam_role WHERE id = ? FOR UPDATE`,
		roleID).Scan(&currentVersion, &channel, &merchantID, &code)
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.NotFound("role not found")
	}
	if err != nil {
		return err
	}
	if currentVersion != expectedVersion {
		return apperr.Conflict("role version conflict")
	}
	if channel != string(role.Channel) || merchantID != role.MerchantID || code != role.Code {
		return apperr.BadRequest("role code, channel and merchant are immutable")
	}

	parentID, err := nullableRoleID(role.ParentID)
	if err != nil {
		return err
	}
	var maxBindings any
	if role.MaxBindings != nil {
		maxBindings = *role.MaxBindings
	}
	result, err := tx.ExecContext(ctx, `UPDATE iam_role
		SET name = ?, description = ?, parent_id = ?, max_bindings = ?, role_type = ?, template_version = ?,
		    status = ?, version = ?, updated_by = ?, updated_at = ?, deleted_at = ?
		WHERE id = ? AND version = ?`,
		role.Name, role.Description, parentID, maxBindings, string(role.Type), role.TemplateVersion,
		string(role.Status), role.Version,
		role.UpdatedBy, role.UpdatedAt.UTC(), nullTime(role.DeletedAt), roleID, expectedVersion)
	if err != nil {
		return err
	}
	// 乐观锁已由上面的 SELECT ... FOR UPDATE + version 比对完成；
	// MySQL RowsAffected 默认统计「实际变更行」，幂等写入（无字段变化）会返回 0，
	// 不能据此判为版本冲突，否则同值保存（如只改前置角色）会被误拒。
	if _, err := result.RowsAffected(); err != nil {
		return err
	}
	return r.syncRoleDetails(ctx, tx, role, roleID)
}

// List 按条件分页返回未软删角色，按 code、id 排序保证分页稳定。
//
// Channel 为空且 MerchantID 为空时表示 A 端内部查询；B 端调用方必须传 MerchantID，
// app 层负责拒绝 B 端空商户的查询（SEC-14）。
func (r *SQLRoleRepository) List(ctx context.Context, filter domain.RoleFilter) ([]*domain.Role, error) {
	clause, args := roleFilterClause(filter)
	builder := strings.Builder{}
	builder.WriteString(`SELECT ` + roleSelectColumns + ` FROM iam_role WHERE deleted_at IS NULL`)
	builder.WriteString(clause)
	builder.WriteString(` ORDER BY code, id LIMIT ? OFFSET ?`)
	args = append(args, normalizeLimit(filter.Limit), normalizeOffset(filter.Offset))

	rows, err := r.exec(ctx).QueryContext(ctx, builder.String(), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]*domain.Role, 0, normalizeLimit(filter.Limit))
	for rows.Next() {
		role, scanErr := scanRole(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		// 列表不返回权限与数据范围明细，避免 N+1；详情接口单独加载。
		role.Permissions = make(domain.PermissionSet)
		role.DataScopes = make(map[string]domain.DataScope)
		out = append(out, role)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Count 返回满足条件的未删除角色数；与 List 共用同一套过滤条件。
func (r *SQLRoleRepository) Count(ctx context.Context, filter domain.RoleFilter) (int, error) {
	clause, args := roleFilterClause(filter)
	var total int
	err := r.exec(ctx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM iam_role WHERE deleted_at IS NULL`+clause, args...).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total, nil
}

// ListByTemplate 返回由指定模板实例化的未删除角色，按 code、id 排序。
//
// 供模板漂移扫描：调用方比对 role.TemplateVersion 与模板当前 Version，
// 决定是否需要显式同步，而不是在读取时隐式联动。
func (r *SQLRoleRepository) ListByTemplate(ctx context.Context, templateID string) ([]*domain.Role, error) {
	parsed, err := parseID(templateID)
	if err != nil {
		return nil, err
	}
	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT `+roleSelectColumns+` FROM iam_role
		 WHERE template_id = ? AND deleted_at IS NULL ORDER BY code, id`, parsed)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]*domain.Role, 0)
	for rows.Next() {
		role, scanErr := scanRole(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, role)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// roleDescendantsSQL 用递归 CTE 展开子角色树；depth 上限与领域层一致，
// 脏数据成环时在到达上限后自然截断，而不是触发 cte_max_recursion_depth 报错。
const roleDescendantsSQL = `
WITH RECURSIVE role_descendants AS (
	SELECT id, parent_id, 1 AS depth
	FROM iam_role
	WHERE parent_id = ? AND deleted_at IS NULL
	UNION ALL
	SELECT child.id, child.parent_id, d.depth + 1
	FROM iam_role child
	JOIN role_descendants d ON child.parent_id = d.id
	WHERE child.deleted_at IS NULL AND d.depth < ?
)
SELECT ` + roleSelectColumns + `
FROM iam_role
WHERE id IN (SELECT id FROM role_descendants)
ORDER BY code, id`

// ListDescendants 返回以 roleID 为根的未删除子孙角色（不含根自身）。
func (r *SQLRoleRepository) ListDescendants(ctx context.Context, roleID string) ([]*domain.Role, error) {
	parsed, err := parseID(roleID)
	if err != nil {
		return nil, err
	}
	rows, err := r.exec(ctx).QueryContext(ctx, roleDescendantsSQL, parsed, domain.MaxRoleHierarchyDepth)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]*domain.Role, 0)
	for rows.Next() {
		role, scanErr := scanRole(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		// 子角色列表只用于删除检查与缓存失效，不需要权限明细。
		role.Permissions = make(domain.PermissionSet)
		role.DataScopes = make(map[string]domain.DataScope)
		out = append(out, role)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// roleAncestorsSQL 用递归 CTE 向上展开祖先链；depth 上限与领域层一致。
const roleAncestorsSQL = `
WITH RECURSIVE role_ancestors AS (
	SELECT r.id, r.parent_id, 1 AS depth
	FROM iam_role anchor
	JOIN iam_role r ON r.id = anchor.parent_id
	WHERE anchor.id = ? AND r.deleted_at IS NULL
	UNION ALL
	SELECT p.id, p.parent_id, a.depth + 1
	FROM iam_role p
	JOIN role_ancestors a ON p.id = a.parent_id
	WHERE p.deleted_at IS NULL AND a.depth < ?
)
SELECT ` + roleSelectColumns + `
FROM iam_role
WHERE id IN (SELECT id FROM role_ancestors)
ORDER BY id`

// ListAncestors 返回 roleID 的未删除祖先（不含自身），按 ID 排序。
//
// 命中已软删的父角色时链路自然断开：更深层角色不再进入结果集，
// 与鉴权快照的继承语义保持一致。
func (r *SQLRoleRepository) ListAncestors(ctx context.Context, roleID string) ([]*domain.Role, error) {
	parsed, err := parseID(roleID)
	if err != nil {
		return nil, err
	}
	rows, err := r.exec(ctx).QueryContext(ctx, roleAncestorsSQL, parsed, domain.MaxRoleHierarchyDepth)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]*domain.Role, 0, domain.MaxRoleHierarchyDepth)
	for rows.Next() {
		role, scanErr := scanRole(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		// 祖先链只用于约束判定，不需要权限明细。
		role.Permissions = make(domain.PermissionSet)
		role.DataScopes = make(map[string]domain.DataScope)
		out = append(out, role)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// roleFilterClause 生成 List / Count 共用的过滤子句，避免两处条件漂移。
func roleFilterClause(filter domain.RoleFilter) (clause string, args []any) {
	var builder strings.Builder
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

// attachRoleDetails 加载角色的权限与数据范围。
func (r *SQLRoleRepository) attachRoleDetails(ctx context.Context, q queryer, role *domain.Role, roleID int64) error {
	if err := loadPermissions(ctx, q, role, roleID); err != nil {
		return err
	}
	return loadScopes(ctx, q, role, roleID)
}

// syncRoleDetails 写入角色的权限与数据范围（差量 + 全量替换）。
func (r *SQLRoleRepository) syncRoleDetails(ctx context.Context, tx db.Querier, role *domain.Role, roleID int64) error {
	if err := syncPermissions(ctx, tx, role, roleID); err != nil {
		return err
	}
	return syncScopes(ctx, tx, role, roleID)
}

// syncPermissions 以「删除不在新集合中的行 + upsert 新集合」同步授权。
//
// 不做全量 delete + insert：保留未被撤销行的 created_at / granted_by，
// 让审计能回答「这个权限是什么时候第一次授予的」。
func syncPermissions(ctx context.Context, tx db.Querier, role *domain.Role, roleID int64) error {
	codes := role.PermissionCodes()
	if len(codes) == 0 {
		_, err := tx.ExecContext(ctx, `DELETE FROM iam_role_permission WHERE role_id = ?`, roleID)
		return err
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(codes)), ",")
	args := make([]any, 0, len(codes)+1)
	args = append(args, roleID)
	for _, code := range codes {
		args = append(args, string(code))
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM iam_role_permission WHERE role_id = ? AND permission_code NOT IN (`+placeholders+`)`,
		args...); err != nil {
		return err
	}

	for _, code := range codes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO iam_role_permission
			(role_id, permission_code, granted_by, created_at) VALUES (?, ?, ?, ?)
			AS new ON DUPLICATE KEY UPDATE granted_by = new.granted_by`,
			roleID, string(code), role.UpdatedBy, role.UpdatedAt.UTC()); err != nil {
			return err
		}
	}
	return nil
}

// syncScopes 差量同步角色的数据范围。
//
// 与权限一致，不做全量 delete + insert：保留未变更行的 created_at，
// 让审计能回答「这个数据范围是什么时候第一次配置的」。
func syncScopes(ctx context.Context, tx db.Querier, role *domain.Role, roleID int64) error {
	scopes := role.SortedDataScopes()
	if len(scopes) == 0 {
		_, err := tx.ExecContext(ctx, `DELETE FROM iam_role_data_scope WHERE role_id = ?`, roleID)
		return err
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(scopes)), ",")
	args := make([]any, 0, len(scopes)+1)
	args = append(args, roleID)
	for _, scope := range scopes {
		args = append(args, scope.Resource)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM iam_role_data_scope WHERE role_id = ? AND resource NOT IN (`+placeholders+`)`,
		args...); err != nil {
		return err
	}

	for _, scope := range scopes {
		payload, err := marshalScopeValues(scope)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO iam_role_data_scope
			(role_id, channel, resource, scope_type, scope_values, granted_by, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			AS new ON DUPLICATE KEY UPDATE
				scope_type = new.scope_type,
				scope_values = new.scope_values,
				granted_by = new.granted_by,
				updated_at = new.updated_at`,
			roleID, string(role.Channel), scope.Resource, string(scope.Type), payload,
			role.UpdatedBy, role.UpdatedAt.UTC(), role.UpdatedAt.UTC()); err != nil {
			return err
		}
	}
	return nil
}

// loadPermissions 加载角色授权码。
func loadPermissions(ctx context.Context, q queryer, role *domain.Role, roleID int64) error {
	rows, err := q.QueryContext(ctx,
		`SELECT permission_code FROM iam_role_permission WHERE role_id = ? ORDER BY permission_code`, roleID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	set := make(domain.PermissionSet)
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return err
		}
		set[iam.PermissionCode(code)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	role.Permissions = set
	return nil
}

// loadScopes 加载角色数据范围。
func loadScopes(ctx context.Context, q queryer, role *domain.Role, roleID int64) error {
	rows, err := q.QueryContext(ctx,
		`SELECT resource, scope_type, scope_values FROM iam_role_data_scope WHERE role_id = ? ORDER BY resource`, roleID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	scopes := make(map[string]domain.DataScope)
	for rows.Next() {
		var (
			resource string
			scopeTyp string
			raw      []byte
		)
		if err := rows.Scan(&resource, &scopeTyp, &raw); err != nil {
			return err
		}
		merchantIDs, err := unmarshalScopeValues(raw)
		if err != nil {
			return err
		}
		scopes[resource] = domain.DataScope{
			Resource:    resource,
			Type:        domain.DataScopeType(scopeTyp),
			MerchantIDs: merchantIDs,
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	role.DataScopes = scopes
	return nil
}

// queryer 抽象 *sql.DB 与 *sql.Tx，使明细加载在事务内外复用。
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// exec 返回当前请求的执行器：在 UnitOfWork 事务内返回 *sql.Tx，否则返回连接池。
//
// 所有读写都经此解析，保证「用例事务」对仓储透明：调用方只需在 ctx 上开一次事务。
func (r *SQLRoleRepository) exec(ctx context.Context) db.Querier {
	return resolveExecutor(ctx, r.sql)
}

// scanRow 是 *sql.Row 与 *sql.Rows 的公共扫描面。
type scanRow interface {
	Scan(dest ...any) error
}

// scanRole 扫描一行角色；deleted_at / parent_id 允许为 NULL，template_id 为 0 表示非模板实例。
func scanRole(row scanRow) (*domain.Role, error) {
	var (
		role        domain.Role
		parentID    sql.NullInt64
		maxBindings sql.NullInt64
		templateID  int64
		deletedAt   sql.NullTime
	)
	err := row.Scan(
		&role.ID, &role.Channel, &role.Scope, &role.MerchantID, &parentID, &maxBindings, &role.Code,
		&role.Name, &role.Description, &role.Type, &templateID, &role.TemplateVersion,
		&role.Status, &role.Version, &role.CreatedBy, &role.UpdatedBy,
		&role.CreatedAt, &role.UpdatedAt, &deletedAt,
	)
	if err != nil {
		return nil, err
	}
	if parentID.Valid {
		role.ParentID = strconv.FormatInt(parentID.Int64, 10)
	}
	if maxBindings.Valid {
		value := int(maxBindings.Int64)
		role.MaxBindings = &value
	}
	if templateID != 0 {
		role.TemplateID = strconv.FormatInt(templateID, 10)
	}
	if deletedAt.Valid {
		value := deletedAt.Time.UTC()
		role.DeletedAt = &value
	}
	role.CreatedAt = role.CreatedAt.UTC()
	role.UpdatedAt = role.UpdatedAt.UTC()
	role.Permissions = make(domain.PermissionSet)
	role.DataScopes = make(map[string]domain.DataScope)
	return &role, nil
}

// nullTime 把可空时间转换为 driver 可接受的值。
func nullTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

// nullableRoleID 把空串父角色转换为 SQL NULL；空串语义为「根角色」。
func nullableRoleID(value string) (any, error) {
	if value == "" {
		return nil, nil
	}
	return parseID(value)
}

// normalizeLimit 把分页大小限制在 [1, maxListLimit]。
func normalizeLimit(limit int) int {
	if limit <= 0 || limit > maxListLimit {
		return maxListLimit
	}
	return limit
}

// normalizeOffset 拒绝负偏移。
func normalizeOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}
