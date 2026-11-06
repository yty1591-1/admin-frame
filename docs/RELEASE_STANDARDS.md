# 工程化发布与镜像规范（硬约束）

> **状态**：生效中（Normative）  
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**  
> **适用范围**：本仓库镜像构建、版本暴露、生产发布与回滚  
> **关联**：架构 §十八；迁移顺序见 `DATA_STANDARDS.md`；探针见 `OBSERVABILITY_STANDARDS.md`  
> **执行方式**：`check-release.sh` + 发布 Checklist + Review  
> **最后更新**：2026-11-07（RELS-15：`v*` tag 产出 GitHub Release 资产；前次 2026-10-21：发版镜像流水线落地）  
> **变更方式**：修改本规范须同步更新 `Dockerfile`、`scripts/ci/check-release.sh`、`docs/release/` 与架构 §十八，经架构 Owner Review

---

## 1. 目标

1. **可追溯**：运行中实例可通过 `/version` 对齐 git commit 与构建时间。  
2. **可回滚**：发布使用不可变引用（tag + digest）；禁止依赖浮动 `latest`。  
3. **可检查**：发版前有固定 Checklist；镜像与迁移顺序受控。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| RELS-01 | `Dockerfile`（及同类构建文件）**禁止**基础镜像或推送标签使用浮动 `latest` | `check-release.sh` |
| RELS-02 | 运行镜像必须有 `HEALTHCHECK`（或编排层等价探针），指向 `/healthz` | `check-release.sh` + OBS |
| RELS-03 | 构建注入 `version` / `commit` / `build_time`（ldflags 或等价）；API 进程 `/version` 暴露上述字段 | 代码 + `check-release.sh` |
| RELS-04 | 生产部署引用 **tag + digest**（或至少不可变 digest）；回滚切换至上一已知良好 digest | Review + 发布 Checklist |
| RELS-05 | 三端 / worker **可独立**构建与发布；禁止无说明的捆绑强绑发版 | Review |
| RELS-06 | 含迁移的发版须遵循 expand → 代码 → contract（DATA-05）；Checklist 勾选 | 发布 Checklist |
| RELS-07 | 发版前完成 [`docs/release/CHECKLIST.md`](./release/CHECKLIST.md) | Review |
| RELS-08 | 合并请求触及发布/镜像时须勾选发布自检 | PR 模板 |
| RELS-09 | 发布流水线必须先过**迁移门禁**（expand-only / append-only / 无未完成迁移）再部署；每次 `helm upgrade` 必须带 `--atomic --wait` | `.github/workflows/release.yml` + `check-migration-gate.sh` + `check-release.sh` |
| RELS-10 | 每次部署后必须跑发布冒烟（`/healthz`、`/readyz`、`/version` 版本一致、未认证 401） | `release-smoke.sh`（流水线强制） |
| RELS-11 | 生产放量前必须有金丝雀阶段：单端单副本、独立 release、指标门禁（流量下限 + 5xx + p95）达标才放量 | `values-canary.yaml` + `release-canary-gate.sh` |
| RELS-12 | 失败必须可自动回滚：`rollback` job 卸载金丝雀并把生产回退到上一 revision；人工命令固化在 runbook | `release.yml` + `runbooks/release-rollback.md` |
| RELS-13 | 每个发版镜像必须产出 **CycloneDX SBOM**（按 digest，随镜像 artifact 上传），并以 `cosign attest` 附加到同一 digest；SBOM 缺失视同发布失败 | `image.yml` + `check-sbom.sh`（SB-02/03/05） |
| RELS-14 | 部署前必须验签：签名身份须为**本仓库 `image.yml` 的 keyless 身份**、issuer 为 GitHub Actions，且同一 digest 上有 CycloneDX SBOM attestation；任一不满足即中止发布，不得 `continue-on-error` 或 `\|\| true` | `verify-supply-chain.sh` + `release.yml` + `check-sbom.sh`（SB-07/08/09/10） |
| RELS-15 | `v*` tag 必须产出 **GitHub Release**，资产固定为：二进制 bundle（`admin-frame-<version>-linux-<arch>.tar.gz`，amd64/arm64）、各 cmd 的 CycloneDX SBOM、`images.json`（镜像→digest）、`SHA256SUMS`；任一缺失视同发版失败 | `image.yml` + `check-release.sh` |

---

## 3. 镜像与标签

| 环境 | 标签约定 | 说明 |
|------|----------|------|
| 本地 | `admin-frame/<cmd>:local` | 仅开发；**禁止**推送（`make docker-push` 会拒绝 `local`） |
| CI / 预发 | `<registry>/<cmd>:sha-<short>`（主干 push） | 可丢弃；对应 `image` 流水线分支产物 |
| 预发 / 生产候选 | `<registry>/<cmd>:<semver>` 与 `<semver>-sha-<short>`（tag push） | 由 `v*` tag 产出；保留 sha 便于回溯 |
| 生产 | `<registry>/<cmd>:<semver>` **且** 记录 digest `sha256:…` | 部署清单保存 digest |

