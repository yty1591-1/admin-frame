# ENT-10 通知中心（投递编排）领域规划

> **状态**：规划 v1.0（待评审）
> **关联**：[`production-readiness-implementation-plan.md`](./production-readiness-implementation-plan.md)（ENT-10）、[`notification-channels.md`](./notification-channels.md)（渠道驱动，M4 即本文）、[`RELIABILITY_STANDARDS.md`](./RELIABILITY_STANDARDS.md)、[`SECURITY_STANDARDS.md`](./SECURITY_STANDARDS.md)、[`OBSERVABILITY_STANDARDS.md`](./OBSERVABILITY_STANDARDS.md)、[`tenant-enterprise-review.md`](./tenant-enterprise-review.md)（T-14 邀请邮件等待接入）
> **规划日期**：2026-11-04
> **目标读者**：后端 / 平台装配 / 安全 Review / SRE

---

## 0. 结论摘要（TL;DR）

1. **定位**：`internal/notification` 是**通知投递编排层**——模板、偏好、去重、频控、路由、重试、DLQ、状态查询；渠道实现复用已就绪的 `pkg/sms` / `pkg/email` / `pkg/push` / `pkg/webhook`，可靠性底座复用 ENT-03 的 `pkg/outbox`（relay / 租约 / 退避 / DLQ），本域**零重复造轮子**。
2. **铁律（规划 5.1.3）**：业务域只写 Outbox 事件，通知中心订阅消费；**禁止**在业务事务里同步调用外部渠道。验证码是唯一例外（同步强交互场景），走受控旁路并收敛频控。
3. **投递语义**：至少一次 + 消费端幂等（IdempotencyKey 去重）；失败按 `ProviderError.Retryable()` 分类——可重试走退避重试，重试耗尽进 DLQ（可人工重放补偿）；永久失败直接终态并告警。
4. **编排管道固定顺序**：去重 → 偏好/退订 → 频控 → 熔断包住重试 → 渠道发送 → 状态回写。熔断包住重试（供应商整体不可用时快速失败），驱动内绝不重试。
5. **验收对齐**：至少一次且幂等 ✅（Outbox + 去重表）；失败可重试/补偿 ✅（DLQ + 重放 API）；投递指标可见 ✅（`notify_*` 指标族 + 告警 + runbook）。
6. **分期**：NT-01～NT-10 共 10 个任务包，从域模型到首个真实调用方（T-14 邀请邮件）端到端闭环；配额执行归 ENT-14、数据生命周期归 ENT-15，本期只留接口。

---

## 1. 现状与差距

### 1.1 已就绪资产（本域复用，不重建）

| 资产 | 位置 | 本域用法 |
|------|------|----------|
| 渠道契约与驱动 | `pkg/sms`（aliyun/memory）、`pkg/email`（smtp/memory）、`pkg/push`（apns/fcm/memory） | `Send(ctx, Message) (Receipt, error)`；`ProviderError` 已带语义码 + `Retryable()` |
| Webhook | `pkg/webhook`（签名/验签/nonce/secret） | 回执回调验签、B 端订阅推送 |
| 异步底座 | `pkg/outbox`（store/relay/router/dedupe/DLQ）、`pkg/mq`（envelope） | 通知事件的事务性落库、消费认领、退避重试、死信 |
| 韧性原语 | `pkg/retry`、`pkg/circuitbreaker`、`pkg/ratelimit` | 编排管道的三个横切层 |
| 观测与安全 | `pkg/metrics`、`pkg/mask`、`pkg/crypto`、`pkg/idempotency` | 指标、PII 脱敏、频控键 HMAC、幂等键 |
| 域工程范式 | `internal/tenant`（domain/app/infrastructure/observability/transport 五层 + 集成测试 + CI 门禁） | 直接对齐 |

### 1.2 缺口（本域要交付的）

| # | 缺口 | 现状证据 |
|---|------|----------|
| 1 | `internal/` 无任何对渠道包的编排引用 | 仅 `internal/identity/infrastructure/sms/code_sender.go` 直连 `pkg/sms`（验证码） |
| 2 | 无模板、偏好、退订、黑名单 | `internal/` 无对应表与代码 |
| 3 | 无通知事件契约，业务域无法「只写 Outbox」 | 事件 schema 未定义 |
| 4 | 无投递记录与状态查询 | 无表、无 API |
| 5 | T-14 邀请邮件「只落库不发送」等待本域 | `internal/tenant/app/invitation.go` 阶段语义注释 |
| 6 | 租户停用/终止前客户通知无载体 | `tenant-enterprise-review.md` 通知客户 = 无（ENT-10） |
| 7 | 验证码频控散落在 identity app 层 | M2 遗留「M4 收敛」项 |

