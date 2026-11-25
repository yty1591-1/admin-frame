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

1. **阻塞判定 = 缺键 ∪ 条件缺键 ∪ 条件缺密 ∪ 连通性失败**：`MissingRequired`（含 storage/SMTP 条件 Gaps）+ 条件敏感键缺失（`secret_missing`）+ `RunInfraProbes`（`connectivity_failed` / `deploy_env_missing`）。值非法（键存在但校验不过）仍不计入阻塞，进程继续 crash-loop——避免静默带病运行。config-api 在 `ready` 启动探测失败时 CAS `ready → failed`，`begin(repair)` 允许从 `ready|failed` 进入。
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
| `ready` / `failed` | `begin(repair)` | Gaps 非空（含连通性） | `installing(mode=repair)`，冻结 `repair_keys` | `40952`（无阻塞键）/ `40951` |
| `installing` | `apply` | owner 与租约有效 | 续租约；首次安装写全量条目，repair 只写白名单 | `40951` owner 不匹配 |
| `installing` | `complete` | owner 与租约有效，无阻塞键 | `ready` | `40000` 仍缺键 |
| `installing` | 租约过期 | — | 允许他人 `begin` 接管，`failApply` 记 `last_error` | — |

repair 的 `complete` 前置与首装一致：`Gaps` 必须为空。也就是说「冻结的阻塞键」全部补齐才算修复完成。

## 接口契约

```text
GET  /internal/v1/install/status
→ {env, status, mode, owner, version, lease_until, completed_at, last_error,
   repair_keys:[namespace/key], blocking_count,
   gaps:[{namespace, key, reason}]}                # reason ∈ no_active_release | missing_in_snapshot | connectivity_failed | deploy_env_missing | secret_missing

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
   blocking_keys:[{namespace, key, label, reason}],          // reason 同 status.gaps
   namespaces:[{namespace, title, order, key_count, blocking_count}],
   fields:[{namespace, key, label, group, group_title, order, widget, value_type,
            required, sensitive, deprecated, blocking, reload_mode, hot_whitelist,
            enum?, min_int?, max_int?, pattern?, description, owner,
            default:{value, source},                          // source ∈ code_default | prod_default | none
            secret?:{algorithm, auto_generate, encoding, min_len, max_len, prefix?}}]}
```

- `mode`：`ready` 之前为 `first_install`，之后为 `repair`；`status` 同步返回 `mode`，前端据此决定「全量表单」还是「只渲染阻塞键」。
- `blocking_keys` 只统计**非敏感**必填键：敏感键的取值走 `config_secret`（自动生成 + 授权），不会进入配置快照，计入会把每个环境都判成永久缺键。
- 阻塞键在 schema 中**一律标记 `required`**，即使 `KeySpec.Required=false`（`storage.s3.bucket` 这类条件必填由进程侧 `configboot.MissingRequired` 补判）：否则首装前端不拦截空值，会出现「装完 → admin-api 重启 → 又降级」的循环。
- `default.source` 解决了「无默认值」与「默认值为空串」不可分的问题：`none` 表示没有默认值，前端不显示「默认：」徽标。
- `group` / `group_title` 由键前缀派生（`identity.sms`、`notification.channels` 等二级分组），`order` 是「命名空间顺序 + 分组顺序 + 组内序号」压成的排序键，前端直接按 `order` 升序渲染即可，无需硬编码分组表。
- `widget` 由声明推导（`password` / `select` / `switch` / `number` / `duration` / `code` / `text`），前端不维护键名到控件的映射。
- 本端点要求 setup token：schema 暴露键名、默认值与校验规则，虽不含密文也不应匿名可读。

### repair 契约（P2 已落地，2026-11-23）

