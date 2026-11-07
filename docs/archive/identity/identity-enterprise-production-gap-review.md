# 身份域企业级 / 生产级对标差距分析报告

> 评估对象：`internal/identity`（app / domain / infrastructure / transport / observability / 运行时装配）
>
> 评估日期：2026-10-31
>
> 评估方法：只读代码审查 + 与 `docs/*_STANDARDS.md`、`docs/identity-authn.md`、`docs/identity-slo.md` 逐项对照。
> 性能与可靠性结论基于代码路径、事务边界、配置默认值与容量公式，**未在 staging 执行压测与故障注入**；涉及实测的结论已标注“待实测”。
>
> 基线说明：本报告不重复 `docs/identity-p0-p1-fix-log.md` 已关闭的问题，只评估**当前代码状态**下仍未达标或需正式风险接受的能力。
>
> 状态更新（2026-10-31）：§8.1 批次（REL-01、REL-02、OPS-02、PERF-01、PERF-02、PERF-03）已完成修复，详见 [identity-p1-batch-8-1-completion-report.md](./identity-p1-batch-8-1-completion-report.md)。
> 状态更新（2026-11-01）：§8.2（SEC-01/02/05/07）与 §8.3（P2 24 项）已闭环，§8.4 四项已专项修复，详见
> [identity-p1-batch-8-2-completion-report.md](./identity-p1-batch-8-2-completion-report.md)、
> [identity-p2-batch-8-3-remaining-completion-report.md](./identity-p2-batch-8-3-remaining-completion-report.md)、
> [identity-batch-8-4-completion-report.md](./identity-batch-8-4-completion-report.md)。本报告其余 P1/P2 结论保持不变。

---

## 0. 结论摘要

### 0.1 总体判断

核心认证链路（登录、刷新、登出、MFA、撤销、账号处置）已达到“可上线”基线：P0 问题已清零，事务内 Outbox、刷新重放检测、MFA 防重放、Redis 故障回源、只读权限拆分、健康检查与告警均已落地。

但距离“企业级生产无保留上线”仍有 **12 项 P1 级差距**，主要集中在上线阻断型契约缺口、可用性放大、事务内重 CPU/IO、审计合规存储和 PII 落库形态：

| 等级 | 定义 | 当前数量 |
|---|---|---|
| P0 | 会导致越权、数据泄露或核心链路必然不可用，禁止上线 | 0（历史项已关闭） |
| P1 | 上线前应完成，或必须走书面风险接受 | 12 |
| P2 | 可迭代改进，不阻断上线 | 36 |

### 0.2 P1 差距清单（需在上线评审中逐项关闭或签风险接受）

| ID | 维度 | 差距 | 影响 |
|---|---|---|---|
| REL-01 | 可靠性 | Redis 故障时 jti 黑名单校验绕过熔断，每个已认证请求仍等待 Redis 超时 | Redis 抖动会把鉴权链路放大为 3s/请求级联超时，最终全量 503 |
| REL-02 | 可靠性 | 清理任务无分布式互斥，生产 worker=2 副本并行执行 | 重复归档/重复删除、计数虚高、锁等待与无效 DB 压力 |
| PERF-01 | 性能 | 改密 / 重置密码 / 强制改密的 Argon2 在数据库事务内执行，且绕过并发槽位限制 | 长事务占用连接、Argon2 并发无上限，登录高峰易打满 CPU/内存 |
| PERF-02 | 性能 | 登录/刷新在数据库事务内写 Redis 会话快照 | Redis 3s 超时延长事务；事务回滚后残留“活跃会话”缓存 |
| PERF-03 | 性能 | 会话链 `ListByFamily` 无界加载整链历史，refresh 每次新增一行 | 单设备 30 天可积累数千行，撤销/登出路径时延随会话年龄线性增长 |
| SEC-01 | 安全 | PII 仍以明文落库：`identity_account.subject`、`identity_oauth_identity.provider_subject`（S4/S5 未发布） | 库表、备份、只读账号可直接读取登录主体，不满足加密落库基线 |
| SEC-02 | 安全 | 特权操作 step-up 覆盖不一致：启用、解锁、锁定、恢复、单会话撤销未强制二次认证 | 会话被盗后可直接解锁/恢复账号或撤销他人设备，削弱 step-up 价值 |
| SEC-03 | 安全 | 认证端点无独立请求体上限，全局默认 32MiB（网关实际 10MiB）且解码前无长度校验 | 未认证即可触发大内存分配；刷新端点缺少应用层限流（biz/c 默认未启用全局限流），构成低成本内存 DoS |
| SEC-05 | 安全/契约 | 账号处置强制 `Idempotency-Key`，但 OpenAPI 未声明 | 契约生成客户端全部账号处置调用 400，重放保护不可用 |
| SEC-07 | 安全/合规 | 审计仅 Outbox → 结构化日志，无独立审计库/SIEM 出口 | 保留期、不可篡改、检索与合规导出均依赖日志平台，不满足审计基线 |
| MNT-01 | 可维护性 | OpenAPI 与实现多处漂移（幂等头、B 端 merchant_id、C 端到期时间的契约） | 前端按契约生成类型后调用失败或能力缺失，违背契约优先门禁 |
| OPS-02 | 运维 | 清理任务无指标，积压/失败只能查日志 | 会话表持续增长不可预警，清理达上限时无告警 |

