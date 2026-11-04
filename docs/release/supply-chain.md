# 供应链安全：SBOM、签名与部署前验签（ENT-19）

> **状态**：已落地（2026-11-05，静态门禁 + 本地等价演练通过；keyless 首次真跑挂账）
> **上游**：[`RELEASE_STANDARDS.md`](../RELEASE_STANDARDS.md) RELS-13 / RELS-14、[`CI_STANDARDS.md`](../CI_STANDARDS.md) §3.5
> **门禁**：`make check-sbom`（SB-01～SB-11）；演练：`bash scripts/ci/supply-chain-rehearsal.sh`

## 1. 要解决的问题

发版链路上原本只做到「不可变引用」：`release.yml` 的 `digests` job 从 tag 解析出 `sha256:` 后直接交给 `helm`。
digest 证明的是**内容没变**，不是**内容来自本仓库的流水线**。因此存在一条没有闸门的路径：

```text
有 registry 写权限的人（或泄漏的 token）
  → 推送一个同 tag、内容任意的镜像
  → release.yml 解析出合法的 sha256:
  → 冒烟通过（这个镜像当然能返回 200）
  → 生产跑起来的是来路不明的二进制
```

发布冻结、审批、金丝雀指标门禁都拦不住它——它们校验的是「行为」，不是「来源」。

## 2. 设计

| 环节 | 落点 | 关键约束 |
|---|---|---|
| SBOM | `image.yml`：Trivy `--format cyclonedx`，输出 `sbom-<cmd>.cdx.json` | 按**推送后的 digest** 生成；artifact 保留 90 天，`if-no-files-found: error` |
| 签名 | `image.yml`：`cosign sign --yes <image>@<digest>` | **keyless**（OIDC → Fulcio 短期证书），不引入长期私钥 secret；只签 digest |
| Attestation | `image.yml`：`cosign attest --type cyclonedx --predicate <sbom>` | 挂到同一 digest（OCI referrer），评审可用 `cosign download attestation` 取回 |
| 验签 | `release.yml` 新增 `verify` job | 位于 `digests` 之后、**第一次 `helm upgrade` 之前**；`staging` 的 `needs` 含 `verify` |
| 校验脚本 | `scripts/ci/verify-supply-chain.sh` | 5 个镜像逐个校验；拒绝 tag 引用、拒绝通配身份、固定 issuer；失败即退出 1 |
| 静态门禁 | `scripts/ci/check-sbom.sh` | 无网络也能跑，断言流水线结构（SB-01～SB-11） |

### 2.1 为什么签名对象必须是 digest

`cosign sign repo/image:1.2.3` 签的是「那一刻 tag 指向的 manifest」。tag 可被重新指向，验签时解析到的 manifest 与签名时可能不是同一个对象——这就是典型的 TOCTOU。
本仓库统一使用 `image@sha256:...`；`verify-supply-chain.sh` 在调用 cosign **之前**就用正则拒绝任何非 digest 形式的入参。

### 2.2 为什么 `staging` 必须 `needs: verify`

GitHub Actions 里，一个 job 只要没被 `needs` 引用，就可以被单独重跑或被后续改动「无意绕过」。
把 `verify` 写进 `needs` 后，验签失败 = `staging` 不启动 = 生产不动；`check-sbom.sh` SB-08 专门守这条，删除 `verify` 会直接让门禁报错。

### 2.3 身份绑定

```text
certificate-identity-regexp = ^https://github\.com/<owner>/<repo>/\.github/workflows/image\.yml@refs/(heads|tags)/
certificate-oidc-issuer     = https://token.actions.githubusercontent.com
```

- 精确到**本仓库的 image.yml**：别的仓库、本仓库其他 workflow（例如 PR 触发的构建）签出来的镜像都不接受；
- `refs/(heads|tags)` 覆盖两种来源：主干 push 的 `sha-<short>` 与 tag 的 `<semver>`；
- `.*` 之类的通配身份被脚本显式拒绝——「只要有签名」不是验签。

## 3. 验证边界（重要）

本地**无法**复现 keyless：Fulcio 证书要靠 GitHub Actions 的 OIDC 令牌签发，Rekor 是可公开审计的透明日志。因此本项分两层验证：

