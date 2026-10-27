package persistence

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// templateKey 是模板编码唯一索引的内存表示，对应 uk_iam_role_template_code。
type templateKey struct {
	channel domain.Channel
	code    string
}

// MemoryRoleTemplateRepository 是基于内存 map 的角色模板仓储。
//
// 仅用于单测与 local / demo；生产使用 SQLRoleTemplateRepository。
type MemoryRoleTemplateRepository struct {
	mu        sync.RWMutex
	templates map[string]*domain.RoleTemplate
	byKey     map[templateKey]string
}

// NewMemoryRoleTemplateRepository 创建空的模板仓储。
func NewMemoryRoleTemplateRepository() *MemoryRoleTemplateRepository {
	return &MemoryRoleTemplateRepository{
		templates: make(map[string]*domain.RoleTemplate),
		byKey:     make(map[templateKey]string),
	}
}

// FindByID 按主键加载模板；不存在返回 NotFound。
func (r *MemoryRoleTemplateRepository) FindByID(_ context.Context, id string) (*domain.RoleTemplate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	template, ok := r.templates[id]
	if !ok {
		return nil, apperr.NotFound("role template not found")
	}
	return cloneRoleTemplate(template), nil
}

// FindByCode 按端别 + 编码加载模板；软删模板仍可查到，用于阻止 code 复用。
func (r *MemoryRoleTemplateRepository) FindByCode(_ context.Context, channel domain.Channel, code string) (*domain.RoleTemplate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.byKey[templateKey{channel: channel, code: code}]
	if !ok {
		return nil, apperr.NotFound("role template not found")
	}
	return cloneRoleTemplate(r.templates[id]), nil
}

// Save 创建或更新模板；expectedVersion 为 0 表示创建。
func (r *MemoryRoleTemplateRepository) Save(_ context.Context, template *domain.RoleTemplate, expectedVersion int64) error {
	if template == nil {
		return apperr.BadRequest("role template is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, exists := r.templates[template.ID]
	if expectedVersion == 0 {
		if exists {
			return apperr.Conflict("role template id already exists")
		}
		key := templateKey{channel: template.Channel, code: template.Code}
		if _, dup := r.byKey[key]; dup {
			return apperr.Conflict("role template code already exists in this channel")
		}
		r.templates[template.ID] = cloneRoleTemplate(template)
		r.byKey[key] = template.ID
		return nil
	}

	if !exists {
		return apperr.NotFound("role template not found")
	}
	if existing.Version != expectedVersion {
		return apperr.Conflict("role template version conflict")
	}
	if existing.Code != template.Code || existing.Channel != template.Channel {
		return apperr.BadRequest("role template code and channel are immutable")
	}
	r.templates[template.ID] = cloneRoleTemplate(template)
	return nil
}

// List 按条件分页返回未软删模板，按 code、ID 排序。
func (r *MemoryRoleTemplateRepository) List(_ context.Context, filter domain.RoleTemplateFilter) ([]*domain.RoleTemplate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*domain.RoleTemplate, 0, len(r.templates))
	for _, template := range r.templates {
		if template.IsDeleted() {
			continue
		}
		if filter.Channel != "" && template.Channel != filter.Channel {
			continue
		}
		if filter.Status != "" && template.Status != filter.Status {
			continue
		}
		if filter.Keyword != "" && !matchesTemplateKeyword(template, filter.Keyword) {
			continue
		}
		out = append(out, cloneRoleTemplate(template))
	}
	slices.SortFunc(out, func(a, b *domain.RoleTemplate) int {
		if byCode := strings.Compare(a.Code, b.Code); byCode != 0 {
			return byCode
		}
		return strings.Compare(a.ID, b.ID)
	})
	return paginate(out, filter.Offset, filter.Limit), nil
}

// Count 返回满足条件的未删除模板数；分页参数不参与计数。
func (r *MemoryRoleTemplateRepository) Count(_ context.Context, filter domain.RoleTemplateFilter) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	total := 0
	for _, template := range r.templates {
		if template.IsDeleted() {
			continue
		}
		if filter.Channel != "" && template.Channel != filter.Channel {
			continue
		}
		if filter.Status != "" && template.Status != filter.Status {
			continue
		}
		if filter.Keyword != "" && !matchesTemplateKeyword(template, filter.Keyword) {
			continue
		}
		total++
	}
	return total, nil
}

// matchesTemplateKeyword 对 code / name 做大小写不敏感包含匹配。
func matchesTemplateKeyword(template *domain.RoleTemplate, keyword string) bool {
	needle := strings.ToLower(keyword)
	return strings.Contains(strings.ToLower(template.Code), needle) ||
		strings.Contains(strings.ToLower(template.Name), needle)
}

// cloneRoleTemplate 返回模板深拷贝，隔离 Permissions / DataScopes / DeletedAt。
func cloneRoleTemplate(template *domain.RoleTemplate) *domain.RoleTemplate {
	if template == nil {
		return nil
	}
	cp := *template
	cp.Permissions = template.Permissions.Clone()
	cp.DataScopes = make(map[string]domain.DataScope, len(template.DataScopes))
	for resource, scope := range template.DataScopes {
		scope.MerchantIDs = slices.Clone(scope.MerchantIDs)
		cp.DataScopes[resource] = scope
	}
	if template.DeletedAt != nil {
		deletedAt := *template.DeletedAt
		cp.DeletedAt = &deletedAt
	}
	return &cp
}
