# 身份域 P0/P1 修复日志

> 基线文档：[identity-module-analysis-and-production-gap.md](./identity-module-analysis-and-production-gap.md)
>
> 修复原则：先 P0 后 P1；每项先定位根因，再做最小闭环改动，最后用单元/集成/构建验证。
>
> 结论：**P0 全部完成；P1 中 P1-1/2/3/4/5/8/10/14 已完整完成，P1-6 完成 S1–S3，P1-7/9/11 已补齐主要缺口，P1-13 保留 staging 实测**。全仓 `go build ./...`、`go test ./...`、`go vet` 均通过。

---

## 1. 状态总览

| 编号 | 问题 | 优先级 | 状态 | 验证 |
|---|---|---|---|---|
| P0-1 | prod 未启用身份域、缺 hash key Secret | P0 | 已完成（按“启用模板”方案） | Helm values 审查、配置校验路径核对 |
| P0-2 | 管理员特权操作无二次认证 | P0 | 已完成 | `TestAccountServicePrivilegedStepUpMFA` |
| P0-3 | Redis 故障导致全站 503 / readiness 摘流 | P0 | 已完成 | `TestRevocationChecker`（DB 回退、jti fail_closed） |
| P0-4 | 清理吞吐不足、删除缺索引 | P0 | 已完成 | `TestSessionCleanupLoopsUntilDrained` + 迁移 000008 |
| P0-5 | NetworkPolicy 未放行 OAuth/SMS 443 | P0 | 已完成 | values 模板审查 |
| P0-6 | 无身份域告警/仪表盘 | P0 | 已完成 | 新增 `identity.yaml` 规则与 `identity.json` |
| P1-1 | Argon2 在 DB 事务内 | P1 | 已完成 | `TestLoginPasswordVerifyOutsideTransaction` + 全量回归 |
| P1-2 | 每请求 2–3 次 Redis 往返 | P1 | 已完成（L1 + 指标） | `TestCachedRevocationAccountHitAvoidsInner` 等 |
| P1-3 | 列表 `LIKE %kw%` + 全表 COUNT | P1 | 已完成（转义 + 计数上限） | `TestEscapeLikePattern` |
| P1-4 | 会话每次刷新新增行、无归档/分区 | P1 | 已完成（归档表） | `TestSessionCleanupArchivesAndPrunesArchive` + 迁移 000010 |
| P1-5 | C 端换绑/注销无再认证 | P1 | 已完成 | `TestSMSRebindPhone`、`TestDeleteConsumerAccount` |
| P1-6 | PII 明文 subject / OAuth subject | P1 | 部分完成（S1/S2/S3） | `TestSubjectEncryptRoundTripAndAAD` + 迁移 000011；S4/S5 未上线 |
| P1-7 | 审计覆盖缺口 | P1 | 已完成（读审计 + MFA setup + 短信 + OAuth + 限流） | `auth.account.read` / `auth.session.read` / `auth.mfa.setup` / `auth.sms.send` / `auth.oauth.*` / `auth.rate_limited` |
| P1-8 | 5xx 根因未记录 | P1 | 已完成（5xx 统一错误日志） | `go test ./internal/shared/httpx/...` |
| P1-9 | SQL/C 端测试覆盖低 | P1 | 部分完成（新增 SQL 集成用例，依赖 MySQL DSN） | `tests/integration/identity/sql_repository_test.go`；无 DSN 时显式跳过 |
| P1-10 | 多租户唯一键缺失 | P1 | 已完成 | 迁移 000009 + `TestMemoryAccountRepositoryTenantIsolation` |
| P1-11 | 管理端无限流/幂等 | P1 | 已完成（处置路由强制幂等键） | `TestAccountMutationsRequireIdempotencyKey` + Helm overlay |
| P1-12 | 运行手册缺失 | P1 | 已完成 | 新增 `docs/runbooks/identity-auth-failure.md` |
| P1-13 | 无 SLO/压测基线 | P1 | 部分完成（SLO 文档） | 新增 `docs/identity-slo.md` |
| P1-14 | 权限粒度仅 read/write | P1 | 已完成 | `TestAccountRoutesRequireSplitPermissions` + 迁移 000012 |

---

## 2. P0 修复明细