---

## 1. 已达标能力（避免重复投入）

| 维度 | 已落地能力 | 证据 |
|---|---|---|
| 可靠性 | 账号/会话快照读取失败回源 DB；连续 3 次失败进入 10s 冷却；singleflight 合并回源 | `internal/identity/app/revocation.go:167-174`、`:273-344`、`:421-453` |
| 可靠性 | 登录失败路径“先落库再返回 401”，业务拒绝提交事务 | `internal/identity/app/auth_service.go:323-348` |
| 数据一致性 | 业务写入与审计 Outbox 同事务；Outbox 主键复用审计事件 ID | `internal/identity/app/uow.go:7-16`、`app/outbox.go:23-73` |
| 会话安全 | 刷新令牌一次性轮换；重放撤销整链；会话绝对上限；单账号设备数上限 | `auth_service.go:846-981`、`:1337-1508` |
| MFA | TOTP 时间步原子消费、恢复码原子删除、票据一次性消费、密钥 AES-GCM + AAD 绑定账号 | `internal/identity/app/mfa.go:644-713`、`infrastructure/persistence/mysql_mfa.go:99-120` |
| 安全 | 密码 Argon2id（19MiB/t=2）、统一 401 文案、dummy 校验对齐时序 | `pkg/password/argon2.go:25-30`、`app/password.go:193-222` |
| 安全 | A/B/C 端路由与 Bearer/权限中间件组合，权限中间件缺失时失败关闭 | `internal/identity/transport/admin/router.go:107-117` |
| 安全 | 高风险账号写操作强制 `Idempotency-Key` | `transport/admin/router.go:119-132` |
| 安全 | 高价值操作审计：登录/登出/刷新/改密/MFA/账号处置/会话撤销/读取 PII/限流 | `app/auth_service.go:1730-1790`、`app/account_service.go:434-443` |
| 运维 | `readyz` 只绑定 DB，Redis 抖动不批量摘流 | `cmd/internal/identityruntime/runtime.go:592-605` |
| 运维 | 身份域告警规则 + Dashboard + Runbook + SLO 文档 | `deploy/observability/prometheus/rules/identity.yaml`、`docs/runbooks/identity-auth-failure.md`、`docs/identity-slo.md` |
| 数据 | 清理索引、归档表、多租户唯一键、权限拆分迁移 | `migrations/000008`~`000012` |
| 配置 | 生产强制项校验：DB 非 memory、async 必开、revocation fail_closed、hash_key、A 端 MFA 必开 | `internal/platform/config/config.go:1795-1991` |

---

## 2. 可靠性维度

> 生产硬性要求：单依赖故障不得放大为全局不可用；故障可自动恢复；关键状态最终一致；错误可定位。

| ID | 生产硬性要求 | 现状与证据 | 差距 / 风险 | 优先级 | 改进建议 |
|---|---|---|---|---|---|
| REL-01 | 任一外部依赖故障必须快速失败或降级，不得逐请求等待超时 | 熔断仅覆盖账号/会话快照读取（`revocation.go:273-288`、`:365-380`）；jti 校验始终调用 Redis（`revocation.go:254-260`），且未查询 `storeSkipped()`。Redis 默认读超时 3s（`pkg/redis/config.go:58-60`） | Redis 故障后：前几次请求触发熔断，但后续每个请求仍在 jti 校验处等待 3s；HTTP 超时 30s，连接/协程快速堆积，表现为全量 503 雪崩 | P1 | 熔断打开时 jti 校验直接快速失败（503）不再触网；或为 jti 增加 DB 事实源/本地短 TTL 负缓存；上线前必须确认 Redis 具备主从/哨兵与容量冗余 |
| REL-02 | 周期任务必须可重入且多副本安全 | `StartCleanup` 直接 `go runCleanupLoop`（`runtime.go:339`）；生产 worker `replicas: 2`（`deploy/helm/admin-frame/values-prod.yaml:167-168`）；无分布式锁/leader election | 两副本同时 `ExpireStale` / `ArchiveExpiredBefore`：重复更新、重复归档尝试、唯一键冲突回滚、计数虚高，扩大 DB 压力 | P1 | 引入 DB advisory lock 或 Redis 锁（带租约），仅 leader 执行；或把清理拆为独立单副本 Job；补充“本轮是否被抢占”的日志与指标 |
| REL-03 | 单请求取消不得影响其他并发请求 | 回源 singleflight 使用第一个调用方的 `ctx`（`revocation.go:311-335`） | 首个请求超时/断开时，同一 key 的等待者全部收到取消错误，fail_closed 下放大为 503 | P2 | singleflight 内部使用与请求解耦的短超时 context，调用方只读取结果并自行判取消 |
| REL-04 | 就绪探针必须真实反映“能否提供服务” | `registerHealth` 只注册 `identity_db`（`runtime.go:599-605`）；而所有访问令牌都含 `jti`，校验强依赖 Redis | Redis 故障时 Pod 仍 Ready、持续接流量并返回 503；这是“避免批量摘流”的显式取舍，但必须配套 jti 快速失败与 Redis 冗余，否则故障面被放大 | P2（设计取舍，需评审确认） | 与 REL-01 一并决策：若保留“Redis 不在 readiness”，必须在 Runbook 声明硬依赖并提供止损；否则增加 `identity_redis` 软检查（带延迟容忍） |
| REL-05 | 失败不得静默，必须可观测、可恢复 | 撤销存储读失败只上报指标，不打错误日志（`revocation.go:463-469`）；清理失败仅 worker 日志（`runtime.go:352-357`） | 故障排查只能看到计数增长，缺少 error/trace 关联；清理积压无法量化 | P2 | 为存储失败补充结构化错误日志（含 op/trace_id，不含 PII）；清理任务输出成功率/行数/耗时指标 |
| REL-06 | 并发重试不得误伤正常用户 | 并发刷新冲突与“已 rotated 再次出现”统一按重放处理并撤销整链（`auth_service.go:890-901`、`:955-968`） | 客户端网络重试/双击刷新会触发整设备下线，属于可用性与体验风险 | P2 | 区分“同设备同 IP 的短窗口并发重试”与真实重放：允许极短宽限或返回同一结果（幂等刷新键） |
| REL-07 | 外部身份建号必须最终一致 | OAuth 自动建号中，账号写入成功但身份映射写入冲突时直接返回 503（`app/oauth.go:335-367`） | 会留下无映射的 pending 孤儿账号；重复登录可能产生多个主体 | P2 | 冲突时回查已存在映射并复用；孤儿账号纳入清理任务或建号前先做幂等占位 |
| REL-08 | 优雅停机不得中断在途批处理 | 清理 goroutine 未纳入 WaitGroup；进程退出仅依赖 ctx 取消（`runtime.go:339-369`） | 归档事务可能在停机瞬间被取消，留下未完成批次（可重入但会产生失败日志/重试成本） | P2 | 停机时先停止 ticker，等待当前批次完成或超时后再退出 |

