package persistence

import (
	"context"
	"sync"

	"github.com/admin-frame/admin-frame/internal/order/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// MemoryRepository 本地/演示用内存仓储，SQL 实现可并行接入。
type MemoryRepository struct {
	mu     sync.RWMutex
	orders map[string]*domain.Order
}

// NewMemoryRepository 创建空的内存订单仓储。
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{orders: make(map[string]*domain.Order)}
}

// Save 覆盖写入订单快照。
func (r *MemoryRepository) Save(_ context.Context, order *domain.Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *order
	r.orders[order.ID] = &cp
	return nil
}

// FindByID 按 ID 查找订单副本；不存在返回 NotFound。
func (r *MemoryRepository) FindByID(_ context.Context, id string) (*domain.Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	o, ok := r.orders[id]
	if !ok {
		return nil, apperr.NotFound("order not found")
	}
	cp := *o
	return &cp, nil
}

// List 按商户/用户过滤并分页返回订单副本。
func (r *MemoryRepository) List(_ context.Context, filter domain.ListFilter) ([]*domain.Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*domain.Order, 0)
	for _, o := range r.orders {
		if filter.MerchantID != "" && o.MerchantID != filter.MerchantID {
			continue
		}
		if filter.UserID != "" && o.UserID != filter.UserID {
			continue
		}
		cp := *o
		out = append(out, &cp)
	}
	if filter.Offset > len(out) {
		return []*domain.Order{}, nil
	}
	out = out[filter.Offset:]
	if filter.Limit > 0 && filter.Limit < len(out) {
		out = out[:filter.Limit]
	}
	return out, nil
}

// Ping 供就绪探针使用；内存仓储恒成功。
func (r *MemoryRepository) Ping() error { return nil }
