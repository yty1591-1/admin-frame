# ADR-0010：SAML 依赖漏洞 GO-2026-6909 采用受控例外

- 状态：Accepted
- 日期：2026-11-06
- 关联：[DEPENDENCY_STANDARDS §5](../DEPENDENCY_STANDARDS.md)、[CI_STANDARDS §10.3](../CI_STANDARDS.md)、[deps/vuln-allowlist.txt](../../deps/vuln-allowlist.txt)、[scripts/ci/check-vuln.sh](../../scripts/ci/check-vuln.sh)

## 背景

`govulncheck v1.1.4` 在 CI 与本地 `make vuln` 报告：

```text
Vulnerability #1: GO-2026-6909
  Nil-pointer panic in validateAssertion and namespace-blind Subject.NameID
  Module: github.com/crewjam/saml
  Found in: github.com/crewjam/saml@v0.5.1
  Fixed in: N/A
  Trace: internal/identity/infrastructure/saml/provider.go:212
         saml.Provider.ParseResponse calls saml.ServiceProvider.ParseXMLResponse
```

`crewjam/saml` 最新发布版本即 `v0.5.1`（无更新版本），上游没有修复版本，也没有官方 workaround，无法通过升级消除。`govulncheck` 以 exit 3 阻断 CI job `vuln`。

## 决策

1. **登记受控例外**：在 [`deps/vuln-allowlist.txt`](../../deps/vuln-allowlist.txt) 写入 `GO-2026-6909 2027-05-01 ADR-0010`，到期日强制复查。
2. **门禁改造而非放宽扫描**：新增 [`scripts/ci/check-vuln.sh`](../../scripts/ci/check-vuln.sh)，`make vuln` 与 `.github/workflows/ci.yml` 的 job 统一调用它。脚本只放行「白名单内 + 代码可达」的 OSV 编号；白名单外、到期日非法或已过期一律失败；`govulncheck` 自身异常（非 0/3 退出码）同样失败。
3. **同步代码侧缓解**（降低实际可利用性，不依赖例外放行）：
   - 畸形断言解析失败统一返回 `40100`，不进入业务逻辑；
   - 进程级 panic 由 `internal/platform/httpserver/pipeline.go` 的 `Recover` 中间件兜底，单请求不会击穿进程；
   - 断言必须通过 IdP 证书验签，外部无法伪造 `NameID`；
   - SP 侧新增格式校验：请求 `persistent` 时若 IdP 返回 `transient` 直接拒绝（`Provider.checkNameIDFormat`），避免重复建号与主体漂移。

## 影响面

- 受影响代码路径：仅 C 端 SAML ACS（`/c/v1/auth/saml/acs`），需公网可达。
- `GO-2026-6909` 的两个要点及本仓库判断：
  - 空指针 panic：可由构造畸形 `SAMLResponse` 触发，但请求级 `Recover` 兜底，影响限于单请求 500，不构成进程级 DoS。
  - `Subject.NameID` 命名空间不敏感：需 IdP 签发异常内容才可能生效；断言必须验签通过，外部攻击者无法注入。
- 镜像侧 Trivy 若仍报同一编号，按本 ADR 在 `check-release` 记录，不另行放宽扫描。

## 后果

- **正面**：CI 门禁恢复可信（其余漏洞仍阻断）；豁免有 ADR、到期日与脚本强制，不靠人工记忆。
- **负面**：引入一个长期例外条目，需在到期日前复查；每季度依赖巡检需确认 `crewjam/saml` 是否发布修复版。
- **移除计划**：上游发布修复版本后，升级依赖并删除 allowlist 条目与本 ADR 的例外效力；到期未处理时门禁自动失败，强制推动复查。
