# 工程化配置与多环境规范（硬约束）

> **状态**：已批准（Approved）；M3 已删除 `configs/` 跟踪文件并关闭迁移期样例例外；见 §10
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**
> **适用范围**：配置 key spec、配置中心加载、首次启动初始化、业务密钥托管、多环境与迁移门禁
> **关联**：[ARCHITECTURE.md](./ARCHITECTURE.md) §4.4 / §5.6 / §17、[SECURITY_STANDARDS.md](./SECURITY_STANDARDS.md)、[config-center-plan.md](./config-center-plan.md)、[ADR-0013](./adr/0013-config-center-in-process-snapshot.md)、[ADR-0014](./adr/0014-secret-management-envelope-encryption.md)、[ADR-0016](./adr/0016-config-service-pull-model.md)
> **执行方式**：`Validate()` 单测 + `check-configcenter.sh` + `check-no-static-config.sh` + gitleaks + Review
> **最后更新**：2026-11-16（v3：CFG-04/11/12 收敛为 config-api 拉取模型，移除签名快照启动前置）
> **变更方式**：修改本规范须同步更新 `pkg/config`、`internal/configcenter`、`cmd/internal/appconfig`、`scripts/ci/check-configcenter.sh`、`scripts/ci/check-no-static-config.sh`、`deploy/` 与架构 §17，经架构 Owner 与安全 Reviewer 评审

---

## 1. 目标

1. **启动即安全**：非法配置、互斥项、生产演示模式在启动时失败，不带病运行。
2. **单一事实源**：配置以配置中心为唯一事实源，代码 key spec 只提供类型、默认值与校验规则。
3. **空环境可启动**：首次部署无配置记录时，按代码默认值自动播种基线 Release，不依赖人工导入。
4. **密钥明文不入库**：业务密钥信封加密后入库，KEK 外部托管；明文不落库、不落快照、不落日志、不回显前端。
5. **优先级清晰**：业务配置「config-api 拉取结果 > 本地缓存 > 代码默认值」；基础设施口令仅 `ADMIN_FRAME_DB_*` / `ADMIN_FRAME_REDIS_*`；业务密钥仅 `secretclient` → `config-api`。

**阶段说明**：ADR-0016 已落地。业务进程经 config-api 拉取配置，本地缓存仅作灾备；禁止业务 `ADMIN_FRAME_*` 覆盖；JWT 等业务密钥禁止 env/文件回退。仓库 `git ls-files configs` 必须为空。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| CFG-01 | 删除 `configs/` 下全部运行时静态配置；**禁止新增** `configs/*.yaml` / `*.env` / `*.example` | `check-no-static-config.sh` |
| CFG-02 | 配置 key spec 由代码声明并作为唯一来源：`key` / `value_type` / `default` / `validation` / `required` / `owner` / `reloadable` / `expires_at`；**不产出引导配置清单与配置中心导入清单** | Review + `check-configcenter.sh` |
| CFG-03 | `LoadConfig` 按 CFG-04 顺序加载，由 `Loader` 自动调用 `Validate()`；失败则进程非零退出 | 代码 + 单测 |
| CFG-04 | 业务配置优先级：**config-api 拉取 > 本地缓存 > 代码默认值**；`ADMIN_FRAME_*` 仅放行 `db.*`/`redis.*`；业务密钥仅 config-api | `configboot` + `EnvPathAllowlist` + 单测 |
| CFG-05 | `env=prod`（或等价）时 `auth.mode` 不得为 `demo`；`auth.expected_channel` 必须与 `channel` 一致；`auth.mode=jwt` 时 `issuer` / `audience` / 密钥必填 | `Validate` + 单测 |
| CFG-06 | 仓库禁止出现真实密钥、生产 DSN、云 AK/SK；测试密钥须用 `strings.Repeat` 等构造，禁止高熵字面量 | gitleaks + Review |
| CFG-07 | 禁止在业务代码硬编码监听地址、超时、功能开关；应用配置或常量默认值经 `applyDefaults`，跨字段派生经 `normalize` | Review |
| CFG-08 | 合并请求须勾选配置自检 | PR 模板 |
| CFG-09 | 首次启动空库时按 key spec 默认值播种基线 Release；初始化幂等、不覆盖已有值、强制审计 | 集成测试 + `check-configcenter.sh` |
| CFG-10 | 业务密钥只允许密文入库：每版本独立 DEK + AES-256-GCM + 外部 KEK；密钥字段必须声明 `secret_rule`，首次启动仅对允许自动生成的字段用 `crypto/rand` 生成 | 单测 + 安全评审 + gitleaks |
| CFG-11 | 普通配置读与密钥解析只经 `cmd/config-api`：mTLS / SPIFFE + 服务身份；密钥短租约；业务进程不接触 KEK，禁止明文批量导出 | 集成测试 + 安全评审 |
| CFG-12 | 业务进程启动必须经 SDK 拉取 config-api；成功写本地缓存，失败读缓存，都没有则 fail-fast；响应不含密钥明文 | 单测 + 灾备演练 |

