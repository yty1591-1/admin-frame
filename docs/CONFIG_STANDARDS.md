# 工程化配置与多环境规范（硬约束）

> **状态**：生效中（Normative）  
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**  
> **适用范围**：本仓库配置样例、加载逻辑、环境变量与多环境约定  
> **关联**：架构 §十七；密钥见 `SECURITY_STANDARDS.md`；CI 见 `CI_STANDARDS.md`  
> **执行方式**：`Validate()` 单测 + 配置样例门禁 + gitleaks + Review  
> **最后更新**：2026-10-13  
> **变更方式**：修改本规范须同步更新 `internal/platform/config.go`、`scripts/ci/check-config.sh`、`configs/*.example` 与架构 §十七，经架构 Owner Review

---

## 1. 目标

1. **启动即安全**：非法配置、互斥项、生产演示模式在启动时失败，不带病运行。  
2. **密钥不入库**：仓库只保留 example；真实密钥来自环境变量 / 密钥管理系统。  
3. **优先级清晰**：环境变量覆盖文件，行为可预期、可测。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| CFG-01 | 各业务进程必须有 `configs/<name>.yaml.example`（admin / biz / c / worker） | `check-config.sh` |
| CFG-02 | Git 只跟踪 `*.yaml.example`；本地 `configs/*.yaml` 由 `.gitignore` 忽略，禁止提交真实密钥配置 | `check-config.sh` + `.gitignore` |
| CFG-03 | `LoadConfig` 后必须调用 `Validate()`；失败则进程非零退出 | 代码 + 单测 |
| CFG-04 | 配置优先级：**环境变量 > 配置文件 > 默认值**（配置中心若引入须插在环境变量与文件之间，并更新本表） | `applyEnv` + 单测 |
| CFG-05 | `env=prod`（或等价）时 `auth.demo_mode` 必须为 false；`auth.expected_channel` 必须与 `channel` 一致 | `Validate` + 单测 |
| CFG-06 | example 中 `db.dsn` 必须为空或明显占位；禁止真实口令 / 生产主机 | `check-config.sh` + gitleaks |
| CFG-07 | 禁止在业务代码硬编码监听地址、超时、功能开关（应用配置或常量默认值经 `applyDefaults`） | Review |
| CFG-08 | 合并请求须勾选配置自检 | PR 模板 |

---

## 3. 文件与命名

| 文件 | 用途 |
|------|------|
| `configs/admin.yaml.example` | A 端样例 |
| `configs/biz.yaml.example` | B 端样例 |
| `configs/c.yaml.example` | C 端样例 |
| `configs/worker.yaml.example` | worker 样例 |
| `configs/*.yaml`（本地） | 开发者从 example 复制；**不入库** |

本地：

```bash
cp configs/admin.yaml.example configs/admin.yaml
# 按需修改；勿提交
```

---

## 4. 环境变量覆盖

前缀：`ADMIN_FRAME_`。在 YAML 加载与默认值之后、`Validate` 之前应用。

| 变量 | 覆盖字段 |
|------|----------|
| `ADMIN_FRAME_ENV` | `env`（local / dev / test / staging / prod） |
| `ADMIN_FRAME_SERVICE` | `service` |
| `ADMIN_FRAME_CHANNEL` | `channel` |
| `ADMIN_FRAME_ADDR` | `addr` |
| `ADMIN_FRAME_SHUTDOWN_TIMEOUT` | `shutdown_timeout`（Go duration，如 `15s`） |
| `ADMIN_FRAME_LOG_LEVEL` | `log.level` |
| `ADMIN_FRAME_LOG_FORMAT` | `log.format` |
| `ADMIN_FRAME_AUTH_DEMO_MODE` | `auth.demo_mode`（`true`/`false`/`1`/`0`） |
| `ADMIN_FRAME_AUTH_EXPECTED_CHANNEL` | `auth.expected_channel` |
| `ADMIN_FRAME_DB_DRIVER` | `db.driver` |
| `ADMIN_FRAME_DB_DSN` | `db.dsn` |
| `ADMIN_FRAME_STORAGE_DRIVER` | `storage.driver`（memory/local/s3/minio/oss/cos/obs） |
| `ADMIN_FRAME_STORAGE_KEY_PREFIX` | `storage.key_prefix` |
| `ADMIN_FRAME_STORAGE_LOCAL_ROOT` | `storage.local.root` |
| `ADMIN_FRAME_STORAGE_S3_ENDPOINT` | `storage.s3.endpoint` |
| `ADMIN_FRAME_STORAGE_S3_REGION` | `storage.s3.region` |
| `ADMIN_FRAME_STORAGE_S3_BUCKET` | `storage.s3.bucket` |
| `ADMIN_FRAME_STORAGE_ACCESS_KEY` | `storage.s3.access_key` |
| `ADMIN_FRAME_STORAGE_SECRET_KEY` | `storage.s3.secret_key` |
| `ADMIN_FRAME_STORAGE_S3_FORCE_PATH_STYLE` | `storage.s3.force_path_style` |
| `ADMIN_FRAME_WORKER_POLL_INTERVAL` | `worker.poll_interval` |
| `ADMIN_FRAME_TRACING_ENABLED` | `tracing.enabled` |
| `ADMIN_FRAME_TRACING_EXPORTER` | `tracing.exporter`（none / otlp / stdout / noop） |
| `ADMIN_FRAME_TRACING_ENDPOINT` | `tracing.endpoint` |
| `ADMIN_FRAME_TRACING_INSECURE` | `tracing.insecure` |
| `ADMIN_FRAME_TRACING_SAMPLE_RATIO` | `tracing.sample_ratio` |

