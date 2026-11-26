# 工程化审计规范（硬约束）

> **状态**：生效中（Normative）  
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**  
> **适用范围**：本仓库 A/B/C 关键操作的审计事件定义、字段、保留与存储完整性  
> **关联**：架构 §十二审计；可观测见 `OBSERVABILITY_STANDARDS.md`（OBS-07）；脱敏见 `SECURITY_STANDARDS.md`  
> **执行方式**：事件目录 + `internal/shared/audit` 抽检单测 + `check-audit.sh` + Review  
> **最后更新**：2026-11-07（审计出口分层：`emitter.go` 收敛为端口，实现层由 cmd 装配期注入）  
> **变更方式**：修改本规范须同步更新 `docs/audit/`、`internal/shared/audit`、`scripts/ci/check-audit.sh` 与架构 §十二，经架构 / 安全 Owner Review

---

## 1. 目标

1. **可追责**：谁在何时对何资源做了何操作，可按 `event_id` / `trace_id` 还原。  
2. **可合规**：保留期与存储完整性满足生产基线；禁止用可随意改写的业务表冒充唯一审计源。  
3. **可强制**：事件目录与字段字典入库；新增关键路径须登记并抽检。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| AUD-01 | 维护事件目录 [`docs/audit/EVENTS.md`](./audit/EVENTS.md)；新增必审操作须登记后方可合入 | `check-audit.sh` + Review |
| AUD-02 | 维护字段字典 [`docs/audit/FIELDS.md`](./audit/FIELDS.md)；实现字段名与字典一致 | `check-audit.sh` + 单测 |
| AUD-03 | 审计事件经 `internal/shared/audit` 发出（或等价封装）；禁止只打无 `audit=true` 的随意业务日志冒充审计 | 代码 + 抽检单测 |
| AUD-04 | 每条事件必含：`event_id`、`action`、`actor_id`、`channel`、`resource_type`、`resource_id`、`outcome`、`occurred_at`、`trace_id`、`service` | `Event.Validate` + 单测 |
| AUD-05 | 分端必审清单见 §4；A **强审计**、B **关键写审计**、C **关键点审计** | Review + EVENTS 目录 |
| AUD-06 | 生产审计存储：**追加写**（append-only）或等价 WORM；禁止业务进程 UPDATE/DELETE 审计行作为常规能力 | `check-audit.sh`（`audit_immutability_check.py`）+ Review |
| AUD-07 | 保留期基线：安全/登录/权限类 ≥ **180 天**；资金/支付/导出类 ≥ **365 天**（更严合规以组织政策为准）；到期销毁须「先归档、后删除」 | `check-audit.sh` + 集成测试（`tests/integration/audit`）+ EVENTS 列 |
| AUD-08 | 审计载荷禁止完整 Token、密码、DSN、支付完整卡号等密钥/高敏原文（对齐 SEC） | Review + 单测（脱敏样例） |
| AUD-09 | 合并请求触及审计相关时须勾选自检 | PR 模板 |

---

## 3. 事件与字段

### 3.1 目录

| 文件 | 用途 |
|------|------|
| [`docs/audit/EVENTS.md`](./audit/EVENTS.md) | 稳定 `action` 枚举、端、触发点、保留期 |
| [`docs/audit/FIELDS.md`](./audit/FIELDS.md) | 字段名、类型、必填、脱敏要求 |

`action` 命名：`{domain}.{verb}` 小写点分，如 `auth.login`、`rbac.role_change`、`data.export`。

### 3.2 实现包

契约层（只放类型、校验与端口，不依赖 `pkg/mq`、`pkg/outbox`、`pkg/db`）：

```text
internal/shared/audit/
  event.go      # Event + Validate
  actions.go    # 与 EVENTS 对齐的常量
  emitter.go    # Emitter 端口（仅接口）
```

实现层（出口实现，由 cmd 在装配期选择注入）：

```text
internal/platform/audit/
  emitter.go    # SlogEmitter（结构化日志出口）
  sql.go        # SQLEmitter（追加写审计表）+ MultiEmitter（多出口顺序写）
  outbox.go     # OutboxEmitter / OutboxHandler（事务内投递）
```

