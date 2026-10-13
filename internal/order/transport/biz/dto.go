package biz

import "time"

// OrderDTO 是 B 端订单响应（不含成本等内部字段）。
type OrderDTO struct {
	ID         string    `json:"id"`
	MerchantID string    `json:"merchant_id"`
	UserID     string    `json:"user_id"`
	AmountCent int64     `json:"amount_cent"`
	Currency   string    `json:"currency"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ListResponse 是 B 端订单列表响应。
type ListResponse struct {
	Items []*OrderDTO `json:"items"`
	Total int         `json:"total"`
}
