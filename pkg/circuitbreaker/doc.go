// Package circuitbreaker 提供进程内熔断器：Closed → Open → HalfOpen 状态机。
//
// 设计边界（对齐 REL-04 熔断/舱壁）：
//   - 不得依赖 internal/；失败判定、是否值得熔断由调用方注入
//   - 只做「快速失败」，不做重试与限流；重试见 pkg/retry，限流见 pkg/ratelimit
//   - 状态在进程内，不跨实例共享；全局一致性由网关/服务网格承担
//   - 执行函数 panic 会计为失败并继续向上 panic，禁止吞掉
//   - OnStateChange 在熔断器锁外调用，可能并发触发；回调内不要反向调用本 Breaker
//
// 典型用法：
//
//	cb := circuitbreaker.MustNew(circuitbreaker.Config{Timeout: 5 * time.Second})
//	err := cb.Do(func() error { return callDownstream(ctx) })
package circuitbreaker
