package persistence

import (
	"context"
	"strings"

	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// AppendApproval 追加一条审批事件（内存实现）。
//
// 与 MySQL 的差异：内存没有外键，这里显式校验租户是否存在，保证
// 「不能给不存在的租户写审批」在所有实现上语义一致。
func (r *MemoryRepository) AppendApproval(_ context.Context, approval domain.Approval) error {
	if err := approval.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[strings.TrimSpace(approval.TenantID)]; !ok {
		return domain.ErrNotFound
	}
	r.approvalSeq++
	approval.ID = r.approvalSeq
	r.approvals[approval.TenantID] = append(r.approvals[approval.TenantID], approval)
	return nil
}

// ListRecentApprovals 按时间倒序返回最近 limit 条审批事件。
func (r *MemoryRepository) ListRecentApprovals(_ context.Context, tenantID string, limit int) ([]domain.Approval, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, domain.ErrApprovalTenantRequired
	}
	if limit <= 0 {
		limit = domain.MaxApprovalHistory()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	events := r.approvals[tenantID]
	capHint := limit
	if len(events) < capHint {
		capHint = len(events)
	}
	out := make([]domain.Approval, 0, capHint)
	for i := len(events) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, events[i])
	}
	return out, nil
}

// 编译期断言：内存仓储实现审批契约。
var _ domain.ApprovalRepository = (*MemoryRepository)(nil)
