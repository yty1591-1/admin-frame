package httpx

import (
	"net/http"

	"github.com/admin-frame/admin-frame/pkg/httpx"
)

const (
	// DefaultPage 默认页码（从 1 开始）。
	DefaultPage = httpx.DefaultPage
	// DefaultPageSize 默认每页条数。
	DefaultPageSize = httpx.DefaultPageSize
	// MaxPageSize 列表 page_size 上限（API-05）。
	MaxPageSize = httpx.MaxPageSize
)

// PageData 列表 data 载荷公共形状（别名）。
type PageData = httpx.PageData

// ParsePage 解析查询参数 page / page_size。
// page 缺省或非法时为 DefaultPage；page_size 缺省或非法时为 DefaultPageSize。
// page_size 超过 MaxPageSize 时返回 CodeInvalidArgument（可由 FromError 映射为 40000）。
func ParsePage(r *http.Request) (page, pageSize int, err error) {
	return httpx.ParsePageWith(r, httpx.PageConfig{
		DefaultPage:     DefaultPage,
		DefaultPageSize: DefaultPageSize,
		MaxPageSize:     MaxPageSize,
	})
}
