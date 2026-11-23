# 工程化 CI 流水线规范（硬约束）

> **状态**：生效中（Normative）
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**
> **适用范围**：本仓库全部合入路径（PR → `main` / 默认保护分支）
> **执行方式**：见下文「多平台」；本地统一 `make ci`
> **最后更新**：2026-11-23（CI-18 增补 apps 冒烟与覆盖率门槛步骤；见 FE-G-06 与 `scripts/ci/check-frontend-gates.sh`）
> **变更方式**：修改本规范须同步更新各平台流水线文件与 `ARCHITECTURE.md` §18.3，经架构 Owner Review

---

## 1. 目标

1. **可强制**：格式、静态分析、测试、漏洞扫描在合并前自动阻断。
2. **可复现**：本地 `make` 目标与 CI 步骤一一对应，避免「只在我机器过」。
3. **平台无关门禁**：GitHub / Gitee 语法不同，但 **CI-03～CI-09 检查项必须一致**。
4. **最小权限**：流水线默认只读仓库；不写 secrets 到日志。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| CI-01 | 默认保护分支的 PR **必须**通过本仓库 CI（全部必跑 job/步骤）方可合并 | 分支保护 + 平台流水线 |
| CI-02 | 每次 PR 与 push 到 `main`/`master` **必须**触发 CI；禁止跳过必跑项（见例外） | 触发器配置 |
| CI-03 | 必跑：格式检查（`gofmt`/`goimports` 无 diff） | job `fmt` |
| CI-04 | 必跑：`golangci-lint`（与本地 `.golangci.yml` 一致） | job `lint` |
| CI-05 | 必跑：单元测试（含 `-race`）且失败即失败 | job `test` |
| CI-06 | 必跑：`govulncheck ./...`；已知高危未缓解则阻断 | job `vuln` |
| CI-07 | 必跑：`go mod tidy` 后 `go.mod`/`go.sum` 无未提交 diff | job `mod` |
| CI-08 | 必跑：全量 `go build` 所有 `cmd/*` | job `build` |
| CI-09 | 必跑：禁止跨端 transport 互引（admin/biz/c） | job `arch` |
| CI-10 | 凭证与权限最小化；写权限按需显式授予；禁向日志打印 token | 流水线审查 |
| CI-11 | 同一 PR 的新推送应取消或取代进行中的旧构建（平台支持时必须开启） | concurrency / 等价配置 |
| CI-12 | 禁止在 CI 中使用生产密钥 / 生产数据源；测试仅用 fixture 或 ephemeral | Review + 本规范 |
| CI-13 | 工具与镜像版本**钉扎**；禁止无说明的浮动 `latest` | 流水线 YAML |
| CI-14 | Job/阶段必须有超时或平台默认超时；禁止无限跑 | 流水线配置 |
| CI-15 | 门禁脚本自身引用的仓库内路径必须存在；文件迁移/重命名须同步 `scripts/ci/*.sh`（路径失效会让断言直接报错或以「grep 无匹配」静默放行） | `make check-ci-paths` |
| CI-16 | 每个 workflow job 必须有本地可执行入口；新增/改名 job 须同 MR 在 `check-ci-parity.sh` 登记，指向的 make 目标必须存在（防「本地没有这个 job 的入口」导致长期绿红不一） | `make check-ci-parity` |
| CI-17 | 本地对齐必须可「判定等价」：门禁因缺依赖而跳过时不得静默视为通过；`make ci-strict` / `make ci-full` 下未登记理由的跳过即失败 | `make ci-strict` / `make ci-full` |
| CI-18 | 前端（`frontend/`）改动**必须**通过前端必跑门禁：`pnpm audit --prod --audit-level=high`（npmjs registry）、`pnpm lint`、`check:cspell`、`check:dep`、`check:circular`、`check:type`、`test:unit`（根 + web-admin + web-biz，apps 覆盖率阈值 30%）、`check-frontend-gates.sh`、`build:admin`、`build:biz`，任一步非 0 即阻断；job 不设 paths 过滤（required check 被 skip 等于门禁失效） | job `frontend` / Gitee step `frontend` / `make frontend-check` |

**安全门禁（SECURITY_STANDARDS，与上表一并必跑）：** job/step `secrets`（gitleaks + 调试端点，SEC-01/SEC-08）；`lint` 内启用 gosec（SEC-09）。

