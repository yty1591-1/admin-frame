package httpx

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/admin-frame/admin-frame/pkg/errors"
)

const (
	// DefaultPage 默认页码（从 1 开始）。
	DefaultPage = 1
	// DefaultPageSize 默认每页条数。
	DefaultPageSize = 20
	// MaxPageSize 列表 page_size 默认上限（可被 PageConfig 覆盖）。
	MaxPageSize = 100
)

// PageConfig 控制分页查询参数的默认值与上限。
//
// 零值字段在 Normalize 时回落到包级默认常量。
type PageConfig struct {
	// DefaultPage 缺省或非法 page 时的回落值；≤0 时使用 DefaultPage。
	DefaultPage int
	// DefaultPageSize 缺省或非法 page_size 时的回落值；≤0 时使用 DefaultPageSize。
	DefaultPageSize int
	// MaxPageSize page_size 允许的最大值；≤0 时使用 MaxPageSize。
	MaxPageSize int
}

// Normalize 返回补全默认值后的配置副本。
func (c PageConfig) Normalize() PageConfig {
	if c.DefaultPage <= 0 {
		c.DefaultPage = DefaultPage
	}
	if c.DefaultPageSize <= 0 {
		c.DefaultPageSize = DefaultPageSize
	}
	if c.MaxPageSize <= 0 {
		c.MaxPageSize = MaxPageSize
	}
	return c
}

// PageData 是列表接口 Envelope.Data 的公共形状（items + total）。
type PageData struct {
	Items any `json:"items"`
	Total int `json:"total"`
}

// NewPageData 构造列表载荷；items 为 nil 时写入空切片 JSON（[]）而非 null。
func NewPageData(items any, total int) PageData {
	if items == nil {
		items = []struct{}{}
	}
	return PageData{Items: items, Total: total}
}

// ParsePage 使用默认 PageConfig 解析查询参数 page / page_size。
//
// page 缺省或非法时为 DefaultPage；page_size 缺省或非法时为 DefaultPageSize。
// page_size 超过 MaxPageSize 时返回 CodeInvalidArgument 错误。
func ParsePage(r *http.Request) (page, pageSize int, err error) {
	return ParsePageWith(r, PageConfig{})
}

// ParsePageWith 按 cfg 解析分页参数；cfg 零值字段使用包默认。
func ParsePageWith(r *http.Request, cfg PageConfig) (page, pageSize int, err error) {
	if r == nil {
		return 0, 0, errors.InvalidArgument("request is nil")
	}
	cfg = cfg.Normalize()
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ = strconv.Atoi(r.URL.Query().Get("page_size"))
	if page <= 0 {
		page = cfg.DefaultPage
	}
	if pageSize <= 0 {
		pageSize = cfg.DefaultPageSize
	}
	if pageSize > cfg.MaxPageSize {
		return 0, 0, errors.InvalidArgument(
			fmt.Sprintf("page_size exceeds maximum of %d", cfg.MaxPageSize),
		)
	}
	return page, pageSize, nil
}

// Offset 将 1-based page 转为数据库常用的 0-based offset。
// page≤0 时按 1 计算，避免负 offset。
func Offset(page, pageSize int) int {
	if page <= 0 {
		page = 1
	}
	if pageSize < 0 {
		pageSize = 0
	}
	return (page - 1) * pageSize
}
