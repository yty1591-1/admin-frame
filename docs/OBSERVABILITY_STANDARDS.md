# 工程化可观测性规范（硬约束）

> **状态**：生效中（Normative）  
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**  
> **适用范围**：本仓库全部可执行进程的日志、指标、追踪、健康检查与关键审计触发点  
> **关联**：架构 §十二；脱敏见 `SECURITY_STANDARDS.md`；审计见 `AUDIT_STANDARDS.md`  
> **执行方式**：`check-observability.sh` + Review + MR 模板  
> **最后更新**：2026-10-13  
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
| OBS-01 | 结构化日志（生产默认 JSON）必须能带上：`trace_id`、`service`、`channel`、`env`；访问日志另含 method/path/status/duration | `NewLogger` + AccessLog + `check-observability.sh` |
| OBS-02 | 每个 API 进程必须暴露 `GET /healthz`（存活）与 `GET /readyz`（就绪）；`security: []` | `MountHealth` + OpenAPI + 检查脚本 |
| OBS-03 | `/readyz` 须探测关键依赖（如 DB）；memory 驱动至少注册仓储 ping；失败返回 503 | 代码 + Review |
| OBS-04 | 每个 API 进程必须暴露 `GET /version`（service + version） | `MountVersion` + 检查脚本 |
| OBS-05 | 业务响应信封携带 `trace_id`，并与访问日志一致（`X-Request-ID` / context） | API-03 + 实现 |
| OBS-06 | 预期内 4xx 默认不打 Error 级，避免告警噪音；5xx / panic 打 Error | Review |
| OBS-07 | A/B/C 关键操作写审计事件；字段与目录以 `AUDIT_STANDARDS` 为准 | `make check-audit` + Review |
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
| 指标 | RED（Rate/Errors/Duration）按 `service`/`channel` 分标签；后续接 Prometheus |
| 追踪 | OpenTelemetry；跨服务透传 W3C Trace Context 或约定 header |
| 队列 | worker 积压、消费失败须可观测 |

脚手架阶段允许尚未导出 Prometheus scrape；新增中间件/客户端时不得阻碍后续挂载。接入后更新本表门禁。

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
- [ ] 新关键写路径有审计或明确豁免理由  
- [ ] 无敏感字段进入日志  
- [ ] `make check-observability` 通过  

---

## 11. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| `internal/platform/logger.go` | OBS-01 基础字段 |
| `internal/platform/middleware.go` | RequestID / AccessLog |
| `internal/platform/wiring.go` | MountHealth / MountVersion |
| `pkg/health` | 存活/就绪实现 |
| `scripts/ci/check-observability.sh` | OBS-01/02/04 |
| 本文件 | 规范正文 |
