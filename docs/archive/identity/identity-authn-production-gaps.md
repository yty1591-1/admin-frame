# 身份域生产级缺陷与整改建议（internal/identity）

> **范围**：`internal/identity`（domain / app / infrastructure / transport）及其装配（`cmd/internal/identityruntime`）、配置校验（`internal/platform/config.go`）与相关迁移。
> **审查方式**：静态代码审查（未执行压测与故障注入）。
> **审查日期**：2026-10-29。
> **基线**：当前 `main` 工作区；行号可能在后续提交中漂移，定位以文件与函数名为准。
> **关联文档**：[identity-authn.md](../.././identity-authn.md)、[production-readiness-gaps.md](../.././production-readiness-gaps.md)、[AUDIT_STANDARDS.md](../.././AUDIT_STANDARDS.md)、[CONFIG_STANDARDS.md](../.././CONFIG_STANDARDS.md)。

## 一、结论摘要

登录、刷新、MFA、登出、撤销的主链路已经闭环，但仍有 **4 项 P0 阻断、7 项 P1 高风险、5 项 P2 配置/合规/硬化项**。其中 P0 项会直接导致安全承诺（≤60s 撤销、失败锁定、MFA 强制）不成立，或让生产环境缺少必要的账号运营能力。

| ID | 级别 | 缺陷 | 主要影响 |
|---|---|---|---|
| IDN-P0-01 | P0 | 撤销快照可能陈旧，旧 Access Token 最长约 1h 仍有效 | 安全：撤销/禁用承诺不成立 |
| IDN-P0-02 | P0 | 账号生命周期与 C 端开户路径缺失 | 运营：无法改密/禁用/解绑 MFA；SMS-only C 端不可用 |
| IDN-P0-03 | P0 | 失败锁定状态机有缺口，可被绕过 | 安全：pending/过期锁定可无限试密码 |
| IDN-P0-04 | P0 | B 端 MFA 配置强制但运行时不生效 | 安全：生产配置承诺与实际不符 |
| IDN-P1-01 | P1 | 刷新即新增会话行，会话表无界增长、列表失真 | 容量/体验：行数 O(刷新次数)，列表不可用 |
| IDN-P1-02 | P1 | 会话级撤销只挡刷新，不挡 Access Token | 安全：被撤销设备仍可用到 token 过期 |
| IDN-P1-03 | P1 | 撤销失败被静默丢弃，app 层无日志/指标 | 安全/可观测：失败不可发现 |
| IDN-P1-04 | P1 | 限流可被 XFF 伪造/新连接/多副本绕过 | 安全：暴力破解与短信刷量 |
| IDN-P1-05 | P1 | MFA 第二因子硬化不足（重放/恢复码/并发/轮换） | 安全：MFA 可被削弱 |
| IDN-P1-06 | P1 | PII 摘要与短信码哈希强度不足 | 隐私：库泄露后字典枚举 |
| IDN-P1-07 | P1 | 刷新令牌无限滑动续期，无绝对上限 | 安全：被盗刷新令牌永久续命 |
| IDN-P2-01 | P2 | 生产配置校验缺口（内存 DB、password/max_per_account 未接线） | 可靠性/一致性 |
| IDN-P2-02 | P2 | 审计覆盖缺口与审计落点缺失 | 合规 |
| IDN-P2-03 | P2 | 令牌与 MFA 敏感响应缺少 no-store | 合规/安全 |
| IDN-P2-04 | P2 | 账号枚举时序侧信道 | 安全 |
| IDN-P2-05 | P2 | OAuth 刷 state、Argon2/SMS 资源耗尽 | 可用性/成本 |

### 严重级别定义

| 级别 | 定义 | 处理要求 |
|---|---|---|
| P0 | 直接破坏安全承诺、导致生产不可运营或配置承诺与实际不符 | 上线前必须修复，或明确关闭对应能力 |
| P1 | 高风险安全问题或会随规模放大、导致不可运维 | 首个生产版本内修复 |
| P2 | 合规、 hardening、容量与配置一致性 | 排期修复，修复前记录风险例外 |

---

## 二、P0 阻断项

### IDN-P0-01 撤销快照可能陈旧，旧 Access Token 最长约 1h 仍有效

**现状（证据）**

