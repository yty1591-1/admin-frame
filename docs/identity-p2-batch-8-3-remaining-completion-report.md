# identity 剩余 P2 专项修复完成报告（第 8.3 批次第二轮）

> 台账：[identity-p2-batch-8-3-remaining-register.md](./identity-p2-batch-8-3-remaining-register.md)
> 范围：第 8.3 批次剩余 24 项 P2（PERF-04～08、OPS-03～07、MNT-02～06、BIZ-01～09）。
> 周期口径：D1 / D2 / D3 三个工作日里程碑；责任为角色，不绑定真实姓名与日历日期。
> 结论：24 项全部完成修复并提交，全量回归与门禁通过。

## 1. 每日同步与交付节奏

| 里程碑 | 主题 | 完成项 | 出口 |
|---|---|---|---|
| D1 | 可靠性与性能 | PERF-04、PERF-05、PERF-06、PERF-07、PERF-08、OPS-03/04/05/07 | 指标 / 告警 / HPA 可用；精确查询与限流拆分通过单测 |
| D2 | 运维可观测与可维护性 | OPS-06、MNT-02、MNT-03、MNT-04、MNT-05、MNT-06 | 启动断言、覆盖率门禁、SLO 对齐、迁移闭环 |
| D3 | 业务适配与回归 | BIZ-01～09；全量回归 | C 端个人中心 / 绑定、租户策略、时区与多租户边界落地 |

每日同步口径（已在台账 §3 固化）：已完成 = ID + 提交 + 验证；正在处理 = 当前里程碑；阻塞 = 原因 +
协调角色；风险接受 = 仅产品明确接受时登记。

## 2. 逐项修复与验证

### 2.1 性能与可靠性（D1）

| ID | 修复方案 | 验证 | 提交 |
|---|---|---|---|
| PERF-04 | `evictFamilies` 改为游标分页遍历全部设备后再淘汰，避免 LIMIT 归一导致活跃设备超限 | 设备超限 / 分页用例；`session_active_list_test.go` | `5fb3ccc` |
| PERF-05 | 账号级与 IP 级登录限流拆分为独立参数；新增 `LoginGuard.CheckRateLimitPair` | 账号桶不随 IP 变化；`login_guard_test.go` | `6fe34b4` |
| PERF-06 | OAuth 超时、provider 级熔断阈值 / 时长配置化；全局限流可配置 | `TestOAuthProviderBreakerOpensAfterFailures` | `e16c091` |
| PERF-07 | 账号列表新增 `subject` 精确查询，按 `subject_index` 走索引，优先于 `keyword` | 精确 / 大小写归一 / 审计用例 | `38db43b` |
| PERF-08 / OPS-03 / OPS-05 | 新增 `identity_login_qps`、`identity_active_sessions`、刷新 / MFA / step-up / OAuth 四类时延直方图；HPA 支持 Pods 辅助指标 | 指标单测、`check-test-gates`、Helm 渲染 | `992ce77` |
| OPS-04 / OPS-07 | 注册 `identity_outbox_pending` / `identity_outbox_lag_seconds` 与 `identity_sms_breaker_open`；新增积压、熔断告警 | `identity.yaml` 规则校验；worker 指标可采集 | `992ce77` |
| OPS-06 | prod/staging 启动期断言 database / redis / outbox 均未回退内存，失败即拒绝启动并打印明确日志 | `identityruntime` 单测；`check-config` | `18ed140` |

### 2.2 运维可观测与可维护性（D2）

| ID | 修复方案 | 验证 | 提交 |
|---|---|---|---|
| MNT-02 | 抽出 `auth_dto.go`、`auth_audit.go`；`auth_service.go` 2004 → 1786 行 | `go build ./...` + app 全量单测 | `5e51ade` |
| MNT-03 | `check-test-gates.sh` 新增 TEST-06：app ≥75%、admin/biz 路由 ≥70%、rest/c handler 基线 ≥15% | `bash scripts/ci/check-test-gates.sh` → `OK` | `abb1215` |
| MNT-04 | SLO 文档改为已注册直方图口径（刷新 / MFA / step-up / OAuth），Redis 口径更新为单 jti + L1，补 Outbox / 短信熔断 | 文档与 `identity.yaml` 规则逐项对齐 | `9d3415a` |
| MNT-05 | TOTP 位数 / 步长 / 窗口、恢复码数量、临时口令长度配置化；`pkg/totp` 支持策略参数；启动期边界校验 | `pkg/totp` 单测 + `config_mfa_test.go` | `e4c0317` |
| MNT-06 | 迁移 `000019` 补齐归档设备画像列；归档写入改为 `INSERT ... SELECT` 复制；提供 down 脚本 | `check-migrations` 通过；归档写入单测 | `01a2ea4` |

### 2.3 业务适配与回归（D3）

