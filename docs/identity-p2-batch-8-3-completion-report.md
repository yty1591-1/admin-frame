# identity P2 修复完成报告（第 8.3 批次）

> 范围：[identity-enterprise-production-gap-review.md](./identity-enterprise-production-gap-review.md) §8.3。
> 登记册：[identity-p2-batch-8-3-register.md](./identity-p2-batch-8-3-register.md)。
> 状态：本轮 10 项 P2 已完成代码、单测与配置校验（M1/M2）；测试团队回归（M3）待执行。
> 其余 25 项按报告标注进入季度工程债 backlog，不在本轮周期。

## 1. 结果概览

| ID | 问题 | 修复状态 | 关键验证 |
|---|---|---|---|
| REL-03 | singleflight 回源复用首个调用方 ctx | 已修复 | 首请求取消后等待者仍成功，回源仅 1 次 |
| REL-06 | 并发刷新误判重放并撤销整链 | 已修复 | 宽限内不撤销；超窗仍撤销并审计 |
| REL-07 | OAuth 映射冲突产生 503 / 孤儿账号 | 已修复 | 冲突回查复用；新账号软删 |
| REL-08 | 停机中断在途清理批次 | 已修复 | Stop 等待批次完成并有超时 |
| SEC-03 | 认证端点无独立请求体上限 | 已修复 | 16KiB 上限在 Handler 前拒绝；关键字段预校验 |
| SEC-04 | refresh / logout 无限流 | 已修复 | IP + 主体双维度 42900，Retry-After |
| SEC-06 | 历史密码可循环复用 | 已修复 | 窗口内复用 40000；历史只存哈希 |
| SEC-08 | 账号列表读取无审计 | 已修复 | 每次列表读取写 `auth.account.read` |
| SEC-09 | 短信无生产硬预算 | 已修复 | prod/staging 缺预算启动失败 |
| SEC-10 | JSON 解析不严格 | 已修复 | 尾随内容 / 错误 Content-Type 返回 40000 |

## 2. 逐项修复与验证

### 2.1 REL-03 撤销回源 context 解耦

- `revocation.go`：singleflight 内部使用 `context.WithoutCancel` + 5s 独立超时；调用方拿到结果后自行判断取消。
- 用例 `TestRevocationRefetchDecoupledFromCallerCancel`：首请求取消后第二请求成功，回源调用计数为 1。

### 2.2 REL-06 并发刷新短宽限

- `auth_service.go`：新增 `RefreshGrace`；已轮换令牌在窗口内只返回 40100，不撤销整链；Save 冲突同样按宽限处理。
- 配置：`identity.session.refresh_grace`，默认 10s，上限 60s。
- 用例 `TestRefreshGraceAvoidsChainRevocation`：宽限内设备链保持 active；超窗撤销并产生 1 条 token.reused。

### 2.3 REL-07 OAuth 映射冲突治理

- `oauth.go`：映射 Save 冲突时回查既有映射并复用账号；本次新建但未建立映射的账号在同一事务内软删。
- 用例 `TestOAuthProvisionConflictReusesExistingAccount`：冲突回调复用既有账号，库内只留 1 个可用账号 + 1 个软删孤儿。

### 2.4 REL-08 停机等待清理批次

- `runtime.go`：`StartCleanup` 返回 `CleanupHandle`；`Stop` 停止调度并等待当前批次（独立 2 分钟超时 context）。
- `cmd/worker`：消费循环结束后调用 `Stop`，超时记录告警。
- 用例 `TestCleanupHandleStopWaitsForInFlightBatch`：正常等待与超时兜底均通过。

### 2.5 SEC-03 认证请求体上限

- `transport/rest`：新增 `AuthBodyLimit`（16KiB），Content-Length 超限在 Handler 前返回 40000；A/B/C 三端认证路由统一接入。
- `app`：新增 subject / password / refresh_token / OAuth code / state 长度上限，在 Argon2 与外部调用前拒绝。
- 用例 `TestAuthBodyLimitRejectsOversizedBeforeHandler`；三端路由测试保持通过。

### 2.6 SEC-04 refresh / logout 端点限流

- 配置：`identity.refresh_rate_limit`（默认 10 rps / burst 20，prod/staging fail_closed）。
- `transport/rest`：按 IP + 主体短摘要双维度限流，超限返回 42900 并带 `Retry-After`。
- `values-identity-enabled.yaml`：biz / c overlay 显式启用 `rate_limit` 且 fail_closed。
- 用例 `TestCheckEndpointLimitRejectsRefreshAndLogout`、`TestSetRetryAfterRoundsUp`。

### 2.7 SEC-06 密码历史与复用限制

