# 配置中心缺口修复规划（Remediation）

> **状态**：规划生效 v1.0（Wave 0–3 任务闭环；CC-R-11 cors/storage 仍多为 STANDARDS 永久部署侧）
> **效力**：L3 活跃计划；任务验收标准对合入有约束力；关闭条目须回写 §12 状态表
> **适用范围**：`cmd/*/main.go`、`cmd/internal/{appconfig,configboot,secretruntime,identityruntime,notificationruntime}`、`internal/configcenter/**`、`pkg/{config,configclient,secretclient}`、`api/openapi/**/config*`、`frontend/apps/web-admin/src/{api,views}/system/config/**`、`scripts/ci/check-configcenter.sh`、`docs/CONFIG_STANDARDS.md`
> **关联**：[CONFIG_STANDARDS.md](./CONFIG_STANDARDS.md)（CFG-02/04/05/07/09/10/11/12）、[config-center-plan.md](./config-center-plan.md)、[ADR-0014](./adr/0014-secret-management-envelope-encryption.md)、[ADR-0015](./adr/0015-config-hot-reload-whitelist.md)、[ADR-0016](./adr/0016-config-service-pull-model.md)、[SECURITY_STANDARDS.md](./SECURITY_STANDARDS.md)、审计画布（Cursor Canvas：`config-center-gap-audit`）
> **执行方式**：每个 `CC-R-*` 以「验收标准」全绿 + 相关 CI 门禁通过为完成；P0 未关闭前不得宣称「配置中心可生产放行」
> **最后更新**：2026-11-17
> **变更方式**：调整范围 / 新增任务须同步更新 §3 映射表、§4 总览与 §12 状态表，经架构 Owner + 安全 Reviewer 评审

---

## 1. 背景与结论

配置中心 M1～M3 已出口：业务进程经 `config-api` 拉取、`configs/` 零跟踪、JWT 已走 `secretclient`、管理面 / 密钥页可用。但 2026-11-17 缺口审计表明：

1. **覆盖率门禁只防「静默漏登」**：`TestPlatformKeyCoverage` 保证 337 个 YAML 叶子路径要么进 KeySpec（189），要么显式排除（150）；**不保证排除合理**。
2. **运行时安全仍有上线阻断项**：`env` 在 `Validate` 之后写入，生产专属校验被绕过；`applyLocalIdentityPIIKeys` 无环境守卫；多类业务密钥仍直读环境变量；`config-api` mTLS 默认 off。
3. **企业级 CRUD 未闭环**：Item 状态机（deprecate/archive）有领域实现，无 HTTP / OpenAPI / 前端；KeySpec 缺 `expires_at`；密钥子域仅 2 个 `SecretRule`；大量策略键 / float / JSON 列表被排除且 ADR-0016 后无注入路径。

**结论**：不推翻 ADR-0016 拉取模型；按本规划分四波收敛——先堵住启动与密钥回退（Wave 0），再补密钥托管与生命周期 API（Wave 1），然后对齐 CFG-02 与登记缺口（Wave 2），最后做治理体验与文档/CI 硬化（Wave 3）。

---

## 2. 范围

### 2.1 在范围内

| 范围 | 说明 |
|------|------|
| 启动校验时序 | 四业务进程 + `config-api` 的 `env` / `Validate` 顺序 |
| 本地密钥回退 | `applyLocalIdentityPIIKeys` 及同类「源码内固定密钥」路径 |
| 业务密钥托管 | identity / notification 等应进 `config_secret` 的键；收回非白名单 env 直读 |
| config-api 鉴权 | staging/prod 强制 mTLS；namespace 授权 |
| Item / Release CRUD 缺口 | Transition、Cancel；前端操作；OpenAPI |
| KeySpec 契约 | `expires_at`、`reloadable(startup\|hot)`、校验区间、枚举对齐 |
| 排除清单收敛 | 应登记的策略键 / float / JSON；或明确「永久部署侧」并写进 CONFIG_STANDARDS |
| 门禁与文档 | `check-configcenter.sh`、覆盖率测试、plan / STANDARDS 漂移 |

