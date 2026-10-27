package persistence

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
)

// MemoryMenuRepository 是基于内存切片的菜单仓储。
//
// 仅用于单测与 local / demo：菜单由代码种子维护，内存仓储需要在构造时显式注入，
// 生产使用 SQLMenuRepository。
type MemoryMenuRepository struct {
	mu    sync.RWMutex
	menus []*domain.Menu
}

// NewMemoryMenuRepository 以给定菜单创建只读仓储（自动拷贝，避免调用方后续修改）。
func NewMemoryMenuRepository(menus ...*domain.Menu) *MemoryMenuRepository {
	copied := make([]*domain.Menu, 0, len(menus))
	for _, menu := range menus {
		if menu == nil {
			continue
		}
		cp := *menu
		copied = append(copied, &cp)
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
		cp := *menu
		out = append(out, &cp)
	}
	slices.SortFunc(out, func(a, b *domain.Menu) int {
		if a.Sort != b.Sort {
			return a.Sort - b.Sort
		}
		return strings.Compare(a.Code, b.Code)
	})
	return out, nil
}
