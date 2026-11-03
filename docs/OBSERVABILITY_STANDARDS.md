# 工程化可观测性规范（硬约束）

> **状态**：生效中（Normative）
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**
> **适用范围**：本仓库全部可执行进程的日志、指标、追踪、健康检查与关键审计触发点
> **关联**：架构 §十二；脱敏见 `SECURITY_STANDARDS.md`；审计见 `AUDIT_STANDARDS.md`
> **执行方式**：`check-observability.sh` + Review + MR 模板
> **最后更新**：2026-10-19
> **变更方式**：修改本规范须同步更新 `platform` 日志/探针、`scripts/ci/check-observability.sh` 与架构 §十二，经架构 Owner Review

---

## 1. 目标

1. **可关联**：任意请求可通过 `trace_id` / `request_id` 串联日志与响应。
2. **可探活**：存活与就绪分离；编排与发布依赖标准探针。
3. **可追责**：A/B 关键写与导出具备审计触发点（字段字典见 AUD 专文）。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| OBS-01 | 结构化日志（生产默认 JSON）必须能带上：`trace_id`、`service`、`channel`、`env`；访问日志另含 method/path/status/duration | `pkg/logger` + `platform.NewLogger` + AccessLog + `check-observability.sh` |
| OBS-02 | 每个 API 进程必须暴露 `GET /healthz`（存活）与 `GET /readyz`（就绪）；`security: []` | `MountHealth` + OpenAPI + 检查脚本 |
| OBS-03 | `/readyz` 须探测关键依赖（如 DB）；memory 驱动至少注册仓储 ping；失败返回 503 | 代码 + Review |
| OBS-04 | 每个 API 进程必须暴露 `GET /version`（service + version） | `MountVersion` + 检查脚本 |
| OBS-05 | 业务响应信封携带 `trace_id`，并与访问日志一致（`X-Request-ID` / context） | API-03 + 实现 |
| OBS-06 | 预期内 4xx 默认不打 Error 级，避免告警噪音；5xx / panic 打 Error | Review |
| OBS-07 | A/B/C 关键操作写审计事件；字段与目录以 `AUDIT_STANDARDS` 为准 | `make check-audit` + Review |
| OBS-09 | 一次请求的 trace 必须能从入口追到 DB / 外部依赖；入口 span 名为 `METHOD <route>`（路由匹配后回填，禁止 `GET unknown`），SQL span 为 `sql.*` 且带 `db.system` | `pkg/db.OpenTraced` + `tracing.RoutePatternMiddleware` + `scripts/observability/check-trace-chain.py` |
| OBS-08 | 合并请求须勾选可观测自检 | PR 模板 |

---

## 3. 日志字段字典（基线）

| 字段 | 必填场景 | 说明 |
|------|----------|------|
| `service` | 全部 | 如 `admin-api` / `biz-api` / `c-api` / `worker` |
| `channel` | 全部 | `admin` / `biz` / `c` / `worker` |
| `env` | 全部 | `local` / `dev` / `test` / `staging` / `prod` |
| `trace_id` | 请求路径 | 与 `X-Request-ID` / 响应 `trace_id` 对齐 |
| `span_id` | 接入 OTel 后 | 可与 trace 并存 |
| `request_id` | 可选别名 | 若与 `trace_id` 分离须文档说明 |
| `merchant_id` / `user_id` | 业务日志 | 键名全局统一；禁止原样打 Token / 密码 |
| `degraded` | 降级路径 | 与 REL-06 对齐 |

脱敏要求见 SEC（PII、密钥、完整 Authorization）。

---

## 4. 健康检查

| 路径 | 语义 | 成功 | 失败 |
|------|------|------|------|
| `/healthz` | 进程存活 | 200 | 进程无响应 |
| `/readyz` | 可接流量 | 200 + checks ok | 503 |
| `/version` | 构建身份 | 200 | — |

- 探针**不得**要求鉴权（SEC-07）。
- 容器 `HEALTHCHECK` 应指向 `/healthz`（发布专文）。
- 就绪检查失败原因可返回依赖名；**禁止**回显 DSN / 密钥。

---

## 5. 指标与追踪（基线）

| 维度 | 要求 |
|------|------|
| 指标 | RED（Rate/Errors/Duration）按 `service`/`channel`（及 `env`）常量标签；实现见 `pkg/metrics` |
| 拉取 | API 进程暴露 `GET /metrics`（Prometheus 文本）；生产须网络隔离，禁止公网裸奔 |
| 追踪 | OpenTelemetry（`pkg/tracing`）；W3C `traceparent`；无 `X-Request-ID` 时复用 OTel TraceID 作为信封/日志 `trace_id` |
| 队列 | worker 积压、消费失败须可观测 |

