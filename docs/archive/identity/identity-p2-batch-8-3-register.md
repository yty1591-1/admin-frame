# identity P2 问题登记册（第 8.3 批次）

> 来源：[identity-enterprise-production-gap-review.md](./identity-enterprise-production-gap-review.md) §8.3。
> 范围：本轮执行 10 项已明确修复动作的 P2；其余 25 项标注“按季度工程债排期”的 P2
> 登记为 backlog，另行排期，不占用本轮周期。
> 责任登记采用「角色 + 里程碑」，不指定个人、不承诺日历日期。

## 1. 批次目标与验收口径

- 本轮目标：关闭 REL-03/06/07/08 与 SEC-03/04/06/08/09/10。
- 批次验收：
  - 单请求取消不再放大同 key 回源失败；停机不中断在途清理批次。
  - 并发刷新短窗口不再直接撤销整链；OAuth 映射冲突不再产生孤儿账号。
  - `/auth/*` 具备独立请求体上限；refresh/logout 有端点级限流并生产启用。
  - 密码不可短期复用；列表读取有审计；生产短信必须有硬预算；JSON 解析严格。
- 回归口径：每项必须有单测或集成用例；四项全部 M1/M2 后交测试团队 M3。

## 2. 里程碑定义

| 里程碑 | 定义 | 出口条件 |
|---|---|---|
| M1 | 代码与单测 | 目标包 `go vet` / 单测通过；`go build ./...` 通过 |
| M2 | 集成与配置 | 全量 `go test ./...` 通过；配置/契约/迁移校验通过 |
| M3 | 回归 | 测试团队按验收标准回归，无新增关联缺陷 |
| M4 | 归档 | 汇总报告、回滚说明、文档记录归档 |

## 3. 本轮 10 项登记

### REL-03 singleflight 回源 context 解耦

| 字段 | 内容 |
|---|---|
| 问题描述 | 回源 singleflight 复用第一个调用方的 `ctx`，首个请求取消会让同 key 等待者全部失败（`revocation.go`）。 |
| 影响范围 | 账号 / 会话快照回源；Redis 抖动或客户端断开时的 fail_closed 放大。 |
| 复现条件 | 同一 key 并发请求，首个请求超时 / 断开，其余等待者收到取消错误。 |
| 关联模块 | `internal/identity/app/revocation.go`。 |
| 责任角色 | identity 后端（主责）；测试（并发取消回归）。 |
| 里程碑 | M1 单测；M2 集成；M3 回归。 |
| 修复方案 | singleflight 内部使用与调用方解耦的短超时 context；调用方只读取结果并自行判断取消。 |
| 验证标准 | 首个调用取消后，后续等待者仍可获得回源结果；回源整体不超过配置超时；原有 fail_closed 行为不变。 |
| 状态 | 已修复（M1/M2，待 M3 回归） |

### REL-06 并发刷新短宽限

| 字段 | 内容 |
|---|---|
| 问题描述 | 并发刷新冲突与“已 rotated 再次出现”统一按重放整链撤销，网络重试 / 双击刷新会误伤正常用户（`auth_service.go`）。 |
| 影响范围 | 刷新链路、设备会话存活、用户体验。 |
| 复现条件 | 同一刷新令牌短窗口内并发提交两次。 |
| 关联模块 | `internal/identity/app/auth_service.go`、会话仓储。 |
| 责任角色 | identity 后端（主责）；测试（并发刷新与重放回归）。 |
| 里程碑 | M1 单测；M2 集成；M3 回归。 |
| 修复方案 | 区分“同设备短窗口并发重试”与真实重放：宽限窗口内返回同一结果或幂等成功；超出窗口仍按重放撤销整链。宽限窗口配置化并有安全上限。 |
| 验证标准 | 短窗口并发刷新不撤销整链；超窗重放仍撤销整链并写审计；宽限不延长设备绝对到期时间。 |
| 状态 | 已修复（M1/M2，待 M3 回归） |

