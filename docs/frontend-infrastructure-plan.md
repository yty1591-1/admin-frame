# 前端基础设施规划与差距评估（A/B 双端）

> **版本**：v0.1（评审稿）
> **状态**：待评审（Draft for Review）
> **适用范围**：`frontend/` 下 A 端 `admin-web`、B 端 `biz-web` 与共享 `packages/*`
> **基线**：2026-10-25，`main` 分支 `5262feb` + 未提交的 A 端认证切片
> **关联文档**：[前端架构与工程规范](./frontend-architecture.md)、[ADR-0005](./adr/0005-frontend-monorepo-react.md)、[CI 流水线规范](./CI_STANDARDS.md)、[安全规范](./SECURITY_STANDARDS.md)、[测试规范](./TESTING_STANDARDS.md)
> **定位**：本文回答“写业务页面前还必须具备什么”，不替代 `frontend-architecture.md` 的规范性约束；两者冲突时以架构文档为准。

---

## 一、评审目的与判定标准

### 1.1 为什么先做基础设施

业务页面一旦铺开，基础设施缺口会以三种方式放大成本：

1. **横向返工**：每个页面各自实现表格分页、错误提示、权限判断、确认弹窗，后期统一替换需改数十处。
2. **安全与合规风险**：令牌、权限、审计、trace_id 一旦在页面里各自处理，越权与信息泄露难以收口。
3. **门禁失效**：没有统一数据层与测试基座，覆盖率、体积、E2E 会随页面数量增长而失控。

### 1.2 判定标准

一个能力被认定为“业务开发前必须具备”，需同时满足：

- 与具体业务无关，或至少被 2 个以上页面复用；
- 缺失时会导致重复实现、返工或安全/合规风险；
- 有可执行的验收标准（能落到脚本、测试或 Review Checklist）。

### 1.3 成熟度定义

| 级别 | 含义 |
|---|---|
| L0 缺失 | 无实现，无约定 |
| L1 可用 | 有零散实现，可跑通单点，但无统一入口与约束 |
| L2 标准化 | 有统一入口、约定与测试，页面可按规范接入 |
| L3 可治理 | 有度量、自动化门禁与回归，可防止退化 |

---

## 二、结论摘要

| 领域 | 现状 | 结论 |
|---|---|---|
| 工程治理（Monorepo/TS/Lint/格式/边界/CI） | **L3** | 已具备，后续只需增量维护 |
| 契约与请求管线 | **L2** | 生成、校验、超时、重试、幂等、401 重放已闭环；缺业务侧数据层约定 |
| 认证与会话 | **L2** | 登录、两步 MFA、单飞刷新、登出、启动恢复已闭环；缺跨标签页同步、空闲超时、会话管理页 |
| 权限与审批 | **L1** | 仅有纯函数 `PermissionChecker`；快照、守卫、菜单、审批引导全部缺失 |
| UI 设计系统与交互基座 | **L1** | 令牌、主题、状态组件、AppShell 已有；表格/表单/弹窗/通知等业务高频件缺失 |
| 可观测与前端安全 | **L1** | 错误模型与根错误边界已有；logger、上报通道、Web Vitals、CSP 注入缺失 |
| 性能 | **L2** | 体积门禁、路由懒加载、manualChunks 已生效；缺 Lighthouse/RUM 与预取 |
| 测试与质量 | **L2** | 单测/MSW/E2E 骨架已有；**apps 测试未纳入 Vitest**、无 axe、无契约夹具沉淀 |
| 发布与运行 | **L1** | 静态构建可用；缺镜像/Helm、缓存头、Source Map 私有化、回滚演练 |
| 多端与国际化 | **L0 / L0** | B 端尚未启动；无 i18n 框架与文案字典 |

**结论**

1. 工程底座与契约层已达到可规模化开发的水平，**不需要重复建设**。
2. 业务页面的真正前置缺口集中在 7 项：**权限与路由、数据层约定、UI 交互基座、错误与可观测、高风险操作套件、i18n 决策、测试基座**。
3. B 端能力与发布运维属于阶段性问题，可在 A 端业务开发并行推进，不阻塞首批页面。

---

## 三、当前基线（代码事实）

