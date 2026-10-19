// Package middleware 提供与业务无关的 HTTP 中间件原语。
//
// 设计边界（对齐架构 §7.2 / OBS / REL / SEC）：
//   - 不得依赖 internal/；鉴权、租户守卫、业务码映射留在 platform
//   - 建议顺序：Recover → RequestID → AccessLog → Metrics → Auth → Timeout → Handler
//   - RequestID 与 contextx / 信封 trace_id / X-Request-ID 对齐（OBS-05）
//   - 限流只做判定与回调；42900 等业务码由 OnLimited 注入（REL-05）
//   - CORS 默认收紧；生产须显式配置 AllowOrigins
//
// 典型用法：
//
//	h := middleware.Chain(mux,
//		middleware.Recover(middleware.RecoverConfig{Logger: log}),
//		middleware.RequestID(),
//		middleware.AccessLog(log),
//		middleware.Timeout(15*time.Second),
//	)
package middleware