拆分理由：`shared/audit` 被 98 处引用，只消费 `Event` 的域不应被拖入 mq / outbox 等基础设施依赖；
契约纯度由 `check-audit.sh` 的 AUD-03b 断言守护（`internal/shared/audit` 出现 `pkg/{mq,outbox,retry,db}` 即失败）。

脚手架阶段允许 `SlogEmitter` 将事件写入结构化日志（`audit=true`）；生产须演进为独立存储或可靠投递（Outbox → 审计库 / SIEM），并补 ADR。

---

## 4. 分端必审清单（基线）

| 端 | 必审 `action`（见 EVENTS） |
|----|---------------------------|
| **A** | `auth.login`、`auth.login_failed`、`rbac.role_change`、`config.change`、`data.export` |
| **B** | `auth.login`、`auth.login_failed`、`rbac.role_change`、`payment.capture`、`payment.refund`、`data.export` |
| **C** | `auth.login`、`auth.login_failed`、`payment.capture`、`payment.refund` |

- A 端跨商户查询若返回明细，须另记 `data.query_scope`（可与导出合并策略，见 EVENTS）。  
- 「仅打访问日志」**不算**满足本表；须走 audit Emitter。

业务尚未实现某路径时：目录中可标 `status: planned`，但 **合入实现该路径的 MR 必须同时改为 `active` 并接线 Emitter**。

---

## 5. 保留与不可篡改

| 类别 | 最短保留 | 存储要求 |
|------|----------|----------|
| 认证 / 权限 / 配置 | 180 天 | 追加写；权限变更不可静默覆盖历史 |
| 支付 / 退款 / 对账 | 365 天 | 追加写；与资金流水可互证 |
| 数据导出 / 大范围查询 | 365 天 | 记录范围摘要（商户数、过滤条件哈希），禁止整包 PII 进审计 |

- 审计数据删除仅允许：到期归档销毁流程、或法律要求下的受限擦除（须工单 + 双人 + 留存「删除事实」元审计）。  
- **禁止** DBA 日常 `DELETE FROM audit_*`；应急须按 DATA-04 双人与补记录。

---

## 6. outcome 与关联

| `outcome` | 含义 |
|-----------|------|
| `success` | 操作成功 |
| `failure` | 操作失败（含鉴权失败、业务拒绝） |
| `denied` | 明确拒绝（权限不足） |

须尽量带 `trace_id` 与请求一致；异步补记时保留原 `trace_id` 或 `correlation_id`。

---

## 7. MUST NOT

- 用可 UPDATE 的业务状态表作为唯一审计依据。  
- 审计中写入密钥、完整 Bearer Token、密码明文、完整支付卡号。  
- 实现必审路径却不登记 EVENTS / 不调用 Emitter。  
- 为省事关闭生产审计或将保留期改到低于本专文基线且无 ADR。  

---

## 8. 本地与 CI

```bash
make check-audit          # AUD-01/02/04 + 契约纯度可自动项
go test ./internal/shared/audit/... ./internal/platform/audit/...
make ci
```

| 平台 | 入口 |
|------|------|
| GitHub / Gitee | job `arch` 内跑 `check-audit.sh` |
| MR | PR 模板「审计自检」 |

---

## 9. 例外

1. **本地 memory / demo**：可只用 SlogEmitter；prod 发版前须有落库或投递计划（Checklist / ADR）。  
2. **第三方 IdP 已有登录审计**：本系统仍须记「本系统接受会话」事件，或 ADR 说明互认与保留对齐。  
3. **紧急关闭某类审计**：Owner + 安全批准，到期日 ≤ 7 天，并记 EVENTS 临时豁免行。

---

## 10. Review 清单

- [ ] 新关键路径已在 EVENTS 登记且 `action` 稳定  
- [ ] 使用 `audit.Emitter`；字段通过 `Validate`  
- [ ] 无高敏原文；保留期符合类别  
- [ ] `make check-audit` 与 audit 包单测通过  

---

## 11. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| `docs/audit/EVENTS.md` | 事件目录（AUD-01） |
| `docs/audit/FIELDS.md` | 字段字典（AUD-02） |
| `internal/shared/audit` | 事件模型、字段校验与 Emitter 端口（AUD-03/04） |
| `internal/platform/audit` | 出口实现：Slog / SQL / Outbox / Multi |
| `scripts/ci/check-audit.sh` | 目录、契约纯度与基线抽检 |
| 本文件 | 规范正文 |
