# ENT-01 阶段 5「收口」实施计划

## 摘要

阶段 4（C 端登录）已合入。本阶段按 `docs/identity-authn.md` §11 / §15 / §16 / §18 与 `.trae/documents/ent-01-identity-authn-plan.md` 完成 T9（审计接入）与 T10 收口，交付四块内容：

1. **事务化审计**：身份域复用 IAM 的 UnitOfWork + `async_outbox` 模式，审计与账号 / 会话写入同事务；审计失败回滚关键写入；
2. **审计目录补齐**：`auth.account.locked`（active）与 `auth.account.disabled`（planned），同步 `actions.go` 与 `docs/audit/EVENTS.md`；
3. **门禁 `check-authn`**：六项检查接入 Makefile `ci` 与 `scripts/ci/gitee-job.sh arch`；
4. **真实依赖集成测试**：`tests/integration/identity/` 下撤销与刷新重放用例（真实 MySQL + Redis），并回写阶段状态文档。

出口标准：`make ci` + `make test-integration` 全绿。

---

## 现状分析（已核实）

| 维度 | 现状 | 证据 |
|---|---|---|
| 审计出口 | `AuthService` 固定 `audit.SlogEmitter`，无事务，失败分支大量 `_ = s.emitAudit(...)` | [runtime.go](file:///d:/MyDocument/admin-frame/cmd/internal/identityruntime/runtime.go#L141-L159)、[auth_service.go](file:///d:/MyDocument/admin-frame/internal/identity/app/auth_service.go#L211-L246) |
| 事务原语 | IAM 已有可复用实现，身份域未接入 | [uow.go](file:///d:/MyDocument/admin-frame/internal/iam/infrastructure/persistence/uow.go)、[uow.go](file:///d:/MyDocument/admin-frame/internal/iam/app/uow.go)、[uow.go](file:///d:/MyDocument/admin-frame/pkg/db/uow.go#L46-L59) |
| Outbox 模式 | IAM `OutboxRecorder` 写 `async_outbox`，`pkg/outbox.MySQLStore.Append` 已支持 ctx 事务 | [outbox.go](file:///d:/MyDocument/admin-frame/internal/iam/app/outbox.go#L27-L74)、[mysql.go](file:///d:/MyDocument/admin-frame/pkg/outbox/mysql.go#L33-L63) |
| 仓储执行器 | 身份域 6 个 SQL 仓储共 27 处直接使用 `r.sql`，事务内不会复用 `*sql.Tx` | `internal/identity/infrastructure/persistence/mysql_*.go` |
| 装配时序 | `platform.Run` 先 `NewSessionValidator`（构造身份运行时）后设置 `deps.Async`，身份域看不到 Async | [bootstrap.go](file:///d:/MyDocument/admin-frame/internal/platform/bootstrap.go#L184-L209) |
| worker 消费 | 仅注册 IAM 两个 topic，未注册身份审计 topic | [main.go](file:///d:/MyDocument/admin-frame/cmd/worker/main.go#L73-L75) |
| 审计目录 | `actions.go` / `EVENTS.md` 缺 `auth.account.locked`、`auth.account.disabled`；C 端已支持 logout / refresh 但端别仍标 A/B | [actions.go](file:///d:/MyDocument/admin-frame/internal/shared/audit/actions.go)、[EVENTS.md](file:///d:/MyDocument/admin-frame/docs/audit/EVENTS.md#L7-L15) |
| 门禁 | 无 `scripts/ci/check-authn.sh`，Makefile `ci` 未包含 | [Makefile](file:///d:/MyDocument/admin-frame/Makefile#L180-L180)、[gitee-job.sh](file:///d:/MyDocument/admin-frame/scripts/ci/gitee-job.sh#L70-L85) |
| 集成测试 | `tests/integration/identity/` 不存在；`testkit.MySQL` / `testkit.Redis` 可直接复用 | [mysql.go](file:///d:/MyDocument/admin-frame/pkg/testkit/mysql.go#L24-L45)、[redis.go](file:///d:/MyDocument/admin-frame/pkg/testkit/redis.go#L14-L37) |
| 文档状态 | 阶段 5 仍为「待做」，AC 未勾选 | [identity-authn.md](file:///d:/MyDocument/admin-frame/docs/identity-authn.md#L760-L767) |

---

## 实施步骤

### p5-1 身份域事务基础设施

**新增 `internal/identity/app/uow.go`**

- `UnitOfWork` 接口：`WithinTx(ctx context.Context, fn func(context.Context) error) error`（与 IAM 同名同义）。
- `NoopUnitOfWork{}`：直接 `return fn(ctx)`，用于内存模式与单测；注释说明「内存仓储无法回滚，允许退化」。

**新增 `internal/identity/infrastructure/persistence/uow.go`**

- `resolveExecutor(ctx, *sql.DB) db.Querier`：`db.TxFromContext` 命中返回 tx，否则返回连接池。
- `SQLUnitOfWork` + `NewSQLUnitOfWork(*sql.DB)`，`WithinTx` 调 `db.WithinUnitOfWork(ctx, u.sql, nil, fn)`。

**改造 6 个 SQL 仓储复用事务**

- `mysql_account.go`(5)、`mysql_credential.go`(6)、`mysql_session.go`(8)、`mysql_login_attempt.go`(3)、`mysql_mfa.go`(3)、`mysql_oauth_identity.go`(2)。
- 每个仓储新增 `func (r *SQLXxxRepository) exec(ctx context.Context) db.Querier { return resolveExecutor(ctx, r.sql) }`，把 `r.sql.XxxContext(...)` 全部替换为 `r.exec(ctx).XxxContext(...)`；`mysql_account.go` 的 `versionFailure` 同样改用 `r.exec(ctx)`。
- 不改构造函数签名、不改领域端口、不改 SQL 语句。

### p5-2 审计 Outbox 出口（topic=`identity.audit`）

**新增 `internal/identity/app/outbox.go`**（对照 IAM `OutboxRecorder` 实现）

- 常量：`TopicAudit = "identity.audit"`、`EventTypeAudit = "audit.event"`。
- `OutboxEmitter{Events outbox.Store}`：`Emit(ctx, audit.Event) error` → `event.Validate()` → `json.Marshal` → `outbox.NewEvent{Topic: TopicAudit, Envelope: mq.Envelope{EventID/Action...}, AggregateType: ResourceType, AggregateID: ResourceID, Now: OccurredAt}` → `Events.Append(ctx, record)`；`Events == nil` 返回内部错误（禁止静默丢弃）。
- `var _ audit.Emitter = OutboxEmitter{}` 编译期断言。
- `AuditEventHandler(emitter audit.Emitter) outbox.Handler`：解析 `audit.event` → 校验 → `emitter.Emit`；不可解析 / 未知类型 `retry.Permanent`（与 IAM 一致）。

### p5-3 AuthService 事务化与失败关闭

**`internal/identity/app/auth_service.go`**

- `AuthServiceConfig` 新增 `UoW UnitOfWork`；`NewAuthService` 中 `nil` 归一为 `NoopUnitOfWork{}`；`AuthService` 新增 `uow UnitOfWork` 字段。
- 新增两个私有辅助：
  - `withinTx(ctx, fn func(context.Context) error) error`：包装 `s.uow.WithinTx`。
  - `isRejected(err) bool`：`errors.Is(err, apperr.ErrUnauthorized)`，标记「业务拒绝：失败尝试与审计必须落库」。
- 统一包装模式（外层返回原错误，事务内拒绝时返回 nil 以提交）：

```go
func (s *AuthService) loginFlow(...) (LoginResult, error) {
    var result LoginResult
    var rejection error
    txErr := s.uow.WithinTx(ctx, func(txCtx context.Context) error {
        r, err := s.loginInTx(txCtx, ...)
        if err != nil {
            if isRejected(err) { rejection = err; return nil }
            return err
        }
        result = r
        return nil
    })
    if txErr != nil { return LoginResult{}, txErr }
    if rejection != nil { return LoginResult{}, rejection }
    return result, nil
}
```

- 包装范围（原方法体整体改名为 `xxxInTx`，新增同名外层）：
  - `Login`、`CompleteVerifiedLogin`、`Refresh`、`Logout`、`LogoutAll`、`RevokeSession`、`VerifyMFA`、`DisableMFA`。
  - `SetupMFA` 只写 enrollment 且无审计，不包装（避免无意义事务）。
- **Redis 副作用移出事务**（提交后执行）：
  - `Logout`：事务内 = 会话撤销 + 审计；提交后 = `revokeCurrentToken`。
  - `LogoutAll`：事务内 = `BumpTokenVersion` + `RevokeByAccount` + 审计；提交后 = `SaveAccountState` + `revokeCurrentToken`。
  - `RevokeSession`：事务内 = 会话撤销 + 审计；提交后 = 当前会话时 `revokeCurrentToken`。
  - `DisableMFA`：事务内 = 删除 enrollment + `revokeAllSessionsInTx` + 审计；提交后 = `saveRevocationState`。
  - 拆分 `revokeAllSessions` 为 `revokeAllSessionsInTx`（账号版本 + 会话）与 `saveRevocationState`（Redis）。
- **失败分支审计不再忽略**：`Login` 内 4 处、`CompleteVerifiedLogin` 1 处、`Refresh` 2 处 `_ = s.emitAudit(...)` 改为检查错误并返回；审计失败 → 事务回滚 / 返回 5xx（失败关闭）。
- `RecordExternalLoginFailure` 改为返回 `error`，内部用 `withinTx` 写审计；`internal/identity/app/sms_login.go` 两处调用改为 `if err := ...; err != nil { return LoginResult{}, err }`。

### p5-4 补齐锁定 / 停用事件

- `internal/shared/audit/actions.go`：新增 `ActionAuthAccountLocked = "auth.account.locked"`、`ActionAuthAccountDisabled = "auth.account.disabled"`，并加入 `KnownActions()`。
- `AuthService` 新增 `emitAccountLockedIfNeeded(ctx, account, traceID)`：`account.Status == domain.AccountStatusLocked` 时发 `auth.account.locked`（Outcome=failure，reason=locked）。
- 调用点：`Login` 的 3 个失败分支（授权拒绝、凭据缺失、密码错误）、`CompleteVerifiedLogin` 授权拒绝分支、`mfaFailure`（`RecordFailure` 之后）。
  - 说明：`LoginGuard.RecordFailure` 达阈值时会原地把 `account.Status` 置为 locked，事后判断即可，无需新增仓储查询。
- `docs/audit/EVENTS.md`：
  - 新增 `auth.account.locked`（端 A/B/C，active）与 `auth.account.disabled`（端 A，planned）。
  - 端别修正：`auth.logout`、`auth.token.refresh`、`auth.token.reused` 改为 A/B/C（C 端阶段 4 已实现）；`auth.token.revoked` 保持 A/B。

### p5-5 装配接线

**`cmd/internal/identityruntime/runtime.go`**

- 构造 `uow`：`deps.Database != nil` → `persistence.NewSQLUnitOfWork(deps.Database.SQL())`，否则 `app.NoopUnitOfWork{}`。
- 构造审计出口：`deps.Async != nil` → `app.OutboxEmitter{Events: deps.Async.Store()}`，否则 `audit.SlogEmitter{Log: deps.Logger}`。
- 把 `Audit`、`UoW` 传入 `AuthServiceConfig`。
- 新增 `func RegisterAsync(deps *platform.Deps) error`：`deps.Async == nil` 时 no-op；否则 `deps.Async.Register(app.TopicAudit, app.AuditEventHandler(audit.SlogEmitter{Log: deps.Logger}))`。
- `New` 末尾 `deps.Async != nil` 时调用 `RegisterAsync(deps)`（与 `iamruntime` 自注册方式一致）。

**`internal/platform/bootstrap.go`**

- 把 `NewAsyncRuntime(deps)` + `deps.Async = asyncRuntime` + `background.add(asyncRuntime.Run)` 整块上移到 `NewSessionValidator` 之前，保证身份运行时构造时可见 `deps.Async`。构造依赖仅 `Config / Database / Metrics / Logger`，均已在 `deps` 中。

**`cmd/worker/main.go`**

- `iamruntime.RegisterAsync(deps)` 之后追加 `identityruntime.RegisterAsync(deps)`，错误包装为 `register identity async handlers`。

### p5-6 审计单测（AC-ENT-01-5）

**新增 `internal/identity/app/audit_test.go`**（`package app_test`）

- 新增 `captureEmitter`（实现 `audit.Emitter`，互斥收集 `[]audit.Event`）。
- 复用 `auth_service_test.go` 的 `newAuthFixture`，通过 `withAuthConfig` 注入 `captureEmitter`。
- 用例：
  - 登录成功 → 1 条 `auth.login`（Outcome=success）；
  - 密码错误 → `auth.login_failed`（reason=bad_credential）；
  - 连续 5 次失败 → 第 5 次同时出现 `auth.login_failed` 与 `auth.account.locked`；
  - 登出 → `auth.logout`；刷新成功 → `auth.token.refresh`；旧令牌重放 → `auth.token.reused`；
  - MFA：绑定确认 → `auth.mfa.bound`、错误码 → `auth.mfa.failed`、解绑 → `auth.mfa.disabled`（复用 `mfa_test.go` 的夹具写法）；
  - 敏感字段断言：把所有事件 `json.Marshal` 后断言不含明文密码、验证码、refresh token、TOTP 密钥、恢复码、完整手机号。
- 追加 `TestKnownActionsIncludeAccountEvents`：断言两个新 action 在 `KnownActions()` 中。

### p5-7 `check-authn` 门禁

**新增 `scripts/ci/check-authn.sh`**（沿用 `set -euo pipefail` + `ROOT` + `FAIL=0` 风格）

| 检查 | 实现 |
|---|---|
| 生产配置 | `grep -q 'identity.mfa.admin_required must be true' internal/platform/config.go`；`go test -count=1 ./internal/platform/ -run 'TestValidateMFA\|TestValidateSMS\|TestValidateOAuth\|TestValidateCChannel'`（规则由配置单测兜底，不断言 local+demo 示例） |
| 三端契约 | `api/openapi/{admin,biz,c}.yaml` 均含 `auth/login`、`auth/refresh`、`auth/logout`（`grep -Fq`） |
| 撤销路径 | `grep -Rq 'RevocationChecker' internal/security`；且 `grep -Rl --include='*_test.go' 'RevocationChecker' internal/security internal/identity` 非空 |
| 迁移 | `migrations/000011_create_identity_tables.{up,down}.sql` 成对存在；`000011_` 前缀文件数为 1 |
| 审计 | 从 `docs/audit/EVENTS.md` 提取所有 `auth.*` action，逐个断言在 `internal/shared/audit/actions.go` 中出现 ≥2 次（常量 + KnownActions） |
| 密钥 | 扫描 `configs/*.yaml.example` 的 `secret_key` / `client_secret` 非空值，占位符（`changeme` / `placeholder` / `your-` / `example` / `<...>` / `${`）放行，其余失败 |

**接入**：

- `Makefile`：`.PHONY` 追加 `check-authn`；新增 `check-authn: bash scripts/ci/check-authn.sh`；`ci:` 末尾列表加入 `check-authn`。
- `scripts/ci/gitee-job.sh` 的 `arch)` 分支追加 `bash scripts/ci/check-authn.sh`（GitHub Actions 与 Gitee 均通过该入口调用 arch job，无需改 yml）。
- 失败验证：临时构造违规样例（如临时删除 EVENTS.md 一行 action）运行脚本确认退出码 1，验证后立即还原并删除临时文件（不提交）。

### p5-8 真实依赖集成测试

**新增 `tests/integration/identity/revocation_test.go`**（`package identity_test`，含共享 helper）

- 共享 helper（供同包另一个文件复用）：
  - `testDB(t)`：`testkit.MySQL(t)`，返回 `*sql.DB`；`reset(t, db)` 按外键顺序清空 `identity_oauth_identity`、`identity_mfa`、`identity_login_attempt`、`identity_session`、`identity_credential`、`identity_account` 与 `async_consumed`、`async_dlq`、`async_outbox`。
  - `newChecker(t, db, redisClient)`：`persistence.NewSQLAccountRepository` + `identitycache.NewRedisRevocationStore(testkit.Redis(t).Cache())` + `app.NewRevocationChecker(..., RevocationFailClosed, 0)`。
- 用例：
  - `TestRevocationAfterAccountDisabled`：建号 → 保存 → `account.Disable(now)` + `accounts.Save` → 用旧 claims（`ver=1`）调用 `Check` 得到 `apperr.ErrUnauthorized`。
  - `TestRevocationAfterLogoutAll`：`account.BumpTokenVersion` + Save → 旧 `ver` claims 拒绝；新 `ver` claims 通过。
  - `TestRevocationJTIBlacklist`：`store.RevokeToken(jti, ttl)` → 同 jti claims 拒绝。
  - `TestRevocationFailClosed`：关闭 Redis 客户端后 `Check` 返回 `apperr.ErrUnavailable`（未配置 `TEST_REDIS_ADDR` 时 `testkit.Redis` 已跳过）。

**新增 `tests/integration/identity/refresh_reuse_test.go`**

- 复用 `revocation_test.go` 的 `testDB` / `reset`。
- 构造 `AuthService`：SQL 六仓储 + `cache.NewMemoryRevocationStore` + `jwt.NewHMAC` + `TokenService` + `LoginGuard` + 真实 `pkg/password` 哈希器 + `audit.SlogEmitter`（日志丢弃），`UoW` 使用 `persistence.NewSQLUnitOfWork`。
- 用例：
  - `TestRefreshReuseRevokesFamily`：建号 + 凭据 → `Login` → `Refresh` → 用旧 refresh token 再刷新 → 401；断言 `RevokeFamily` 后同 Family 会话全部 `revoked`；断言 `async_outbox` 中存在 `topic='identity.audit'` 且载荷 action=`auth.token.reused`。
  - `TestConcurrentRefreshOnlyOneSucceeds`：10 个 goroutine 同 token 并发刷新，成功数 = 1，其余 401，且会话链被撤销。
- 包内测试必须可串行（CI 以 `-p 1` 执行，已有脚本保证）。

### p5-9 文档状态回写

- `docs/identity-authn.md` §18 状态表：阶段 5 → `已完成`。
- `docs/production-readiness-implementation-plan.md`：ENT-01 章节状态行与「十二、状态跟踪」中 ENT-01 行更新为「已完成（2026-10-23，阶段 1–5）」；T9/T10 标记完成。
- `docs/production-readiness-gaps.md`：`AC-ENT-01-1`～`5` 勾选；ENT-01 状态行更新为「已完成」。
- `docs/audit/EVENTS.md`：按 p5-4 更新。

### p5-10 全量验证

依次执行（Windows + Git Bash 环境）：

```bash
gofmt -l internal cmd pkg tests
go build ./...
go vet ./...
go test ./...
bash scripts/ci/check-audit.sh
bash scripts/ci/check-config.sh
bash scripts/ci/check-openapi.sh
bash scripts/ci/check-migrations.sh
bash scripts/ci/check-layering.sh
bash scripts/ci/check-transport-imports.sh
bash scripts/ci/check-outbox.sh
bash scripts/ci/check-idempotency.sh
bash scripts/ci/check-deps.sh
bash scripts/ci/check-authn.sh
bash scripts/ci/run-tests.sh          # make test-ci 等价
bash scripts/ci/run-integration-tests.sh   # Docker 可用时
make ci                                # make 可用时；否则以上脚本集合等价
```

要求：全部通过；集成测试未配置 Docker 时脚本打印 SKIP（如出现 SKIP，需在总结中明确说明）。

---

## 关键决策与假设

1. **失败路径也必须提交**：401 类业务拒绝（含登录失败尝试、重放撤销、MFA 失败）的事务内返回 `nil` 提交，事务外返回原错误；只有基础设施错误回滚。否则失败审计会随回滚丢失。
2. **Redis 副作用在提交后**：`jti` 黑名单与账号撤销快照无法回滚，移出事务避免「DB 回滚但令牌已拉黑」的不一致；失败时返回 5xx，由调用方幂等重试。
3. **审计失败即失败关闭**：认证成功路径审计写入失败 → 回滚并返回 5xx；与 §11.2 一致。
4. **`auth.account.disabled` 本期仅目录占位**：阶段 5 无管理员停用端点，`EVENTS.md` 标 `planned`，`actions.go` 常量与 `KnownActions()` 先行登记。
5. **内存模式退化**：`db.driver=memory` 或 `async.enabled=false` 时用 `NoopUnitOfWork` + `SlogEmitter`，仅限 local/test。
6. **check-authn 的生产配置断言以配置单测兜底**：示例配置本身是 local+demo，脚本不断言示例值，只断言规则存在且单测通过。
7. **MFA 票据消费仍在事务内**：票据为一次性凭据，事务回滚导致票据作废属于安全方向的失败关闭，不做补偿。
8. **集成测试表清理范围**：identity 六表 + `async_*` 三表，按子表优先顺序删除；`-p 1` 串行避免与 IAM 清表冲突。

## 不做的范围

- 不新增管理员停用 / 解锁端点，不实现 `auth.account.disabled` 的发出路径。
- 不改令牌模型、OpenAPI 契约、配置字段、迁移脚本。
- 不改动 IAM 既有实现（仅复用其模式）。
- 不把集成测试并入 `make ci`（仍走 `make test-integration`）。