| 能力 | 现状 | 证据 |
|---|---|---|
| Monorepo 与依赖边界 | pnpm workspace；dependency-cruiser 阻断跨端/跨 feature/反向依赖 | `frontend/dependency-cruiser.cjs` |
| 类型与规范 | TS strict 全开；ESLint type-checked + a11y + 禁止直连 antd/fetch/localStorage | `frontend/tsconfig.base.json`、`frontend/packages/config/src/eslint-base.mjs` |
| 契约生成 | openapi-typescript 7.4.0 生成 admin/biz，CI 校验无漂移，MANIFEST 已 active | `frontend/scripts/gen-api.mjs`、[MANIFEST](./codegen/MANIFEST.md) |
| 请求管线 | 统一信封、错误归一、15s 超时、幂等重试、401 刷新后单次重放 | `frontend/packages/api-client/src/http/client.ts` |
| 认证能力 | 内存 Access Token、sessionStorage Refresh、单飞刷新 + `navigator.locks`、MFA 票据仅内存 | `frontend/packages/auth/src/*` |
| 认证装配 | 登录页、两步 MFA、启动恢复、登录守卫、403、退出登录 | `frontend/apps/admin-web/src/app/auth/*`、`frontend/apps/admin-web/src/pages/LoginPage.tsx` |
| UI 基础 | 设计令牌、主题、Empty/Error/PageLoading、AppShell、原子组件白名单 | `frontend/packages/ui/src/*` |
| 工具函数 | 时间/金额格式化、分页上限、运行期配置校验 | `frontend/packages/shared/src/*` |
| 测试 | 53 个单测用例，覆盖率门禁 70%，MSW 契约 Mock，2 条 admin E2E | `frontend/packages/*/src/*.test.*`、`frontend/e2e/admin/shell.spec.ts` |
| 门禁 | format/lint/boundaries/typecheck/test/build/size/license/audit/E2E 均有本地与 CI 入口 | `frontend/package.json`、`scripts/ci/check-frontend.sh`、`.github/workflows/ci.yml` |

**已确认的空白**

- `vitest` 的 `include` 仅覆盖 `packages/*/src`，**apps 内组件测试不会被执行**（见 `frontend/packages/config/src/vitest-base.ts`）。
- MSW 仅覆盖 admin 登录/刷新/登出/权限快照 4 个端点，无会话/MFA/异常流夹具。
- 运行期 `config.features` 已解析但无消费封装；无 feature flag 读取入口。
- `ThemeProvider` 支持 CSP nonce，但入口未从运行期配置注入。
- 无 logger、无错误上报 SDK、无 Web Vitals 采集。
- 无 i18n；界面文案为硬编码中文（`zh-CN` 由 antd locale 提供）。

---

## 四、能力清单（按域）

> 优先级：**P0** = 业务页面开工前必须完成；**P1** = 首批业务页面并行交付；**P2** = 规模化阶段；**P3** = 按需评估。

### 4.1 工程与治理

| 编号 | 能力 | 现状 | 目标 | 验收标准 | 优先级 |
|---|---|---|---|---|---|
| INF-ENG-01 | 工作区与依赖边界 | L3 | 保持 | `pnpm boundaries` 零违规 | 已具备 |
| INF-ENG-02 | 类型/规范/格式门禁 | L3 | 保持 | `lint --max-warnings=0`、`format:check`、`typecheck` 全绿 | 已具备 |
| INF-ENG-03 | 契约漂移阻断 | L3 | 保持 | CI `gen:api` 后 `git diff --exit-code` | 已具备 |
| INF-ENG-04 | 体积门禁 | L3 | 保持 | admin ≤300KB、biz ≤260KB、单路由 ≤120KB | 已具备 |
| INF-ENG-05 | 依赖许可证与漏洞门禁 | L2 | L3 | `license` + `audit --audit-level=high` 进入 `frontend-ci` | P1 |
| INF-ENG-06 | 环境一致性 | L2 | L3 | Node/pnpm 钉扎；`--frozen-lockfile`；本地与 CI 同命令 | P1 |

### 4.2 契约与数据层

