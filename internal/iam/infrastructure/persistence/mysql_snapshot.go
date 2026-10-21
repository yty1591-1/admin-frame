package persistence

import (
	"context"
	"database/sql"
	"slices"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// snapshotPermissionSQL 是权限快照查询（docs/rbac-iam.md §8.3）。
//
// 单条 SQL 展开 绑定 → 角色 → 角色权限 → 权限点，禁止按角色循环查询（N+1）。
// 过滤条件同时校验角色状态、软删、权限点状态与端别，任何一项不满足都不进入快照。
const snapshotPermissionSQL = `
SELECT DISTINCT r.id, r.code, r.version, p.code
FROM iam_role_binding b
JOIN iam_role r
		ON r.id = b.role_id
	AND r.channel = b.channel
	AND r.status = 'active'
	AND r.deleted_at IS NULL
JOIN iam_role_permission rp
	ON rp.role_id = r.id
JOIN iam_permission p
	ON p.code = rp.permission_code
	AND p.status = 'active'
	AND p.channel IN (?, 'both')
WHERE b.channel = ?
	AND b.subject_type = ?
	AND b.subject_id = ?
	AND b.merchant_id = ?
	AND (b.expires_at IS NULL OR b.expires_at > ?)`

// snapshotScopeSQL 是数据范围快照查询（docs/rbac-iam.md §8.3）。
const snapshotScopeSQL = `
SELECT DISTINCT s.resource, s.scope_type, s.scope_values
FROM iam_role_data_scope s
JOIN iam_role_binding b
	ON b.role_id = s.role_id
JOIN iam_role r
		ON r.id = b.role_id
	AND r.channel = b.channel
	AND r.status = 'active'
	AND r.deleted_at IS NULL
WHERE b.channel = ?
	AND b.subject_type = ?
	AND b.subject_id = ?
	AND b.merchant_id = ?
	AND (b.expires_at IS NULL OR b.expires_at > ?)`

// SQLSnapshotRepository 是 MySQL 8.0 的鉴权快照只读实现。
//
// 返回空快照（而非 NotFound）表示主体没有任何有效授权：调用方据此默认拒绝，
// 不能把「没有绑定」当成依赖故障，否则一个无权限用户会产生 503。
type SQLSnapshotRepository struct {
	sql *sql.DB
}

// NewSQLSnapshotRepository 基于已打开的 *sql.DB 创建快照仓储。
func NewSQLSnapshotRepository(sqlDB *sql.DB) *SQLSnapshotRepository {
	return &SQLSnapshotRepository{sql: sqlDB}
}

// Load 加载主体有效权限快照；两次固定查询（权限 + 数据范围），非 N+1。
func (r *SQLSnapshotRepository) Load(ctx context.Context, query domain.SubjectQuery) (*domain.SubjectSnapshot, error) {
	if err := validateSubjectQuery(query); err != nil {
		return nil, err
	}
	if r.sql == nil {
		return nil, apperr.Unavailable("iam snapshot repository is not configured")
	}
	at := query.EffectiveTime()

	roles, permissions, version, err := r.loadPermissions(ctx, query, at)
	if err != nil {
		return nil, err
	}
	scopes, err := r.loadScopes(ctx, query, at)
	if err != nil {
		return nil, err
	}

	return &domain.SubjectSnapshot{
		Channel:     query.Channel,
		SubjectType: query.SubjectType,
		SubjectID:   query.SubjectID,
		MerchantID:  query.MerchantID,
		Roles:       roles,
		Permissions: permissions,
		Scopes:      scopes,
		Version:     version,
		GeneratedAt: time.Now().UTC(),
	}, nil
}

// loadPermissions 执行权限快照查询，返回去重排序的角色码、权限码与最大角色版本。
//
// 版本取所有生效角色的最大 version：任一角色被修改都会使其 version 递增，
// 因此该值可作为缓存比对信号，无需额外查询。
func (r *SQLSnapshotRepository) loadPermissions(
	ctx context.Context,
	query domain.SubjectQuery,
	at time.Time,
) (roles []string, permissions []iam.PermissionCode, version int64, err error) {
	rows, err := r.sql.QueryContext(ctx, snapshotPermissionSQL,
		string(query.Channel), string(query.Channel), string(query.SubjectType),
		query.SubjectID, query.MerchantID, at)
	if err != nil {
		return nil, nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	roleSet := make(map[string]struct{})
	permissionSet := make(map[iam.PermissionCode]struct{})
	for rows.Next() {
		var (
			roleID     string
			roleCode   string
			rowVersion int64
			permission string
		)
		if err := rows.Scan(&roleID, &roleCode, &rowVersion, &permission); err != nil {
			return nil, nil, 0, err
		}
		roleSet[roleCode] = struct{}{}
		permissionSet[iam.PermissionCode(permission)] = struct{}{}
		if rowVersion > version {
			version = rowVersion
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, 0, err
	}

	roles = make([]string, 0, len(roleSet))
	for code := range roleSet {
		roles = append(roles, code)
	}
	slices.Sort(roles)

	permissions = make([]iam.PermissionCode, 0, len(permissionSet))
	for code := range permissionSet {
		permissions = append(permissions, code)
	}
	slices.Sort(permissions)
	return roles, permissions, version, nil
}

// loadScopes 执行数据范围查询，并按资源取并集（最宽者胜出）。
func (r *SQLSnapshotRepository) loadScopes(ctx context.Context, query domain.SubjectQuery, at time.Time) ([]domain.DataScope, error) {
	rows, err := r.sql.QueryContext(ctx, snapshotScopeSQL,
		string(query.Channel), string(query.SubjectType), query.SubjectID, query.MerchantID, at)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	raw := make([]domain.DataScope, 0)
	for rows.Next() {
		var (
			resource string
			scopeTyp string
			value    []byte
		)
		if err := rows.Scan(&resource, &scopeTyp, &value); err != nil {
			return nil, err
		}
		merchantIDs, err := unmarshalScopeValues(value)
		if err != nil {
			return nil, err
		}
		raw = append(raw, domain.DataScope{
			Resource:    resource,
			Type:        domain.DataScopeType(scopeTyp),
			MerchantIDs: merchantIDs,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return []domain.DataScope{}, nil
	}
	return domain.MergeDataScopes(raw)
}
