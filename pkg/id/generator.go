package id

// Generator 是分布式 ID 生成器接口；实现须并发安全。
type Generator interface {
	// Next 生成下一个 int64 ID；在时钟严重回拨或内部错误时返回 error。
	Next() (int64, error)
	// NextString 生成十进制字符串形式的 ID（无业务前缀）。
	NextString() (string, error)
}