- 迁移 `000018_identity_password_history`：`identity_credential.password_history JSON NULL`，只存哈希。
- 领域：`Credential.RecordPasswordChange` / `RecentPasswordHistory`，按数量与窗口裁剪。
- 应用：自助改密、管理员重置、强制改密均校验历史并记录旧哈希。
- 配置：`identity.password.history_count`（默认 5）、`history_window`（默认 365 天）。
- 用例 `TestAccountServicePasswordHistoryRejectsReuse`：A→B→C 后回退 A 返回 40000，历史保留 2 条。

### 2.8 SEC-08 列表读取审计

- `AccountService.List` 增加 `Actor`，成功后写 `auth.account.read`，attrs 标记 `scope=list` 与分页 / 状态；不记录主体原文。
- 用例 `TestAccountServiceListAndGet` 断言 2 条列表审计与 `keyword_present`。

### 2.9 SEC-09 短信生产硬预算

- `config.go`：prod/staging 启用短信时 `daily_budget` 必须 > 0。
- 测试基线补充预算值，新增“缺预算”失败用例；示例配置注释同步。
- `cmd/internal/identityruntime`、`cmd/worker` 编译与配置测试通过。

### 2.10 SEC-10 严格 JSON 解析

- `transport/rest`：导出统一 `DecodeJSON` / `DecodeOptionalJSON`；校验 Content-Type、拒绝尾随内容、保留未知字段拒绝。
- `transport/c`：删除重复实现，改用共享解码。
- 用例 `TestDecodeJSONStrict`、`TestDecodeOptionalJSONEmptyBody`。

## 3. 验证记录

| 检查 | 命令 | 结果 |
|---|---|---|
| 全量编译 | `go build ./...` | 通过 |
| 全量测试 | `go test ./...` | 通过（含 identity / platform / cmd / integration 包） |
| 静态检查 | `go vet ./internal/identity/... ./internal/platform/... ./cmd/...` | 通过 |
| 契约门禁 | `bash scripts/ci/check-authn.sh` | 通过 |
| OpenAPI 校验 | `bash scripts/ci/check-openapi.sh` | 通过 |

说明：
- 本机 Windows 无 CGO，`-race` 由 CI 执行。
- SEC-06 迁移与 SQL 集成用例依赖 `TEST_MYSQL_DSN`，需在 staging 演练库执行。
- SEC-09 生产配置若启用短信但未设置 `daily_budget` 将无法启动，属预期门禁。

## 4. 提交清单

| 提交 | 说明 |
|---|---|
| `bf7fde0` | `fix(identity): 生产强制短信日预算` |
| `1366e2b` | `fix(identity): 解耦撤销回源与请求取消` |
| `db0619f` | `fix(identity): 停机等待在途清理批次` |
| `c2eb09c` | `fix(identity): 账号列表读取写入审计` |
| `80b5d37` | `fix(identity): 统一严格 JSON 请求解析` |
| `5b08c49` | `fix(identity): 收敛认证请求体上限与关键字段长度` |
| `b71f967` | `fix(identity): 增加刷新与登出端点限流` |
| `c234e49` | `fix(identity): 并发刷新增加短宽限窗口` |
| `ba0e705` | `fix(identity): 复用已存在的 OAuth 映射账号` |
| `9882ac6` | `fix(identity): 增加密码历史与复用限制` |

## 5. 风险、兼容与回滚

| 项 | 说明 |
|---|---|
| SEC-06 迁移 | 新增可空 JSON 列，旧行无需回填；回滚删除列，历史限制失效。 |
| SEC-04 配置 | 生产 Redis 故障默认失败关闭；需确认 Redis 冗余与限流参数。 |
| REL-06 宽限 | 宽限窗口不延长绝对到期；设为 0 可恢复严格重放策略。 |
| REL-07 软删 | 冲突新建账号被软删，不参与登录；可纳入既有清理任务。 |
| SEC-09 预算 | 生产必须显式设置预算，否则启动失败；上线前需与运维确认额度。 |
| 未覆盖 | PERF-04~08、OPS-03~07、MNT-02~06、BIZ-01~09 已登记季度 backlog。 |

## 6. 测试团队回归入口（M3）

按登记册第 3 节逐项验证，重点补充：

1. 首请求取消 / 断开的并发回源场景。
2. 并发刷新双击与超窗重放对照。
3. OAuth 并发回调与孤儿账号检查。
4. 停机时清理批次完成 / 超时日志。
5. `/auth/*` 超大请求、refresh/logout 限流与 Retry-After。
6. 密码历史 A→B→A 回退、管理员重置与强制改密链路。
7. 列表读取审计按 actor/trace 检索。
8. 生产配置缺短信预算的启动失败；严格 JSON 的尾随内容 / Content-Type 用例。
