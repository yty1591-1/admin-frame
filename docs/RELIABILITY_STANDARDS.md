# 工程化可靠性与弹性规范（硬约束）

> **状态**：生效中（Normative）  
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**  
> **适用范围**：本仓库全部对外 API、worker、以及对外部系统（HTTP/RPC/DB/MQ/缓存）的调用  
> **关联**：架构 §十一、§九；API 错误码见 `API_STANDARDS.md`；可观测见 `OBSERVABILITY_STANDARDS.md`  
> **执行方式**：`check-reliability.sh` + 单测 + Review + MR 模板  
> **最后更新**：2026-10-13  
> **变更方式**：修改本规范须同步更新 `scripts/ci/check-reliability.sh`、`apperr` 限流码与架构 §十一，经架构 Owner Review

---

## 1. 目标

1. **失败可预期**：外部调用必超时；禁止无限等待拖垮进程。  
2. **写路径安全**：可重试场景必须幂等；限流返回稳定业务码。  
3. **降级可感知**：降级不得静默；须有日志/指标并可说明业务影响。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| REL-01 | 所有外部 I/O（HTTP/RPC/SDK）必须使用带 deadline 的 `context`；禁止无超时的 `http.DefaultClient` / `http.Get` / `http.Post` 用于业务路径 | `check-reliability.sh` + Review |
| REL-02 | 进程优雅关闭超时可配置（`shutdown_timeout`）；`ListenAndServe` 必须尊重该超时 | 代码 + CFG |
| REL-03 | 重试仅针对幂等或明确可安全重试的错误；指数退避 + 抖动 + **有上限**；禁止热循环空转 | Review |
| REL-04 | C 端创建类写操作与支付回调等必须幂等（`Idempotency-Key` 或业务唯一键）；消费者至少一次投递下须幂等 | API-11 + 单测 + Review |
| REL-05 | 限流超限返回稳定业务码 **`42900`**（HTTP 429）；按端配置：C 最严、B 按商户、A 适中 | `apperr` + OpenAPI + Review |
| REL-06 | 非关键依赖失败可降级，但必须打点/打日志（含 `degraded=true` 或等价字段），并在 MR/runbook 说明影响 | Review |
| REL-07 | 列表接口强制分页与 `page_size` 上限（与 API-05 一致），作为背压手段之一 | 已有实现 + OpenAPI |
| REL-08 | 合并请求须勾选可靠性自检 | PR 模板 |

---

## 3. 超时分层

| 层级 | 要求 |
|------|------|
| 入口 HTTP | 全局或按路由超时；C 端读接口宜更短 |
| 下游 HTTP/RPC | **严格小于**入口剩余时间，预留缓冲 |
| DB | statement / context deadline；连接池按端可配 |
| Worker / 异步 | 独立超时与取消，不绑请求生命周期（除非明确需要） |

默认建议（可配置，禁止魔法数散落业务逻辑）：入口 5–30s；下游 ≤ 入口的 50%–80%。

---

## 4. 重试、熔断与限流

### 4.1 重试

- 允许：网络抖动、明确的 `Unavailable` / 限流后的受控重试（须尊重 `Retry-After` 若有）。  
- 禁止：对非幂等写、未知 5xx 盲目重试导致重复下单/扣款。  
- 实现：上限次数、总预算时间、指数退避 + 抖动（统一用 `pkg/retry`；默认仅重试 `errors.RetryableOf`；不可重试用 `retry.Permanent`）。

### 4.2 熔断 / 舱壁

- 下游持续失败时应快速失败，避免线程/连接池被打满。  
- 关键路径使用独立连接池或配额（舱壁）。

### 4.3 限流与错误码

| code | HTTP | 含义 |
|------|------|------|
| 42900 | 429 | 请求过于频繁 / 配额耗尽 |

- 限流原语：`pkg/ratelimit`（令牌桶 / 滑动窗口 / 按 key 注册表）；业务键与按端松紧在 platform。  
- 实现入口：拒绝时映射 `apperr.TooManyRequests`（`errors.Is(..., ratelimit.ErrLimited)`）；契约 `ErrorCode` 含 `42900`。  
- 限流键建议：`channel` + 主体（C: user/IP；B: merchant_id；A: subject 或 IP）。  
- 响应 `message` 笼统（如 `too many requests`），不泄露内部配额细节给 C 端；可用 `Result.RetryAfter` 填 `Retry-After`。

---

## 5. 幂等

| 场景 | 要求 |
|------|------|
| C 创建类写 | 请求头 `Idempotency-Key`（API-11） |
| 支付回调 | 验签 + 业务唯一键 + 状态机 |
| MQ 消费者 | 去重表 / 唯一约束；允许重复投递结果一致 |
| 内部重试 | 与上游幂等键或自然键对齐 |

同一幂等键冲突且载荷不一致时，返回 `40900`，不得静默覆盖。

---

## 6. 降级

- 适用：个性化推荐、次要缓存、非关键附属服务。  
- 不适用：鉴权、计费、库存扣减、支付确认等正确性关键路径。  
- **必须**可观测：日志字段或指标标明降级；禁止仅 `return nil` 无痕迹。

---

## 7. MUST NOT

- 使用无超时的默认 HTTP 客户端访问外部系统。  
- 对非幂等写做无限或高次数重试。  
- 限流返回 200 或与 `42900` 无关的随意码。  
- 静默吞掉降级错误。  
- 用「加大超时」掩盖下游故障而不告警。  

---

## 8. 本地与 CI

```bash
make check-reliability   # REL-01/05 可自动项
make test                # 含幂等单测
make openapi             # 42900 在契约枚举中
make ci
```

| 平台 | 入口 |
|------|------|
| GitHub / Gitee | job `arch` 内跑 `check-reliability.sh` |
| MR | PR 模板「可靠性自检」 |

---

## 9. 例外

1. **第三方 SDK 自带超时**：须在封装层断言/配置超时，并在 Review 注明默认值。  
2. **本地演示无下游**：可用 memory；一旦接入真实外部调用须满足 REL-01。  
3. **紧急关闭限流**：仅 Owner 批准 + 到期日；须有替代防护（扩容/WAF）。

---

## 10. Review 清单

- [ ] 新增外部调用均带 timeout context；无 DefaultClient  
- [ ] 写路径幂等策略明确（键、存储、冲突行为）  
- [ ] 限流使用 `42900`；按端策略合理  
- [ ] 降级有日志/指标与业务影响说明  
- [ ] `make check-reliability` 通过  

---

## 11. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| `internal/shared/apperr` | `42900` / `TooManyRequests` |
| `pkg/retry` | REL-03：可取消重试、退避抖动、次数/时间上限 |
| `pkg/ratelimit` | REL-05：令牌桶 / 滑动窗口原语；`ErrLimited` |
| `pkg/errors` | `Retryable` / `RetryableOf` 默认可重试判定 |
| `api/openapi/components.yaml` | ErrorCode 枚举 |
| `internal/platform/server.go` | 优雅关闭超时 |
| `scripts/ci/check-reliability.sh` | REL-01/05 |
| 本文件 | 规范正文 |
