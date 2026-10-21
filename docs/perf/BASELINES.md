# 性能关键路径基线登记（PERF-01）

| 路径 ID | 包 | Benchmark | 说明 | 备注 |
|---------|----|-----------|------|------|
| `order.domain.NewOrder` | `./internal/order/domain` | `BenchmarkNewOrder` | 订单创建与不变量校验 | 微基准；默认 CI 仅验证可运行 |
| `webhook.Verifier.Verify` | `./pkg/webhook` | `BenchmarkVerifierVerify` | 入站验签：头解析 + HMAC + 时间窗（1 KiB body） | 微基准；不含 NonceStore I/O |
| `iam.authorize.CacheHit` | `./internal/iam/app` | `BenchmarkAuthorize_CacheHit` | L1 命中：解码 + 清洗 + 快照复用 | 实测 ~5.8µs/op（目标 ≤ 1ms） |
| `iam.authorize.CacheMiss` | `./internal/iam/app` | `BenchmarkAuthorize_CacheMiss_DB` | 回源 DB + 回填 L1/L2（fake repo） | 实测 ~5.7µs/op（不含真实 SQL） |
| `iam.snapshot.100Permissions` | `./internal/iam/app` | `BenchmarkSnapshotLoad_100Permissions` | 100 权限快照命中加载 | 实测 ~19µs/op（目标 ≤ 1ms） |

新增行后须提交对应 `Benchmark*`，并保证 `make check-perf` 通过。