| 层 | 覆盖内容 | 证据 |
|---|---|---|
| 静态门禁 | 流水线结构是否正确（该签的签了、该验的验了、位置对不对、有没有后门） | `make check-sbom` 全绿 + 13 条证伪 |
| 本地等价演练 | cosign 的 digest 签名/验签、attestation/referrer、脚本控制流 | `supply-chain-rehearsal.sh` 11/11 PASS（2026-11-05） |
| CI 首跑（**挂账**） | keyless OIDC → Fulcio 证书 → Rekor 条目、GitHub identity 真实匹配 | 首次 `v*` tag 发布后回填本节 |

**没有做的事，不写成做过了**：本地演练用的是「本地密钥对 + 本地 registry」，`--tlog-upload=false`、`--insecure-ignore-tlog`，
这些正是生产脚本**禁止**的参数。它们只用于在无 OIDC 的环境里验证「签名与 digest 的绑定关系」这一层语义，
不能作为「生产验签已通过」的证据。

## 4. 本地等价演练记录（2026-11-05）

环境：Windows 11 / Docker 29.9.2（Desktop）/ cosign v2.4.1（容器）/ registry:2 / 探针镜像 alpine:3.24 与 alpine:3.20。

| # | 用例 | 结果 |
|---|---|---|
| 1 | 按 digest 签名 | PASS |
| 2 | 同一公钥验签 | PASS |
| 3 | 另一把公钥验签（应失败） | PASS（`no matching signatures: invalid signature`） |
| 4 | 未签名 digest 验签（应失败） | PASS |
| 5 | 同 tag 改指向另一份内容后，旧签名不可复用（应失败） | PASS |
| 6 | `attest --type cyclonedx` + attestation 载荷解码为 `bomFormat=CycloneDX` | PASS |
| 7 | `verify-supply-chain.sh`：cosign 成功放行 / 失败中止 / 拒绝 tag 引用 / 拒绝通配身份 | PASS ×4 |

用例设计上的两个坑（已固化进脚本）：

1. **探针镜像不能用同一份内容**：同内容 → 同 digest → 「未签名」用例会命中已签名对象，测出来的是假绿；
2. **tag 重指向必须换内容**：只重新 tag 同一个镜像，digest 不变，用例恒真。

复现：

```bash
bash scripts/ci/supply-chain-rehearsal.sh          # 需要 docker；首次会拉取 cosign / registry / alpine
bash scripts/ci/supply-chain-rehearsal.sh --port 5056
```

产物落在 `tmp-supply-chain/`（已 gitignore），密钥是演练用的临时密钥，不入库、不进任何真实仓库。

## 5. 日常操作

```bash
# 合并门禁（无网络、无需 cosign）
make check-sbom

# 查某个镜像的 SBOM（需要 cosign 与 registry 读权限）
cosign download attestation <registry>/<ns>/admin-api@sha256:<digest> | jq -r .payload | base64 -d | jq .

# 手工验签（与流水线同口径）
GITHUB_REPOSITORY=<owner>/<repo> bash scripts/ci/verify-supply-chain.sh \
  <registry>/<ns>/admin-api@sha256:<digest>
```

## 6. 挂账（随首次真实发布关闭）

| ID | 事项 | 关闭判据 |
|---|---|---|
| SC-01 | keyless 首次真跑（OIDC → Fulcio → Rekor） | 首个 `v*` tag 的 `image` job 产出签名与 attestation，`verify` job 通过，回填第 3 节 |
| SC-02 | 私有 registry 的 referrers 支持确认 | 若 registry 不支持 OCI referrers，`cosign attest` 回落 tag 方案前须先记录并更新本节 |
| SC-03 | SLSA provenance（`provenance: mode=max`）与 `verify-attestation --type slsaprovenance` | 仅在客户安全评审明确要求「构建来源证明」时启用；当前签名已绑定 workflow 身份，未宣称具备 SLSA 等级 |
| SC-04 | DAST | 需公网可达环境或等保测评触发（`ENT-20`），本期只做 SAST/依赖/镜像扫描 |

## 7. 变更本文件时必须同步

- 改 `image.yml` / `release.yml` 的签名验签结构 → `scripts/ci/check-sbom.sh`（SB-01～SB-10）
- 改 `verify-supply-chain.sh` 的参数或身份口径 → 本文 §2.3 与 `RELEASE_STANDARDS` RELS-14
- 首次真实发布后 → 回填 §3 第三行与 §6 的 SC-01
