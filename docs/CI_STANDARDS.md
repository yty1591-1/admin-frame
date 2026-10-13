# 工程化 CI 流水线规范（硬约束）

> **状态**：生效中（Normative）  
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**  
> **适用范围**：本仓库全部合入路径（PR → `main` / 默认保护分支）  
> **执行方式**：[`.github/workflows/ci.yml`](../.github/workflows/ci.yml) + 分支保护必过检查  
> **最后更新**：2026-10-13  
> **变更方式**：修改本规范须同步更新 workflow 与 `ARCHITECTURE.md` §18.3，经架构 Owner Review

---

## 1. 目标

1. **可强制**：格式、静态分析、测试、漏洞扫描在合并前自动阻断。
2. **可复现**：本地 `make` 目标与 CI 步骤一一对应，避免「只在我机器过」。
3. **最小权限**：流水线默认只读仓库；不写 secrets 到日志。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| CI-01 | 默认保护分支的 PR **必须**通过名为 `CI` 的 workflow（全部 required job）方可合并 | 分支保护 + Actions |
| CI-02 | 每次 PR 与 push 到 `main` **必须**触发 CI；禁止跳过必跑 job（见例外） | workflow `on` |
| CI-03 | 必跑：格式检查（`gofmt`/`goimports` 无 diff） | job `fmt` |
| CI-04 | 必跑：`golangci-lint`（与本地 `.golangci.yml` 一致） | job `lint` |
| CI-05 | 必跑：单元测试（含 `-race`）且失败即失败 | job `test` |
| CI-06 | 必跑：`govulncheck ./...`；已知高危未缓解则阻断 | job `vuln` |
| CI-07 | 必跑：`go mod tidy` 后 `go.mod`/`go.sum` 无未提交 diff | job `mod` |
| CI-08 | 必跑：全量 `go build` 所有 `cmd/*` | job `build` |
| CI-09 | 必跑：禁止跨端 transport 互引（admin/biz/c） | job `arch` |
| CI-10 | workflow `permissions` 默认最小化（至少 `contents: read`）；写权限按 job 显式授予 | workflow 审查 |
| CI-11 | 同一 PR 的新 push **取消**进行中的旧 run（concurrency） | workflow |
| CI-12 | 禁止在 CI 中使用生产密钥 / 生产数据源；测试仅用 fixture 或 ephemeral | Review + 本规范 |
| CI-13 | Action 与 linter 版本**钉扎**（tag 或 commit SHA）；禁止浮动 `latest` 无说明 | workflow |
| CI-14 | Job 必须设 `timeout-minutes`；禁止无限跑 | workflow |

**尚未纳入必跑、但已规划**（见 `standards-roadmap.md`）：OpenAPI breaking、集成测试容器、镜像扫描、gitleaks、覆盖率硬门槛。启用后须更新本表与 workflow。

---

## 3. 流水线拓扑

```text
                    ┌── fmt
                    ├── mod
PR / push main ──►  ├── lint
                    ├── test  (+ race, coverage artifact)
                    ├── vuln
                    ├── arch  (cross-transport)
                    └── build (all cmd)
```

- 各 job **并行**（互不依赖），缩短反馈时间。
- 任一 required job 失败 → 合并阻断。

---

## 4. 本地对齐（MUST）

开发者合并前应能本地复现：

```bash
make fmt-check      # CI-03
make tidy-check     # CI-07
make lint           # CI-04
make test-ci        # CI-05
make vuln           # CI-06
make check-transport # CI-09
make build          # CI-08
```

或一键：

```bash
make ci
```

`make ci` 失败时**禁止**依赖「CI 会过」而强行合入。

---

## 5. 分支保护与仓库设置（MUST，人工一次配置）

仓库管理员须在托管平台配置（GitHub 示例）：

1. 保护 `main`（或默认分支）。
2. Require a pull request before merging。
3. Require status checks to pass：勾选本仓库 `CI` workflow 下全部必跑 job（或单一 gate job，若采用）。
4. 禁止管理员无检查强推（组织策略允许时）。
5. 限制绕过：仅应急账号可跳过，且须事后补跑与记录。

未完成分支保护前，CI 文件合入仍视为「门禁未闭环」——须在上线清单中跟踪。

---

## 6. 失败策略

| 场景 | 策略 |
|------|------|
| fmt / tidy diff | 本地 `make fmt` / `go mod tidy` 后提交 |
| lint | 按 `.golangci.yml` 与注释/风格专文修复；例外走 `nolint`+原因 |
| test / race | 修复或补测；禁止 `t.Skip` 掩盖必测场景而无 issue |
| vuln | 升级依赖或官方缓解；无法短期修复须 ADR + 风险接受记录（Owner 签字）后方可临时豁免 |
| arch 跨端引用 | 删除错误 import；端能力只通过 app/domain 共享 |
| Action 基础设施故障 | 可重跑；连续失败升级平台/Owner，不降低检查标准 |

---

## 7. 缓存、性能与成本

- 缓存 Go modules 与 build cache（`setup-go` cache）。
- golangci-lint 使用官方 action 缓存。
- 不在每个 PR 默认构建并推送镜像（发布流水线另议，见 `RELEASE_STANDARDS`）。
- 集成测试 / 容器任务启用后单独 job，可按路径过滤（`paths`）以省分钟数——**不可**用路径过滤跳过 fmt/lint/test/vuln。

---

## 8. 安全（CI 面）

- 不 `echo` secrets；不把 `GITHUB_TOKEN` 打进日志。
- fork PR 不向本仓库写权限；secrets 不对外部 fork 暴露（GitHub 默认行为，勿改弱）。
- 第三方 Action 优先用官方或组织允许列表；升级须看 release notes。

---

## 9. 例外与豁免

1. **临时跳过某检查**：禁止在 workflow 中 `continue-on-error: true` 作用于 CI-03～CI-09，除非 Owner 书面批准且设到期日。
2. **紧急热修**：允许平台管理员绕过分支保护时，须在 24h 内补跑 CI 全绿，并在变更单注明。
3. **govuln 误报/无修复**：开 ADR，记录 CVE、影响面、缓解措施、复查日期；workflow 豁免须可追溯（注释 + ADR 编号）。

---

## 10. Review / 变更检查清单

改动 `.github/workflows/**` 或本专文时：

- [ ] CI-03～CI-09 仍为失败即失败
- [ ] `permissions` 未被放宽到超出需要
- [ ] Action 版本仍钉扎
- [ ] `make ci` 与 workflow 步骤一致（或文档说明差异）
- [ ] 已更新 `ARCHITECTURE.md` / `standards-roadmap.md` 状态（如需要）

---

## 11. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| `.github/workflows/ci.yml` | 强制执行 CI-02～CI-11、CI-13、CI-14 |
| `Makefile`（`ci` / `fmt-check` / `test-ci` / …） | 本地对齐 CI-03～CI-09 |
| `scripts/ci/check-transport-imports.sh` | CI-09 |
| `.golangci.yml` | CI-04 规则集 |
| 本文件 | 规范正文 |

后续 SEC/API/TEST 专文启用新 job 时，增补本文件 §2 表并保持编号稳定。