### 1.3 依赖与边界

- **依赖 ENT-03**（已完成）：`pkg/outbox` 多实例安全、租约回收、DLQ 已验收。
- **非阻塞联动**：ENT-14（分布式限流/配额）就绪后，把进程内 `pkg/ratelimit` 换成 Redis 分键实现，接口不变；ENT-15 就绪后，发送记录 90 天保留由生命周期任务执行，本域只提供清理 SQL 与保留声明。
- **明确不做**（对齐 notification-channels.md §2.2）：模板可视化编辑、营销自动化、A/B 实验、收件箱解析、厂商账单对账、跨渠道统一收件箱。

---

## 2. 领域能力清单（本模块应该具备什么）

### A. 通知单域模型（Notification Request）

一次「要发通知」的领域表达，与渠道、厂商解耦：

- **寻址三元组**：`(recipient, scene, channels)`——收件人（user_id / email / phone / 订阅端点）、业务场景键（`tenant.invitation`、`tenant.suspend_notice`、`auth.verify_code`…）、期望渠道（email/sms/push/inbox，含顺序偏好）。
- **内容声明**：`template_key + locale + params`（模板优先）；**禁止裸内容**进入编排层（D5），渲染后的成品只存在于投递瞬时内存，**正文不落库**。
- **上下文**：`tenant_id`（强制隔离键）、`actor`（触发主体）、`reference`（业务引用，低基数非 PII）、`idempotency_key`（`notify:<scene>:<biz_id>`，≤128B）。
- **优先级与时效**：`priority`（critical/normal/low）影响重试预算与频控豁免；`expires_at` 过期即弃发（验证码类必须有过期）。

### B. 模板与渲染

- 模板按 `(key, channel, locale)` 唯一；内容含渠道载体（邮件主题/正文、短信模板 ID+参数、Push 标题/正文）。
- 变量白名单声明 + 渲染期校验（缺失/多余参数拒绝）；短信参数沿用 `pkg/sms` 的 CR/LF 禁令。
- 模板有 `enabled` 与审计动作（创建/更新/停用均入审计）；渲染用注入时钟与显式 HTML 转义约定（`html/template`，调用方模板层负责转义用户输入）。
- v1 模板存库 + 版本字段（`version` 递增，渲染锁定单版本），不做可视化编辑。

### C. 收件人解析与偏好

- **地址解析**：user_id → 各渠道地址（email/phone/设备 token/站内信 inbox），地址来源与加密存储对接 identity/设备域；解析不到目标渠道地址 → 终态 `unreachable`，不重试。
- **偏好（preference）**：用户/租户级渠道开关矩阵（scene 分类 × channel）；`opt_out` 持久化（邮件 `List-Unsubscribe` 落点）。
- **黑名单**：租户级 + 平台级（投诉号码、退信硬跳地址）；命中即终态 `rejected`，审计留痕。
- **静默时段**（v1 可选配置）：营销类场景的发送时间窗，窗口外顺延不丢弃。

### D. 渠道路由与主备

- **场景 → 渠道映射**：配置驱动（YAML 装配 + 库内路由表），支持按租户覆盖（多租户独立通道账号留到开放问题 #7）。
- **主备 failover**：仅对 `Retryable()` 且渠道级熔断 Open 的错误切换备用供应商；永久错误不切换；切换必须打点 `degraded=true`（REL-06 禁止静默降级）。
- v1 只做「渠道内主备」（如 SMTP 主 + SES 备），跨渠道降级链（邮件失败转短信）列为非目标。

### E. 投递编排管道（核心）

```text
Outbox 事件 (notification.requested)
  └─► 消费 Handler（幂等键 = 事件 ID）
       1. 去重：IdempotencyKey 已投递成功 → 直接确认（at-least-once 吸收）
       2. 偏好/黑名单/退订 → 命中则终态 rejected（审计）
       3. 渲染模板 → 消息体（瞬时内存）
       4. 频控：pkg/ratelimit（供应商 QPS + 收件人 HMAC 键频控；429 语义）
       5. circuitbreaker.Do → retry.Do → driver.Send（单次尝试）
       6. 回写投递记录：accepted / failed(+语义码, retry_after)
       └─ 失败：Retryable → 交还 outbox 退避重试；重试耗尽 → DLQ
              永久失败 → 终态 failed + 告警（人工补偿走重放）
```

