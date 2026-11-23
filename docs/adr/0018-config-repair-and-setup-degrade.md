# ADR-0018：配置中心重配置态与启动降级（repair）

- 状态：Proposed（草案，待评审，2026-11-21）
- 日期：2026-11-21
- 关联：[ADR-0013](0013-config-center-in-process-snapshot.md)、[ADR-0016](0016-config-service-pull-model.md)、[ADR-0017](0017-config-center-install-state.md)（本 ADR 修订其「dev 自动播种」）、[`../CONFIG_STANDARDS.md`](../CONFIG_STANDARDS.md) §4、[`../config-center-plan.md`](../config-center-plan.md) D16

## 背景

ADR-0017 把 prod-like 的首次配置收敛成「安装态 + setup token」，但仍然只覆盖「空库首装」一条路径：

- 向导只接受一段 `Overrides` JSON（`frontend/apps/web-admin/src/views/_core/install/index.vue`），不是「所有配置项可配置」；
- 安装态一旦 `ready`，`install.Begin` 直接返回 `40952`（`CodeInstallCompleted`），向导关闭；
- 新业务代码可能引入 `Required` 键。`domain.KeySpec.Validate` 强制 `Required` 与 `Default` 互斥，所以这类键没有默认值；`EnsureBaseline` 把「敏感或空默认值」的键记进 `SkippedKeys` 后照常建指针，不报错（`internal/configcenter/app/bootstrap.go`）。快照缺键 → 业务进程在配置校验阶段失败退出 → admin-api 同时退出 → 浏览器打不开向导 → **死锁**；
- 反之，`Required` 键带默认值时由 `reconcileMissingDefaults` 自动补进快照，不阻塞启动，此时应该走管理面审批而不是安装向导；
- `spec.Registry.ValidateValues` 有完整校验能力，但非测试代码里没有调用者；启动期真正把关的是 `pkg/config` 的 schema 校验，失败形态是 `config: invalid config: config validation failed: ...`，进程无法区分「缺键」与「值非法」。
- ADR-0017 之后代码里长期存在**两套播种逻辑**：local/dev/test 走后端自动播种并直接 `MarkReady`，staging/prod 走安装向导。两套路径的差异只在 `cmd/internal/configcenterruntime/runtime.go` 的一个 `if IsProdLikeEnv` 分支，等于同一件事写两遍、测两遍；dev 也就永远测不到向导与 repair 链路。

因此需要把「安装」扩成「安装 + 重配置（repair）」，把「缺键阻塞启动」与「错误配置阻塞启动」分开处置，并让所有环境共用同一条安装路径。

## 决策

1. **阻塞判定只看缺键**：`blocking := KeySpec.Required && (快照缺失 || 值为空)`。值非法（键存在但校验不过）不计入阻塞，进程继续 crash-loop，不降级——避免静默带病运行。
2. **进程自动降级，且仅 admin-api**：`configboot.Load` 在 Refresh 之后用注册表计算 `Gaps`；非空时 admin-api 进入 setup-only 模式，只挂 `/healthz`、`/readyz`（503 + 缺失键清单）、`/version` 与 `/admin/v1/install/*` 代理，不装配业务依赖、不注册业务路由；biz-api / c-api / worker 仍快速失败退出（无人值守进程降级只会掩盖故障）。
3. **向导写操作始终要 setup token**：降级不额外放宽鉴权，`X-Config-Install-Token` 继续由 `app.RequireSetupToken` 常量时间比较；token 不入库、不回显。
4. **安装态新增 repair 入口**：`ready` 之后允许 `begin(reason=repair)`，前置条件是 `Gaps` 非空；迁移 `ready → installing(mode=repair)`，`complete` 后回 `ready`。`Gaps` 为空时仍返回 `40952`，防止把向导当成日常配置渠道。
5. **repair 只能改阻塞键**：`begin` 时把阻塞键集合冻结进 `config_install_state.repair_keys`；`apply` 按白名单逐键校验，白名单外的键返回 `40000` 并指明键名。非阻塞键的变更仍只能走管理面 `submit → approve → publish`（双人审批 + 审计）。
6. **首次安装全量可配置**：新增目录 API，返回 6 个 namespace（platform / iam / identity / audit / tenant / notification）的 `KeySpec` 元数据——类型、默认值、是否必填、枚举、区间、正则、说明、是否敏感、是否热更、是否弃用；向导按目录渲染分域表单，默认值预填，留空即默认。服务端仍用 `Registry.ValidateValues` 做整体校验，不信任前端。
7. **敏感项自动生成 + 可选覆盖**：`Sensitive` 键默认按 `SecretRule` 自动生成并写入信封加密存储；允许向导填写覆盖值（同样加密入库）；任何接口都不回显明文或密文。
8. **补齐写入保持不可变**：repair 为阻塞键生成新的 item version 与新的 release（`reason=install-repair`），再以指针 CAS 切换；不原地修改历史版本，回滚语义与普通发布一致。
9. **非阻塞变更不进向导**：带默认值的新键由 `reconcileMissingDefaults` 自动入快照；运行期改值走 `/admin/v1/configs/*`，不需要 setup token，也不受安装态限制。
10. **所有环境共用一条安装路径**：删除「dev 自动播种并直接 ready」分支，`NewConfigAPI` 一律只 `EnsureState(pending)`，由安装向导（`begin → apply → complete`）完成播种；`RequireSetupTokenConfigured` 对所有环境都要求 `CONFIG_INSTALL_SETUP_TOKEN`，缺失即启动失败。dev/test 的便利性由本地脚本提供（账号内固定 token + 启动时自动驱动向导），而不是由后端保留第二条代码路径。`ready` 之后每次启动仍做幂等 `bootstrap`/`bootstrapSecrets`（补齐新增键），与旧 dev 行为一致。