**API 门禁（API_STANDARDS，与上表一并必跑）：** job/step `openapi`（oasdiff validate + 分端 breaking，API-01/07/08）。

**测试门禁（TESTING_STANDARDS，并入 job `test`）：** `-race` + `coverage.out` + `scripts/ci/check-test-gates.sh`（domain 覆盖率 ≥70%、B/C 必测、禁 test 步注入 secrets）。

**依赖门禁（DEPENDENCY_STANDARDS，并入 job `vuln`）：** `govulncheck` + `scripts/ci/check-deps.sh`（go.sum、replace、许可证）。

**配置门禁（CONFIG_STANDARDS，并入 job `secrets`）：** `scripts/ci/check-no-static-config.sh`（禁止跟踪运行时静态配置、样例只减不增、DSN 占位符策略）+ gitleaks。

**数据 / 可靠性 / 可观测门禁（DATA / REL / OBS，并入 job `arch`）：** `check-migrations.sh` + `check-reliability.sh` + `check-observability.sh`。

**Git / 发布 / Runbook / 审计门禁（GIT / RELS / RB / AUD，并入 job `arch`）：** `check-git.sh` + `check-release.sh` + `check-runbooks.sh` + `check-oncall.sh`（ENT-18：分级矩阵 / P1 路由 / 复盘模板 / 行动项 / 演练 / 交接）+ `check-audit.sh`。

**许可证 / 代码生成 / 性能门禁（LIC / GEN / PERF，并入 job `arch`）：** `check-license.sh` + `check-codegen.sh` + `check-perf.sh`（微基准抽检）+ `check-load-baseline.sh`（ENT-16：压测登记表 ↔ 执行器 ↔ 产物对账，**不重跑压测**；全链路压测本身按 PERF-06 仍为手动 / nightly）。

**发布流水线（ENT-17，`.github/workflows/release.yml`）**：tag `v*` 或手动触发；`preflight` 先跑迁移门禁（`check-migration-gate.sh` 的 expand-only / append-only / dirty 检查），`staging` 与 `production` 均为 GitHub Environment（required reviewers），金丝雀阶段由 `release-canary-gate.sh` 校验流量下限与 5xx/p95，失败由 `rollback` job 自动卸载金丝雀并 `helm rollback`。缺凭据时显式失败，`RELEASE_ENABLED` 未开启时由 `notice` job 写明未部署原因。

**发版镜像（不在 PR 必跑，见 §3.5）**：`image` job 在 push `main`/`master` 与 `v*` tag 时构建多架构镜像、推送并用 Trivy 扫描；命中 `CRITICAL/HIGH` 即失败，该 digest 不得上生产。

**部署制品门禁（ENT-05/ENT-06）**：job `deploy` 安装 Helm + promtool，执行 `check-deploy.sh`（`helm lint`、三套 values `helm template`、明文密钥与 digest 断言）与 `check-alerts.sh`（告警目录 ↔ 规则 ↔ runbook ↔ 看板一致性）。

**尚未纳入必跑、但已规划**：集成测试容器常态化。启用后须更新本表与各平台流水线。

---

## 3. 多平台（GitHub / Gitee）

> **重要**：`.github/workflows/*.yml` **不会**被 Gitee 执行；`.workflow/*.yml` **不会**被 GitHub 执行。二者并存，检查项对齐。

| 平台 | 配置位置 | 引擎 | 命令入口 |
|------|----------|------|----------|
| **GitHub** | [`.github/workflows/ci.yml`](../.github/workflows/ci.yml) | GitHub Actions | 各 job 内联 / Action |
| **Gitee** | [`.workflow/`](../.workflow/)（`PRPipeline.yml` / `MasterPipeline.yml` / `BranchPipeline.yml`） | Gitee Go | [`scripts/ci/gitee-job.sh`](../scripts/ci/gitee-job.sh) |
| **本地** | `Makefile` | 开发者机器 | `make ci` |

### 3.1 检查项映射（必须保持同步）