### 2.1 P0-1 生产启用模板与 Secret 注入

**根因**

- `values-prod.yaml` 与 prod 配置模板均保持 `identity.enabled=false`。
- Helm 只注入了 MFA secret（可选），缺少 `ADMIN_FRAME_IDENTITY_HASH_KEY`；启用后配置校验会直接失败。

**修复**

- 新增 `deploy/helm/admin-frame/values-identity-enabled.yaml`：
  - 全局保持关闭，仅对 admin/biz 的 `configOverrides.identity.enabled=true`；
  - worker/c 保持关闭，避免 worker 被“identity 仅支持 admin/biz/c”校验拒绝；
  - admin 强制 `mfa.admin_required=true`；
  - 管理端开启幂等与按用户限流。
- `values.yaml` 新增 `identitySecrets`：
  - `hashKeyKey`、`mfaSecretKey`、`secretName`。
- `_workload.tpl` 在 `identitySecrets.enabled=true` 时注入：
  - `ADMIN_FRAME_IDENTITY_HASH_KEY`
  - `ADMIN_FRAME_IDENTITY_MFA_SECRET_KEY`

**验证**

- 配置校验仍要求 prod/staging 的 `hash_key` 非空；Secret 缺失会在启动期失败关闭。
- 本机未安装 helm，未执行渲染；模板结构经人工核对（env 列表合并且不重复 `env:` 键）。

**回滚**

- 不应用 overlay 即保持 prod 关闭；删除 overlay 不影响现有部署。

---

### 2.2 P0-2 管理员特权操作 step-up MFA

**根因**

- 账号管理路由只校验 `identity:account:write`，任一持有该权限的管理员可重置他人密码 / MFA，形成高权限账号接管链。

**修复**

- `AuthService.requireAdminStepUp`：
  - 仅 A 端且 `mfa.admin_required=true` 时强制；
  - 校验操作者账号状态、账号级限流、已确认 MFA 绑定；
  - 支持 TOTP 与一次性恢复码；
  - 失败走 `mfaFailure`（计入登录失败与锁定）。
- 以下操作在事务内、执行前调用 step-up：
  - `ResetPassword`
  - `ResetMFA`
  - `Delete`
  - `RevokeTargetSessions`
- `AccountOperationInput` 新增 `MFACode`；REST 请求新增 `mfa_code`。

**验证**

- `TestAccountServicePrivilegedStepUpMFA`：无 code 返回 40000；正确 TOTP 成功。
- 既有账号服务测试在 `mfa.admin_required=false` 下保持原行为。

**边界**

- TOTP 时间步会被登录消费；step-up 若与登录同一 30s 窗口会判为重放，需要等待下一个时间步或使用恢复码。

---

### 2.3 P0-3 Redis 故障降级与 readiness 解耦

**根因**

- `RevocationChecker` 读 Redis 失败直接走 `failure_mode=fail_closed` → 503；
- `/readyz` 同时绑定 Redis，Redis 抖动会让全部副本同时 NotReady，放大为全站不可用。

**修复**

- 账号 / 会话快照读取失败时**回源数据库**（DB 是事实源）；
- 增加 3 次连续失败 / 10s 冷却的撤销存储熔断：
  - 熔断期内跳过 Redis，直接 DB 回源；
  - 成功读取重置计数。
- jti 黑名单无 DB 事实源，仍按 fail_closed 拒绝（安全优先）。
- `registerHealth` 只注册 `identity_db`；Redis 故障通过指标/告警与请求级 503 暴露，不再批量摘流。

**验证**

- `TestRevocationChecker`：
  - Redis 故障 + 账号存在 → DB 回退放行；
  - jti 不可用 → fail_closed 503；
  - fail_open 仍不允许账号不存在。
- `cmd/internal/identityruntime` 测试通过。

**回滚**

- 恢复原 readiness 只需重新注册 `identity_redis`；熔断逻辑可通过常量回退。

---

### 2.4 P0-4 清理吞吐与索引

**根因**

- `SessionCleanup.Run` 每类只执行一个 500 行批次；
- 会话/设备删除按 `status + updated_at` 排序，但表无 `updated_at` 索引 → 全表扫描。

**修复**

