package persistence

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// maxListLimit 是列表查询的分页上限，与 API-05（page_size ≤ 100）一致。
const maxListLimit = 100

// roleKey 是角色编码唯一索引的内存表示，对应 uk_iam_role_scope_code。
type roleKey struct {
	channel    domain.Channel
	merchantID string
	code       string
}

// MemoryRoleRepository 是基于内存 map 的角色仓储。
//
// 并发安全：内部使用 RWMutex；所有返回值均为深拷贝，避免调用方持锁外修改内部状态。
// code / channel / merchant 一旦创建即不可变，Save 会拒绝变更，模拟唯一索引的约束。
type MemoryRoleRepository struct {
	mu    sync.RWMutex
	roles map[string]*domain.Role
	byKey map[roleKey]string
}

// NewMemoryRoleRepository 创建空的角色仓储。
func NewMemoryRoleRepository() *MemoryRoleRepository {
	return &MemoryRoleRepository{
		roles: make(map[string]*domain.Role),
		byKey: make(map[roleKey]string),
	}
}

// FindByID 按主键加载角色；不存在返回 NotFound。
func (r *MemoryRoleRepository) FindByID(_ context.Context, id string) (*domain.Role, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	role, ok := r.roles[id]
	if !ok {
		return nil, apperr.NotFound("role not found")
	}
	return cloneRole(role), nil
}

// FindByCode 按端别 + 商户 + 编码加载角色；软删角色仍可查到，用于阻止 code 复用。
func (r *MemoryRoleRepository) FindByCode(_ context.Context, channel domain.Channel, merchantID, code string) (*domain.Role, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.byKey[roleKey{channel: channel, merchantID: merchantID, code: code}]
	if !ok {
		return nil, apperr.NotFound("role not found")
	}
	return cloneRole(r.roles[id]), nil
}

// Save 创建或更新角色。
//
// expectedVersion 为 0 表示创建：主键或 (channel, merchant, code) 冲突返回 Conflict。
// expectedVersion 非 0 表示更新：版本必须与仓储一致，否则返回 Conflict（乐观锁）。
func (r *MemoryRoleRepository) Save(_ context.Context, role *domain.Role, expectedVersion int64) error {
	if role == nil {
		return apperr.BadRequest("role is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, exists := r.roles[role.ID]
	if expectedVersion == 0 {
		if exists {
			return apperr.Conflict("role id already exists")
		}
		key := roleKey{channel: role.Channel, merchantID: role.MerchantID, code: role.Code}
		if _, dup := r.byKey[key]; dup {
			return apperr.Conflict("role code already exists in this channel and merchant")
		}
		r.roles[role.ID] = cloneRole(role)
		r.byKey[key] = role.ID
		return nil
	}

	if !exists {
		return apperr.NotFound("role not found")
	}
	if existing.Version != expectedVersion {
		return apperr.Conflict("role version conflict")
	}
	if existing.Code != role.Code || existing.Channel != role.Channel || existing.MerchantID != role.MerchantID {
		return apperr.BadRequest("role code, channel and merchant are immutable")
	}
	r.roles[role.ID] = cloneRole(role)
	return nil
}

// List 按条件分页返回未软删的角色，按 code 升序（code 相同再按 ID）。
//
// 软删角色不出现在列表中，但仍可通过 FindByCode 查到——这是「code 不可复用」的实现基础。
func (r *MemoryRoleRepository) List(_ context.Context, filter domain.RoleFilter) ([]*domain.Role, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*domain.Role, 0, len(r.roles))
	for _, role := range r.roles {
		if role.IsDeleted() {
			continue
		}
		if filter.Channel != "" && role.Channel != filter.Channel {
			continue
		}
		if filter.MerchantID != "" && role.MerchantID != filter.MerchantID {
			continue
		}
		if filter.Status != "" && role.Status != filter.Status {
			continue
		}
		if filter.Keyword != "" && !matchesKeyword(role, filter.Keyword) {
			continue
		}
		out = append(out, cloneRole(role))
	}

	slices.SortFunc(out, func(a, b *domain.Role) int {
		if byCode := strings.Compare(a.Code, b.Code); byCode != 0 {
			return byCode
		}
		return strings.Compare(a.ID, b.ID)
	})

	return paginate(out, filter.Offset, filter.Limit), nil
}

// Count 返回满足条件的未删除角色数；分页参数不参与计数。
func (r *MemoryRoleRepository) Count(_ context.Context, filter domain.RoleFilter) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	total := 0
	for _, role := range r.roles {
		if role.IsDeleted() {
			continue
		}
		if filter.Channel != "" && role.Channel != filter.Channel {
			continue
		}
		if filter.MerchantID != "" && role.MerchantID != filter.MerchantID {
			continue
		}
		if filter.Status != "" && role.Status != filter.Status {
			continue
		}
		if filter.Keyword != "" && !matchesKeyword(role, filter.Keyword) {
			continue
		}
		total++
	}
	return total, nil
}

// matchesKeyword 对 code / name 做大小写不敏感包含匹配。
func matchesKeyword(role *domain.Role, keyword string) bool {
	needle := strings.ToLower(keyword)
	return strings.Contains(strings.ToLower(role.Code), needle) ||
		strings.Contains(strings.ToLower(role.Name), needle)
}

// paginate 按 Offset / Limit 截取，Limit 上限为 maxListLimit；越界返回空切片（非 nil）。
func paginate[T any](items []T, offset, limit int) []T {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(items) {
		return []T{}
	}
	out := items[offset:]
	if limit <= 0 || limit > maxListLimit {
		limit = maxListLimit
	}
	if limit < len(out) {
		out = out[:limit]
	}
	return out
}

// cloneRole 返回角色深拷贝，隔离 Permissions / DataScopes / DeletedAt。
func cloneRole(role *domain.Role) *domain.Role {
	if role == nil {
		return nil
	}
	cp := *role
	cp.Permissions = role.Permissions.Clone()
	cp.DataScopes = make(map[string]domain.DataScope, len(role.DataScopes))
	for resource, scope := range role.DataScopes {
		scope.MerchantIDs = slices.Clone(scope.MerchantIDs)
		cp.DataScopes[resource] = scope
	}
	if role.DeletedAt != nil {
		deletedAt := *role.DeletedAt
		cp.DeletedAt = &deletedAt
	}
	return &cp
}
