# 工程化 CI 流水线规范（硬约束）

> **状态**：生效中（Normative）  
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**  
> **适用范围**：本仓库全部合入路径（PR → `main` / 默认保护分支）  
> **执行方式**：见下文「多平台」；本地统一 `make ci`  
> **最后更新**：2026-10-13（纳入 LIC/GEN/PERF；P0～P4 专文门禁齐套）  
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

**安全门禁（SECURITY_STANDARDS，与上表一并必跑）：** job/step `secrets`（gitleaks + 调试端点，SEC-01/SEC-08）；`lint` 内启用 gosec（SEC-09）。

**API 门禁（API_STANDARDS，与上表一并必跑）：** job/step `openapi`（oasdiff validate + 分端 breaking，API-01/07/08）。

**测试门禁（TESTING_STANDARDS，并入 job `test`）：** `-race` + `coverage.out` + `scripts/ci/check-test-gates.sh`（domain 覆盖率 ≥70%、B/C 必测、禁 test 步注入 secrets）。

**依赖门禁（DEPENDENCY_STANDARDS，并入 job `vuln`）：** `govulncheck` + `scripts/ci/check-deps.sh`（go.sum、replace、许可证）。

**配置门禁（CONFIG_STANDARDS，并入 job `secrets`）：** `scripts/ci/check-config.sh`（example 齐全、禁跟踪本地 yaml、DSN 策略）+ gitleaks。

**数据 / 可靠性 / 可观测门禁（DATA / REL / OBS，并入 job `arch`）：** `check-migrations.sh` + `check-reliability.sh` + `check-observability.sh`。

**Git / 发布 / Runbook / 审计门禁（GIT / RELS / RB / AUD，并入 job `arch`）：** `check-git.sh` + `check-release.sh` + `check-runbooks.sh` + `check-audit.sh`。

**许可证 / 代码生成 / 性能门禁（LIC / GEN / PERF，并入 job `arch`）：** `check-license.sh` + `check-codegen.sh` + `check-perf.sh`（微基准抽检；全链路压测仍可选）。

**尚未纳入必跑、但已规划**：集成测试容器常态化、发版流水线镜像扫描推送。启用后须更新本表与各平台流水线。

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
| SEC-01/08 + CFG-01/02/06 | `secrets` | `secrets` | `make secrets` + `make check-debug` + `make check-config` |
| API-01/07/08 | `openapi` | `openapi` | `make openapi` |
| CI-08 | `build` | `build` | `make build` |

变更任一平台的检查命令时，**必须同 MR** 更新：另一平台 YAML、`gitee-job.sh`（若改逻辑）、`Makefile`（若改本地），并更新本表。

### 3.2 GitHub Actions

- 触发：`push` / `pull_request` → `main`、`master`
- 并行 job + `concurrency` 取消旧 run
- `permissions: contents: read`
- Go 版本：`actions/setup-go` 读取 `go.mod`

### 3.3 Gitee Go

- 目录约定：仓库根目录 `.workflow/`（开通 Gitee Go 后由平台识别）
- 触发：
  - `PRPipeline.yml`：PR 目标为 `main`/`master`
  - `MasterPipeline.yml`：push 到 `main`/`master`
  - `BranchPipeline.yml`：其它分支 push（排除主干，避免重复）
- 插件：`build@golang`；阶段 `strategy: fast`（一失败则快速失败）
- 代理：默认 `GOPROXY=https://goproxy.cn,direct`
- **Go 版本**：YAML 中 `GO_VERSION` / `golangVersion` 须改为控制台**当前可用最高版本**，且须满足 `go.mod`。若云端插件版本低于 `go.mod`：
  1. 优先使用企业自建构建环境 / Agent，安装匹配 Go 后执行 `make ci`；或
  2. 升级组织提供的 Golang 构建镜像；  
  **禁止**为迁就过旧插件而擅自降低 `go.mod` 语言版本（除非 ADR 批准）。

开通清单：

1. 仓库启用 Gitee Go  
2. 确认 `.workflow` 三个文件生效（或控制台从代码库导入）  
3. 调整 `golangVersion`  
4. 开启「合并前流水线通过」类保护（名称以 Gitee 控制台为准）

