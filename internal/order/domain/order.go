package domain

import (
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// Status 订单状态机取值。
type Status string

const (
	// StatusCreated 已创建未支付。
	StatusCreated Status = "created"
	// StatusPaid 已支付。
	StatusPaid Status = "paid"
	// StatusCancelled 已取消。
	StatusCancelled Status = "cancelled"
)

// Order 订单聚合根。
//
// 不变量：AmountCent > 0；MerchantID、UserID 非空。
// Remark、CostCent 为内部字段，不得对 C 端暴露。
type Order struct {
	ID         string
	MerchantID string
	UserID     string
	AmountCent int64
	Currency   string
	Status     Status
	Remark     string // 运营内部备注，不得对 C 端暴露
	CostCent   int64  // 成本，不得对 C 端暴露
	Version    int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// NewOrder 创建处于 StatusCreated 的订单；校验商户、用户与金额。
func NewOrder(id, merchantID, userID string, amountCent int64, currency string) (*Order, error) {
	if merchantID == "" {
		return nil, apperr.BadRequest("merchant_id required")
	}
	if userID == "" {
		return nil, apperr.BadRequest("user_id required")
	}
	if amountCent <= 0 {
		return nil, apperr.BadRequest("amount must be positive")
	}
	if currency == "" {
		currency = "CNY"
	}
	now := time.Now().UTC()
	return &Order{
		ID:         id,
		MerchantID: merchantID,
		UserID:     userID,
		AmountCent: amountCent,
		Currency:   currency,
		Status:     StatusCreated,
		Version:    1,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

// Cancel 取消订单；已支付或已取消时返回 Conflict。
func (o *Order) Cancel() error {
	if o.Status == StatusPaid {
		return apperr.Conflict("paid order cannot be cancelled")
	}
	if o.Status == StatusCancelled {
		return apperr.Conflict("order already cancelled")
	}
	o.Status = StatusCancelled
	o.UpdatedAt = time.Now().UTC()
	o.Version++
	return nil
}

// MarkPaid 将订单标为已支付；仅 StatusCreated 可迁移。
func (o *Order) MarkPaid() error {
	if o.Status != StatusCreated {
		return apperr.Conflict("only created order can be paid")
	}
	o.Status = StatusPaid
	o.UpdatedAt = time.Now().UTC()
	o.Version++
	return nil
}
