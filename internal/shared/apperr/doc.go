// Package apperr 定义本仓库稳定业务数字错误码与 AppError。
//
// 语义码、调用栈等可移植能力由 pkg/errors 提供；本包在其上挂接对外契约码（如 40400）。
// HTTP 信封写响应见 pkg/httpx；本仓库经 internal/shared/httpx 注入本包数字码。
package apperr