| 检查 | GitHub job | Gitee step `name` | 本地目标 |
|------|------------|-------------------|----------|
| CI-03 | `fmt` | `fmt` | `make fmt-check` |
| CI-07 | `mod` | `mod` | `make tidy-check` |
| CI-04 | `lint` | `lint` | `make lint` |
| CI-05 + TEST-01～05 | `test` | `test` | `make test-ci` |
| CI-06 + DEP-01/03～05 | `vuln` | `vuln` | `make vuln` + `make check-deps` |
| CI-09 + DATA…AUD + LIC/GEN/PERF | `arch` | `arch` | `make check-transport` + 各 standards 检查脚本（见 gitee-job arch） |
| SEC-01/08 + CFG-01/02/06 | `secrets` | `secrets` | `make secrets` + `make check-debug` + `make check-no-static-config` |
| API-01/07/08 | `openapi` | `openapi` | `make openapi` |
| API 契约产物（§3.2，非合并门禁） | `openapi-docs` | —（Gitee 待接入） | `make openapi-artifacts` |
| CI-08 | `build` | `build` | `make build` |
| CI-18 + FE-25～28 | `frontend` | `frontend` | `make frontend-check` |
| CI-15 / CI-16 | 并入 `arch` | `arch` | `make check-ci-paths` + `make check-ci-parity` |
| RELS-01/03/06 + 镜像产物 | `image`（非合并门禁，见 §3.5） | —（Gitee 待接入） | `make image-scan`（构建 + Trivy）+ `make docker-push` + `make check-release` |
| CI-17（本地判定等价） | —（本地专用） | — | `make ci-strict` / `make ci-full` |

变更任一平台的检查命令时，**必须同 MR** 更新：另一平台 YAML、`gitee-job.sh`（若改逻辑）、`Makefile`（若改本地），并更新本表。

**服务依赖（CI-05 补充）**：`tests/integration/iam` 需要 MySQL 8.0。GitHub Actions 的 `test` job 已声明 `services.mysql`（`mysql:8.0` + health check）并注入 `IAM_TEST_MYSQL_DSN` / `TENANT_TEST_MYSQL_DSN`（两者指向同一 service 与库）；Gitee Go 无等价 service 声明，需在控制台配置同名环境变量后启用，未配置时用例显式跳过（非静默通过）。详见 `docs/rbac-iam.md` §13.6。

**并发约束（CI-05 补充）**：注入 DSN 后 `go test ./...` 会并行执行多个集成包，而每个包启动时都会 `reset` 清表，**并行必然互踩**（表被清空、固定 bootstrap 账号争抢唯一键），失败与被测代码无关。`scripts/ci/run-tests.sh` 因此固定 `-p 1` 串行执行包，与 `run-integration-tests.sh` 的既有约定一致；新增集成包沿用同一前提，不得依赖「runner 核数少所以不撞」。

**工具依赖（CI-05 补充）**：`check-oncall.sh` 的 OC-03 通过 `scripts/ops/paging_probe.py` 解析 Alertmanager 路由，该脚本依赖 PyYAML。版本钉在 `scripts/ci/requirements.txt`（`pyyaml==6.0.3`），GitHub `arch` job 在运行门禁前安装该文件。本地首次运行前执行一次 `make ci-deps`（优先创建仓库内 `.venv`，避免污染受 PEP 668 管理的系统 Python）；`check-oncall.sh` 会优先使用 `.venv` 解释器，并在缺少 PyYAML 时直接提示修复命令，不再以笼统的汇总结论失败。Gitee Go 的 `build@golang` 镜像未预装 PyYAML，若启用 `arch` step 须显式执行 `make ci-deps`（或等效安装），否则 OC-03 报 `ModuleNotFoundError: No module named 'yaml'`。

### 3.2 GitHub Actions

- 触发：`push` / `pull_request` → `main`、`master`
- 并行 job + `concurrency` 取消旧 run
- `permissions: contents: read`
- Go 版本：`actions/setup-go` 读取 `go.mod`
- `arch` job 使用 runner 系统 Python；运行前钉扎安装 `pyyaml==6.0.3`，供 `check-oncall.sh`（OC-03）的 `paging_probe.py` 解析 Alertmanager 路由
- `openapi-docs` job（非合并门禁）：用 Node 20 + `make openapi-artifacts` 产出文档 / TS 类型 / changelog，并上传 `openapi-artifacts`；契约门禁仍由 `openapi` job 负责
- `frontend` job（CI-18 / FE-CI-01）：`pnpm/action-setup@v4` 读取 `frontend/package.json` 的 `packageManager`，`setup-node@v4` 用 `frontend/.node-version`（24.16.0）与 pnpm cache（键 `frontend/pnpm-lock.yaml`），执行 `bash scripts/ci/frontend-check.sh`（10 步，第 2 步为 `pnpm audit`）；与 `make frontend-check`、Gitee step 同源，禁止三处各写一套命令