---

## 3. 性能维度

> 生产硬性要求：明确并发上限与容量基线；避免事务内重 CPU/IO；查询有界；缓存策略可解释。

| ID | 生产硬性要求 | 现状与证据 | 差距 / 风险 | 优先级 | 改进建议 |
|---|---|---|---|---|---|
| PERF-01 | 重 CPU（Argon2）必须移出数据库事务，并受并发上限保护 | 登录已采用事务外 probe（`auth_service.go:449-555`），但改密 `Verify` + `Hash`（`account_service.go:121-127`）、管理员重置 `Hash`（`:558`）、强制改密 `Verify` + `Hash`（`password_change.go:122-125`）仍在事务内；且直接调用 `PasswordService.Verify/Hash`，绕过 `WithConcurrencyLimit` 槽位（仅 `VerifyContext/VerifyDummyContext` 有限流，`password.go:193-212`） | 单个改密请求可占用 DB 连接数十毫秒；并发改密/重置时 Argon2 并发无上限，内存与 CPU 可被瞬间打满 | P1 | 统一“事务外计算、事务内复核”模式：事务外校验旧密码并计算新哈希，事务内只做哈希一致性复核与写入；所有 Argon2 调用必须经过信号量槽位 |
| PERF-02 | 事务内只做数据库操作，缓存写必须在提交后 | `createSession` 在事务内调用 `markSessionActive`（`auth_service.go:1395`）；刷新 `issueNewSession` 同样（`:1507`）；而 `markSessionState` 注释明确要求提交后调用（`:1575-1578`） | Redis 写超时可把 DB 事务拉长至秒级；事务回滚后 Redis 残留 active 快照，违反一致性约定 | P1 | 将会话快照写入收集为返回值，复用 `markSessionsRevoked` 的提交后刷新模式 |
| PERF-03 | 查询必须有界；历史数据增长不得放大关键路径 | 每次刷新新增一行 `identity_session`（`auth_service.go:1491-1505`）；撤销链路 `ListByFamily` 加载整链全部历史（`:1643`、`infrastructure/persistence/mysql_session.go:370-395`）；在线保留 30 天（默认） | 15 分钟刷新一次的单设备 30 天约 2,880 行；每次登出/撤销都要全量加载并逐条构造快照，时延与内存随会话年龄线性增长 | P1 | 撤销时只取 `active/rotated` 的会话主键（UPDATE 前后各一次有界查询），或由 UPDATE 返回受影响 ID；同时评估缩短 rotated 保留期与同链压缩策略 |
| PERF-04 | 单账号活跃设备上限必须严格生效 | `evictFamilies` 以 `limit=0` 拉取设备（`auth_service.go:1407`），SQL 仓储把 `limit<=0` 归一为 20（`mysql_session.go:332-334`） | 活跃设备超过 20 时，只能看到最新 20 台；计算出的淘汰数不足，最终活跃设备可超过 `max_per_account` | P2 | 分页循环直到 `len(families) <= max-1`；或先 `COUNT(*)` 再按需淘汰 |
| PERF-05 | 限流默认值必须匹配真实网络拓扑 | 身份域 overlay 将登录限流设为 1 rps / burst 5（`deploy/helm/admin-frame/values-identity-enabled.yaml:55-58`），账号级与 IP 级使用同一组参数（`auth_service.go:465-468`） | 同一出口 NAT 下大量员工共享 1 rps 的 IP 桶，正常登录会被 429；账号级桶在携带 IP 时退化为“账号+IP”，可被换 IP 绕过 | P2 | 拆分账号级（严格）与 IP 级（更宽松，如 20-50 rps）配置；对共享出口提供可信代理/网段白名单策略 |
| PERF-06 | 外部 IdP 调用必须有超时、熔断与合理配额 | OAuth 固定 10s 超时（`infrastructure/oauth/oauth2.go:22`、`:69-72`），无熔断/重试；全局 authorize/callback 默认 1 rps、burst 5（`internal/platform/config/config.go:1260-1267`） | IdP 抖动时回调请求长时间占用协程；全局 1 rps 会让正常规模的 C 端登录直接 429 | P2 | 超时下沉到 3-5s 且可配置；增加 provider 级熔断与失败率告警；按业务规模调整默认配额 |
| PERF-07 | 列表查询不得依赖全表/大范围扫描 | 关键词使用前置通配符 `LIKE %kw%`，总数统计以 `LIMIT 10000` 截断（`mysql_account.go:217-231`） | 每次账号列表请求最多扫描 1 万行；账号规模上升后管理端列表成为慢查询热点 | P2 | 改为前缀匹配/独立搜索索引（如 ngram/ES）；或提供“精确主体查询 + 默认无关键词分页”双模式 |
| PERF-08 | 容量指标必须可量化 | 已认证请求至少 1 次 Redis（jti）且不缓存；账号/会话 L1 默认 1s（`cached_revocation.go:60-105`） | 该成本属于设计取舍，但缺少“活跃会话数 / 登录 QPS / Redis 调用量”容量指标，HPA 只能按 CPU | P2 | 增加活跃会话 gauge、Redis 调用速率、DB 回源速率面板，作为容量评审与 HPA 辅助信号 |

