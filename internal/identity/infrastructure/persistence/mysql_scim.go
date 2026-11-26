package persistence

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// scimTokenColumns 是 SCIM 令牌表的显式列清单。
const scimTokenColumns = `id, provider, token_hash, fingerprint, status, expires_at, created_by, created_at, revoked_at`

// scimGroupColumns 是 SCIM 组表的显式列清单。
const scimGroupColumns = `id, provider, external_id, display_name, mapped_role, version, created_at, updated_at, deleted_at`

// SQLScimTokenRepository 是 MySQL 8.0 的 SCIM 令牌仓储。
type SQLScimTokenRepository struct {
	sql *sql.DB
}

// SQLScimGroupRepository 是 MySQL 8.0 的 SCIM 组仓储。
type SQLScimGroupRepository struct {
	sql *sql.DB
}

// 编译期断言。
var (
	_ domain.ScimTokenRepository = (*SQLScimTokenRepository)(nil)
	_ domain.ScimGroupRepository = (*SQLScimGroupRepository)(nil)
)

// NewSQLScimRepositories 创建 SCIM 令牌与组仓储。
func NewSQLScimRepositories(sqlDB *sql.DB) (*SQLScimTokenRepository, *SQLScimGroupRepository) {
	return &SQLScimTokenRepository{sql: sqlDB}, &SQLScimGroupRepository{sql: sqlDB}
}

// exec 返回当前请求应使用的执行器：事务上下文优先。
func (r *SQLScimTokenRepository) exec(ctx context.Context) db.Querier {
	return resolveExecutor(ctx, r.sql)
}

// exec 返回当前请求应使用的执行器：事务上下文优先。
func (r *SQLScimGroupRepository) exec(ctx context.Context) db.Querier {
	return resolveExecutor(ctx, r.sql)
}

// Create 写入新令牌；摘要重复返回 Conflict。
func (r *SQLScimTokenRepository) Create(ctx context.Context, token *domain.ScimToken) error {
	if token == nil {
		return apperr.BadRequest("scim token is required")
	}
	id, err := parseID(token.ID)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx).ExecContext(ctx, `INSERT INTO identity_scim_token
		(id, provider, token_hash, fingerprint, status, expires_at, created_by, created_at, revoked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, token.Provider, token.TokenHash, token.Fingerprint, string(token.Status),
		nullTime(token.ExpiresAt), token.CreatedBy, token.CreatedAt.UTC(), nullTime(token.RevokedAt))
	return conflictOnDuplicate(err, "scim token already exists")
}

// UpdateStatus 更新令牌状态、宽限到期与吊销时间。
func (r *SQLScimTokenRepository) UpdateStatus(ctx context.Context, token *domain.ScimToken) error {
	if token == nil {
		return apperr.BadRequest("scim token is required")
	}
	id, err := parseID(token.ID)
	if err != nil {
		return err
	}
	result, err := r.exec(ctx).ExecContext(ctx, `UPDATE identity_scim_token
		SET status = ?, expires_at = ?, revoked_at = ? WHERE id = ?`,
		string(token.Status), nullTime(token.ExpiresAt), nullTime(token.RevokedAt), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return apperr.NotFound("scim token not found")
	}
	return nil
}

// FindByHash 按 provider + 摘要加载令牌。
func (r *SQLScimTokenRepository) FindByHash(ctx context.Context, provider, tokenHash string) (*domain.ScimToken, error) {
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+scimTokenColumns+` FROM identity_scim_token WHERE provider = ? AND token_hash = ?`,
		strings.TrimSpace(provider), strings.TrimSpace(tokenHash))
	token, err := scanScimToken(row)
	if err != nil {
		return nil, resolveNotFound(err, "scim token not found")
	}
	return token, nil
}

// FindByID 按主键加载令牌。
func (r *SQLScimTokenRepository) FindByID(ctx context.Context, id string) (*domain.ScimToken, error) {
	pk, err := parseID(id)
	if err != nil {
		return nil, apperr.NotFound("scim token not found")
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+scimTokenColumns+` FROM identity_scim_token WHERE id = ?`, pk)
	token, err := scanScimToken(row)
	if err != nil {
		return nil, resolveNotFound(err, "scim token not found")
	}
	return token, nil
}

// ListByProvider 返回 provider 下全部令牌，按创建时间倒序。
func (r *SQLScimTokenRepository) ListByProvider(ctx context.Context, provider string) ([]*domain.ScimToken, error) {
	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT `+scimTokenColumns+` FROM identity_scim_token
		 WHERE provider = ? ORDER BY created_at DESC, id DESC`, strings.TrimSpace(provider))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*domain.ScimToken, 0, 4)
	for rows.Next() {
		token, scanErr := scanScimToken(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, token)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// FindByID 按主键加载组；软删组仍返回。
func (r *SQLScimGroupRepository) FindByID(ctx context.Context, id string) (*domain.ScimGroup, error) {
	pk, err := parseID(id)
	if err != nil {
		return nil, apperr.NotFound("scim group not found")
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+scimGroupColumns+` FROM identity_scim_group WHERE id = ?`, pk)
	group, err := scanScimGroup(row)
	if err != nil {
		return nil, resolveNotFound(err, "scim group not found")
	}
	return group, nil
}

// FindByExternalID 按 provider + 外部 ID 加载组。
func (r *SQLScimGroupRepository) FindByExternalID(ctx context.Context, provider, externalID string) (*domain.ScimGroup, error) {
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+scimGroupColumns+` FROM identity_scim_group WHERE provider = ? AND external_id = ?`,
		strings.TrimSpace(provider), strings.TrimSpace(externalID))
	group, err := scanScimGroup(row)
	if err != nil {
		return nil, resolveNotFound(err, "scim group not found")
	}
	return group, nil
}

