// Package pagination 提供 HTTP 分页/排序查询参数解析与默认值（API-05）。
//
// 设计边界：
//   - 不得依赖 internal/；列表 data 形状用 Result，由 transport 经 httpx.OK 写出
//   - page 默认 1、page_size 默认 20、最大 100（可配置）；超限 → InvalidArgument / 40000
//   - 排序字段必须白名单，禁止将客户端字符串直接拼进 SQL ORDER BY
//   - 导出/大批量不得靠放大 page_size 绕过上限（走专用任务）
//
// 典型用法：
//
//	q, err := pagination.Parse(r)
//	sorts, err := pagination.ParseSort(r, pagination.SortConfig{Allowed: map[string]string{
//		"created_at": "created_at", "amount": "amount_cent",
//	}})
package pagination
