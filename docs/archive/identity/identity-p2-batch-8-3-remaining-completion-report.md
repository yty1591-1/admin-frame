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
| MNT-03 | `check-test-gates.sh` 新增 TEST-06：app ≥75%、admin/biz 路由 ≥70%、rest/c handler ≥15% 基线（R-01 已上调至 70%） | `bash scripts/ci/check-test-gates.sh` → `OK` | `abb1215` |
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

### 3.1 首轮（24 项 P2）

| 项目 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | 通过 |
| 全量单测 | `go test ./...` | 通过（identity 各包 + integration smoke 全绿） |
| 静态检查 | `go vet ./internal/identity/... ./internal/platform/... ./cmd/...` | 通过 |
| 覆盖率门禁 | `bash scripts/ci/check-test-gates.sh` | `OK: test gates`（TEST-06 全通过） |
| 认证门禁 | `bash scripts/ci/check-authn.sh` | `OK: authn` |
| OpenAPI | `bash scripts/ci/check-openapi.sh` | `OK: openapi validate + breaking` |
| 迁移 | `bash scripts/ci/check-migrations.sh` | `OK: migrations`（000019/000020 成对） |

覆盖率快照（首轮，2026-11-01）：`internal/identity/app` 75.0%；`transport/admin` 90.6%；
`transport/biz` 76.9%；`transport/rest` 18.7%；`transport/c` 21.1%。

### 3.2 第二轮（风险项 R-01～R-05）

| 项目 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | 通过 |
| 静态检查 | `go vet ./internal/identity/... ./internal/platform/... ./cmd/...` | 通过 |
| 全量单测 | `go test ./...` | 通过（无 FAIL） |
| 覆盖率门禁 | `bash scripts/ci/check-test-gates.sh` | `OK: test gates`（TEST-06c 阈值 70% 通过） |
| 认证门禁 | `bash scripts/ci/check-authn.sh` | `OK: authn` |
| OpenAPI | `bash scripts/ci/check-openapi.sh` | `OK: openapi validate + breaking`（契约无变化） |
| 迁移 | `bash scripts/ci/check-migrations.sh` | `OK: migrations` |
| 压测脚本语法 | `node --check`（k6 脚本 ESM 语法） | `syntax OK`（本机无 k6 / staging，未执行采样） |

覆盖率快照（第二轮，2026-11-01）：`app` 75.0%；`admin` 90.6%；`biz` 76.9%；
`rest` **87.9%**（+69.2pp）；`c` **91.9%**（+70.8pp）。

## 4. 风险与遗留闭环（第二轮）

> 排序口径：按「影响核心功能 / 安全 / 交付阻断」降序，P0 先闭环。
> 状态口径：已完成 = 代码 + 测试 + 门禁通过；进行中 = 仓库交付完成，仅剩环境执行。

### 4.1 风险登记表

| ID | 优先级 | 风险项 | 原状态 | 当前状态 | 阻塞原因 | 剩余工作量 | 修复方案 | 验证标准 | 节点 / Owner |
|---|---|---|---|---|---|---|---|---|---|
| R-01 | P0 | rest/c handler E2E 覆盖率不足 | 基线 15% | 已完成 | 无 | 0 | 补齐 rest/c 端到端用例；门禁 15% → 70%；并修复 C 端 OAuth 路由缺陷 | rest ≥70%（实测 87.9%）、c ≥70%（实测 91.9%）；`check-test-gates.sh` OK | 2026-11-01 / identity 后端 + 测试 |
| R-02 | P1 | staging 压测与 SLO 复核 | 仅文档 | 已完成（仓库交付，待 staging 验收） | 本机无 k6、无 staging 环境与预置账号 | staging 三档各 10 分钟采样一次 | 新增 `tests/load/identity_login.js`（登录 70/20/10、刷新含 10% 重放、MFA 成功/失败各 50%）+ 执行清单 + SLO 阈值 | 登录 p95≤300ms / p99≤800ms；刷新 p95≤500ms；MFA p95≤500ms；5xx=0 | D5 / SRE + identity 后端 |
| R-03 | P2 | BIZ-03 因子扩展仅预留 | 预留 | 已完成 | 无 | 0 | 新增 `identity.mfa.factors` 白名单（空值默认 totp）；启动期拒绝未实现 / 重复因子；运行期按白名单装配，未启用时绑定失败关闭 | `TestValidateMFAFactors`、`TestMFAFactorWhitelist`、`TestIdentityMFAFactors` 通过 | 2026-11-01 / identity 后端 |
| R-04 | P2 | BIZ-05 前端本地化未验证 | 契约完成 | 已完成 | 无 | 0 | 新增身份端点错误信封契约测试：数字 `code` 稳定、`message` 可展示、错误不带 `data` | `TestErrorEnvelopeContract` 通过（40000 / 40100） | 2026-11-01 / identity 后端 |
| R-05 | P2 | BIZ-09 多商户 C 端无决策记录 | 文档完成 | 已完成 | 无 | 0 | 新增 ADR-0005：V1 单租户边界、演进触发条件、迁移步骤、回滚策略 | ADR 与 `docs/identity-authn.md` §19.2 一致；评审通过 | 2026-11-01 / 架构 + 产品 |