| 编号 | 能力 | 现状 | 目标 | 验收标准 | 优先级 |
|---|---|---|---|---|---|
| INF-DATA-01 | 契约类型唯一来源 | L2 | L3 | 业务代码零手写响应结构；生成物禁止手改 | 已具备 |
| INF-DATA-02 | 错误码 → 用户文案映射 | L1 | L2 | 40000/40100/40300/40400/40900/42900/50000/50300 各有统一文案与展示位置 | **P0** |
| INF-DATA-03 | Query Key 约定与工厂 | L0 | L2 | 按「端别/领域/资源/形态/参数」生成；禁止散落字符串 | **P0** |
| INF-DATA-04 | 列表 URL 状态同步 | L1 | L2 | 分页/筛选/排序写入 URL；切换筛选重置页码；上限 100 | **P0** |
| INF-DATA-05 | 写操作幂等键 | L1 | L2 | 统一生成 UUIDv4；同一次提交重试复用；缺失时开发环境抛错 | **P0** |
| INF-DATA-06 | 请求取消与竞态 | L1 | L2 | 路由切换/参数变化通过 `AbortSignal` 取消；列表翻页保留旧数据 | **P0** |
| INF-DATA-07 | 服务端状态边界 | L1 | L2 | 服务端数据只进 TanStack Query；Zustand 仅存 UI 元信息 | **P0** |

### 4.3 认证与会话

| 编号 | 能力 | 现状 | 目标 | 验收标准 | 优先级 |
|---|---|---|---|---|---|
| INF-AUTH-01 | 令牌存储隔离 | L3 | 保持 | 仅 `packages/auth` 访问存储；无 localStorage；无令牌入 URL/日志 | 已具备 |
| INF-AUTH-02 | 单飞刷新 | L3 | 保持 | 并发 401 只刷新一次；失败清会话并跳登录 | 已具备 |
| INF-AUTH-03 | 登录与两步 MFA | L2 | L3 | 登录/MFA/失败重登/票据一次性消费有单测 | 已具备 |
| INF-AUTH-04 | 多标签页会话同步 | L0 | L2 | `BroadcastChannel` 同步登录/登出/刷新；并发轮换不触发重放 | P1 |
| INF-AUTH-05 | 空闲超时 | L0 | L2 | 30 分钟告警、60 分钟强制登出；与后端 Token 有效期匹配 | P1 |
| INF-AUTH-06 | 会话管理页 | L1 | L2 | 列表/撤销单会话/登出全部；不展示 IP/UA 原文 | P1 |
| INF-AUTH-07 | MFA 绑定与解绑页 | L0 | L2 | 密钥/恢复码只展示一次；解绑需动态码；成功后撤销全部会话 | P1 |
| INF-AUTH-08 | 强制绑定 MFA 流程 | L0 | L2 | 登录前绑定引导可用（契约已定稿，见 `identity-authn.md` §7.2.1） | P1 |
| INF-AUTH-09 | 刷新令牌 HttpOnly 适配 | L0 | L2 | `TokenStore` 适配器切换，业务零改动 | P2 |

### 4.4 权限与审批

| 编号 | 能力 | 现状 | 目标 | 验收标准 | 优先级 |
|---|---|---|---|---|---|
| INF-IAM-01 | 权限快照 Provider | L1 | L2 | `/iam/me/permissions` 统一查询；60s 复用；403 强制刷新一次 | **P0** |
| INF-IAM-02 | 路由权限 meta | L0 | L2 | `meta.permission` 驱动守卫；未授权渲染 403 且保留导航上下文 | **P0** |
| INF-IAM-03 | 按钮级权限 | L1 | L2 | `usePermission` / `<Can>` 统一实现；禁止页面散落权限码 | **P0** |
| INF-IAM-04 | 菜单生成 | L0 | L2 | 静态路由表 + 权限快照过滤；B 端不出现 A 端权限码 | **P0** |
| INF-IAM-05 | 权限不可用 fail-closed | L0 | L2 | 50300/网络失败展示不可用页，禁止先放行 | **P0** |
| INF-IAM-06 | 40900 审批引导 | L0 | L2 | IAM 写操作映射为“需审批”，预填审批单，禁止自动重试 | **P0** |
| INF-IAM-07 | 审批职责分离（SoD） | L0 | L2 | 申请人不得操作自己的通过/拒绝按钮 | P1 |
| INF-IAM-08 | 数据范围 UI 约束 | L0 | L2 | 明显越界的批量操作禁用；跨商户按 404 处理 | P1 |

### 4.5 UI 设计系统与交互基座

