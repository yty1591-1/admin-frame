# 工程化依赖与供应链规范（硬约束）

> **状态**：生效中（Normative）  
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**  
> **适用范围**：本仓库 `go.mod` / `go.sum`、直接与间接依赖、许可证与漏洞治理  
> **关联**：架构 §8.5；漏洞与密钥见 `SECURITY_STANDARDS.md`；CI 见 `CI_STANDARDS.md`  
> **执行方式**：`go mod tidy` 无 diff + `govulncheck` + replace 检查 + 许可证检查 + Review  
> **最后更新**：2026-10-21（补「无修复版本」受控例外：ADR + `deps/vuln-allowlist.txt` + `check-vuln.sh` 到期阻断）  
> **变更方式**：修改本规范须同步更新 `scripts/ci/check-deps.sh`、`deps/*`、流水线与架构 §8.5，经架构 Owner Review

---

## 1. 目标

1. **可复现**：依赖锁定在 `go.sum`，CI 与本地解析一致。  
2. **可审计**：禁止未审批的 `replace`；新增依赖可评估许可证与风险。  
3. **可强制**：已知高危漏洞与禁止类许可证在合并前阻断。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| DEP-01 | 使用 Go modules；仓库必须提交非空 `go.sum` | `check-deps.sh` |
| DEP-02 | `go mod tidy` 后 `go.mod` / `go.sum` 无未提交 diff | job `mod` / `make tidy-check` |
| DEP-03 | 禁止未审批的 `replace`（含指向未审计 fork / 本地路径）；例外须列入 allowlist 并有 ADR | `check-deps.sh` + `deps/replace-allowlist.txt` |
| DEP-04 | CI 必跑 `govulncheck ./...`；已知高危未缓解不得合并/发版 | job `vuln` / `make vuln` |
| DEP-05 | 依赖许可证须在允许列表内；策略以 `LICENSE_STANDARDS` 为准；禁止列表见 `deps/forbidden-licenses.txt` | `go-licenses check` + `check-license.sh` |
| DEP-06 | 新增**直接**依赖须在 MR 说明用途、许可证与替代方案（若有） | Review + PR 模板 |
| DEP-07 | 依赖更新由机器人或人工定期发起；禁止无说明大范围混升无关依赖 | Dependabot + Review |
| DEP-08 | 合并请求须勾选依赖自检 | PR 模板 |

---

## 3. replace 例外

允许 `replace` 的唯一路径：

1. 提交 ADR 说明原因、上游 issue、移除计划与到期日。  
2. 将模块路径（`replace` 左侧）写入 [`deps/replace-allowlist.txt`](../deps/replace-allowlist.txt)（一行一个，`#` 注释）。  
3. Owner Approve。

禁止：无到期日的永久 replace；指向含密钥的私有路径；用 replace 绕过漏洞修复。

---

## 4. 许可证

> **完整策略**见 [`LICENSE_STANDARDS.md`](./LICENSE_STANDARDS.md)（LIC-01～LIC-07）。  
> 允许列表：`deps/allowed-licenses.txt`；禁止列表：`deps/forbidden-licenses.txt`；声明：`deps/NOTICE`。

工具钉扎：`github.com/google/go-licenses@v1.6.0`。

---

## 5. 漏洞与更新节奏

- **高危**：`govulncheck` 失败即阻断；缓解须升级依赖或官方 workaround，并在 MR/ADR 记录 CVE。  
- **无修复版本**：按 `CI_STANDARDS` §10.3 提交 ADR，并把 OSV 编号与到期日登记到 [`deps/vuln-allowlist.txt`](../deps/vuln-allowlist.txt)；[`scripts/ci/check-vuln.sh`](../scripts/ci/check-vuln.sh) 只放行已登记且未过期的编号，到期或未登记一律阻断。  
- **镜像侧漏洞**：发版镜像流水线的 Trivy（`image` job）按 **二进制/OS 实际内容**扫描，与 `govulncheck` 的**源码可达性**判定口径不同：
  - `govulncheck` 只报「从本仓库代码可调用到」的漏洞；间接依赖里未被调用到的路径不报。
  - Trivy 报二进制中**所有**已收录漏洞（含未触达路径），因此可能出现在 `vuln` job 全绿后 `image` job 仍失败的情况。
  - 处理口径：**以 image 结果为准**阻断发版；优先升级到已修复版本；确属不可触达且无修复时，走 `check-release` 记录的豁免（ADR + 到期日），不得直接放宽扫描等级。
- **机器人**：GitHub Dependabot（`.github/dependabot.yml`）每周检查 `gomod` / `github-actions` / `docker`；Gitee 侧可用等价依赖机器人或人工周检。  
- **批量升级**：安全紧急可单独 MR；常规升级按域/模块拆分，便于回滚。

---

## 6. MUST NOT

- 提交缺失或过期的 `go.sum`。  
- 未经 allowlist + ADR 的 `replace`。  
- 为消告警而 `exclude` 高危漏洞检查且无到期日。  
- 引入允许列表外许可证的直接依赖。  
- 将私有模块凭证写进 `go.mod` 或 CI 日志。

---

## 7. 本地与 CI

```bash
make tidy-check   # DEP-02
make vuln         # DEP-04
make check-deps   # DEP-01/03/05
make ci           # 含以上
```

| 平台 | 入口 |
|------|------|
| GitHub | job `mod` + `vuln`（含 deps 检查） |
| Gitee | step `mod` + `vuln` |
| MR | PR 模板「依赖自检」 |

---

## 8. 例外

1. **govuln 无修复**：ADR 记录 CVE、影响面、缓解与复查日期；Owner 批准后方可临时合入。  
2. **许可证误判**：在 MR 附上游 SPDX 证明，经 Owner 同意后可调整 allowlist（须写明原因）。  
3. **紧急热修**：24h 内补跑 `make vuln` / `make check-deps` 全绿。

---

## 9. Review 清单

- [ ] `go.sum` 已更新；无意外 replace  
- [ ] 新直接依赖已说明用途与许可证  
- [ ] `make vuln` 与 `make check-deps` 通过  
- [ ] 无「顺手」无关大升级混进业务 MR  

---

## 10. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| `go.mod` / `go.sum` | 模块与锁定 |
| `deps/replace-allowlist.txt` | replace 白名单 |
| `deps/allowed-licenses.txt` | 许可证允许列表 |
| `scripts/ci/check-deps.sh` | DEP-01/03/05 |
| `make vuln` / job `vuln` | DEP-04 |
| `.github/dependabot.yml` | DEP-07 |
| 本文件 | 规范正文 |