### 3.3 Gitee Go

- 目录约定：仓库根目录 `.workflow/`（开通 Gitee Go 后由平台识别）
- 触发：
  - `PRPipeline.yml`：PR 目标为 `main`/`master`
  - `MasterPipeline.yml`：push 到 `main`/`master`
  - `BranchPipeline.yml`：其它分支 push（排除主干，避免重复）
- 插件：`build@golang`（后端 job）+ `build@nodejs`（`frontend` step，`nodeVersion` 以控制台可用列表为准，pnpm 由 corepack 按 `packageManager` 固定为 11.16.0）；阶段 `strategy: fast`（一失败则快速失败）
- 代理：默认 `GOPROXY=https://goproxy.cn,direct`
- **Go 版本**：YAML 中 `GO_VERSION` / `golangVersion` 须改为控制台**当前可用最高版本**，且须满足 `go.mod`。若云端插件版本低于 `go.mod`：
  1. 优先使用企业自建构建环境 / Agent，安装匹配 Go 后执行 `make ci`；或
  2. 升级组织提供的 Golang 构建镜像；
  **禁止**为迁就过旧插件而擅自降低 `go.mod` 语言版本（除非 ADR 批准）。

开通清单：

1. 仓库启用 Gitee Go
2. 确认 `.workflow` 三个文件生效（或控制台从代码库导入）
3. 调整 `golangVersion`
4. 确认 Node 构建插件可用，并把 `frontend` step 的 `nodeVersion` 调成控制台实际提供的版本（目标 24.x）
5. 开启「合并前流水线通过」类保护（名称以 Gitee 控制台为准）

详见 [`.workflow/README.md`](../.workflow/README.md)。

### 3.4 双托管 / 仅单边

| 场景 | 要求 |
|------|------|
| 仅 GitHub | 维护 `.github/workflows`；`.workflow` 可保留作模板，但须在 README 标明未启用 |
| 仅 Gitee | 维护 `.workflow`；分支保护走 Gitee；`.github` 可保留供镜像仓使用 |
| 双远端 | 两边流水线均须绿；禁止「只过一边就合入」 |

### 3.5 发版镜像流水线（`.github/workflows/image.yml`）

| 项 | 约定 |
|----|------|
| 触发 | push `main`/`master`、push `v*` tag、手动 `workflow_dispatch`；**不在 PR 触发** |
| 注册表 | 默认 `ghcr.io`（`GITHUB_TOKEN` 推送）；私有仓库设 Repository variable `IMAGE_REGISTRY` 并另外注入凭据 |
| 镜像名 | `<registry>/<owner>/<cmd>`（admin-api / biz-api / c-api / worker 各自独立，RELS-05） |
| 标签 | 分支：`sha-<short>`；tag：`<semver>` 与 `<semver>-sha-<short>`；**禁止** `latest`（RELS-01） |
| 架构 | `linux/amd64,linux/arm64`；后端 `Dockerfile` 固定 `BUILDPLATFORM` 后按 `TARGETARCH` 交叉编译 |
| 版本 | 后端：`VERSION`/`COMMIT`/`BUILD_TIME` 经 ldflags 注入，`/version` 可核对（RELS-03） |
| 扫描 | 推送后 Trivy `CRITICAL,HIGH`（`ignore-unfixed`）命中即 job 失败（RELS-06 / §6）；含 OS 包与 Go 二进制 |
| SBOM | 按推送后的 **digest** 生成 CycloneDX，作为 `sbom-<cmd>` artifact 上传（缺文件即失败）；同一 digest 上以 `cosign attest --type cyclonedx` 附加（RELS-13） |
| 签名 | `cosign sign`（keyless，`id-token: write`）签 **digest**，不签 tag；cosign 版本经 `COSIGN_VERSION` 钉扎（RELS-13） |
| 验签 | 不在本流水线：由 `release.yml` 的 `verify` job 在部署前完成（RELS-14）；本流水线只负责「签得对」 |
| 基础镜像 | 后端运行镜像 OS 必须在支持期内（当前 `alpine:3.24` 起） |
| 并发 | 不取消进行中的发布构建，避免「一半已推送」的不可追溯状态（与 CI-11 的区别在此：发布不适用取消） |