// FindByDisplayName 按 provider + 展示名加载组。
func (r *SQLScimGroupRepository) FindByDisplayName(ctx context.Context, provider, displayName string) (*domain.ScimGroup, error) {
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+scimGroupColumns+` FROM identity_scim_group WHERE provider = ? AND display_name = ?`,
		strings.TrimSpace(provider), strings.TrimSpace(displayName))
	group, err := scanScimGroup(row)
	if err != nil {
		return nil, resolveNotFound(err, "scim group not found")
	}
	return group, nil
}

// Create 创建组；唯一键冲突返回 Conflict。
func (r *SQLScimGroupRepository) Create(ctx context.Context, group *domain.ScimGroup) error {
	if group == nil {
		return apperr.BadRequest("scim group is required")
	}
	id, err := parseID(group.ID)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx).ExecContext(ctx, `INSERT INTO identity_scim_group
		(id, provider, external_id, display_name, mapped_role, version, created_at, updated_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, group.Provider, group.ExternalID, group.DisplayName, group.MappedRole,
		group.Version, group.CreatedAt.UTC(), group.UpdatedAt.UTC(), nullTime(group.DeletedAt))
	return conflictOnDuplicate(err, "scim group already exists")
}

// Update 更新组并使用乐观锁。
func (r *SQLScimGroupRepository) Update(ctx context.Context, group *domain.ScimGroup, expectedVersion int64) error {
	if group == nil {
		return apperr.BadRequest("scim group is required")
	}
	id, err := parseID(group.ID)
	if err != nil {
		return err
	}
	result, err := r.exec(ctx).ExecContext(ctx, `UPDATE identity_scim_group
		SET provider = ?, external_id = ?, display_name = ?, mapped_role = ?,
		    version = ?, updated_at = ?, deleted_at = ?
		WHERE id = ? AND version = ?`,
		group.Provider, group.ExternalID, group.DisplayName, group.MappedRole,
		group.Version, group.UpdatedAt.UTC(), nullTime(group.DeletedAt), id, expectedVersion)
	if err != nil {
		return conflictOnDuplicate(err, "scim group already exists")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return r.versionConflict(ctx, id, "scim group version conflict")
	}
	return nil
}

// SoftDelete 软删组并使用乐观锁。
func (r *SQLScimGroupRepository) SoftDelete(ctx context.Context, group *domain.ScimGroup, expectedVersion int64) error {
	if group == nil {
		return apperr.BadRequest("scim group is required")
	}
	id, err := parseID(group.ID)
	if err != nil {
		return err
	}
	result, err := r.exec(ctx).ExecContext(ctx, `UPDATE identity_scim_group
		SET version = ?, updated_at = ?, deleted_at = ? WHERE id = ? AND version = ?`,
		group.Version, group.UpdatedAt.UTC(), nullTime(group.DeletedAt), id, expectedVersion)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return r.versionConflict(ctx, id, "scim group version conflict")
	}
	return nil
}

// versionConflict 区分「记录不存在」与「版本不匹配」。
func (r *SQLScimGroupRepository) versionConflict(ctx context.Context, id int64, message string) error {
	var exists int
	err := r.exec(ctx).QueryRowContext(ctx,
		`SELECT 1 FROM identity_scim_group WHERE id = ?`, id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.NotFound("scim group not found")
	}
	if err != nil {
		return err
	}
	return apperr.Conflict(message)
}

// List 分页列出未删除组。
func (r *SQLScimGroupRepository) List(ctx context.Context, filter domain.ScimGroupFilter) ([]*domain.ScimGroup, int, error) {
	where := []string{"provider = ?", "deleted_at IS NULL"}
	args := []any{strings.TrimSpace(filter.Provider)}
	if filter.DisplayName != "" {
		where = append(where, "display_name = ?")
		args = append(args, filter.DisplayName)
	}
	if filter.ExternalID != "" {
		where = append(where, "external_id = ?")
		args = append(args, filter.ExternalID)
	}
	condition := strings.Join(where, " AND ")
	var total int
	if err := r.exec(ctx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM identity_scim_group WHERE `+condition, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT `+scimGroupColumns+` FROM identity_scim_group WHERE `+condition+
			` ORDER BY created_at ASC, id ASC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*domain.ScimGroup, 0, limit)
	for rows.Next() {
		group, scanErr := scanScimGroup(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		out = append(out, group)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// ListByAccount 返回账号所属的未删除组。
func (r *SQLScimGroupRepository) ListByAccount(ctx context.Context, accountID string) ([]*domain.ScimGroup, error) {
	pk, err := parseID(accountID)
	if err != nil {
		return nil, err
	}
	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT g.id, g.provider, g.external_id, g.display_name, g.mapped_role, g.version,
		        g.created_at, g.updated_at, g.deleted_at
		   FROM identity_scim_group g
		   JOIN identity_scim_group_member m ON m.group_id = g.id
		  WHERE m.account_id = ? AND g.deleted_at IS NULL
		  ORDER BY g.id ASC`, pk)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*domain.ScimGroup, 0, 4)
	for rows.Next() {
		group, scanErr := scanScimGroup(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, group)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListMembers 返回组成员，按账号 ID 升序。
func (r *SQLScimGroupRepository) ListMembers(ctx context.Context, groupID string) ([]*domain.ScimGroupMember, error) {
	pk, err := parseID(groupID)
	if err != nil {
		return nil, err
	}
	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT group_id, account_id, created_at FROM identity_scim_group_member
		 WHERE group_id = ? ORDER BY account_id ASC`, pk)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*domain.ScimGroupMember, 0, 8)
	for rows.Next() {
		var (
			groupPK   int64
			accountPK int64
			createdAt time.Time
		)
		if scanErr := rows.Scan(&groupPK, &accountPK, &createdAt); scanErr != nil {
			return nil, scanErr
		}
		out = append(out, &domain.ScimGroupMember{
			GroupID:   formatID(groupPK),
			AccountID: formatID(accountPK),
			CreatedAt: createdAt.UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// AddMembers 批量添加成员；已存在的成员幂等跳过。
func (r *SQLScimGroupRepository) AddMembers(ctx context.Context, members []*domain.ScimGroupMember) error {
	for _, member := range members {
		if member == nil {
			continue
		}
		groupPK, err := parseID(member.GroupID)
		if err != nil {
			return err
		}
		accountPK, err := parseID(member.AccountID)
		if err != nil {
			return err
		}
		// INSERT IGNORE：重复成员不报错，配合唯一键实现幂等。
		if _, err := r.exec(ctx).ExecContext(ctx,
			`INSERT IGNORE INTO identity_scim_group_member (group_id, account_id, created_at)
			 VALUES (?, ?, ?)`, groupPK, accountPK, member.CreatedAt.UTC()); err != nil {
			return err
		}
	}
	return nil
}

// RemoveMembers 批量移除成员；不存在的成员幂等跳过。
func (r *SQLScimGroupRepository) RemoveMembers(ctx context.Context, groupID string, accountIDs []string) error {
	if len(accountIDs) == 0 {
		return nil
	}
	groupPK, err := parseID(groupID)
	if err != nil {
		return err
	}
	placeholders := make([]string, 0, len(accountIDs))
	args := make([]any, 0, len(accountIDs)+1)
	args = append(args, groupPK)
	for _, accountID := range accountIDs {
		accountPK, parseErr := parseID(accountID)
		if parseErr != nil {
			return parseErr
		}
		placeholders = append(placeholders, "?")
		args = append(args, accountPK)
	}
	_, err = r.exec(ctx).ExecContext(ctx,
		`DELETE FROM identity_scim_group_member WHERE group_id = ? AND account_id IN (`+
			strings.Join(placeholders, ", ")+`)`, args...)
	return err
}

// RemoveAllMembers 清空组成员。
func (r *SQLScimGroupRepository) RemoveAllMembers(ctx context.Context, groupID string) error {
	groupPK, err := parseID(groupID)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx).ExecContext(ctx,
		`DELETE FROM identity_scim_group_member WHERE group_id = ?`, groupPK)
	return err
}

// CountMembers 返回组成员数量。
func (r *SQLScimGroupRepository) CountMembers(ctx context.Context, groupIDs []string) (map[string]int, error) {
	out := make(map[string]int, len(groupIDs))
	if len(groupIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, 0, len(groupIDs))
	args := make([]any, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		groupPK, err := parseID(groupID)
		if err != nil {
			return nil, err
		}
		placeholders = append(placeholders, "?")
		args = append(args, groupPK)
	}
	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT group_id, COUNT(*) FROM identity_scim_group_member
		 WHERE group_id IN (`+strings.Join(placeholders, ", ")+`) GROUP BY group_id`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			groupPK int64
			count   int
		)
		if scanErr := rows.Scan(&groupPK, &count); scanErr != nil {
			return nil, scanErr
		}
		out[formatID(groupPK)] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// scanScimToken 扫描一行令牌。
func scanScimToken(row scanRow) (*domain.ScimToken, error) {
	var (
		token     domain.ScimToken
		status    string
		expiresAt sql.NullTime
		revokedAt sql.NullTime
		pk        int64
	)
	if err := row.Scan(&pk, &token.Provider, &token.TokenHash, &token.Fingerprint, &status,
		&expiresAt, &token.CreatedBy, &token.CreatedAt, &revokedAt); err != nil {
		return nil, err
	}
	token.ID = formatID(pk)
	token.Status = domain.ScimTokenStatus(status)
	token.ExpiresAt = nullableTime(expiresAt)
	token.RevokedAt = nullableTime(revokedAt)
	token.CreatedAt = token.CreatedAt.UTC()
	return &token, nil
}

// scanScimGroup 扫描一行组。
func scanScimGroup(row scanRow) (*domain.ScimGroup, error) {
	var (
		group     domain.ScimGroup
		pk        int64
		deletedAt sql.NullTime
	)
	if err := row.Scan(&pk, &group.Provider, &group.ExternalID, &group.DisplayName,
		&group.MappedRole, &group.Version, &group.CreatedAt, &group.UpdatedAt, &deletedAt); err != nil {
		return nil, err
	}
	group.ID = formatID(pk)
	group.CreatedAt = group.CreatedAt.UTC()
	group.UpdatedAt = group.UpdatedAt.UTC()
	group.DeletedAt = nullableTime(deletedAt)
	return &group, nil
}

// formatID 把 BIGINT 主键还原为十进制字符串；与 domain 的字符串 ID 约定一致。
func formatID(value int64) string {
	return strconv.FormatInt(value, 10)
}