镜像名与注册表：`<registry>/<owner>/<cmd>`，默认 `ghcr.io`（见 `docs/CI_STANDARDS.md` §3.5）。

命名示例：`ghcr.io/org/admin-api:1.4.2@sha256:…`

**禁止**：生产 Deployment 仅写 `:latest`；禁止「构建机上最新的那次」无记录引用。

基础镜像：钉扎 major.minor（如 `alpine:3.24`）；生产流水线**推荐**锁定 digest，并在升级时显式变更。

**运行镜像 OS 必须在发行版支持期内**：过期基线（如已 EOL 的 `alpine:3.20`）会让 Trivy 只给出 `This OS version is no longer supported` 警告并**停止提供 OS 漏洞数据**，扫描结果变成「假绿」。`check-release.sh` 已对该下限做静态校验（`MIN_ALPINE_MINOR`），升级基础镜像时同步抬高该值。

---

## 4. 版本暴露

`GET /version`（匿名，见 SEC-07）响应至少包含：

```json
{
  "service": "admin-api",
  "version": "1.4.2",
  "commit": "abcdef0",
  "build_time": "2026-10-13T12:00:00Z"
}
```

- 本地未注入时可为 `dev` / `none` / `unknown`。  
- 生产禁止长期停留全 `dev`（发版 Checklist 校验）。

构建示例：

```bash
docker build --target admin-api \
  --build-arg VERSION=1.4.2 \
  --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  --build-arg BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t admin-frame/admin-api:1.4.2 .
```

---

## 5. 发布与回滚

### 5.1 默认策略

- **滚动发布**；三端可独立变更（RELS-05）。  
- 高风险变更（含迁移、鉴权、计费路径）：**金丝雀**——见 §5.3。  
- DB：expand/contract，与 DATA 专文一致。

### 5.2 流水线（ENT-17 已落地）

[`.github/workflows/release.yml`](../.github/workflows/release.yml) 的阶段与门禁：

| 阶段 | 触发条件 | 门禁 | 失败后果 |
|---|---|---|---|
| `plan` | tag `v*` / 手动 | 版本号 semver 校验 | 不进入后续阶段 |
| `preflight` | 同上 | `check-migration-gate.sh`（expand-only / append-only / dirty=false）+ `check-release.sh` | **任何环境都不部署** |
| `digests` | 非 dry_run 且 `RELEASE_ENABLED=true` | 每个镜像解析出 `sha256:` digest，形态校验 | 不允许用浮动 tag 继续 |
| `verify` | 同上 | `verify-supply-chain.sh`：5 个镜像逐个验签（keyless 身份 + OIDC issuer）并校验 CycloneDX SBOM attestation 内容 | **中止发布**（RELS-14） |
| `staging` | Environment `staging` 审批 | `helm upgrade --atomic --wait` + 发布冒烟 +（可选）部署后 schema 探测 | 自动回滚，不进生产 |
| `canary` | Environment `production` 审批 | 独立 release 单副本 + 金丝雀冒烟 + 指标门禁（流量下限 / 5xx / p95） | 只保留金丝雀，生产流量不变 |
| `promote` | 金丝雀通过 | 全量 `helm upgrade --atomic --wait` + 冒烟 + 生产指标门禁 | `rollback` job 接管 |
| `rollback` | 任一发布阶段失败 | 卸载金丝雀、`helm rollback <release> 0`、回滚后冒烟 | 自动失败 → 人工按 runbook 介入（P1） |

关键约定：

- **迁移先于代码**：`preflight` 不过，`digests` 之后的所有作业都不会启动；
- **不可变引用**：全部 workload 与迁移 Job 都按 digest 部署（`image.digest`），tag 只作可读标识；
- **来源可信**：`verify` 在第一次 `helm upgrade` 之前完成，且是 `staging` 的硬依赖——删掉它或让它软失败都会被 `check-sbom.sh`（SB-07/08/10）拦住；
- **fail closed**：缺 `KUBE_CONFIG_*` / `STAGING_BASE_URL` / `CANARY_BASE_URL` / `PROM_URL` 时显式报错，不静默跳过；
- **dry_run**：手动触发默认只打印计划（`plan` + `preflight`），不接触任何集群；
- **开关**：仓库变量 `RELEASE_ENABLED` 未设为 `true` 时，`notice` job 明确写出「未部署及原因」。

### 5.3 金丝雀细则

- 以**独立 Helm release** 承载（`admin-frame-canary`），不复用生产 release 名：
  `Deployment.spec.selector` 不可变，改标签等于重建；独立 release 的 Service/Pod 名带 `-canary` 前缀，
  网关可按权重切流，回滚只需卸载该 release；
