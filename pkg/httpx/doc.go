// Package httpx 提供与业务域无关的 HTTP JSON 响应原语：
// 统一信封、分页解析、列表载荷形状，以及错误到 HTTP 状态 / 信封码的映射。
//
// 设计边界：
//   - 不得依赖 internal/；稳定业务数字码由调用方（如 internal/shared/apperr）注入
//   - 语义码与 HTTP 状态映射委托 pkg/errors；本包负责写响应与信封形状
//   - 按端字段裁剪、鉴权与 OpenAPI 契约仍由 transport / 契约层负责
//
// 典型用法：
//
//	httpx.OK(w, data, traceID)
//	page, size, err := httpx.ParsePage(r)
//	httpx.FromError(w, err, traceID) // 可选 WithBizCode 覆盖数字码映射
package httpx