### 2.2 不在范围内（另行立任务）

| 项 | 原因 |
|----|------|
| 灰度发布（percentage / tenant / channel / instance） | `config-center-plan` 已降级 P1；本规划不展开 |
| 跨环境复制、导入/导出、explain、容量配额 UI | plan P1/P2；依赖本规划 Wave 1～2 闭环后再开 |
| KEK / Vault 生产接入与 key ceremony 实操 | 已有 runbook；属运维交付，非本缺口修复 |
| featureflag 域迁入配置中心 | featureflag 为独立聚合；不在 `appconfig.Config` 树 |
| 微服务拆分 config-api | 当前规模不需要 |

---

## 3. 问题与任务映射

| 任务 | 问题摘要 | 严重级 | 违反条款 | 现状证据 |
|------|----------|--------|----------|----------|
| CC-R-01 | `env` 在 `Validate` 之后写入，prod 门禁被绕过 | P0 | CFG-05、CFG-03 | `cmd/{admin,biz,c}-api/main.go`、`cmd/worker/main.go`、`cmd/config-api/main.go`：`LoadFromValues` 后再 `appCfg.Env = …` |
| CC-R-02 | `applyLocalIdentityPIIKeys` 无 env 守卫，写入固定十六进制 | P0 | CFG-06、CFG-10、SEC | `cmd/admin-api/main.go` `applyLocalIdentityPIIKeys` |
| CC-R-03 | 业务密钥直读 env（短信 AK、通知盐/回调密钥等） | P0 | CFG-04、CFG-10、CFG-11 | `identityruntime/runtime_c.go` `ALIYUN_SMS_*`；`notificationruntime` `ADMIN_FRAME_NOTIFICATION_*` |
| CC-R-04 | config-api mTLS 默认 off，信任 `X-Service-Identity`；拉取无 namespace 授权 | P0 | CFG-11、SEC | `secretruntime/mtls.go`；internal pull handler |
| CC-R-05 | 仅 JWT + demo HMAC 进 SecretRule；hash_key / MFA / SAML / subject / 通知凭据未托管 | P0 | CFG-10、plan §5 | `spec` 仅 2 个 sensitive；`secretruntime` 仅 JWT/demo |
| CC-R-06 | Item `Transition` 未暴露；无法 deprecate/archive | P0 | 企业 CRUD、plan §6.7 | `ItemService.Transition` 无 router / OpenAPI / 前端 |
| CC-R-07 | 发布 / 回滚无整体 `appconfig.Validate` dry-run | P1 | plan CC-F-08 精神 | `ReleaseService.Publish/Rollback` 只做单键历史校验 |
| CC-R-08 | 回滚绕过审批、无 break-glass 标记 | P1 | 发布治理 | `Rollback` 直接 Approved |
| CC-R-09 | KeySpec 缺 `expires_at`；`Reloadable` 为 bool 非 `startup\|hot` | P1 | CFG-02 §3.3 | `domain/keyspec.go` |
| CC-R-10 | 大量 int/duration 无 Min/Max；枚举与代码不一致 | P1 | CFG-02 | identity/audit/iam 多数 int 无 MinInt；`storage.driver` 缺 minio |
| CC-R-11 | 150 排除项中策略键 / float / JSON / cors / storage 非密钥无注入路径 | P1 | CFG-07、ADR-0016 | `config_spec_coverage_test.go` 排除清单 |
| CC-R-12 | 前端创建手填 key；密钥页缺 versions/activate/grants/accesses | P1 | 管理面完整性 | `web-admin` config views / `api/configcenter/secrets.ts` |
| CC-R-13 | 已播种环境不增量补种；required 缺失只记 skipped_count | P1 | CFG-09 | `BootstrapService.EnsureBaseline` 遇指针即返回 |
| CC-R-14 | Secret 审计吞错；ActivateVersion 无审批；不校验 SecretRule | P1 | CFG-10、AUD | `SecretService` `_ = audit.emit` |
| CC-R-15 | 硬编码时长 / 端口 / issuer 覆盖配置中心值 | P1 | CFG-07 | 各 `cmd/*/main.go` 固定值 |
| CC-R-16 | Release Cancel 无路由；列表无 keyword/owner | P2 | plan P1 | router 对照 |
| CC-R-17 | 发布 UI 无热更/需重启标注；非热更变更仅指标 | P2 | ADR-0015 | HotKeys vs 管理面 |
| CC-R-18 | `check-configcenter.sh` / plan 注释漂移；无「排除合理性」门禁 | P2 | CI-17 精神 | scripts + plan §6.8 |

