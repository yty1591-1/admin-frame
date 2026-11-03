// Package persistence 提供租户仓储实现（内存 / MySQL）。
//
// 两套实现共享同一组领域语义（ErrNotFound / ErrVersionConflict），
// 使应用层与传输层不需要按驱动分支。
package persistence

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// MemoryRepository 是租户仓储的内存实现（local / dev / test）。
//
// 语义与 MySQL 实现保持一致：创建检测主键冲突、更新走版本 CAS。
type MemoryRepository struct {
	mu      sync.RWMutex
	items   map[string]domain.Tenant
	members map[string]map[string]domain.Member
	quotas  map[string]map[string]domain.Quota
}

// NewMemoryRepository 构造内存仓储。
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		items:   make(map[string]domain.Tenant),
		members: make(map[string]map[string]domain.Member),
		quotas:  make(map[string]map[string]domain.Quota),
	}
}

// Create 新建租户；ID 重复返回 ErrAlreadyExists。
func (r *MemoryRepository) Create(_ context.Context, tenant domain.Tenant) error {
	if err := tenant.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[tenant.ID]; ok {
		return fmt.Errorf("%w: %s", domain.ErrAlreadyExists, tenant.ID)
	}
	r.items[tenant.ID] = tenant
	return nil
}

// Get 按 ID 读取租户副本。
func (r *MemoryRepository) Get(_ context.Context, id string) (domain.Tenant, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tenant, ok := r.items[strings.TrimSpace(id)]
	if !ok {
		return domain.Tenant{}, domain.ErrNotFound
	}
	return tenant, nil
}

// List 按状态 / 名称过滤并分页。
func (r *MemoryRepository) List(_ context.Context, filter domain.Filter) ([]domain.Tenant, int, error) {
	filter = filter.Normalize()
	r.mu.RLock()
	defer r.mu.RUnlock()

	matched := make([]domain.Tenant, 0, len(r.items))
	for _, tenant := range r.items {
		if filter.Status != "" && tenant.Status != filter.Status {
			continue
		}
		if filter.Keyword != "" && !strings.Contains(tenant.Name, filter.Keyword) {
			continue
		}
		matched = append(matched, tenant)
	}
	// 与 SQL 的 ORDER BY created_at DESC, id DESC 对齐，保证分页稳定。
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].ID > matched[j].ID
		}
		return matched[i].CreatedAt.After(matched[j].CreatedAt)
	})

	total := len(matched)
	if filter.Offset >= total {
		return nil, total, nil
	}
	end := filter.Offset + filter.Limit
	if end > total {
		end = total
	}
	return matched[filter.Offset:end], total, nil
}

// UpdateStatus 以版本 CAS 写入迁移结果。
func (r *MemoryRepository) UpdateStatus(_ context.Context, tenant domain.Tenant) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.items[tenant.ID]
	if !ok {
		return domain.ErrNotFound
	}
	expected := tenant.Version - 1
	if current.Version != expected {
		return fmt.Errorf("%w: tenant %s version %d, expected %d",
			domain.ErrVersionConflict, tenant.ID, current.Version, expected)
	}
	if err := tenant.Validate(); err != nil {
		return err
	}
	r.items[tenant.ID] = tenant
	return nil
}

// UpsertMember 按 (tenant_id, subject_id) 幂等写入成员。
//
// 已存在时保留首次加入时间：created_at 表达「何时进这个租户」，
// 改角色或停用后再启用都不应重置它。
func (r *MemoryRepository) UpsertMember(_ context.Context, member domain.Member) error {
	if err := member.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	bySubject, ok := r.members[member.TenantID]
	if !ok {
		bySubject = make(map[string]domain.Member)
		r.members[member.TenantID] = bySubject
	}
	if existing, exists := bySubject[member.SubjectID]; exists {
		member.CreatedAt = existing.CreatedAt
	}
	bySubject[member.SubjectID] = member
	return nil
}

// GetMember 读取单个成员。
func (r *MemoryRepository) GetMember(_ context.Context, tenantID, subjectID string) (domain.Member, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	member, ok := r.members[strings.TrimSpace(tenantID)][strings.TrimSpace(subjectID)]
	if !ok {
		return domain.Member{}, domain.ErrMemberNotFound
	}
	return member, nil
}

// ListMembers 列出租户成员；默认只返回在册成员，按角色与主体排序保证分页稳定。
func (r *MemoryRepository) ListMembers(_ context.Context, tenantID string, includeDisabled bool) ([]domain.Member, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	bySubject := r.members[strings.TrimSpace(tenantID)]
	out := make([]domain.Member, 0, len(bySubject))
	for _, member := range bySubject {
		if !includeDisabled && !member.Active() {
			continue
		}
		out = append(out, member)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Role != out[j].Role {
			return roleOrder(out[i].Role) < roleOrder(out[j].Role)
		}
		return out[i].SubjectID < out[j].SubjectID
	})
	return out, nil
}

// CountActiveByRole 统计某角色的在册成员数。
func (r *MemoryRepository) CountActiveByRole(_ context.Context, tenantID string, role domain.MemberRole) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, member := range r.members[strings.TrimSpace(tenantID)] {
		if member.Role == role && member.Active() {
			count++
		}
	}
	return count, nil
}

// UpsertQuota 按 (tenant_id, resource) 幂等写入配额。
func (r *MemoryRepository) UpsertQuota(_ context.Context, quota domain.Quota) error {
	if err := quota.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	byResource, ok := r.quotas[quota.TenantID]
	if !ok {
		byResource = make(map[string]domain.Quota)
		r.quotas[quota.TenantID] = byResource
	}
	byResource[quota.Resource] = quota
	return nil
}

// ListQuotas 列出租户配额，按资源键排序。
func (r *MemoryRepository) ListQuotas(_ context.Context, tenantID string) ([]domain.Quota, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	byResource := r.quotas[strings.TrimSpace(tenantID)]
	out := make([]domain.Quota, 0, len(byResource))
	for _, quota := range byResource {
		out = append(out, quota)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Resource < out[j].Resource })
	return out, nil
}

// roleOrder 定义角色排序权重：owner 在前，便于列表直接读「谁负责」。
func roleOrder(role domain.MemberRole) int {
	switch role {
	case domain.MemberRoleOwner:
		return 0
	case domain.MemberRoleAdmin:
		return 1
	default:
		return 2
	}
}
