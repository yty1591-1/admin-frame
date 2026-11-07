# 身份域剩余 P1 修复完成报告

> 依据：[identity-p1-remaining-implementation-plan.md](./identity-p1-remaining-implementation-plan.md)、[identity-p0-p1-fix-log.md](./identity-p0-p1-fix-log.md) §5。
>
> 范围：P1-2（Redis 往返）、P1-4（会话归档）、P1-6（PII 加密 + 索引，S1–S3）、P1-14（权限拆分）。
>
> 性质：已完成代码与单元/集成回归；性能阈值、迁移 up/down 与灰度演练仍需在 staging 执行，未在本环境伪造结果。

---

## 1. 完成状态

| 编号 | 问题 | 状态 | 交付物 | 验证 |
|---|---|---|---|---|
| P1-2 | 每请求 2–3 次 Redis 往返 | 已完成 | L1 装饰器、三项指标、配置项 | 6 个缓存单测 + 全量回归 |
| P1-4 | 会话表增长无归档 | 已完成 | 000010 迁移、归档仓储、清理改造、`archive_days` | 清理单测 + 集成清理用例 |
| P1-6 | PII 明文 subject | S1/S2/S3 已完成；S4/S5 未上线 | 000011 迁移、SubjectProtector、双写双读、回填 CLI | 3 个加密/索引单测 |
| P1-14 | 权限粒度仅 read/write | 已完成 | 3 个新权限点、000012 种子、路由矩阵 | 权限矩阵 + 失败关闭 + catalog 一致性 |

---

## 2. P1-2 降低 Redis 往返与回源风暴

### 2.1 实现

- 新增 [cached_revocation.go](../../../internal/identity/infrastructure/cache/cached_revocation.go)：`cachedRevocationStore` 装饰 `RevocationStore`。
  - 账号 / 会话快照：进程内 L1，TTL 默认 1s，容量默认 4096，LRU 淘汰；
  - 写路径：`SaveAccountState` / `SaveSessionState` 成功后同步写 L1，`Delete*` 先清 L1 再删 Redis；
  - jti：`IsTokenRevoked` / `RevokeToken` 全部透传，**禁止缓存「未撤销」结果**。
- [revocation.go](../../../internal/identity/app/revocation.go)：新增 `RevocationCacheObserver`，在真正回源 DB 时上报 `OnRevocationDBRefetch`；新增 `WithRevocationCacheObserver`。
- [metrics.go](../../../internal/identity/observability/metrics.go)：新增
  - `identity_revocation_l1_hit_total{kind}`；
  - `identity_revocation_redis_calls_total{op}`；
  - `identity_revocation_db_refetch_total{kind}`。
- [config.go](../../../internal/platform/config/config.go)：新增 `identity.revocation.local_cache_ttl`（0→1s，负数关闭）与 `local_cache_capacity`（0→4096，上限 100000），并加入生产校验。
- [runtime.go](../../../cmd/internal/identityruntime/runtime.go)：Redis 存储统一包一层 L1，Checker 与 Invalidator 共享同一实例，保证本副本撤销立即可见。

### 2.2 验证

```text
go test ./internal/identity/infrastructure/cache/...   PASS
  TestCachedRevocationAccountHitAvoidsInner
  TestCachedRevocationExpiresByTTL
  TestCachedRevocationInvalidatedBySave
  TestCachedRevocationDeleteClearsL1
  TestCachedRevocationNeverCachesJTIAbsence
  TestCachedRevocationEvictsLRU
```

### 2.3 回滚

将 `identity.revocation.local_cache_ttl` 置为负数并重启，`NewCachedRevocationStore` 原样返回 Redis 存储，行为回到改造前。

---

## 3. P1-4 会话归档

### 3.1 实现

- `migrations/000010_identity_session_archive.up.sql`（已被后续基线合并删除）：新增
  - `identity_session_archive`、`identity_session_family_archive`；
  - `archived_at` 索引（归档保留期清理）与 `account_id` 索引（历史追溯）；
  - 归档表不建外键，避免账号软删与归档长期保留冲突。
- [repository.go](../../../internal/identity/domain/repository.go)：`SessionRepository` 新增 `ArchiveExpiredBefore`、`DeleteArchivedBefore`。
- [mysql_session.go](../../../internal/identity/infrastructure/persistence/mysql_session.go)：归档在**单事务**内完成：
  1. 按 `updated_at` 选取本批主键；
  2. `INSERT ... SELECT` 写入归档表；
  3. 按主键删除在线行；
  4. 任一步失败整体回滚。
- [memory_session.go](../../../internal/identity/infrastructure/persistence/memory_session.go)：内存实现模拟同一语义，供单测与本地运行。
- [session_cleanup.go](../../../internal/identity/app/session_cleanup.go)：默认「归档 + 删除」，并按 `ArchiveRetention` 清理归档；`ArchiveRetention <= 0` 回退到只删不归档。
- [config.go](../../../internal/platform/config/config.go)：新增 `identity.retention.archive_days`（0→365，负数关闭归档），校验 `archive_days >= session_days`。

