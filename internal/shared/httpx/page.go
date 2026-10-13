package httpx

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

const (
	// DefaultPage 默认页码（从 1 开始）。
	DefaultPage = 1
	// DefaultPageSize 默认每页条数。
	DefaultPageSize = 20
	// MaxPageSize 列表 page_size 上限（API-05）。
	MaxPageSize = 100
)

// ParsePage 解析查询参数 page / page_size。
// page 缺省或非法时为 DefaultPage；page_size 缺省或非法时为 DefaultPageSize。
// page_size 超过 MaxPageSize 时返回业务错误 40000。
func ParsePage(r *http.Request) (page, pageSize int, err error) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ = strconv.Atoi(r.URL.Query().Get("page_size"))
	if page <= 0 {
		page = DefaultPage
	}
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		return 0, 0, apperr.BadRequest(fmt.Sprintf("page_size exceeds maximum of %d", MaxPageSize))
	}
	return page, pageSize, nil
}
