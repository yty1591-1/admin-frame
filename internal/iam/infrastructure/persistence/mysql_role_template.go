package persistence

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// roleTemplateSelectColumns 是模板表的显式列清单（禁止 SELECT *，DATA-04）。
const roleTemplateSelectColumns = `id, channel, code, name, description, status, version,
	created_by, updated_by, created_at, updated_at, deleted_at`

// SQLRoleTemplateRepository 是 MySQL 8.0 的角色模板仓储实现。
//
// 与角色仓储一致：模板主体与权限 / 数据范围明细同事务提交，
// 明细采用差量同步以保留首次授予时间。
type SQLRoleTemplateRepository struct {
	sql *sql.DB
}

// NewSQLRoleTemplateRepository 基于已打开的 *sql.DB 创建模板仓储。
func NewSQLRoleTemplateRepository(sqlDB *sql.DB) *SQLRoleTemplateRepository {
	return &SQLRoleTemplateRepository{sql: sqlDB}
}

// exec 返回当前请求的执行器（UnitOfWork 事务内为 *sql.Tx）。
func (r *SQLRoleTemplateRepository) exec(ctx context.Context) db.Querier {
	return resolveExecutor(ctx, r.sql)
}

// FindByID 按主键加载模板（含权限与数据范围）。
func (r *SQLRoleTemplateRepository) FindByID(ctx context.Context, id string) (*domain.RoleTemplate, error) {
	if r.sql == nil {
		return nil, apperr.Unavailable("iam role template repository is not configured")
	}
	templateID, err := parseID(id)
	if err != nil {
		return nil, err
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+roleTemplateSelectColumns+` FROM iam_role_template WHERE id = ?`, templateID)
	template, err := scanRoleTemplate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.NotFound("role template not found")
	}
	if err != nil {
		return nil, err
	}
	return template, r.attachTemplateDetails(ctx, r.exec(ctx), template, templateID)
}

// FindByCode 按端别 + 编码加载模板；软删模板仍返回，用于阻止 code 复用。
func (r *SQLRoleTemplateRepository) FindByCode(ctx context.Context, channel domain.Channel, code string) (*domain.RoleTemplate, error) {
	if r.sql == nil {
		return nil, apperr.Unavailable("iam role template repository is not configured")
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+roleTemplateSelectColumns+` FROM iam_role_template WHERE channel = ? AND code = ?`,
		string(channel), code)
	template, err := scanRoleTemplate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.NotFound("role template not found")
	}
	if err != nil {
		return nil, err
	}
	templateID, err := parseID(template.ID)
	if err != nil {
		return nil, err
	}
	return template, r.attachTemplateDetails(ctx, r.exec(ctx), template, templateID)
}

// Save 创建（expectedVersion=0）或更新模板，并同步权限与数据范围。
func (r *SQLRoleTemplateRepository) Save(ctx context.Context, template *domain.RoleTemplate, expectedVersion int64) error {
	if template == nil {
		return apperr.BadRequest("role template is required")
	}
	if r.sql == nil {
		return apperr.Unavailable("iam role template repository is not configured")
	}
	return db.WithinUnitOfWork(ctx, r.sql, nil, func(ctx context.Context) error {
		if expectedVersion == 0 {
			return r.insert(ctx, template)
		}
		return r.update(ctx, template, expectedVersion)
	})
}