---

## 4. 任务总览

| 任务 | 名称 | 波次 | 预估 | 依赖 | 主责 |
|------|------|------|------|------|------|
| CC-R-01 | 启动期提前注入 env 并恢复 prod Validate | W0 | 0.5d | — | 后端 |
| CC-R-02 | 守卫 / 删除本地 PII 密钥回退 | W0 | 0.3d | CC-R-01 | 后端 + 安全 |
| CC-R-03 | 收回非白名单业务密钥 env 直读 | W0 | 0.5d | CC-R-05（可并行设计） | 后端 |
| CC-R-04 | staging/prod 强制 mTLS + namespace 授权 | W0 | 1.0d | — | 后端 + 安全 |
| CC-R-05 | 业务密钥 SecretRule 登记与 secretclient 消费 | W1 | 2.0d | CC-R-02/03/04 | 后端 + 安全 |
| CC-R-06 | Item Transition API + OpenAPI + 前端 | W1 | 1.0d | — | 后端 + 前端 |
| CC-R-07 | 发布 / 回滚前整体 Validate dry-run | W1 | 1.0d | CC-R-01 | 后端 |
| CC-R-08 | 回滚 break-glass 与审计补强 | W1 | 0.5d | CC-R-07 | 后端 |
| CC-R-14 | SecretService 审计 fail-closed + SecretRule 校验 | W1 | 0.8d | CC-R-05 | 后端 |
| CC-R-09 | KeySpec 对齐 CFG-02（expires_at / reloadable 枚举） | W2 | 1.0d | — | 后端 |
| CC-R-10 | 校验区间与枚举对齐代码 | W2 | 0.8d | CC-R-09 | 后端 |
| CC-R-11 | 排除清单收敛：登记或永久部署侧文档化 | W2 | 1.5d | CC-R-09 | 后端 + 架构 |
| CC-R-12 | 管理面：KeySpec 驱动创建 + 密钥页补齐 | W2 | 1.5d | CC-R-06/05 | 前端 |
| CC-R-13 | 增量补种与 spec 漂移对账 | W2 | 1.0d | CC-R-09 | 后端 |
| CC-R-15 | 去掉覆盖中心值的硬编码（或改为声明默认） | W2 | 0.8d | CC-R-01 | 后端 |
| CC-R-16 | Release Cancel + 列表检索增强 | W3 | 0.8d | CC-R-06 | 后端 + 前端 |
| CC-R-17 | 发布变更标注 hot vs restart | W3 | 0.5d | CC-R-09 | 前端 |
| CC-R-18 | CI / 文档漂移与排除理由门禁 | W3 | 0.5d | CC-R-11 | 后端 + CI |

**里程碑**

| 里程碑 | 包含 | 出口标准 |
|--------|------|----------|
| **W0 可安全启动** | CC-R-01～04 | prod 配置下 demo/缺 hash_key/样例密钥启动失败；非生产才允许本地 PII 回退；staging/prod mTLS=require；`make ci` 绿 |
| **W1 密钥与生命周期** | CC-R-05～08、14 | 清单内业务密钥可经 secretclient 解析；Item 可 archive；Publish dry-run 失败阻断；Secret 审计 fail-closed |
| **W2 契约与登记** | CC-R-09～13、15 | KeySpec 满足 CFG-02；排除清单仅含「永久部署侧」或已登记；前端创建受 KeySpec 约束；增量补种可运行 |
| **W3 治理体验** | CC-R-16～18 | Cancel/检索可用；发布 UI 显示影响面；CI 能拦无理由排除与文档漂移 |