- `RetentionPolicy` 新增 `MaxBatchesPerRun`；
- `Run` 对过期、会话删除、登录尝试删除分别循环到“本批未满”或达到上限；
- 默认批次提升到 1000，上限 50 批/轮；
- 新增迁移 `000008_add_identity_cleanup_indexes`：
  - `identity_session(updated_at)`、`(status, updated_at)`
  - `identity_session_family(updated_at)`、`(status, updated_at)`

**验证**

- `TestSessionCleanupLoopsUntilDrained`：3 轮循环、统计累加正确。
- 迁移 SQL 与查询条件匹配；回滚脚本删除索引。

---

### 2.5 P0-5 NetworkPolicy 放行 443

**根因**

- 生产 NetworkPolicy 只放行 3306/5432/6379/4317/4318；C 端 OAuth IdP 与云短信 HTTPS 被阻断。

**修复**

- `values.yaml` 的 `networkPolicy.egressPorts` 增加 443/80；
- 注释说明如需最小权限可改为域名/网段级策略。

**验证**

- prod `networkPolicy.enabled=true` 会继承该端口列表；需在集群做联通性测试。

---

### 2.6 P0-6 身份域告警与 Dashboard

**根因**

- 有 `identity_*` 指标，但 Prometheus 规则只有短信余额；无 dashboard。

**修复**

- 新增 `deploy/observability/prometheus/rules/identity.yaml`：
  - token reuse、lockout spike、revocation store errors、cache write errors、refresh/login failure rate、MFA recovery、session revoke failure。
- 新增 `deploy/observability/grafana/dashboards/identity.json`：
  - 登录结果、P95/P99、锁定/刷新/重放、MFA、撤销失败、会话撤销、短信/OAuth、供应商告警。

**验证**

- 规则引用指标名与代码注册一致；runbook 路径指向新增手册。

---

## 3. P1 完成项明细

### 3.1 P1-3 列表查询转义与计数上限

**根因**

- `keyword` 未转义 `%`/`_`，且每次 `COUNT(*)` 可能全表扫描。

**修复**

