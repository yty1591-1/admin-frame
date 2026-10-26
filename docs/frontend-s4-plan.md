# 前端 S4 方案：测试与发布硬化

| 字段 | 内容 |
|---|---|
| 阶段 | S4 |
| 状态 | **已落地**（S4-1 / S4-2 完成；S4-3 演练待排期） |
| 最后更新 | 2026-10-26 |
| 依赖 | S2 UI 基座、S3 错误与可观测 |
| 规划来源 | [frontend-infrastructure-plan.md](./frontend-infrastructure-plan.md) §4.8 / §4.9 / §6 |

---

## 一、目标与范围

S4 解决两件事：

1. **测试可信**：apps 内的页面与流程测试必须真的被执行；关键旅程（登录/MFA、权限守卫、40900、列表）有回归保护；可访问性有可执行断言。
2. **发布可控**：前端与后端独立发布；静态资源的缓存语义可验证；镜像非 root；回滚有据可依。

| 编号 | 交付 | 结果 |
|---|---|---|
| S4-1 | 测试基座与组件/流程测试 | 42 个测试文件 / 188 用例全绿，覆盖率 92% |
| S4-2 | 缓存头、非 root 镜像、Helm 前端工作负载、镜像流水线、回滚 runbook | 镜像与 Chart 模板可渲染；静态检查纳入门禁 |
| S4-3 | 回滚演练与 CDN 缓存治理 | 待与运维排期（本方案给出步骤与验证点） |

---

## 二、S4-1 测试基座

### 2.1 基座改造

| 文件 | 变更 |
|---|---|
| `packages/config/src/vitest-base.ts` | `include` 增加 `apps/*/src/**/*.test.{ts,tsx}`；覆盖率统计范围仍限定 `packages/*`（apps 作为回归保护，不参与阈值） |
| `packages/testing/src/render.tsx` | `renderWithProviders` 支持 `MemoryRouter` 初始路由、`ToastProvider`；导出 `createTestRouter` |
| `packages/testing/src/handlers.ts` | MSW v3 契约夹具：A/B 登录（tokens/MFA/强制绑定/40100）、动态码校验、权限快照（ok/empty/503）、角色列表与删除（40900、缺 Idempotency-Key） |
| `packages/testing/src/fixtures.ts` | 角色、权限目录、MFA、会话夹具与契约类型对齐 |
| `packages/testing/src/axe.ts` | `findSeriousA11yViolations`：只拦截 serious/critical；jsdom 关闭 `color-contrast`，对比度交由 E2E/Lighthouse |
| `packages/api-client/src/http/client.ts` | `fetchImpl` 改为调用时解析 `globalThis.fetch`，保证模块级客户端可被 MSW 拦截 |

### 2.2 测试清单

| 范围 | 用例 |
|---|---|
| 登录页 | 表单渲染 + axe、必填校验、40100 统一文案与 trace_id、MFA 两步验证、动态码失效回退、登录前强制绑定 MFA |
| 路由与守卫 | 菜单过滤/排序、`RequireAuth` 启动态与 `returnTo`、`RequirePermission` 快照未就绪/放行/403/503 fail-closed |
| 业务交互 | 角色列表加载与写权限、删除原因必填、幂等键复用、40900 审批引导不重试、错误态 trace_id、关键字 URL 同步、重置筛选 |
| 公共能力 | `useUnsavedChangesGuard` 放行/取消/`beforeunload`、根错误边界兜底页与 trace_id |
| B 端 | 工作台与契约版本、404、路由外壳与未知路径 |
| 包级单测 | 既有 42 个测试文件沿用；合计 **188 用例** |

### 2.3 门禁

`frontend/package.json` 的 `ci` 脚本顺序执行：format → lint → i18n → boundaries → typecheck → test:coverage → build → size → license。

本次结果：

- 42/42 测试文件、188/188 用例通过。
- 覆盖率：语句 91.99%、分支 89.06%、函数 89.26%、行 92.03%（阈值 70%）。
- admin/biz 体积预算与许可证检查通过。

---

## 三、S4-2 发布硬化

### 3.1 静态服务与缓存（INF-REL-02）

`deploy/frontend/nginx.conf`，运行于 `nginx-unprivileged`（UID/GID 101，监听 8080）：

| 路径 | Cache-Control | 说明 |
|---|---|---|
| `/assets/*` | `public, max-age=31536000, immutable` | 内容哈希文件；缺失直接 404，不回退入口 |
| `/index.html` | `no-store, no-cache, must-revalidate` | 发版后必须立即拿到新入口 |
| `/config.js` | `no-store, no-cache, must-revalidate` | 运行期配置热修/环境切换即时生效 |
| 其他（SPA 回退） | `no-store, no-cache, must-revalidate` | 未知路由返回入口，不缓存 |
| `/healthz` | `no-store` | 探针；不写访问日志 |

