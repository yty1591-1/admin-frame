package domain

import "context"

// Repository 订单仓储端口（由 infrastructure 实现）。
type Repository interface {
	// Save 创建或更新订单（含乐观版本时由实现处理冲突）。
	Save(ctx context.Context, order *Order) error
	// FindByID 按 ID 加载；不存在返回 apperr.ErrNotFound。
	FindByID(ctx context.Context, id string) (*Order, error)
	// List 按过滤条件分页列举。
	List(ctx context.Context, filter ListFilter) ([]*Order, error)
}

// ListFilter 列表过滤条件。
//
// MerchantID / UserID 为空表示不按该维度过滤；空商户过滤仅 A 端允许。
type ListFilter struct {
	MerchantID string
	UserID     string
	Limit      int
	Offset     int
}