- 镜像推送**不是**合并门禁；但 `arch` job 内的 `check-release.sh` 会静态校验该文件（无 `:latest`、已禁用隐式 `latest`、扫描与版本注入齐备），`check-sbom.sh` 校验 SBOM/签名/验签结构（SB-01～SB-11）。
- 供应链门禁是**静态**的：`make check-sbom` 不联网、不需要 cosign，因此可在合并门禁里跑；真实密码学链路由 `release.yml` 首跑与 `scripts/ci/supply-chain-rehearsal.sh` 覆盖。
- Gitee 端暂未接入（需组织 registry 凭据）；接入前镜像只由 GitHub 流水线产出，Gitee 只跑质量门禁。

---

## 4. 流水线拓扑

```text
                    ┌── fmt
                    ├── mod
PR / push main ──►  ├── lint
                    ├── test  (+ race, coverage, TEST gates)
                    ├── vuln  (+ deps / licenses)
                    ├── arch  (cross-transport)
                    ├── secrets (gitleaks + debug + config examples)
                    ├── openapi (validate + breaking)
                    ├── build (all cmd)
                    └── frontend (lint/type/unit/build；CI-18 / FE-CI-01)
```

```text
push main / v* tag ──► image (buildx 多架构 + Trivy 扫描；非合并门禁)
```

- 各检查 **并行**（平台支持时）；任一失败 → 合并阻断。
- Gitee：同 stage 下多 step 无 `dependsOn` 即并行；`strategy: fast`。

---

## 5. 本地对齐（MUST）

```bash
make fmt-check       # CI-03
make tidy-check      # CI-07
make lint            # CI-04
make test-ci         # CI-05
make vuln            # CI-06
make check-transport # CI-09
make openapi         # API-01/07/08
make test-ci         # CI-05 + TEST 门禁
make vuln            # CI-06 / DEP-04
make check-deps      # DEP-01/03/05
make check-no-static-config    # CFG-01/06（禁止新增静态配置）
make check-migrations     # DATA-01/02/03
make check-reliability    # REL-01/02/05
make check-observability  # OBS-01/02/04
make check-git            # GIT-05/06
make check-release        # RELS-01/02/03/07
make check-runbooks       # RB-01～04
make check-oncall         # RB-08/09（ENT-18）
make check-audit          # AUD-01/02/04
make check-license        # LIC-01/02/06
make check-codegen        # GEN-01/02
make check-perf           # PERF-01/02
make build                # CI-08
make docker-push VERSION=<semver|sha>   # 发版镜像推送（本地复现；多架构见 CI `image`）
make image-scan                        # 本地复现 image job 的 Trivy 扫描（HIGH/CRITICAL）
make frontend-check                    # CI-18 / FE-CI-01：前端 audit/lint/type/unit/build（需 pnpm 11 + Node 24；不进 make ci；含两 app 冒烟与覆盖率）
```

或一键：

```bash
make ci
```

`make ci` 失败时**禁止**依赖「远端 CI 会过」而强行合入。

### 5.1 本地判定等价（CI-17）

`make ci` 是**降级版**：门禁缺前置条件时 SKIP 而非 FAIL（无 MySQL → IAM 总体覆盖率与
persistence 门禁消失；无 CGO → 不带 `-race`；无 promtool/helm → 相应段跳过）。
因此「本地绿」不等于「CI 绿」。按可信度从低到高：

| 命令 | 覆盖 | 用途 |
|------|------|------|
| `make ci` | 快，但允许静默跳过 | 日常迭代 |
| `make ci-strict` | 同上，但把未豁免跳过判为失败，并打印跳过清单 | 提交前快检（需 MySQL DSN，否则按 CI-17 失败） |
| `make ci-full` | `make ci`（严格）+ 真实 MySQL/Redis/MinIO + `tests/integration/...` + `-race` + 镜像 Trivy | 合并前 / 改 CI、依赖、覆盖率相关代码时 |

`make ci-full` 需要 docker + compose v2；`--skip-race` / `--skip-image` 可裁剪耗时。
跳过项必须先在自己的前置条件上补齐，**不得**为了让门禁变绿而在 `scripts/ci/ci-run.sh` 的
`ALLOW` 表里新增「判据类」豁免——该表只接受「本地工具缺失、且 CI 有等价覆盖」的条目。

典型红绿不一致的成因与对策（2026-11-07 实例）：

