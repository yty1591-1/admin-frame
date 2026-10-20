# 性能关键路径基线登记（PERF-01）

| 路径 ID | 包 | Benchmark | 说明 | 备注 |
|---------|----|-----------|------|------|
| `order.domain.NewOrder` | `./internal/order/domain` | `BenchmarkNewOrder` | 订单创建与不变量校验 | 微基准；默认 CI 仅验证可运行 |
| `webhook.Verifier.Verify` | `./pkg/webhook` | `BenchmarkVerifierVerify` | 入站验签：头解析 + HMAC + 时间窗（1 KiB body） | 微基准；不含 NonceStore I/O |

新增行后须提交对应 `Benchmark*`，并保证 `make check-perf` 通过。