---

## 3. 配置来源与优先级

### 3.1 运行时优先级

```text
config-api 拉取结果 > 本地缓存目录 > 代码默认值（key spec / applyDefaults）
```

补充通道（不属于业务配置层）：

| 通道 | 允许项 | 禁止项 |
|------|--------|--------|
| `ADMIN_FRAME_*` 环境变量 | 仅 `db.*` / `redis.*`（含 username/password） | `LOG_*`、`AUTH_*`、`IAM_*`、`NOTIFICATION_*`、JWT Secret 等 |
| `CONFIG_API_*` / `CONFIGCENTER_ENV` 等 | config-api URL、服务身份、TLS、环境、可选 namespaces / cache dir | 业务键取值、快照文件路径、签名公钥 |
| `secretclient` → config-api | JWT、identity、notification 等业务密钥 | 以 `ADMIN_FRAME_AUTH_JWT_SECRET` 等 env 回退 |

- yaml 文件层不再作为运行时取值来源。
- 远端与本地缓存都不可用时业务进程 **fail-fast**。
- 启动期业务键由 config-api 当前生效 Release 提供；热更只允许显式 `reloadable=hot` 的键。
- 热更配置只允许显式 `reloadable=hot` 的键；`addr` / `auth` / `storage.driver` 等需重建资源的键必须排除。
- **`CONFIGCENTER_ENV` / 进程 `env` 必须在 `Validate` 前可见**（经 `configboot.WithProcessIdentity` 或自举 Values 注入）。禁止 `LoadFromValues` / `Validate` 成功后再赋值 `appCfg.Env` 却不重新校验；否则 CFG-05 生产门禁被绕过（CC-R-01）。

#### 3.1.1 永久排除（不得进配置中心 Item 播种）

以下类别保持排除；`config_spec_coverage_test` 必须给出与本表一致的理由，**禁止**写「M1 暂不支持」：

| 分类 | 示例 | 注入方式 |
|------|------|----------|
| 部署身份 | `env` / `service` / `channel` / `addr` / `id.node_id` | `WithProcessIdentity` 或编排 |
| 基础设施口令 | `db.dsn` / `redis.password` | 仅 `ADMIN_FRAME_DB_*` / `REDIS_*` |
| 密钥环与列表 | `identity.subject.keys` / `previous_hash_keys` / `mfa.keys` / JWT keys | 密钥子域或本地回退 |
| 云厂商连接参数 | `storage.s3.*` / `notification.channels.*.smtp.host` 等 | 部署侧 / Provider |
| CORS / 本地路径 | `cors.*` / `storage.local.*` | 网关与节点部署 |

浮点速率与采样率须以 **string + pattern**（或未来 float ValueType）登记进 KeySpec，不得长期排除。

