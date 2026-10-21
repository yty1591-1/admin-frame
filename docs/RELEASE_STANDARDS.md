# 工程化发布与镜像规范（硬约束）

> **状态**：生效中（Normative）  
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**  
> **适用范围**：本仓库镜像构建、版本暴露、生产发布与回滚  
> **关联**：架构 §十八；迁移顺序见 `DATA_STANDARDS.md`；探针见 `OBSERVABILITY_STANDARDS.md`  
> **执行方式**：`check-release.sh` + 发布 Checklist + Review  
> **最后更新**：2026-10-21（发版镜像流水线 `image` 落地：多架构构建 + 推送 + Trivy 扫描）  
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

基础镜像：钉扎 major.minor（如 `alpine:3.20`）；生产流水线**推荐**锁定 digest，并在升级时显式变更。

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

- **滚动发布**；三端可独立变更。  
- 高风险：金丝雀 / 蓝绿（可只对单端）。  
- DB：expand/contract，与 DATA 专文一致。

### 5.2 回滚

1. 将 Deployment 镜像改回上一 **digest**（或预存的良好 tag+digest）。  
2. 若已执行不可逆 contract 迁移：按 DATA / runbook，**禁止**盲目 down；走数据修复预案。  
3. 验证 `/readyz`、关键 RED 指标与业务冒烟。  
4. 事后补 Postmortem（若构成事故）。

详细步骤见 [`docs/runbooks/release-rollback.md`](./runbooks/release-rollback.md)。

---

## 6. 镜像安全（基线）

- 发版流水线应对推送镜像做漏洞扫描（组织工具：Trivy / 云厂商扫描等）；**高危未缓解不得上生产**。  
  已落地：`.github/workflows/image.yml` 推送后立即 Trivy 扫描 `CRITICAL,HIGH`（`ignore-unfixed`），命中即 job 失败，该 digest 不得进入生产。  
- 镜像非 root 运行；不含 `.git` 与源码树（多阶段构建）。  
- SBOM / 签名按组织要求启用（推荐，本仓库脚手架不强制工具版本）。

---

## 7. MUST NOT

- 生产使用 `:latest` 或可变浮动标签作为唯一引用。  
- 跳过 `/version` 字段注入的「纯手工」生产镜像。  
- 未完成发布 Checklist 即变更生产流量。  
- 在未就绪（`/readyz` 失败）时接入流量。  

---

## 8. 本地与 CI

```bash
make check-release   # RELS-01/02/03 可自动项（含 image 流水线静态校验）
make docker          # 本地 tag:local（非生产）
make docker-push VERSION=<semver|sha>   # 本地复现推送（single-arch，不扫描）
make ci
```

| 平台 | 入口 |
|------|------|
| GitHub / Gitee | job `arch` 内跑 `check-release.sh` |
| 发版镜像 | GitHub `image` 流水线（push main / `v*` tag；buildx 多架构 + Trivy） |
| 发版 | `docs/release/CHECKLIST.md` |
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

---

## 11. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| `Dockerfile` | 多 stage；版本 ARG；HEALTHCHECK；`BUILDPLATFORM` 交叉编译 |
| `.github/workflows/image.yml` | 发版镜像：多架构构建 + 推送 + Trivy 扫描 |
| `internal/platform/endpoints.go` | `/version` |
| `docs/release/CHECKLIST.md` | 发版核对 |
| `scripts/ci/check-release.sh` | RELS-01/02/03 |
| `docs/runbooks/release-rollback.md` | 回滚处置 |
| 本文件 | 规范正文 |