生产密钥、DSN **优先**用环境变量注入，不写进文件或镜像层。

---

## 5. Validate 基线

`platform.Config.Validate()` 至少校验：

- `service`、`channel`、`addr` 非空  
- `channel` ∈ {admin, biz, c, worker}  
- `auth.expected_channel` 与 `channel` 一致（缺省时由默认值填齐）  
- `db.driver` ∈ {memory, mysql, postgres}；非 memory 时 `db.dsn` 必填  
- `storage.driver` ∈ {memory, local, s3, minio, oss, cos, obs}；`local` 时 `storage.local.root` 必填；`env=prod` 禁止 `memory`  
- `log.level` ∈ {debug, info, warn, error}  
- `env` 为空或 ∈ {local, dev, test, staging, prod}  
- `env=prod` ⇒ `auth.demo_mode=false`  
- `tracing.exporter` ∈ {none, otlp, stdout, noop}；`otlp` 时 `endpoint` 必填；`env=prod` 禁止 `stdout`  

鉴权装配另见 SEC-04：`demo_mode=false` 且无生产 Authenticator 时拒绝启动。

---

## 6. 多环境

| 环境 | `env` | 要求 |
|------|-------|------|
| local | local | 可用 `demo_mode=true`、memory DB |
| dev / test | dev / test | 可销毁数据；禁止生产凭据 |
| staging | staging | 拓扑近似生产；密钥隔离 |
| prod | prod | `demo_mode=false`；密钥走 KMS/环境变量；审计开启 |

---

## 7. MUST NOT

- 将含真实 DSN / 云密钥的 `configs/*.yaml` 提交入库。  
- 跳过 `Validate` 直接使用配置。  
- 在 prod 打开 `auth.demo_mode`。  
- 用业务代码魔法数替代可配置超时 / 地址（演示常量除外且不得用于生产路径）。  

---

## 8. 本地与 CI

```bash
make check-config   # CFG-01/02/06
make test           # 含 config Validate / env 单测
make secrets        # gitleaks（密钥）
make ci
```

| 平台 | 入口 |
|------|------|
| GitHub / Gitee | step/job 纳入 `config`（或与 `secrets` 同批） |
| MR | PR 模板「配置自检」 |

---

## 9. 例外

1. **临时本地 yaml 需分享结构**：只更新 `.example`，不共享含密钥的副本。  
2. **配置中心**：引入时写 ADR，并更新本专文优先级表与加载代码。  
3. **紧急热修**：24h 内补齐 `Validate` 与 example，轮换任何可能泄露的凭据。

---

## 10. Review 清单

- [ ] example 已同步；无真实密钥入库  
- [ ] 新配置项有默认值或 Validate；prod 相关互斥已覆盖  
- [ ] 环境变量名已写入本专文或 example 注释  
- [ ] `make check-config` 与相关单测通过  

---

## 11. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| `configs/*.yaml.example` | 入库样例 |
| `.gitignore`（`configs/*.yaml`） | CFG-02 |
| `internal/platform/config.go` | 加载 / env / Validate |
| `scripts/ci/check-config.sh` | CFG-01/02/06 |
| gitleaks | 密钥扫描 |
| 本文件 | 规范正文 |
