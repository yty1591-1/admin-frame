// Package ratelimit 提供进程内限流原语：令牌桶、滑动窗口计数，以及按 key 的注册表。
//
// 设计边界（对齐 REL-05）：
//   - 不得依赖 internal/；业务键（channel/user/merchant）由调用方拼装
//   - 本包只做「允不允许」；映射 42900 / HTTP 429 / Retry-After 由 platform 或 transport 负责
//   - 默认内存实现，适合单进程；跨实例限流需后续 Redis 等后端（接口可替换）
//   - HTTP 中间件见 pkg/middleware.RateLimit；42900 映射仍由 platform/transport 负责
//
// 典型用法：
//
//	lim := ratelimit.NewTokenBucket(100, 20) // 100 token/s，突发 20
//	if r := lim.Allow(); !r.Allowed {
//		return apperr.TooManyRequests("too many requests")
//	}
package ratelimit