- `escapeLikePattern` 转义 `\`、`%`、`_`；
- 总数统计改为 `SELECT COUNT(*) FROM (SELECT 1 ... LIMIT 10000)`，限制扫描上限；
- 返回 total 在超限时为 10000，前端应展示“约 N 条”。

**验证**

- `TestEscapeLikePattern`。

**遗留**

- 前置通配符仍无法命中索引；大规模账号应改为前缀搜索或独立搜索服务。

---

### 3.2 P1-5 C 端再认证

**根因**

- 换绑/注销仅凭 Bearer，令牌泄露即可账号接管。

**修复**

- `SMSRebindInput` 新增 `OldCode`：
  - 新号验证码 + 旧号验证码双重校验；
  - 旧号摘要从账号 subject（`phone:<hmac>`）推导，不要求客户端传旧号明文。
- 新增 `SMSLoginService.DeleteAccount`：短信启用时校验当前手机号验证码后注销。
- C 端路由：
  - `phone/rebind` 接受 `old_code`；
  - `DELETE /account` 接受可选 `{code}`。

**验证**

- `TestSMSRebindPhone`：缺失旧码拒绝；旧码+新码成功；旧号失效。
- `TestDeleteConsumerAccount`：验证码通过后软删。

**遗留**

- OAuth-only 账号无手机号时仍只能凭 Bearer 注销；后续可接 OAuth 再认证。

---

### 3.3 P1-7 审计覆盖

**根因**

- 账号/会话读取属敏感 PII 访问，无审计。
- MFA 绑定发起、短信发送、OAuth 授权/回调与限流拒绝均未落审计。

**修复**

- 新增 `auth.account.read`、`auth.session.read`；
- `AccountService.Get` 与 `ListTargetSessions` 成功后写审计；
- Handler 注入 actor。
- 新增并接线：
  - `auth.mfa.setup`：MFA 绑定发起（`SetupMFA` 成功后写入）；
  - `auth.sms.send`：短信发送成功/失败（`resource_id` 为手机号摘要）；
  - `auth.oauth.authorize` / `auth.oauth.callback`：OAuth 发起与回调结果（只记 provider 与粗粒度原因）；
  - `auth.rate_limited`：登录 / 短信限流拒绝。
- 审计事件目录 `docs/audit/EVENTS.md` 与 `internal/shared/audit/actions.go` 同步。

**验证**

- `go test ./internal/identity/...`、`./internal/shared/audit/...` 通过。
- `TestAuditMFAEvents` 增加 `auth.mfa.setup` 断言。

**遗留**

- 限流审计为按事件写入，攻击流量下需通过采样或日志限速控制成本；
- 配置变更、数据导出类审计仍由平台侧路径负责。

---

### 3.4 P1-8 5xx 根因日志

**修复**

- `internal/shared/httpx.FromError` 对 `code >= 50000` 使用 `slog.Default().Error` 记录完整错误链与 trace_id；
- 客户端响应仍使用笼统文案，不泄露内部细节。

**验证**

- `go test ./internal/shared/httpx/...`。

---

### 3.5 P1-9 测试补齐

新增：

- `TestSessionCleanupLoopsUntilDrained`
- `TestEscapeLikePattern`
- `TestAccountServicePrivilegedStepUpMFA`
- `TestMemoryAccountRepositoryTenantIsolation`
- 更新 `TestSMSRebindPhone` / `TestDeleteConsumerAccount`
- `tests/integration/identity/sql_repository_test.go`：
  - `TestSQLAccountSubjectProtection`（双写双读 + 回填）；
  - `TestSQLSessionArchiveSameTransaction`（归档与删除同事务）。
- `TestAccountMutationsRequireIdempotencyKey`（处置路由强制幂等键）；
- `TestCachedRevocation*`、`TestSubject*`、`TestAccountRoutesRequireSplitPermissions`。

**说明**

- SQL 集成测试依赖 `TEST_MYSQL_DSN`（CI 提供 mysql:8 service）；未设置时显式跳过，不伪造通过。
- C 端 transport 已有 handler 测试，本次新增短信/OAuth 审计路径覆盖。

---

### 3.6 P1-10 多租户唯一键

**根因**

- `(channel, subject)` 全局唯一，B 端不同商户无法复用同名主体。

**修复**

- `AccountRepository.FindBySubject` 增加 `merchantID`；
- SQL / 内存仓储按 `(channel, merchant_id, subject)` 查找与唯一；
- 迁移 `000009_identity_account_tenant_unique`；
- `LoginInput` / `loginRequest` 增加 `merchant_id`：
  - B 端必填；
  - A/C 端传入返回 40000。

**验证**

- `TestMemoryAccountRepositoryTenantIsolation`；
- 既有 A/B 登录测试通过（fixture 为 B 端自动带 `m-1`）。

**兼容**

- 旧数据满足新唯一键（旧约束更严格），无需回填。
- 回滚需先处理跨商户同名主体。

---

### 3.7 P1-11 管理端限流与幂等

**修复**

- `values-identity-enabled.yaml` 的 admin `configOverrides`：
  - `idempotency.enabled=true`，`driver=redis`，`required=false`；
  - `rate_limit.enabled=true`，`key=channel_user`，20 rps / 40 burst，fail_closed。
- 账号处置路由（disable / enable / lock / unlock / restore / password reset / MFA reset / delete / 会话撤销）
  统一增加 `Idempotency-Key` 强制校验：缺少时返回 40000，避免重试造成重复操作。
- 真正的去重与响应回放仍由全局 pipeline 在 `idempotency.enabled=true` 时完成。

**说明**

- 未把全局 `required=true`：登录、刷新等无幂等键请求会被误拒；仅对高风险处置路由强制。
- 客户端需为上述路由生成稳定的幂等键（建议按「操作者 + 目标 + 动作 + 时间窗」派生）。

---

### 3.8 P1-12 运行手册

新增 `docs/runbooks/identity-auth-failure.md`，覆盖：

- 快速分诊
- Redis 故障
- 令牌重放
- 管理员接管
- 清理积压

---

### 3.9 P1-13 SLO / 容量基线

新增 `docs/identity-slo.md`：

- 登录/刷新/MFA 的成功率与 P95/P99 目标；
- Argon2 内存、DB 连接、Redis 回源、清理吞吐公式；
- 压测场景与故障注入清单。

---

## 4. 整体回归结果

执行命令与结果：

```text
go build ./...            # PASS
go test ./...             # PASS（全部包）
go vet ./internal/identity/... ./internal/shared/httpx/... ./cmd/internal/identityruntime/...   # PASS
```

重点包：

```text
internal/identity/app                         PASS
internal/identity/domain                      PASS
internal/identity/infrastructure/persistence  PASS
internal/identity/transport/admin             PASS
internal/identity/transport/biz               PASS
internal/identity/transport/c                 PASS
internal/shared/audit                         PASS
internal/shared/httpx                         PASS
cmd/internal/identityruntime                  PASS
tests/integration/identity                    PASS
```

---

## 5. 结构性 P1 完成情况（本阶段）

> P1-1 执行报告见 `identity-p1-1-argon2-execution-report.md`；P1-2 / P1-4 / P1-6（S1–S3）/ P1-14 的完整执行记录见 `identity-p1-remaining-completion-report.md`。

### 5.1 P1-1 Argon2 移出数据库事务（已完成）

- 登录拆为 `probeLogin`（事务外 Argon2）+ `completeProbeInTx`（事务内复核/写入）。
- 失败路径由 `recordLoginFailureInTx` 在事务内写 attempt/audit，不再执行 Argon2。
- 验证：`TestLoginPasswordVerifyOutsideTransaction`；全仓 `go test ./...` 通过。

### 5.2 P1-2 Redis 往返与回源风暴（已完成）

- 新增 `cachedRevocationStore` 装饰器：账号 / 会话快照本地 TTL 1s + LRU，命中后 0 次 Redis；
- 写路径（账号 / 会话快照的 SET / DEL）同步更新或清除 L1，本副本撤销立即生效；
- jti 黑名单显式不缓存「未撤销」结果，保持 fail-closed 语义；
- 新增 `identity_revocation_l1_hit_total` / `identity_revocation_redis_calls_total` / `identity_revocation_db_refetch_total`；
- 配置 `identity.revocation.local_cache_ttl`（0 默认 1s，负数关闭）与 `local_cache_capacity`；
- 回滚：置负数 TTL 即回到逐请求读 Redis 的旧路径。
- 验证：`TestCachedRevocation*` 6 个用例通过；`go test ./...` 全绿。

### 5.3 P1-4 会话归档/分区（已完成，采用归档表）

- 迁移 `000010_identity_session_archive`：新增会话与设备归档表（无外键，含 `archived_at` 索引）；
- 仓储 `ArchiveExpiredBefore` 在同一事务内「选中主键 → INSERT SELECT 归档 → 按主键删除在线」；
- 清理任务默认「归档 + 删除」，并按 `archive_days`（默认 365 天）清理归档表；
- 配置 `identity.retention.archive_days`，负数回滚为「只删不归档」；
- 验证：`TestSessionCleanupArchivesAndPrunesArchive`、`TestSessionCleanupExpiresAndDeletes` 通过。

### 5.4 P1-6 PII 加密 + 索引（S1/S2/S3 已完成；S4/S5 未上线）

- 迁移 `000011`：新增可空 `subject_index` / `subject_ciphertext` 与非唯一索引；
- `SubjectProtector`：index = HMAC(hash_key, `account-subject|channel|merchant|subject`)，密文 = AES-256-GCM，AAD 绑定 `account_id`；
- 仓储双写（insert / rebind）与双读（index 优先、未命中回退明文），读路径由密文回填明文；
- 回填工具 `cmd/identity-maintenance`（S3），幂等、可断点续跑；
- 未执行项：S4 唯一键切换到 `subject_index`、S5 删除明文列；`provider_subject` 加密同属后续阶段。
- 验证：`TestSubjectIndexDeterministicAndTenantIsolated`、`TestSubjectEncryptRoundTripAndAAD`、`TestSubjectDecryptAfterKeyRotation` 通过。

### 5.5 P1-14 权限粒度拆分（已完成）

- 新增 `identity:account:credential` / `identity:account:delete` / `identity:account:session`；
- 迁移 `000012` 先把新权限授予所有已持有 `identity:account:write` 的角色，再切换路由；
- 路由矩阵：生命周期 → write；密码 / MFA 重置 → credential；删除 → delete；会话撤销 → session；
- 无权限中间件时账号管理路由失败关闭（403）；
- 验证：`TestAccountRoutesRequireSplitPermissions`、`TestAccountRoutesFailClosedWithoutPermissionMiddleware`、catalog 一致性测试通过。

---

## 6. 变更文件索引

### 代码

- `internal/identity/app/session_cleanup.go`
- `internal/identity/app/session_cleanup_test.go`
- `internal/identity/app/auth_service.go`
- `internal/identity/app/mfa.go`
- `internal/identity/app/account_service.go`
- `internal/identity/app/account_service_test.go`
- `internal/identity/app/sms_login.go`
- `internal/identity/app/consumer_service.go`
- `internal/identity/app/sms_login_test.go`
- `internal/identity/app/revocation.go`
- `internal/identity/app/auth_service_test.go`
- `internal/identity/domain/repository.go`
- `internal/identity/infrastructure/persistence/mysql_account.go`
- `internal/identity/infrastructure/persistence/mysql_helpers.go`
- `internal/identity/infrastructure/persistence/mysql_helpers_test.go`
- `internal/identity/infrastructure/persistence/memory_account.go`
- `internal/identity/infrastructure/persistence/memory_repository_test.go`
- `internal/identity/transport/rest/handler.go`
- `internal/identity/transport/c/handler.go`
- `cmd/internal/identityruntime/runtime.go`
- `internal/shared/httpx/response.go`
- `internal/shared/audit/actions.go`
- `internal/shared/audit/event_test.go`
- `internal/identity/infrastructure/cache/cached_revocation.go`
- `internal/identity/infrastructure/cache/cached_revocation_test.go`
- `internal/identity/infrastructure/persistence/mysql_session.go`
- `internal/identity/infrastructure/persistence/memory_session.go`
- `internal/identity/infrastructure/persistence/subject_protector.go`
- `internal/identity/infrastructure/persistence/subject_protector_test.go`
- `internal/identity/observability/metrics.go`
- `internal/identity/domain/account.go`
- `internal/identity/transport/admin/router.go`
- `internal/identity/transport/admin/router_test.go`
- `internal/identity/app/sms_login.go`
- `internal/identity/app/oauth.go`
- `internal/identity/app/mfa.go`
- `tests/integration/identity/sql_repository_test.go`
- `internal/iam/perm.go`
- `internal/iam/catalog/catalog.go`
- `cmd/identity-maintenance/main.go`
- `cmd/internal/identityruntime/runtime.go`
- `internal/platform/config/config.go`

### 迁移

- `migrations/000008_add_identity_cleanup_indexes.up.sql`
- `migrations/000008_add_identity_cleanup_indexes.down.sql`
- `migrations/000009_identity_account_tenant_unique.up.sql`
- `migrations/000009_identity_account_tenant_unique.down.sql`
- `migrations/000010_identity_session_archive.up.sql`
- `migrations/000010_identity_session_archive.down.sql`
- `migrations/000011_identity_account_subject_protection.up.sql`
- `migrations/000011_identity_account_subject_protection.down.sql`
- `migrations/000012_seed_identity_account_permissions.up.sql`
- `migrations/000012_seed_identity_account_permissions.down.sql`

### 交付与文档

- `deploy/helm/admin-frame/values.yaml`
- `deploy/helm/admin-frame/values-identity-enabled.yaml`
- `deploy/helm/admin-frame/templates/_workload.tpl`
- `deploy/observability/prometheus/rules/identity.yaml`
- `deploy/observability/grafana/dashboards/identity.json`
- `docs/runbooks/identity-auth-failure.md`
- `docs/identity-slo.md`
- `docs/audit/EVENTS.md`
- `docs/identity-module-analysis-and-production-gap.md`
- `docs/identity-p1-remaining-completion-report.md`
- `api/openapi/components.yaml`
- `api/openapi/paths/c/auth.yaml`

---

## 7. 上线注意

1. 先执行迁移 `000008`、`000009`，再发布启用 step-up 的版本。
2. 应用 `values-identity-enabled.yaml` 前创建 `admin-frame-identity` Secret。
3. 客户端需同步：
   - B 端登录携带 `merchant_id`；
   - 特权操作携带 `mfa_code`；
   - C 端换绑携带 `old_code`，注销携带 `code`。
4. 回滚顺序：先回滚应用，再按需回滚迁移 000009；000008 可保留（仅索引）。
