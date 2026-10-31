# identity 第 8.4 批次专项修复完成报告

> 台账：[identity-batch-8-4-register.md](./identity-batch-8-4-register.md)
> 范围：[identity-enterprise-production-gap-review.md](./identity-enterprise-production-gap-review.md) §8.4 四项风险。
> 结论：4 / 4 闭环；无风险接受降级；全量单测与门禁通过。

## 1. 结果概览

| ID | 问题 | 严重等级 | 原状态 | 当前状态 | 结论 |
|---|---|---|---|---|---|
| R-8.4-01 | Redis 故障导致 jti fail_closed 全量 503 | 严重 | 需风险接受 | 已修复 | 新增 jti 本地宽限，故障期不再全量 503 |
| R-8.4-02 | 审计仅日志存储 | 严重 | 需风险接受 | 已闭环（复核） | 8.2 SEC-07 已落独立审计表 + SQL 出口 |
| R-8.4-03 | PII 明文过渡期 | 严重 | 需风险接受 | 已闭环（复核） | 8.2 SEC-01 已删除明文列，只保留索引 / 密文 |
| R-8.4-04 | OAuth-only 账号仅 Bearer 注销 | 一般 | 需风险接受 | 已修复 | 注销要求最近登录，超窗需重新登录 |

## 2. 根因与修复

### 2.1 R-8.4-01 Redis 故障 jti 全量 503

根因：
- 账号 / 会话快照有数据库事实源，Redis 故障时可回源；jti 黑名单没有数据库事实源，
  熔断或读失败时只能按 `failure_mode=fail_closed` 返回 50300，导致 Redis 故障期间全部已认证请求不可用。

修复：
- `RevocationChecker` 新增 jti 本地宽限：正常路径校验 Redis 未撤销后，记录 `jti → 到期时间`；
  到期时间取 `min(token_grace, Access Token 剩余寿命)`。
- 仅在 Redis 读失败或熔断冷却期内启用本地宽限；超出宽限、令牌已过期或从未验证过的 jti 仍返回 50300。
- 新增配置 `identity.revocation.token_grace`：默认 30s，上限 60s，负数显式关闭；启动期校验上限。
- 本地记录按写入量（4096 次）惰性清理，避免无限增长；发现 jti 已撤销时立即删除本地记录。

代价与边界：撤销（登出 / 改密）在该副本上的可见延迟最多一个宽限窗口（默认 30s），
跨副本仍由既有 `state_ttl` 与 Redis 黑名单收敛；未放宽 `failure_mode` 默认值。

验证用例：
- `jti_grace_allows_recently_verified_token_during_outage`：故障 + 宽限内放行、超宽限 503、未验证 jti 503。
- `jti_grace_capped_by_token_expiry`：令牌剩余寿命短于宽限时以令牌到期为上限。
- `jti_grace_capped_by_config_max`：配置超上限被夹到 60s。
- `jti_grace_disabled_keeps_fail_closed`：关闭宽限后行为与旧版一致。
- `jti_grace_sweeps_expired_entries`：写入量触发过期清理。

### 2.2 R-8.4-02 审计仅日志存储（复核）

既有实现（8.2 SEC-07）：
- `migrations/000017_identity_audit_event`：追加写审计表，索引覆盖 actor / target / trace / occurred_at。
- `internal/shared/audit/sqlstore.go`：`SQLEmitter`（`ON DUPLICATE KEY UPDATE` 幂等）。
- `cmd/internal/identityruntime/runtime.go`：Outbox 消费出口为 `SQL 审计表 + 结构化日志`（`MultiEmitter`）；
  生产配置强制启用 async，因此审计不会只停留在日志。

验证：
- `go test ./internal/shared/audit/` 通过。
- `tests/integration/identity/audit_store_test.go`：落库、检索与重试幂等（无 `TEST_MYSQL_DSN` 时按约定跳过）。
- 本批次不再新增代码，仅确认风险已由 8.2 闭环。

### 2.3 R-8.4-03 PII 明文过渡期（复核）

既有实现（8.2 SEC-01）：
- `migrations/000015_identity_subject_s4`：新增 `subject_index` / `subject_ciphertext` 与 provider 对应列。
- `migrations/000016_identity_subject_s5`：把索引 / 密文列改为 `NOT NULL`（回填完整性门禁），
  并 `DROP COLUMN subject` / `provider_subject`；库表中不再存在可读登录主体明文。

