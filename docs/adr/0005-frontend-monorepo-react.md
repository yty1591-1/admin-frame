# ADR-0005：前端采用同仓 monorepo（React 19 + Vite）

- 状态：已接受（2026-10-25）
- 日期：2026-10-24
- 决策人：架构 Owner、前端负责人、后端接口负责人
- 关联：[前端架构与工程规范](../frontend-architecture.md)

## 背景

项目当前仅包含 A/B/C 三端 Go API 与分端 OpenAPI 契约，尚未纳入 Web 前端。`docs/ARCHITECTURE.md` 明确本文档止于后端契约边界，但 API 契约已为前端准备了分发物（TypeScript 类型、HTML 文档、changelog 与 `contract-commit.txt`）。

首期需要建设 A 端运营后台与 B 端商户后台。两者共享大量基础设施（请求管线、鉴权、设计系统），但在身份域、租户上下文、权限码与发布节奏上必须隔离。团队规模与业务边界尚不需要微前端或独立服务化前端。

## 决策

1. 前端工程放入本仓库 `frontend/`，使用 pnpm workspace 管理 `apps/admin-web`、`apps/biz-web` 与共享 `packages/*`。
2. 技术栈：React 19 + TypeScript（strict）+ Vite；React Router 7、TanStack Query、Zustand、React Hook Form + Zod、Ant Design 5。
3. 采用 `app → pages → features → entities → shared` 单向分层，共享能力下沉到 `packages/*`；通过 ESLint + dependency-cruiser 强制边界。
4. 契约优先：仅消费 `api/openapi/{admin,biz}.yaml` 生成的 TypeScript 类型；生成物入库并带生成头、禁止手改，CI 重新生成后 `git diff --exit-code` 校验无漂移；同 commit 即契约版本。
5. A/B 两端独立构建、独立域名、独立 Token 域与存储命名空间；构建期固定 `VITE_CHANNEL`，禁止运行时切换。
6. 首期只覆盖 A/B；C 端与 SSR 方案不进本 ADR，另行决策。

## 备选方案

| 方案 | 优点 | 未采用原因 |
|---|---|---|
| Vue 3 + Vite + Element Plus | 国内中后台上手快、生态成熟 | 与本仓库 OpenAPI 类型生成、团队 React 取向及长期组件复用规划不一致 |
| Next.js（SSR） | C 端 SEO/首屏友好 | A/B 中后台无 SEO 需求，额外引入 Node 运行时、缓存与运维复杂度 |
| 独立前端仓库 | 前后端发布解耦 | 契约需跨仓锁定 tag/commit，联调与门禁同步成本高；当前阶段收益不足 |
| 微前端（Module Federation） | 独立部署、团队自治 | 业务边界与团队规模未达到拆分条件，治理成本高于收益 |

## 后果

**正面**

- 契约与前端同 commit，类型漂移在编译期暴露。
- 共享请求管线、鉴权、设计系统，避免两端重复实现与安全策略分叉。
- 门禁可与后端 `make ci` 对齐，形成统一工程标准。

**负面 / 风险**

- 仓库体积与 CI 时长增加；需新增 Node/pnpm 工具链与缓存策略。
- monorepo 存在边界腐化风险，必须依赖自动化边界检查而非人工约定。
- 过渡期刷新令牌存储受后端契约限制（响应体下发），需以 `sessionStorage` + 严格 CSP 过渡，并推动 HttpOnly Cookie 增强。

## 后续行动

1. 按 [frontend-architecture.md](../frontend-architecture.md) P0 阶段落地脚手架与 `make frontend-ci`。
2. 同 MR 更新 `docs/CI_STANDARDS.md` 检查映射、`.github/workflows/*`、`.workflow/*`、`docs/codegen/MANIFEST.md` 与 `README.md`。
3. 推动后端补齐 `GET /{channel}/v1/auth/me`、HttpOnly 刷新 Cookie 与审批专用响应语义（见前端架构第十八章）。
4. C 端方案另立 ADR，评估 SSR/SEO、性能预算与字段最小化。