- `RevocationChecker.loadState` 命中 Redis 后直接返回，不再回源数据库：[revocation.go](../../../internal/identity/app/revocation.go#L145-L162)。
- 账号快照缓存 TTL 下限为 1 小时：[revocation.go](../../../internal/identity/app/revocation.go#L23-L24)；默认取 `access_ttl + 1m`，实际通常为 1h。
- `LogoutAll` 与 `DisableMFA` 都在数据库事务提交后才写快照：[auth_service.go](../../../internal/identity/app/auth_service.go#L671-L676)、[mfa.go](../../../internal/identity/app/mfa.go#L592-L601)。写失败会返回 500，但数据库已提交；进程在提交与写缓存之间崩溃同样留下窗口。
- `RevocationChecker` 比较的是 `claims.ver < state.Version`；缓存仍是旧版本时，旧令牌的 `ver` 与缓存版本相等，直接放行：[revocation.go](../../../internal/identity/app/revocation.go#L116-L142)。
- 文档承诺账号禁用/改密 ≤60s 生效：[identity-authn.md](../.././identity-authn.md#L363-L367)。

**影响**

- `logout-all`、禁用、改密之后，旧 Access Token 在缓存 TTL 内仍然有效；多副本部署时每个副本都可能持有旧快照。
- 未来若 IAM 或运维直接改库（当前没有身份域禁用端点，见 IDN-P0-02），没有任何缓存失效入口，问题会被放大。
- Redis 写失败返回 500 会误导客户端以为操作失败，但账号实际已不可用（或相反：旧令牌仍可用），状态不一致。

**生产级整改建议**

1. **明确 DB 为唯一事实源，Redis 只做加速层**：缓存值改为 `{version, status, fetched_at, key_id}`；命中时若 `now - fetched_at` 超过允许陈旧度（生产建议 30s，硬上限 60s），必须回源数据库并回填。
2. **收紧 TTL**：新增 `identity.revocation.state_ttl`，生产校验强制 `≤60s`；默认取 `min(access_ttl + 1m, 60s)`。文档同步修订 TTL 表。
3. **变更路径统一失效**：新增 `RevocationInvalidator` 端口，所有 `token_version` 变更（禁用、改密、删除、MFA 解绑、logout-all）在同一用例中调用；事务提交后执行 `SET`，失败时执行 `DEL` 并重试，仍失败则记录 Error 日志 + 指标 + 告警。禁止任何绕过该端口的账号状态直写（代码评审 + CI 检索 + DB 写权限收敛）。
4. **避免“提交后失败即 500”**：数据库事务提交后缓存写入失败时，不要把业务结果改成失败；应返回成功，同时异步重试失效并将 `revocation_cache_write_error_total` 置为告警。若 Redis 整体不可用，Checker 的 fail_closed 会把后续请求统一变成 50300，语义一致。
5. **低访问量兜底**：对高风险管理操作，可在提交后同步 `DEL` 账号快照，让下一次请求必然回源 DB，而不是依赖写入新值。
6. **并发回源保护**：按 `account_id` 做 singleflight，避免缓存失效瞬间大量请求同时打 DB。

**验收标准**

- 预热缓存为 active 状态后，直接 `Disable` + `Save`（不写缓存），旧令牌在下一次 `Check` 必须被拒绝；新增集成测试覆盖“缓存命中 + DB 已变更”路径。
- 模拟 Redis `SET` 失败：业务返回成功，旧令牌在 ≤60s 内被拒绝（通过 `DEL` 或 TTL），并产生告警指标。
- `go test ./internal/identity/... ./tests/integration/identity/...` 通过，新增用例在缓存预热场景下不依赖 sleep。

**涉及变更**：`app/revocation.go`、`app/auth_service.go`、`app/mfa.go`、`infrastructure/cache/revocation_redis.go`、`internal/platform/config.go`、`docs/identity-authn.md`、集成测试。

---

### IDN-P0-02 账号生命周期与 C 端开户路径缺失

**现状（证据）**

- A/B 端首个管理员由迁移 [`000005_seed_bootstrap_admin`](../../../migrations/000005_seed_bootstrap_admin.up.sql) 开通；C 端只能走短信 / OAuth 自助开户，没有密码凭据。
- 应用层没有改密、重置密码、禁用、启用、解锁、重置 MFA 的用例或 HTTP 端点。`Account.Disable`、`ChangePassword`、`Unlock` 只被领域测试调用：[account.go](../../../internal/identity/domain/account.go#L253-L313)。
- C 端短信登录按 `phone:<hash>` 查找已存在账号，找不到直接失败：[sms_login.go](../../../internal/identity/app/sms_login.go#L227-L243)；仓库内没有任何创建 `phone` 凭据或绑定手机号的路径（`CredentialTypePhone` 只在领域与测试中出现）。
- OAuth 只有在 `auto_provision=true` 时才自动建号：[oauth.go](../../../internal/identity/app/oauth.go#L228-L280)；默认 `auto_provision=false`。
- `auth.account.disabled` 审计 action 已定义，但全仓没有发出点。

**影响**

- 客服/运维无法重置密码、解绑 MFA、禁用离职员工；账号一旦锁定或丢失 MFA，只能人工改库。
- `channel=c` 可以只启用短信并在生产通过配置校验，但没有任何用户能完成首次开户；SMS-only 部署实际不可用。
- 文档中的“账号禁用 ≤60s 生效”无法被真实业务路径触发。

**生产级整改建议**

1. **新增 `AccountService`（app 层）与 A 端账号管理端点**，至少覆盖：
   - 用户改密：校验旧密码 + 新密码策略 + 禁止与旧密码相同；成功后 `token_version++`、撤销全部会话、写审计。
   - 管理员重置密码：生成一次性临时口令或重置令牌，设置 `must_change_password`；首次登录强制改密；全程不写日志。
   - 禁用/启用：`Disable` + `token_version++` + 撤销会话 + 快照失效 + `auth.account.disabled` 审计；启用不恢复旧会话。
   - 解锁：管理员手动 `Unlock`，写审计。
   - 重置 MFA：管理员在完成二次确认/审批后删除绑定、撤销会话、写 `auth.mfa.reset` 审计；必须支持“用户丢失 TOTP 与恢复码”的场景。
2. **高风险操作接入审批**：禁用、重置 MFA、重置密码属于高风险写操作，按项目约定接入 IAM 审批或破玻璃流程，申请人不得自审；端点必须挂权限点，禁止匿名管理端点。
3. **C 端开户**：
   - 方案 A（推荐）：新增 `POST /c/v1/auth/sms/register`（或 `phone/bind`）：验证码校验通过后，在同一事务内创建账号 + 手机号凭据 + 审计事件；手机号摘要作为唯一登录主体，防止并发重复开户。
   - 方案 B：明确 C 端只允许 OAuth 自动建号；则配置校验必须要求至少一个 provider 且 `auto_provision=true`，SMS-only 部署直接启动失败，并在文档中写明无自助注册。
   - 若未来支持手机号换绑，必须同时更新账号 `subject` 与凭据，并使用唯一键保证一个手机号只对应一个账号。
4. **迁移与兼容**：新增 `must_change_password` 列、必要的账号管理审计 action；若已有 C 账号数据，补一次核对脚本，确保 `phone` 主体与凭据一一对应。
5. **防枚举与防滥用**：注册端点必须使用与登录相同的统一错误文案、Redis 限流、短信发送配额与审计。

**验收标准**

- 新增端到端用例：改密后旧会话全部 401；禁用后旧令牌 ≤60s 失效；重置 MFA 后用户可用新绑定登录；C 端短信注册后可立即登录。
- 所有管理端点具备权限校验与审计；未授权调用返回 403/401，不泄露账号是否存在。
- `auth.account.disabled`、`auth.mfa.reset` 进入 `KnownActions()` 与 `docs/audit/EVENTS.md`。

**涉及变更**：新增 `app/account_service.go`、`transport/admin` 路由、OpenAPI admin 契约、迁移、审计目录、配置示例。

---

### IDN-P0-03 失败锁定状态机有缺口，可被绕过

**现状（证据）**

- `RecordFailure` 只在 `account.Status == active` 时执行 `Lock`：[login_guard.go](../../../internal/identity/app/login_guard.go#L199-L207)。`pending` 账号永远不会锁定，而 seed 创建出来的账号正是 `pending`。
- 锁定期满后，`Authorize` 因 `LockedUntil` 已过而放行：[login_guard.go](../../../internal/identity/app/login_guard.go#L151-L162)；但 `RecordFailure` 看到状态仍是 `locked`，直接返回，不再计数或重新锁定。`TryUnlockExpired` 只在 `RecordSuccess` 中调用：[login_guard.go](../../../internal/identity/app/login_guard.go#L210-L248)。
- 失败计数是“先 `Append`，再 `CountFailuresSince`，最后 `Save`”：并发请求在默认 REPEATABLE READ 隔离级别下互相看不到未提交记录，阈值可能漏判；临界点 `Save` 返回版本冲突后，被上层包装成 500：[auth_service.go](../../../internal/identity/app/auth_service.go#L303-L315)。
- `emitAccountLockedIfNeeded` 只看最终状态，对已经锁定的账号每次失败都会重复发 `auth.account.locked`：[auth_service.go](../../../internal/identity/app/auth_service.go#L944-L950)。

**影响**

- 攻击者可以对 `pending` 账号无限试密码；对已锁定账号，等锁定期过后同样可以无限试密码。5 次锁定只在“首次从 active 进入 locked”时有效。
- 并发失败可能导致锁定漏判，或让正常客户端收到 500 而非 401。
- 审计被重复的锁定事件污染。

**生产级整改建议**

1. **统一状态迁移**：新增 `Account.UnlockIfExpired(now)`，在 `Authorize` 成功判定前和 `RecordFailure` 计数前都尝试把过期的 `locked` 迁回 `active`（带乐观锁，冲突则重读）。`pending` 账号允许登录，也允许被锁定。
2. **锁定条件改为“非 disabled / 非 deleted”**，而不是仅 `active`；锁定后的失败仍追加尝试但不再重复锁定，只有状态发生迁移时才发锁定审计。
3. **计数原子化**：
   - 优先在仓储层新增 `RecordFailureAndMaybeLock`，在单个数据库事务中对 `identity_account` 行 `SELECT ... FOR UPDATE`，再统计窗口失败数并更新锁定；并发请求串行化，避免 TOCTOU。
   - 或在 Redis 使用 `INCR + EXPIRE` 维护窗口计数，DB 只保存最终锁定状态；Redis 故障时 fail_closed（生产）或回退 DB 行锁。
4. **版本冲突处理**：`accounts.Save` 返回 40900 时重试 1～2 次，仍冲突则按 401 处理，不返回 500；同时记录指标。
5. **策略增强**：引入渐进延迟（如第 3 次失败后增加 1s/2s/4s 延迟）与“账号 + IP”双维度限制，降低对已知账号的恶意锁定 DoS。
6. **审计语义**：`RecordFailure` 返回 `lockedNow bool`，仅当本次触发迁移时发 `auth.account.locked`；对已锁定账号的失败只发 `auth.login_failed(reason=locked)`。

**验收标准**

- 表驱动用例：pending 账号连续 5 次失败后锁定；锁定过期后再次失败会重新锁定；正确密码在过期锁定后能成功解锁。
- 并发用例：20 个 goroutine 同时错误登录，账号被锁定，所有响应为 401/429，无 500。
- 审计用例：一次锁定只产生一次 `auth.account.locked`；后续失败不重复。
- `go test -race ./internal/identity/app/...` 通过。

**涉及变更**：`domain/account.go`、`app/login_guard.go`、`app/auth_service.go`、`infrastructure/persistence/mysql_account.go`、Redis limiter（可选）、测试。

---

### IDN-P0-04 B 端 MFA 配置强制但运行时不生效

**现状（证据）**

- prod/staging 对 `channel != c` 强制 `identity.mfa.admin_required=true`：[config.go](../../../internal/platform/config.go#L1496-L1509)。
- `mfaChallenge` 只要 `s.channel != admin` 就直接返回 `mfaGateNotRequired`：[mfa.go](../../../internal/identity/app/mfa.go#L289-L301)。
- `SetupMFA` / `VerifyMFA` 对非 admin 直接返回 40000：[mfa.go](../../../internal/identity/app/mfa.go#L79-L82)、[mfa.go](../../../internal/identity/app/mfa.go#L191-L197)。
- B 端路由没有注册任何 MFA 端点：[router.go](../../../internal/identity/transport/biz/router.go#L16-L28)。
- `configs/biz.yaml.example` 没有 `mfa` 段，开启 identity 后按生产校验会因缺少 `admin_required/secret_key` 启动失败。

**影响**

- `channel=biz` 的生产配置声称强制 MFA，实际完全不校验，形成“配置合规、运行不安全”的假象。
- 若按文档开启 B 端生产配置，可能因缺少 MFA 配置而启动失败，运维只能照抄 A 端配置，进一步加深误解。

**生产级整改建议**

二选一，必须在配置、文档、代码三处一致：

- **方案 A：B 端不要求 MFA**
  - 配置校验改为仅 `channel == admin` 时强制 `admin_required=true`；`channel == biz` 显式禁止设置 `admin_required=true`（fail fast，避免“设了但无效”）。
  - 文档 §7 与部署文档同步说明“MFA 仅 A 端”。
  - `AuthService` 对 B 端强制 `mfaRequired=false`，并在启动日志打印实际生效值。
- **方案 B：B 端要求 MFA**
  - 把 `mfaChallenge` 与 `SetupMFA/VerifyMFA/DisableMFA` 的端别判断改为 `admin || biz`；票据 audience/scope 按端别签发与校验（不能共用 admin audience）。
  - B 端注册 setup/verify（按需 disable），并更新 OpenAPI biz 契约、前端调用与配置示例。
  - B 端 MFA 的 `merchant_id` 仍由账号推导，票据不得跨端使用。
- 无论哪种方案，都补 channel 维度测试矩阵，并增加配置门禁：生产配置中 `mfa.admin_required` 与实际支持端别不匹配时启动失败。

**验收标准**

- `channel=biz` 生产配置：方案 A 启动成功且登录不返回 MFA 挑战；方案 B 未绑定账号返回 `mfa_binding_required`，绑定后可完成登录。
- 配置单测覆盖 admin/biz/c 三种端别与 `admin_required` 组合。
- `make check-authn` 或等价门禁能阻止“B 端强制 MFA 但未实现”的配置。

**涉及变更**：`internal/platform/config.go`、`app/mfa.go`、`app/auth_service.go`、`transport/biz/router.go`、OpenAPI、配置示例、文档。

---

## 三、P1 高风险项

### IDN-P1-01 刷新即新增会话行，会话表无界增长、列表失真

**现状（证据）**

- 每次刷新都创建新的 `identity_session` 行：[auth_service.go](../../../internal/identity/app/auth_service.go#L826-L857)；旧行只标记 `rotated`，不删除。
- `ListByAccount` 返回账号全部会话，按创建时间倒序，无分页、无 family 聚合：[mysql_session.go](../../../internal/identity/infrastructure/persistence/mysql_session.go#L184-L210)。
- `Session` 与 `sessionDTO` 没有 IP/UA 字段：[session.go](../../../internal/identity/domain/session.go#L81-L108)、[handler.go](../../../internal/identity/transport/rest/handler.go#L361-L371)；文档却要求会话列表返回 IP/UA 摘要：[identity-authn.md](../.././identity-authn.md#L326-L328)。
- `identity.session.max_per_account` 只出现在配置与校验中：[config.go](../../../internal/platform/config.go#L195-L196)、[config.go](../../../internal/platform/config.go#L920-L921)、[config.go](../../../internal/platform/config.go#L1462-L1464)，运行时无任何引用。
- 迁移注释预留了“过期清理任务扫描”索引：[000001_create_identity_tables.up.sql](../../../migrations/000001_create_identity_tables.up.sql#L69-L70)、[000001_create_identity_tables.up.sql](../../../migrations/000001_create_identity_tables.up.sql#L99-L102)，但 worker 只运行 async relay：[main.go](../../../cmd/worker/main.go#L66-L92)，仓库内没有清理任务。

**影响**

- 一个活跃浏览器按 15m access TTL 刷新，7 天约产生 672 行；10k 用户一周可达数百万行。
- A 端“会话管理”会把每次令牌轮换都显示成一条会话，用户无法识别设备；响应体随账号使用时长无限增长。
- 登录尝试表同样只增不减，最终影响查询性能与存储成本。
- `max_per_account` 不生效，单账号可无限创建会话链。

**生产级整改建议**

1. **拆分“设备会话（family）”与“令牌代（session generation）”语义**：
   - 新增 `identity_session_family`：`family_id`、`account_id`、`created_at`、`last_used_at`、`expires_at`、`absolute_expires_at`、`status`、`ip_hash`、`ua_hash`、`device_label`、`version`。
   - `identity_session` 继续保存每一代刷新令牌哈希，用于重放检测与审计；列表/撤销/上限全部以 family 为单位。
   - 迁移时按 `family_id` 回填 family 表，并把每个 family 的最新 active 会话作为设备当前状态。
2. **会话列表**：只返回 active/近期 revoked 的 family，游标分页；补充 `ip_hash`、`ua_hash`（由登录/刷新时写入，禁止明文）。
3. **实现 `max_per_account`**：登录创建新 family 前，统计 active family 数；达到上限时在同一事务内撤销最久未使用的 family，并写 `auth.session.evicted` 审计。并发下对账号行加锁或使用 Redis 计数。
4. **清理任务**：在 worker 增加可配置的 retention Job（或部署 CronJob）：
   - 过期 family/session 标记 expired；
   - 删除超过保留期的 rotated/revoked 门；
   - 删除超过 180 天的 `identity_login_attempt`（可配置）；
   - 批量 `DELETE ... LIMIT`，按索引扫描，带指标与失败重试。
5. **索引**：为 `(account_id, status, last_used_at)`、`(expires_at)`、`(family_id, status)` 补索引；清理语句避免全表扫描。
6. **审计**：会话淘汰、批量清理写系统审计或至少结构化日志，便于容量分析。

**验收标准**

- 连续刷新 100 次后，会话列表只返回 1 个设备 family；`identity_session` 行数受保留期约束。
- `max_per_account` 生效：第 N+1 次登录会淘汰最久未使用 family，并产生审计。
- 清理 Job 幂等，重复执行不重复删除；有 `deleted_rows` 指标。
- 列表接口有分页，单页响应大小有上限。

**涉及变更**：迁移、domain、仓储、AuthService、transport、worker、配置、OpenAPI、前端会话页。

---

### IDN-P1-02 会话级撤销只挡刷新，不挡 Access Token

**现状（证据）**

- `RevokeSession` 仅在目标会话就是当前会话时把当前 jti 拉黑：`revokeToken = sessionID == actor.SessionID`：[auth_service.go](../../../internal/identity/app/auth_service.go#L781)。
- 其他会话的 Access Token 不会被拉黑，也没有 `token_version` 递增（会话级撤销不改变账号版本）。
- `RevocationChecker` 只校验账号状态、`ver` 与 jti，不读取 `sid` 对应的会话状态：[revocation.go](../../../internal/identity/app/revocation.go#L116-L142)。
- 刷新令牌重放触发的 `RevokeFamily` 也只改数据库会话状态，不使已签发的 Access Token 立即失效。

**影响**

- 管理员在会话管理中撤销某设备后，该设备的 Access Token 仍可继续访问业务接口，直到自然过期（当前配置最长 1h）。
- 刷新令牌重放触发整链撤销后，攻击者已窃取的 Access Token 仍可用到过期；“整链撤销”只对刷新生效。
- 与文档“单会话撤销下一次请求立即生效”的语义不一致。

**生产级整改建议**

1. **在 Checker 中增加会话状态校验**：Access Token 已携带 `sid`；新增 `SessionStateStore`（Redis）保存 `{status, revoked_at, expires_at, epoch}`，键为 `authn:sid:<sid>`；登录/刷新/撤销时写入，Checker 在账号校验后校验会话。陈旧度与账号快照一致（≤60s）。
2. **会话撤销写穿**：`RevokeSession`、`Logout`、`RevokeFamily` 在同一事务提交后写 `authn:sid:<sid>`（或直接 DEL 让下次回源 DB）；写失败按 IDN-P0-01 的策略处理。
3. **降低对缓存的依赖**：若不想引入 sid 缓存，可在会话撤销时递增 family 级 `session_epoch` 并写入 Access Token 的 `sver`，Checker 比较 `sver` 与 family 当前 epoch；仍需短 TTL 缓存。
4. **兜底**：把 `access_ttl` 上限从 1h 收紧到 15m（生产推荐），并明确“会话撤销最终一致性 ≤ access_ttl”作为可接受降级；但默认方案应做到 ≤60s。
5. **审计与指标**：`auth.session.revoked` 记录 `session_id` 摘要、`family_id` 摘要、撤销原因；指标 `identity_session_revoke_total{reason}`。

**验收标准**

- 管理员撤销非当前会话后，该会话 Access Token 在 ≤60s 内被拒绝。
- 刷新令牌重放撤销 family 后，该 family 所有未过期 Access Token 被拒绝。
- 集成测试覆盖 Redis 预热、缓存写失败、单副本/多副本。

**涉及变更**：`app/revocation.go`、`app/auth_service.go`、`infrastructure/cache`、`domain/session.go`（epoch/sver）、迁移、测试。

---

### IDN-P1-03 撤销失败被静默丢弃，app 层无日志/指标

**现状（证据）**

- `revokeFamily` 使用 `_, _ = s.sessions.RevokeFamily(...)`，错误被丢弃：[auth_service.go](../../../internal/identity/app/auth_service.go#L872-L878)。随后仍然写 `auth.token.reused` 审计并提交事务。
- app 层没有 logger/metrics 依赖；全包没有结构化日志或指标埋点。
- Redis 撤销存储、短信、OAuth、MFA 的失败只向上返回错误，没有独立可观测信号。

**影响**

- DB 故障或权限问题时，重放检测可能“审计已记录、整链未撤销”，攻击者继续持有有效刷新令牌。
- 无法通过监控发现暴力破解、MFA 失败、重放、撤销存储故障，只能事后查日志。
- 与项目经验“重放撤销逻辑需增加日志与指标监控，避免撤销失败静默”直接冲突。

**生产级整改建议**

1. **错误不再吞掉**：`revokeFamily` 返回 error；`refreshInTx` 在撤销失败时返回 `apperr.Unavailable` 或 `apperr.Internal`，让事务回滚，避免只提交“重放审计”而没撤销。若产品坚持不向客户端暴露 5xx，可返回 401 但必须记录 Error 日志与告警指标。
2. **注入 `Logger` 与 `Metrics`**：为 `AuthService`、`LoginGuard`、`RevocationChecker`、`OAuthService`、`SMSLoginService` 增加可选 logger/metrics 接口（app 层定义端口，platform 注入，保持依赖方向）。
3. **核心指标**：
   - `identity_login_total{channel,result,reason}`
   - `identity_login_duration_seconds`
   - `identity_lockout_total`
   - `identity_mfa_verify_total{result}`
   - `identity_refresh_total{result}`
   - `identity_token_reuse_total`
   - `identity_session_revoke_total{reason}`
   - `identity_revocation_store_errors_total{op}`
   - `identity_revocation_cache_write_error_total`
   - `identity_sms_send_total{result}`
   - `identity_oauth_callback_total{provider,result}`
4. **日志规范**：结构化字段包含 `trace_id`、`channel`、`account_ref`（摘要）、`family_id`（摘要）、`result`、`error_class`；禁止记录密码、令牌原文、验证码、TOTP 密钥、恢复码、完整手机号/邮箱。
5. **告警**：重放率突增、撤销错误、MFA 失败率、锁定量突增、Redis 回源失败、Outbox 堆积。

**验收标准**

- 单元测试用 fake logger/metrics 断言关键失败路径有日志/指标。
- 故障注入：关闭 Redis/DB，重放路径返回 503/500 且产生告警指标，不出现“审计成功但未撤销”。
- 文档新增监控/告警章节。

**涉及变更**：app 各服务、platform 装配、`pkg/metrics`、文档、测试。

---

### IDN-P1-04 限流可被 XFF 伪造、新连接与多副本绕过

**现状（证据）**

- `rest/clientIP` 与 `c/clientIP` 无条件取 `X-Forwarded-For` 第一段；没有可信代理解析：[handler.go](../../../internal/identity/transport/rest/handler.go#L429-L438)、[handler.go](../../../internal/identity/transport/c/handler.go#L193-L201)。
- 无 XFF 时直接返回带端口的 `RemoteAddr` 作为 IP 摘要输入；同一 IP 每个新 TCP 连接端口不同，限流 key 随之变化。
- 登录限流器使用进程内 `ratelimit.NewKeyed`：[runtime.go](../../../cmd/internal/identityruntime/runtime.go#L285-L299)，没有 Redis 驱动；多副本按副本数放大，重启清零。
- 平台级 `rate_limit` 默认关闭，且同样信任 XFF 首段：[pipeline.go](../../../internal/platform/pipeline.go#L220-L231)。

**影响**

- 攻击者可通过伪造 `X-Forwarded-For` 或不断建立新连接绕过按 IP 的登录与短信小时限。
- 多副本部署时，实际允许的登录尝试次数是单副本配置 × 副本数。
- 与 IDN-P0-03 的锁定缺口叠加后，暴力破解几乎没有有效上限；SMS 发送可被用来刷费用。

**生产级整改建议**

1. **统一客户端 IP 解析**：在 `pkg/httpx` 或 platform 提供唯一实现：
   - 配置 `http.trusted_proxies`（CIDR 列表）；只有来自可信代理的请求才解析 XFF。
   - 从右向左跳过可信代理，取第一个不可信地址；禁止直接取最左段。
   - 无 XFF 时使用 `net.SplitHostPort(r.RemoteAddr)`，IPv6 规范化。
   - 所有 transport（identity/IAM/平台限流）复用同一函数，删除重复实现。
2. **分布式限流**：identity 登录限流改用 `pkg/ratelimit.NewRedisKeyed`，生产 `failure_mode=fail_closed`；键设计为 `channel|accountRef|ip` 与 `channel|accountRef` 两级：
   - 账号级（如 20 次/15min）防止 botnet 绕过 IP；
   - IP 级（如 5 次/分钟）；
   - 全局登录并发/速率保护。
3. **SMS 防刷**：Redis 计数器 + 全局日预算 + 供应商熔断；同一手机号发送间隔、日限、IP 小时限全部走 Redis；发送失败回滚重发窗口（当前 SETNX 已占用）。
4. **响应头**：429 设置 `Retry-After`；文档说明限流维度。
5. **测试**：可信代理/伪造 XFF、IPv4/IPv6、多副本共享计数、Redis 故障 fail_closed。

**验收标准**

- 伪造 `X-Forwarded-For` 不再改变限流 key；直连场景同一 IP 不同端口共享同一 key。
- 3 副本部署下，账号级限流总次数等于配置值（±1 秒窗口误差）。
- SMS 刷量测试被账号级 + IP 级 + 全局预算拦截。

**涉及变更**：`pkg/httpx`（或 platform）、identity transport、`cmd/internal/identityruntime`、`pkg/ratelimit`、config、部署文档。

---

### IDN-P1-05 MFA 第二因子硬化不足

**现状（证据）**

- TOTP 校验只比较当前与前后的一个时间步，没有记录已使用的时间步：[totp.go](../../../pkg/totp/totp.go#L48-L74)；`identity_mfa` 表也没有 `last_used_step` 列：[000001_create_identity_tables.up.sql](../../../migrations/000001_create_identity_tables.up.sql#L76-L87)。
- 恢复码由 5 字节随机生成（40 bit），使用无盐 SHA-256 存储：[mfa.go](../../../internal/identity/app/mfa.go#L603-L624)。
- `identity_mfa.Save` 是无版本控制的 `INSERT ... ON DUPLICATE KEY UPDATE`：[mysql_mfa.go](../../../internal/identity/infrastructure/persistence/mysql_mfa.go#L50-L76)。恢复码消费是“读整表 → 删除一项 → 整体写回”，并发下可能把已消费的码写回；`DisableMFA` 的 `Delete` 与 `VerifyMFA` 的 `Save` 并发也可能让绑定“复活”。
- 强制绑定票据在 `setup` 阶段不消费，只在 `verify` 消费；`verify` 遇到已消费票据会走 `mfaFailure` 计入失败并可锁定账号：[mfa.go](../../../internal/identity/app/mfa.go#L432-L440)、[mfa.go](../../../internal/identity/app/mfa.go#L359-L367)。文档 D5 期望“重放返回 40100 且无副作用”。
- MFA 密钥由单一 `identity.mfa.secret_key` 派生，密文只有版本字节，没有 key id：[aead.go](../../../pkg/crypto/aead.go#L42-L57)。密钥轮换后旧密文无法解密，且没有管理员重置 MFA 的入口（IDN-P0-02）。

**影响**

- TOTP 码在 ±1 窗口（约 90s）内可重放；被肩窥/钓鱼获取的码可再次使用。
- 数据库或备份泄露后，40 bit 恢复码可被离线爆破；恢复码并发消费可能失效不彻底。
- 并发 `Delete`/`Save` 可能让已解绑的 MFA 重新出现，或让已消费的恢复码重新可用。
- 密钥轮换会导致所有已绑定用户无法登录/解绑，形成运维事故。
- 强制绑定票据重放可被用来恶意锁定账号，与文档“无副作用”不一致。

**生产级整改建议**

1. **TOTP 防重放**：
   - `identity_mfa` 增加 `last_used_step BIGINT NOT NULL DEFAULT 0` 与 `version BIGINT`。
   - 校验成功后用 CAS 更新：`UPDATE identity_mfa SET last_used_step=?, version=version+1 WHERE account_id=? AND version=? AND last_used_step < ?`；失败则视为重放。
   - 更新必须与登录成功写入同一事务；登录失败回滚时不得推进 `last_used_step`，避免“失败一次就作废当前码”的可用性问题（可按产品策略选择）。
2. **恢复码**：
   - 熵提升到 ≥80 bit（10～16 字节），展示时分组格式化。
   - 存储改为 `HMAC-SHA256(identity.hash_key, "recovery:"+accountID+":"+code)` 或 Argon2id；推荐 HMAC（高熵 + 固定成本）。
   - 消费改为逐条 CAS/原子删除，不要整表回写；可用独立 `identity_mfa_recovery_code` 表（account_id, code_hash, used_at, UNIQUE(account_id, code_hash)）。
   - 新增审计 `auth.mfa.recovery_used`，并在恢复码剩余数量低于阈值时告警。
3. **并发控制**：
   - `identity_mfa` 增加 `version`，所有 `Save/Confirm/Delete` 使用 CAS；`Delete` 与 `Save` 冲突时以“已删除”为最终状态，禁止 upsert 复活。
   - 或者在校验事务内 `SELECT ... FOR UPDATE` 锁定绑定行。
4. **密钥轮换**：
   - 密文格式改为 `version || kid || nonce || ciphertext`，或新增 `key_id` 列；配置支持 `active_key` + `keys[]`。
   - 解密按 kid 选键；登录成功时后台重加密为 active key；提供离线迁移脚本。
   - 管理员 MFA 重置流程（IDN-P0-02）作为最终兜底。
5. **票据语义**：
   - 明确 `mfa_setup_ticket` 是否一次性：若一次性，`setup` 首次调用即消费（或使用单独的一次性 setup nonce），并让 `verify` 使用另一个一次性 nonce；若允许重复 setup，文档删除“一次性”表述。
   - `!consumed` 的重放不应计入密码/MFA 失败次数，避免恶意锁定；仅记录审计 `auth.mfa.ticket_replay`。
6. **限流**：增加账号维度 MFA 尝试限流（不依赖票 jti），如 10 次/15min，超过锁定并审计。

**验收标准**

- 同一个 TOTP 码在窗口内第二次使用被拒绝；并发 10 次使用同一码只有 1 次成功。
- 并发消费 10 个不同恢复码，最终每个码只能成功一次；无码被写回。
- 并发 `DisableMFA` 与 `VerifyMFA` 后，绑定最终为删除状态，不被复活。
- 密钥轮换后旧密文可解密并完成一次登录，随后后台重加密；无 KID 的旧数据有迁移脚本。
- 重放 setup ticket 不导致账号锁定，且产生独立审计。

**涉及变更**：迁移、`domain/mfa.go`、`app/mfa.go`、`infrastructure/persistence/mysql_mfa.go`、`pkg/crypto`、配置、审计目录、测试。

---

### IDN-P1-06 PII 摘要与短信码哈希强度不足

**现状（证据）**

- `HashAccountRef`、`HashClientRef` 使用无盐 SHA-256：[login_guard.go](../../../internal/identity/app/login_guard.go#L266-L272)、[auth_service.go](../../../internal/identity/app/auth_service.go#L952-L960)。
- 手机号主体 `phoneSubject` 直接拼接无盐 SHA-256：[sms_login.go](../../../internal/identity/app/sms_login.go#L256-L264)。
- 短信验证码存储为 `SHA-256("sms:"+phoneHash+code)`：[sms_login.go](../../../internal/identity/app/sms_login.go#L261-L265)，6 位码空间仅 1e6。

**影响**

- 邮箱、手机号、IP 属于低熵数据；拿到数据库/备份/日志的人可以字典或暴力枚举还原，所谓“不可逆摘要”不成立，存在隐私与合规风险。
- 短信码哈希在 Redis 或备份泄露后可瞬间反推当前验证码，绕过二次验证。
- 审计、登录尝试表、会话表都使用这些摘要，泄露面较大。

**生产级整改建议**

1. **统一改为带密钥摘要**：
   - 新增 `identity.hash_key`（32 字节，环境变量/密钥管理），支持 `key_id` 与双读轮换。
   - 定义 `HMAC-SHA256(key, domain || "|" || normalized)`；domain 分隔：`account-ref`、`client-ip`、`client-ua`、`phone-subject`、`sms-code`、`recovery-code`。
   - 存储格式 `v1:<hex>` 或增加 `key_version` 列，便于轮换。
2. **迁移策略**：
   - 现有 `account_ref`/`ip_hash`/`ua_hash` 已是摘要，无法反推重算；可采用“新数据新格式、旧数据双读”的兼容方案，或在下一次登录时重写（能做到的字段）。
   - `identity_account.subject` 中的 `phone:<hash>` 需要迁移；若无法反推，至少新增账号使用 HMAC，旧账号保留并在文档标记。
3. **短信码**：改为 `HMAC-SHA256(identity.hash_key, "sms-code|"+phoneHash+"|"+code)`；即使 Redis 泄露，没有 key 无法离线枚举。保持常量时间比较。
4. **密钥管理**：`identity.hash_key` 生产必填；禁止写入 YAML/示例；启动时校验长度；提供轮换 SOP 与离线回填脚本。
5. **日志**：所有摘要日志标记 `hash_key_id`，不打印原文；审计字段继续禁止 PII。

**验收标准**

- 使用已知向量测试 HMAC 实现；不同 domain/端别不可交叉复用。
- 数据库导出后，无密钥无法在合理时间内枚举手机号/邮箱/IP。
- 短信码哈希在无密钥情况下不可离线爆破。
- 密钥轮换后新旧摘要均可校验，完成后再切换写路径。

**涉及变更**：`pkg/crypto`（HMAC 封装）、`app` 摘要调用、迁移/回填脚本、配置、文档。

---

### IDN-P1-07 刷新令牌无限滑动续期，无绝对上限

**现状（证据）**

- `issueNewSession` 每次刷新都设置 `ExpiresAt = now + refresh_ttl`、`IdleExpiresAt = now + idle_timeout`：[auth_service.go](../../../internal/identity/app/auth_service.go#L826-L857)。
- `domain.Session.ExpiresAt` 注释为“绝对过期时间”：[session.go](../../../internal/identity/domain/session.go#L96-L99)，但实际每次刷新都被重算。

**影响**

- 被盗刷新令牌只要持续轮换，会话可以永久续命；设备丢失、令牌泄露没有天然终止点。
- 与“绝对过期”语义不一致，审计与用户预期都会偏差。

**生产级整改建议**

1. 新增 `identity.session.absolute_ttl`（例如 30d，需大于 `refresh_ttl` 且可配置），在 family 上记录 `absolute_expires_at`。
2. `issueNewSession` 计算 `ExpiresAt = min(now + refresh_ttl, family.AbsoluteExpiresAt)`；若已到期，直接拒绝并撤销 family。
3. `IdleExpiresAt` 仍可按最近活动滑动，但不得超过绝对上限。
4. 登录响应与会话列表返回 `absolute_expires_at`，前端可在临近时提示重新登录。
5. 文档修订“刷新令牌有效期”与“会话绝对上限”的区别；配置校验 `absolute_ttl > refresh_ttl`。

**验收标准**

- 连续刷新超过绝对上限后，refresh 返回 401，family 被撤销。
- 会话列表展示绝对到期时间；审计记录 family 绝对到期。
- 配置非法组合启动失败。

**涉及变更**：`domain/session.go`、`app/auth_service.go`、迁移、配置、OpenAPI、文档。

---

## 四、P2 配置、合规与硬化项

### IDN-P2-01 生产配置校验缺口

**现状（证据）**

- `validateIdentity` 不要求持久化数据库；`db.driver=memory` 在生产仅被 IAM 审批/async 条件间接限制：[config.go](../../../internal/platform/config.go#L1441-L1516)、[config.go](../../../internal/platform/config.go#L1186-L1198)。
- `identityruntime.buildRepositories` 在 `deps.Database == nil` 时返回内存仓储：[runtime.go](../../../cmd/internal/identityruntime/runtime.go#L220-L239)。
- `IdentityConfig` 没有 `password` 字段：[config.go](../../../internal/platform/config.go#L76-L97)，运行时固定使用 `app.DefaultPasswordPolicy()`：[runtime.go](../../../cmd/internal/identityruntime/runtime.go#L79-L86)；但文档示例包含 `identity.password`：[identity-authn.md](../.././identity-authn.md#L640-L643)。
- `identity.session.max_per_account` 被校验但未使用（见 IDN-P1-01）。
- `configs/admin.yaml.example`、`configs/biz.yaml.example` 没有 `mfa` 段，生产开启 identity 需要人工补全。

**影响**

- 生产环境可能以内存仓储运行身份域：重启丢账号/会话，多副本互不识别，Access Token 在另一副本直接 401。
- 运维按文档配置 `identity.password` 时会因严格 YAML 解码（未知字段）启动失败，或误以为策略已生效。
- `max_per_account` 给人“已限制”的错觉。

**生产级整改建议**

1. **强制持久化**：`identity.enabled=true` 且 `env in {prod, staging}` 时要求 `db.driver != memory`；运行时 `deps.Database == nil` 直接启动失败（fail fast），不再静默回退内存仓储。
2. **审计/异步前置**：生产身份域要求 `async.enabled=true`（Outbox）或显式配置审计落库；至少启动日志/健康检查标记审计出口，禁止仅 Slog。
3. **补齐 `identity.password`**：新增 `IdentityPasswordConfig{MinLength, MaxLength, RequireComplexity}`，校验范围，装配时传入 `app.PasswordPolicy`；若不实现，删除文档示例并明确“策略固定”。
4. **实现或移除 `max_per_account`**：优先按 IDN-P1-01 实现；实现前在生产校验中拒绝非默认值，避免“配置有效”的误解。
5. **配置示例补全**：admin/biz/c 示例增加完整 `identity.mfa`、`session.absolute_ttl`、`revocation.state_ttl`、`retention`、`hash_key` 说明（密钥用环境变量占位）。
6. **新增配置项**（详见附录 A）并配套 `config_*_test.go` 表驱动用例。

**验收标准**

- 生产配置 `identity.enabled=true` + `db.driver=memory` 启动失败，错误信息只含字段路径。
- `identity.password` 配置生效：长度/复杂度不符合时启动失败或登录拒绝策略正确。
- `max_per_account` 要么生效，要么配置被拒绝。
- 示例配置可直接通过 `Validate()`（除密钥占位）。

**涉及变更**：`internal/platform/config.go`、`cmd/internal/identityruntime/runtime.go`、配置示例、文档、配置测试。

---

### IDN-P2-02 审计覆盖缺口与审计落点缺失

**现状（证据）**

- `completeMFALogin`、`completeMFASetup`、`confirmMFABinding` 在 `guard.Authorize` 失败（locked/disabled）时直接返回，不写审计：[mfa.go](../../../internal/identity/app/mfa.go#L352-L354)、[mfa.go](../../../internal/identity/app/mfa.go#L425-L427)、[mfa.go](../../../internal/identity/app/mfa.go#L493-L495)。
- OAuth 自动建号没有 `auth.account.created` 审计：[oauth.go](../../../internal/identity/app/oauth.go#L236-L280)。
- `emitAccountLockedIfNeeded` 对已锁定账号的每次失败重复发 `auth.account.locked`（见 IDN-P0-03）。
- `identity.audit` 的消费端固定为 `SlogEmitter`：[runtime.go](../../../cmd/internal/identityruntime/runtime.go#L196-L206)；`SlogEmitter` 只写结构化日志：[emitter.go](../../../internal/shared/audit/emitter.go#L14-L20)。
- `auth.account.disabled`、`auth.mfa.recovery_used` 等 action 未实际发出。

**影响**

- 安全事件（MFA 登录被禁用账号、OAuth 自动建号、恢复码使用）缺少审计，难以满足合规与追责。
- 生产审计仅存在于日志系统，受日志保留/采样/丢失影响，不是不可篡改的审计存储。
- 重复锁定事件污染审计，降低告警信噪比。

**生产级整改建议**

1. **补齐事件**：
   - MFA 校验遇到 locked/disabled：写 `auth.login_failed(reason=locked|disabled)` 后再返回。
   - OAuth 自动建号：在同一事务内写 `auth.account.created(actor_type=system, provider=...)`，不记录 provider subject 原文。
   - 恢复码使用：写 `auth.mfa.recovery_used`（含剩余数量，不含码值）。
   - 会话淘汰/批量清理/账号管理操作：新增对应 action 并登记 `KnownActions()` 与 `docs/audit/EVENTS.md`。
2. **锁定事件去重**：只在实际发生 `active/pending -> locked` 迁移时发 `auth.account.locked`；后续失败只发 login_failed。
3. **审计落库**：
   - 生产启用 `async.enabled=true`，`identity.audit` 由 worker 投递到审计库/SIEM；不要以 `SlogEmitter` 作为生产终点。
   - 若平台审计存储尚未实现，至少明确 ADR/风险例外，并配置日志采集与保留 ≥180 天。
4. **事务性**：所有高风险审计与业务写入继续使用 UnitOfWork + Outbox；OAuth 自动建号需要引入 UoW，避免“账号已建、审计丢失”。
5. **审计不可变**：审计表只追加，禁止业务账号具备 UPDATE/DELETE 权限；保留期按 AUD-07 执行。
6. **测试**：每个失败分支断言“恰好一条”审计事件；锁定只发一次；OAuth 建号有 account.created。

**验收标准**

- 审计事件目录、`KnownActions()`、`docs/audit/EVENTS.md` 三处一致，CI 门禁通过。
- 集成测试覆盖 MFA locked/disabled、OAuth 自动建号、恢复码使用、锁定去重。
- 生产审计事件可通过审计库/SIEM 查询，日志丢失不影响审计完整性。

**涉及变更**：app 各服务、`shared/audit/actions.go`、`docs/audit/EVENTS.md`、`identityruntime`、worker、平台审计存储。

---

### IDN-P2-03 令牌与 MFA 敏感响应缺少 no-store

**现状（证据）**

- `pkg/httpx.WriteJSON` 只设置 `Content-Type`：[envelope.go](../../../pkg/httpx/envelope.go#L32-L42)，没有 `Cache-Control`、`Pragma`、`Expires`。
- 登录/刷新响应包含 Access/Refresh Token；MFA setup 响应包含 TOTP 密钥与恢复码：[handler.go](../../../internal/identity/transport/rest/handler.go#L74-L75)、[handler.go](../../../internal/identity/transport/rest/handler.go#L132-L136)。
- 平台未统一注入安全响应头（仓库内搜索无 `Cache-Control: no-store` / `X-Content-Type-Options`）。

**影响**

- 浏览器缓存、企业代理、扩展或共享终端可能缓存包含令牌/密钥的响应，造成凭据泄露。
- 不符合通用认证端点安全基线。

**生产级整改建议**

1. 为所有认证路由统一加中间件：
   - `Cache-Control: no-store, no-cache, must-revalidate, private`
   - `Pragma: no-cache`
   - `Expires: 0`
   - `Vary: Authorization`
2. 全站安全头基线：`X-Content-Type-Options: nosniff`、`Referrer-Policy`、HTTPS 下的 HSTS；MFA setup 响应额外 `X-Frame-Options: DENY`（如经浏览器直接访问）。
3. 在 `pkg/httpx` 或 `transport/rest` 提供 `NoStore` 辅助函数，避免各 Handler 遗漏。
4. 测试：httptest 断言 login/refresh/mfa/setup 响应头；反向代理配置示例同步。

**验收标准**

- 所有令牌/MFA 响应包含 `no-store`；`/me` 等敏感 GET 也包含。
- 安全扫描/渗透测试无“敏感响应可缓存”项。

**涉及变更**：`pkg/httpx`、`transport/rest`、`transport/c`、platform middleware、部署配置、测试。

---

### IDN-P2-04 账号枚举时序侧信道

**现状（证据）**

- 账号不存在时在密码哈希之前直接返回：[auth_service.go](../../../internal/identity/app/auth_service.go#L290-L300)。
- 账号存在且密码错误时要执行 Argon2id（默认约 19 MiB、t=2）：[argon2.go](../../../pkg/password/argon2.go#L24-L33)、[auth_service.go](../../../internal/identity/app/auth_service.go#L336-L349)。
- 响应码与文案一致，但耗时差异明显；locked/disabled 也在密码校验前返回。

**影响**

- 远程攻击者可通过响应时间统计区分“账号存在/不存在/锁定/停用”，绕过统一文案的枚举防护。
- 对高价值账号的定向攻击可先确认账号存在。

**生产级整改建议**

1. **Dummy verify**：启动时预计算一个固定 Argon2id 哈希；账号不存在、无密码凭据时，仍对 dummy 哈希执行一次 `Verify`，使耗时与真实账号一致。
2. **状态分支时序对齐**：locked/disabled 分支也执行 dummy verify（或真实 verify 后再返回统一错误），避免状态枚举；注意不要改变锁定语义。
3. **统一错误路径**：所有失败分支最终返回同一个 `invalidCredentials()`；审计写入放在固定位置，避免早退导致时序差异。
4. **限流与告警**：按 IP + 账号摘要限流；对高延迟差异做监控（可选）。
5. **测试**：基准测试统计存在/不存在账号的响应时间分布，确保无数量级差异；安全评审记录残余风险。

**验收标准**

- 基准测试中“存在但密码错误”与“账号不存在”的 p50/p95 差异在可接受范围（例如 <20%，需结合网络噪声评估）。
- 代码中不存在“账号不存在直接 return”而无 dummy verify 的路径。

**涉及变更**：`app/auth_service.go`、`app/password.go`、测试、安全文档。

---

### IDN-P2-05 OAuth 刷 state、Argon2/SMS 资源耗尽

**现状（证据）**

- `OAuthService.Authorize` 没有速率限制，每次请求写一个 Redis state key（TTL 10m）：[oauth.go](../../../internal/identity/app/oauth.go#L137-L159)。
- `OAuthService.Callback` 对随机 state 会写 `oauth:state:used:<state>` 标记，同样可被刷：[oauth_state.go](../../../internal/identity/infrastructure/cache/oauth_state.go#L117-L145)。
- 登录密码校验使用 Argon2id，内存约 19 MiB/请求，没有全局并发信号量：[runtime.go](../../../cmd/internal/identityruntime/runtime.go#L79-L86)。
- SMS 发送只有单手机号日限与单 IP 小时限，没有全局日预算/供应商熔断。

**影响**

- 攻击者可刷爆 Redis 内存或 state 键空间；OAuth 授权端点可被用作低成本放大。
- 大量并发登录可耗尽 CPU/内存（Argon2 memory-hard），导致正常用户不可用。
- SMS 可被跨大量手机号/IP 刷量，产生真实费用。

**生产级整改建议**

1. **OAuth**：
   - `authorize` 与 `callback` 增加 IP/会话级限流、全局 QPS 上限；state 键设置单 IP 配额。
   - 对 provider 增加熔断与超时（已有 10s/1MB，补 circuit breaker）；记录 `oauth_state_created_total` 与 Redis 使用量。
   - state 存储使用独立 Redis DB/前缀与内存上限告警；必要时改用短期签名 state（无状态）+ nonce 表。
2. **Argon2 并发保护**：
   - 新增 `identity.password.max_concurrent`（默认 `2×CPU`），用带缓冲 channel 或 semaphore 控制；排队超时返回 429/503，避免排队雪崩。
   - 将密码校验放入独立 worker pool，与普通请求隔离；监控 `password_verify_inflight`、`password_verify_wait_seconds`。
   - 评估提高 Argon2 参数前先做容量测试。
3. **SMS 预算**：
   - 新增全局日发送预算与供应商账户余额告警；超预算拒绝发送并告警。
   - 供应商熔断、重试上限、发送失败回滚重发窗口。
   - 对可疑手机号/IP 增加图形验证码或人工审核（可选）。
4. **压测**：登录 100/500/1000 并发、OAuth authorize 1000 QPS、SMS 峰值；观察 p99、内存、Redis 键数量。

**验收标准**

- 登录并发达到上限时返回 429/503，进程内存稳定，不 OOM；正常用户 p99 可接受。
- Redis state 键数量有上限/告警，刷量不会打爆 Redis。
- SMS 全局预算生效，超额告警。

**涉及变更**：`app/oauth.go`、`infrastructure/cache/oauth_state.go`、`app/auth_service.go`/`password.go`、`identityruntime`、`pkg/ratelimit`、配置、压测脚本。

---

## 五、建议实施顺序

> 不包含工期估算，仅给出依赖顺序。

1. **止血（P0）**：IDN-P0-01（撤销陈旧）→ IDN-P0-03（锁定）→ IDN-P0-04（B 端 MFA 决策）→ IDN-P0-02（账号生命周期/C 开户）。
2. **会话与撤销一致性（P1）**：IDN-P1-02（session 状态校验）→ IDN-P1-01（family 模型 + 清理 + max_per_account）→ IDN-P1-07（绝对上限）。
3. **可观测与限流（P1）**：IDN-P1-03（日志/指标/告警）→ IDN-P1-04（可信代理 + Redis 限流）。
4. **MFA 与隐私硬化（P1）**：IDN-P1-05（TOTP 重放/恢复码/轮换）→ IDN-P1-06（HMAC 摘要）。
5. **配置与合规收口（P2）**：IDN-P2-01 → IDN-P2-02 → IDN-P2-03 → IDN-P2-04 → IDN-P2-05。
6. **回归门禁**：把新增用例接入 `make ci` / `make test-integration`；更新 `scripts/ci/check-authn.sh`，校验生产配置、审计 action、撤销路径与关键指标存在。

---

## 附录 A：建议新增/调整的配置项

| 配置 | 默认 | 生产约束 | 用途 |
|---|---|---|---|
| `identity.revocation.state_ttl` | `min(access_ttl+1m, 60s)` | `≤60s` | 账号撤销快照最大陈旧度 |
| `identity.session.absolute_ttl` | `720h`（30d） | `> refresh_ttl` | family 绝对生命上限 |
| `identity.session.max_per_account` | `10` | 必须实现后生效 | 单账号活跃设备上限 |
| `identity.retention.session_days` | `30` | `≥7` | rotated/revoked 令牌代保留期 |
| `identity.retention.login_attempt_days` | `180` | `≥180` | 登录尝试保留期 |
| `identity.password.min_length` | `12` | `[8,128]` | 密码策略 |
| `identity.password.max_length` | `128` | `≤4096 bytes` | 密码策略 |
| `identity.password.require_complexity` | `true` | 建议 true | 密码策略 |
| `identity.password.max_concurrent` | `2×CPU` | `≥1` | Argon2 并发保护 |
| `identity.hash_key` | 空 | 生产必填 32B | PII/验证码 HMAC |
| `identity.hash_key_id` | `v1` | 轮换用 | 摘要版本 |
| `identity.mfa.keys[]` | 空 | 建议启用 | MFA 密钥环 |
| `identity.mfa.active_key_id` | 空 | keys 非空时必填 | 当前加密密钥 |
| `http.trusted_proxies` | 空 | 生产按入口配置 | XFF 可信代理 |
| `identity.oauth.authorize_rate_limit` | 关闭 | 生产建议开启 | 防 state 刷量 |
| `identity.sms.daily_budget` | `0`（不限） | 生产建议设置 | 短信全局预算 |

## 附录 B：建议指标与告警

| 指标 | 类型 | 告警建议 |
|---|---|---|
| `identity_login_total{channel,result,reason}` | counter | 失败率突增 |
| `identity_login_duration_seconds` | histogram | p99 异常 |
| `identity_lockout_total{channel}` | counter | 突增（暴力破解/DoS） |
| `identity_mfa_verify_total{result}` | counter | 失败率突增 |
| `identity_mfa_recovery_used_total` | counter | 任何使用都通知账号所有者 |
| `identity_refresh_total{result}` | counter | 失败率突增 |
| `identity_token_reuse_total` | counter | 任何非零都告警 |
| `identity_session_revoke_total{reason}` | counter | 异常批量撤销 |
| `identity_revocation_store_errors_total{op}` | counter | 任何非零都告警 |
| `identity_revocation_cache_write_error_total` | counter | 任何非零都告警 |
| `identity_revocation_staleness_seconds` | gauge | 超过 60s 告警 |
| `identity_password_verify_inflight` | gauge | 接近并发上限 |
| `identity_password_verify_wait_seconds` | histogram | p99 排队过长 |
| `identity_sms_send_total{result}` | counter | 失败率/量突增 |
| `identity_oauth_callback_total{provider,result}` | counter | 失败率突增 |
| `identity_session_rows` / `identity_login_attempt_rows` | gauge | 增长异常，清理失败 |

## 附录 C：测试矩阵

| 场景 | 类型 | 断言 |
|---|---|---|
| 缓存预热后禁用账号 | 集成（MySQL+Redis） | 旧令牌立即/≤60s 拒绝 |
| 缓存写失败 | 故障注入 | 不出现“操作成功但旧令牌长期有效” |
| pending 账号连续失败 | 单测/集成 | 5 次后锁定 |
| 锁定过期后继续失败 | 单测 | 重新锁定 |
| 20 并发错误登录 | 并发/race | 无 500，最终锁定 |
| 管理员撤销非当前会话 | 集成 | 该会话 Access Token ≤60s 失效 |
| 刷新 100 次 | 集成 | 会话列表 1 个设备，行数受控 |
| max_per_account 淘汰 | 集成 | 最久未使用 family 被撤销并审计 |
| TOTP 同码二次使用 | 单测 | 第二次拒绝 |
| 并发恢复码消费 | 并发 | 每个码只成功一次 |
| 并发 disable/verify | 并发 | 最终绑定为删除 |
| 密钥轮换 | 集成 | 旧密文可解密并重加密 |
| 伪造 XFF / 新连接 | 集成 | 限流 key 不变 |
| 多副本限流 | 集成 | 总次数等于配置 |
| 生产 memory DB | 配置测试 | 启动失败 |
| 生产缺 hash_key | 配置测试 | 启动失败 |
| 审计失败分支 | 单测 | 恰好一条事件，action/outcome 正确 |
| 令牌响应头 | httptest | `Cache-Control: no-store` |
| 账号不存在时序 | 基准 | 与密码错误路径同量级 |
| OAuth state 刷量 | 压测 | Redis 键数量/内存受控 |
| 登录并发 500 | 压测 | 无 OOM，429/503 可预期 |

---

## 六、风险例外记录模板

若某项无法在首个生产版本修复，必须在发布评审中登记：

```text
缺陷 ID：
风险描述：
影响范围（端别/接口/数据）：
临时缓解（配置关闭/限流/WAF/人工流程）：
残留风险与接受人：
复查日期：
关联监控/告警：
```

---

**文档维护**：本文件随 `internal/identity` 与 `internal/platform/config.go` 变更同步更新；新增缺陷使用 `IDN-P<级别>-<序号>` 编号，关闭后在条目末尾标注“已修复（提交/版本）”，不要删除历史记录。