| 编号 | 能力 | 现状 | 目标 | 验收标准 | 优先级 |
|---|---|---|---|---|---|
| INF-UI-01 | 设计令牌与主题 | L2 | L3 | 颜色/圆角/字号只来自 `packages/ui`；禁止页面散落常量 | 已具备 |
| INF-UI-02 | 状态组件 | L2 | L3 | Loading/Empty/Error/403/404 统一；错误带 trace_id | 已具备 |
| INF-UI-03 | 表格容器 | L0 | L2 | 服务端分页、筛选栏、列设置、空/错/加载态、`placeholderData` | **P0** |
| INF-UI-04 | 表单基座 | L1 | L2 | RHF + Zod 统一接入；标签布局、字段错误、未保存离开提示 | **P0** |
| INF-UI-05 | 弹窗与确认 | L0 | L2 | Modal/Drawer/Popconfirm 薄封装；危险操作红色主按钮 + 影响说明 | **P0** |
| INF-UI-06 | 全局通知 | L0 | L2 | 成功/失败 toast；失败可复制 trace_id；禁止页面直接调 message | **P0** |
| INF-UI-07 | 页面骨架 | L0 | L2 | PageHeader/面包屑/Skeleton/筛选区布局统一 | **P0** |
| INF-UI-08 | 状态与枚举展示 | L1 | L2 | 状态 Tag、字典映射集中；禁止页面写死颜色与中文 | **P0** |
| INF-UI-09 | 大表格与虚拟滚动 | L0 | L2 | 超过 200 行启用虚拟滚动；列设置可持久化（非敏感） | P2 |
| INF-UI-10 | 上传/日期/导出组件 | L0 | L2 | 统一封装；导出走服务端任务，禁止前端循环翻页 | P2 |

### 4.6 可观测与前端安全

| 编号 | 能力 | 现状 | 目标 | 验收标准 | 优先级 |
|---|---|---|---|---|---|
| INF-OBS-01 | 统一 logger | L0 | L2 | 生产禁用 console；默认脱敏；仅装配层可输出诊断 | **P0** |
| INF-OBS-02 | 错误上报通道 | L0 | L2 | 上报 release/commit/路由/trace_id；不含表单内容与令牌 | **P0** |
| INF-OBS-03 | 路由级错误边界 | L1 | L2 | 区块级局部重试；chunk 加载失败自动刷新一次 | **P0** |
| INF-OBS-04 | Web Vitals 采集 | L0 | L2 | LCP/INP/CLS/TTFB 按采样率上报 | P1 |
| INF-OBS-05 | 发布标识可视化 | L1 | L2 | 关于/诊断页展示版本与 contract_commit | P1 |
| INF-OBS-06 | CSP 与安全头 | L1 | L2 | 部署期 nonce 注入；`frame-ancestors 'none'`；生产无 Source Map | P1 |
| INF-OBS-07 | 敏感信息防泄漏 | L2 | L3 | gitleaks + Review 覆盖；日志/URL/存储无令牌与票据 | P1 |

### 4.7 性能与体验

| 编号 | 能力 | 现状 | 目标 | 验收标准 | 优先级 |
|---|---|---|---|---|---|
| INF-PERF-01 | 路由级分包 | L2 | L3 | 每路由独立 chunk；入口不预载 antd | 已具备 |
| INF-PERF-02 | 体积预算 | L3 | 保持 | `pnpm size` 阻断 | 已具备 |
| INF-PERF-03 | 运行时性能门禁 | L0 | L2 | Lighthouse CI：LCP ≤2.5s、CLS ≤0.1 | P1 |
| INF-PERF-04 | 预取策略 | L0 | L2 | 菜单 hover / intent 预取；不预取低频大数据 | P2 |
| INF-PERF-05 | 图片与静态资源 | L0 | L2 | CDN、avif/webp、明确宽高、懒加载 | P2 |

### 4.8 测试与质量

| 编号 | 能力 | 现状 | 目标 | 验收标准 | 优先级 |
|---|---|---|---|---|---|
| INF-TEST-01 | 单测与覆盖率 | L2 | L3 | 行/函数/分支/语句 ≥70% | 已具备 |
| INF-TEST-02 | 组件测试执行范围 | L0 | L2 | **apps 组件测试纳入 Vitest**；`renderWithProviders` 支持 Router/Auth | **P0** |
| INF-TEST-03 | 契约 Mock 夹具 | L1 | L2 | MSW 覆盖成功/400/401/403/409/429/5xx/超时；与生成类型对齐 | **P0** |
| INF-TEST-04 | 必测场景 | L0 | L2 | 登录/MFA、并发 401、权限 403、fail-closed、B 端隔离、40900 | **P0** |
| INF-TEST-05 | 可访问性 | L1 | L2 | 关键页面 axe 严重问题为 0；键盘可达 | P1 |
| INF-TEST-06 | E2E 分层 | L1 | L2 | PR smoke + nightly 全量；不稳定用例隔离并限期修复 | P1 |
| INF-TEST-07 | 视觉回归 | L0 | L2 | 仅当组件数 ≥30 再评估 ADR | P3 |