## 状态机

```
                          ┌────────── first_install ──────────┐
  pending ──begin(first)──▶ installing ──apply(全量)──▶ installing ──complete──▶ ready
     ▲                         │ fail                                  ▲            │
     │                         ▼                                       │            │ begin(repair)
     └───────────────────── failed ─────────── begin(repair) ──────────┘            │
                                                                                   ▼
                                                                     installing(mode=repair)
                                                                                   │ apply(仅阻塞键)
                                                                                   ▼
                                                                                 ready
```

| 当前态 | 动作 | 前置 | 结果 | 失败码 |
| --- | --- | --- | --- | --- |
| `pending` / `failed` | `begin(first_install)` | 无 | `installing(mode=first_install)` | `40951` 租约被他人持有 |
| `ready` | `begin(first_install)` | — | 拒绝 | `40952` |
| `ready` | `begin(repair)` | `Gaps` 非空 | `installing(mode=repair)`，冻结 `repair_keys` | `40952`（无阻塞键）/ `40951` |
| `installing` | `apply` | owner 与租约有效 | 续租约；首次安装写全量条目，repair 只写白名单 | `40951` owner 不匹配 |
| `installing` | `complete` | owner 与租约有效，无阻塞键 | `ready` | `40000` 仍缺键 |
| `installing` | 租约过期 | — | 允许他人 `begin` 接管，`failApply` 记 `last_error` | — |

repair 的 `complete` 前置与首装一致：`Gaps` 必须为空。也就是说「冻结的阻塞键」全部补齐才算修复完成。

## 接口契约

```text
GET  /internal/v1/install/status
→ {env, status, mode, owner, version, lease_until, completed_at, last_error,
   repair_keys:[namespace/key], blocking_count,
   gaps:[{namespace, key, reason}]}                # reason ∈ no_active_release | missing_in_snapshot

GET  /internal/v1/install/schema                    # P1 已落地，见下方契约
→ 向导渲染元数据（字段清单 + 分组 + 默认值来源 + 阻塞键）
   · 只出声明信息，不出任何已入库值；敏感项只出生成规则

POST /internal/v1/install/begin     {owner, reason:"first_install"|"repair"}
POST /internal/v1/install/apply     {owner, values:{namespace:{key:value}}, overrides?}
POST /internal/v1/install/complete  {owner}
```