### REL-07 OAuth 孤儿账号治理

| 字段 | 内容 |
|---|---|
| 问题描述 | OAuth 自动建号时账号写入成功但身份映射冲突直接返回 503，留下无映射的 pending 孤儿账号（`app/oauth.go`）。 |
| 影响范围 | OAuth 登录、账号创建、身份映射一致性。 |
| 复现条件 | 并发 / 重试回调对同一 `provider\|subject` 建号，映射唯一键冲突。 |
| 关联模块 | `internal/identity/app/oauth.go`、`domain` 账号与映射仓储。 |
| 责任角色 | identity 后端（主责）；测试（并发回调回归）。 |
| 里程碑 | M1 单测；M2 集成；M3 回归。 |
| 修复方案 | 冲突时回查已存在映射并复用对应账号，不再返回 503；无法复用时记录可清理的孤儿账号，并提供维护工具清理。 |
| 验证标准 | 并发回调只产生一个可用账号与一条映射；冲突路径不再 503；孤儿账号可被维护工具识别并清理。 |
| 状态 | 已修复（M1/M2，待 M3 回归） |

### REL-08 停机等待清理批次

| 字段 | 内容 |
|---|---|
| 问题描述 | 清理 goroutine 未纳入 WaitGroup，停机仅依赖 ctx 取消，归档事务可能在停机瞬间被中断（`runtime.go`）。 |
| 影响范围 | 优雅停机、归档事务完整性、停机日志噪声。 |
| 复现条件 | 清理批次执行中触发进程退出。 |
| 关联模块 | `cmd/internal/identityruntime/runtime.go`、`app/session_cleanup.go`。 |
| 责任角色 | identity 后端（主责）；平台运维（停机演练）。 |
| 里程碑 | M1 单测；M2 集成；M3 回归。 |
| 修复方案 | 停机时先停止 ticker，等待当前批次完成或到达停机超时后再退出；超时记录告警。 |
| 验证标准 | 停机时当前批次完成或明确超时；无“事务被取消”错误日志；重启后无重复归档。 |
| 状态 | 已修复（M1/M2，待 M3 回归） |

### SEC-03 认证端点独立请求体上限

| 字段 | 内容 |
|---|---|
| 问题描述 | 全局请求体默认 32MiB，认证 Handler 解码前无长度检查（`transport/rest/handler.go`、`transport/c/handler.go`）。 |
| 影响范围 | 未认证端点内存占用；登录 / 刷新 / MFA / OAuth 回调。 |
| 复现条件 | 向 `/auth/*` 提交超大 JSON。 |
| 关联模块 | `transport/admin/router.go`、`transport/biz/router.go`、`transport/c/router.go`、`transport/rest/handler.go`、`transport/c/handler.go`。 |
| 责任角色 | identity 后端（主责）；测试（超限回归）。 |
| 里程碑 | M1 单测；M2 集成；M3 回归。 |
| 修复方案 | 为认证路由增加独立 body limit（4–16KiB 可配置）；对 `subject` / `refresh_token` / OAuth `state` / `code` 在解码前做长度上限校验。 |
| 验证标准 | 超限请求在解码前返回 40000 或 413；正常请求不受影响；三端行为一致。 |
| 状态 | 已修复（M1/M2，待 M3 回归） |

### SEC-04 refresh / logout 端点级限流

| 字段 | 内容 |
|---|---|
| 问题描述 | 刷新、登出无应用层限流；biz/c 生产 overlay 未启用全局 rate_limit（`rest/handler.go`、`values-identity-enabled.yaml`）。 |
| 影响范围 | 匿名可写端点；DB 查询放大与异常流量。 |
| 复现条件 | 高频提交 refresh / logout。 |
| 关联模块 | `transport/rest/handler.go`、`cmd/internal/identityruntime/runtime.go`、`internal/platform/config/config.go`、overlay 配置。 |
| 责任角色 | identity 后端（主责）；平台运维（生产 overlay）；测试（限流回归）。 |
| 里程碑 | M1 单测；M2 配置校验；M3 回归。 |
| 修复方案 | 对 refresh / logout 增加按 IP 与令牌前缀的限流；biz/c 生产 overlay 显式启用 `rate_limit` 并指定 fail_closed。 |
| 验证标准 | 超限返回 42900；正常刷新 / 登出不受影响；生产配置校验通过。 |
| 状态 | 已修复（M1/M2，待 M3 回归） |