详见 [`.workflow/README.md`](../.workflow/README.md)。

### 3.4 双托管 / 仅单边

| 场景 | 要求 |
|------|------|
| 仅 GitHub | 维护 `.github/workflows`；`.workflow` 可保留作模板，但须在 README 标明未启用 |
| 仅 Gitee | 维护 `.workflow`；分支保护走 Gitee；`.github` 可保留供镜像仓使用 |
| 双远端 | 两边流水线均须绿；禁止「只过一边就合入」 |

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
                    └── build (all cmd)
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
make check-config         # CFG-01/02/06
make check-migrations     # DATA-01/02/03
make check-reliability    # REL-01/02/05
make check-observability  # OBS-01/02/04
make check-git            # GIT-05/06
make check-release        # RELS-01/02/03/07
make check-runbooks       # RB-01～04
make check-audit          # AUD-01/02/04
make check-license        # LIC-01/02/06
make check-codegen        # GEN-01/02
make check-perf           # PERF-01/02
make build                # CI-08
```

或一键：

```bash
make ci
```

`make ci` 失败时**禁止**依赖「远端 CI 会过」而强行合入。

---

## 6. 分支保护与仓库设置（MUST，人工一次配置）

### GitHub

1. 保护 `main`（或默认分支）  
2. Require a pull request before merging  
3. Require status checks：勾选 `CI` workflow 全部必跑 job  
4. 限制无检查强推；应急绕过须事后补跑  

### Gitee

1. 保护默认分支 / 设置合并规则  
2. 要求关联 PR 流水线（`PR CI` / `pr-pipeline`）通过后方可合并  
3. 限制管理员绕过；应急流程同 GitHub  

未完成分支保护前，流水线文件合入仍视为「门禁未闭环」。

---

## 7. 失败策略

| 场景 | 策略 |
|------|------|
| fmt / tidy diff | 本地 `make fmt` / `go mod tidy` 后提交 |
| lint | 按 `.golangci.yml` 与注释/风格专文修复；例外走 `nolint`+原因 |
| test / race | 修复或补测；禁止无 issue 的 `t.Skip` 掩盖必测场景 |
| vuln | 升级依赖或官方缓解；否则 ADR + 风险接受后方可临时豁免 |
| arch 跨端引用 | 删除错误 import；端能力只通过 app/domain 共享 |
| 平台 / 插件故障 | 可重跑；连续失败升级 Owner，不降低检查标准 |
| Gitee Go 版本不足 | 换自建 Agent 或升级构建镜像，见 §3.3 |

---

## 8. 缓存、性能与成本

- GitHub：`setup-go` cache；golangci-lint-action 缓存  
- Gitee：依赖平台构建缓存；配置 `GOPROXY` 加速模块下载  
- 不在每个 PR 默认构建并推送镜像（见后续 `RELEASE_STANDARDS`）  
- 不可用路径过滤跳过 fmt/lint/test/vuln  

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
- [ ] 已更新本专文映射表（若有增删 job）  

---

## 12. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| `.github/workflows/ci.yml` | GitHub 强制执行 |
| `.workflow/*.yml` | Gitee Go 强制执行 |
| `scripts/ci/gitee-job.sh` | Gitee 与本地可复用的 job 实现 |
| `scripts/ci/check-transport-imports.sh` | CI-09 |
| `scripts/ci/check-openapi.sh` | API-01/07/08 |
| `scripts/ci/run-tests.sh` + `check-test-gates.sh` | TEST-01～05 |
| `scripts/ci/check-deps.sh` | DEP-01/03/05 |
| `scripts/ci/check-config.sh` | CFG-01/02/06 |
| `scripts/ci/check-migrations.sh` | DATA-01/02/03 |
| `scripts/ci/check-reliability.sh` | REL-01/02/05 |
| `scripts/ci/check-observability.sh` | OBS-01/02/04 |
| `scripts/ci/check-git.sh` | GIT-05/06 |
| `scripts/ci/check-release.sh` | RELS-01/02/03/07 |
| `scripts/ci/check-runbooks.sh` | RB-01～04 |
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