---

## 4. 安全维度

> 生产硬性要求：最小权限、敏感数据加密落库、完整输入校验、防注入/越权/重放、可追责审计。

| ID | 生产硬性要求 | 现状与证据 | 差距 / 风险 | 优先级 | 改进建议 |
|---|---|---|---|---|---|
| SEC-01 | PII 落库必须加密或不可逆摘要 | `identity_account.subject` 明文列仍保留且唯一键依赖明文（`migrations/000001:19`、`000009:14-16`）；S4/S5 明确未发布（`000011:5-9`）；`provider_subject` 明文（`000001:148`）；读路径未命中索引时回退明文查询（`mysql_account.go:107-110`） | 数据库、备份、慢查询日志、只读账号可直接获得登录主体；PII 加密能力形同“双写演练” | P1 | 排期 S4（唯一键切 `subject_index`）与 S5（删明文列）；`provider_subject` 同步 index/ciphertext 双写；完成后清理回退分支 |
| SEC-02 | 高风险管理操作必须二次认证 | 仅 ResetPassword / ResetMFA / Delete / 全量撤销调用 `requireAdminStepUp`（`account_service.go:286`、`:548`、`:600`、`:723`）；Enable / Unlock / Lock / Restore / RevokeTargetSession 未调用（`:199`、`:213`、`:472`、`:627`、`:679`） | 会话被盗的管理员可解锁被锁账号、恢复已删账号、撤销他人设备；step-up 防线不完整 | P1 | 将 step-up 覆盖到所有“改变账号可用性/凭据/会话”的操作；至少 Enable/Unlock/Restore/单会话撤销必须二次认证 |
| SEC-03 | 认证端点必须有独立、严格的请求体上限 | 全局默认 `body_limit_bytes=32MiB`（`config.go:1132-1134`）；认证 Handler 解码前无长度检查（`transport/rest/handler.go:924-932`、`transport/c/handler.go:342-350`） | 未认证即可提交 32MiB JSON：`subject`、`refresh_token`、OAuth `state/code` 在解析/解码阶段产生大内存分配；刷新端点又无限流，构成低成本内存 DoS | P1 | 为 `/auth/*` 路由单独设置 4-16KiB 上限；对 subject/refresh_token/state/code 在分配前做长度上限校验 |
| SEC-04 | 匿名可写端点必须有独立限流 | 登录/MFA/短信有应用层限流；刷新、登出无应用层限流（`rest/handler.go:566-617`）；biz/c overlay 未启用全局 rate_limit（`values-identity-enabled.yaml:93-108`） | 刷新令牌虽不可爆破，但可被用作低成本 DB 查询放大器；异常流量无每端点配额 | P2 | 对 refresh/logout 增加按 IP/令牌前缀的限流；在 biz/c 生产 overlay 显式启用 rate_limit 并指定 fail_closed |
| SEC-05 | 契约必须声明所有强制请求约束 | 账号处置路由强制 `Idempotency-Key`（`transport/admin/router.go:82-104`），但 OpenAPI 仅定义了 header（`api/openapi/components.yaml:28-36`），没有任何 path 引用；描述还写成“C 端写操作” | 契约生成客户端不会携带幂等键，所有账号处置调用 400；同时无法按契约校验重放保护 | P1 | 在所有高风险 A 端处置 path 引用 `IdempotencyKey` 并标记 `required: true`；修正描述；契约门禁增加“强制头是否被引用”检查 |
| SEC-06 | 密码策略应包含复用限制/泄露检测 | 仅校验“新密码≠旧密码/临时口令”（`account_service.go:124-126`、`password_change.go:122-124`） | 用户可在 A→B→A 间循环复用历史密码；无法阻止已泄露口令 | P2 | 增加最近 N 次密码哈希历史（独立表或 JSON 列）与可配置复用窗口；可选接入离线泄露口令库 |
| SEC-07 | 审计必须落到不可抵赖的生产存储 | 生产审计出口仍为 Outbox → `SlogEmitter`（`runtime.go:227-236`、`:301-309`；`app/outbox.go:79-99`）；仓库无审计表迁移 | 审计仅存在于日志系统：保留期、完整性、检索与合规导出均依赖日志平台；无法满足“独立审计源” | P1 | 新增审计库表或 SIEM 出口（Outbox → audit_store），保留期按 `docs/AUDIT_STANDARDS.md`；补 ADR 与查询/导出接口 |
| SEC-08 | 批量 PII 读取必须审计 | `AccountService.Get`、`ListTargetSessions` 已审计（`account_service.go:434-443`、`:663-673`）；`List` 无审计（`:397-430`、`rest/handler.go:379-401`） | 管理员可批量拉取账号主体而不留痕，与“读取 PII 必审计”不一致 | P2 | 列表读取写 `auth.account.read`（批量标记 `scope=list`），注意采样/成本控制 |
| SEC-09 | 外部成本型资源必须有硬预算 | 短信全局日预算默认 0 表示不限制（`config.go:188-189`、`app/sms_login.go:209-216`）；生产校验未强制预算 | 单手机号/单 IP 限额可被分布式号码池绕过，造成短信费用攻击面 | P2 | 生产环境强制 `daily_budget > 0` 并配套余额告警；接入供应商余额熔断（已有 breaker 基础） |
| SEC-10 | 输入解析必须严格、可预期 | `decodeJSON` 只解码第一个 JSON 对象，不校验尾随内容与 `Content-Type`（`rest/handler.go:924-932`） | 解析歧义会影响审计与网关一致性；与严格输入校验基线有差距 | P2 | 解码后检查 EOF、校验 `Content-Type: application/json`；统一到共享解码工具避免 A/B/C 三份实现漂移 |

