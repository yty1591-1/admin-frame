# 生产级与企业级平台能力差距评估（Production Readiness Gaps）

> **状态**：评估完成（v0.2，2026-10-22）；基线 `dad8fc0`
> **定位**：本文只评估**平台级能力**——任何未来业务域都需要的身份、租户、数据、异步、多实例、运维、安全与交付能力；具体业务域的实现不在评估范围内
> **适用范围**：`internal/platform/`、`internal/security/`、`internal/shared/`、`internal/iam/`、`cmd/`、`pkg/`、`migrations/`、`deploy/`、`tests/`、`.github/`、`docs/`
> **关联**：[ARCHITECTURE.md](./ARCHITECTURE.md)、[standards-roadmap.md](./standards-roadmap.md)、[platform-remediation-plan.md](./archive/platform/platform-remediation-plan.md)、[rbac-iam.md](./rbac-iam.md)、[SECURITY_STANDARDS.md](./SECURITY_STANDARDS.md)、[RELIABILITY_STANDARDS.md](./RELIABILITY_STANDARDS.md)、[OBSERVABILITY_STANDARDS.md](./OBSERVABILITY_STANDARDS.md)、[AUDIT_STANDARDS.md](./AUDIT_STANDARDS.md)、[DATA_STANDARDS.md](./DATA_STANDARDS.md)、[RELEASE_STANDARDS.md](./RELEASE_STANDARDS.md)、[RUNBOOK_STANDARDS.md](./RUNBOOK_STANDARDS.md)、[TESTING_STANDARDS.md](./TESTING_STANDARDS.md)
> **最后更新**：2026-10-22
> **变更方式**：新增 / 关闭条目须同步更新「十、状态跟踪」与「执行记录」；P0 关闭须附验收命令与输出。优先级调整须架构 Owner 确认
> **目标读者**：技术负责人、后端工程师、SRE、安全 / 合规 Reviewer、产品负责人

---

## 目录