// insert 写入新模板；端内编码冲突映射为 40900。
func (r *SQLRoleTemplateRepository) insert(ctx context.Context, template *domain.RoleTemplate) error {
	tx := r.exec(ctx)
	templateID, err := parseID(template.ID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO iam_role_template
		(id, channel, code, name, description, status, version,
		 created_by, updated_by, created_at, updated_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		templateID, string(template.Channel), template.Code, template.Name, template.Description,
		string(template.Status), template.Version, template.CreatedBy, template.UpdatedBy,
		template.CreatedAt.UTC(), template.UpdatedAt.UTC(), nullTime(template.DeletedAt))
	if err := conflictOnDuplicate(err, "role template code already exists in this channel"); err != nil {
		return err
	}
	return r.syncTemplateDetails(ctx, tx, template, templateID)
}

// update 以乐观锁更新模板；version 不匹配返回 40900。
func (r *SQLRoleTemplateRepository) update(ctx context.Context, template *domain.RoleTemplate, expectedVersion int64) error {
	tx := r.exec(ctx)
	templateID, err := parseID(template.ID)
	if err != nil {
		return err
	}
	var (
		currentVersion int64
		channel        string
		code           string
	)
	err = tx.QueryRowContext(ctx,
		`SELECT version, channel, code FROM iam_role_template WHERE id = ? FOR UPDATE`, templateID).
		Scan(&currentVersion, &channel, &code)
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.NotFound("role template not found")
	}
	if err != nil {
		return err
	}
	if currentVersion != expectedVersion {
		return apperr.Conflict("role template version conflict")
	}
	if channel != string(template.Channel) || code != template.Code {
		return apperr.BadRequest("role template code and channel are immutable")
	}

	result, err := tx.ExecContext(ctx, `UPDATE iam_role_template
		SET name = ?, description = ?, status = ?, version = ?,
		    updated_by = ?, updated_at = ?, deleted_at = ?
		WHERE id = ? AND version = ?`,
		template.Name, template.Description, string(template.Status), template.Version,
		template.UpdatedBy, template.UpdatedAt.UTC(), nullTime(template.DeletedAt),
		templateID, expectedVersion)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return apperr.Conflict("role template version conflict")
	}
	return r.syncTemplateDetails(ctx, tx, template, templateID)
}