---

## 5. 可维护性维度

> 生产硬性要求：结构清晰、契约与实现一致、配置可解释、可测试、可平滑升级/回滚。

| ID | 生产硬性要求 | 现状与证据 | 差距 / 风险 | 优先级 | 改进建议 |
|---|---|---|---|---|---|
| MNT-01 | 契约与实现必须无漂移 | 除 SEC-05 外还存在：B 端登录描述称 `merchant_id` 由账号推导（`api/openapi/paths/biz/auth.yaml:5-7`），但实现要求请求体必填（`app/auth_service.go:454-461`，`components.yaml:123-127` 为可选）；C 端 TokenPair 未返回 `absolute_expires_at`（`transport/c/handler.go:325-330` vs `rest/handler.go:836-843`） | 前端按契约生成类型后，B 端登录缺字段、C 端拿不到设备绝对到期时间；契约评审与代码评审结论冲突 | P1 | 修正 B 端 `merchant_id` 的必填约束与描述；C 端补齐 `absolute_expires_at`；CI 增加“路由/字段/强制头”三向一致性检查 |
| MNT-02 | 模块边界与文件职责应可维护 | `auth_service.go` 约 1,836 行，同时承载登录编排、令牌票据方法（`:717-725`）、审计、会话快照；`rest/handler.go` 约 1,000 行；`account_service.go` 781 行；A/B/C 各一份 `decodeJSON` 与 `tokenResponse` | 变更影响面大、评审困难；共享 DTO/解码逻辑重复，容易出现契约漂移（已发生） | P2 | 拆分 `login_service` / `session_service` / `token_ticket_service` / `audit_service`；把 DTO 与解码收敛到共享包 |
| MNT-03 | 关键层必须有覆盖率门禁 | 覆盖率门禁仅覆盖 `internal/**/domain`、`internal/platform`、`internal/security`（`scripts/ci/check-test-gates.sh:22-35`）；identity 的 app/transport/infrastructure 无门槛；SQL 集成测试依赖 `TEST_MYSQL_DSN`，本地默认跳过 | 高风险编排层（step-up、事务边界、重放）回归保护依赖“自觉”，新增分支容易漏测 | P2 | 为 `internal/identity/app`、`transport` 设分层门槛（建议 app≥75%、transport≥70%）；SQL 集成纳入必跑 CI job |
| MNT-04 | 文档必须与代码同步 | `docs/identity-slo.md:33` 仍写“每个已认证请求 2–3 次 Redis 读”，L1 已上线；`identity-slo.md:15` 定义 MFA P95，但指标集无 MFA 时延直方图（`observability/metrics.go:70-73`）；`identity-p0-p1-fix-log.md` 的 P1-6 为“部分完成” | 运维按过期口径做容量规划；SLO 无法被真实测量，评审结论不可复核 | P2 | 文档标注“当前有效口径”；补齐 MFA/刷新/step-up 时延直方图后再声明对应 SLO |
| MNT-05 | 关键业务参数应可配置且有边界 | OAuth 超时固定 10s（`infrastructure/oauth/oauth2.go:22`）；TOTP 位数/步长/窗口固定（`pkg/totp/totp.go:20-25`）；恢复码数量 10 固定（`app/mfa.go:34`）；临时口令长度 20 固定（`app/password_change.go:16-18`） | 不同合规等级/租户无法调整；变更需改代码重新发版 | P2 | 将 OAuth 超时、TOTP 参数、恢复码数量、临时口令长度纳入配置并做启动期边界校验 |
| MNT-06 | 在线表与归档表结构必须一致 | `identity_session_family` 已有设备画像列（`migrations/000013:12-20`），但归档表 `identity_session_family_archive` 未包含 `device_type/os_name/browser_name/ip_masked`（`000010:38-54`），归档写入也未复制（`mysql_session.go:590-601`） | 历史设备会话丢失设备画像，安全审计/用户申诉时无法还原设备 | P2 | 归档表补齐列并同步 `INSERT SELECT`；回滚脚本与数据回填一并纳入迁移 |

