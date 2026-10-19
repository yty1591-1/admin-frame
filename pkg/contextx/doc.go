// Package contextx 提供请求上下文中的关联 ID、用户、租户等只读取值原语。
//
// 设计边界（对齐架构 §7.1 / OBS / SEC）：
//   - 不得依赖 internal/；完整业务 Principal 仍放 internal/shared/auth
//   - context key 使用未导出类型（架构 S-04）
//   - RequestID 与 logger.WithTraceID / 信封 trace_id / X-Request-ID 同源
//   - 禁止把 context 存进结构体长期持有；禁止用 context 传可选业务参数
//   - 不包含鉴权策略；仅存取已由 middleware 注入的字段
//
// 典型用法：
//
//	ctx = contextx.WithRequestID(ctx, id)
//	ctx = contextx.WithIdentity(ctx, contextx.Identity{UserID: "u1", MerchantID: "m1", Channel: "biz"})
//	mid := contextx.MerchantID(ctx)
package contextx