---

## 5. Wave 0 — 可安全启动（上线阻断）

### CC-R-01 启动期提前注入 env 并恢复 prod Validate

**目标**：`Validate` / `Normalize` 看到的 `Env` 与运行环境一致，CFG-05 生效。

**改动要点**

1. 在 `LoadFromValues` 之前或之内注入 `env`（优先扩展 `configboot.WithProcessIdentity` / `LoadFromValues` 显式参数，避免「先校验后赋值」）。
2. 四业务进程 + `config-api` 统一时序；禁止 `Load` 成功后再覆盖 `Env` 却不重新 Validate。
3. 集成 / 单测：`env=prod` + `auth.mode=demo`、缺 `identity.hash_key`、样例密钥 → 非零退出。

**验收**

- [x] 上述用例在 admin/biz/c/worker 至少各有一条启动失败断言
- [x] `make test-ci` / 相关包测试通过（见合入前 CI）
- [x] 文档：`CONFIG_STANDARDS` §3.1 补充「`CONFIGCENTER_ENV` 必须在 Validate 前可见」

### CC-R-02 守卫 / 删除本地 PII 密钥回退

**目标**：生产路径不得使用源码内固定密钥。

**改动要点**

1. `applyLocalIdentityPIIKeys` 仅当 `env ∈ {local,dev,test}`（或显式 `ALLOW_LOCAL_PII_KEYS=1` 且非 prod/staging）。
2. 其它环境缺 HashKey / Subject.Keys → fail-fast，提示走 config_secret。
3. 安全 Review：确认无第二处同类回退。

**验收**

- [x] prod/staging 缺密钥启动失败（Validate 要求 hash_key；本地回退已守卫）
- [x] local/dev 行为保持可开发
- [ ] gitleaks / Review 无新增高熵「生产样例」误用（待 Review）

### CC-R-03 收回非白名单业务密钥 env 直读

**目标**：业务密钥只经 `secretclient` → config-api；`ADMIN_FRAME_*` 仅 db/redis。

**改动要点（可与 CC-R-05 分 PR：本任务先「禁止静默成功」）**

1. 盘点并列出：`ALIYUN_SMS_*`、`ADMIN_FRAME_NOTIFICATION_*`、OAuth `client_secret_env` 等。
2. Wave 0 最低标准：staging/prod 若仍依赖裸 env → 启动或装配报错（或明确临时豁免票 + 过期日）。
3. 完整迁入密钥子域放在 CC-R-05。

**验收**

- [x] 清单完整并挂到本文件附录 A
- [x] staging/prod 路径无「读不到密钥却用空/默认继续」（`RejectBareSecretEnv` + 空凭据 fail-fast）

### CC-R-04 staging/prod 强制 mTLS + namespace 授权

**目标**：config-api 内部面不可被任意持有 URL 的调用方冒充。

**改动要点**

1. `env ∈ {staging,prod}` 时 `CONFIG_API_MTLS=require`（缺 CA 则启动失败）。
2. `GetNamespaceConfig` / `Notifications` 按服务身份做 namespace 授权（与 secret grant 模型对齐或简化 allowlist）。
3. 指标 / 审计：拒绝次数可观测。

**验收**

- [x] 单测：prod + mTLS=off → config-api 拒绝启动
- [x] 单测：无授权身份拉 namespace → 403
- [x] runbook `config-center.md` / `secret-management.md` 更新引导变量

---

## 6. Wave 1 — 密钥托管与生命周期

### CC-R-05 业务密钥 SecretRule 登记与 secretclient 消费

**优先顺序（附录 A 冻结后按此实施）**

1. `identity.hash_key` / `previous_hash_keys` / subject 密钥环  
2. MFA `secret_key` / keys  
3. SAML `private_key`  
4. 通知 SMTP / 短信 / 推送凭据  
5. `iam.breakglass.allowed_subjects`（非密钥但高敏，可走 JSON KeySpec + 双人发布）

**验收**

