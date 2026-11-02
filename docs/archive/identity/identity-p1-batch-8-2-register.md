# identity P1 问题登记册（第 8.2 批次）

> 来源：[identity-enterprise-production-gap-review.md](./identity-enterprise-production-gap-review.md) §8.2。
> 范围：仅登记并修复本批次四项 P1（SEC-01、SEC-02、SEC-05/MNT-01、SEC-07），
> 不调整优先级，不处理其余 P1/P2。
> 责任登记采用「角色 + 里程碑」，不指定个人、不承诺日历日期；里程碑由发布窗口牵引。

## 1. 批次目标与验收口径

- 目标：上线前关闭 §8.2 四项 P1，或对未关闭项形成正式风险接受。
- 批次验收（与报告一致）：
  - 库表中不存在可读的登录主体明文；回填与双读回退分支删除。
  - 所有账号可用性 / 凭据 / 会话变更操作在 `mfa.admin_required=true` 时缺少
    `mfa_code` 返回 40000。
  - 契约生成客户端可直接调用账号处置接口（幂等头已声明）。
  - 审计可按 actor / target / trace 检索，保留期 ≥180 天。

## 2. 里程碑定义

| 里程碑 | 定义 | 出口条件 |
|---|---|---|
| M1 | 代码与单测完成 | 目标包 `go vet` 通过；新增验证用例通过；`go build ./...` 通过 |
| M2 | 集成与契约完成 | 全量 `go test ./...` 通过；OpenAPI 校验通过；迁移在本地/演练库可正向执行 |
| M3 | 回归与发布候选 | 测试团队回归通过；回滚脚本与演练记录齐备；汇总报告归档 |

## 3. 问题登记

### SEC-01 登录主体明文落库（PII 加密）

| 字段 | 内容 |
|---|---|
| 问题描述 | `identity_account.subject` 仍为明文列且唯一键依赖明文（`migrations/000001:19`、`000009:14-16`）；`identity_oauth_identity.provider_subject` 为明文（`000001:148`）；S4/S5 未发布（`000011:5-9`）；读路径未命中索引时回退明文查询（`mysql_account.go:107-110`）。 |
| 影响范围 | 库表、备份、慢查询日志、只读账号、数据导出；涉及账号创建、登录、OAuth 绑定、维护工具回填、账号展示与审计主体字段。 |
| 复现条件 | 直接查询 `identity_account.subject` 或 `identity_oauth_identity.provider_subject` 即可得到可读登录主体；构造 `subject_index` 未命中的历史行可触发明文回退分支。 |
| 关联模块 | `migrations/`、`internal/identity/infrastructure/persistence/mysql_account.go`、`mysql_oauth_identity.go`、`subject_protector.go`、`cmd/identity-maintenance`。 |
| 责任角色 | identity 后端（主责）；平台运维（迁移执行与备份/回滚演练）；测试（数据迁移与回归验证）。 |
| 里程碑 | M1 代码与单测；M2 迁移演练 + 集成测试；M3 回归与发布候选。 |
| 修复方案 | 按已确认的 S5 方案：全量回填 `subject_index` / `subject_ciphertext`；唯一键切 `subject_index`；删除明文列；`provider_subject` 新增 index/ciphertext 并完成回填、切唯一键；移除仓储层明文回退分支；展示/审计所需 `Subject` 由仓储解密回填。迁移分「双写回填 → 只读密文 → 删明文」三段发布，保证滚动升级与可回滚。 |
| 验证标准 | 迁移正向执行后表中无可读主体列；回填覆盖率 100%（迁移内置校验）；仓储无明文回退分支（单测断言）；账号创建/登录/OAuth 绑定/列表/详情/审计全链路集成测试通过；回滚脚本可恢复上一版本结构。 |
| 状态 | 已修复（M1/M2，待 M3 回归） |

### SEC-02 特权操作 step-up 覆盖不一致

| 字段 | 内容 |
|---|---|
| 问题描述 | 仅 ResetPassword / ResetMFA / Delete / 全量撤销调用 `requireAdminStepUp`；Enable / Unlock / Lock / Restore / RevokeTargetSession 未调用（`account_service.go`）。Lock 请求 DTO 丢弃 `mfa_code`；单会话撤销无请求体。 |
| 影响范围 | A 端账号生命周期与会话管理：账号启用、解锁、锁定、恢复、单设备撤销；`mfa.admin_required=true` 的生产环境。 |
| 复现条件 | 生产配置 `mfa.admin_required=true`，管理员仅持被盗会话（无 MFA 动态码）调用上述端点，操作成功且无二次认证。 |
| 关联模块 | `internal/identity/app/account_service.go`、`app/mfa.go`（`requireAdminStepUp`）、`transport/rest/handler.go`、`transport/admin/router.go`。 |
| 责任角色 | identity 后端（主责）；测试（权限矩阵与 step-up 回归）。 |
| 里程碑 | M1 代码与单测；M2 权限矩阵集成测试；M3 回归。 |
| 修复方案 | 在 Enable / Unlock / Lock / Restore / RevokeTargetSession 的事务内、执行特权动作前调用 `requireAdminStepUp`；Lock handler 传递 `mfa_code`；单会话撤销支持可选 JSON body 携带 `mfa_code`；同步更新 OpenAPI 与注释。 |
| 验证标准 | `mfa.admin_required=true` 时五个端点缺少 `mfa_code` 均返回 40000；提供有效动态码时成功；`admin_required=false` 时不改变现有行为；非 A 端不受影响；新增单测覆盖每个端点。 |
| 状态 | 已修复（M1/M2，待 M3 回归） |