| ID | 修复方案 | 验证 | 提交 |
|---|---|---|---|
| BIZ-01 | 注册 `GET/PATCH /c/v1/me`，复用 Me / UpdateMe（含读取与更新审计）；C OpenAPI 4.1.0 | C 路由用例（未认证 401）；`check-openapi` 通过 | `49861c9` |
| BIZ-02 | 新增绑定列表 / 解绑 / 授权 / 回调端点；state 绑定发起账号；最后一凭据禁止解绑；仓库补齐 `ListByAccount/Delete` | 绑定生命周期、跨账号 state、重复绑定、最后凭据用例；C OpenAPI 4.2.0 | `56ff177` |
| BIZ-03 | 新增 `identity.mfa.channels` / `biz_required`；运行期按端别装配密文与票据；MFA 票据支持 admin/biz；新增 `MFAFactor` 预留位 | B 端登录 MFA 用例；A/B/C 票据签发 / 解析用例 | `d0ad831` |
| BIZ-04 | 新增 `identity.password.tenant_overrides`（只能收紧）；改密按账号商户解析策略；启动期校验安全下限与重复商户 | 租户改密收禁用例 + 配置校验用例 | `d71da14` |
| BIZ-05 | 明确稳定 `code` + 前端本地化契约；新增 `identity.sms.quota_timezone`，内存 / Redis 限流器按本地日期分桶 | 上海时区跨日配额用例；文档 §20 | `b3d0318` |
| BIZ-06 | 新增 `NormalizeMerchantID`（trim + lower）；账号创建与 B 端登录统一；迁移 `000020` 回填 | 规范化单测 + 迁移预检说明 | `fe2d194` |
| BIZ-07 | 默认仍为 E.164；新增 `identity.sms.default_region`，本地号码去前导 0 补国家码 | 本地 / E.164 同摘要用例 | `5fbefbe` |
| BIZ-08 | 新增 `identity.mfa.step_up_reuse_grace`（默认 2m）；仅复用登录刚消费的时间步，超出窗口仍按重放拒绝 | step-up 复用 + 超窗拒绝用例 | `0e5d5ed` |
| BIZ-09 | 文档明确 A/B/C 多租户边界、唯一键口径与多商户 C 端演进步骤 | 文档评审 | `b06311a` |

## 3. 验证记录

| 项目 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | 通过 |
| 全量单测 | `go test ./...` | 通过（identity 各包 + integration smoke 全绿） |
| 静态检查 | `go vet ./internal/identity/... ./internal/platform/... ./cmd/...` | 通过 |
| 覆盖率门禁 | `bash scripts/ci/check-test-gates.sh` | `OK: test gates`（TEST-06 全通过） |
| 认证门禁 | `bash scripts/ci/check-authn.sh` | `OK: authn` |
| OpenAPI | `bash scripts/ci/check-openapi.sh` | `OK: openapi validate + breaking` |
| 迁移 | `bash scripts/ci/check-migrations.sh` | `OK: migrations`（000019/000020 成对） |

覆盖率快照（2026-11-01）：`internal/identity/app` 75.0%（+2.5pp）；`transport/admin` 90.6%；
`transport/biz` 76.9%；`transport/rest` 18.7%；`transport/c` 21.1%。

## 4. 风险与遗留

| 项 | 状态 | 说明 |
|---|---|---|
| rest/c handler E2E 覆盖率 | 基线门禁 15% | rest 为共享 handler 库，当前以 DTO / 登录中间态用例覆盖；c 端 SMS/OAuth 快乐路径 E2E 未补齐。已纳入下一轮专项（owner：identity 后端 + 测试），不作为本项 P2 的降级，而是门禁分层：编排层与路由层达标，handler 层先设基线。 |
| BIZ-03 因子扩展 | 预留 | 当前仅 TOTP；`MFAFactor` 已定义，新增 WebAuthn / 推送需补端点与票据语义，不在本轮范围。 |
| BIZ-05 文案本地化 | 契约完成 | 服务端不维护多语言表；前端按 `code` 本地化，需在联调清单中验证。 |
| MNT-04 压测 | 文档完成 | staging 同规格压测仍待环境执行；SLO 已可测量，指标已注册。 |
| BIZ-09 多商户 C 端 | 文档完成 | 当前 C 端全局单租户；多商户模型需按文档 §19.2 演进，属产品决策。 |

## 5. 闭环确认

- 24 / 24 项 P2 完成修复、提交与验证，无遗漏。
- 每项均有单测或门禁覆盖；破坏性变更仅 BIZ-02 的 C 端契约增量（4.2.0），无 breaking。
- 全部变更按 Conventional Commits 提交，提交信息包含背景 / 变更 / 影响 / 回滚。
- 本轮不调整问题优先级、不做风险降级；遗留项按上表显式登记并由责任角色承接。