### 4.9 发布与运行

| 编号 | 能力 | 现状 | 目标 | 验收标准 | 优先级 |
|---|---|---|---|---|---|
| INF-REL-01 | 运行期配置 | L2 | L3 | `config.js` 覆盖；校验失败 fail-closed；`no-store` | 已具备 |
| INF-REL-02 | 静态服务与缓存头 | L0 | L2 | `index.html`/`config.js` 不缓存；hash 资源 immutable | P1 |
| INF-REL-03 | 镜像与部署模板 | L0 | L2 | Nginx 非 root 镜像；Helm 前后端独立发布与回滚 | P1 |
| INF-REL-04 | Source Map 私有化 | L0 | L2 | 产物不公开；上传私有监控平台并关联 release | P1 |
| INF-REL-05 | 回滚演练 | L0 | L2 | 前端回退上一静态版本；`index.html` 不长缓存 | P1 |

### 4.10 多端与国际化

| 编号 | 能力 | 现状 | 目标 | 验收标准 | 优先级 |
|---|---|---|---|---|---|
| INF-I18N-01 | 文案字典 | L0 | L2 | 业务文案走 key；默认 zh-CN；禁止组件硬编码中文 | **P0（需决策）** |
| INF-I18N-02 | 时区/数字/金额 | L2 | L3 | 统一走 `packages/shared`；禁止页面 `toFixed` | 已具备 |
| INF-MULTI-01 | B 端认证与租户上下文 | L0 | L2 | 独立 Token 域；不发送 `merchant_id`；跨商户按 404 | P3 |

---

## 五、业务页面前必须完成的 7 项（P0 明细）

### 5.1 权限快照、路由守卫与菜单

- **问题**：当前只有纯函数判定，路由无 `meta`，菜单硬编码；页面一旦接入会各自判断权限。
- **影响**：越权展示、菜单与路由不一致、权限服务故障时可能“先放行”。
- **范围**：`PermissionProvider` + `usePermission`/`<Can>` + 路由 `meta.permission` + 菜单过滤 + fail-closed 页。
- **验收**：无权限路由渲染 403 且保留导航；权限服务 50300 时展示不可用页；权限判定有单测。

### 5.2 业务数据层约定

- **问题**：请求管线已闭环，但页面如何组织 Query Key、URL 参数、取消、写后失效尚无约定。
- **影响**：缓存串页、翻页闪烁、重复请求、写后列表不刷新。
- **范围**：`queryKeys` 工厂、`useListParams`（URL 同步 + 上限 100）、`createIdempotencyKey`、mutation 精确失效模板。
- **验收**：至少 2 个示例 feature 按模板实现；切换筛选重置页码；写后列表与详情同步失效。

### 5.3 UI 交互基座第二批

- **问题**：表格、表单布局、弹窗、通知、页面骨架缺失，页面会直接依赖 antd 细节。
- **影响**：交互不一致；未来替换组件库成本高。
- **范围**：TableShell、FormLayout、Modal/Drawer/Popconfirm、通知服务、PageHeader/Breadcrumb、Skeleton、StatusTag。
- **验收**：业务页面只从 `@admin-frame/ui` 导入；危险操作统一二次确认 + 影响说明。

### 5.4 错误码映射、trace_id 与 logger

- **问题**：`AppError` 只有 kind 分类，无统一用户文案；错误展示散落；无上报通道。
- **影响**：用户看到后端原始 message；线上问题无法定位。
- **范围**：`error-code-map`、`logger`（默认脱敏）、`reportError` 抽象、全局错误提示组件。
- **验收**：8 个错误码均有文案与展示位置；错误提示可复制 trace_id；生产无 `console.*`。

### 5.5 高风险操作套件