验证：
- `go test ./tests/integration/identity/` 通过（无 DSN 用例跳过）。
- 既有 `TestProviderSubjectProtection`：索引确定性、跨 provider 隔离、AAD 绑定。
- 迁移门禁 `check-migrations.sh` 通过（15 / 16 / 17 成对）。

### 2.4 R-8.4-04 OAuth-only 仅 Bearer 注销

根因：
- 短信未启用或 OAuth-only 账号无手机号时，注销退化为「持有 Bearer 即可删除账号」；
  令牌被盗后可直接销号，缺少再认证环节。

修复：
- 新增配置 `identity.consumer.delete_reauth_window`：默认 10m，范围 [1m, 30m]。
- `AuthService.DeleteConsumerAccount`（无验证码路径）要求当前会话所属设备家庭在窗口内创建；
  家庭不存在、已撤销或创建时间超窗时返回 40100。
- 短信验证码路径调用新增的 `DeleteConsumerAccountVerified`，验证码本身即强再认证，不受窗口限制。
- 设备家庭创建时间只在真实登录时写入，刷新不更新，因此不能以「最近刷新」冒充重新登录。
- C 端 OpenAPI 升级至 4.3.0，注销端点补充再认证语义说明。

验证用例：
- `TestDeleteConsumerAccountRequiresRecentLogin`：超窗 40100、无会话 40100、家庭缺失 40100、设备已撤销 40100、最近登录成功软删。
- `TestE2ESMSDeleteAccountWithoutSMS`：最近登录 204；超窗 40100。
- `TestDeleteConsumerAccount`（既有）：短信验证码路径不回归。

## 3. 验证记录

| 项目 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | 通过 |
| 静态检查 | `go vet ./internal/identity/... ./internal/platform/... ./cmd/...` | 通过 |
| 全量单测 | `go test ./...` | 通过（无 FAIL） |
| 覆盖率门禁 | `bash scripts/ci/check-test-gates.sh` | `OK: test gates`（app 75.3%） |
| 认证门禁 | `bash scripts/ci/check-authn.sh` | `OK: authn` |
| OpenAPI | `bash scripts/ci/check-openapi.sh` | `OK: openapi validate + breaking`（c 4.2.0 → 4.3.0，无 breaking） |
| 迁移 | `bash scripts/ci/check-migrations.sh` | `OK: migrations` |
| 审计包 | `go test ./internal/shared/audit/` | 通过 |
| 身份集成 | `go test ./tests/integration/identity/` | 通过（无 DSN 用例跳过） |

覆盖率快照：`internal/identity/app` 75.3%；`transport/admin` 90.6%；`transport/biz` 76.9%；
`transport/rest` 87.9%；`transport/c` 92.8%。

## 4. 验收标准核对

| ID | 验收项 | 结果 |
|---|---|---|
| R-8.4-01 | 故障 + 宽限内不再全量 503 | 通过（用例覆盖放行与超窗失败） |
| R-8.4-01 | 超宽限 / 未验证 / 令牌过期仍 503 | 通过 |
| R-8.4-01 | 关闭宽限时与旧版一致 | 通过 |
| R-8.4-02 | 审计落库 + 幂等 + 可检索 | 通过（代码与集成用例） |
| R-8.4-03 | 库表无明文列 | 通过（000016 `DROP COLUMN` + 集成用例） |
| R-8.4-04 | 超窗 / 无会话 / 家庭缺失 / 已撤销 → 40100 | 通过 |
| R-8.4-04 | 最近登录可注销；短信验证码路径不回归 | 通过 |

## 5. 闭环确认与评审

- 4 / 4 风险闭环；未做风险接受降级，未调整既有 P 级。
- 仅收紧安全与可用性标准：jti 故障期由「全量失败」改为「有界宽限」；注销由「仅 Bearer」改为「要求最近登录」。
- 兼容性：两处新增配置均有默认值，既有部署无需改配置；C 端注销在旧会话上会新增 40100，需前端引导重新登录。
- 回滚：R-8.4-01 设置 `identity.revocation.token_grace=-1s` 即回到严格 fail_closed；
  R-8.4-04 回退代码或把窗口调至 30m 上限；不涉及数据迁移。
- 提交专项评审：评审通过后由运维在 staging 执行一次 Redis 故障注入与 C 端注销回归，结果回填本报告。