- 请求头：`X-Config-Install-Token`；`apply` 的 `values` 由服务端按当前 `mode` 决定接受范围：`first_install` 接受任意已声明键，`repair` 只接受冻结白名单内的键。`overrides` 是旧的扁平键值形态，仍兼容（与 `values` 合并，`values` 优先）。
- `apply` 响应带 `remaining_gaps`：前端不必再轮询 status 就能判断「还差几个键」；为空才允许 `complete`。
- admin-api 继续同源代理 `/admin/v1/install/*`（ADR-0017 决策 5 不变），浏览器不直连 config-api。
- **审计不新增事件名**：repair 复用 `config.install.{begin,apply,complete,fail}`，在 metadata 里带 `mode`、`repair_keys`、`remaining`。理由：action 名在三处登记（`internal/shared/audit` 的 allowlist、`docs/audit/EVENTS.md`、`scripts/ci/check-configcenter.sh`），新增三个 `*.repair.*` 事件会让三处同步漂移，而「同一动作的两种模式」用字段表达已经足以筛选（`mode=repair`）。

### 向导 schema 契约（P1 已落地，2026-11-23）

```text
GET /internal/v1/install/schema        头：X-Config-Install-Token（必需）
→ {env, mode, status, blocking_count,
   blocking_keys:[{namespace, key, label, reason}],          // reason ∈ no_active_release | missing_in_snapshot
   namespaces:[{namespace, title, order, key_count, blocking_count}],
   fields:[{namespace, key, label, group, group_title, order, widget, value_type,
            required, sensitive, deprecated, blocking, reload_mode, hot_whitelist,
            enum?, min_int?, max_int?, pattern?, description, owner,
            default:{value, source},                          // source ∈ code_default | prod_default | none
            secret?:{algorithm, auto_generate, encoding, min_len, max_len, prefix?}}]}
```

- `mode`：`ready` 之前为 `first_install`，之后为 `repair`；`status` 同步返回 `mode`，前端据此决定「全量表单」还是「只渲染阻塞键」。
- `blocking_keys` 只统计**非敏感**必填键：敏感键的取值走 `config_secret`（自动生成 + 授权），不会进入配置快照，计入会把每个环境都判成永久缺键。
- `default.source` 解决了「无默认值」与「默认值为空串」不可分的问题：`none` 表示没有默认值，前端不显示「默认：」徽标。
- `group` / `group_title` 由键前缀派生（`identity.sms`、`notification.channels` 等二级分组），`order` 是「命名空间顺序 + 分组顺序 + 组内序号」压成的排序键，前端直接按 `order` 升序渲染即可，无需硬编码分组表。
- `widget` 由声明推导（`password` / `select` / `switch` / `number` / `duration` / `code` / `text`），前端不维护键名到控件的映射。
- 本端点要求 setup token：schema 暴露键名、默认值与校验规则，虽不含密文也不应匿名可读。

### repair 契约（P2 已落地，2026-11-23）

```text
POST /internal/v1/install/begin   {owner, reason}
  · reason 省略 → 按状态推断：ready 走 repair，其余 first_install
  · reason 非法 → 40000 reason must be "first_install" or "repair"
  · first_install 遇 ready → 40952 install already completed
  · repair 遇非 ready → 40000 repair requires install status ready
  · repair 且 gaps 为空 → 40952 no blocking keys to repair（防止把向导当日常配置渠道）
  → state.repair_keys = ["platform/http.metrics_url", ...]（begin 时冻结）

POST /internal/v1/install/apply   {owner, values:{ns:{key:value}}}
  · repair：白名单外的键 → 40000
    "repair only accepts frozen blocking keys; rejected: platform/log.level"
  · 响应 data.remaining_gaps 为 apply 之后仍未满足的必填键

POST /internal/v1/install/complete {owner}
  · 仍有阻塞键 → 40000 "install incomplete; blocking keys remain: ..."
    且**不改状态**（保持 installing 与租约，可继续 apply）
```

- 白名单比对使用 `namespace/key` 形式（`RequiredGap.KeyRef()`）；键到 namespace 的解析走注册表 `Lookup`，不依赖前端传分组。
- repair 生成的新 release 固定 `reason=install-repair`、描述「安装向导 repair 补齐阻塞启动的必填键」，指针仍走 CAS，历史版本不原地修改。
- `mode` 在状态行上持久化，重启后 repair 轮次仍可续（租约 15 分钟内）；`ready` 之后 DTO 的 `mode` 统一展示为 `repair`（表示「再改就是重配置」），与 schema 的 mode 判定一致。

## 进程侧