// List 按条件分页返回未软删模板，按 code、id 排序保证分页稳定。
func (r *SQLRoleTemplateRepository) List(ctx context.Context, filter domain.RoleTemplateFilter) ([]*domain.RoleTemplate, error) {
	if r.sql == nil {
		return nil, apperr.Unavailable("iam role template repository is not configured")
	}
	clause, args := roleTemplateFilterClause(filter)
	args = append(args, normalizeLimit(filter.Limit), normalizeOffset(filter.Offset))

	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT `+roleTemplateSelectColumns+` FROM iam_role_template WHERE deleted_at IS NULL`+
			clause+` ORDER BY code, id LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]*domain.RoleTemplate, 0, normalizeLimit(filter.Limit))
	for rows.Next() {
		template, scanErr := scanRoleTemplate(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		// 列表不返回明细，避免 N+1；详情接口单独加载。
		template.Permissions = make(domain.PermissionSet)
		template.DataScopes = make(map[string]domain.DataScope)
		out = append(out, template)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Count 返回满足条件的未删除模板数；与 List 共用过滤条件。
func (r *SQLRoleTemplateRepository) Count(ctx context.Context, filter domain.RoleTemplateFilter) (int, error) {
	if r.sql == nil {
		return 0, apperr.Unavailable("iam role template repository is not configured")
	}
	clause, args := roleTemplateFilterClause(filter)
	var total int
	if err := r.exec(ctx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM iam_role_template WHERE deleted_at IS NULL`+clause, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

// roleTemplateFilterClause 生成 List / Count 共用的过滤子句。
func roleTemplateFilterClause(filter domain.RoleTemplateFilter) (string, []any) {
	var builder strings.Builder
	args := make([]any, 0, 3)
	if filter.Channel != "" {
		builder.WriteString(` AND channel = ?`)
		args = append(args, string(filter.Channel))
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

// attachTemplateDetails 加载模板的权限与数据范围。
func (r *SQLRoleTemplateRepository) attachTemplateDetails(
	ctx context.Context, q queryer, template *domain.RoleTemplate, templateID int64,
) error {
	permissions, err := loadTemplatePermissions(ctx, q, templateID)
	if err != nil {
		return err
	}
	scopes, err := loadTemplateScopes(ctx, q, templateID)
	if err != nil {
		return err
	}
	template.Permissions = permissions
	template.DataScopes = scopes
	return nil
}

// syncTemplateDetails 写入模板的权限与数据范围。
func (r *SQLRoleTemplateRepository) syncTemplateDetails(
	ctx context.Context, tx db.Querier, template *domain.RoleTemplate, templateID int64,
) error {
	if err := syncTemplatePermissions(ctx, tx, template, templateID); err != nil {
		return err
	}
	return syncTemplateScopes(ctx, tx, template, templateID)
}

// syncTemplatePermissions 差量同步模板权限，保留未撤销行的 created_at。
func syncTemplatePermissions(ctx context.Context, tx db.Querier, template *domain.RoleTemplate, templateID int64) error {
	codes := template.PermissionCodes()
	if len(codes) == 0 {
		_, err := tx.ExecContext(ctx, `DELETE FROM iam_role_template_permission WHERE template_id = ?`, templateID)
		return err
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(codes)), ",")
	args := make([]any, 0, len(codes)+1)
	args = append(args, templateID)
	for _, code := range codes {
		args = append(args, string(code))
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM iam_role_template_permission
		 WHERE template_id = ? AND permission_code NOT IN (`+placeholders+`)`,
		args...); err != nil {
		return err
	}

	for _, code := range codes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO iam_role_template_permission
			(template_id, permission_code, granted_by, created_at) VALUES (?, ?, ?, ?)
			AS new ON DUPLICATE KEY UPDATE granted_by = new.granted_by`,
			templateID, string(code), template.UpdatedBy, template.UpdatedAt.UTC()); err != nil {
			return err
		}
	}
	return nil
}

// syncTemplateScopes 差量同步模板数据范围。
func syncTemplateScopes(ctx context.Context, tx db.Querier, template *domain.RoleTemplate, templateID int64) error {
	scopes := template.SortedDataScopes()
	if len(scopes) == 0 {
		_, err := tx.ExecContext(ctx, `DELETE FROM iam_role_template_data_scope WHERE template_id = ?`, templateID)
		return err
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(scopes)), ",")
	args := make([]any, 0, len(scopes)+1)
	args = append(args, templateID)
	for _, scope := range scopes {
		args = append(args, scope.Resource)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM iam_role_template_data_scope
		 WHERE template_id = ? AND resource NOT IN (`+placeholders+`)`,
		args...); err != nil {
		return err
	}

	for _, scope := range scopes {
		payload, err := marshalScopeValues(scope)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO iam_role_template_data_scope
			(template_id, resource, scope_type, scope_values, granted_by, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
			AS new ON DUPLICATE KEY UPDATE
				scope_type = new.scope_type,
				scope_values = new.scope_values,
				granted_by = new.granted_by,
				updated_at = new.updated_at`,
			templateID, scope.Resource, string(scope.Type), payload,
			template.UpdatedBy, template.UpdatedAt.UTC()); err != nil {
			return err
		}
	}
	return nil
}

// loadTemplatePermissions 加载模板权限码。
func loadTemplatePermissions(ctx context.Context, q queryer, templateID int64) (domain.PermissionSet, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT permission_code FROM iam_role_template_permission
		 WHERE template_id = ? ORDER BY permission_code`, templateID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	set := make(domain.PermissionSet)
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		set[iam.PermissionCode(code)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return set, nil
}

// loadTemplateScopes 加载模板数据范围。
func loadTemplateScopes(ctx context.Context, q queryer, templateID int64) (map[string]domain.DataScope, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT resource, scope_type, scope_values FROM iam_role_template_data_scope
		 WHERE template_id = ? ORDER BY resource`, templateID)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		merchantIDs, err := unmarshalScopeValues(raw)
		if err != nil {
			return nil, err
		}
		scopes[resource] = domain.DataScope{
			Resource:    resource,
			Type:        domain.DataScopeType(scopeTyp),
			MerchantIDs: merchantIDs,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return scopes, nil
}

// scanRoleTemplate 扫描一行模板；deleted_at 允许为 NULL。
func scanRoleTemplate(row scanRow) (*domain.RoleTemplate, error) {
	var (
		template  domain.RoleTemplate
		deletedAt sql.NullTime
	)
	if err := row.Scan(
		&template.ID, &template.Channel, &template.Code, &template.Name, &template.Description,
		&template.Status, &template.Version, &template.CreatedBy, &template.UpdatedBy,
		&template.CreatedAt, &template.UpdatedAt, &deletedAt,
	); err != nil {
		return nil, err
	}
	if deletedAt.Valid {
		value := deletedAt.Time.UTC()
		template.DeletedAt = &value
	}
	template.CreatedAt = template.CreatedAt.UTC()
	template.UpdatedAt = template.UpdatedAt.UTC()
	return &template, nil
}