- **熔断包住重试**（notification-channels.md §7.1），每供应商一个熔断器，状态接 `notify_breaker_state` Gauge。
- 通知中心是**尽力而为**能力：渠道不可用不得导致 `/readyz` 失败或阻塞业务事务（OBS-03）。

### F. 可靠性语义

| 语义 | 实现 |
|------|------|
| 至少一次 | 业务 Outbox 事务落库 + relay 认领（租约/条件更新） |
| 幂等 | 投递记录表 `uk(idempotency_key, channel)` 唯一键 + 消费前查询短路 |
| 失败可重试 | `ProviderError.Retryable()` 分类；outbox 指数退避；`RetryAfter` 尊重 |
| 补偿 | DLQ 查询 API + 人工重放（重放走新事件、保留原幂等键 → 天然防重复） |
| 失败可判定 | 每条失败记录含语义码、供应商原始码（仅排障字段）、重试计数 |

### G. 投递状态机与状态查询

**通知单（notification_request）状态机**：

```text
pending ──► dispatched ──► sent ──► delivered（回执）
   │            │            └──► undelivered（回执失败/超时，按需）
   │            └──► failed（永久错误）
   └──► rejected（偏好/黑名单/过期/不可达）
```

- 迁移用 CAS（`expected_version`）条件更新，对齐 T-15 的条件更新惯例；状态矩阵用快照测试硬编码（对齐 check-tenant 模式）。
- **查询 API**：A 端 `GET /admin/v1/notifications`（按租户/场景/状态过滤）+ `GET /admin/v1/notifications/{id}/deliveries`（逐渠道投递明细）；B 端 `GET /biz/v1/notifications`（本租户，强制 tenant_id 过滤）。
- DLQ 运维 API（A 端）：列表 + 重放；重放是写操作，入审计。

### H. 回执与送达状态（M5 语义，本期接口预留）

- 渠道回执回调端点：`pkg/webhook` 验签 + nonce 防重放 + 幂等更新（三件套）；回执只更新状态，不重新触发投递。
- v1 先落回调端点骨架与验签（阿里云短信回执、SMTP 无回执），SMTP 渠道 `sent` 即终态。
- Push Token 失效（`ErrTokenInvalid`）→ 通知中心负责停用清理 + `notify_token_invalid_total` 指标。

### I. 多租户隔离与配额

- 全部表强制 `tenant_id`（平台级通知 tenant_id = 平台哨兵值），仓储层注入，对齐 5.1 约束 1。
- 配额接口预留：`QuotaChecker` 端口（scene × tenant 维度），v1 实现为进程内 `pkg/ratelimit`，ENT-14 就绪后换 Redis 实现——与 T-13 席位配额同模式。

### J. 同步旁路：验证码收敛（M2 遗留）

- identity 验证码保持**同步**路径（登录强交互，不走 Outbox），但频控、审计、指标从 identity app 层收敛到通知中心提供的 `DirectSender` 受控门面（同步语义 + 频控 + 脱敏 + 指标，**无**模板/偏好/DLQ）。
- 明确注释例外原因与禁令：仅验证码类场景允许；业务通知禁止走旁路。

### K. 安全与合规

| 风险 | 控制 |
|------|------|
| PII 入日志/Trace/指标 | 收件人经 `pkg/mask`；频控键 HMAC 带盐；正文/验证码/Token 永不落日志；指标标签只允许低基数枚举 |
| 密钥 | 只经 env/KMS 注入渠道驱动（CFG-06）；通知中心自身不接触凭据 |
| 短信轰炸/枚举 | 人机校验在入口；频控 1/min、5/h、10/day（手机号）+ IP 维度；统一 42900 文案 |
| 邮件伪造/退订 | From 域校验在驱动；`List-Unsubscribe` 头 + opt_out 落库 |
| 越权 | B 端查询强制 CurrentTenant 过滤；A 端跨租户查询入审计（对齐 ENT-12 方向） |
| 审计 | 模板变更、黑名单变更、DLQ 重放、偏好变更、A 端代发全部入 `EVENTS.md` |
| 数据保留 | 发送记录（不含正文）保留 90 天，归口 ENT-15；正文只存模板引用与参数摘要 |