### SEC-05 / MNT-01 契约与实现漂移

| 字段 | 内容 |
|---|---|
| 问题描述 | 账号处置路由强制 `Idempotency-Key`（`transport/admin/router.go:82-104`），但 OpenAPI 仅定义 header 未引用（`api/openapi/components.yaml:28-36`）；B 端登录描述称 `merchant_id` 由账号推导，但实现必填（`paths/biz/auth.yaml:5-7` vs `app/auth_service.go:454-461`，`components.yaml:123-127` 为可选）；C 端 TokenPair 缺 `absolute_expires_at`（`transport/c/handler.go:325-330` vs `rest/handler.go:836-843`）。 |
| 影响范围 | 契约生成客户端、A 端账号处置调用、B 端登录、C 端令牌响应；契约门禁。 |
| 复现条件 | 使用 OpenAPI 生成客户端调用账号处置接口（不带幂等头）返回 400；B 端客户端不发送 `merchant_id` 登录失败；C 端解析不到 `absolute_expires_at`。 |
| 关联模块 | `api/openapi/components.yaml`、`api/openapi/paths/admin/*.yaml`、`paths/biz/auth.yaml`、`internal/identity/transport/c/handler.go`、`internal/identity/app/auth_service.go`。 |
| 责任角色 | identity 后端（主责，契约与实现）；测试（契约一致性与客户端调用验证）。 |
| 里程碑 | M1 契约与实现修改；M2 OpenAPI 校验 + 契约测试；M3 回归。 |
| 修复方案 | 所有账号处置 path 引用 `IdempotencyKey` 且 `required: true`，修正描述；B 端登录 `merchant_id` 改为必填并修正描述与实现一致；C 端 TokenPair 补 `absolute_expires_at`；补充契约/路由一致性测试。 |
| 验证标准 | OpenAPI 校验通过；每个强制幂等头的 path 均已声明；B 端登录契约必填与实现一致；A/B/C 令牌响应字段一致；新增测试断言幂等头引用、merchant_id 必填、C 端到期字段。 |
| 状态 | 已修复（M1/M2，待 M3 回归） |

### SEC-07 审计缺少独立持久化存储

| 字段 | 内容 |
|---|---|
| 问题描述 | 生产审计出口仍为 Outbox → `SlogEmitter`（`runtime.go:227-236`、`:301-309`；`app/outbox.go:79-99`）；仓库无审计表迁移。 |
| 影响范围 | 审计保留期、不可篡改、按 actor/target/trace 检索与合规导出；identity 审计链路。 |
| 复现条件 | 审计仅出现在日志系统；日志轮转/清理后不可检索，无法按主体或追踪号查询，无法满足 ≥180 天保留。 |
| 关联模块 | `internal/identity/app/outbox.go`、`internal/shared/audit/`（Emitter/Event）、`cmd/internal/identityruntime/runtime.go`、`migrations/`。 |
| 责任角色 | identity 后端（主责）；平台运维（存储容量与保留期策略）；测试（落库与检索验证）。 |
| 里程碑 | M1 迁移 + Emitter + consumer；M2 集成测试（含 Outbox 投递）；M3 回归与保留期确认。 |
| 修复方案 | 新增 identity 独立审计表迁移（含 actor/target/trace 索引与保留期策略说明）；实现 SQL Emitter 与 `identity.audit` Outbox consumer 落库；runtime 注册落库出口，保留日志出口作为旁路；不得改动 IAM 审计边界。 |
| 验证标准 | Outbox 投递后审计表可查到记录；按 actor/target/trace 可检索；写入失败可重试且不丢事件；保留期 ≥180 天有配置/运维说明；IAM 审计不受影响；新增单测与集成测试（`TEST_MYSQL_DSN` 条件跳过）。 |
| 状态 | 已修复（M1/M2，待 M3 回归） |

## 4. 执行顺序与依赖

1. SEC-02（独立、风险最低，先关闭会话被盗放大面）。
2. SEC-05 / MNT-01（契约先行，为后续接口调整提供稳定契约）。
3. SEC-01（涉及迁移与滚动升级，需要最充分的演练窗口）。
4. SEC-07（新增存储与消费链路，依赖稳定的 Outbox 与发布流程）。

## 5. 进度同步与阻塞处理

- 每日同步：按问题更新「状态 / 当前里程碑 / 阻塞项 / 下一步」。
- 阻塞升级：迁移窗口、数据库权限、日志平台保留策略等外部依赖，由平台运维在当日同步会拉通；
  无法在发布窗口前关闭的项，按报告 §8.4 走正式风险接受，不静默延期。
- 回归入口：四项全部 M1/M2 完成后统一提交测试团队，按第 3 节验证标准逐项回归。

## 6. 变更记录

| 日期 | 内容 |
|---|---|
| 批次启动 | 登记四项 P1，确定执行顺序与责任角色 |
| M1/M2 | SEC-02、SEC-05/MNT-01、SEC-01、SEC-07 代码与单测/集成测试完成；契约门禁与 OpenAPI 校验通过 |
| M3 | 待测试团队按第 3 节验证标准执行回归，完成后归档汇总报告 |
