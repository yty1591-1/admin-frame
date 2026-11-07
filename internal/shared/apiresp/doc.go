// Package apiresp 是 apperr → 统一响应信封的适配层：挂接业务数字码与 API 契约约定。
//
// 可移植信封 / 状态映射实现见 pkg/httpx；transport 与 platform middleware
// 统一通过本包写响应，避免与 pkg/httpx 同名包混用。
// 分页/排序解析请直接使用 pkg/pagination（API_STANDARDS API-05）。
package apiresp