### L. 可观测性与运维

- 指标：`notify_send_total{channel,provider,result,code}`、`notify_send_duration_seconds`、`notify_retry_total`、`notify_rate_limited_total`、`notify_breaker_state`、`notify_dlq_size`（Gauge）、`notify_request_total{scene,status}`——全部低基数。
- 告警：成功率 <95%/5min（P1）、熔断 Open 持续 1min（P1）、认证失败连续 3 次（P1）、DLQ 有积压（P2）、限流突增 5x（P2）。
- 文档：`docs/runbooks/notification-delivery.md`（症状/影响/排查/缓解/升级六章节）+ `docs/notification-center.md` 架构文档。
- CI 门禁：`scripts/ci/check-notification.sh`——审计动作 ↔ EVENTS.md 对账、路由 ↔ OpenAPI 双向对账、状态机矩阵快照、（若有）模板键 ↔ 代码引用对账，入 `make ci`。

### M. 业务域集成契约（面向未来调用方）

- **事件契约**：`notification.requested` 信封（pkg/mq Envelope）载荷 `{request_id, tenant_id, scene, recipient_ref, channels, template_key, locale, params, idempotency_key, priority, expires_at}`；schema 版本化，破变更大版本。
- **两种接入方式**：① 业务事务内写 Outbox 事件（推荐，保证「库已写、通知必达」）；② A 端运营代发 API（`POST /admin/v1/notifications`，审计触发点）。
- 首个调用方：**T-14 邀请邮件**（tenant 域在邀请创建/重发事务内写 `notification.requested`，替换「线下转交」阶段语义）+ **租户停用/终止前通知**（ENT-09 遗留通知项）。

---

## 3. 数据模型（迁移 000016 起）

| 表 | 关键列 | 说明 |
|----|--------|------|
| `notification_request` | id、tenant_id、scene、recipient_ref、channels、template_key、locale、params_digest、idempotency_key（uk）、priority、expires_at、status、version、created_at | 通知单；正文不落库 |
| `notification_delivery` | id、request_id、tenant_id、channel、provider、status、attempt、semantic_code、provider_code、message_id、next_retry_at、finished_at | 逐渠道投递；`uk(request_id, channel)` |
| `notification_template` | key、channel、locale、version、content_json、enabled、created_by | 模板；`uk(key, channel, locale, version)` |
| `notification_preference` | tenant_id、subject_type、subject_id、scene_class、channel、enabled | 偏好矩阵；唯一键约束 |
| `notification_blacklist` | tenant_id、channel、target_hmac、reason、source | 黑名单；target 存 HMAC 不存明文 |
| 复用 | `async_outbox`、`async_dlq` | ENT-03 已有，不改 |

- 全部含 `created_at/updated_at`；`notification_delivery.attempt` 与 outbox 重试计数分离（一个事件可产生多次渠道尝试）。
- 迁移命名遵循 `check-migrations.sh` 门禁；幂等回填不做（全新表）。

## 4. 代码布局（对齐 tenant 五层范式）

```text
internal/notification/
├── domain/         # Notification、Delivery、Template、Preference、状态机 Transitions()、错误、仓储接口
├── app/            # Orchestrator（消费 Handler）、QueryService、TemplateService、PreferenceService、
│                   # ReplayService、DirectSender（验证码旁路门面）、Observer 端口、QuotaChecker 端口
├── infrastructure/ # MySQL 持久化、渠道路由器（sms/email/push Sender 装配）、频控/熔断组装
├── observability/  # metrics.go（notify_* 指标族）
└── transport/
    ├── rest/       # B 端查询
    └── admin/      # A 端查询 / DLQ 重放 / 代发 / 模板管理
cmd/notificationruntime/runtime.go   # 装配（复用 iamruntime 式 BuildRepositories 模式）
```

- 分层依赖：`app` 只依赖 `domain` + 端口；渠道契约（pkg/sms 等）只出现在 `infrastructure`（对齐 check-layering）。
- 消费 runtime 复用 `pkg/outbox` 的 Router/Handler 注册模式（对齐 audit 域既有用法）。

## 5. 任务拆解（NT-01 ～ NT-10）