---

## 6. 运维适配维度

> 生产硬性要求：容器化可水平扩展、健康检查真实、监控指标完备、可平滑扩缩容。

| ID | 生产硬性要求 | 现状与证据 | 差距 / 风险 | 优先级 | 改进建议 |
|---|---|---|---|---|---|
| OPS-01 | 就绪状态应反映核心依赖可用性 | 见 REL-01/REL-04：jti 是硬依赖，但 readiness 不检查 Redis | 依赖故障时 Pod 继续接收流量并返回 503，无法通过摘流止损 | P2（设计取舍，需评审确认） | 与 REL-01 合并决策；若保持现状，必须在 Runbook 明确“Redis 故障即鉴权全不可用”的止损动作（扩容/切主/网关限流） |
| OPS-02 | 后台任务必须有指标与成功率 | `SessionCleanup` 仅输出结构化日志（`app/session_cleanup.go:163-175`），无 Metrics 依赖；无“本轮耗时/剩余积压/是否达上限”指标 | 清理积压只能靠日志检索；达到 `MaxBatchesPerRun` 时无告警，磁盘增长不可预警 | P1 | 暴露 `identity_cleanup_rows_total{kind,result}`、`identity_cleanup_duration_seconds`、`identity_cleanup_backlog_estimate`；对“本批未清空”打 WARN |
| OPS-03 | SLO 必须有对应 SLI 与告警 | 仅登录有耗时直方图（`metrics.go:56-65`）；刷新/MFA/step-up/OAuth 无耗时直方图；告警规则无时延类规则（`prometheus/rules/identity.yaml:1-105`） | 文档声明的刷新/MFA P95 无法计算；“登录 P95≤300ms”也无对应告警，只能依赖平台 P99 间接观测 | P2 | 补齐 `identity_refresh_duration_seconds`、`identity_mfa_duration_seconds`、`identity_oauth_duration_seconds`；增加 P95 超阈告警 |
| OPS-04 | 审计投递积压必须可观测 | 身份域指标无 outbox 积压 gauge（`metrics.go:19-37`）；IAM 已有 `RegisterOutboxGauges`（`cmd/internal/iamruntime/runtime.go:148`） | 审计消费失败/积压时，身份域侧无告警；审计延迟直接影响合规 | P2 | 复用 outbox gauge 注册到 `identity.audit` topic；增加积压年龄告警 |
| OPS-05 | 扩容依据应覆盖认证负载 | HPA 仅按 CPU（`deploy/helm/admin-frame/values-prod.yaml:100-112`）；无登录 QPS/活跃会话/在途请求指标 | Argon2 与 Redis 延迟场景下 CPU 不能代表真实瓶颈，扩缩容滞后 | P2 | 引入认证 QPS、P95、活跃会话作为 HPA 辅助指标或至少作为人工扩容触发器 |
| OPS-06 | 扩缩容必须无本地状态依赖 | 生产强制 DB/Redis/async（`config.go:1823-1830`）；内存驱动仅限非生产 | 运行时装配仍有内存回退（`runtime.go:384-400`、`runtime_c.go:135-144`），依赖配置校验兜底 | P2 | 在 `env=prod/staging` 启动日志中显式打印“无内存回退”；补充一条启动自检断言 |
| OPS-07 | 供应商熔断状态必须可监控 | breaker 状态变化仅写日志（`runtime_c.go:161-166`）；无 `identity_sms_breaker_state` 指标 | 熔断打开后仅日志可见，告警面缺少“短信通道不可用”的明确信号 | P2 | 暴露 breaker state / 失败率指标，并配置供应商失败率告警 |

---

## 7. 业务适配维度

> 生产硬性要求：多租户隔离、国际化、边界场景覆盖、业务规则可配置。