| 现象 | 成因 | 对策 |
|------|------|------|
| `test` job 报 IAM 总覆盖率不足，本地全绿 | 无 `IAM_TEST_MYSQL_DSN` 时 total/persistence 门禁**不执行** | `make ci-full`，或用 `ci-full.sh` 导出的 DSN 跑 `check-iam-coverage.sh` |
| `image` job 报 HIGH CVE，本地 `make vuln` 绿 | govulncheck 看代码可达性，Trivy 扫二进制模块 + OS 包 | `make image-scan` |
| `integration` job 红，本地 `make ci` 绿 | `tests/integration/...` 不在 `make ci` 里（Tier B） | `make ci-full`，或 `make test-integration` |
| 新增 job 本地没有对应命令 | job 与 make 目标各自演进 | `make check-ci-parity`（CI-16） |

---

## 6. 分支保护与仓库设置（MUST，人工一次配置）

### GitHub

1. 保护 `main`（或默认分支）
2. Require a pull request before merging
3. Require status checks：勾选 `CI` workflow 全部必跑 job（含 `frontend`；该 job 无 paths 过滤，不会因纯后端 PR 被 skip）
4. 限制无检查强推；应急绕过须事后补跑

### Gitee

1. 保护默认分支 / 设置合并规则
2. 要求关联 PR 流水线（`PR CI` / `pr-pipeline`）通过后方可合并
3. 确认质量门禁 stage 内包含 `frontend` step（`build@nodejs`，调用 `gitee-job.sh frontend`）
4. 限制管理员绕过；应急流程同 GitHub

未完成分支保护前，流水线文件合入仍视为「门禁未闭环」。

---

## 7. 失败策略

| 场景 | 策略 |
|------|------|
| fmt / tidy diff | 本地 `make fmt` / `go mod tidy` 后提交 |
| lint | 按 `.golangci.yml` 与注释/风格专文修复；例外走 `nolint`+原因 |
| test / race | 修复或补测；禁止无 issue 的 `t.Skip` 掩盖必测场景 |
| vuln | 升级依赖或官方缓解；否则 ADR + 风险接受后方可临时豁免 |
| image（Trivy 命中） | 升级依赖 / 基础镜像到修复版本（如 grpc 补丁版、`alpine` 支持期内 minor）；**禁止**放宽 severity 或加 ignore 消警；确属不可触达且无修复走 ADR 豁免（含到期日） |
| arch 跨端引用 | 删除错误 import；端能力只通过 app/domain 共享 |
| arch OC-03 报 `ModuleNotFoundError: No module named 'yaml'` | 按 §3.1「工具依赖」安装钉扎版 PyYAML；禁止跳过路由断言 |
| 平台 / 插件故障 | 可重跑；连续失败升级 Owner，不降低检查标准 |
| Gitee Go 版本不足 | 换自建 Agent 或升级构建镜像，见 §3.3 |

---

## 8. 缓存、性能与成本

- GitHub：`setup-go` cache；golangci-lint-action 缓存
- Gitee：依赖平台构建缓存；配置 `GOPROXY` 加速模块下载
- 不在 PR 构建/推送镜像；仅在 push `main`/`master` 与 `v*` tag 触发 `image`（§3.5），避免重复构建与成本
- 前端：`setup-node` 的 pnpm store 缓存，键为 `frontend/pnpm-lock.yaml`；Gitee `build@nodejs` 依赖平台缓存
- 不可用路径过滤跳过 fmt/lint/test/vuln（含 frontend）

---

## 9. 安全（CI 面）

- 不 `echo` secrets；不把平台 Token 打进日志
- Fork/外部 PR 不授予写权限、不暴露组织密钥
- 第三方 Action / 插件钉扎版本，升级看 release notes

---

## 10. 例外与豁免

1. **临时跳过某检查**：禁止对 CI-03～CI-09 使用「失败继续」，除非 Owner 书面批准且设到期日。
2. **紧急热修**：绕过保护后 24h 内补跑全绿，并在变更单注明。
3. **govuln 误报/无修复**：ADR 记录 CVE、影响面、缓解与复查日期。

---

## 11. Review / 变更检查清单

改动 `.github/workflows/**`、`.workflow/**`、`scripts/ci/gitee-job.sh` 或本专文时：