### 3.2 迁移期优先级

| 阶段 | 取值来源 | 说明 |
|------|----------|------|
| M0 | 现状文件 + 环境变量 | 仅契约冻结，不改运行行为 |
| M1 | 配置中心为主；文件仅双读对比 | 文件不参与取值；差异 > 0 阻断切换 |
| M2 | 配置中心 + 密钥子域 | 密钥类配置迁入；文件只读一个版本周期 |
| M3 | 配置中心 + 代码默认值 | 删除 `configs/`；CI 防回退 |

### 3.3 key spec 声明

每个配置键必须在代码中声明以下字段，缺失即 Review 阻断：

```text
key / value_type / default / validation / required / owner / reloadable(startup|hot) / expires_at
密钥字段额外声明：secret_rule（min_len / max_len / encoding / algorithm / prefix / auto_generate）
```

---

## 4. 首次启动初始化

### 4.1 触发条件

配置中心数据库为空（无 `config_release_pointer` 或目标 namespace 无基线 Release）时触发；非空库不重复播种。

### 4.2 初始化步骤

```text
1. 扫描代码 key spec，校验 key 唯一、类型与默认值合法、secret_rule 完整
2. 校验默认值可启动、无真实生产凭据、敏感字段不含明文默认值
3. 为每个 namespace 生成 baseline Release（status=active）
4. 对 auto_generate=true 的密钥字段用 crypto/rand 生成，按信封加密写入密钥子域
5. 对无生成规则且 required 的密钥字段，标记 missing；启动阶段 fail-fast
6. 写 config_release_pointer（CAS 初始化）
7. 审计 config.bootstrap.completed（含 key 数量、生成密钥数量、缺失项）
```

### 4.3 约束

- 初始化幂等：重复启动不新增 Release、不覆盖已有值、不重新生成密钥。
- 初始化不得调用外部业务系统；不依赖人工导入清单。
- 初始化失败必须回滚事务并 fail-fast，禁止带半初始化状态启动。
- 默认值中不得包含真实第三方凭据、生产 DSN、私钥或高熵密钥。
- 初始化日志只打印 key、来源与数量，不打印值。

---

## 5. 业务密钥管理

### 5.1 托管范围

| 允许托管 | 禁止托管 |
|----------|----------|
| 第三方 API Key、SMTP / 短信凭据、Webhook 签名密钥、业务加密 / 签名密钥 | 根密钥 / KEK、配置中心自身 DB / Redis 口令、业务进程基础设施口令、K8s 集群凭据、云账号根密钥、SSH 私钥、人员口令 |

### 5.2 密文格式

```text
algorithm   = aes-256-gcm
nonce       = 12 bytes（随机）
ciphertext  = AES-256-GCM(DEK, plaintext, AAD)
AAD         = env + ":" + namespace + ":" + name + ":" + version
wrapped_dek = KEK.wrap(DEK)
kek_key_id  = KEK 版本标识
value_hash  = SHA-256(plaintext)[:8]（仅比对，不可逆）
```

- 每个密钥版本独立 DEK；禁止复用 DEK。
- KEK 只存在于外部 KMS / Vault / HSM；配置库只存 `wrapped_dek` 与 `kek_key_id`。
- 密文、`wrapped_dek`、明文均不得出现在列表 / 详情 / 导出 / 日志 / 指标 / 追踪 / 错误体 / 拉取响应 / 本地缓存 / 前端响应中。

### 5.3 secret_rule 与自动生成

- `secret_rule` 必须声明长度、编码（`base64url` / `hex`）、算法、前缀与 `auto_generate`。
- 首次启动仅对 `auto_generate=true` 的字段用 `crypto/rand` 生成；生成即版本 1 并审计。
- 无规则或 `auto_generate=false` 的密钥必须人工录入；`required` 且缺失时 fail-fast。
- 生成值只返回指纹；不得回显、不得落日志、不得写入快照。

