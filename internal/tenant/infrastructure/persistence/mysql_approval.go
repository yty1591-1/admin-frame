package persistence

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// AppendApproval 追加一条审批事件（append-only，无更新路径）。
//
// 外键 tenant_id → tenant(id)：给不存在的租户写审批会被数据库拒绝，
// 这里翻译为 ErrNotFound，与内存实现一致。
func (r *MySQLRepository) AppendApproval(ctx context.Context, approval domain.Approval) error {
	if err := approval.Validate(); err != nil {
		return err
	}
	_, err := r.exec(ctx).ExecContext(ctx,
		`INSERT INTO tenant_approval (tenant_id, action, actor_id, note, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		strings.TrimSpace(approval.TenantID), string(approval.Action),
		approval.ActorID, approval.Note, approval.CreatedAt)
	if err != nil {
		// 1452 = ER_NO_REFERENCED_ROW_2：外键指向的租户不存在。
		if isForeignKeyViolation(err) {
			return domain.ErrNotFound
		}
		return fmt.Errorf("tenant: append approval for %s: %w", approval.TenantID, err)
	}
	return nil
}

// ListRecentApprovals 按时间倒序返回最近 limit 条审批事件。
//
// 用 id 倒序而不是 created_at：同一毫秒内的「提交 + 通过」在测试里很常见，
// 时间戳可能相同，自增 id 才能给出确定的先后顺序。
func (r *MySQLRepository) ListRecentApprovals(ctx context.Context, tenantID string, limit int) ([]domain.Approval, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, domain.ErrApprovalTenantRequired
	}
	if limit <= 0 {
		limit = domain.MaxApprovalHistory()
	}
	rows, err := r.query(ctx).QueryContext(ctx,
		`SELECT id, tenant_id, action, actor_id, note, created_at
		   FROM tenant_approval
		  WHERE tenant_id = ?
		  ORDER BY id DESC
		  LIMIT ?`, strings.TrimSpace(tenantID), limit)
	if err != nil {
		return nil, fmt.Errorf("tenant: list approvals for %s: %w", tenantID, err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]domain.Approval, 0, 4)
	for rows.Next() {
		var event domain.Approval
		var action string
		if scanErr := rows.Scan(&event.ID, &event.TenantID, &action, &event.ActorID,
			&event.Note, &event.CreatedAt); scanErr != nil {
			return nil, fmt.Errorf("tenant: scan approval for %s: %w", tenantID, scanErr)
		}
		event.Action = domain.ApprovalAction(action)
		out = append(out, event)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("tenant: iterate approvals for %s: %w", tenantID, rowsErr)
	}
	return out, nil
}

// isForeignKeyViolation 判断是否为外键约束失败（MySQL 1452）。
//
// 不 import go-sql-driver 的错误类型：仓储层已经用文本匹配识别唯一键
// （见 isDuplicateKey），保持一致，避免多一处驱动耦合。
func isForeignKeyViolation(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, errForeignKeyViolation) || strings.Contains(err.Error(), "Error 1452")
}

// errForeignKeyViolation 供测试注入（驱动错误无法在单测里构造真实类型）。
var errForeignKeyViolation = errors.New("tenant: foreign key violation")