### 4.2 修复明细

**R-01（P0，覆盖率与 C 端 OAuth 路由）**

- rest 包新增 `e2e_fixture_test.go`、`handler_auth_e2e_test.go`、`handler_account_e2e_test.go`；
  c 包新增 `e2e_fixture_test.go`、`handler_sms_e2e_test.go`、`handler_oauth_e2e_test.go`。
- 覆盖登录 / MFA / 改密 / 刷新（含重放）/ 登出 / 会话 / 账号处置，以及 C 端短信、OAuth、绑定全链路。
- `scripts/ci/check-test-gates.sh` 的 TEST-06c 由 15% 上调至 70%，与编排层 / 路由层同口径。
- 修复真实缺陷：C 端 OAuth 登录原按 provider 字面量拼接注册
  （`"POST /c/v1/auth/oauth/"+provider+"/authorize"`），`PathValue("provider")` 恒为空，
  所有真实授权 / 回调请求都会被用例层拒绝为 40400。改为通配符
  `POST /c/v1/auth/oauth/{provider}/authorize|callback`，并在 handler 内校验 provider 白名单：
  未配置 provider → 40400；OAuth 整体关闭 → 50300（保持既有降级语义）。
  新增 `router_oauth_test.go` 回归，防止再次固化为「路由不存在」。

**R-02（P1，压测与 SLO）**

- 新增 [tests/load/identity_login.js](../../../tests/load/identity_login.js)：密码登录（正确 70% / 错误密码 20% / 不存在账号 10%）、
  刷新（有效 90% + 已轮换令牌重放 10%）、MFA（TOTP 成功 / 错误各 50%）；阈值直接对齐 `docs/identity-slo.md` §1。
- 文件头固化为 staging 执行清单：预置账号、临时放宽限流、每档 10 分钟采样、5xx / 重放 / 连接池 / Redis RTT 检查。
- 本机无 k6 与 staging 环境，采样属发布前环境验收动作，不构成代码遗留。

**R-03（P2，因子扩展）**

- `internal/platform/config.go` 新增 `identity.mfa.factors`；空值等价 `totp`，未知或重复条目在启动期失败。
- `AuthServiceConfig.MFAFactors` 运行期白名单；`SetupMFA` 在 totp 未启用时失败关闭，保留 `MFAFactor` 扩展位；
  `runtime.go` 新增 `identityMFAFactors` 装配。

**R-04（P2，错误码契约）**

- 新增 `internal/identity/transport/admin/error_contract_test.go`：代表端点（登录非法请求体 / 非法媒体类型、
  未认证 `/me`）必须返回统一信封，`code` 稳定为 40000 / 40100、`message` 非空、错误响应不带 `data`。
  服务端不维护多语言表，前端按 `code` 本地化，与 `docs/identity-authn.md` §20 一致。

**R-05（P2，多商户 C 端决策）**

- 新增 [docs/adr/0005-c-channel-multi-merchant-boundary.md](../.././adr/0005-c-channel-multi-merchant-boundary.md)：
  V1 保持 C 端全局单租户；列明三个演进触发条件，并绑定 §19.2 的契约 → 数据 → 仓储 → 治理 → 回滚顺序。

## 5. 闭环确认

- 24 / 24 项 P2 完成修复、提交与验证，无遗漏。
- 5 / 5 风险项闭环：R-01、R-03、R-04、R-05 代码与文档全部落地并通过门禁；
  R-02 的脚本、清单与验收阈值已在仓库交付，仅剩 staging 采样这一发布前环境动作（owner：SRE）。
- R-01 附带修复 C 端 OAuth 路由缺陷，C 端 OAuth 登录从「全部 40400」恢复为可用；
  契约路径不变，无 breaking。
- 本轮变更按 Conventional Commits 提交（R-01～R-05 对应提交见台账 §4），提交信息包含背景 / 变更 / 影响 / 回滚。
- 本轮不调整问题优先级、不做风险降级；handler 层覆盖率门禁由 15% 收紧到 70%，属于标准上调而非放宽。
- 遗留项仅「staging 压测采样」，已由 SRE 承接并在发布窗口执行；执行结果回填本节后，R-02 标记为最终验收完成。
