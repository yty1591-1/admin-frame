# 工程化测试规范（硬约束）

> **状态**：生效中（Normative）
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**
> **适用范围**：本仓库全部 Go 测试、CI 测试步骤与测试用配置
> **关联**：架构 §十三；安全见 `SECURITY_STANDARDS.md`；CI 见 `CI_STANDARDS.md`
> **执行方式**：`go test`（含 `-race`）+ 覆盖率门槛 + 必测场景检查 + MR 模板 + Review
> **最后更新**：2026-10-22（TEST-03 改为 IAM 自助管理基线；必测函数名与示例路径 `internal/order` → `internal/iam`）
> **变更方式**：修改本规范须同步更新 `scripts/ci/check-test-gates.sh`、流水线 test job 与架构 §十三，经架构 Owner Review

---

## 1. 目标

1. **防回归**：领域不变量与三端隔离/越权有自动化用例，合并前必绿。
2. **可度量**：核心 domain 包覆盖率有硬门槛，避免「只测快乐路径」。
3. **环境安全**：测试与 CI **永不**触碰生产凭据 / 生产库。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| TEST-01 | 每个业务 `internal/<domain>/domain` 包必须有同包 `*_test.go`，覆盖聚合/不变量等纯逻辑 | `scripts/ci/check-test-gates.sh` |
| TEST-02 | 核心 domain 包语句覆盖率 ≥ **70%**（按包分别计量） | `check-test-gates.sh`（`TEST_DOMAIN_COVER_MIN`） |
| TEST-02b | `internal/platform` 语句覆盖率 ≥ **70%**（配置校验、Server 加固、请求链路、鉴权装配） | `check-test-gates.sh`（`TEST_PLATFORM_COVER_MIN`） |
| TEST-02c | `internal/security` 语句覆盖率 ≥ **80%**（JWT 校验、密钥轮换、授权中间件） | `check-test-gates.sh`（`TEST_SECURITY_COVER_MIN`） |
| TEST-03 | **B 商户隔离**（当前以 IAM 自助管理为基线）必须有可执行用例，且 CI 必跑；存在 C 端业务资源时还必须有越权用例 | 必测函数名 + `go test -run` |
| TEST-04 | CI（Linux）/ `make test-ci` 必须带 `-race`；失败即阻断合并。本地 Windows 无 CGO 工具链时可降级并告警 | job `test` / `run-tests.sh` |
| TEST-05 | 禁止在测试或 CI 中使用生产凭据、生产 DSN、生产库；仅用 memory / fixture / ephemeral 容器 | Review + CI-12；流水线禁注入 prod secrets |
| TEST-06 | 禁止无 issue 引用的 `t.Skip` 掩盖 TEST-03 必测场景 | Review + 必测 `-run` |
| TEST-07 | 集成测试（若有）放在 `tests/integration/`，使用 testcontainers 或等价 ephemeral，不得连共享生产/预发库；集成测试清单由 `run-integration-tests.sh` 前置断言（包存在 / 关键用例点名 / 数量基线，S5-5），删除测试确定性阻断 CI integration job | Review |
| TEST-08 | 合并请求须使用仓库 MR 模板并勾选测试自检 | `.github/` 与 `.gitee/` PR 模板 |

---

## 3. 分层放置

| 类型 | 位置 | 要求 |
|------|------|------|
| 单元测试 | 同包 `*_test.go`（推荐 `xxx_test` 黑盒包名，domain 可为白盒） | domain **必须**；app 对隔离/用例逻辑应有覆盖 |
| 集成测试 | `tests/integration/` | 真实 DB/MQ 时用容器或 CI service（ephemeral）；含租户隔离；DSN 经环境变量注入，未配置时显式 `t.Skipf` 并说明原因，不得静默通过 |
| 端到端 | `tests/e2e/{admin,biz,c}/` | 按需；分别拉起对应 api |
| Benchmark | `BenchmarkXxx` | 热路径按需，不替代功能测试 |

---

## 4. 必测场景（三端）