### 3.2 验证

```text
go test ./internal/identity/app/...                     PASS
  TestSessionCleanupArchivesAndPrunesArchive
  TestSessionCleanupExpiresAndDeletes（改为断言 Archived*）
go test ./internal/identity/infrastructure/persistence/... PASS
```

### 3.3 回滚

停止归档无需改代码：配置 `archive_days: -1` 后重启 worker，即回到「只删不归档」；归档表可保留，不影响在线查询。

---

## 4. P1-6 PII 加密与索引（S1/S2/S3）

### 4.1 已完成阶段

| 阶段 | 内容 | 状态 |
|---|---|---|
| S1 | 新增 `subject_index`、`subject_ciphertext` 可空列与非唯一索引 | 已完成（000011） |
| S2 | 写路径双写、读路径 index 优先 + 明文回退 | 已完成 |
| S3 | 存量回填工具（幂等、分批、可断点续跑） | 已完成（cmd/identity-maintenance） |
| S4 | 唯一键切换到 `(channel, merchant_id, subject_index)` | **未执行** |
| S5 | 停止写明文并删除 `subject` 列 | **未执行** |

### 4.2 实现

- `migrations/000011_identity_account_subject_protection.up.sql`（已被后续基线合并删除）：仅新增可空列与索引，不触碰既有唯一键与明文，可滚动升级。
- [subject_protector.go](../../../internal/identity/infrastructure/persistence/subject_protector.go)：
  - `Index` = `HMAC-SHA256(identity.hash_key, "account-subject|channel|merchant|normalizedSubject")`；
  - `Encrypt` = AES-256-GCM，AAD = `identity:account:<account_id>`；
  - `Decrypt` 按密文头选择密钥，支持轮换双读。
- [mysql_account.go](../../../internal/identity/infrastructure/persistence/mysql_account.go)：
  - `insert` / `RebindSubject` 双写 index 与 ciphertext；
  - `FindBySubject` 先查 `subject_index`，未命中回退明文；
  - `FindByID` / `List` 读取后用密文回填明文；
  - `BackfillSubjectProtection` 按批回填缺失行，可重复执行。
- [account.go](../../../internal/identity/domain/account.go)：新增 `SubjectIndex` / `SubjectCiphertext` 字段（仅持久化层维护）。
- [config.go](../../../internal/platform/config/config.go)：新增 `identity.subject.keys` / `active_key_id` / `legacy_key`，并校验 active key 存在。
- `cmd/identity-maintenance/main.go`（文件已被后续基线合并删除）：回填命令，复用 `identity.hash_key` 与 `identity.subject.keys`。

### 4.3 验证

```text
go test ./internal/identity/infrastructure/persistence/... PASS
  TestSubjectIndexDeterministicAndTenantIsolated
  TestSubjectEncryptRoundTripAndAAD
  TestSubjectDecryptAfterKeyRotation
```

### 4.4 上线顺序与回滚

1. 发布 000011；
2. 配置 `identity.subject.keys` 与 `active_key_id`，重启应用（开始双写）；
3. 执行 `go run ./cmd/identity-maintenance -config <config> -batch 500`；
4. 校验 `subject_index` / `subject_ciphertext` 非空率 100% 后，再单独立项 S4/S5；
5. 任一步回滚：删除 `identity.subject.*` 配置并重启，读路径回退明文；S1 列可保留或按 000011 down 删除。

> 未执行 S4/S5 是刻意保留：唯一键切换与删列属于破坏性变更，必须在 staging 完成预检、双读灰度与回滚演练后单独发布。

---

## 5. P1-14 权限粒度拆分

### 5.1 实现

- [perm.go](../../../internal/iam/perm.go)：新增
  - `identity:account:credential`；
  - `identity:account:delete`；
  - `identity:account:session`。
- [catalog.go](../../../internal/iam/catalog/catalog.go)：新增 3 条目录条目（Sort 265/270/275），`identity:account:write` 收敛为生命周期动作。
- `migrations/000012_seed_identity_account_permissions.up.sql`（已被后续基线合并删除）：
  - 先插入权限点；
  - 再把新权限授予**所有已持有** `identity:account:write` 的角色，做到「先授权后切路由」。
- [router.go](../../../internal/identity/transport/admin/router.go)：路由矩阵
  - 查询 → `read`；
  - disable / enable / lock / unlock / restore → `write`；
  - password/reset、mfa/reset → `credential`；
  - DELETE account → `delete`；
  - 单会话撤销 / revoke-all → `session`。
- 文档同步：[PERMISSIONS.md](../../../docs/iam/PERMISSIONS.md)、[identity-authn.md](../.././identity-authn.md)、OpenAPI `paths/admin/auth.yaml`。

### 5.2 验证

```text
go test ./internal/identity/transport/admin/... PASS
  TestAccountRoutesRequireSplitPermissions
  TestAccountRoutesFailClosedWithoutPermissionMiddleware
go test ./internal/iam/catalog/...              PASS（目录/常量/种子/文档四方一致）
```

