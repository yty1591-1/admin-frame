# identity P1 修复完成报告（第 8.2 批次）

> 范围：[identity-enterprise-production-gap-review.md](./identity-enterprise-production-gap-review.md) §8.2 四项 P1。
> 登记册：[identity-p1-batch-8-2-register.md](./identity-p1-batch-8-2-register.md)。
> 状态：代码、单测与集成测试（M1/M2）完成；测试团队回归（M3）待执行。

## 1. 结果概览

| ID | 问题 | 修复状态 | 关键验证 |
|---|---|---|---|
| SEC-02 | 特权操作 step-up 覆盖不一致 | 已修复 | 新增用例覆盖 5 个端点；缺码返回 40000 |
| SEC-05 / MNT-01 | 契约与实现漂移 | 已修复 | OpenAPI validate 通过；10 条处置 path 声明幂等头 |
| SEC-01 | 登录主体明文落库 | 已修复（S5） | 迁移删除明文列；仓储只写索引/密文；集成测试断言列不存在 |
| SEC-07 | 审计无独立存储 | 已修复 | 审计表 + SQL 出口 + Outbox 幂等落库；按 actor/target/trace 检索 |

## 2. 逐项修复与验证

### 2.1 SEC-02 特权操作二次认证

变更：
- `account_service.go` 的 `Enable` / `Unlock`（经 `changeStatus`）、`Lock`、`Restore`、
  `RevokeTargetSession` 在事务内、执行特权动作前调用 `requireAdminStepUp`。
- `LockAccount` 请求透传 `mfa_code`；`RevokeAccountSession` 支持可选 JSON body 携带
  `mfa_code` / `reason`。
- OpenAPI 同步更新 `AccountActionRequest` / `AccountLockRequest` 的 `mfa_code` 说明。

验证：
- `internal/identity/app/account_service_test.go` 新增
  `TestAccountServiceStepUpCoversLifecycleAndSessions`：
  `mfa.admin_required=true` 时 5 个端点缺少 `mfa_code` 均返回 40000，提供有效动态码后成功。
- 既有 `TestAccountServicePrivilegedStepUpMFA` 仍通过。
- 结果：`go test ./internal/identity/app/` 通过。

### 2.2 SEC-05 / MNT-01 契约对齐

变更：
- `api/openapi/paths/admin/auth.yaml`：disable / enable / lock / unlock / restore /
  mfa-reset / password-reset / delete / 单会话撤销 / 全量撤销共 10 条 path 引用
  `IdempotencyKey` 且 `required: true`。
- `api/openapi/components.yaml`：修正幂等头描述；新增 `BizLoginRequest`
  （`merchant_id` 必填）；`AccountLockRequest` 补充 `mfa_code`。
- `api/openapi/paths/biz/auth.yaml` 改用 `BizLoginRequest`，修正“merchant_id 由账号推导”描述。
- `transport/c/handler.go`：令牌响应补齐 `absolute_expires_at`，与 A/B 端一致。
- `scripts/ci/check-authn.sh`：新增“10 条处置 path 幂等头引用”和“B 端必填契约”门禁。

验证：
- `bash scripts/ci/check-openapi.sh`：components / admin / biz / c 全部 `No findings detected`。
- `bash scripts/ci/check-authn.sh`：幂等头 10/10、B 端契约检查通过。
- `TestTokenResponseFromIncludesAbsoluteExpiresAt` 通过。
- `TestAccountMutationsRequireIdempotencyKey` 扩展到 10 条 path，通过。

### 2.3 SEC-01 登录主体明文清除（S5）

变更：
- `migrations/000015_identity_subject_s4`：账号唯一键切到
  `(channel, merchant_id, subject_index)`；外部身份新增
  `provider_subject_index` / `provider_subject_ciphertext`，唯一键切到
  `(provider, provider_subject_index)`。
- `migrations/000016_identity_subject_s5`：以 `MODIFY ... NOT NULL` 作为回填完整性门禁，
  删除 `identity_account.subject` 与 `identity_oauth_identity.provider_subject`。
- `mysql_account.go`：读写只使用 `subject_index` / `subject_ciphertext`；删除明文回退分支；
  未注入保护器时失败关闭；列表关键词改为解密后内存过滤（上限 10000 行）。
- `mysql_oauth_identity.go`：外部身份同样只写索引/密文，并新增回填方法。
- `subject_protector.go`：新增 `ProviderIndex` / `EncryptProviderSubject` /
  `DecryptProviderSubject`（AAD 绑定映射主键）。
- `cmd/identity-maintenance`：回填同时覆盖账号与外部身份。
- `cmd/internal/identityruntime`：SQL 外部身份仓储注入主体保护器。

验证：
- `TestSQLAccountSubjectProtection`（集成）：断言 `identity_account.subject` 列不存在、
  密文不含明文、索引查找/换绑/关键词列表正常、无保护器仓储失败关闭。