原方案是给 `platform.Options` 加 `SetupOnly` 走 `platform.Run`；P3 落地时改为**独立的 `platform.RunSetupOnly`**。原因：`platform.Run` 的 `prepareInfra` 强依赖 `opts.Config`，而缺键正是「配置装配失败」，要么伪造一份假配置、要么改坏正常装配顺序；独立入口只复用了 `httpserver.ListenAndServe` 与探针，取舍更干净。

- `cmd/internal/configboot/gaps.go`：`MissingRequired(values)` 从注册表算必填缺值（`Required && !Sensitive`，敏感键由 `secretclient` 解析、不进快照，计入会把环境判成永久缺键）；`RequireComplete(service, values)` 给 fail-fast 进程用。
- **必须在进程级补齐之后判定**：`admin-api` 在 `LoadFromValues` 的闭包末尾（`WithProcessIdentity` / `SetIfEmpty` / `ApplyProdLikeProcessDefaults` / `ApplyDeployOverlays` 全部执行完）才调 `MissingRequired`，否则把代码能补的 `auth.jwt.issuer` 误判成缺键。
- `internal/platform/setup_only.go`：`RunSetupOnly(ctx, SetupOnlyOptions)`；只挂 `/healthz`、`/readyz`、`/version` 与调用方 `Mount` 的安装代理（**不挂 `/metrics`、不装配数据库/鉴权/限流/业务路由**）；`/readyz` 固定 503，`checks[config_install].error` 形如 `config_incomplete: missing required keys: platform/auth.jwt.issuer; run admin install wizard`；首行日志 `SETUP-ONLY MODE: missing required keys, install wizard required`（含 `service/channel/env/addr/missing_keys`）。
- `cmd/admin-api/setup_only.go`：`runSetupOnly` 以 `ADMIN_API_ADDR`（缺省 `:8081`）起降级态，挂 `configboot.NewInstallProxy(boot.ConfigAPIURL, boot.HTTPClient)` 的 5 条向导路由（含 `/install/schema`），后台每 5s 查安装态。
- **退出策略（结案原待定 2）**：`ready` 后默认自动退出（退出码 0，编排重启为正常进程）；`ADMIN_FRAME_SETUP_ONLY_AUTO_EXIT=false` 时只打日志继续服务，留给无编排环境手工重启。
- 其他进程（biz/c/worker）：在各自 `LoadFromValues` 闭包里调 `configboot.RequireComplete`，缺键时返回 `biz-api: config incomplete: missing required keys: ...; run the admin install wizard to seed them`，保持 crash-loop——它们无法自行修复，降级只会把故障面扩大。
- 过渡期保留 `InstallPending` 兜底（安装态非 `ready` 且 config-api 可达时，仍用 key spec 默认值让进程监听）：两种情形语义不同，安装未开始不该降级成「缺键」。

**当前覆盖面（P3 实测）**：全仓 `Required` 键只有 `platform/auth.jwt.issuer`、`platform/auth.jwt.audience` 两个，而 `admin-api` 会用 `SetIfEmpty` 补齐这两项——因此降级态今天是一条**安全网**（未来新增必填键时才触发），实际让 prod 起不来的多半是 `secretclient`（JWT 密钥、KEK）与 mTLS 证书这类「非快照」故障，它们不在 `Gaps` 口径内。扩大阻塞判定需要先把「启动校验的缺值类错误」纳入进来，并让 repair 接受运维声明的白名单——记作待定 5。

## 代码改造清单

