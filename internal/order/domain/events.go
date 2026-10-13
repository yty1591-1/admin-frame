package domain

import "time"

// Event 领域事件端口。
type Event interface {
	// EventName 返回稳定事件名（如 order.created）。
	EventName() string
	// OccurredAt 返回事件发生时间（UTC）。
	OccurredAt() time.Time
}

// OrderCreated 订单创建事件。
type OrderCreated struct {
	OrderID    string
	MerchantID string
	UserID     string
	At         time.Time
}

// EventName 返回事件名 order.created。
func (e OrderCreated) EventName() string { return "order.created" }

// OccurredAt 返回事件发生时间。
func (e OrderCreated) OccurredAt() time.Time { return e.At }

// OrderCancelled 订单取消事件。
type OrderCancelled struct {
	OrderID    string
	MerchantID string
	UserID     string
	At         time.Time
}

// EventName 返回事件名 order.cancelled。
func (e OrderCancelled) EventName() string { return "order.cancelled" }

// OccurredAt 返回事件发生时间。
func (e OrderCancelled) OccurredAt() time.Time { return e.At }