### SEC-06 密码历史与复用限制

| 字段 | 内容 |
|---|---|
| 问题描述 | 仅校验“新密码≠当前密码”，用户可在 A→B→A 间循环复用历史密码（`account_service.go`、`password_change.go`）。 |
| 影响范围 | 改密、管理员重置、强制改密。 |
| 复现条件 | 用户连续改密回退到历史密码。 |
| 关联模块 | `internal/identity/app/account_service.go`、`password_change.go`、`domain/credential`、`migrations/`。 |
| 责任角色 | identity 后端（主责）；测试（历史复用回归）。 |
| 里程碑 | M1 单测；M2 迁移与集成；M3 回归。 |
| 修复方案 | 新增密码历史存储（最近 N 条哈希 + 时间）与可配置复用窗口；改密 / 重置前校验历史；迁移新增历史表。 |
| 验证标准 | 窗口内复用历史密码返回 40000；超出窗口允许；重置与强制改密同样生效；历史不存明文。 |
| 状态 | 已修复（M1/M2，待 M3 回归） |

### SEC-08 账号列表读取审计

| 字段 | 内容 |
|---|---|
| 问题描述 | `AccountService.List` 无审计，管理员可批量拉取账号主体而不留痕（`account_service.go`、`rest/handler.go`）。 |
| 影响范围 | A 端账号列表；PII 读取合规。 |
| 复现条件 | 管理员调用账号列表接口。 |
| 关联模块 | `internal/identity/app/account_service.go`、`transport/rest/handler.go`。 |
| 责任角色 | identity 后端（主责）；测试（审计回归）。 |
| 里程碑 | M1 单测；M2 集成；M3 回归。 |
| 修复方案 | 列表读取写 `auth.account.read` 审计，attrs 标记 `scope=list` 与过滤条件摘要；不记录主体原文。 |
| 验证标准 | 每次列表读取产生一条审计；审计可按 actor/trace 检索；失败读取不产生成功审计。 |
| 状态 | 已修复（M1/M2，待 M3 回归） |

### SEC-09 短信硬预算

| 字段 | 内容 |
|---|---|
| 问题描述 | 短信全局日预算默认 0 表示不限制，生产校验未强制预算（`config.go`、`sms_login.go`）。 |
| 影响范围 | 短信成本攻击面、供应商余额。 |
| 复现条件 | 分布式号码池绕过单手机号 / IP 限额持续发送。 |
| 关联模块 | `internal/platform/config/config.go`、`internal/identity/app/sms_login.go`、SMS 配置。 |
| 责任角色 | identity 后端（主责）；平台运维（预算与告警）；测试（配置校验回归）。 |
| 里程碑 | M1 单测；M2 配置校验；M3 回归。 |
| 修复方案 | 生产环境强制 `daily_budget > 0`；预算耗尽返回 42900 并告警；接入供应商余额告警（复用现有 breaker 基础）。 |
| 验证标准 | 生产配置缺少预算时启动失败；预算耗尽后拒绝发送；告警可观测。 |
| 状态 | 已修复（M1/M2，待 M3 回归） |

### SEC-10 严格 JSON 解析