| 层 | 文件 | 改动 |
| --- | --- | --- |
| spec | `internal/configcenter/spec/meta.go` | `GroupOf`（分组与顺序）、`NamespaceTitle`、`DeriveLabel`、`WidgetFor`（P1 已落地） |
| app | `internal/configcenter/app/bootstrap.go` | `MissingRequired(env)`（阻塞键统计，排除敏感键；P1 已落地）；`BootstrapInput.Repair` 与 `install-repair` release 名（P2 已落地） |
| app | `internal/configcenter/app/install_schema.go` | `InstallService.Schema(env)`：字段清单 + 默认值来源 + 阻塞键（P1 已落地） |
| app | `internal/configcenter/app/install.go` | `Gaps`（P1'）、`Begin(reason)` 的 repair 分支、`Apply` 白名单校验、`complete` 阻塞键门禁（P2 已落地）；审计复用既有 4 个 action + `mode` 字段 |
| 持久化 | `migrations/000038_*` + repository | `config_install_state` 增 `mode`、`repair_keys`（expand-only，可空默认 `first_install`；000037 已被 ADR-0017 占用；P2 已落地） |
| transport | `internal/configcenter/transport/rest/{internal_handler,install_handler}.go` | `/install/schema`（P1）；`status` 增 `mode`（P1）/`gaps`+`blocking_count`（P2）；`begin.reason`、`apply.values`/`overrides` 合并、`remaining_gaps`、`repair_keys`（P2） |
| 进程 | `cmd/internal/configboot/gaps.go` | `MissingRequired`（必填缺值，排除敏感键）、`RequireComplete`（fail-fast 进程的错误文案；P3 已落地） |
| 平台 | `internal/platform/setup_only.go` | `RunSetupOnly` + `SetupOnlyAutoExitEnv`：探针/版本/安装代理，`/readyz` 固定 503 并带缺失键（P3 已落地） |
| 进程 | `cmd/admin-api/setup_only.go` | 降级态装配与 `ready` 后自动退出（P3 已落地） |
| 前端 | `frontend/apps/web-admin/src/views/_core/install/index.vue`、路由表 | 目录驱动表单（分域、默认值徽标、必填/敏感标记）、repair 只显阻塞键、`/setup/install`免登录白名单 |
| 契约 | `api/openapi/paths/{internal,admin}/install.yaml`、`schemas/internal/install.yaml` | schema 端点与响应契约（P1 已落地）；gaps 契约随 P2 |
| 门禁 | `scripts/ci/check-configcenter.sh` | 断言每个 `Required` 键可经向导填写（非敏感或带 `SecretRule`）；install 端点与状态机契约 |
| 装配 | `cmd/internal/configcenterruntime/runtime.go` | 删除 `IsProdLikeEnv` 分支：一律 `EnsureState(pending)`，仅 `ready` 时幂等 `bootstrap`/`bootstrapSecrets`；删除 `MarkReady` |
| 装配 | `internal/configcenter/app/install.go` | `RequireSetupTokenConfigured` 对所有环境强制；删除 `MarkReady` |
| 本地编排 | `scripts/local/{dev-services.sh,dev-services.ps1,env.example}`、Makefile | dev 固定 token；`start` 在 config-api 就绪后自动驱动向导；新增 `install` 子命令与 `make local-install` |

## 实施状态（2026-11-21）

| 阶段 | 内容 | 状态 |
| --- | --- | --- |
| P0' | 统一安装路径：删 `IsProdLikeEnv` 分支与 `MarkReady`；所有环境强制 setup token；本地脚本自动驱动向导（`install` 子命令 + `make local-install`） | 已完成 |
| P0'' | 现状契约补齐：admin 安装代理四端点登记 OpenAPI（`admin.yaml` 9.3.0，`InstallState` 上提 `components.yaml` 共享）+ `check-configcenter` 断言 | 已完成 |
| P1 | `MissingRequired`（`app/bootstrap.go`）+ `spec/meta.go` 派生元数据 + `InstallService.Schema` + `GET /install/schema`（internal 与 admin 代理）+ `status.mode` + OpenAPI 登记 + 单测 | 已完成 |
| P1' | `status.gaps`（与 P2 的 repair 白名单同源，避免两处统计口径漂移） | 已完成 |
| P2 | `Begin(repair)`、`Apply` 白名单、`RepairKeys` 写入；迁移 000038；`remaining_gaps`；`complete` 阻塞键门禁 | 已完成 |
| P3 | 进程侧降级：`configboot.MissingRequired`/`RequireComplete`、`platform.RunSetupOnly`、`admin-api` 降级 + `ready` 后自动退出、biz/c/worker fail-fast；单测覆盖探针/挂载面/自动退出 | 已完成 |
| P4 | 前端 schema 驱动表单（分域、默认值徽标、必填/敏感标记）；`mode=repair` 时只渲染阻塞键 | 待做 |

## 安全与失效