- [ ] CI-03～CI-09 在 **GitHub 与 Gitee**（若都启用）仍失败即失败
- [ ] 两边检查命令与 `make ci` / `gitee-job.sh` 一致
- [ ] 版本仍钉扎；权限未被放宽
- [ ] `image` 相关改动仍满足 RELS（无 `:latest`、扫描保留、版本注入未删减）
- [ ] 已更新本专文映射表（若有增删 job）
- [ ] `make check-ci-parity` 通过：新增/改名 job 已在 `check-ci-parity.sh` 登记本地入口（CI-16）
- [ ] 前端 job / Gitee step / `make frontend-check` 仍同源于 `scripts/ci/frontend-check.sh`（CI-18 / FE-CI-01），且含 `check-frontend-gates.sh`（FE-G-06）与 `pnpm audit`（FE-G-07）
- [ ] 新门禁若依赖外部工具/服务，已说明 `make ci-full` 如何提供，或已在 `ci-run.sh` 的 ALLOW 表写明豁免理由（CI-17）

---

## 12. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| `.github/workflows/ci.yml` | GitHub 强制执行 |
| `.github/workflows/image.yml` | 发版镜像构建 / 推送 / Trivy 扫描（§3.5） |
| `.workflow/*.yml` | Gitee Go 强制执行 |
| `scripts/ci/gitee-job.sh` | Gitee 与本地可复用的 job 实现 |
| `scripts/ci/check-vuln.sh` | `vuln` job：govulncheck + `deps/vuln-allowlist.txt` 受控例外（带到期日） |
| `scripts/ci/check-transport-imports.sh` | CI-09 |
| `scripts/ci/check-ci-paths.sh` | CI-15 |
| `scripts/ci/check-ci-parity.sh` | CI-16：workflow job ↔ 本地入口一致性 |
| `scripts/ci/frontend-check.sh` | CI-18 / FE-CI-01：前端门禁唯一入口（GitHub job、Gitee step、make 共用；10 步含 audit、apps 冒烟与覆盖率） |
| `scripts/ci/check-frontend-gates.sh` | FE-G-06：apps 冒烟用例存在性 + `v-access`/`pageMap` 覆盖断言（由 `frontend-check.sh` 第 9 步调用） |
| `frontend/.node-version` / `frontend/pnpm-lock.yaml` | 前端工具链钉扎（Node 24.16.0 + pnpm 11.16.0） |
| `scripts/ci/ci-run.sh` | CI-17：跳过项汇总与严格模式（被 `make ci-strict` 调用） |
| `scripts/ci/ci-full.sh` | CI-17：本地判定等价（service 容器 + integration + `-race` + 镜像扫描） |
| `scripts/ci/run-race-tests.sh` | 容器内 `-race`（补足本地无 CGO 的降级） |
| `scripts/ci/check-image-scan.sh` | 本地复现 `image` job 的 Trivy 扫描 |
| `scripts/ci/check-openapi.sh` | API-01/07/08 |
| `scripts/ci/run-tests.sh` + `check-test-gates.sh` | TEST-01～05 |
| `scripts/ci/check-deps.sh` | DEP-01/03/05 |
| `scripts/ci/check-no-static-config.sh` | CFG-01/06 |
| `scripts/ci/check-migrations.sh` | DATA-01/02/03 |
| `scripts/ci/check-reliability.sh` | REL-01/02/05 |
| `scripts/ci/check-observability.sh` | OBS-01/02/04 |
| `scripts/ci/check-git.sh` | GIT-05/06 |
| `scripts/ci/check-release.sh` | RELS-01/02/03/07 |
| `scripts/ci/check-runbooks.sh` | RB-01～04 |
| `scripts/ci/check-oncall.sh` | RB-08/09（ENT-18） |
| `scripts/ci/check-audit.sh` | AUD-01/02/04 |
| `scripts/ci/check-license.sh` | LIC-01/02/06 |
| `scripts/ci/check-codegen.sh` | GEN-01/02 |
| `scripts/ci/check-perf.sh` | PERF-01/02 |
| `deps/allowed-licenses.txt` / `forbidden-licenses.txt` / `NOTICE` | 许可证策略 |
| `deps/replace-allowlist.txt` | 依赖 replace |
| `.github/dependabot.yml` | DEP-07 |
| `Makefile`（`ci` / `fmt-check` / …） | 本地对齐 |
| `.golangci.yml` | CI-04 规则集 |
| 本文件 | 规范正文 |

后续 TEST/DEP 等专文启用新检查时，须 **同时** 更新 GitHub workflow、Gitee `.workflow` 与（如需要）`gitee-job.sh`。