- 金丝雀**不执行迁移**（`migrate.enabled=false`）：迁移由生产 release 的 `pre-upgrade` Job 执行一次；
- 金丝雀固定单副本、关闭 HPA/PDB/Ingress（`deploy/helm/admin-frame/values-canary.yaml`）；
- 指标门禁先校验**请求速率下限**，避免「零流量 → 零错误 → 假绿」；指标按
  `namespace + pod=~"<canary>-<workload>-.*"` 过滤，不与稳定版混算。

### 5.4 回滚

1. 优先让流水线的 `rollback` job 完成（卸载金丝雀 + `helm rollback <release> 0` + 冒烟）；  
2. 自动化失败时按 runbook 人工执行（`helm history` → `helm rollback` → 冒烟）；  
3. 若已执行不可逆 contract 迁移：按 DATA / runbook，**禁止**盲目 down；走数据修复预案；  
4. 验证 `/readyz`、关键 RED 指标与业务冒烟；  
5. 事后补 Postmortem（若构成事故）。

详细命令见 [`docs/runbooks/release-rollback.md`](./runbooks/release-rollback.md)。

---

## 6. 镜像安全（基线）

- 发版流水线应对推送镜像做漏洞扫描（组织工具：Trivy / 云厂商扫描等）；**高危未缓解不得上生产**。  
  已落地：`.github/workflows/image.yml` 推送后立即 Trivy 扫描 `CRITICAL,HIGH`（`ignore-unfixed`），命中即 job 失败，该 digest 不得进入生产。  
- 扫描失败**不得**通过「放宽 severity / 加 ignore 文件」解决；须先升级依赖或基础镜像。确属不可触达且无修复时，按 DEPENDENCY_STANDARDS §5 走 ADR 豁免（含到期日）。  
- 与 `govulncheck` 口径差异见 `docs/DEPENDENCY_STANDARDS.md` §5：`govulncheck` 按源码可达性判定，Trivy 按镜像实际内容判定，后者可能更严格。  
- 镜像非 root 运行；不含 `.git` 与源码树（多阶段构建）。  
- **SBOM（RELS-13）**：每个镜像按**推送后的 digest** 生成 CycloneDX（Trivy `--format cyclonedx`），作为 `sbom-<cmd>` artifact 上传（保留 90 天），并以 `cosign attest --type cyclonedx` 附加到同一 digest。按 tag 生成 SBOM 会把「被扫描的镜像」与「被部署的镜像」解耦，禁止。
- **签名（RELS-13）**：`cosign sign` 的对象必须是 digest（`<image>@sha256:...`），使用 **keyless**（OIDC → Fulcio 短期证书，`id-token: write`），不引入长期私钥 secret；签 tag 等于没签。
- **部署前验签（RELS-14）**：`release.yml` 的 `verify` job 在第一次 `helm upgrade` 之前调用 `scripts/ci/verify-supply-chain.sh`，逐个校验 5 个镜像的签名与 SBOM，失败即中止（`staging` 把 `verify` 列入 `needs`，没有「跳过继续」的路径）。
- **为什么 digest 不够**：digest 只证明「内容没变」，不证明「内容来自本仓库的流水线」。有 registry 写权限的人（或凭据泄漏）推一个同 tag 的新 digest，`digests` 阶段照样能解析出合法 `sha256:`，冒烟也不会失败——验签是唯一能拦住这条路径的环节。
- **验证边界**：keyless 链路依赖 GitHub OIDC，本地无法复现。签名/验签/attestation 的等价演练见 `scripts/ci/supply-chain-rehearsal.sh`（本地 registry + 本地密钥对，11 项断言），细节与挂账见 [`release/supply-chain.md`](./release/supply-chain.md)。

### 6.1 Release 资产（RELS-15）

`image.yml` 在 `v*` tag 上除推送镜像外，还必须创建 GitHub Release，资产固定为：

| 资产 | 内容 | 用途 |
|---|---|---|
| `admin-frame-<version>-linux-{amd64,arm64}.tar.gz` | 5 个静态二进制（`CGO_ENABLED=0`，ldflags 与 Dockerfile 同源）+ `configs/**/*.example` + `migrations/` | 无容器运行时可直接取包核对/演练；**不含** `configs/prod/.env`（演练凭据不外发，SEC-01） |
| `sbom-<cmd>.cdx.json` | 每个镜像按 digest 生成的 CycloneDX SBOM | 让 SBOM 在 workflow artifact 90 天过期后仍可追溯（RELS-13 的长期留存） |
| `images.json` | `version` / `commit` / `build_time` / `images`（每个 cmd 的 `<registry>/<owner>/<cmd>@sha256:…`） | 部署清单与运行中实例 `/version` 的核对基准（RELS-04） |
| `SHA256SUMS` | 上述所有资产的 sha256 | 离线校验制品未被替换 |

