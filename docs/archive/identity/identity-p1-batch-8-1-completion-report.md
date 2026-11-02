# 身份域 8.1 批次 P1 修复完成报告

> 依据：[identity-enterprise-production-gap-review.md](./identity-enterprise-production-gap-review.md) §8.1
>
> 范围：仅处理 8.1 批次指定的 5 项 P1（REL-01、REL-02、OPS-02、PERF-01、PERF-02、PERF-03），未调整优先级，未处理其他 P1/P2。
>
> 基线：修复前全仓 `go build ./...`、`go test ./...` 通过；修复后再次全量通过。

---

## 1. 修复结果总览

| 编号 | 问题 | 状态 | 核心变更 | 验证 |
|---|---|---|---|---|
| REL-01 | Redis 故障时 jti 校验逐请求等待超时 | 已完成 | 熔断冷却期内 jti 快速失败；回源时跳过 Redis 读写 | `TestRevocationCheckerBreakerFailsFastWithoutStoreCalls` |
| REL-02 | 多副本 worker 并行清理 | 已完成 | MySQL `GET_LOCK('identity:cleanup')` 连接级互斥；worker 清理不再被误关闭 | `TestRunCleanupOnceSkipsWhenLockHeld` |
| OPS-02 | 清理任务无指标 | 已完成 | 清理 runs/rows/duration/batch_limit 指标；worker 暴露 `/metrics`；新增告警 | `TestSessionCleanupReportsBatchLimitHit`、`TestCleanupMetricsRecordRun` |
| PERF-01 | Argon2 在事务内且绕过并发槽位 | 已完成 | 改密/重置/强制改密采用事务外 probe + `HashContext` 槽位 | `TestPasswordMutationsRunOutsideTransaction` |
| PERF-02 | 会话快照在事务内写 Redis | 已完成 | 快照随结果返回，事务提交后统一写入 | `TestSessionSnapshotWrittenAfterCommit` |
| PERF-03 | 撤销链路无界加载会话链 | 已完成 | 仅加载 `access_ttl` 内存活会话，SQL/内存实现硬上限 256 | `TestMemorySessionListActiveByFamilyFiltersAndCaps` |

---

## 2. 逐项修复明细

### 2.1 REL-01 jti 熔断快速失败

**根因**

- 熔断仅覆盖账号/会话快照读取；jti 黑名单始终调用 Redis。
- 熔断打开后，回源仍尝试 `SaveAccountState/SaveSessionState`，Redis 故障时每个请求继续等待 3s 写超时。

**变更**

- `internal/identity/app/revocation.go`：
  - `Check` 在 jti 校验前检查 `storeSkipped()`；冷却期内直接按 `failure_mode` 返回（fail_closed → 503），不再触网。
  - `refetchState` / `refetchSessionState` 在熔断期内同时跳过 Redis 读与回填，只回源 DB。
- `docs/runbooks/identity-auth-failure.md`：补充 Redis 必须 HA、熔断行为与止损步骤。

**验证**

- 新增 `TestRevocationCheckerBreakerFailsFastWithoutStoreCalls`：阈值触发后，第 3 次请求的存储调用次数保持不变且返回 50300。
- `TestRevocationChecker` 既有 fail_closed / fail_open / DB 回退用例全部通过。

**残余风险**

- jti 无 DB 事实源，Redis 故障期间已认证请求仍会 fail_closed 503；需以 Redis HA 与 Runbook 止损为前提（风险接受项）。

### 2.2 REL-02 / OPS-02 清理互斥与指标

**根因**

- `StartCleanup` 直接启动 goroutine，无分布式互斥；生产 worker `replicas: 2`。
- worker 的 `identity.enabled=false` 导致 `StartCleanup` 直接返回，生产清理任务实际未运行。
- 清理仅有日志，无指标；worker 无 HTTP 端点，指标无法被抓取。

**变更**

- `cmd/internal/identityruntime/runtime.go`：
  - `StartCleanup` 改为仅以 `deps.Database != nil` 为开关，确保 worker 在生产执行清理。
  - 新增 `runCleanupOnce`：先抢锁，未抢到则本轮跳过并上报 `skipped`。
  - MySQL 下使用 `GET_LOCK('identity:cleanup')`；连接断开/进程退出自动释放。