HTTP `route` 标签须使用低基数模板（ServeMux `Pattern` 或显式映射），禁止默认打原始 URL.Path。

异步基础设施（ENT-03，`internal/platform/async_metrics.go`）指标基线：

| 指标 | 类型 | 说明 / 告警 |
|------|------|-------------|
| `async_outbox_delivery_total{result}` | Counter | `success/retry/dead/poison`；`dead`/`poison` 增长触发 P2 |
| `async_outbox_pending` | Gauge | 未完成事件数（含重试等待与死信）；持续 >0 说明投递受阻 |
| `async_outbox_lag_seconds` | Gauge | 最老未完成事件积压秒数；持续增长触发 P2 |
| `async_dlq_depth` | Gauge | 未重放死信数；>0 需按 `docs/runbooks/async-dlq.md` 处理 |
| `db_pool_open_connections` / `db_pool_in_use_connections` / `db_pool_max_open_connections` | Gauge | 连接池使用率；`in_use/max > 90%` 触发 P1（`internal/platform/db_metrics.go`） |
| `db_pool_wait_total` / `db_pool_wait_duration_seconds_total` | Counter | 等待连接次数与耗时；持续增长说明池容量或慢查询问题 |

告警规则、SLO 与看板制品见 [`deploy/observability/README.md`](../deploy/observability/README.md)；
告警 ↔ runbook 目录见 [`docs/runbooks/ALERTS.md`](./runbooks/ALERTS.md)。

IAM 审计投递仍保留 `iam_outbox_delivery_total` / `iam_outbox_pending` / `iam_outbox_lag_seconds`，
由 IAM 观测适配器映射到同一 Registry；两套指标不得互相替代。

---

## 6. 审计触发点

**完整硬约束**见 [`AUDIT_STANDARDS.md`](./AUDIT_STANDARDS.md)（事件目录、字段字典、保留期、不可篡改）。
实现统一走 `internal/shared/audit` Emitter；禁止用普通访问日志冒充审计。

---

## 7. MUST NOT

- 生产使用无 `service`/`channel` 的裸 `log.Printf` 作为主访问日志。
- 去掉 `/healthz` 或 `/readyz`，或对其强制鉴权。
- 在探针或日志中打印 DSN、密钥、完整 Token。
- 用 Error 级别刷屏记录预期 4xx。

---

## 8. 本地与 CI

```bash
make check-observability   # OBS-01/02/04 可自动项
curl -s localhost:8081/healthz
curl -s localhost:8081/readyz
make ci
```

| 平台 | 入口 |
|------|------|
| GitHub / Gitee | job `arch` 内跑 `check-observability.sh` |
| MR | PR 模板「可观测自检」 |

---

## 9. 例外

1. **migrate CLI**：短时命令可不挂 HTTP 探针；须有非零退出与日志。
2. **本地 text 日志**：`log.format=text` 仅限 local/dev；prod example 保持 json。
3. **审计存储未就绪**：允许 `SlogEmitter`（见 AUD）；生产落库/投递须有 ADR 或发版 Checklist。

---

## 10. Review 清单

- [ ] Logger 绑定 `service`/`channel`/`env`；访问日志含 `trace_id`
- [ ] 三端 API 具备 healthz/readyz/version
- [ ] 一次请求的 trace 可从入口追到 DB / 外部依赖（OBS-09）
- [ ] 新关键写路径有审计或明确豁免理由
- [ ] 无敏感字段进入日志
- [ ] `make check-observability` 通过

---

## 11. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| `pkg/logger` | OBS-01：结构化 slog、字段字典、ContextHandler / WithTraceID |
| `internal/platform/logger.go` | 进程配置 → `pkg/logger` 薄封装 |
| `internal/platform/middleware.go` | RequestID / AccessLog |
| `internal/platform/endpoints.go` | MountHealth / MountVersion / MountMetrics |
| `pkg/health` | 存活/就绪实现 |
| `pkg/metrics` | Prometheus Registry、HTTP RED、build_info |
| `pkg/tracing` | OTel Init / Shutdown、HTTPMiddleware、RoutePatternMiddleware、span 工具 |
| `pkg/db`（`OpenTraced`） | SQL CLIENT span（`db.system` / `db.name` / 语句），依赖 `github.com/XSAM/otelsql` |
| `scripts/ci/check-observability.sh` | OBS-01/02/04 + metrics / tracing 挂载 |
| 本文件 | 规范正文 |