1. [结论摘要](#一结论摘要)
2. [评估口径](#二评估口径)
3. [成熟度总览](#三成熟度总览)
4. [P0 上线阻断项](#四p0-上线阻断项)
5. [P1 企业运营能力](#五p1-企业运营能力)
6. [P2 规模化与合规能力](#六p2-规模化与合规能力)
7. [里程碑与依赖](#七里程碑与依赖)
8. [门禁与规范回写](#八门禁与规范回写)
9. [风险与实施原则](#九风险与实施原则)
10. [状态跟踪](#十状态跟踪)
11. [附录](#附录)

---

## 一、结论摘要

### 1.1 总评

本项目的**工程治理层已接近企业级**：分层架构、18 份硬约束专文、`make ci` 门禁、依赖 / 许可证 / 密钥扫描、ADR、runbook、IAM 生命周期与事务性 Outbox 均已成体系。但**平台运行时能力仍是「单进程可演示骨架」**，距离「可多实例、可运维、可审计、可恢复」的生产平台有明显缺口。

| 维度 | 当前判断 | 说明 |
|---|---|---|
| 工程规范与门禁 | **L3（企业级）** | 硬约束专文 + 双平台流水线 + 覆盖率门禁 |
| 架构分层与可扩展性 | **L2～L3** | 分层清晰、IAM 已按 DDD 落地，可作为未来业务域参考实现 |
| 身份与访问 | **L1～L2** | 只有 JWT 校验；无登录、刷新、撤销、账号域 |
| 租户模型 | **L1** | 只有 `merchant_id` 透传，无租户注册、状态、配额与退出机制 |
| 异步与事件基础设施 | **L2** | IAM Outbox 完整；通用 Outbox + 消费运行时齐备；`async.driver=nats` 可选接入 NATS JetStream |
| 多实例运行 | **L2** | ID 节点号可配置、幂等与限流支持 Redis 共享；待 staging 多副本验收 |
| 可观测性 | **L2～L3** | 指标 / 追踪 / 日志齐备；告警规则、SLO、看板与 Collector 已入库，告警触达与演练待 staging |
| 部署与运维 | **L2** | Helm Chart、三套 values、迁移 Job、备份 CronJob 已入库；环境演练待执行 |
| 安全与合规 | **L1～L2** | 校验与扫描强；密钥托管、审计留存、数据生命周期缺失 |
| 质量保障 | **L2** | 单测 / 竞态 / 覆盖率强；集成、E2E、契约、压测不足 |

**一句话结论**：当前最需要的是把平台从「单进程 + 内存 + 手工运维」升级为「真实身份 + 可复用领域基础设施 + 可观测 + 可恢复 + 可多实例」，而不是继续补演示业务接口。

### 1.2 P0 上线阻断项（Top 8）

| ID | 缺口 | 直接后果 | 现状证据 |
|---|---|---|---|
| `ENT-01` | 身份闭环缺失：无登录 / 刷新 / 登出 / 撤销 / 账号域 | 无法接入真实用户；令牌泄露只能等过期 | `internal/security/` 仅验签；全仓无身份令牌端点 |
| `ENT-02` | ~~多实例语义未闭环~~ **已关闭（2026-10-22）** | NodeID 配置化、Redis 幂等 / 限流、worker 独立发布器 | `internal/platform/id.go`、`pkg/idempotency/redis.go`、`pkg/ratelimit/redis.go` |
| `ENT-03` | ~~无通用异步基础设施~~ **已关闭（2026-10-22）** | 通用 Outbox / 消费运行时 / DLQ；IAM 已完成迁移 | `pkg/outbox`、`internal/platform/async.go`、`cmd/worker/main.go` |
| `ENT-04` | 审计只写结构化日志，不可查询 / 保留 | 无法满足取证、合规与 180 天留存 | `internal/shared/audit/emitter.go:14`；`audit:read` 无查询端点 |
| `ENT-05` | 无部署制品与环境基线 | 无法可重复部署、无法做资源与滚动安全 | `deploy/README.md:1-9` 仅说明文字 |
| `ENT-06` | 告警规则 / 看板 / SLO 未代码化 | 只有告警目录，监控系统不会真正触发 | `docs/runbooks/ALERTS.md:5-17`；无 PrometheusRule / Grafana 文件 |
| `ENT-07` | 无密钥托管与轮换流程 | 密钥只能静态注入；轮换需停机 | 配置仅支持 env / 文件；无 KMS / Vault 适配 |
| `ENT-08` | 无备份、恢复与灾备演练 | 数据损坏或误删不可恢复 | `DATA_STANDARDS` 有要求，无实现与 runbook |

> P0 的关闭标准不是「代码写完」，而是「在 staging 多副本环境验证通过 + 有回滚方式 + 有自动化门禁」。

### 1.3 建议顺序

1. **先运行时与身份**：`ENT-02 → ENT-03 → ENT-01`，为未来业务域提供可复用底座。
2. **再审计**：`ENT-04`，任何对外写入都必须可追溯。
3. **并行补运维底座**：`ENT-05 / ENT-06 / ENT-07 / ENT-08` 可与上述并行，但必须在对外灰度前完成。
4. **P1 按「租户 → 通知 / 开关 → 数据范围 → 测试与发布」推进**。
5. **P2 只在有明确规模或合规诉求时启动**，避免过度设计。

---

## 二、评估口径

### 2.1 基线与方法

| 项 | 内容 |
|---|---|
| 代码基线 | `dad8fc0`（2026-10-22，工作区干净） |
| 评估方法 | 平台与公共层走查 + 关键路径静态检索 + 现有测试与流水线盘点 + 既有文档对照 |
| 证据原则 | 只把**平台 / 公共 / 运维层可验证的事实**列为现状 |
| 对照文档 | `ARCHITECTURE.md`、`platform-remediation-plan.md`（PLAT 已完成）、`rbac-iam.md`（M0～M4.7 已完成，M5 进行中）、18 份 `*_STANDARDS.md` |
| 复核命令 | 见 [附录 A](#附录-a核对命令) |

### 2.2 成熟度等级

| 等级 | 定义 | 典型特征 |
|---|---|---|
| **L0 缺失** | 无实现，也无明确方案 | 只能靠人工或不存在 |
| **L1 开发骨架** | 单进程 / 内存 / 演示可用 | 重启丢状态、不能多副本、手工运维 |
| **L2 可生产灰度** | 真实依赖 + 基础运维 + 可回滚 | 小流量、单区域、有限副本可运行 |
| **L3 企业级规模化** | 多实例、多环境、可观测、可审计、可恢复、有合规流程 | 有 SLO、演练、容量模型与治理机制 |

### 2.3 已具备能力（不重复建设）

- **架构与治理**：模块化单体 + 多 cmd、分层依赖检查、ADR、18 份硬约束专文、`make ci` 全套门禁。
- **平台层**：`platform.Run` 统一装配、HTTP Server 加固、超时 / 限流 / BodyLimit / CORS 链路、配置校验与脱敏、健康检查、版本与指标端点。
- **安全基础**：JWT（HS256 / RS256）验签、三端隔离、默认拒绝、`Authorizer` 权限中间件、gitleaks / gosec / govulncheck、Trivy 镜像扫描。
- **IAM**：角色 / 权限 / 绑定 / 数据范围 / 审批单 / 破玻璃 / 两级缓存 / MySQL 仓储 / 事务性 Outbox / UnitOfWork / 生产强制约束，可作为未来业务域的参考实现。
- **公共能力**：`pkg/` 已覆盖 DB、Redis、缓存、MQ（内存）、Outbox、HTTP Client、重试、熔断、限流、JWT、密码哈希、加密、存储、短信 / 邮件 / 推送 / Webhook、分页、校验、脱敏、幂等。
- **交付**：多阶段 Dockerfile、多架构镜像、迁移工具与 up/down 门禁、runbook 框架、发布 Checklist。

---

## 三、成熟度总览

| 能力域 | 现状 | 目标（E1 / E3） | 关键缺口 | 结论 |
|---|---|---|---|---|
| 工程治理与 CI | L3 | L3 | 分支保护人工项、部署 / 告警门禁缺失 | 保持并补门禁 |
| 身份与访问 | L1～L2 | L2 / L3 | 无账号域、登录、刷新、撤销、MFA、SSO | **P0** |
| 租户模型 | L1 | L2 / L3 | 无租户注册、状态、配额、退出与数据隔离契约 | P1 |
| 平台可扩展性 | L2 | L3 | 未来业务域缺少统一脚手架与契约测试 | P1 |
| 异步与事件 | L2 | L2 / L3 | 通用 Outbox/运行时 + NATS JetStream 适配已入库；broker 运维与容量演练待办 | P1 |
| 多实例与容量 | L2 | L2 / L3 | 多实例语义已闭环；缺压测基线与容量模型 | P1 |
| 可观测性 | L2～L3 | L3 | 规则/看板/SLO/Collector 已入库；告警触达与演练待 staging | **P0** |
| 部署与运维 | L2 | L2 / L3 | Helm Chart/三套 values/迁移 Job/备份 CronJob 已入库；CD 与演练待补 | **P0** |
| 安全与合规 | L1～L2 | L2 / L3 | 密钥托管、审计留存、数据生命周期、边缘防护 | **P0 / P1 / P2** |
| 质量保障 | L2 | L3 | 集成矩阵、E2E、契约、负载测试不足 | P1 |

---

## 四、P0 上线阻断项

> 关闭定义：验收标准全绿 + staging 多副本验证 + 回滚方式明确 + 门禁生效。
> 编号规则：`ENT-xx`（Enterprise capability）。

### ENT-01 身份闭环：账号域与令牌生命周期

**状态**：`已完成（2026-10-23，阶段 1–5）`；领域模型、令牌 / 撤销语义、配置与任务拆解详见 [`identity-authn.md`](./identity-authn.md)。缺口结论与验收标准不变。

**现状证据**

- `internal/security/` 只做**校验侧**：demo / JWT 验签、Principal 映射、`Authorizer`；`pkg/jwt/manager.go:181` 虽能签发，但没有对外端点。
- `internal/` 下无 `user` / `account` / `staff` 域；`internal/shared/auth/principal.go:30-36` 只承载 `SubjectID` / `MerchantID` / `Roles`，没有账号状态与凭据概念。
- 全仓无**身份令牌**的 `login` / `logout` / `refresh` / `revoke` 端点（IAM 破玻璃的撤销属于权限侧，不解决会话失效）；`configs/*.yaml.example` 只有 `auth.mode` 与验签参数。
- `ARCHITECTURE.md` §10.1 要求 A 端员工账号（密码 / SSO）+ 独立 Token 域、B 端商户员工账号、C 端消费者账号，当前均未落地。

**缺口**

1. 无账号实体、密码 / 凭据存储、账号状态（启用 / 禁用 / 锁定）。
2. 无登录、刷新、登出、令牌撤销；JWT 在过期前无法失效。
3. 无 MFA（A 端）、无 SSO / OIDC、无密码策略与登录防爆破。
4. C 端无手机号 / OAuth 登录与验证码防刷。

**风险**：无法接入真实用户；离职或令牌泄露后只能等待过期；不满足 `SEC-04` 的生产要求。

**目标态**

- 新增 `internal/identity`（或 `internal/account`）域：账号、凭据、状态、登录审计。
- 三端认证端点：`/admin/v1/auth/*`、`/biz/v1/auth/*`、`/c/v1/auth/*`；刷新令牌轮换与重放检测；登出即撤销。
- 令牌撤销采用 `token_version`（账号级）或 `jti` 黑名单（Redis）；校验中间件读取撤销状态。
- A 端强制 MFA（TOTP），组织已有 SSO 时优先接 OIDC；C 端登录 / 验证码独立限流。
- 密码使用 Argon2id（`pkg/password` 已有能力）；登录失败锁定与审计事件接入。

**验收标准**

- [x] `AC-ENT-01-1`：账号禁用或登出后 ≤ 60s，旧访问令牌被拒绝；有自动化用例。
- [x] `AC-ENT-01-2`：刷新令牌一次性使用；重放触发撤销并记审计。
- [x] `AC-ENT-01-3`：三端 issuer / audience / 密钥隔离，跨端令牌互不通过。
- [x] `AC-ENT-01-4`：登录失败限流与锁定生效；A 端未完成 MFA 不得获得管理令牌。
- [x] `AC-ENT-01-5`：登录、登出、失败、锁定、MFA 绑定均产生审计事件。

**门禁**：`internal/security` 与身份域单测；OpenAPI 三端契约；`make secrets`；生产配置校验禁止 demo。

---

### ENT-02 多实例运行语义

**状态**：`已关闭（2026-10-22，代码与门禁闭环；staging 两副本验收待部署环境）`

**原缺口证据（已修复）**

- ~~Snowflake 节点号固定为 1~~：`cmd/internal/iamruntime/runtime.go` 与 IAM 用例已改为消费 `platform.Deps.IDs`，缺失即失败关闭；`internal/platform/id.go` 支持显式 NodeID 与 StatefulSet ordinal。
- ~~限流为进程内令牌桶~~：`pkg/ratelimit.RedisKeyed` 以 Redis `TIME` 实现跨副本令牌桶，`rate_limit.driver=redis` 时两副本共享配额。
- ~~IAM Outbox 发布器只在 API 进程内~~：ENT-03 起统一为平台事务性 Outbox（`async_outbox`），
  `async.outbox.publish_in_api=false` 时仅 `cmd/worker` 的 `AsyncRuntime` 投递，IAM 指标由 Observer 映射。
- ~~`pkg/idempotency` 无统一装配点~~：`internal/platform/pipeline.go` 在写方法前统一挂载，Redis Store 支持跨副本回放。

**落地证据**

- ID：`internal/platform/id.go`、`internal/platform/config.go`（`validateID`）、`platform.Deps.IDs`。
- 幂等：`pkg/idempotency/redis.go`、`internal/platform/pipeline.go`（`newIdempotency`）。
- 限流：`pkg/ratelimit/redis.go`、`pkg/middleware/ratelimit.go`（`Buckets` 接口）。
- Outbox：`internal/platform/async.go`（`AsyncRuntime`）、`pkg/outbox`、`cmd/internal/iamruntime/runtime.go`（`RegisterAsync`）、`cmd/worker/main.go`。
- 用例：`tests/integration/multiinstance/multiinstance_test.go`；门禁：`scripts/ci/check-idempotency.sh`。

**验收标准**

- [x] `AC-ENT-02-1`：两个副本使用不同 NodeID 启动，批量创建无 ID 冲突（已由 multiinstance 用例覆盖）。
- [x] `AC-ENT-02-2`：同一 `Idempotency-Key` 打到不同副本返回同一结果（Redis Store + 管线用例覆盖）。
- [x] `AC-ENT-02-3`：两个副本共享同一限流配额；超限统一返回 `42900`。
- [x] `AC-ENT-02-4`：滚动重启期间无事件丢失、无重复副作用（MySQL outbox 重启用例覆盖）。
- [ ] staging 两副本环境实测记录（需部署制品，随 `ENT-05` 补）。

**门禁**：`make check-idempotency`（挂载 / 配置 / 固定 NodeID / OpenAPI 契约）；`check-config` 校验生产 NodeID 与 Redis 强制项。

---

### ENT-03 通用异步基础设施：Outbox、Broker 与消费运行时

**状态**：`已关闭（2026-10-22，代码、门禁与真实 MySQL 集成用例闭环；broker 适配按 T7 仅预留接口）`

**原缺口证据（已修复）**

- `pkg/mq` 目前只有内存实现；`docs/pkg-roadmap.md:41` 明确 Kafka / NATS 尚未接入。
- `cmd/worker/main.go` 当前为生命周期骨架（配置加载 + 信号退出 + 心跳），等待平台级异步运行时接入。
- IAM 的事务性 Outbox 已完整（`cmd/internal/iamruntime/runtime.go:111-136`），但**绑定在 IAM 装配内**，未沉淀为平台级、任何业务域可声明式接入的运行时。
- `pkg/mq` 提供 Outbox 原语，但没有统一的发布调度、消费者注册、重试策略、DLQ 与事件版本治理。

**缺口**

1. 无生产级 broker 适配（Kafka / NATS / RabbitMQ / 云 MQ 任选其一）。
2. 无通用 Outbox Relay：未来业务域各自实现会重复踩坑，且容易漏事务。
3. 无统一消费运行时：重试、退避、幂等、死信、并发度、优雅退出、积压指标缺失。
4. 无事件 envelope 规范（`event_id` / `event_type` / `version` / `tenant_id` / `occurred_at` / trace 上下文）。

**目标态**

- 平台提供 `pkg/mq` 的生产 broker 适配与 `OutboxRelay`（先支持 DB 轮询；有容量诉求再换 broker）。
- 提供消费框架：注册 handler、至少一次、幂等键、指数退避、DLQ、并发与限流、优雅退出。
- 事件 envelope 与 schema 版本化写入规范；不兼容变更走新版本事件。
- 业务域只需声明「事件类型 + handler + Outbox 表」，不重复实现可靠性逻辑。

**验收标准**

- [x] `AC-ENT-03-1`：示例业务域在「事务已提交、发布前」杀进程，重启后事件仍被投递。
  （`tests/integration/async/async_test.go:TestOutboxResumeAfterRestart`）
- [x] `AC-ENT-03-2`：重复投递不产生重复副作用；消费幂等用例通过。
  （`pkg/outbox/dedupe_test.go`、`TestOutboxConcurrentReplicasDeliverOnce`）
- [x] `AC-ENT-03-3`：毒消息进入 DLQ 并告警；积压 / 滞后指标可见。
  （`TestOutboxPermanentFailureWritesDLQ`、`TestOutboxRetryExhaustedWritesDLQ`、`internal/platform/async_metrics.go`）
- [x] `AC-ENT-03-4`：worker 可消费由 API 进程写入的事件（跨进程验证）。
  （`TestOutboxCrossProcessDelivery`，两条独立连接模拟 API / worker）
- [x] `AC-ENT-03-5`：新增业务域接入异步能力不需要修改平台核心代码。
  （`pkg/outbox/router_test.go`，仅 `Router.Register` 接入；platform 无业务分支）

**落地证据**

- 运行时：`pkg/outbox`（Event / Store / MySQLStore / Relay / Router / Deduper / DeadLetterReplayer）、`internal/platform/async.go`（`AsyncRuntime`）。
- 迁移：`migrations/000004_create_async_outbox.*`（平台异步表，含 IAM 审计事件）。
- worker：`cmd/worker/main.go`（Relay 宿主 + `-replay-dlq`）。
- IAM 迁移：`internal/iam/app/outbox.go`（`OutboxRecorder` 写 `async_outbox`）、`cmd/internal/iamruntime/runtime.go`（`RegisterAsync`）。

**门禁**：`scripts/ci/check-outbox.sh`、集成测试（真实 MySQL）、`make check-observability`（积压指标断言）。

---

### ENT-04 审计持久化、查询与保留

**现状证据**

- 默认审计出口是结构化日志：`internal/shared/audit/emitter.go:14-20`；Outbox 最终也投递到 `SlogEmitter`（`cmd/internal/iamruntime/runtime.go:106-120`）。
- `internal/iam/perm.go:58` 定义了 `audit:read`，但 `internal/iam/transport/admin/router.go` 无审计查询 / 导出端点。
- `docs/audit/EVENTS.md` 与 `AUDIT_STANDARDS.md` 要求事件目录、字段字典、追加写存储与保留期；当前只有目录与字段，**无存储实现**。
- 审计日志会随容器日志轮转，无法满足 180 天留存与按主体 / 资源检索。

**缺口**

1. 无追加写审计存储（数据库表 / 对象存储 WORM），无防篡改设计。
2. 无查询、导出、按时间 / 主体 / 资源过滤的 A 端接口。
3. 无保留期、归档、法务保留（legal hold）与清理任务。
4. 无审计写入失败的业务策略（哪些操作必须失败关闭）。

**目标态**

- 新增审计存储（建议独立表或对象存储分区），经 Outbox 异步落库；关键操作写入失败即业务失败。
- A 端提供 `GET /admin/v1/audit/events` 与导出接口，权限点 `audit:read`，导出本身再审计。
- 保留 ≥ 180 天，支持归档与法务保留；敏感字段脱敏后存储。
- 可选哈希链 / 签名，用于防篡改验证。

**验收标准**

- [x] `AC-ENT-04-1`：角色 / 绑定 / 破玻璃 / 登录 / 导出事件可查询，过滤条件正确
      （`tests/integration/audit/query_test.go` 覆盖 action / actor / 时间窗过滤与分页总量；
      `docs/audit-export-verify.md` 记录 `action=auth.login` 查询 total=7 与导出 CSV 行数一致）。
- [x] `AC-ENT-04-2`：保存期限与归档任务生效；到期数据可验证清理
      （`audit.retention.archive_prefix` 开启后每批「先归档到对象存储、再按 ID 删除」，
      归档失败即中止且一行不删；`TestAuditRetentionArchivesThenDeletes` 验证 12 行分 3 批归档、
      归档对象行数 = 删除行数、未到期数据保留；dry-run / legal_hold 只统计不删除）。
- [x] `AC-ENT-04-3`：审计存储不可更新 / 删除（除保留期任务），有测试或约束证明
      （`scripts/ci/audit_immutability_check.py` 并入 `check-audit`：保留期仓储之外不得出现
      `DELETE FROM`，涉及 `audit_event` 的文件不得出现 `UPDATE`；删除只按 cutoff 或事件 ID 两种受控方式）。
- [x] `AC-ENT-04-4`：审计写失败时，关键操作返回失败而非静默成功
      （`TestAuditFailureFailsClosed` 角色创建在记录器不可用时返回 50000；
      `TestMultiEmitterStopsOnFailure` 任一出口失败即中断；导出任务 emit 失败返回错误而非静默置成功）。

**门禁**：`make check-audit` 扩展为「事件目录 ↔ 代码 ↔ 查询契约」一致性检查。

---

### ENT-05 部署制品与环境基线

**现状证据**

- `deploy/README.md:1-9` 只有部署建议，无任何 Deployment / Service / Ingress / HPA / PDB / NetworkPolicy 清单。
- 仓库只有 `configs/*.yaml.example`，无 dev / staging / prod 环境基线；`configs/*.yaml` 已被 `.gitignore` 忽略。
- `Dockerfile` 与 `.github/workflows/image.yml` 已支持多架构镜像与 Trivy 扫描，但无部署流水线。

**缺口**

1. 无 Helm Chart / Kustomize overlays，无法可重复部署与差异化环境。
2. 无资源 requests/limits、HPA、PDB、反亲和、NetworkPolicy、ServiceAccount / RBAC。
3. 无 Secret 注入方案（ExternalSecret / Vault / KMS），无迁移 Job 与发布顺序约束。
4. 无 staging 环境与冒烟 / 回滚脚本。

**目标态**

- 提供 Helm Chart（或 Kustomize base + overlays）：三端 API + worker + migrate Job。
- 每端独立 Deployment / Service / Ingress；readiness `/readyz`、liveness `/healthz`；资源与 HPA；PDB；NetworkPolicy 默认拒绝。
- Secret 通过外部密钥管理注入，镜像固定 digest；安全上下文非 root、只读根文件系统、最小 capabilities。
- 迁移以 Job 在发布前执行，失败即阻断发布。

**验收标准**

- [ ] `AC-ENT-05-1`：`helm template` / `kubectl --dry-run` 在 dev / staging / prod 三套 values 下通过。
- [ ] `AC-ENT-05-2`：清单包含探针、资源、HPA、PDB、NetworkPolicy、安全上下文。
- [ ] `AC-ENT-05-3`：仓库与渲染产物中无明文密钥；镜像使用 digest。
- [ ] `AC-ENT-05-4`：staging 完成一次部署 → 冒烟 → 回滚演练。

**门禁**：新增 `make check-deploy`（清单存在性、探针、资源、密钥、digest）。

---

### ENT-06 告警规则、看板与 SLO 落地

**现状证据**

- `docs/runbooks/ALERTS.md:5-17` 已定义 11 条 P1/P2 告警，但仓库中无 PrometheusRule / Alertmanager / Grafana 文件。
- `/metrics`、RED 指标、OTel 追踪与结构化日志已具备（`internal/platform`、`pkg/metrics`、`pkg/tracing`）。
- `docs/runbooks/` 仅 4 个场景 runbook，且部分告警复用同一 runbook。

**缺口**

1. 告警只存在于文档，监控系统不会触发；无 `for`、阈值、路由、抑制规则。
2. 无 SLO / 错误预算 / 燃烧率告警；无端侧 P99 与可用性定义。
3. 无看板（RED、IAM、Outbox、worker、DB 连接池）；无日志聚合与追踪后端接入说明。
4. 无告警演练机制，无法证明告警有效。

**目标态**

- 告警规则代码化（PrometheusRule / 云监控配置），每条 P1/P2 必须关联 runbook。
- SLO 记录规则与错误预算燃烧率告警；按 `service` / `channel` 分标签。
- 看板代码化（Grafana JSON / IaC）；覆盖 RED、IAM 授权、Outbox 积压、worker 滞后、连接池。
- 接入日志聚合与 OTLP 后端；定义采样率与保留期。

**验收标准**

- [x] `AC-ENT-06-1`：每条 `ALERTS.md` 告警都有规则文件与 runbook 链接（`make check-alerts` 通过）。
- [x] `AC-ENT-06-2`：staging 注入故障（5xx / DB 池耗尽 / Outbox 积压）可触发对应告警
      （2026-11-02 kind 真集群实测：`API5xxRateHigh` / `DBPoolExhaustion` / `AsyncOutboxBacklog` 均实触发；
      记录见 [alert-drill-ent06.md](./alert-drill-ent06.md)）。
- [x] `AC-ENT-06-3`：看板能看到三端 RED、IAM 授权结果、Outbox / worker 指标
      （2026-11-03 kind 真集群实测：37 个面板查询错误 0、空数据 0；记录见
      [observability-dashboard-verify.md](./observability-dashboard-verify.md)）。
- [x] `AC-ENT-06-4`：一次请求的 trace 可从入口追到 DB / 外部依赖
      （2026-11-03 kind 真集群实测：`GET /admin/v1/audit/events` → `sql.conn.query`
      `db.system=mysql` 且带语句，父子链完整；做法为 `pkg/db.OpenTraced` 用 otelsql 包装驱动，
      记录见 [trace-chain-verify.md](./trace-chain-verify.md)）。
      外部 HTTP 依赖 span 由 `pkg/httpclient` 埋点提供，本轮请求未触发外部调用，
      该分支在 staging 待补一次真实调用。

**门禁**：新增 `make check-alerts`（ALERTS ↔ 规则 ↔ runbook 三方一致）。

---

### ENT-07 密钥管理与轮换

**现状证据**

- 配置仅支持 `configs/*.yaml` + `ADMIN_FRAME_*` 环境变量；`internal/platform/config.go` 已做脱敏，但无 KMS / Vault 适配。
- `internal/security` 支持多公钥 / 多密钥（`jwt_keys_test.go`），但无轮换流程、无 `kid` 选取策略文档。
- DB DSN、对象存储密钥、JWT 密钥均以静态配置注入；无动态凭据。

**缺口**

1. 无密钥托管（Vault / KMS / Secret Manager）适配层；本地 env 与生产托管未分离。
2. 无 JWT 签名密钥轮换（重叠期、`kid`、旧密钥退出）与应急吊销流程。
3. 无 DB / Redis / 对象存储凭据轮换；无密钥访问审计。
4. 无加密密钥层级（KEK / DEK）与 `pkg/crypto` 的落地用法。

**目标态**

- `internal/platform/secrets` 定义 `SecretProvider` 接口，支持 env / file / Vault / KMS；启动期拉取，失败拒绝启动。
- JWT 支持 `kid` + 多密钥重叠，轮换不停机；提供轮换 runbook 与演练。
- DB 使用短期凭据或至少可轮换账号；对象存储使用最小权限角色。
- 敏感字段加密使用 `pkg/crypto`，密钥经 KMS 包装。

**验收标准**

- [ ] `AC-ENT-07-1`：轮换 JWT 签名密钥期间请求不中断，旧令牌在重叠期内仍可用。
- [ ] `AC-ENT-07-2`：轮换 DB 凭据无需改镜像 / 重启全部副本（或明确滚动重启步骤）。
- [ ] `AC-ENT-07-3`：仓库、镜像、日志中无密钥；gitleaks 与镜像扫描通过。
- [ ] `AC-ENT-07-4`：轮换与吊销 runbook 完成一次演练。

**门禁**：`make secrets`、`make check-config`、发布 Checklist。

---

### ENT-08 备份、恢复与灾备演练

**现状证据**

- `DATA_STANDARDS.md` 要求生产库定期备份与恢复演练，但仓库无备份脚本、保留策略或恢复 runbook。
- `docs/runbooks/` 只有 API 5xx、DB 连接池、IAM、release rollback；无备份 / 恢复 / 灾备场景。
- 无 RPO / RTO 定义，无备份加密与异地保存说明。

**缺口**

1. 无自动备份（全量 + binlog / PITR）与保留策略。
2. 无恢复流程、校验与演练记录；无租户级误删恢复方案。
3. 无 RPO / RTO、无备份加密、无异地副本。
4. 对象存储 / Redis 的数据保护策略未定义。

**目标态**

- 数据库自动备份 + 时间点恢复；保留期与加密策略；备份失败告警。
- 恢复 runbook：全量恢复、PITR、单租户数据修复；每季度演练并记录。
- 明确 RPO / RTO 并与业务确认；对象存储版本化 / 跨区复制按需启用。

**验收标准**

- [ ] `AC-ENT-08-1`：staging 完成一次全量 + PITR 恢复，数据校验通过。
- [ ] `AC-ENT-08-2`：RPO / RTO 写入 runbook 并有告警覆盖备份失败。
- [ ] `AC-ENT-08-3`：备份加密、访问受控、保留期生效。
- [ ] `AC-ENT-08-4`：恢复演练记录归档，含耗时与问题清单。

**门禁**：`make check-runbooks` 扩展备份 / 恢复条目；季度演练记录。

---

## 五、P1 企业运营能力

> P1 目标：完成 E1 后可安全运营，但仍需这些能力才能支撑真实业务、客户与合规。
> 关闭标准：功能上线 + 自动化用例 + 观测指标 + 文档回写。

| ID | 能力缺口 | 现状证据 | 目标态 | 关键验收 |
|---|---|---|---|---|
| `ENT-09` | 租户模型与生命周期缺失 | `internal/shared/auth/principal.go:30-36` 只有 `MerchantID` 透传；无租户注册、状态、配额与退出机制 | 租户注册 / 审核 / 启停 / 套餐配额 / 退出；停用与 IAM、缓存、令牌联动 | 租户停用后 ≤ 60s 拒绝访问；退出后数据按策略处置；全链路审计 |
| `ENT-10` | 通知 / Webhook 只有渠道包，无投递编排 | `pkg/sms` / `email` / `push` / `webhook` 已完成；`internal/` 无引用；无模板、偏好、投递 Outbox、重试与状态 | 通知中心：模板、渠道偏好、去重、限流、重试、DLQ、投递状态查询 | 至少一次投递且幂等；失败可重试 / 补偿；投递指标可见 |
| `ENT-11` | 特性开关与灰度缺失 | `ARCHITECTURE.md` §17.3 要求 feature flag；全仓无实现，配置为静态 | Flag 服务（DB / 配置），支持 channel / tenant / 百分比灰度、审计与到期清理 | 灰度可秒级开关；关闭即回退；无长期遗留 flag |
| `ENT-12` | IAM 数据范围无业务侧消费契约 | IAM 已建模 `all` / `merchant` / `self` / `merchant_set`；`Principal` 无 scope 字段，未来业务域无统一接入方式 | 数据范围注入 context；平台提供查询 / 写入强制助手；A 端跨租户查询审计 | 未来业务域接入即有隔离；跨租户读写被拒绝；有越权自动化用例 |
| `ENT-13` | 外部依赖韧性与第三方集成框架 | `pkg/circuitbreaker` / `retry` / `httpclient` 已具备，但 `internal/` 无引用；无统一依赖注册与超时策略 | 依赖注册表（超时 / 重试 / 熔断 / 舱壁）；第三方回调验签 + 幂等 + 状态机模板 | 下游故障快速失败且可观测；重复回调不重复处理；依赖指标可见 |
| `ENT-14` | 分布式限流与租户配额 | `internal/platform/pipeline.go:101-121` 为进程内令牌桶；无套餐 / 配额管理 | 网关或 Redis 全局限流；按 channel / tenant / user / 接口分键；配额管理与 429 指标 | 多副本共享配额；超限统一 `42900`；配额变更可审计 |
| `ENT-15` | 数据生命周期与隐私合规 | 无 PII 分类、保留、导出、删除、同意记录；审计仅保留主体 ID | 数据分类分级、保留策略、DSAR 导出 / 删除、法务保留、脱敏与最小化 | 删除请求可验证执行；保留任务生效；导出经审批并审计 |
| `ENT-16` | 测试基础设施不完整 | `tests/` 仅 `tests/integration/iam/` 三个 MySQL 用例；无 `tests/e2e/`；`pkg/testkit` 待做（`pkg-roadmap.md:122`） | 集成矩阵（MySQL / Redis / MinIO）、三端 E2E、契约测试、负载测试基线 | CI 跑真实依赖集成；E2E 覆盖登录 → 核心写路径 → 查询；压测基线登记 |
| `ENT-17` | 发布自动化与迁移门禁不足 | 有镜像构建与 Trivy，无 CD、无金丝雀 / 蓝绿、无迁移 Job 编排 | staging → prod 审批发布；迁移前置检查；金丝雀指标；自动 / 一键回滚 | 发布可重复；迁移失败阻断；金丝雀异常自动回滚 |
| `ENT-18` | 值班与事故管理未闭环 | 4 个 runbook；无 on-call 轮值、分级矩阵、复盘模板、演练计划 | on-call 排班、P1/P2 分级与升级路径、事故沟通、Postmortem、Game Day | 一次演练完成；P1 告警 5 分钟内可触达；复盘行动项跟踪 |

---

## 六、P2 规模化与合规能力

> P2 目标：规模化、合规与长期治理；在没有明确业务量或合规要求前**不应提前投入**。

| ID | 能力 | 缺失说明 | 启动条件 |
|---|---|---|---|
| `ENT-19` | 供应链强化 | 仅有 Trivy 扫描；无 SBOM、镜像签名 / 来源证明、DAST、依赖更新 SLA 落地 | 对外发布或客户安全评审要求 |
| `ENT-20` | 边缘与传输安全 | 无 WAF / API Gateway 配置、无 mTLS / 服务身份、无 HSTS 等安全头基线、C 端无 Bot 防护 | 公网暴露或等保 / 渗透测试要求 |
| `ENT-21` | 治理与文档运营 | 分支保护仍待管理员勾选（`standards-roadmap.md:68`）；无 CODEOWNERS 落地、无文档站点 | 团队规模扩大或多人协作 |
| `ENT-22` | 容量与成本治理 | 只有微基准（`docs/perf/BASELINES.md`），无负载模型、容量评估、成本标签与预算告警 | 进入放量阶段 |
| `ENT-23` | 多区域 / 读副本 / 多活 | 无只读副本、跨区备份、故障切换方案 | 可用性目标提升或多区域合规 |
| `ENT-24` | 搜索 / 报表 / 数仓 | 无 CDC / ETL、读模型、导出任务、BI 对接 | 运营分析或对账需求 |
| `ENT-25` | ABAC / 组织架构 | v1 明确不做；无部门树、角色继承、条件策略 | 权限复杂度或组织架构诉求明确后另立 ADR |

---

## 七、里程碑与依赖

### 7.1 里程碑

| 里程碑 | 范围 | 出口标准 | 目标等级 |
|---|---|---|---|
| **E1 可生产灰度** | `ENT-01` ～ `ENT-08` | staging 多副本可运行；真实身份、异步底座、审计、备份、告警、部署全部闭环；`make ci` + 新增门禁全绿 | L2 |
| **E2 可企业运营** | `ENT-09` ～ `ENT-18` | 租户 / 通知 / 开关 / 数据范围 / 测试 / 发布 / 值班闭环；可承接真实客户 | L2+ |
| **E3 规模化与合规** | `ENT-19` ～ `ENT-25` | 供应链、边缘安全、容量、多区域、数仓按需落地；有 SLO 与演练记录 | L3 |

### 7.2 依赖关系

```text
ENT-02 多实例语义 ──┬──► ENT-03 异步底座 ──► ENT-10 通知投递
                    ├──► ENT-14 分布式限流 / 配额
                    └──► ENT-17 发布自动化
ENT-01 身份闭环 ────┬──► ENT-09 租户生命周期
                    └──► ENT-12 数据范围契约
ENT-05 部署制品 ────┬──► ENT-06 告警 / SLO
                    ├──► ENT-07 密钥托管
                    └──► ENT-08 备份 / 灾备
ENT-16 测试基础设施 ─► 所有 P0 的验收自动化
```

### 7.3 明确不做（避免过度设计）

- 不因「企业级」而立即拆微服务、上服务网格或多活；
- 不在低流量阶段引入 Kafka；先以 DB Outbox 轮询满足可靠性，容量触发后再换；
- 不把 IAM 扩成完整 IdP；登录 / MFA / SSO 放身份域，优先复用组织 OIDC；
- 不在 v1 引入 ABAC 表达式引擎；
- 不为每个能力单独建可执行文件，仍按模块化单体演进。

---

## 八、门禁与规范回写

### 8.1 新增门禁建议

| 门禁 | 覆盖条目 | 检查内容 | 失败即阻断 |
|---|---|---|---|
| `make check-deploy` | ENT-05 | 清单存在、探针、资源、安全上下文、无明文密钥、镜像 digest | 是 |
| `make check-alerts` | ENT-06 | `ALERTS.md` ↔ 规则文件 ↔ runbook 三方一致 | 是 |
| `make check-authn` | ENT-01 | 生产配置禁止 demo；认证端点契约存在；撤销路径有测试 | 是 |
| `make check-idempotency` | ENT-02 | 写接口必须挂幂等中间件或声明唯一键 | 是 |
| `make check-audit-store` | ENT-04 | 关键事件有存储实现与查询契约，非仅日志 | 是 |
| `make check-backup` | ENT-08 | 备份配置 / runbook / 演练记录模板存在 | 是 |
| `make check-e2e` | ENT-16 | 三端 E2E 存在且可运行（可 nightly） | 否（nightly） |
| `make check-sbom` | ENT-19 | 镜像 SBOM / 签名产物 | 否（P2） |

### 8.2 文档回写矩阵

| 条目 | 需要更新的文档 / 清单 |
|---|---|
| ENT-01 | `SECURITY_STANDARDS.md` §10.1、`API_STANDARDS.md`、`CONFIG_STANDARDS.md`、三端 OpenAPI |
| ENT-02 | `ARCHITECTURE.md` §11 / §18、`CONFIG_STANDARDS.md`、部署清单 |
| ENT-03 | `RELIABILITY_STANDARDS.md`、`ARCHITECTURE.md` §15、`docs/audit/EVENTS.md`、`pkg-roadmap.md` |
| ENT-04 | `AUDIT_STANDARDS.md`、`docs/audit/FIELDS.md`、`docs/audit/EVENTS.md` |
| ENT-05 | `RELEASE_STANDARDS.md`、`deploy/README.md`、`CI_STANDARDS.md` |
| ENT-06 | `OBSERVABILITY_STANDARDS.md`、`RUNBOOK_STANDARDS.md`、`docs/runbooks/ALERTS.md` |
| ENT-07 | `SECURITY_STANDARDS.md` §10.3、`CONFIG_STANDARDS.md`、新增 runbook |
| ENT-08 | `DATA_STANDARDS.md` DATA-07、`docs/runbooks/` 新增恢复演练 |
| ENT-09 ～ ENT-18 | 对应域文档、三端 OpenAPI、`TESTING_STANDARDS.md`、`PERFORMANCE_STANDARDS.md` |
| 全部门禁 | `Makefile`、`.github/workflows/ci.yml`、`.workflow/*.sh`、PR 模板 |

---

## 九、风险与实施原则

| 风险 | 影响 | 缓解 |
|---|---|---|
| 一次性大改 | 长期不可发布、回归面失控 | 每条 ENT 独立 MR；P0 内按依赖顺序小步合入；保留开关 |
| 引入 MQ 过早 | 运维复杂度高于收益 | 先 DB Outbox 轮询；用积压 / 延迟指标触发升级 |
| 自建 IdP 范围失控 | 安全责任与工期膨胀 | 复用组织 OIDC；只实现账号映射与会话策略 |
| 审计存储成本 | 存储与查询成本上升 | 分级保留、冷热分层、归档对象存储 |
| 密钥托管强依赖 | 本地开发受阻 | `SecretProvider` 接口 + env/file 本地实现，生产强制 Vault/KMS |
| 部署清单与代码漂移 | 上线配置错误 | 清单入库 + `check-deploy` + staging 每次发布验证 |
| 测试补齐全量铺开 | 拖慢交付 | 先覆盖登录、核心写路径、越权、Outbox、恢复五条主链路 |

**实施原则**

1. **先闭环，后优化**：每条能力先达到「可运行 + 可观测 + 可回滚」，再做性能与体验。
2. **失败默认安全**：认证、审计、密钥、迁移失败一律拒绝，不静默降级。
3. **可验证**：每条验收标准必须能落到自动化用例或演练记录。
4. **不重复建设**：优先复用 `pkg/*` 与 IAM 已验证的模式（Outbox、UnitOfWork、缓存）。
5. **文档同步**：关闭条目的同一 MR 回写标准文档与门禁。

---

## 十、状态跟踪

> 状态：`待做` / `进行中` / `已完成` / `暂缓`。完成须附验收证据链接或命令输出。

> 状态以 [`production-readiness-implementation-plan.md`](./production-readiness-implementation-plan.md) 的实施状态列为准；本表保留缺口原始描述，状态列随关闭动作更新。

| ID | 优先级 | 状态 | 关闭判据（摘要） |
|---|---|---|---|
| ENT-01 | P0 | 已完成（2026-10-23，阶段 1–5） | 登录 / 刷新 / 登出 / 撤销 / MFA 可用，禁用 ≤ 60s 生效；详见 [`identity-authn.md`](./identity-authn.md) |
| ENT-02 | P0 | 已完成（2026-10-22） | 多副本 ID 唯一、幂等与限流共享；staging 实测随 ENT-05 补 |
| ENT-03 | P0 | 已完成（2026-10-22） | 通用 Outbox + 消费运行时 + DLQ；broker 适配按 T7 预留接口，容量触发再评估 |
| ENT-04 | P0 | 完成（2026-11-03） | 审计查询 / 导出 / 保留 ≥ 180 天；`AC-ENT-04-1`～`4` 达成 |
| ENT-05 | P0 | 完成（2026-11-02） | Helm 三套 values + 探针 / 资源 / Secret 注入，kind 真集群验证 |
| ENT-06 | P0 | 完成（2026-11-03） | 规则 / 看板 / SLO 代码化；`AC-ENT-06-1`～`4` 达成 |
| ENT-07 | P0 | 完成（2026-11-03） | Vault HTTP API 托管 + 不停机轮换演练 |
| ENT-08 | P0 | 完成（2026-11-02） | 备份 + PITR（字节精确/秒级截断）实测；生产剩余为配置项 |
| ENT-09 | P1 | 主体完成（2026-11-03），验收缺口未关闭 | 租户四层 + 三表 + 状态机 + 11 个 A 端端点；B 端停用生效 |
| ENT-10 | P1 | 完成（2026-11-04） | NT-01～NT-10：五层 + 五表 + 编排 / Replay / 回执回调 |
| ENT-11 | P1 | 完成（2026-11-04） | FF-01～FF-08：评估引擎 + 稳定分桶 + 两级缓存 + 治理门禁 |
| ENT-12 | P1 | 完成（2026-11-04） | `ScopeWhere` / `Enforce*` 原语 + 三域接线，越界统一 40300 |
| ENT-13 | P1 | 完成（2026-11-04） | 依赖注册表 + `Caller.Do`（超时/熔断/重试）+ 回调三件套 |
| ENT-14 | P1 | 完成（2026-11-04） | 配额执行器 + Redis 分键令牌桶 + 统一 42900 |
| ENT-15 | P1 | 主体完成（2026-11-04） | Purge 五步证据链 + 导出证据表 + PII 登记 |
| ENT-16 | P1 | 完成（2026-11-04） | 集成矩阵 + 三端 E2E + 契约测试 + 压测基线登记 |
| ENT-17 | P1 | 完成（2026-11-04） | `release.yml` 八阶段 + 迁移门禁 + 金丝雀指标门禁 + 自动回滚 |
| ENT-18 | P1 | 完成（2026-11-05）；staging 实战演练挂行动项 | 分级矩阵 + 响应流程 + 复盘模板 + 行动项台账 + 排班交接 + Game Day（tabletop） |
| ENT-19 | P2 | 待做 | SBOM / 签名 / DAST |
| ENT-20 | P2 | 待做 | WAF / 网关 / mTLS / 安全头 |
| ENT-21 | P2 | 待做 | CODEOWNERS + 分支保护 + 文档站点 |
| ENT-22 | P2 | 待做 | 容量模型 + 成本治理 |
| ENT-23 | P2 | 待做 | 读副本 / 多区域 / 切换演练 |
| ENT-24 | P2 | 待做 | CDC / 数仓 / 报表 |
| ENT-25 | P2 | 暂缓 | 先立 ADR，再评估 ABAC / 组织树 |

### 执行记录

| 日期 | 事项 | 状态 | 备注 |
|---|---|---|---|
| 2026-10-22 | v0.2 完成平台级能力差距评估 | 完成 | 基线 `dad8fc0`；识别 P0 8 项、P1 10 项、P2 7 项 |
| — | 后续条目关闭时在此追加 | — | 须附验收命令与输出 |

---

## 附录

### 附录 A：核对命令

```bash
# 基线
git rev-parse --short HEAD
git status --short

# 身份闭环是否存在
rg -ni "func .*(Login|Logout|Refresh|Revoke)" internal pkg

# 多实例固定节点号
rg -n "NodeID:\s*1" internal cmd

# 进程内限流与幂等装配
rg -n "newRateLimit|idempotency" internal/platform

# 事件总线与 worker
rg -n "NewMemoryBus|Drain\(" cmd internal

# 生产级 broker 是否存在
rg -n "kafka|nats|rabbit" pkg internal go.mod

# 审计出口与查询
rg -n "SlogEmitter|audit:read" internal

# 部署与告警制品
ls deploy .github/workflows docs/runbooks

# 测试矩阵
find tests -type f

# 门禁
make ci
```

### 附录 B：术语

| 术语 | 含义 |
|---|---|
| P0 / P1 / P2 | 上线阻断 / 上线后 90 天 / 规模化阶段 |
| E1 / E2 / E3 | 可生产灰度 / 可企业运营 / 规模化与合规里程碑 |
| Outbox | 业务事务内写事件表，异步可靠投递 |
| PITR | Point-In-Time Recovery，时间点恢复 |
| SLO / 错误预算 | 服务等级目标与可消耗的失败额度 |
| DSAR | 数据主体访问 / 删除请求 |
| DLQ | 死信队列 |
| SBOM | 软件物料清单 |
| RPO / RTO | 恢复点目标 / 恢复时间目标 |

### 附录 C：与现有路线图的关系

| 文档 | 关系 |
|---|---|
| `standards-roadmap.md` | 工程硬约束专文已完成；本文补「平台运行时能力」缺口 |
| `platform-remediation-plan.md` | PLAT-01～15 已完成；本文在其之上评估生产闭环 |
| `rbac-iam.md` | IAM M0～M4.7 已完成、M5 进行中；本文不重复 IAM 内部任务，只评估其对外依赖与运维闭环 |
| `pkg-roadmap.md` | `pkg` 原语大多完成；本文关注「原语是否真正接线到平台运行时与运维」 |
| 各 `*_STANDARDS.md` | 条目关闭后回写为硬约束与门禁，本文不替代标准正文 |