```text
POST /internal/v1/install/begin   {owner, reason, declared_keys?}
  · reason 省略 → 按状态推断：ready 走 repair，其余 first_install
  · reason 非法 → 40000 reason must be "first_install" or "repair"
  · first_install 遇 ready → 40952 install already completed
  · repair 遇非 ready → 40000 repair requires install status ready
  · declared_keys 与 first_install 同交 → 40000
    "declared_keys is only valid with reason \"repair\""
  · repair 且 gaps 为空且无 declared_keys → 40952 no blocking keys to repair
    （防止把向导当日常配置渠道）
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
- `declared_keys` 的三条拒绝规则（`InstallService.normalizeDeclaredKeys`）：键未在注册表声明、键是敏感项（敏感值由向导自动生成）、键在生效快照里**已有值**（属正常变更，应走 `submit → approve → publish`）。上限 64 个，去重后排序冻结。
- 被声明的键与注册表必填键同源计数：`apply`/`complete` 都用 `MissingRefs(RepairKeys)` 复核，因此「声明了却没填」不会在 `complete` 时漏过。
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

**当前覆盖面（P3 实测 + P5 扩展）**：全仓 `Required` 键只有 `platform/auth.jwt.issuer`、`platform/auth.jwt.audience` 两个，而 `admin-api` 会用 `SetIfEmpty` 补齐这两项——因此降级态按「注册表必填键」计算时是**安全网**（未来新增必填键时才触发），实际让 prod 起不来的多半是 `secretclient`（JWT 密钥、KEK）与 mTLS 证书这类「非快照」故障。为覆盖这类故障，P5 把阻塞判定扩成两条腿（见下方「声明白名单与缺值诊断」）：`begin(declared_keys)` 让运维登记服务端算不出的键，`configboot.DiagnoseMissingValues` 用占位法区分「缺值」与「值非法」。

## 声明白名单与缺值诊断（P5 已落地，2026-11-23）

待定 5 的解法是**两条腿同时上**，缺一条都会形成新死锁：

1. **声明白名单**（服务端）：`begin(reason=repair, declared_keys=[...])` 让运维把「服务端算不出、但进程确实起不来」的键登记进白名单。三条拒绝规则保证它不能被当成后门：键必须在注册表声明、必须非敏感、必须尚未在生效快照里有值（已有值的属正常变更，走审批流）。上限 64。冻结后 `apply`/`complete` 都用 `MissingRefs(RepairKeys)` 复核，`remaining_gaps` 会把「声明了却没填」如实报出来。
2. **缺值诊断**（进程侧）：`configboot.DiagnoseMissingValues(values, validate)` 在 `LoadFromValues` 失败后，给注册表里**未声明值且非敏感**的键填占位值（Enum 首项 / `max(MinInt,1)` / `true` / `1s` / `{}` / `placeholder`），再跑一次同一套校验：
   - 通过 → 判定「只是缺值」，进入 setup-only；
   - 仍失败 → 判定「值非法」，不降级，继续 crash-loop（决策 3）。

   方向是**保守**的：占位值本身不合法时宁可让进程继续崩，也不把类型/枚举错误吞成「缺值」放进向导——否则用户在向导里填对三个键、重启后照样起不来，故障更难定位。

因此「`auth.jwt.secret` 缺失导致 crash-loop」这类故障现在有两条自救路径：进程侧诊断出缺值 → 自动降级 → 向导打开（不需要知道键名）；或诊断判不出来（值在但错）→ 运维用 `declared_keys` 登记该键 → repair 修值。两者的审计都落在既有 `config.install.*` 事件上，带 `mode`/`declared_keys`/`remaining` 字段。

## 启动自愈：取值非法重置为默认值（P6 已落地，2026-11-23）

决策 3 规定「值非法不降级」，于是「键存在但取值写错」会让向导也进不去。P6 的解法不是开放写入，而是**把值退回声明默认值**：

1. **诊断**（进程侧）：`configboot.DiagnoseInvalidValues(values, validate)` 只在 `LoadFromValues` 失败后调用，两阶段定位——先按错误文案里点名的键（如 `db.driver`）精确匹配；匹配不到再逐个单键试默认值。两阶段都判不出就返回 `false`，继续 crash-loop，**不做批量重置**：宁可让人来处理，也不顺手改掉一批无关键。
2. **重置**（服务端）：`POST /internal/v1/configs/repair/reset-defaults`，body `{env?, refs:[namespace/key]}`，上限 32 键；与读路径同一套服务身份（mTLS SAN / `X-Service-Identity` + namespace allowlist）。取值由 config-api 解析（prod-like overlay > 代码默认值），**调用方不能指定值**；敏感键、无默认值、未声明、已归档键写入 `skipped` 并回原因，不静默吞掉。
3. **生效**：每键追加 item 版本 → 生成 `install-reset-defaults` 发布 → 旧发布 superseded → 指针 CAS；响应回 `release_keys`。admin-api 重置成功后重新 `configboot.Load` 并重建配置，成功则继续正常启动，失败则保留 fail-fast（不掩盖真正的 bug）。
4. **审计**：复用 `config.release.publish`，`mode=reset-defaults`，`attrs` 带 `env/namespace/release_id/release_no/keys/key_count`——action 白名单不加新名字，避免三处登记表漂移。

边界：只重置「注册表声明过、有默认值、非敏感」的键；入口只在启动失败路径上触发，管理面不暴露。运维仍可在 `refs` 里点名，但值不可控。

## 资源装配失败降级（2026-11-24 补充）

决策 3 原为「值非法不降级」，但 `OpenStorageSet` 这类**资源装配**失败发生在配置校验通过之后：典型形态是 prod-like 播种 `storage.driver=s3`、而bucket/region 缺失，错误 `s3 requires bucket and region` 由 `platformstorage.OpenStorageSet` 返回，`configboot.DiagnoseInvalidValues` 看不到它，于是 admin-api 与其他进程一起退出，安装向导再次不可达。

处置：

1. **凭据与连接配置分开**：`storage.driver` / `storage.s3.{bucket,region,endpoint,force_path_style}` 登记为普通 KeySpec（可由向导填写或管理面审批）；`storage.s3.{access_key,secret_key}` 登记为敏感键，只进 `config_secret`。这同时修订了 `CONFIG_STANDARDS.md` §3.1.1 的排除表。
2. **条件必填**：`bucket` / `region` 无默认值，仅当 `storage.driver=s3|minio` 时要求有值——`KeySpec.Required` 表达不了跨键依赖，改由 `configboot.MissingRequired` 的 `missingStorageRefs` 在进程侧补判，因此仍走既有「缺值 → setup-only」链路。
3. **可识别错误**：`internal/platform` 导出 `ErrStorageUnavailable`，`prepareInfra` 在 `OpenStorageSet` 失败时包装返回。
4. **仅 admin-api 降级**：捕获 `errors.Is(err, ErrStorageUnavailable)` 后转 `runSetupOnly`，由运维经 `/setup/install` 重配存储并重启；`worker` / `biz-api` / `c-api` 保持 fail-fast（无人值守进程降级只会掩盖故障，与决策 2 一致）。

边界：该降级只覆盖「存储资源打不开」，不覆盖数据库、Redis 等其他基础设施故障——那些仍应 fail-fast，避免把「连不上核心依赖」误判成「配置待补」。

## 代码改造清单

| 层 | 文件 | 改动 |
| --- | --- | --- |
| spec | `internal/configcenter/spec/meta.go` | `GroupOf`（分组与顺序）、`NamespaceTitle`、`DeriveLabel`、`WidgetFor`（P1 已落地） |
| app | `internal/configcenter/app/bootstrap.go` | `MissingRequired(env)`（阻塞键统计，排除敏感键；P1 已落地）；`BootstrapInput.Repair` 与 `install-repair` release 名（P2 已落地） |
| app | `internal/configcenter/app/install_schema.go` | `InstallService.Schema(env)`：字段清单 + 默认值来源 + 阻塞键（P1 已落地） |
| app | `internal/configcenter/app/install.go` | `Gaps`（P1'）、`Begin(reason)` 的 repair 分支、`Apply` 白名单校验、`complete` 阻塞键门禁（P2 已落地）；`normalizeDeclaredKeys` / `MissingRefs` 合并 / `mergeGaps`（P5 已落地）；审计复用既有 4 个 action + `mode` 字段 |
| 持久化 | `migrations/000038_*` + repository | `config_install_state` 增 `mode`、`repair_keys`（expand-only，可空默认 `first_install`；000037 已被 ADR-0017 占用；P2 已落地） |
| transport | `internal/configcenter/transport/rest/{internal_handler,install_handler}.go` | `/install/schema`（P1）；`status` 增 `mode`（P1）/`gaps`+`blocking_count`（P2）；`begin.reason`、`apply.values`/`overrides` 合并、`remaining_gaps`、`repair_keys`（P2） |
| 进程 | `cmd/internal/configboot/gaps.go` | `MissingRequired`（必填缺值，排除敏感键）、`RequireComplete`（fail-fast 进程的错误文案；P3 已落地） |
| 平台 | `internal/platform/setup_only.go` | `RunSetupOnly` + `SetupOnlyAutoExitEnv`：探针/版本/安装代理，`/readyz` 固定 503 并带缺失键（P3 已落地） |
| 进程 | `cmd/admin-api/setup_only.go` | 降级态装配与 `ready` 后自动退出（P3 已落地） |
| 进程 | `cmd/internal/configboot/diagnose.go` | `DiagnoseMissingValues`（占位法区分缺值/值非法）、`FillPlaceholders`、`placeholderValue`（P5 已落地） |
| 进程 | `cmd/internal/configboot/{diagnose_invalid.go,repair.go}` | `DiagnoseInvalidValues`（点名键 → 单键试探，判不出不降级）、`ResetInvalidValues`（调内部自愈端点）（P6 已落地） |
| app | `internal/configcenter/app/reset_defaults.go` | `BootstrapService.ResetToDefaults`：逐键校验（敏感/无默认/已归档跳过）→ 新版本 → `install-reset-defaults` 发布 → 指针 CAS → 审计 `mode=reset-defaults`（P6 已落地） |
| transport | `internal/configcenter/transport/rest/reset_handler.go` | `ResetDefaultsHandler`：服务身份 + namespace allowlist + env 一致性 + 事务包裹（P6 已落地） |
| 前端 | `frontend/apps/web-admin/src/views/_core/install/index.vue`、路由表 | 目录驱动表单（分域、默认值徽标、必填/敏感标记）、repair 只显阻塞键、`/setup/install`免登录白名单 |
| 契约 | `api/openapi/paths/{internal,admin}/install.yaml`、`schemas/internal/install.yaml` | schema 端点与响应契约（P1 已落地）；gaps/repair 契约（P2 已落地）；`begin.declared_keys`（P5，internal 1.4.0 / admin 9.6.0） |
| 契约 | `api/openapi/paths/internal/repair.yaml`、`schemas/internal/repair.yaml` | 启动自愈端点与响应契约（P6，internal 1.5.0） |
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
| P5 | 待定 5 两条腿：`begin.declared_keys` 声明白名单（三条拒绝规则 + 上限 64 + `MissingRefs` 复核）、`configboot.DiagnoseMissingValues` 占位诊断（保守方向）、`admin-api` 校验失败时按诊断结果分流；OpenAPI 1.4.0/9.6.0 | 已完成 |
| P4 | 前端 schema 驱动向导：`api/configcenter/install.ts` 补 schema/status/begin/apply/complete 类型与 `declared_keys`；`views/_core/install/schema.ts` 纯函数层（分组/默认值徽标/缺项/草稿转换）；`index.vue` 五步向导（repair 只渲染阻塞键、密钥只读） | 已完成 |
| P6 | 启动自愈：`configboot.DiagnoseInvalidValues`（按错误文案点名键 → 单键试探两阶段）+ `ResetInvalidValues`；`app.BootstrapService.ResetToDefaults`（新版本 + `install-reset-defaults` 发布 + 指针 CAS + 审计 `mode=reset-defaults`）；`POST /internal/v1/configs/repair/reset-defaults`（internal 1.5.0）；admin-api 重置后重载重试；单测覆盖定位/保守/HTTP 契约 | 已完成 |

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

1. ~~**「值非法」死锁的自救通道**~~：已结案（P6，2026-11-23）——不做 `CONFIGCENTER_REPAIR_FORCE` 这类逃生令牌，改为**把非法值重置为声明默认值后启动**：`configboot.DiagnoseInvalidValues` 判定「键在但值错」后调 `POST /internal/v1/configs/repair/reset-defaults`，config-api 按 key spec（prod-like overlay 优先）解析默认值、追加 item 版本、生成 `install-reset-defaults` 发布并以 CAS 推进指针，再由编排重启为正常进程。调用方只声明 `namespace/key`，不能指定取值；敏感键、无默认值、未声明、已归档键一律跳过并回 `skipped` 原因。
2. ~~自动退出还是手动重启~~：已结案（P3）——默认 `ready` 后自动退出并由编排重启，`ADMIN_FRAME_SETUP_ONLY_AUTO_EXIT=false` 可退回手动重启。
3. ~~schema 的可见范围~~：已定案——`/install/schema` 要求 setup token（键名、默认值与校验规则虽不含密文，也不应匿名可读）；若后续要支持「只读预览不填 token」再单独评估。
4. ~~**repair 白名单与运行期改值的边界**~~：已定案（2026-11-23，用户决策「不需要」）——repair 严格只覆盖阻塞键，非阻塞键的变更必须退出向导走管理面审批；不提供 `also_update` 之类的旁路。
5. ~~阻塞判定是否扩到「校验层缺值」~~：已结案（P5）——两条腿同时落地：`begin(declared_keys)` 让运维登记服务端算不出的阻塞键（须已声明、非敏感、尚未在生效快照有值，上限 64），`configboot.DiagnoseMissingValues` 用占位法区分「缺值」与「值非法」，只有前者降级。保守方向：占位值本身不合法时继续 crash-loop，绝不把类型/枚举错误吞成缺值。

## 同步文档

- [`../CONFIG_STANDARDS.md`](../CONFIG_STANDARDS.md) §4：补「缺键阻塞 → setup-only → repair」链路；CFG-09 改为全环境安装向导。
- [`../runbooks/config-center.md`](../runbooks/config-center.md)：补 repair 值班流程与告警处置；P5 增「gaps 为空但进程起不来」的 `declared_keys` 操作步骤。
- [`../local-development.md`](../local-development.md)：dev 启动流程补安装向导步骤与 `CONFIG_INSTALL_SETUP_TOKEN`。
- [`../config-center-plan.md`](../config-center-plan.md)：增补 D17（repair 与降级），D16 标注被 ADR-0018 部分修订。
