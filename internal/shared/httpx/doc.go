// Package httpx 是本仓库对 pkg/httpx 的薄封装：挂接 apperr 业务数字码与 API 契约约定。
//
// 可移植信封 / 状态映射实现见 pkg/httpx；本包保持既有导入路径稳定，
// 供 transport 与 platform middleware 使用。
// 分页/排序解析请直接使用 pkg/pagination（API_STANDARDS API-05）。
package httpx