### 5.4 生命周期

- 创建 / 轮换 / 吊销 / 授权均双人审批，提交人 ≠ 审批人；生产密钥强制。
- 轮换保留旧版本 grace（默认 24h，可配 ≤ 7d）；`dual_version=1` 的验签类密钥返回 active + grace。
- 吊销后旧租约 ≤ TTL 失效；历史版本不可变。
- 解析经 `cmd/config-api`，mTLS / SPIFFE + 服务授权 + 短租约（默认 5m，上限 15m）；业务进程不接触 KEK。
- 密钥服务不可用时按 `required` 分级降级：required 缺失 fail-fast，非 required 使用内存缓存并标记 degraded。

---

## 6. 加载、校验与降级

### 6.1 启动顺序

```text
引导变量（CONFIG_API_* / CONFIGCENTER_ENV）→ SDK 拉取 config-api（约 3s）
→ 成功写本地缓存 / 失败读缓存 / 都没有 fail-fast
→ 合并 Values + 进程身份证（service/channel/env/addr）→ normalize → Validate → 装配
```

`env` 属于进程身份证，与 `service` / `channel` 一并在进入 Loader 前注入；不得在 Validate 通过后补写。

config-api 自身启动：代码默认值 + db/redis 环境变量 → 播种基线（空库）→ 装配拉取 / 长轮询 / 密钥 resolve。

### 6.2 失败策略

| 场景 | 行为 |
|------|------|
| config-api 不可用且无本地缓存 | 业务进程 fail-fast |
| config-api 不可用但有本地缓存 | 使用缓存启动，标记 degraded，打点告警 |
| 非关键键缺失 | 使用默认值，标记 degraded，打点告警 |
| 密钥解析不可用 | required 密钥 fail-fast；非 required 用内存缓存并告警 |

### 6.3 Validate 基线

保留现有平台段与域段校验，至少覆盖：

- `service`、`channel`、`addr` 非空；`channel ∈ {admin, biz, c, worker}`。
- `auth.expected_channel` 与 `channel` 一致；`env=prod/staging` 且未显式声明 `auth.mode` 时回退 `jwt`。
- `auth.mode=jwt` ⇒ `issuer` 非空、`audience` 含当前 channel、签名密钥满足算法要求。
- `db.driver ∈ {memory, mysql}`；非 memory 时连接参数完整；连接池上限与生命周期合法。
- `storage.driver ∈ {memory, local, s3, minio, oss, cos, obs}`；`env=prod` 禁止 memory。
- `log.level` / `log.format` 合法；`env=prod` 禁止 `text`。
- `http.*` 非负；`env=prod` 时 `http.timeout > 0`。
- 限流 / 幂等 / Outbox / 审计导出参数的取值范围与互斥项保持现有规则。
- 配置中心新增：`reloadable` 合法；`required` 与默认值互斥关系合法；`secret_rule` 完整；初始化缺失项可枚举。

### 6.4 脱敏

`platform.Config.LogValue` / `DBConfig.LogValue` / `StorageConfig.LogValue` 与各域脱敏摘要必须覆盖新增密钥字段；禁止 `%+v` / `json.Marshal(cfg)` 直接打印配置。日志、Trace、错误体不得出现密钥明文、完整密文、`wrapped_dek` 或 KEK 标识以外的密钥材料。

---

## 7. 多环境

| 环境 | `env` | 配置来源 | 密钥要求 |
|------|-------|----------|----------|
| local | local | 代码默认值 + 本地配置中心（可选） | 可用自动生成密钥；禁止生产凭据 |
| dev / test | dev / test | 配置中心 dev namespace | 自动生成或测试占位；禁止生产凭据 |
| staging | staging | 配置中心 staging namespace | 独立 KEK；`auth.mode=jwt`；双人审批 |
| prod | prod | 配置中心 prod namespace | 独立 KMS / Vault / HSM；双人审批；审计开启；轮换演练 |

