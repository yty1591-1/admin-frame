package pagination

const (
	// DefaultPage 默认页码（从 1 开始）。
	DefaultPage = 1
	// DefaultPageSize 默认每页条数。
	DefaultPageSize = 20
	// MaxPageSize 列表 page_size 默认上限（API-05）。
	MaxPageSize = 100

	defaultPageParam     = "page"
	defaultPageSizeParam = "page_size"
	defaultSortParam     = "sort"
)

// Config 控制分页查询参数的默认值、上限与参数名。
type Config struct {
	DefaultPage     int
	DefaultPageSize int
	MaxPageSize     int
	// PageParam 页码查询键；空则 "page"。
	PageParam string
	// PageSizeParam 每页条数查询键；空则 "page_size"。
	PageSizeParam string
}

// Normalize 补全默认值。
func (c Config) Normalize() Config {
	if c.DefaultPage <= 0 {
		c.DefaultPage = DefaultPage
	}
	if c.DefaultPageSize <= 0 {
		c.DefaultPageSize = DefaultPageSize
	}
	if c.MaxPageSize <= 0 {
		c.MaxPageSize = MaxPageSize
	}
	if c.PageParam == "" {
		c.PageParam = defaultPageParam
	}
	if c.PageSizeParam == "" {
		c.PageSizeParam = defaultPageSizeParam
	}
	return c
}