- `TestSQLOAuthSubjectProtection`（集成）：断言 `provider_subject` 列不存在、
  索引查找与解密回填正常。
- `TestProviderSubjectProtection`（单测）：索引确定性、跨 provider 隔离、AAD 绑定。
- 本地未配置 `TEST_MYSQL_DSN`，集成用例按约定跳过；迁移需在 staging 演练库执行。

### 2.4 SEC-07 独立审计存储

变更：
- `migrations/000017_identity_audit_event`：新增 `identity_audit_event` 追加写表，
  `event_id` 主键，索引覆盖 actor / target / trace / occurred_at；表注释声明
  ≥180 天保留期（AUD-07）。
- `internal/shared/audit/sqlstore.go`：新增 `SQLEmitter`（`ON DUPLICATE KEY UPDATE`
  保证重试幂等）与 `MultiEmitter`。
- `cmd/internal/identityruntime/runtime.go`：Outbox 消费出口改为
  `SQL 审计表 + 结构化日志`；审计写失败由 Relay 重试。
- 不改动 IAM 审计出口与 `iam.audit` 边界。

验证：
- `TestSQLEmitterRequiresDatabase` / `TestSQLEmitterRejectsInvalidEvent` /
  `TestMultiEmitterStopsOnFailure` 通过。
- `TestIdentityAuditSQLEmitter`（集成）：重复投递仍 1 行；按 actor / resource / trace
  检索均为 1 行；outcome/action/attrs 正确落库。
- 本地无 MySQL，集成用例按约定跳过。

## 3. 验证记录

| 检查 | 命令 | 结果 |
|---|---|---|
| 全量编译 | `go build ./...` | 通过 |
| 目标包测试 | `go test ./internal/identity/... ./cmd/...` | 通过 |
| 审计包测试 | `go test ./internal/shared/audit/` | 通过 |
| 集成测试编译/运行 | `go test ./tests/integration/...` | 通过（无 DSN 用例跳过） |
| 静态检查 | `go vet ./internal/identity/... ./cmd/identity-maintenance/` | 通过 |
| 契约门禁 | `bash scripts/ci/check-authn.sh` | 通过 |
| OpenAPI 校验 | `bash scripts/ci/check-openapi.sh` | 通过 |
| 全量测试 | `go test ./...` | 除 `pkg/config` watcher 超时外全部通过；单包重跑通过（与本批次无关） |

说明：
- 本机 Windows 无 CGO，`-race` 由 CI 执行。
- SEC-01 迁移未在本机 MySQL 执行；须在 staging 演练库按“回填 → 000015 → 000016”
  顺序验证，并完成回滚演练。

## 4. 提交清单

| 提交 | 说明 |
|---|---|
| `4a40462` | `docs(identity): 登记 8.2 批次 P1 问题与责任里程碑` |
| `1ad5cb2` | `fix(identity): 补齐账号处置操作的二次认证覆盖` |
| `a5f4159` | `fix(api): 对齐身份契约与 C 端令牌到期字段` |
| `1186cb6` | `fix(identity): 删除登录主体明文列并切换索引唯一键` |
| `f581fb9` | `fix(identity): 新增独立审计表与落库出口` |

## 5. 风险、兼容与回滚

| 项 | 说明 |
|---|---|
| 升级顺序 | 先发布双写版本 → 执行 `identity-maintenance backfill-subject` → 执行 000015 → 执行 000016 → 发布只读密文版本。顺序颠倒会导致登录主体无法解密。 |
| 回填门禁 | 000016 的 `MODIFY NOT NULL` 在存在未回填行时直接失败，禁止带数据删除明文。 |
| 数据回滚 | 明文删除不可逆；完整回滚需迁移前数据库快照。结构回滚按 000016 → 000015 逆序执行。 |
| 关键词搜索 | 无明文列后改为内存解密过滤（上限 10000 行）；超大租户需评估列表时延。 |
| 审计保留 | 表为追加写，应用不提供 UPDATE/DELETE；≥180 天保留由运维分区/归档执行。 |
| 未覆盖 | 本批次不含 SEC-03/04/06/08/09/10 与其余 P2；不调整优先级。 |

## 6. 测试团队回归入口（M3）

按登记册第 3 节逐项验证，重点补充：

1. `identity.mfa.admin_required=true` 下 10 个账号处置端点的缺码/有效码矩阵。
2. 用 OpenAPI 生成客户端直连账号处置接口，验证幂等头可用。
3. staging 迁移演练：回填覆盖率、唯一键切换、删列、回滚到快照。
4. 审计表写入后按 actor / target / trace 查询，并验证保留期策略配置。
5. 回归登录、刷新、撤销、账号列表关键词搜索、OAuth 绑定全链路。