- **攻击面**：setup-only 只暴露 `/healthz`、`/readyz`、`/version` 与 `/admin/v1/install/*`（5 条向导代理），业务路由与 `/metrics` 一律 404，等同把进程收窄成安装器；仍需 TLS（prod 走 mTLS/ingress），并对 `begin/apply/complete` 按 IP 限流。
- **一致性**：`apply` 在单事务内完成「新版本 + 新 release + 指针 CAS」；指针被其他发布抢占时返回冲突，向导需刷新后重试。
- **可观测**：降级态**不挂 `/metrics`**（没有可信鉴权与限流，宁可少一个面），信号只有三条——`/readyz` 持续 503（`checks[config_install].error` 带缺失键）、首行日志 `SETUP-ONLY MODE: missing required keys ...`（便于 grep 与日志告警）、`/version` 可确认版本。告警建议按「admin-api `/readyz` 503 持续 10m」配置；`config_install_blocked{service}` 指标留到降级态有独立 metrics 出口后再补。
- **误降级防护**：`Gaps` 只由注册表声明计算；未声明键导致的失败仍走 crash-loop，杜绝「代码 bug 被降级掩盖」。

## 后果

**正面**：新键阻塞启动时不再死锁，运维用浏览器即可自救；首装可全量确认默认值；非阻塞变更仍受审批与审计约束。

**负面**：多一条状态机分支与一次迁移；admin-api 存在「降级态」这一非常规运行形态，需要告警与 runbook 配合；repair 语义要求键声明必须准确（`Required` 用错会放大阻塞面）。统一安装路径后，**所有环境**首次启动都多一步向导（本地脚本已自动化），且 `CONFIG_INSTALL_SETUP_TOKEN` 成为全环境必填变量（prod 由编排注入，严禁默认值）。

**守卫**：`check-migration-gate`（expand-only）、`check-configcenter`、安装态单测（`gaps`/`repair` 白名单/状态迁移）、集成用例「缺键 → 降级 → 修复 → 重启变绿」。

## 待定

1. **「值非法」死锁的自救通道**：按决策 1 不降级，则「键在、值错」时向导也进不去。是否增加一个显式逃生门——例如运维在进程侧注入 `CONFIGCENTER_REPAIR_FORCE=<token 指纹>` 后，允许把「存在但非法」的键纳入 repair 白名单（仍要 token、仍留审计）？
2. ~~自动退出还是手动重启~~：已结案（P3）——默认 `ready` 后自动退出并由编排重启，`ADMIN_FRAME_SETUP_ONLY_AUTO_EXIT=false` 可退回手动重启。
3. ~~schema 的可见范围~~：已定案——`/install/schema` 要求 setup token（键名、默认值与校验规则虽不含密文，也不应匿名可读）；若后续要支持「只读预览不填 token」再单独评估。
4. **repair 白名单与运行期改值的边界**：当前 repair 只覆盖「缺键」，运维想顺手改一个非阻塞键仍需退出向导走管理面。是否给 `apply` 一个 `also_update` 列表（仍需 token + 审计 + 双人审批中的至少一项）留作后续评估。
5. **阻塞判定是否扩到「校验层缺值」**：现在 `Gaps` 只认 `Required` 键，而全仓只有 issuer/audience 两个、admin-api 还会代码补齐 → 降级是安全网而非日常路径。若要覆盖「`auth.jwt.secret` 缺失导致 crash-loop」这类真实故障，需要（a）把启动校验的缺值类错误纳入阻塞判定，（b）让 `begin(repair)` 接受运维声明的白名单（服务端校验键存在），否则 `Gaps` 为空会直接返回 `40952` 形成新死锁。改动跨 transport 契约与白名单冻结逻辑，单独评估。

## 同步文档

- [`../CONFIG_STANDARDS.md`](../CONFIG_STANDARDS.md) §4：补「缺键阻塞 → setup-only → repair」链路；CFG-09 改为全环境安装向导。
- [`../runbooks/config-center.md`](../runbooks/config-center.md)：补 repair 值班流程与告警处置。
- [`../local-development.md`](../local-development.md)：dev 启动流程补安装向导步骤与 `CONFIG_INSTALL_SETUP_TOKEN`。
- [`../config-center-plan.md`](../config-center-plan.md)：增补 D17（repair 与降级），D16 标注被 ADR-0018 部分修订。
