package pagination

// Result 是列表 data 的公共形状（items + total）。
type Result struct {
	Items any `json:"items"`
	Total int `json:"total"`
}

// NewResult 构造列表载荷；items 为 nil 时写入空切片语义（由调用方 JSON 编码为 []）。
func NewResult(items any, total int) Result {
	if items == nil {
		items = []struct{}{}
	}
	return Result{Items: items, Total: total}
}
