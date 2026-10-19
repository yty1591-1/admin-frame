// Package retry 提供可取消的重试原语：次数/时间预算上限、指数退避与抖动。
//
// 设计边界（对齐 REL-03）：
//   - 不得依赖 internal/；默认用 pkg/errors.RetryableOf 判定是否可重试
//   - 必须尊重 context 取消与 deadline；禁止热循环空转
//   - 非幂等写路径应由调用方收紧 RetryIf，或用 Permanent 短路
//   - 本包不实现熔断/限流（见 pkg/ratelimit 与业务策略）
//
// 典型用法：
//
//	err := retry.Do(ctx, retry.DefaultPolicy(), func(ctx context.Context) error {
//		return callDownstream(ctx)
//	})
package retry
