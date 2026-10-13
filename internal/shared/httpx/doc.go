// Package httpx 提供统一 JSON 响应信封、分页解析与错误到 HTTP 的映射。
//
// 业务数字码来自 apperr；按端字段裁剪由各 transport 负责。
// 分页上限见 MaxPageSize（API_STANDARDS API-05）。
package httpx