- `internal/identity/infrastructure/persistence/cleanup_lock.go`：新增 `MySQLCleanupLock`。
- `internal/identity/app/session_cleanup.go`：
  - 新增 `CleanupStats.BatchLimitHit`、`CleanupRunResult`、`CleanupObserver`、`CleanupLock`。
  - 每类清理达到 `MaxBatchesPerRun` 仍未清空时标记积压。
- `internal/identity/observability/metrics.go`：新增 `CleanupMetrics`：
  - `identity_cleanup_runs_total{result}`
  - `identity_cleanup_rows_total{kind}`
  - `identity_cleanup_duration_seconds`
  - `identity_cleanup_batch_limit_hit_total`
- `cmd/worker/main.go`：初始化指标注册表，暴露 `/metrics`、`/healthz`、`/readyz`（readyz 检查 DB）。
- `deploy/helm/admin-frame/values-prod.yaml`：worker 开启探针与 Service，供 ServiceMonitor 抓取。
- `deploy/observability/prometheus/rules/identity.yaml`：新增 `IdentityCleanupFailures`、`IdentityCleanupBacklog`、`IdentityCleanupNotRunning`。
- `docs/runbooks/identity-auth-failure.md`：清理排障改为指标驱动。

**验证**

- `TestRunCleanupOnceSkipsWhenLockHeld`：未抢到锁时清理不执行、结果标记 skipped；抢到锁后正常执行。
- `TestSessionCleanupReportsBatchLimitHit`：满批且达到上限时 `BatchLimitHit=true`。
- `TestCleanupMetricsRecordRun`：成功/跳过/失败与积压计数正确。
- 既有清理循环/归档/过期用例全部通过。

**部署注意**

- 无需新迁移；需确认 worker 的 Service/ServiceMonitor 与探针随 overlay 生效。
- Redis 高可用与 worker 指标抓取属于上线前置条件。

### 2.3 PERF-01 Argon2 移出事务并接入并发槽位

**根因**

- 改密、重置密码、强制改密的 `Verify/Hash` 在数据库事务内执行。
- 请求路径直接调用 `PasswordService.Verify/Hash`，绕过 `WithConcurrencyLimit` 槽位；仅登录的 `VerifyContext` 受约束。

**变更**

- `internal/identity/app/password.go`：新增 `HashContext`，所有请求路径的哈希必须在槽位内执行。
- `internal/identity/app/account_service.go`：
  - `ChangePassword` 拆为事务外 probe（校验旧密码 + 计算新哈希）与事务内复核/写入；并发改密返回 40900。
  - `ResetPassword` 目标预检、临时口令生成与 `HashContext` 移到事务外；事务内保留 step-up、写入、撤销与审计。
- `internal/identity/app/password_change.go`：
  - 新增 `probePasswordChangeCompletion`：事务外解析票据、账号校验、限流、新密码比对与哈希。
  - 事务内只做票据一次性消费、哈希一致性复核、写入、撤销与审计。
- `internal/identity/app/auth_service.go`：登录透明重哈希改用 `HashContext`。

**验证**

- 新增 `TestPasswordMutationsRunOutsideTransaction`：用事务感知 hasher 断言改密与重置密码的 `Hash/Verify` 均未在事务内执行。
- 既有改密、强制改密、step-up、密码策略用例全部通过。

**行为变化与残余风险**

- 改密/强制改密在 probe 与事务之间若凭据被并发修改，返回 40900 而不是覆盖新凭据。
- 强制改密对新密码哈希损坏由“继续执行”改为“失败关闭”（更严格）。
- `ResetPassword` 在事务外先生成并哈希临时口令，再进入事务执行 step-up；高权限账号的异常调用仍受 Argon2 并发槽位与限流约束。

### 2.4 PERF-02 会话快照提交后写入

**根因**

- `createSession` / `issueNewSession` 在事务内调用 `markSessionActive` 写 Redis。
- Redis 3s 超时会延长数据库事务；回滚后可能残留 active 快照。

**变更**

- `internal/identity/app/auth_service.go`：
  - `LoginResult`、`MFAVerifyResult`、`refreshOutcome` 携带未导出的 `activatedSession` 快照。
  - `createSession` / `issueNewSession` 不再写 Redis，只返回快照。
  - `Login`、`Refresh`、`CompleteVerifiedLogin`、`RegisterConsumerByPhone`、`VerifyMFA`、`CompletePasswordChange` 在事务成功后统一调用 `markActivatedSession`。
- `internal/identity/app/mfa.go`、`sms_login.go`：补齐 MFA 登录/短信开户的事务后写入。

