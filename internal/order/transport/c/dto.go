package c

import "time"

// OrderDTO C 端字段最小化：不含 remark / cost_cent。
type OrderDTO struct {
	ID         string    `json:"id"`
	MerchantID string    `json:"merchant_id"`
	AmountCent int64     `json:"amount_cent"`
	Currency   string    `json:"currency"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

// CreateOrderRequest 是 C 端下单请求体。
type CreateOrderRequest struct {
	MerchantID string `json:"merchant_id"`
	AmountCent int64  `json:"amount_cent"`
	Currency   string `json:"currency"`
}

// ListResponse 是 C 端订单列表响应。
type ListResponse struct {
	Items []*OrderDTO `json:"items"`
	Total int         `json:"total"`
}