安全头通过 `security-headers.conf` 在每个 location 显式 `include`，避免 `add_header` 继承规则导致安全头被缓存头覆盖。CSP 仍需 nonce 注入，留待 P1。

### 3.2 镜像与非 root（INF-REL-03）

- `deploy/frontend/Dockerfile`：Node 构建阶段执行 `pnpm install --frozen-lockfile` + `pnpm --filter @admin-frame/<APP> build`；运行阶段仅保留静态产物与 Nginx 配置。
- 运行基座固定 `nginxinc/nginx-unprivileged:<major>.<minor>-alpine`，`USER 101:101`，`EXPOSE 8080`，`HEALTHCHECK /healthz`。
- `.dockerignore` 以仓库根为上下文，排除 `node_modules`、`dist`、`.git`、文档与本地产物；`deploy/*` 仅保留 `deploy/frontend`。
- `.github/workflows/image.yml` 新增 `frontend-image` job（matrix：`admin-web` / `biz-web`），多架构构建、禁止 `latest`、推送后 Trivy `CRITICAL,HIGH` 扫描。

### 3.3 Helm 前端工作负载（INF-REL-03）

`deploy/helm/admin-frame-frontend`：

- Deployment（`maxUnavailable: 0` / `maxSurge: 1`）、Service、可选 Ingress/PDB。
- 运行期配置渲染为 ConfigMap 的 `config.js`，以 `subPath` 挂载；`checksum/config` 变化触发滚动。
- Pod 级加固：`runAsNonRoot`、只读根文件系统、丢弃全部 capabilities、`automountServiceAccountToken: false`、`/tmp` 使用 `emptyDir`。
- 镜像 digest 优先于 tag；缺省回退 `Chart.AppVersion`，禁止隐式 `latest`。
- `values-prod.yaml` 给出三副本、PDB、TLS/Ingress 与 digest 注入位置。

同源部署注意：若 `runtime.apiBaseUrl` 留空，Ingress 必须先把 `/admin`、`/biz` 等 API 前缀转发到后端，否则会被 SPA 回退吞掉。

### 3.4 发布门禁与回滚（INF-REL-05）

- `scripts/ci/check-release.sh` 增加前端静态校验：基础镜像不得 `:latest`、必须钉扎 `nginx-unprivileged` minor、必须有 `/healthz` 的 `HEALTHCHECK`、必须声明数值非 root `USER`。
- 发布清单 `docs/release/CHECKLIST.md` 增加前端专章（digest、`config.js`、缓存头、冒烟、回滚入口）。
- 回滚手册：[`docs/runbooks/frontend-rollback.md`](./runbooks/frontend-rollback.md)，覆盖镜像回滚、仅配置回滚、CDN purge 与三层缓存头验证。
- 同步更新 `docs/CI_STANDARDS.md` §3.5 与 `docs/RELEASE_STANDARDS.md` §3。

---

## 四、验收结果

| 项 | 命令 | 结果 |
|---|---|---|
| 前端完整门禁 | `pnpm run ci`（`frontend/`） | 通过（188 用例，覆盖率 92%） |
| Chart 静态检查 | `helm lint deploy/helm/admin-frame-frontend --strict` | 0 failed |
| Chart 渲染 | `helm template admin-frame-frontend deploy/helm/admin-frame-frontend -f values-prod.yaml` | 404 行，资源名/镜像/ConfigMap 符合预期 |
| 发布规范 | `bash scripts/ci/check-release.sh` | `OK: release`（含前端镜像四项检查） |
| YAML 语法 | `js-yaml` 解析 `image.yml` / `values*.yaml` / `Chart.yaml` | 全部 OK |

已知限制：

- 本机 Docker 无法访问镜像仓库，`docker build` 与 `nginx -t` 未在本地执行；由 `frontend-image` 流水线在 CI 中完成构建与扫描，发布前须按 Checklist 验证缓存头。
- CDN/Ingress 层缓存策略不在仓库内，需运维按 runbook 确认 `index.html`/`config.js` 未被外层缓存。

---

## 五、后续（S4-3 及之后）

| 项 | 内容 | 优先级 |
|---|---|---|
| 回滚演练 | staging 连续两次发布后回退，记录耗时与缓存命中；补齐 CDN purge 流程 | P1 |
| MSW 补全 | 429 限流、超时、网络中断夹具 | P1 |
| Source Map | 构建产物不公开 + 私有上传并关联 release（INF-REL-04） | P1 |
| CSP | 基于运行期 nonce 注入，先 report-only 再收紧 | P1 |
| 视觉回归 | 组件数达到阈值后评估 | P3 |
