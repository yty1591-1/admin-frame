package persistence

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// maxSodViolationLimit 是单次预检返回的违规主体上限，避免管理端一次拉取过多数据。
const maxSodViolationLimit = 100

// SQLSodViolationRepository 是 MySQL 8.0 的互斥组存量违规预检实现。
//
// 查询先对租户内全部有效绑定递归展开 RBAC1 祖先链，再与候选组的角色集合求交，
// 按主体聚合后筛选超出阈值的记录；只用于低频配置期检查。
type SQLSodViolationRepository struct {
	sql *sql.DB
}

// NewSQLSodViolationRepository 基于已打开的 *sql.DB 创建违规预检仓储。
func NewSQLSodViolationRepository(sqlDB *sql.DB) *SQLSodViolationRepository {
	return &SQLSodViolationRepository{sql: sqlDB}
}

// List 返回候选互斥组在 at 时刻的存量违规主体。
func (r *SQLSodViolationRepository) List(
	ctx context.Context, group *domain.SodGroup, at time.Time, limit int,
) ([]domain.SodViolation, error) {
	if group == nil {
		return nil, apperr.BadRequest("sod group is required")
	}
	if r.sql == nil {
		return nil, apperr.Unavailable("iam sod violation repository is not configured")
	}
	if len(group.RoleIDs) == 0 {
		return []domain.SodViolation{}, nil
	}
	limit = normalizeSodViolationLimit(limit)

	placeholders := make([]string, 0, len(group.RoleIDs))
	args := make([]any, 0, len(group.RoleIDs)+6)
	args = append(args, string(group.Channel), group.MerchantID, at.UTC(), domain.MaxRoleHierarchyDepth)
	for _, roleID := range group.RoleIDs {
		parsed, err := parseID(roleID)
		if err != nil {
			return nil, err
		}
		placeholders = append(placeholders, "?")
		args = append(args, parsed)
	}
	args = append(args, group.MaxActiveRoles, limit)

	//nolint:gosec // G202: IN 列表占位符由固定 "?" 逐个生成，实参经 args 绑定，无外部输入拼接。
	rows, err := r.sql.QueryContext(ctx, `
WITH RECURSIVE subject_chain AS (
	SELECT b.channel, b.subject_type, b.subject_id, b.merchant_id, b.role_id, 0 AS depth
	FROM iam_role_binding b
	JOIN iam_role r
			ON r.id = b.role_id
		AND r.status = 'active'
		AND r.deleted_at IS NULL
		AND r.role_type <> 'breakglass'
	WHERE b.channel = ? AND b.merchant_id = ?
		AND b.revoked_at IS NULL
		AND (b.expires_at IS NULL OR b.expires_at > ?)
	UNION ALL
	SELECT sc.channel, sc.subject_type, sc.subject_id, sc.merchant_id, r.parent_id, sc.depth + 1
	FROM subject_chain sc
	JOIN iam_role r
			ON r.id = sc.role_id
		AND r.status = 'active'
		AND r.deleted_at IS NULL
		AND r.role_type <> 'breakglass'
	WHERE r.parent_id IS NOT NULL AND sc.depth < ?
),
member_hits AS (
	SELECT channel, subject_type, subject_id, merchant_id, role_id
	FROM subject_chain
	WHERE role_id IN (`+strings.Join(placeholders, ",")+`)
)
SELECT channel, subject_type, subject_id, merchant_id,
       GROUP_CONCAT(DISTINCT role_id ORDER BY role_id SEPARATOR ',') AS role_ids
FROM member_hits
GROUP BY channel, subject_type, subject_id, merchant_id
HAVING COUNT(DISTINCT role_id) > ?
ORDER BY subject_id
LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]domain.SodViolation, 0)
	for rows.Next() {
		var (
			channel     string
			subjectType string
			subjectID   string
			merchantID  string
			roleIDs     sql.NullString
		)
		if err := rows.Scan(&channel, &subjectType, &subjectID, &merchantID, &roleIDs); err != nil {
			return nil, err
		}
		out = append(out, domain.SodViolation{
			Channel:     domain.Channel(channel),
			SubjectType: domain.SubjectType(subjectType),
			SubjectID:   subjectID,
			MerchantID:  merchantID,
			RoleIDs:     splitSodRoleIDs(roleIDs),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// splitSodRoleIDs 解析 GROUP_CONCAT 结果；空值返回空切片而非 nil。
func splitSodRoleIDs(value sql.NullString) []string {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return []string{}
	}
	parts := strings.Split(value.String, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// normalizeSodViolationLimit 把预检上限限制在 [1, maxSodViolationLimit]。
func normalizeSodViolationLimit(limit int) int {
	if limit <= 0 || limit > maxSodViolationLimit {
		return maxSodViolationLimit
	}
	return limit
}
