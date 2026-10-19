// Package idempotency 提供幂等键（Idempotency-Key）存储接口、应用层 Guard 与 HTTP 中间件。
//
// 设计边界（对齐 API-11 / REL-04 / 架构 §9.2）：
//   - 不得依赖 internal/；40900 等业务数字码由 platform/transport 经 OnError 注入
//   - 同一键 + 相同请求指纹：返回首次已完成响应；指纹不一致 → AlreadyExists（冲突）
//   - 键占用中（并发重放）→ Aborted，不得并行执行业务
//   - 5xx / panic 后释放占用，允许客户端安全重试；2xx/4xx 落库缓存
//   - 本包提供 MemoryStore；跨实例 Redis/DB 由调用方实现 Store
//   - 业务唯一键（如 out_trade_no）仍可在 app/domain 层并行使用
//
// 典型用法（HTTP）：
//
//	store := idempotency.NewMemoryStore()
//	h := idempotency.Middleware(idempotency.Config{
//		Store: store,
//		Scope: func(r *http.Request) string { return contextx.UserID(r.Context()) },
//		OnError: func(w http.ResponseWriter, r *http.Request, err error) {
//			httpx.FromError(w, err, contextx.RequestID(r.Context()))
//		},
//	})(mux)
//
// 典型用法（app Guard）：
//
//	res, err := guard.Run(ctx, key, fp, func(ctx context.Context) (idempotency.Result, error) {
//		return idempotency.Result{StatusCode: 200, Body: payload}, nil
//	})
package idempotency