- 各环境通过 `env + namespace` 隔离；禁止跨环境直接复制密钥。
- 首次启动在每个环境独立播种；生产默认值不得含真实第三方凭据。
- local 可使用 `auth.mode=demo` 与 memory DB；staging / prod 必须 `jwt` 与持久化存储。

---

## 8. 本地与 CI

```bash
make test                  # CFG-03/05/07 与 Validate
make check-configcenter    # key spec / 权限 / 审计 / OpenAPI / 迁移成对
make check-no-static-config # CFG-01 防回退
make test                  # 含初始化 / 密钥生成 / 双读单测
make secrets               # gitleaks（CFG-06）
make ci
```

| 平台 | 入口 |
|------|------|
| GitHub / Gitee | job `config` + `secrets` + `arch` |
| MR | PR 模板「配置自检」 |

---

## 9. MUST NOT

- 新增 `configs/*.yaml` / `*.env` / `*.example`，或以文件作为运行时配置来源。
- 使用环境变量覆盖业务配置（基础设施口令除外，且不属于业务配置层）。
- 产出引导配置清单或配置中心导入清单。
- 将业务密钥明文写入配置库、快照、日志、指标、Trace、错误体或前端响应。
- 让业务进程直接访问 KMS / Vault / KEK，或调用 `admin-api` 解析密钥。
- 跳过 `Validate` 直接使用配置；在 prod 打开 `auth.mode=demo`。
- 用业务代码魔法数替代可配置超时 / 地址（演示常量除外且不得用于生产路径）。

---

## 10. 例外

1. **迁移期双读 / 样例基线（已关闭）**：M1/M2 临时例外已在 M3 关闭；`configs-legacy-baseline.txt` 仅保留注释，`check-no-static-config.sh` 要求零跟踪。
2. **紧急热修**：仅限故障止血；24h 内补齐 key spec / Validate / 审计，并轮换可能泄露的凭据。
3. **配置中心故障**：使用本地缓存；无缓存则 fail-fast，禁止静默用代码默认值冒充已配置。
4. **密钥轮换窗口**：允许旧版本 grace；超过 grace 仍未切换的调用方须登记整改。
5. **本地调试文件**：允许未跟踪的 `configs/**/*.yaml` / `.env` 仅用于本机；不得 `git add`，门禁拒绝入库。

---

## 11. Review 清单

- [ ] 新配置键已在代码 key spec 声明（类型 / 默认值 / 校验 / owner / reloadable / expires_at）
- [ ] 密钥字段已声明 `secret_rule`；无规则字段未被自动生成
- [ ] 未新增 `configs/` 文件或环境变量配置覆盖
- [ ] `Validate` 覆盖互斥与生产约束；初始化幂等用例通过
- [ ] 无真实密钥入库；日志 / 快照 / 前端无明文
- [ ] `make check-configcenter` / `check-no-static-config` / `make secrets` / `make test` 通过

---

## 12. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| 代码 key spec | 配置键唯一来源（类型 / 默认值 / 校验 / reloadable / secret_rule） |
| `internal/configcenter` | 配置中心域：Item / Version / Release / Pointer / Secret |
| `pkg/config` | 通用 Loader：代码默认值 + 配置中心快照 + normalize + Validate |
| `pkg/configclient` | HTTP 拉取、本地缓存、长轮询、Watch |
| `cmd/config-api` | 内部密钥解析（mTLS / SPIFFE + grant + 租约） |
| `cmd/internal/appconfig` | 组合加载与聚合校验 |
| `scripts/ci/check-configcenter.sh` | key spec / 权限 / 审计 / OpenAPI / 迁移成对 |
| `scripts/ci/check-no-static-config.sh` | CFG-01 防回退 |
| gitleaks | CFG-06 密钥扫描 |
| 本文件 | 规范正文 |