- [x] 单值密钥已 Sensitive+SecretRule：hash_key / mfa.secret_key / saml.private_key / subject.key / blacklist_salt / callback_secret / smtp.password / aliyun AK secret / apns / fcm
- [x] 上述键经 `secretclient.Resolve`（SAML 有则注入；渠道凭据 local 可回退裸 env）
- [x] staging/prod 要求 `CONFIG_BOOTSTRAP_GRANT_IDENTITY`（禁止默认过宽 grant）
- [x] OAuth `client_secret`：`identity.oauth.<provider>.client_secret` + secretclient（local 可回退 env）
- [x] `subject.key` 托管当前 active 槽；多版本 `subject.keys` / `previous_hash_keys` / `mfa.keys` 仍按 STANDARDS §3.1.1 永久排除

### CC-R-06 Item Transition API + 前端

**改动要点**

1. `POST /admin/v1/configs/items/{id}/transition`（或 `/archive` `/deprecate`）接 `ItemService.Transition`
2. OpenAPI + 权限（write；归档可要求 approve）
3. 前端 items / edit 页操作 + 审计事件

**验收**

- [x] handler 测试覆盖合法迁移与非法终态
- [x] 前端 edit 页可完成 active → deprecated → archived
- [ ] `make check-openapi` 通过（合入前跑）

### CC-R-07 / CC-R-08 发布 dry-run 与回滚治理

**验收**

- [x] Publish/Rollback 前对快照键跑 KeySpec.ValidateValue dry-run；失败不改指针
- [x] Rollback 带 break-glass 标记与审计；runbook 规定 24h 补审

### CC-R-14 SecretService  hardening

**验收**

- [x] 审计 emit 失败则请求失败（`requireAudit`）
- [x] Create/Rotate 校验 Registry + SecretRule（`enforceSecretRule`）
- [x] ActivateVersion 须显式 break_glass + reason（OpenAPI 已对齐）

---

## 7. Wave 2 — 契约与登记收敛

### CC-R-09 / CC-R-10 KeySpec 对齐 CFG-02

- [x] 增加 `ExpiresAt`（可选治理到期）与 `ReloadMode`（`startup|hot`）；`Reloadable` 布尔保留并与 Mode 同步
- [x] `storage.driver` 补 `minio`；`auth.expected_channel` 补 `worker`/`internal`
- [x] identity / audit / iam / tenant / platform http 主要 int 键补 MinInt

### CC-R-11 排除清单收敛

对附录 B 每一项二选一：

1. **登记进 KeySpec**（推荐：策略参数、cors、storage 非密钥、float 用 string+pattern 或新 float 类型）  
2. **永久部署侧**：写入 `CONFIG_STANDARDS` §3.1 补充表 + 排除理由不可再写「M1 暂不支持」

**进度**

- [x] `CONFIG_STANDARDS` §3.1.1 永久排除表
- [x] identity `*.rps` / `*.ip_rps` 与 `tracing.sample_ratio` 登记为 string+pattern
- [x] 禁止 `excludedSuffixes` 以「M1 暂不支持」排除浮点
- [ ] cors / storage 非密钥连接参数分类收口（仍多为永久部署侧）

### CC-R-12 / CC-R-13 / CC-R-15

- [x] 前端创建：声明键 API + items 创建下拉（排除敏感/已存在）
- [ ] 密钥页：versions / activate / grant revoke / accesses（API 已有；UI 待补齐）
- [x] `EnsureBaseline` 已有指针时增量补齐缺失默认键（`spec-reconcile` Release）
- [x] admin-api 对 issuer/mode 等改为 `SetIfEmpty`；addr/service/channel 仍属部署身份覆盖

---

## 8. Wave 3 — 治理体验与门禁

- [x] CC-R-16：Release Cancel 路由 + OpenAPI + 前端；List keyword/owner  
- [x] CC-R-17：声明键 `hot_whitelist` + 发布创建/详情标注热更 vs 需重启  
- [x] CC-R-18：`check-configcenter` CC-15；排除理由禁过时表述；plan §6.8 对齐 ADR-0015/0016

---

## 9. 依赖与风险