| ID | 生产硬性要求 | 现状与证据 | 差距 / 风险 | 优先级 | 改进建议 |
|---|---|---|---|---|---|
| BIZ-01 | C 端应具备完整自助资料能力 | C 端仅注册/登录/换绑/注销（`transport/c/router.go:14-32`），无 `/c/v1/me`；`rest` 的 Me/UpdateMe 只注册在 admin/biz（`transport/admin/router.go:49-51`、`transport/biz/router.go:34-36`） | C 端用户无法查看/修改昵称与安全设置状态；产品功能与 A/B 端不一致 | P2 | 若 C 端需要个人中心，补 `GET/PATCH /c/v1/me` 与对应审计；否则在 API 文档明确“C 端不提供资料能力” |
| BIZ-02 | 多身份方式应可关联到同一账号 | OAuth 未映射时按 `provider\|subject` 自动建新账号（`app/oauth.go:294-381`）；无“绑定/解绑 OAuth/手机号”端点；OAuth-only 账号在短信启用时注销返回 409（`phone is not bound`），短信未启用时仅凭 Bearer 注销（`transport/c/handler.go:184-217`、`app/consumer_service.go:79-82`） | 同一自然人用手机号+微信登录会得到两个账号，资产/权益割裂；OAuth-only 账号缺少可用的再认证注销路径 | P2 | 增加身份绑定/合并流程（已验证会话+另一身份验证）；OAuth-only 注销接入 IdP 再认证或强制二次验证码 |
| BIZ-03 | MFA 策略应可按端/租户配置 | MFA 仅 A 端 TOTP，B 端配置 `admin_required=true` 会在启动期失败（`config.go:1945-1949`、`runtime.go:212-214`）；仅 TOTP 一种因子 | B 端商户无法启用 MFA；高合规租户无法选择 WebAuthn 等更强因子 | P2 | 将 MFA 策略改为“端 × 租户”配置；抽象因子接口，预留 WebAuthn/Passkey |
| BIZ-04 | 密码与租户策略应可差异化 | 密码策略为全局配置（`config.go:412-426`）；无过期、历史、泄露检测；临时口令长度固定 | 无法满足“金融租户更严、普通租户更宽”的业务诉求 | P2 | 引入租户级策略覆盖（带安全下限）；补密码历史与过期策略 |
| BIZ-05 | 国际化应覆盖用户可见文案与时间口径 | 仅短信模板 `locale`（`config.go:176-177`）；错误文案硬编码英文（如 `app/password.go:77-89`）；短信日/时限按 UTC 分桶（`cache/sms_limiter.go:234-242`） | 多语言用户看到英文校验错误；跨时区用户遇到“配额在本地零点重置”的预期偏差 | P2 | 错误响应保留稳定 code，由前端本地化；短信配额按租户时区可配置；审计时间保持 UTC 并前端展示本地时间 |
| BIZ-06 | 租户标识必须规范化且与库排序规则一致 | 主体做了 `trim+lower`（`domain/account.go:47-54`），`merchant_id` 仅 `TrimSpace`（`auth_service.go:454`）；库排序规则大小写/重音不敏感（`migrations/000001:35`） | `M-1` 与 `m-1` 在库内互相冲突，但应用视为不同租户；可能导致“建号成功却登录不到正确租户” | P2 | 对 `merchant_id` 实施与唯一键一致的规范化（或改为大小写敏感排序规则），并加单测覆盖 |
| BIZ-07 | 手机号输入应覆盖目标市场格式 | 仅接受 E.164（`app/sms_login.go:30`、`:174-177`） | 国内用户按本地格式输入会被 400；国际化市场缺少号码规范化 | P2 | 明确“仅 E.164”为契约要求并在前端做格式化；或接入 libphonenumber 做区域规范化后再入库 |
| BIZ-08 | 边界场景必须有明确产品策略 | 并发刷新按重放整链撤销（`auth_service.go:955-968`）；登录消费的 TOTP 时间步会让紧随其后的 step-up 判为重放（`app/mfa.go:358-363`） | 合法用户双击刷新会被登出；管理员登录后立即执行特权操作可能需等待下一个 30s 时间步 | P2 | 并发刷新引入短宽限/幂等键；step-up 使用独立时间步命名空间或允许“登录后 N 秒内复用同一时间步” |
| BIZ-09 | 多租户能力边界应明确 | 身份域仅 B 端有 `merchant_id`；A 端平台级；C 端无租户维度（`domain/account.go`、`migrations/000009`） | 若未来需要“一个 C 端用户在多个商户下独立身份”，当前模型不支持 | P2 | 明确产品边界；如需支持，扩展为 `(channel, tenant_scope, subject)` 并在令牌与 IAM 侧同步 |

---

## 8. 改进路线图

### 8.1 第一批（上线前必须完成，P1 可用性/性能）

1. REL-01：jti 快速失败 + Redis 冗余与 Runbook 止损（REL-04 / OPS-01 的就绪探针取舍一并评审确认）。
2. REL-02 / OPS-02：清理任务 leader election + 指标。
3. PERF-01：Argon2 全部移出事务并统一走并发槽位。
4. PERF-02：会话快照写移出事务，改提交后刷新。
5. PERF-03：撤销链路只加载 `active/rotated` 会话，查询有界。

验收标准：
- Redis 故障注入下，已认证请求在 1s 内稳定快速失败/降级，不出现 3s 逐请求等待；`/readyz` 行为与 Runbook 一致。
- worker 双副本并行时，归档/删除无重复计数、无唯一键冲突；清理指标可看到每轮行数/耗时/是否清空。
- 改密/重置密码 P95 不随并发线性恶化；数据库事务时长不包含 Argon2。
- 单设备 30 天会话链的登出/撤销 P95 不随链长增长（目标：<50ms，待 staging 实测）。

