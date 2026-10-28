package persistence

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// sodGroupKey 是互斥组编码唯一索引的内存表示，对应 uk_iam_sod_group_scope_code。
type sodGroupKey struct {
	channel    domain.Channel
	merchantID string
	code       string
}

// MemorySodGroupRepository 是基于内存 map 的互斥角色组仓储。
//
// 仅用于单测与 local / demo；生产使用 SQLSodGroupRepository。
// 所有返回值均为深拷贝，避免调用方持锁外修改内部状态。
type MemorySodGroupRepository struct {
	mu     sync.RWMutex
	groups map[string]*domain.SodGroup
	byKey  map[sodGroupKey]string
}

// NewMemorySodGroupRepository 创建空的互斥组仓储。
func NewMemorySodGroupRepository() *MemorySodGroupRepository {
	return &MemorySodGroupRepository{
		groups: make(map[string]*domain.SodGroup),
		byKey:  make(map[sodGroupKey]string),
	}
}

// FindByID 按主键加载互斥组；不存在返回 NotFound。
func (r *MemorySodGroupRepository) FindByID(_ context.Context, id string) (*domain.SodGroup, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	group, ok := r.groups[id]
	if !ok {
		return nil, apperr.NotFound("sod group not found")
	}
	return cloneSodGroup(group), nil
}

// FindByCode 按端别 + 商户 + 编码加载互斥组；软删组仍可查到，用于阻止 code 复用。
func (r *MemorySodGroupRepository) FindByCode(
	_ context.Context, channel domain.Channel, merchantID, code string,
) (*domain.SodGroup, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.byKey[sodGroupKey{channel: channel, merchantID: merchantID, code: code}]
	if !ok {
		return nil, apperr.NotFound("sod group not found")
	}
	return cloneSodGroup(r.groups[id]), nil
}

// Save 创建或更新互斥组；expectedVersion 为 0 表示创建。
func (r *MemorySodGroupRepository) Save(
	_ context.Context, group *domain.SodGroup, expectedVersion int64,
) error {
	if group == nil {
		return apperr.BadRequest("sod group is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, exists := r.groups[group.ID]
	if expectedVersion == 0 {
		if exists {
			return apperr.Conflict("sod group id already exists")
		}
		key := sodGroupKey{channel: group.Channel, merchantID: group.MerchantID, code: group.Code}
		if _, dup := r.byKey[key]; dup {
			return apperr.Conflict("sod group code already exists in this channel and merchant")
		}
		r.groups[group.ID] = cloneSodGroup(group)
		r.byKey[key] = group.ID
		return nil
	}

	if !exists {
		return apperr.NotFound("sod group not found")
	}
	if existing.Version != expectedVersion {
		return apperr.Conflict("sod group version conflict")
	}
	if existing.Code != group.Code || existing.Channel != group.Channel || existing.MerchantID != group.MerchantID {
		return apperr.BadRequest("sod group code, channel and merchant are immutable")
	}
	r.groups[group.ID] = cloneSodGroup(group)
	return nil
}

// List 按条件分页返回未软删互斥组，按 code、ID 排序。
func (r *MemorySodGroupRepository) List(
	_ context.Context, filter domain.SodGroupFilter,
) ([]*domain.SodGroup, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*domain.SodGroup, 0, len(r.groups))
	for _, group := range r.groups {
		if group.IsDeleted() {
			continue
		}
		if filter.Channel != "" && group.Channel != filter.Channel {
			continue
		}
		if filter.MerchantID != "" && group.MerchantID != filter.MerchantID {
			continue
		}
		if filter.Status != "" && group.Status != filter.Status {
			continue
		}
		if filter.Keyword != "" && !matchesSodKeyword(group, filter.Keyword) {
			continue
		}
		out = append(out, cloneSodGroup(group))
	}
	slices.SortFunc(out, func(a, b *domain.SodGroup) int {
		if byCode := strings.Compare(a.Code, b.Code); byCode != 0 {
			return byCode
		}
		return strings.Compare(a.ID, b.ID)
	})
	return paginate(out, filter.Offset, filter.Limit), nil
}

// Count 返回满足条件的未删除互斥组数；分页参数不参与计数。
func (r *MemorySodGroupRepository) Count(_ context.Context, filter domain.SodGroupFilter) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	total := 0
	for _, group := range r.groups {
		if group.IsDeleted() {
			continue
		}
		if filter.Channel != "" && group.Channel != filter.Channel {
			continue
		}
		if filter.MerchantID != "" && group.MerchantID != filter.MerchantID {
			continue
		}
		if filter.Status != "" && group.Status != filter.Status {
			continue
		}
		if filter.Keyword != "" && !matchesSodKeyword(group, filter.Keyword) {
			continue
		}
		total++
	}
	return total, nil
}

// ListByRole 返回包含指定角色的未软删互斥组。
func (r *MemorySodGroupRepository) ListByRole(
	_ context.Context, channel domain.Channel, merchantID, roleID string,
) ([]*domain.SodGroup, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*domain.SodGroup, 0)
	for _, group := range r.groups {
		if group.IsDeleted() || group.Channel != channel || group.MerchantID != merchantID {
			continue
		}
		if !slices.Contains(group.RoleIDs, roleID) {
			continue
		}
		out = append(out, cloneSodGroup(group))
	}
	slices.SortFunc(out, func(a, b *domain.SodGroup) int {
		if byCode := strings.Compare(a.Code, b.Code); byCode != 0 {
			return byCode
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

// matchesSodKeyword 对 code / name 做大小写不敏感包含匹配。
func matchesSodKeyword(group *domain.SodGroup, keyword string) bool {
	needle := strings.ToLower(keyword)
	return strings.Contains(strings.ToLower(group.Code), needle) ||
		strings.Contains(strings.ToLower(group.Name), needle)
}

// cloneSodGroup 返回互斥组深拷贝，隔离 RoleIDs 与 DeletedAt。
func cloneSodGroup(group *domain.SodGroup) *domain.SodGroup {
	if group == nil {
		return nil
	}
	cp := *group
	cp.RoleIDs = slices.Clone(group.RoleIDs)
	if group.DeletedAt != nil {
		deletedAt := *group.DeletedAt
		cp.DeletedAt = &deletedAt
	}
	return &cp
}