| 风险 | 缓解 |
|------|------|
| Wave 0 收紧后本地开发摩擦 | 保留 local/dev 回退；`scripts/local` 与 runbook 给一份最小密钥播种步骤 |
| 密钥迁入导致已有环境缺 grant | 提供一次性 migrate/grant 脚本；staging 先演练 |
| dry-run Validate 与多进程配置子集不一致 | dry-run 按 namespace 使用与目标进程相同的 Validate 入口 |
| mTLS 强制阻断现有联调 | 分环境开关；dev 可 optional，文档标明禁止用于 staging |

---

## 10. 门禁与验证矩阵

| 波次 | 必跑 |
|------|------|
| 每 PR | `make fmt-check lint test-ci check-configcenter check-openapi`（按改动） |
| W0 出口 | 上表 + 新增 prod 启动失败用例 + `make ci-strict`（或 `ci-full --skip-image`） |
| W1 出口 | W0 + secret 集成测 + Item transition 契约测 |
| W2 出口 | W1 + KeySpec/覆盖率测试更新 + 前端冒烟清单 |
| W3 出口 | W2 + CI 排除合理性检查 + 文档会签 |

---

## 11. MUST NOT

- 不得用「扩大 excludedKeys」关闭覆盖率失败来代替登记或 STANDARDS 声明。
- 不得在未完成 CC-R-01/02 时宣称「CFG-05 已在生产生效」。
- 不得新增 `ADMIN_FRAME_*` 业务密钥通道。
- 不得在 Secret 路径 `_ = audit.emit` 后继续返回成功。
- 不得通过 `--no-verify` / 临时关掉 `check-configcenter` 合入 Wave 任务。

---

## 12. 状态表

| 任务 | 状态 | 更新日期 | 备注 |
|------|------|----------|------|
| CC-R-01 | 已完成 | 2026-11-17 | ProcessIdentity.Env + 各 cmd 注入；CONFIG_STANDARDS §3.1 |
| CC-R-02 | 已完成 | 2026-11-17 | IsLocalDevEnv 守卫；待安全 Review 勾选 gitleaks 项 |
| CC-R-03 | 已完成 | 2026-11-17 | RejectBareSecretEnv；附录 A 已冻结；完整迁入见 CC-R-05 |
| CC-R-04 | 已完成 | 2026-11-17 | prod-like 强制 mTLS；CONFIG_API_NAMESPACE_ALLOWLIST |
| CC-R-05 | 已完成 | 2026-11-17 | 单密钥+subject.key+OAuth；多版本环按 STANDARDS 永久排除 |
| CC-R-06 | 已完成 | 2026-11-17 | Transition API + OpenAPI + edit 页 |
| CC-R-07 | 已完成 | 2026-11-17 | Publish/Rollback KeySpec dry-run |
| CC-R-08 | 已完成 | 2026-11-17 | break_glass 强制 + 审计 + runbook |
| CC-R-09 | 已完成 | 2026-11-17 | ReloadMode + ExpiresAt；HotKeys 仍为热更唯一源 |
| CC-R-10 | 已完成 | 2026-11-17 | 枚举对齐；主要 int MinInt 已补 |
| CC-R-11 | 进行中 | 2026-11-17 | STANDARDS §3.1.1 + 浮点登记；cors/storage 仍多为永久部署侧 |
| CC-R-12 | 已完成 | 2026-11-17 | 声明键 API + 创建下拉 + 密钥详情（版本/授权/访问） |
| CC-R-13 | 已完成 | 2026-11-17 | EnsureBaseline 增量 reconcile |
| CC-R-14 | 已完成 | 2026-11-17 | audit fail-closed + SecretRule + Activate break_glass |
| CC-R-15 | 已完成 | 2026-11-17 | SetIfEmpty；部署身份仍覆盖 addr |
| CC-R-16 | 已完成 | 2026-11-17 | Cancel API + keyword/owner 列表；Admin OpenAPI 9.2.0 |
| CC-R-17 | 已完成 | 2026-11-17 | DeclaredKey.hot_whitelist=HotKeySet；发布页标注 |
| CC-R-18 | 已完成 | 2026-11-17 | CC-15 门禁 + 排除理由测试 + plan §6.8 对齐 |