| 字段 | 内容 |
|---|---|
| 问题描述 | `decodeJSON` 只解码第一个 JSON 对象，不校验尾随内容与 `Content-Type`（`rest/handler.go`、`c/handler.go`）。 |
| 影响范围 | A/B/C 三端 JSON 输入；解析歧义与网关一致性。 |
| 复现条件 | 请求体包含多个 JSON 对象或错误 Content-Type。 |
| 关联模块 | `internal/identity/transport/rest/handler.go`、`transport/c/handler.go`。 |
| 责任角色 | identity 后端（主责）；测试（解析回归）。 |
| 里程碑 | M1 单测；M2 集成；M3 回归。 |
| 修复方案 | 统一严格解码工具：校验 `Content-Type: application/json`、拒绝尾随非空内容、未知字段保持拒绝；A/B/C 共用。 |
| 验证标准 | 尾随内容 / 错误 Content-Type 返回 40000；合法请求不受影响；三端一致。 |
| 状态 | 已修复（M1/M2，待 M3 回归） |

## 4. 季度工程债 backlog（不在本轮周期）

| ID | 问题 | 责任角色 | 排期 |
|---|---|---|---|
| PERF-04 | 单账号活跃设备上限未严格生效 | identity 后端 | 季度 |
| PERF-05 | 限流默认值与真实网络拓扑不匹配 | identity 后端 + 平台运维 | 季度 |
| PERF-06 | 外部 IdP 超时 / 熔断 / 配额 | identity 后端 | 季度 |
| PERF-07 | 账号列表关键词全表扫描 | identity 后端 | 季度 |
| PERF-08 | 容量指标缺失 | 平台运维 | 季度 |
| OPS-03 | SLO 缺 SLI 直方图与告警 | 平台运维 | 季度 |
| OPS-04 | 审计 Outbox 积压不可观测 | 平台运维 | 季度 |
| OPS-05 | HPA 仅按 CPU | 平台运维 | 季度 |
| OPS-06 | 扩缩容本地状态依赖 | 平台运维 | 季度 |
| OPS-07 | 供应商熔断状态不可监控 | 平台运维 | 季度 |
| MNT-02 | 大文件职责拆分与 DTO 收敛 | identity 后端 | 季度 |
| MNT-03 | identity 分层覆盖率门禁 | identity 后端 + CI | 季度 |
| MNT-04 | 文档与指标口径同步 | identity 后端 | 季度 |
| MNT-05 | 关键业务参数可配置化 | identity 后端 | 季度 |
| MNT-06 | 在线表与归档表结构一致 | identity 后端 | 季度 |
| BIZ-01 | C 端个人资料能力 | 产品 + identity 后端 | 季度 |
| BIZ-02 | 多身份绑定 / 合并 | 产品 + identity 后端 | 季度 |
| BIZ-03 | MFA 按端 / 租户配置 | 产品 + identity 后端 | 季度 |
| BIZ-04 | 租户级密码策略 | 产品 + identity 后端 | 季度 |
| BIZ-05 | 国际化文案与时间口径 | 产品 + identity 后端 | 季度 |
| BIZ-06 | merchant_id 规范化 | identity 后端 | 季度 |
| BIZ-07 | 手机号本地格式规范化 | 产品 + identity 后端 | 季度 |
| BIZ-08 | 边界场景产品策略 | 产品 + identity 后端 | 季度 |
| BIZ-09 | 多租户能力边界 | 产品 | 季度 |

## 5. 进度同步与阻塞处理

- 每日同步：按问题更新「状态 / 当前里程碑 / 阻塞项 / 下一步」。
- 阻塞升级：配置项、生产 overlay、迁移窗口、密码历史表容量等依赖，由平台运维当日拉通。
- 回归入口：10 项全部 M1/M2 后统一提交测试团队，按第 3 节验证标准逐项回归。

## 6. 变更记录

| 日期 | 内容 |
|---|---|
| 批次启动 | 确认范围：本轮 10 项，25 项季度 backlog；登记责任角色与里程碑 |
| M1/M2 | 10 项代码、单测与配置校验完成；全量 `go test ./...`、`go vet`、契约门禁通过 |
| M3 | 待测试团队按第 3 节验收标准回归，完成后归档汇总报告 |
