# 工程化安全规范（硬约束）

> **状态**：生效中（Normative）  
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**  
> **适用范围**：本仓库全部代码、配置样例、流水线与发布物  
> **关联**：架构 §十；密钥扫描与 gosec 见 CI；注释中的隐私约束见 `COMMENT_STANDARDS.md` C-08  
> **执行方式**：gitleaks + gosec（golangci-lint）+ 调试端点检查 + MR 模板 + Review  
> **最后更新**：2026-10-13  
> **变更方式**：修改本规范须同步更新 `.gitleaks.toml`、`.golangci.yml`（gosec）、流水线与架构 §十，经架构 Owner Review

---

## 1. 目标

1. **默认拒绝**：未认证、越权、跨端身份不得访问业务接口。
2. **密钥不入库**：真实密钥、生产连接串、完整令牌不得进入 git（含历史）。
3. **可强制**：能用扫描拦住的，不靠口头约定。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| SEC-01 | 禁止将密钥、私钥、云 AK/SK、生产 DSN、完整会话 Token 提交到仓库（含历史） | `make secrets` / CI job `secrets`（gitleaks） |
| SEC-02 | SQL 必须参数化；禁止拼接 SQL、shell 命令或把不可信输入传入 `exec` | gosec（G201/G202/G204）+ Review |
| SEC-03 | 三端 Token 域、DTO、Handler 隔离；禁止 transport 跨端互引 | `make check-transport` + Review |
| SEC-04 | `auth.demo_mode=true` 仅限本地/演示。为 false 时进程**必须拒绝启动**，不得静默使用 `DemoAuth` | `NewAuthMiddleware` + 单测 |
| SEC-05 | 日志、Trace、错误上报不得记录密钥、完整 Token、证件号、银行卡；手机号等 PII 须脱敏 | Review |
| SEC-06 | C 端响应禁止包含运营内部字段（如成本、内部备注） | Review + 分端 DTO |
| SEC-07 | 业务接口默认鉴权；仅 `/healthz`、`/readyz`、`/version` 可匿名 | Review |
| SEC-08 | 生产代码禁止引入 pprof、Swagger UI，除非该文件含 `//sec:debug-allowed` 且 MR 说明网络隔离 | `scripts/ci/check-debug-endpoints.sh` |
| SEC-09 | `gosec` 作为 `make lint` 的一部分，发现项阻断合并 | golangci-lint |
| SEC-10 | 生产入口强制 TLS；Cookie 若使用则 `HttpOnly` + `Secure` + 合理 `SameSite` | 发布 Review / 部署清单 |
| SEC-11 | 合并请求须使用仓库 MR 模板并勾选安全自检 | `.github/` 与 `.gitee/` PR 模板 |
| SEC-12 | 已知高危标准库/依赖漏洞未缓解不得发版（与 CI-06 相同） | `make vuln` |
| SEC-13 | CI 不得使用生产凭据或生产库 | Review（见 `CI_STANDARDS` CI-12） |
| SEC-14 | B 端读写必须约束 `merchant_id`；C 端必须校验资源归属；越权对外表现为不存在，而非泄露他人数据 | Review + 后续 TEST 专文用例 |

---

## 3. 身份与三端隔离

| 端 | 要求 |
|----|------|
| A | 独立 Token 域；权限点默认拒绝；关键写操作可审计 |
| B | Token 内商户与资源 `merchant_id` 一致；仓储条件兜底 |
| C | 仅本人资源；响应字段最小化 |

- 校验失败（401）与权限不足（403）使用不同业务码（`apperr`）。
- 进程 `channel` 必须与令牌渠道一致；`auth.expected_channel` 与启动端不一致时拒绝启动。
- **演示令牌**格式见 README，只存在于文档与 `demo_mode=true`。生产配置必须 `demo_mode: false`，在真实鉴权接入前进程会启动失败——这是预期行为，禁止用演示中间件“先顶上”。
- JWT 原语见 `pkg/jwt`（HS256/RS256）；三端须独立 `Issuer` / `Audience` / 密钥；日志禁止打印完整 Token。

---

## 4. 输入、注入与输出

- 外部输入校验类型、长度、范围；拒绝契约外字段。
- 禁止 `fmt.Sprintf` 拼进 SQL 文本。动态表名/列名若不可避免，必须白名单。
- 对外错误：C 端使用笼统文案；内部细节只进日志（`pkg/errors` 的 `PublicMessage` / `Detail`）。
- 禁止把堆栈、DSN、SQL 原文返回给终端用户。

---

## 5. 密钥与配置样例

- 仓库只提交 `dsn: ""` 或占位说明，不提交真实口令。
- 运行时密钥来自环境变量或 KMS，不写进镜像层。
- gitleaks 命中后：轮换已泄露凭证（即使是误提交），从历史清除须单独变更，不能只删最新文件。
- 允许列表仅用于**明确的演示文案**（`.gitleaks.toml`）。禁止用 allowlist 放过真实密钥。

---

## 6. 调试端点与运行面

- 禁止在 `cmd/` 或 `internal/` 空白导入 `net/http/pprof` 或挂载 Swagger UI。
- 排障需要时：独立端口 + 网络隔离 + `//sec:debug-allowed`，并在 MR 写明关闭条件。
- 容器非 root；依赖漏洞见 `make vuln`。

---

## 7. 本地与 CI

```bash
make secrets          # SEC-01 gitleaks
make check-debug      # SEC-08
make lint             # SEC-09 gosec（含在 golangci-lint 中）
make check-transport  # SEC-03
make ci               # 以上均包含
```

| 平台 | 入口 |
|------|------|
| GitHub Actions | job `secrets` + `lint`（gosec）+ `arch` |
| Gitee Go | step `secrets`（`gitee-job.sh secrets`）+ `lint` + `arch` |
| MR | `.github/PULL_REQUEST_TEMPLATE.md`、`.gitee/PULL_REQUEST_TEMPLATE.md` |

工具版本钉扎：gitleaks `v8.24.2`（module `github.com/zricethezav/gitleaks/v8`，见 `scripts/ci/gitleaks.sh`），golangci-lint 见 CI 配置。

---

## 8. 例外

1. gitleaks / gosec 误报：在 MR 说明原因，并收窄 allowlist 或 `//gosec:disable Gxxx`（须带理由，受 nolintlint 约束的用对应指令）。禁止整包关闭 gosec。
2. 调试端点：仅 `//sec:debug-allowed` + Owner 同意。
3. 紧急绕过分支保护：24h 内补跑 `make ci` 全绿，并轮换任何可能暴露的凭证。

---

## 9. Review 清单

- [ ] 模板中的安全项已勾选且与 diff 相符
- [ ] 无新密钥、无跨端引用、无未标注调试端点
- [ ] C 端 DTO 未增加内部字段
- [ ] `demo_mode` 未在将用于生产的配置中打开
