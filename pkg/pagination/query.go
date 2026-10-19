package pagination

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/admin-frame/admin-frame/pkg/errors"
)

// Query 是解析后的分页参数。
type Query struct {
	Page     int
	PageSize int
}

// Offset 返回 0-based 偏移（page≤0 按 1 计）。
func (q Query) Offset() int {
	page := q.Page
	if page <= 0 {
		page = 1
	}
	size := q.PageSize
	if size < 0 {
		size = 0
	}
	return (page - 1) * size
}

// Limit 返回每页条数（同 PageSize，便于仓储命名）。
func (q Query) Limit() int {
	if q.PageSize < 0 {
		return 0
	}
	return q.PageSize
}

// Parse 使用默认 Config 解析 page / page_size。
func Parse(r *http.Request) (Query, error) {
	return ParseWith(r, Config{})
}

// ParseWith 按 cfg 解析分页参数。
//
// page / page_size 缺省或非法（≤0、非数字）时回落默认值；
// page_size 超过 MaxPageSize 时返回 InvalidArgument。
func ParseWith(r *http.Request, cfg Config) (Query, error) {
	if r == nil {
		return Query{}, errors.InvalidArgument("request is nil")
	}
	cfg = cfg.Normalize()
	page := atoiDefault(r.URL.Query().Get(cfg.PageParam), 0)
	pageSize := atoiDefault(r.URL.Query().Get(cfg.PageSizeParam), 0)
	if page <= 0 {
		page = cfg.DefaultPage
	}
	if pageSize <= 0 {
		pageSize = cfg.DefaultPageSize
	}
	if pageSize > cfg.MaxPageSize {
		return Query{}, errors.InvalidArgument(
			fmt.Sprintf("page_size exceeds maximum of %d", cfg.MaxPageSize),
		)
	}
	return Query{Page: page, PageSize: pageSize}, nil
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