- **问题**：IAM 写操作需要确认、原因、幂等键、40900 审批引导，目前无统一组件。
- **影响**：误操作、重复提交、把审批当冲突重试。
- **范围**：`DangerActionConfirm`（对象/影响摘要/原因 ≤200 字）、幂等键生成、40900 → 审批引导映射。
- **验收**：示例写操作完整走通；40900 不自动重试；审批引导预填 payload。

### 5.6 i18n 决策与最小落地

- **问题**：文案硬编码中文，后续多语言改造面大。
- **影响**：返工成本随页面数量线性增长。
- **范围**：先建立 `zh-CN` 字典与 `t()` 入口，业务文案走 key；不引入完整多语言切换。
- **验收**：新增页面无硬编码业务文案；ESLint 自定义规则或 Review 约束生效。

### 5.7 测试基座补强

- **问题**：`vitest` 不执行 apps 测试；MSW 仅 4 个端点；无 axe。
- **影响**：页面逻辑无回归保护，权限与错误流不可验证。
- **范围**：Vitest `include` 纳入 `apps/**`；`renderWithProviders` 支持 Router/Auth；补齐契约夹具；axe 断言。
- **验收**：示例页面有组件测试；关键页面 axe 严重问题为 0；覆盖率保持 ≥70%。

---

## 六、目标基础设施拓扑

```text
main.tsx
  └─ loadRuntimeConfig（fail-closed）
      └─ App
          ├─ AppErrorBoundary（根）
          ├─ ThemeProvider（令牌 + CSP nonce）
          ├─ ObservabilityProvider（logger / Web Vitals / 上报）
          ├─ I18nProvider（zh-CN 字典）
          ├─ QueryClientProvider（重试/缓存/取消规范）
          ├─ AuthProvider（TokenStore + RefreshManager + SessionRuntime）
          ├─ PermissionProvider（快照查询 + fail-closed）
          └─ RouterProvider
              ├─ /login（full layout）
              └─ RequireAuth → RequirePermission → AppLayout（菜单由路由表过滤）
                  └─ pages → features → entities → shared
                        └─ packages/{api-client,auth,ui,shared}
```

**装配原则**

- Provider 只做装配与状态编排，不含业务逻辑。
- 权限、日志、i18n、通知均以 Context/服务形式提供，页面不得绕过。
- 所有跨页面能力先进 `packages/*` 或 `entities`，禁止复制到多个 feature。

---

## 七、实施路线

| 阶段 | 范围 | 退出标准 | 依赖 |
|---|---|---|---|
| **S1 权限与数据层** | 5.1 + 5.2 | 权限守卫与菜单可用；示例列表页按 Query/URL 规范接入 | 认证切片（已完成） |
| **S2 UI 基座与交互** | 5.3 + 5.5 | 表格/表单/弹窗/通知/危险操作套件可用 | S1 |
| **S3 错误、可观测与 i18n** | 5.4 + 5.6 | 错误码映射、logger、trace_id、文案字典就绪 | S1 |
| **S4 测试与发布硬化** | 5.7 + 4.8/4.9 | apps 测试纳入、axe、缓存头、镜像/Helm、回滚演练 | S2 |
| **S5 会话与 MFA 管理** | 4.3 的 P1 项 | 会话页、MFA 绑定/解绑、空闲超时、跨标签页同步 | S1 |
| **S6 B 端** | 4.10 + 4.4 租户隔离 | B 端登录/IAM 可用；不发送 `merchant_id`；跨商户 404 | S1～S3 |

> 建议业务页面在 **S1 + S2 完成后开工**；S3 可与首批页面并行，但错误提示与文案规范必须先定。

---

## 八、评审需决策项

| 编号 | 决策 | 选项 | 建议 |
|---|---|---|---|
| D-01 | 监控选型 | Sentry / OpenTelemetry Web + 自有 collector | 优先 OTel Web，满足私有化与后端 trace 对齐 |
| D-02 | i18n 深度 | 仅 zh-CN 字典 / 完整多语言切换 | 首期只做字典与 `t()`，切换能力等有明确诉求再引入 |
| D-03 | 权限快照策略 | 每次路由进入 / Provider 缓存 60s | Provider 缓存 + 403 强制刷新一次 |
| D-04 | 菜单来源 | 路由表 / 后端菜单接口 | 路由表为准；契约补齐菜单元数据后再评估切换 |
| D-05 | UI 封装深度 | 薄封装 / 自建组件 DSL | 薄封装 + 组合式页面模板，避免维护第二套组件库 |
| D-06 | Storybook / 视觉回归 | 现在引入 / 组件 ≥30 再评估 | 延后，先保证 axe 与组件测试 |
| D-07 | 刷新令牌 HttpOnly | 继续 sessionStorage / 推动后端 Cookie | 推动后端，前端保留 `TokenStore` 适配器 |
| D-08 | 强制绑定 MFA 契约 | 临时票据 / 专用错误码 | **已定稿**：登录 200 新分支 + `mfa_setup_ticket`，首个管理员由 `identity-seed` 开通；详见 [`identity-authn.md`](./identity-authn.md) §7.2.1 |
| D-09 | B 端启动时点 | 与 A 端并行 / A 端首批页面后 | A 端 S1/S2 完成后启动，复用 Provider |