| ID | 任务包 | 关键产出 | 验收要点 |
|----|--------|----------|----------|
| NT-01 | 域模型与迁移 | 迁移 000016（四张新表）；domain 层模型 + 状态机 + Transitions() 快照测试 + memory 仓储 | 状态机矩阵测试；check-migrations 过 |
| NT-02 | 模板与渲染 | TemplateService（CRUD + 审计）；渲染校验；模板 seed（邀请邮件 2 个模板） | 缺参/裸内容拒绝；审计动作入 EVENTS.md |
| NT-03 | 偏好 / 黑名单 / 退订 | PreferenceService、BlacklistService、Unsubscribe 端点（List-Unsubscribe 落点） | 命中即 rejected 且审计；target 仅 HMAC |
| NT-04 | 编排管道与渠道路由 | Orchestrator 管道（去重→偏好→渲染→频控→熔断→retry→发送→回写）；渠道路由器 + 主备 | 管道单测逐层拦截可测；降级打 degraded |
| NT-05 | 事件契约与消费 runtime | `notification.requested` schema；outbox Handler 注册与装配（cmd/notificationruntime） | at-least-once 消费幂等；重复事件不重复发送（集成验证） |
| NT-06 | API 面 | admin.yaml 8.0.0：查询/DLQ 重放/代发/模板管理；biz.yaml：本租户查询；B 端 CurrentTenant 强制 | check-openapi 双向对账；越权用例 |
| NT-07 | 回执回调 | 回调端点骨架 + webhook 验签三件套；Token 失效清理钩子 | 验签/nonce/幂等用例；伪造回执被拒 |
| NT-08 | 可观测性与运维 | observability 指标族；告警规则；runbook；check-notification.sh 门禁入 make ci | 指标标签低基数检查；告警对账 |
| NT-09 | 首批调用方接入 | tenant 邀请邮件（替换线下转交）；租户停用前通知；MySQL 集成用例 | 端到端：业务事务→Outbox→投递→状态可查 |
| NT-10 | 验证码旁路收敛 + 收尾 | identity 接 DirectSender；生命周期/配额端口预留；ADR-0008（事件驱动 vs 同步）；review 文档 | 全仓 CI 绿；覆盖率对齐 tenant 基线（app ≥80%） |

**顺序与依赖**：NT-01 → 02/03（可并行）→ 04/05 → 06/07 → 08/09 → 10。NT-05 依赖 NT-01 事件表；NT-09 是端到端验收锚点。

## 6. 整体验收标准（ENT-10 关闭条件）

- [ ] 至少一次：业务事务写入后，进程崩溃/重启不丢通知（集成用例模拟 relay 中断）。
- [ ] 幂等：同 IdempotencyKey 重复事件、重放、双副本并发消费均只投递一次（MySQL 集成用例）。
- [ ] 可重试/补偿：可重试错误按退避重试并尊重 RetryAfter；重试耗尽进 DLQ；DLQ 重放 API 闭环且入审计。
- [ ] 指标可见：notify_* 指标在 /metrics 可查；成功率/熔断/DLQ 告警注册。
- [ ] 铁律验证：无任何业务域在事务内同步调用渠道包（check-notification.sh 静态扫描 `pkg/sms|email|push` 的 internal 引用面，白名单仅 identity 旁路 + notification infrastructure）。
- [ ] PII：错误消息/日志/指标无收件人、正文、验证码（脱敏测试）。
- [ ] T-14 邀请邮件端到端可发送（memory/沙箱驱动），openapi 中「线下转交」阶段语义更新。

## 7. 风险与开放问题

| # | 风险/问题 | 建议默认 |
|---|-----------|----------|
| 1 | 渠道凭据缺失，无法真实验证 | CI 用 memory/httptest 驱动；沙箱凭据 nightly（TEST-05） |
| 2 | 站内信（inbox）渠道 v1 是否需要 | 不做；channels 枚举预留 `inbox`，有 C 端需求再开 |
| 3 | 多租户独立通道账号（问题 #7 遗留） | v1 单账号 + 路由表按租户覆盖留接口 |
| 4 | 静默时段复杂度 | v1 仅营销类场景 + 单一时区配置 |
| 5 | 回执渠道覆盖面 | SMTP 无回执，sent 即终态并写明语义；阿里云短信回执先接 |
| 6 | 事件 schema 演进 | 载荷带 `schema_version`；只加字段不破变更（破变更升 major） |
| 7 | 与 ENT-14 的边界 | 本域定义 QuotaChecker 端口与键规范，ENT-14 提供实现 |
