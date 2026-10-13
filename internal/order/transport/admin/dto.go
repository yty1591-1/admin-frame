package admin

import "time"

// OrderDTO 是 A 端订单响应（含内部字段 Remark/CostCent）。
type OrderDTO struct {
	ID         string    `json:"id"`
	MerchantID string    `json:"merchant_id"`
	UserID     string    `json:"user_id"`
	AmountCent int64     `json:"amount_cent"`
	Currency   string    `json:"currency"`
	Status     string    `json:"status"`
	Remark     string    `json:"remark"`
	CostCent   int64     `json:"cost_cent"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ListResponse 是 A 端订单列表响应。
type ListResponse struct {
	Items []*OrderDTO `json:"items"`
	Total int         `json:"total"`
}