### 5.3 回滚

执行 `000012` 的 down（先删授权再删权限点）；路由已切换的版本需同时回滚应用，或临时把新权限继续授予旧角色。

---

## 6. 全量回归结果

```text
go build ./...     PASS
go vet ./...       PASS
go test ./...      PASS（全部包，含 tests/integration/identity）
```

重点包：

```text
internal/identity/app                          PASS
internal/identity/infrastructure/cache         PASS
internal/identity/infrastructure/persistence  PASS
internal/identity/observability                PASS
internal/identity/transport/admin              PASS
internal/iam/catalog                           PASS
cmd/internal/identityruntime                   PASS
cmd/identity-maintenance                       PASS（编译）
tests/integration/identity                     PASS
```

---

## 7. 未完成项与 staging 验收清单

以下项目**未在本环境执行**，不得视为已验收：

| 项 | 原因 | 需在 staging 执行 |
|---|---|---|
| P1-2 性能 | 无 k6 / 3 副本环境 | Redis 往返 P95 ≤1、L1 命中率 ≥80%、Redis 停机 5 分钟回源 ≤1.5 倍 |
| P1-4 容量与锁 | 无 10M/50M 数据集 | 归档吞吐 ≥1.2 倍日新增、在线 P95 变化 ≤5%、锁等待 ≤100ms、up/down 演练 |
| P1-6 回填与唯一键 | 无生产规模数据与密钥 | 1M/10M 回填演练、S4 预检无重复、S5 前备份与回滚演练 |
| P1-14 灰度双跑 | 无多副本权限比对环境 | 迁移前后行为对比、负向权限矩阵（仅 credential 不能删除/撤销会话） |
| 监控面板 | 指标已埋点 | 在 Grafana 增加 L1 命中率、归档吞吐、回填进度面板 |

---

## 8. 风险与后续动作

| 风险 | 当前控制 | 后续动作 |
|---|---|---|
| L1 陈旧导致撤销延迟 | TTL 1s + 写后失效 + jti 不缓存 | staging 双副本验证 ≤1s 本副本生效 |
| 归档任务锁表 | 批大小可配 + 单事务短批 | 低峰执行并监控锁等待 |
| 回填中断 | 幂等 + 只处理缺失行 | staging 断点续跑演练 |
| S4 唯一键冲突 | 本阶段未切换 | 先跑预检脚本，再单独发布 |
| 权限迁移漏授权 | 000012 先授权后切路由 | staging 用旧角色回归全部操作 |

---

## 9. 残余 P1 补齐（本轮追加）

在完成 4 项结构性改造后，继续补齐了 P1-7 / P1-9 / P1-11 的主要缺口，并修正文档口径。

### 9.1 P1-7 审计覆盖

- 新增审计动作：`auth.mfa.setup`、`auth.sms.send`、`auth.oauth.authorize`、`auth.oauth.callback`、`auth.rate_limited`；
- 接线位置：
  - `SetupMFA` 成功写入 `auth.mfa.setup`；`MFASetupInput` 增加 `TraceID`；
  - 短信发送在发出验证码前写 `auth.sms.send`（审计失败则不发送），发送失败补 failure 事件；限流拒绝写 `auth.rate_limited`；
  - OAuth authorize/callback 记录 provider 与粗粒度原因；回调失败同样落审计；
  - 登录限流拒绝写 `auth.rate_limited`（审计失败不改变 429）。
- 同步更新 `internal/shared/audit/actions.go`、`docs/audit/EVENTS.md` 与 `KnownActions` 测试。

### 9.2 P1-11 处置路由强制幂等

- 账号处置路由（disable / enable / lock / unlock / restore / password reset / MFA reset / delete / 会话撤销）
  增加 `Idempotency-Key` 强制校验，缺失返回 40000；
- 真正的去重与响应回放仍由平台 pipeline 完成，避免把登录等无幂等键请求误拒；
- 新增 `TestAccountMutationsRequireIdempotencyKey`。

### 9.3 P1-9 SQL 集成测试

- 新增 `tests/integration/identity/sql_repository_test.go`：
  - `TestSQLAccountSubjectProtection`：验证双写、index 双读、换绑与回填；
  - `TestSQLSessionArchiveSameTransaction`：验证归档与在线删除同事务。
- 依赖 `TEST_MYSQL_DSN`；未设置时显式跳过，报告中不作为“已通过”。

### 9.4 文档口径修正

- `docs/identity-module-analysis-and-production-gap.md` 原为空文件，已恢复为**基线索引**（问题编号、状态与明细链接），避免引用悬空；
- P1-1 计划中的 `identity.password.verify_outside_tx` 开关未在代码中实现，已统一口径为**无运行时开关、回退应用版本**，并同步修订实施计划。

### 9.5 验证

```text
go build ./...                                   PASS
go test ./internal/shared/audit/...              PASS
go test ./internal/identity/app/...              PASS
go test ./internal/identity/transport/...        PASS
go test ./tests/integration/identity/...         PASS（无 DSN 时 SQL 用例显式跳过）
```