**验证**

- 新增 `TestSessionSnapshotWrittenAfterCommit`：登录与刷新过程中 `SaveSessionState` 均未在事务内发生，且提交后快照存在。
- 既有登录、刷新、MFA、登出、撤销用例全部通过。

### 2.5 PERF-03 撤销链路查询有界

**根因**

- 每次刷新新增一行 `identity_session`；撤销时 `ListByFamily` 加载整条链全部历史。
- 在线保留 30 天时，单设备可积累数千行，撤销时延随链长增长。

**变更**

- `internal/identity/domain/repository.go`：`ListByFamily` 替换为 `ListActiveByFamily(ctx, familyID, activeSince)`。
- SQL/内存实现：
  - 仅返回 `active/rotated` 且 `last_used_at >= activeSince` 的会话。
  - 硬上限 256 条，防御异常高频刷新。
- `internal/identity/app/auth_service.go`：
  - `revokeFamilyChain` 按 `access_ttl + 1m` 计算 `activeSince`，只构造仍可能存活 Access Token 的快照。
  - 撤销本身仍覆盖整链；列表失败不阻断已完成的安全撤销，仅记录日志（快照缺失最长影响 `state_ttl` ≤60s）。

**验证**

- 新增 `TestMemorySessionListActiveByFamilyFiltersAndCaps`：旧 rotated/revoked 会话被过滤；超过上限时返回 256 条。
- 既有重放、登出、单会话撤销、管理员撤销用例全部通过。

---

## 3. 全量验证

```text
go build ./...        PASS
go test ./...         PASS
go vet ./internal/identity/... ./cmd/internal/identityruntime/... ./cmd/worker/...   PASS
```

说明：

- 本机为 Windows 且未启用 CGO，`go test -race` 无法在本地执行；CI 的 `scripts/ci/run-tests.sh` 会在 Linux 上执行 `-race`。
- 未执行 staging 压测与 Redis 故障注入；相关结论仍需在 staging 按 `docs/identity-slo.md` 验收。

---

## 4. 变更文件索引

### 代码

- `internal/identity/app/revocation.go`
- `internal/identity/app/password.go`
- `internal/identity/app/account_service.go`
- `internal/identity/app/password_change.go`
- `internal/identity/app/auth_service.go`
- `internal/identity/app/mfa.go`
- `internal/identity/app/sms_login.go`
- `internal/identity/app/session_cleanup.go`
- `internal/identity/domain/repository.go`
- `internal/identity/infrastructure/persistence/memory_session.go`
- `internal/identity/infrastructure/persistence/mysql_session.go`
- `internal/identity/infrastructure/persistence/cleanup_lock.go`
- `internal/identity/observability/metrics.go`
- `cmd/internal/identityruntime/runtime.go`
- `cmd/worker/main.go`

### 测试

- `internal/identity/app/rel01_test.go`
- `internal/identity/app/perf01_test.go`
- `internal/identity/app/perf02_test.go`
- `internal/identity/app/session_cleanup_test.go`
- `internal/identity/infrastructure/persistence/session_active_list_test.go`
- `internal/identity/observability/metrics_test.go`
- `cmd/internal/identityruntime/runtime_test.go`

### 交付与文档

- `deploy/helm/admin-frame/values-prod.yaml`
- `deploy/helm/admin-frame/values.yaml`
- `configs/worker.yaml.example`
- `deploy/observability/prometheus/rules/identity.yaml`
- `docs/runbooks/identity-auth-failure.md`

---

## 5. 回滚方式

| 变更 | 回滚 |
|---|---|
| jti 快速失败 | 移除 `Check` 中的 `storeSkipped()` 分支与回填跳过逻辑；恢复逐请求 Redis 探测 |
| 清理互斥/指标 | 删除 `runCleanupOnce` 的锁分支并回到直接 `cleanup.Run`；worker HTTP 端点可保留（无副作用） |
| Argon2 移出事务 | 恢复原事务内校验/哈希路径；注意保留 `HashContext` 不影响旧路径 |
| 会话快照提交后写 | 恢复 `createSession` / `issueNewSession` 内 `markSessionActive` 调用 |
| 撤销查询有界 | 恢复 `ListByFamily` 全量查询（不建议，属性能回滚） |

无数据库迁移，回滚仅需回退应用镜像；worker Service/探针可按 Helm overlay 单独关闭。