---

## 九、度量与验收

| 指标 | 目标 | 采集方式 |
|---|---|---|
| 单测覆盖率（行/函数/分支/语句） | ≥70% | `pnpm test:ci` |
| 入口体积 | admin ≤300KB、biz ≤260KB（gzip） | `pnpm size` |
| 单路由体积 | ≤120KB（gzip） | `pnpm size` |
| LCP / INP / CLS（P75） | ≤2.5s / ≤200ms / ≤0.1 | Lighthouse CI + RUM |
| 关键页面 axe 严重问题 | 0 | axe 断言 |
| 业务页面权限覆盖 | 100% 路由配置 `meta.permission` | Review Checklist |
| 错误可定位率 | ≥90% 错误展示或上报携带 trace_id | 监控看板 |
| 门禁通过率 | `frontend-ci` 无静默放宽 | CI 记录 |

---

## 十、明确不做（防过度设计）

- 不引入 SSR/Next.js：A/B 中后台收益低，增加运行时与运维面。
- 不做微前端（Module Federation）：当前团队与业务边界不支持。
- 不自建组件库或 fork antd：只做薄封装与设计令牌。
- 不引入第二套状态管理：服务端状态只用 TanStack Query。
- 不复制后端权限规则：前端只做可见性与提前拦截。
- 不为假设需求预建：导出中心、图表、暗色主题等按需走 ADR。

---

## 附录 A：现有资产索引

| 资产 | 路径 |
|---|---|
| 前端架构规范 | [frontend-architecture.md](./frontend-architecture.md) |
| Monorepo 配置 | `frontend/package.json`、`frontend/pnpm-workspace.yaml`、`frontend/tsconfig.base.json` |
| ESLint 基线 | `frontend/packages/config/src/eslint-base.mjs` |
| 依赖边界规则 | `frontend/dependency-cruiser.cjs` |
| 契约生成 | `frontend/scripts/gen-api.mjs`、`frontend/packages/api-client/src/generated/` |
| 请求管线 | `frontend/packages/api-client/src/http/client.ts` |
| 认证能力 | `frontend/packages/auth/src/{token-store,refresh-manager,session-runtime}.ts` |
| 认证装配 | `frontend/apps/admin-web/src/app/auth/` |
| 设计系统 | `frontend/packages/ui/src/` |
| 测试辅助 | `frontend/packages/testing/src/` |
| E2E | `frontend/e2e/admin/`、`frontend/e2e/biz/` |
| CI 门禁脚本 | `scripts/ci/check-frontend.sh`、`scripts/ci/check-frontend-boundaries.sh` |

## 附录 B：本地门禁命令

```bash
make frontend-install
make frontend-gen
make frontend-lint
make frontend-format
make frontend-typecheck
make frontend-boundaries
make frontend-test
make frontend-build
make frontend-size
make frontend-audit
make frontend-e2e
make frontend-ci
```

## 附录 C：变更记录

| 日期 | 版本 | 说明 |
|---|---|---|
| 2026-10-25 | v0.1 | 初稿：基于 P0 底座与认证切片的现状评估，提出 7 项 P0 前置能力 |

---

## Review Checklist

- [ ] 能力清单是否覆盖了首批业务页面会复用的全部横切能力
- [ ] P0 / P1 / P2 的划分是否符合当前交付节奏
- [ ] 决策项 D-01～D-09 是否已有结论或明确 Owner
- [ ] 是否存在重复建设（与现有 `packages/*` 能力重叠）
- [ ] 验收标准是否都能落到脚本、测试或 Review Checklist
- [ ] 是否遗漏安全、合规或私有化部署的强制要求