**会签（开 Wave 0 前）**

| 角色 | 姓名 | 结论 | 日期 |
|------|------|------|------|
| 架构 Owner | | | |
| 安全 Reviewer | | | |

---

## 附录 A — 业务密钥 / 高敏 env 迁入清单（已冻结 2026-11-17）

> Wave 0：staging/prod 禁止裸 env 直读成功（`secretruntime.RejectBareSecretEnv`）。
> Wave 1（CC-R-05）按行迁入 `config_secret` + `secretclient`。

| 名称 | 当前来源 | Wave 0 行为 | 目标 | 状态 |
|------|----------|-------------|------|------|
| identity.hash_key / previous_hash_keys | 排除 + 本地回退 | 仅 local/dev/test 回退；prod Validate 必填 | config_secret + secretclient | hash_key 已迁入；previous 环永久排除 |
| identity.subject.keys / subject.key | 排除 + 本地回退 | 同上 | config_secret | subject.key 已迁入；多版本 keys 永久排除 |
| identity.mfa.secret_key / keys | 排除 | Validate 生产约束 | config_secret | secret_key 已迁入；keys 环永久排除 |
| identity.saml.private_key | 排除 | Validate 生产约束 | config_secret | 已迁入 SecretRule + Resolve |
| ALIYUN_SMS_ACCESS_KEY_ID / SECRET | 裸 env | prod-like 拒绝；空值 fail-fast | config_secret | 短信 AK Secret 已迁入；ID 与 identity 短信待续 |
| OAuth `client_secret_env` | 裸 env（名可配） | prod-like 拒绝；空值 fail-fast | config_secret | 已迁入 secretclient（动态名） |
| notification SMTP PasswordEnv | 裸 env | prod-like 拒绝；空值 fail-fast | config_secret | 已迁入 SecretRule + Resolve |
| notification Aliyun AccessKeySecretEnv | 裸 env | 同上 | config_secret | 已迁入 SecretRule + Resolve |
| notification APNS PrivateKeyEnv | 裸 env | 同上 | config_secret | 已迁入 SecretRule + Resolve |
| notification FCM CredentialsJSONEnv | 裸 env | 同上 | config_secret | 已迁入 SecretRule + Resolve |
| ADMIN_FRAME_NOTIFICATION_BLACKLIST_SALT | 非白名单 env | prod-like 拒绝；空值 fail-fast | config_secret | 已迁入 SecretRule + Resolve（local 可回退 env） |
| ADMIN_FRAME_NOTIFICATION_CALLBACK_SECRET | 非白名单 env | prod-like 拒绝；长度不足 32 字节 fail-fast | config_secret | 已迁入 SecretRule + Resolve（local 可回退 env） |
| iam.breakglass.allowed_subjects | 排除 | 非密钥 | JSON KeySpec + 双人发布 | 待 CC-R-05/11 |

## 附录 B — 排除项分类原则

| 分类 | 处理 | 示例 |
|------|------|------|
| 部署身份 | 保持排除，Validate 前注入 | env/service/channel/id.node_id |
| 基础设施口令 | 保持排除，仅 ADMIN_FRAME_DB_/REDIS_ | db.dsn、redis.password |
| 应登记策略参数 | Wave 2 进 KeySpec | password/mfa 默认、rate_limit、cors |
| 应进密钥子域 | Wave 1 | 附录 A |
| 云厂商非密钥连接参数 | 登记或 STANDARDS「部署侧」 | storage.endpoint/region |
| 过时理由（「M1 无 float」） | 禁止保留 | *.rps、sample_ratio |

---

## 附录 C — 审计输入

| 输入 | 说明 |
|------|------|
| 2026-11-17 配置中心缺口审计 | Cursor Canvas `config-center-gap-audit`；覆盖 KeySpec 189 / 排除 150 / CRUD 缺口 |
| 深度审计补充 | env 时序、PII 回退、config-api 鉴权、注入路径缺失（与本规划 §3 对齐） |
