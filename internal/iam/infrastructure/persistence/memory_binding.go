package persistence

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// bindingKey 是绑定的唯一键，对应 uk_iam_binding。
type bindingKey struct {
	channel     domain.Channel
	subjectType domain.SubjectType
	subjectID   string
	merchantID  string
	roleID      string
}

// MemoryBindingRepository 是基于内存 map 的绑定仓储。
//
// 幂等语义与唯一索引一致：同一唯一键重复绑定视为成功且保留既有记录（含其 ID），
// 不会产生第二条绑定；解绑不存在时同样返回成功。
type MemoryBindingRepository struct {
	mu       sync.RWMutex
	bindings map[string]*domain.Binding
	byKey    map[bindingKey]string
}

// NewMemoryBindingRepository 创建空的绑定仓储。
func NewMemoryBindingRepository() *MemoryBindingRepository {
	return &MemoryBindingRepository{
		bindings: make(map[string]*domain.Binding),
		byKey:    make(map[bindingKey]string),
	}
}

// Save 创建绑定；唯一键已存在时幂等成功并保留既有记录。
func (r *MemoryBindingRepository) Save(_ context.Context, binding *domain.Binding) error {
	if binding == nil {
		return apperr.BadRequest("binding is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	key := bindingKey{
		channel:     binding.Channel,
		subjectType: binding.SubjectType,
		subjectID:   binding.SubjectID,
		merchantID:  binding.MerchantID,
		roleID:      binding.RoleID,
	}
	if id, ok := r.byKey[key]; ok {
		// 重复绑定（含过期后重绑）刷新有效期与授权人，保留原主键与首次绑定时间，
		// 保证调用方按返回 ID 能正常解绑。
		existing := r.bindings[id]
		refreshed := cloneBinding(binding)
		if existing != nil {
			refreshed.ID = existing.ID
			refreshed.CreatedAt = existing.CreatedAt
		}
		r.bindings[refreshed.ID] = refreshed
		r.byKey[key] = refreshed.ID
		return nil
	}

	r.bindings[binding.ID] = cloneBinding(binding)
	r.byKey[key] = binding.ID
	return nil
}

// Delete 解绑；不存在也返回 nil（幂等）。
func (r *MemoryBindingRepository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	binding, ok := r.bindings[id]
	if !ok {
		return nil
	}
	delete(r.bindings, id)
	delete(r.byKey, bindingKey{
		channel:     binding.Channel,
		subjectType: binding.SubjectType,
		subjectID:   binding.SubjectID,
		merchantID:  binding.MerchantID,
		roleID:      binding.RoleID,
	})
	return nil
}

// FindByID 按主键加载绑定；不存在返回 NotFound。
func (r *MemoryBindingRepository) FindByID(_ context.Context, id string) (*domain.Binding, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	binding, ok := r.bindings[id]
	if !ok {
		return nil, apperr.NotFound("binding not found")
	}
	return cloneBinding(binding), nil
}

// ListBySubject 返回主体在 query.EffectiveTime() 时刻仍有效的绑定（过滤已过期）。
//
// MerchantID 为空时按「A 端语义」匹配：只返回 merchant_id 为空的绑定，
// 绝不退化为「匹配所有商户」——否则一次调用即可跨租户读取授权。
func (r *MemoryBindingRepository) ListBySubject(_ context.Context, query domain.SubjectQuery) ([]*domain.Binding, error) {
	if err := validateSubjectQuery(query); err != nil {
		return nil, err
	}
	at := query.EffectiveTime()

	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*domain.Binding, 0, len(r.bindings))
	for _, binding := range r.bindings {
		if binding.Channel != query.Channel ||
			binding.SubjectType != query.SubjectType ||
			binding.SubjectID != query.SubjectID ||
			binding.MerchantID != query.MerchantID {
			continue
		}
		if binding.IsExpired(at) {
			continue
		}
		out = append(out, cloneBinding(binding))
	}

	slices.SortFunc(out, func(a, b *domain.Binding) int {
		if byRole := strings.Compare(a.RoleID, b.RoleID); byRole != 0 {
			return byRole
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

// ListByRole 返回绑定到指定角色的全部记录（含已过期），用于删除前引用检查。
func (r *MemoryBindingRepository) ListByRole(_ context.Context, roleID string) ([]*domain.Binding, error) {
	if roleID == "" {
		return nil, apperr.BadRequest("role id is required")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*domain.Binding, 0, len(r.bindings))
	for _, binding := range r.bindings {
		if binding.RoleID != roleID {
			continue
		}
		out = append(out, cloneBinding(binding))
	}
	slices.SortFunc(out, func(a, b *domain.Binding) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// validateSubjectQuery 拒绝缺失端别 / 主体与 B 端空商户的查询。
func validateSubjectQuery(query domain.SubjectQuery) error {
	if err := query.Channel.Validate(); err != nil {
		return err
	}
	if err := query.SubjectType.Validate(); err != nil {
		return err
	}
	if query.SubjectID == "" {
		return apperr.BadRequest("subject id is required")
	}
	if query.Channel == domain.ChannelBiz && query.MerchantID == "" {
		return apperr.BadRequest("merchant id is required for channel biz")
	}
	if query.Channel == domain.ChannelAdmin && query.MerchantID != "" {
		return apperr.BadRequest("merchant id must be empty for channel admin")
	}
	return nil
}

// cloneBinding 返回绑定深拷贝，隔离 ExpiresAt 指针。
func cloneBinding(binding *domain.Binding) *domain.Binding {
	if binding == nil {
		return nil
	}
	cp := *binding
	if binding.ExpiresAt != nil {
		expiresAt := *binding.ExpiresAt
		cp.ExpiresAt = &expiresAt
	}
	return &cp
}
