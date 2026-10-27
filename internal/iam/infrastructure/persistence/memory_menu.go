package persistence

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// MemoryMenuRepository 是基于内存切片的菜单仓储。
//
// 仅用于单测与 local / demo：菜单由代码种子维护，内存仓储需要在构造时显式注入，
// 生产使用 SQLMenuRepository。写入语义与 SQL 实现保持一致（唯一键 + 乐观锁）。
type MemoryMenuRepository struct {
	mu    sync.RWMutex
	menus []*domain.Menu
}

// NewMemoryMenuRepository 以给定菜单创建仓储（自动拷贝，避免调用方后续修改）。
func NewMemoryMenuRepository(menus ...*domain.Menu) *MemoryMenuRepository {
	copied := make([]*domain.Menu, 0, len(menus))
	for _, menu := range menus {
		if menu == nil {
			continue
		}
		copied = append(copied, cloneMenu(menu))
	}
	return &MemoryMenuRepository{menus: copied}
}

// List 按端别与状态过滤，并按 sort、code 升序返回。
func (r *MemoryMenuRepository) List(_ context.Context, channel domain.Channel, status domain.MenuStatus) ([]*domain.Menu, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*domain.Menu, 0, len(r.menus))
	for _, menu := range r.menus {
		if menu.Channel != channel {
			continue
		}
		if status != "" && menu.Status != status {
			continue
		}
		out = append(out, cloneMenu(menu))
	}
	slices.SortFunc(out, func(a, b *domain.Menu) int {
		if a.Sort != b.Sort {
			return a.Sort - b.Sort
		}
		return strings.Compare(a.Code, b.Code)
	})
	return out, nil
}

// Get 按主键加载菜单；不存在时返回 40400。
func (r *MemoryMenuRepository) Get(_ context.Context, id string) (*domain.Menu, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, menu := range r.menus {
		if menu.ID == id {
			return cloneMenu(menu), nil
		}
	}
	return nil, apperr.NotFound("menu not found")
}

// Create 写入新菜单；编码或路由名冲突返回 40900。
func (r *MemoryMenuRepository) Create(_ context.Context, menu *domain.Menu) error {
	if menu == nil {
		return apperr.BadRequest("menu is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.menus {
		if existing.Channel != menu.Channel {
			continue
		}
		if existing.Code == menu.Code || existing.RouteName == menu.RouteName {
			return apperr.Conflict("menu code or route name already exists in this channel")
		}
	}
	r.menus = append(r.menus, cloneMenu(menu))
	return nil
}

// Update 以乐观锁保存菜单；version 不匹配返回 40900。
func (r *MemoryMenuRepository) Update(_ context.Context, menu *domain.Menu, expectedVersion int64) error {
	if menu == nil {
		return apperr.BadRequest("menu is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, existing := range r.menus {
		if existing.ID != menu.ID {
			continue
		}
		if existing.Version != expectedVersion {
			return apperr.Conflict("menu version conflict")
		}
		for j, other := range r.menus {
			if j == i || other.Channel != menu.Channel {
				continue
			}
			if other.Code == menu.Code || other.RouteName == menu.RouteName {
				return apperr.Conflict("menu code or route name already exists in this channel")
			}
		}
		r.menus[i] = cloneMenu(menu)
		return nil
	}
	return apperr.NotFound("menu not found")
}

// cloneMenu 深拷贝菜单，避免仓储内部状态被调用方修改。
func cloneMenu(menu *domain.Menu) *domain.Menu {
	cp := *menu
	return &cp
}
