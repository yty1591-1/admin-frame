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
	mu    sync.RWMutex
	items map[string]domain.Tenant
}

// NewMemoryRepository 构造内存仓储。
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{items: make(map[string]domain.Tenant)}
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
