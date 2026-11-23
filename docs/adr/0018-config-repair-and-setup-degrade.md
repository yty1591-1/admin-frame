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
   gaps:[{namespace, key, description}]}

GET  /internal/v1/install/catalog[?namespace=]
→ {namespaces:[{name, owner}],
   keys:[{namespace, key, type, default?, required, sensitive, reloadable, deprecated,
          description, enum?, min?, max?, pattern?, secret_rule?}]}
   · sensitive 项不返回 default；不返回任何已入库值
   · 读接口不要求 token；写接口要求

POST /internal/v1/install/begin     {owner, reason:"first_install"|"repair"}
POST /internal/v1/install/apply     {owner, values:{namespace:{key:value}}, comment?}
POST /internal/v1/install/complete  {owner}
```

- 请求头：`X-Config-Install-Token`；`apply` 的 `values` 由服务端按当前 `mode` 决定接受范围：`first_install` 接受任意已声明键，`repair` 只接受冻结白名单内的键。
- admin-api 继续同源代理 `/admin/v1/install/*`（ADR-0017 决策 5 不变），浏览器不直连 config-api。
- 新增审计事件：`config_install.repair.begin`、`config_install.repair.apply`、`config_install.repair.complete`；每条事件带 `mode`、`repair_keys` 与操作者 `system:setup-token`。

## 进程侧

- `cmd/internal/configboot/load.go`：`Load` 的返回值增加 `Gaps []MissingKey` 与 `Blocked bool`；计算时排除敏感键（敏感键由 `secretclient` 解析，不在快照里，不能计入缺键）。
- `internal/platform/bootstrap.go`：`Options` 增加 `SetupOnly bool`；为真时跳过依赖装配与 `Register`，挂载探针与安装代理；`/readyz` 返回 `503` 且 `reason=config_incomplete`，`details.missing_keys` 带 `Gaps`，便于告警与排障。
- `cmd/admin-api/main.go`：`boot.Blocked` 为真时走 setup-only 分支——不解析 JWT、不建仓储、不注册业务路由；后台每 5s 查一次安装态，一旦 `ready` 则 `os.Exit(0)`，由编排（`restartPolicy: Always`）重启为正常进程；本地演练由 `scripts/prod/prod-services.sh` 提示手动重启。
- 其他进程：`boot.Blocked` 为真时返回明确错误（`fmt.Errorf("%s is required by process %s; run admin /setup/install", key, service)`），保持 crash-loop。
- 过渡期：`Gaps`/`Blocked` 落地前，`configboot.Load` 保持现有 `InstallPending` 兜底（安装态非 `ready` 时用 key spec 默认值继续启动），避免一次性改动装配骨架；P2 用 setup-only 替换该兜底。

## 代码改造清单

| 层 | 文件 | 改动 |
| --- | --- | --- |
| spec | `internal/configcenter/spec/registry.go` | `MissingRequired(ns, values)`、`MissingRequiredAll(values)`、`Catalog()`（脱敏、可过滤 namespace） |
| app | `internal/configcenter/app/install.go` | `Gaps`、`Begin(reason)` 的 repair 分支、`Apply` 白名单校验、repair 审计事件 |
| app | `internal/configcenter/app/bootstrap.go` | 新增 `RepairKeys`：只写白名单键，生成新 version + `install-repair` release + 指针 CAS |
| 持久化 | `migrations/000038_*` + repository | `config_install_state` 增 `mode`、`repair_keys`（expand-only，可空默认 `first_install`；000037 已被 ADR-0017 占用） |
| transport | `internal/configcenter/transport/rest/{internal_handler,install_handler}.go` | `/install/catalog`；`status` 增 `gaps`/`mode`；`apply` 白名单与错误分支 |
| 进程 | `cmd/internal/configboot/{load,install}.go` | `Gaps`/`Blocked`；`FetchInstallStatus` 解析 `gaps` |
| 平台 | `internal/platform/bootstrap.go` | `Options.SetupOnly`；`/readyz` 携带缺失键 |
| 进程 | `cmd/admin-api/{main,register}.go` | setup-only 分支与 `ready` 后自动退出 |
| 前端 | `frontend/apps/web-admin/src/views/_core/install/index.vue`、路由表 | 目录驱动表单（分域、默认值徽标、必填/敏感标记）、repair 只显阻塞键、`/setup/install`免登录白名单 |
| 契约 | `api/openapi/paths/admin/install.yaml` 等 | 新增 catalog 与 gaps 契约 |
| 门禁 | `scripts/ci/check-configcenter.sh` | 断言每个 `Required` 键可经向导填写（非敏感或带 `SecretRule`）；install 端点与状态机契约 |
| 装配 | `cmd/internal/configcenterruntime/runtime.go` | 删除 `IsProdLikeEnv` 分支：一律 `EnsureState(pending)`，仅 `ready` 时幂等 `bootstrap`/`bootstrapSecrets`；删除 `MarkReady` |
| 装配 | `internal/configcenter/app/install.go` | `RequireSetupTokenConfigured` 对所有环境强制；删除 `MarkReady` |
| 本地编排 | `scripts/local/{dev-services.sh,dev-services.ps1,env.example}`、Makefile | dev 固定 token；`start` 在 config-api 就绪后自动驱动向导；新增 `install` 子命令与 `make local-install` |

## 实施状态（2026-11-21）

| 阶段 | 内容 | 状态 |
| --- | --- | --- |
| P0' | 统一安装路径：删 `IsProdLikeEnv` 分支与 `MarkReady`；所有环境强制 setup token；本地脚本自动驱动向导（`install` 子命令 + `make local-install`） | 已完成 |
| P0'' | 现状契约补齐：admin 安装代理四端点登记 OpenAPI（`admin.yaml` 9.3.0，`InstallState` 上提 `components.yaml` 共享）+ `check-configcenter` 断言 | 已完成 |
| P1 | `spec.Registry` 的 `MissingRequired` / `Catalog`；`status` 出 `gaps`/`mode`；`catalog` 端点 | 待做 |
| P2 | `Begin(repair)`、`Apply` 白名单、`RepairKeys` 写入；迁移 000038 | 待做 |
| P3 | `configboot.Load` 出 `Gaps`/`Blocked`；`platform.Options.SetupOnly`；admin-api 降级与自动退出 | 待做 |
| P4 | 前端目录驱动表单；`gaps` / `catalog` / `repair` 契约（安装代理现状契约已在 P0'' 补齐）；目录端点断言 | 待做 |

## 安全与失效

- **攻击面**：setup-only 只暴露 4 个路由，业务路由一律 404，等同把进程收窄成安装器；仍需 TLS（prod 走 mTLS/ingress），并对 `begin/apply/complete` 按 IP 限流。
- **一致性**：`apply` 在单事务内完成「新版本 + 新 release + 指针 CAS」；指针被其他发布抢占时返回冲突，向导需刷新后重试。
- **可观测**：新增指标 `config_install_blocked{service}`、`config_install_gaps`；告警 `ConfigInstallBlocked`（readyz 503 持续 10m）；日志首行固定 `SETUP-ONLY MODE: missing required keys ...`，便于 grep。
- **误降级防护**：`Gaps` 只由注册表声明计算；未声明键导致的失败仍走 crash-loop，杜绝「代码 bug 被降级掩盖」。

## 后果

**正面**：新键阻塞启动时不再死锁，运维用浏览器即可自救；首装可全量确认默认值；非阻塞变更仍受审批与审计约束。

**负面**：多一条状态机分支与一次迁移；admin-api 存在「降级态」这一非常规运行形态，需要告警与 runbook 配合；repair 语义要求键声明必须准确（`Required` 用错会放大阻塞面）。统一安装路径后，**所有环境**首次启动都多一步向导（本地脚本已自动化），且 `CONFIG_INSTALL_SETUP_TOKEN` 成为全环境必填变量（prod 由编排注入，严禁默认值）。

**守卫**：`check-migration-gate`（expand-only）、`check-configcenter`、安装态单测（`gaps`/`repair` 白名单/状态迁移）、集成用例「缺键 → 降级 → 修复 → 重启变绿」。

## 待定

1. **「值非法」死锁的自救通道**：按决策 1 不降级，则「键在、值错」时向导也进不去。是否增加一个显式逃生门——例如运维在进程侧注入 `CONFIGCENTER_REPAIR_FORCE=<token 指纹>` 后，允许把「存在但非法」的键纳入 repair 白名单（仍要 token、仍留审计）？
2. **自动退出还是手动重启**：setup-only 的 admin-api 在 `ready` 后自动 `exit(0)`（依赖编排重启）还是只提示运维手动重启？前者少一步人工，后者更可控。
3. **catalog 的可见范围**：目录元数据是否需要 token？当前设计为免 token 只读（不含任何已入库值），但默认值本身可能暴露内部端点结构。

## 同步文档

- [`../CONFIG_STANDARDS.md`](../CONFIG_STANDARDS.md) §4：补「缺键阻塞 → setup-only → repair」链路；CFG-09 改为全环境安装向导。
- [`../runbooks/config-center.md`](../runbooks/config-center.md)：补 repair 值班流程与告警处置。
- [`../local-development.md`](../local-development.md)：dev 启动流程补安装向导步骤与 `CONFIG_INSTALL_SETUP_TOKEN`。
- [`../config-center-plan.md`](../config-center-plan.md)：增补 D17（repair 与降级），D16 标注被 ADR-0018 部分修订。