约束：

- 只在 tag 上产出。主干 push 的 `sha-` 镜像是可丢弃产物，进 Release 页会让「哪个是正式版」变模糊；
- digest 从 registry 解析（`docker buildx imagetools inspect`）而非复用构建步骤输出：Release 里写的必须是**已推送**的那个 digest；
- 同一 tag 重推时用 `gh release upload --clobber` 更新资产，不新建同名 Release；
- 发布说明由 `gh release create --generate-notes` 生成，不手写、不编造条目。

---

## 7. MUST NOT

- 生产使用 `:latest` 或可变浮动标签作为唯一引用。  
- 跳过 `/version` 字段注入的「纯手工」生产镜像。  
- 未完成发布 Checklist 即变更生产流量。  
- 在未就绪（`/readyz` 失败）时接入流量。  
- 绕过迁移门禁手工 `helm upgrade`（`--atomic` 与 expand 校验同时失效）。  
- 用「金丝雀零错误」作为放量依据而不校验其是否真的接到流量。  
- 部署未通过验签的镜像，或用 `continue-on-error` / `|| true` / `--insecure-ignore-tlog` 让验签「软失败」。  
- 只在 tag 上签名，或为「签名成功」而放宽 `--certificate-identity-regexp`（`.*` 等于不校验来源）。
- 只在 workflow artifact 里留 SBOM / 制品而不发布 Release 资产（90 天后无据可查，RELS-15）。

---

## 8. 本地与 CI

```bash
make check-release   # RELS-01/02/03 可自动项（含 image 流水线静态校验）
make check-sbom      # RELS-13/14 可自动项（SBOM / 签名 / 部署前验签的静态校验）
make docker          # 本地 tag:local（非生产）
make docker-push VERSION=<semver|sha>   # 本地复现推送（single-arch，不扫描）
make ci
```

| 平台 | 入口 |
|------|------|
| GitHub / Gitee | job `arch` 内跑 `check-release.sh` + `check-sbom.sh` |
| 发版镜像 | GitHub `image` 流水线（push main / `v*` tag；buildx 多架构 + Trivy + SBOM + cosign 签名） |
| 发版 | `docs/release/CHECKLIST.md`；部署前 `verify` job 验签 |
| 发布资产 | GitHub `image` 流水线的 `release` job（tag `v*`；二进制 bundle + SBOM + `images.json` + `SHA256SUMS`） |
| 供应链演练 | `bash scripts/ci/supply-chain-rehearsal.sh`（手动，需 docker） |
| MR | PR 模板「发布自检」 |

---

## 9. 例外

1. **本地 `:local` 标签**：允许，禁止推送到生产仓库。  
2. **热修来不及扫镜像**：Owner 批准 + 24h 内补扫；记录风险。  
3. **基础镜像 digest 滚动**：安全补丁升级须单独 MR，更新钉扎引用。

---

## 10. Review 清单

- [ ] Dockerfile 无 `latest`；HEALTHCHECK 指向 healthz  
- [ ] `/version` 含 version/commit/build_time  
- [ ] 生产引用含 digest；回滚点明确  
- [ ] 镜像由 `image` 流水线产出（非本机手工构建）  
- [ ] 迁移顺序与 Checklist 已完成  
- [ ] `make check-release` 通过  
- [ ] SBOM 已生成并随 digest 签名；`make check-sbom` 通过

---

## 11. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| `Dockerfile` | 多 stage；版本 ARG；HEALTHCHECK；`BUILDPLATFORM` 交叉编译 |
| `.github/workflows/image.yml` | 发版镜像：多架构构建 + 推送 + Trivy 扫描 + CycloneDX SBOM + cosign keyless 签名/attestation |
| `.github/workflows/release.yml` | 发布：迁移门禁 → **验签** → staging → 金丝雀 → 放量 → 回滚 |
| `scripts/ci/verify-supply-chain.sh` | RELS-14：部署前验签 + SBOM attestation 内容校验（fail closed） |
| `scripts/ci/check-sbom.sh` | RELS-13/14 静态门禁（SB-01～SB-11） |
| `scripts/ci/supply-chain-rehearsal.sh` | 本地等价演练（registry + 密钥对，不含 OIDC） |
| `docs/release/supply-chain.md` | 威胁模型、验证边界与挂账 |
| `internal/platform/endpoints.go` | `/version` |
| `docs/release/CHECKLIST.md` | 发版核对 |
| `scripts/ci/check-release.sh` | RELS-01/02/03 |
| `docs/runbooks/release-rollback.md` | 回滚处置 |
| 本文件 | 规范正文 |
