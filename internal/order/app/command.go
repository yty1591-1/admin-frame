package app

// CreateOrderCommand 创建订单命令。
type CreateOrderCommand struct {
	MerchantID string
	UserID     string
	AmountCent int64
	Currency   string
	IdemKey    string // 非空时启用进程内幂等（演示用）
}

// CancelOrderCommand 取消订单命令。
//
// B 端须填 MerchantID；C 端须填 UserID；A 端可均空（管理取消）。
type CancelOrderCommand struct {
	OrderID    string
	MerchantID string
	UserID     string
}