| 场景 | 最低要求 | 约定测试函数名（可同包多个，名称须匹配） |
|------|----------|------------------------------------------|
| B 商户隔离 | 他商户读/写本商户资源失败；对外为 **NotFound**（或等价不泄露） | `TestBizRoleTenantIsolation`、`TestBizChangeRequestTenantIsolation` |
| C 用户归属 / 越权 | 他用户读/写本人资源失败；对外为 **NotFound** | 存在 C 端业务资源时启用 `TestUserOwnership` |

- 函数可位于任意 `*_test.go`，但必须能被 `go test ./... -run '^(TestBizRoleTenantIsolation|TestBizChangeRequestTenantIsolation)$'` 选中并全部通过。
- 当前 C 端仅有平台 probe 端点、无业务资源，因此 `TestUserOwnership` 暂不设门禁；**首个 C 端业务资源落地时必须恢复该必测函数**并加入 `check-test-gates.sh`。
- 新增对多租户/多用户暴露的业务域时：为该域补充同类用例，并同步 `REQUIRED_TESTS` 与 PR 模板。
- A 端权限点否定路径建议有单测，暂不设同级硬函数名门槛。

---

## 5. 覆盖率

- **门槛范围 1**：`go list ./internal/...` 中路径以 `/domain` 结尾的每个包。
- **门槛范围 2**：`./internal/platform`（进程装配、配置校验、Server 加固、请求链路）。
- **门槛范围 3**：`./internal/security`（JWT 校验与密钥轮换、授权中间件）。
- **阈值**：默认 domain / platform **70%**、security **80%** 语句覆盖；分别可用 `TEST_DOMAIN_COVER_MIN` / `TEST_PLATFORM_COVER_MIN` / `TEST_SECURITY_COVER_MIN` 上调，**禁止下调**除非 Owner + ADR。
- **计量**：按包分别 `go test -coverprofile` 后读 `go tool cover -func` 的 `total`。
- 纯接口/无语句包：无 `.go` 实现文件（仅 `doc.go` / 接口）时跳过覆盖率，但仍建议保留契约测试于 app 层。
- 全仓 coverage 产物（`coverage.out`）继续上传 CI artifact，供趋势分析；**硬门槛为 domain、platform 与 security**。

---

## 6. MUST NOT

- domain 变更无单测、仅靠手工点点。
- 用生产 / 预发真实库跑单元或 CI 测试。
- 将 TEST-03 用例 `t.Skip`、注释掉或改名绕过门禁。
- 为冲覆盖率写无断言的空测试。
- 在 CI test job 中挂载生产向 `secrets`（数据库口令、云 AK 等）。

---

## 7. 本地与 CI

```bash
make test-ci          # -race + coverage.out + TEST 门禁
make check-test-gates # 仅跑 TEST-01～03（需已有或现场测 domain 覆盖率）
make ci               # 含 test-ci
```

| 平台 | 入口 |
|------|------|
| GitHub Actions | job `test`（含 `check-test-gates.sh`） |
| Gitee Go | step `test`（`gitee-job.sh test`） |
| MR | PR 模板「测试自检」 |

工具：Go 标准 `testing` / `go test` / `go tool cover`；版本跟随 `go.mod`。

---

## 8. 例外

1. **覆盖率短期不达标**：仅允许 Owner 书面批准 + ADR，设定到期日与补测计划；禁止永久豁免。
2. **必测改名/迁移**：同 MR 更新 `scripts/ci/check-test-gates.sh` 中的函数名列表与本专文 §4。
3. **紧急热修**：绕过保护后 24h 内补跑 `make test-ci` 全绿并补隔离用例。

---

## 9. Review 清单

- [ ] 变更触及的 domain 有对应单测；核心包覆盖率未倒退破线
- [ ] B 隔离 / C 越权场景仍存在且与行为一致（越权 → NotFound）
- [ ] 未引入生产凭据或连生产库的测试
- [ ] 无无故 `t.Skip` 必测；`make test-ci` 通过

---

## 10. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| `scripts/ci/run-tests.sh` | 全量测试 + 调用 gates（TEST-04） |
| `scripts/ci/check-test-gates.sh` | TEST-01～03、TEST-05 |
| `Makefile`（`test-ci` / `check-test-gates`） | 本地对齐 |
| job/step `test` | `run-tests.sh` |
| `internal/iam/transport/biz/router_test.go` | 基线 B 端租户隔离用例 |
| 本文件 | 规范正文 |