### 8.2 第二批（上线前完成或签风险接受，P1 安全/合规/契约）

1. SEC-01：S4 唯一键切 `subject_index`、S5 删明文；`provider_subject` 加密。
2. SEC-02：Enable/Unlock/Lock/Restore/单会话撤销接入 step-up。
3. SEC-05 / MNT-01：OpenAPI 补齐 `Idempotency-Key`、B 端 `merchant_id` 必填、C 端 `absolute_expires_at`；加入契约门禁。
4. SEC-07：审计落独立存储/SIEM，保留期与查询能力对齐 `AUDIT_STANDARDS`。

验收标准：
- 库表中不存在可读的登录主体明文；回填与双读回退分支删除。
- 所有账号可用性/凭据/会话变更操作在 `mfa.admin_required=true` 时缺少 `mfa_code` 返回 40000。
- 契约生成客户端可直接调用账号处置接口（幂等头已声明）。
- 审计可按 actor/target/trace 检索，保留期 ≥180 天。

### 8.3 第三批（P2 迭代）

- REL-03 / REL-06 / REL-07 / REL-08：singleflight context 解耦、并发刷新宽限、OAuth 孤儿账号治理、停机等待。
- SEC-03 / SEC-04 / SEC-06 / SEC-08 / SEC-09 / SEC-10：路由级 body limit、refresh/logout 限流、密码历史、列表读审计、短信硬预算、严格 JSON 解析。
- PERF-04~08、OPS-03~07、MNT-02~06、BIZ-01~09：按季度工程债排期。

### 8.4 需正式风险接受（若不排期修复）

| 风险 | 接受条件 | 到期复核 |
|---|---|---|
| Redis 故障导致 jti fail_closed 全量 503 | Redis 具备主从/哨兵、跨 AZ、容量冗余；Runbook 明确止损；SLO 中声明该依赖为硬依赖 | 每季度 |
| 审计仅日志存储 | 日志平台具备 WORM/不可篡改、保留期与检索能力，且通过合规评审 | 上线前 |
| PII 明文过渡期 | 数据库访问最小化、备份加密、审计只读访问；S4/S5 有明确排期 | 每季度 |
| OAuth-only 账号仅 Bearer 注销 | 产品接受；后续接入 IdP 再认证 | 下个版本 |

> 8.4 状态（2026-11-01）：四项均不再以「风险接受」收口，已按专项修复闭环。
> - Redis 故障 jti 全量 503：新增 `identity.revocation.token_grace` 本地宽限，故障期有界放行（R-8.4-01）。
> - 审计仅日志存储：8.2 SEC-07 已落独立审计表 + SQL 出口，本批次复核通过（R-8.4-02）。
> - PII 明文过渡期：8.2 SEC-01 已完成 S4/S5，明文列已删除，本批次复核通过（R-8.4-03）。
> - OAuth-only 仅 Bearer 注销：新增 `identity.consumer.delete_reauth_window`，要求最近登录（R-8.4-04）。
> 详见 [identity-batch-8-4-completion-report.md](./identity-batch-8-4-completion-report.md)。

---

## 9. 证据索引（关键代码位置）

- `internal/identity/app/revocation.go:254-260`：jti 校验未受熔断保护。
- `internal/identity/app/revocation.go:273-288`、`:365-380`：熔断仅覆盖账号/会话快照读取。
- `internal/identity/app/auth_service.go:1395`、`:1507`：事务内写 Redis 会话快照。
- `internal/identity/app/auth_service.go:1643`、`infrastructure/persistence/mysql_session.go:370-395`：整链无界加载。
- `internal/identity/app/account_service.go:121-127`、`:558`；`app/password_change.go:122-125`：事务内 Argon2。
- `internal/identity/app/password.go:193-212`、`:248-257`：并发槽位只覆盖 `VerifyContext`，`Hash`/`Verify` 可绕过。
- `internal/identity/app/account_service.go:199-224`、`:472-531`、`:627-712`：step-up 未覆盖。
- `internal/identity/transport/rest/handler.go:440-452`：Lock 请求丢弃 `mfa_code`。
- `internal/identity/transport/admin/router.go:82-104`：高风险路由强制幂等键。
- `api/openapi/components.yaml:28-36`：幂等头仅定义未引用。
- `api/openapi/paths/biz/auth.yaml:5-7` vs `app/auth_service.go:454-461`：B 端 merchant_id 契约漂移。
- `internal/platform/config/config.go:1132-1134`：全局请求体默认 32MiB。
- `internal/platform/config/config.go:188-189`、`:1260-1267`：短信预算与 OAuth 限流默认值。
- `migrations/000001:19`、`:148`；`000009:14-16`；`000011:5-9`：PII 明文与 S4/S5 状态。
- `cmd/internal/identityruntime/runtime.go:227-236`、`:301-309`、`:339`、`:592-605`：审计出口、清理启动、就绪探针。
- `deploy/helm/admin-frame/values-prod.yaml:167-168`：worker 双副本。
- `deploy/observability/prometheus/rules/identity.yaml:1-105`：现有时延/清理/审计积压告警缺口。
- `scripts/ci/check-test-gates.sh:22-35`：覆盖率门禁范围不含 identity app/transport。
