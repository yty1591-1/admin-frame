package configcenterruntime

import (
	"time"

	"github.com/admin-frame/admin-frame/internal/platform"
	platformasync "github.com/admin-frame/admin-frame/internal/platform/async"
	"github.com/admin-frame/admin-frame/pkg/outbox"
)

// ApplyRuntimeParams 把热更视图接到平台运行期参数（ADR-0015）。
//
// 接线范围（全部为白名单键）：
//   - 入口限流：`rate_limit.rps` / `burst` / `idle_ttl` / `degrade_on_failure`
//   - 幂等 TTL：`idempotency.ttl`
//   - 异步 relay：`async.outbox.*`；消费租约：`async.consumer.visibility_timeout`、
//     `async.consumer.retry_backoff`
//
// 必须在 platform 的 Register 回调内调用：buildRouter 在 Register 之后才读取
// Deps 上的这些字段。重复调用是幂等的。视图缺键时逐项回落到配置文件静态值。
func (r *Runtime) ApplyRuntimeParams(deps *platform.Deps) {
	if r == nil || r.Hot == nil || deps == nil || deps.Config == nil {
		return
	}
	deps.RateLimitParams = r.RateLimitParams(deps.Config.RateLimit)
	deps.IdempotencyTTL = r.HotDuration("idempotency.ttl", deps.Config.Idempotency.TTL)

	if deps.Async == nil {
		return
	}
	// 一次取出读取函数，避免每次循环都重建闭包。
	pollInterval := r.HotDuration("async.outbox.poll_interval", 0)
	batchSize := r.HotInt("async.outbox.batch_size", 0)
	lease := r.HotDuration("async.outbox.lease", 0)
	maxAttempts := r.HotInt("async.outbox.max_attempts", 0)
	backoff := r.HotString("async.consumer.retry_backoff", "")
	visibility := r.HotDuration("async.consumer.visibility_timeout", 0)

	deps.Async.SetLiveParams(platformasync.LiveParams{
		Outbox: func() outbox.Params {
			return outbox.Params{
				// 0 值即「沿用装配期配置」，由 outbox.Relay 判断。
				PollInterval: pollInterval(),
				BatchSize:    int(batchSize()),
				Lease:        lease(),
				MaxAttempts:  int(maxAttempts()),
				Backoff:      outbox.Backoff(backoff()),
			}
		},
		Visibility: func() time.Duration { return visibility() },
	})
}
