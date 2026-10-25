# 前端架构与工程规范（A/B 双端）

> **版本**：v1.0
> **状态**：生效中（Normative）
> **适用范围**：仓库内 `frontend/` 下的 A 端（`admin-web`）与 B 端（`biz-web`），以及共享 `packages/*`
> **读者**：前端工程师、后端接口负责人、Code Reviewer、SRE、安全与架构 Owner
> **最后更新**：2026-10-25
> **决策记录**：[ADR-0005](./adr/0005-frontend-monorepo-react.md)
> **关联硬约束**：
> - [架构总览](./ARCHITECTURE.md)（分层、三端隔离、依赖方向）
> - [API 契约规范](./API_STANDARDS.md)（API-01～API-12，§3.2 契约分发与前端订阅）
> - [CI 流水线规范](./CI_STANDARDS.md)（CI-01～CI-14）
> - [安全规范](./SECURITY_STANDARDS.md)（SEC-01～SEC-14）
> - [测试规范](./TESTING_STANDARDS.md)（TEST-01～TEST-08）
> - [代码生成规范](./CODEGEN_STANDARDS.md)（GEN-01～GEN-07）与 [MANIFEST](./codegen/MANIFEST.md)
> - [依赖与供应链规范](./DEPENDENCY_STANDARDS.md)（DEP-01～DEP-08）、[许可证规范](./LICENSE_STANDARDS.md)（LIC-01～LIC-07）
> - [发布规范](./RELEASE_STANDARDS.md)（RELS-01～RELS-08）、[Git 规范](./GIT_STANDARDS.md)（GIT-01～GIT-08）
> **变更方式**：目录、分层、技术选型、鉴权/契约方案变更须先提交 ADR，经评审合入后同步更新本文与相关门禁脚本。

---

## 目录

1. [目标与非目标](#一目标与非目标)
2. [架构决策摘要](#二架构决策摘要)
3. [总体架构](#三总体架构)
4. [仓库与目录结构](#四仓库与目录结构)
5. [分层规则与依赖边界](#五分层规则与依赖边界)
6. [A/B 双端隔离](#六ab-双端隔离)
7. [路由、菜单与权限](#七路由菜单与权限)
8. [状态管理](#八状态管理)
9. [API 契约层与请求生命周期](#九api-契约层与请求生命周期)
10. [认证、会话与前端安全](#十认证会话与前端安全)
11. [UI、设计系统与交互规范](#十一ui设计系统与交互规范)
12. [错误处理与可观测性](#十二错误处理与可观测性)
13. [性能与容量](#十三性能与容量)
14. [测试策略](#十四测试策略)
15. [代码规范硬约束](#十五代码规范硬约束)
16. [工程门禁与 CI](#十六工程门禁与-ci)
17. [配置、环境与部署](#十七配置环境与部署)
18. [与后端契约的缺口](#十八与后端契约的缺口)
19. [落地路线](#十九落地路线)
20. [风险与缓解](#二十风险与缓解)
21. [Review Checklist](#二十一review-checklist)

---

## 一、目标与非目标

### 1.1 目标

| 目标 | 含义 |
|---|---|
| **契约优先** | 前端只消费 `api/openapi/{admin,biz}.yaml` 生成的类型，禁止手写接口类型或探测运行中的服务 |
| **端隔离** | A/B 两套独立构建、独立域名、独立 Token 域与存储命名空间，禁止跨端 Token / DTO / 构建产物复用 |
| **分层清晰** | `app → pages → features → entities → shared` 单向依赖，跨 feature 只能在上层组合，禁止网状引用 |
| **服务端权威** | 权限、数据范围、审批、审计由后端判定；前端只做展示与提前拦截，**默认拒绝** |
| **生产就绪** | 安全基线（CSP、Token 存储、XSS/CSRF）、可观测、性能预算、可回滚发布在首期即具备 |
| **可强制** | 格式、类型、Lint、边界、测试、构建、体积、许可证、漏洞全部有本地与 CI 门禁 |
| **可演进** | 共享内核与按端差异分离，后续新增 C 端或替换 UI 库时改动可控 |

### 1.2 非目标

- 不在首期实现 C 端消费者应用；C 端的 SSR/SEO、性能预算与字段最小化另立 ADR。
- 不为 A/B 中后台引入 SSR / Next.js：收益低，且会引入 Node 运行时与额外运维面。
- 不在前端复制后端业务规则（权限判定、数据范围、审批状态机以后端为准）。
- 不建设前端微前端（Module Federation）。当前团队与业务边界未达到需要拆分的程度，先以 monorepo + 包边界治理。
- 不修改后端公开契约语义；需要的后端增强在[第十八章](#十八与后端契约的缺口)登记，按 API 契约流程推进。

---

## 二、架构决策摘要

| 类别 | 决策 | 说明 |
|---|---|---|
| 语言 | TypeScript 5.x，`strict: true` | 禁止隐式 `any`；`noUncheckedIndexedAccess`、`exactOptionalPropertyTypes` 开启 |
| 框架 | React 19 | 函数组件 + Hooks；不引入类组件新代码 |
| 构建 | Vite 7 | 按端独立构建；产物为纯静态资源 |
| 包管理 | pnpm workspace | `packageManager` 钉扎版本；`save-exact=true`；`--frozen-lockfile` |
| 路由 | React Router 7（Data Router） | 路由即权限/菜单/懒加载的单一描述源 |
| 服务端状态 | TanStack Query v5 | 缓存、失效、重试、取消统一收口；禁止把服务端数据塞进全局 store |
| 客户端状态 | Zustand | 仅存会话元信息、主题、布局等非敏感 UI 状态 |
| 表单 | React Hook Form + Zod | Zod 同时作为输入边界校验与类型来源 |
| UI | Ant Design 5 + 自建设计系统包 | 业务代码统一从 `@admin-frame/ui` 导入，隔离第三方组件库 |
| 契约类型 | openapi-typescript（钉扎 7.4.0） | 由仓库 OpenAPI 源生成；生成物入库、CI 校验无漂移、禁止手改 |
| 单元/组件测试 | Vitest + Testing Library + MSW | MSW 处理器与契约类型对齐 |
| E2E | Playwright | 按端分目录，复用后端真实进程或契约 Mock |
| 代码质量 | ESLint 9 Flat + typescript-eslint（type-checked）+ jsx-a11y + 边界插件 | `--max-warnings=0` |
| 格式 | Prettier | 只检查不写回的门禁模式与后端 `fmt-check` 对齐；样式文件同样由 Prettier 处理 |
| 安全 | CSP、内存优先 Token、单飞刷新、无第三方脚本 | 细则见第十章 |
| 部署 | 静态资源 + 同源 Ingress + Nginx 非 root 镜像 | 与 API_STANDARDS §3.1 域名/前缀对齐 |
| 仓库 | 同仓 `frontend/` monorepo | 与 OpenAPI 源、CI 同 commit 锁定，契约零漂移 |

> 选型变更须走 ADR，并同步更新本表、`frontend/package.json` 与门禁配置。

---

## 三、总体架构

```text
                    ┌──────────────────────────────────────────┐
                    │            浏览器（同源部署）              │
                    │  admin.example.com     biz.example.com   │
                    └───────────────┬──────────────┬───────────┘
                                    │              │
                        静态资源 + SPA  │              │ 静态资源 + SPA
                                    v              v
                    ┌──────────────────────────────────────────┐
                    │   CDN / Ingress（TLS、CSP、缓存策略）       │
                    │   /admin/* → admin-api   /biz/* → biz-api │
                    └───────────────┬──────────────┬───────────┘
                                    │              │
                          /admin/v1 │              │ /biz/v1
                                    v              v
                    ┌──────────────────────────────────────────┐
                    │        admin-api            biz-api       │
                    │  统一信封 code/message/data/trace_id      │
                    │  JWT（端别隔离）+ IAM 权限快照 + 审计       │
                    └──────────────────────────────────────────┘
```

前端 monorepo 内部：

```text
apps/admin-web ─┐
apps/biz-web  ─┼─► packages/api-client ─► packages/shared
                ├─► packages/auth ──────► packages/api-client
                ├─► packages/ui ────────► packages/shared
                └─► packages/testing（仅测试）
```

**核心约束**

1. 两个 app 之间零引用；共享能力下沉到 `packages/*`。
2. `packages/*` 不得反向依赖 `apps/*`。
3. 只有 `packages/api-client` 允许发起 HTTP 请求；业务代码不得直接使用 `fetch`/`axios`。
4. 只有 `packages/auth` 允许读写令牌；业务代码不得直接访问 `window.sessionStorage` 中的令牌键。
5. 契约类型只由 `api/openapi/**` 生成，业务代码不得手写接口响应结构。

---

## 四、仓库与目录结构

```text
frontend/
├── pnpm-workspace.yaml
├── package.json                     # 根脚本：format / lint / typecheck / boundaries / test / build / gen:api / size / license / audit / e2e
├── pnpm-lock.yaml                   # 必须入库，CI 使用 --frozen-lockfile
├── .npmrc                           # save-exact=true；strict-peer-dependencies=true
├── tsconfig.base.json               # strict 基线，供各包 extends
├── tsconfig.json                    # 根解决方案配置（供 IDE 与 ESLint 解析）
├── eslint.config.js                 # Flat Config 入口（引用 packages/config）
├── prettier.config.mjs              # 格式配置（.prettierignore 为排除清单）
├── dependency-cruiser.cjs           # 分层/跨包边界规则
├── playwright.config.ts             # E2E 配置
├── vitest.config.ts                 # 单测配置
├── vitest.setup.ts                  # 单测全局 setup
├── license-allowlist.json           # 许可证白名单（FE-17 / LIC）
├── scripts/                         # gen-api.mjs / check-size.mjs / check-licenses.mjs
├── apps/
│   ├── admin-web/                   # A 端：平台运营
│   │   ├── index.html
│   │   ├── vite.config.ts
│   │   ├── tsconfig.json
│   │   ├── public/config.js         # 运行期配置（非敏感，部署时可覆盖）
│   │   └── src/
│   │       ├── app/                 # 启动、Provider、路由、布局：只装配不写业务
│   │       ├── pages/               # 路由级页面组合（login、iam、audit、sessions…）
│   │       ├── features/            # 业务能力切片（auth、iam-roles、iam-bindings、approval…）
│   │       ├── entities/            # 业务实体类型与展示（principal、role、permission…）
│   │       └── shared/              # 本端通用 hooks/lib/constants（无业务语义）
│   └── biz-web/                     # B 端：商户后台（结构同上，差异仅在装配与页面）
│       └── ...
├── packages/
│   ├── api-client/                  # 生成类型 + HTTP 管线 + 分端 endpoint 模块
│   │   └── src/
│   │       ├── generated/           # 生成物（入库、带生成头、禁止手改）
│   │       ├── http/                # fetch 封装、超时、重试、错误归一、trace_id
│   │       ├── endpoints/admin/     # A 端接口函数（*.api.ts）
│   │       ├── endpoints/biz/       # B 端接口函数（*.api.ts）
│   │       ├── errors/              # 业务错误码映射
│   │       ├── admin-client.ts      # createAdminClient（A 端工厂）
│   │       ├── biz-client.ts        # createBizClient（B 端工厂）
│   │       └── types.ts             # 面向业务的领域类型别名
│   ├── auth/                        # 令牌存储、单飞刷新、登录态、权限守卫原语
│   ├── ui/                          # 设计令牌、基础组件封装、状态组件
│   ├── shared/                      # 运行期配置、日期、金额、脱敏、通用工具（无业务）
│   ├── config/                      # ESLint / Vitest 预设（tsconfig 基线在根 tsconfig.base.json）
│   └── testing/                     # MSW handlers、fixtures、renderWithProviders
└── e2e/
    ├── admin/                       # Playwright 用例（A 端）
    └── biz/                         # Playwright 用例（B 端）
```

> `features/`、`entities/`、`shared/` 与 `e2e/fixtures/` 自 P1 起按需创建，P0 仅落地 `app` 与 `pages`。测试渲染辅助统一放在 `packages/testing`，各 app 不再单设 `tests/` 目录。

**命名约定**

| 类型 | 规则 | 示例 |
|---|---|---|
| 目录 | kebab-case | `iam-roles/`、`change-requests/` |
| React 组件文件 | PascalCase | `RoleTable.tsx`、`LoginForm.tsx` |
| hooks / 工具 / 类型 | kebab-case | `use-roles.ts`、`format-date.ts`、`role.types.ts` |
| API 模块 | `*.api.ts` | `iam-roles.api.ts` |
| Store | `*.store.ts` | `session.store.ts` |
| 测试 | `*.test.ts(x)` / `*.spec.ts(x)` | `role-table.test.tsx` |
| 常量 | 全大写 + 下划线（导出） | `MAX_PAGE_SIZE` |

> 组件文件用 PascalCase 是为了与 React DevTools 一致；其余文件统一 kebab-case，避免 Windows/macOS 大小写不敏感导致的重命名冲突。
> `packages/ui` 内的基础组件沿用 kebab-case 文件名（如 `empty-state.tsx`），对外导出名保持 PascalCase，属于 FE-05 的既有例外。

---

## 五、分层规则与依赖边界

### 5.1 层职责

| 层 | 职责 | 禁止 |
|---|---|---|
| `app` | 入口装配：Provider、Router、全局错误边界、布局、启动期配置校验 | 业务逻辑、数据请求、权限判断细节 |
| `pages` | 路由级组合：把多个 feature/entity 拼成页面，处理页面级 loading/error/权限元信息 | 直接写 HTTP、直接操作 Token、复制业务规则 |
| `features` | 单个业务能力的 UI + hooks + 局部状态；对 `entities`/`shared` 单向依赖 | 引用其他 feature 的内部文件 |
| `entities` | 业务实体的类型、展示组件、纯函数（如角色状态文案） | 发起请求、依赖路由、依赖 feature |
| `shared`（app 内） | 本端通用 hooks、常量、工具；不含业务语义 | 依赖 features/pages |
| `packages/*` | 跨端复用的基础设施与设计系统 | 依赖 apps；包含端别业务分支 |

### 5.2 允许的依赖方向

```text
app → pages → features → entities → shared
  │        │        │         │
  └────────┴────────┴─────────┴──────► packages/{ui,api-client,auth,shared}
```

- 同层不允许横向 import：`features/iam-roles` **不得** import `features/iam-bindings`。需要组合时由 `pages` 组合，或把共用部分下沉到 `entities`/`shared`。
- 每个 feature 必须有 `index.ts` 公共出口；跨层只能从出口导入，禁止深链 `features/x/internal/...`。
- `packages` 内部依赖图固定为：`shared ← api-client ← auth`、`api-client ← testing`、`ui ← testing`（测试渲染辅助）；`ui` 不依赖接口与鉴权包。禁止反向或成环。

### 5.3 强制手段

| 规则 | 工具 |
|---|---|
| 目录分层与跨 feature 引用 | `dependency-cruiser` + ESLint `import/no-restricted-paths` |
| 包公共出口 | ESLint `import/no-internal-modules`（限制深链） |
| `apps/*` 互相引用 | dependency-cruiser forbidden 规则 |
| HTTP 只允许在 api-client | ESLint `no-restricted-globals` / `no-restricted-imports`（禁止 `axios`，限制 `fetch`） |
| 生成物禁止手改 | CI 比对生成物哈希 + 文件头 `DO NOT EDIT` |

> 门禁脚本：`scripts/ci/check-frontend-boundaries.sh`（落地时新增），本地入口 `make frontend-boundaries`。

---

## 六、A/B 双端隔离

| 维度 | A 端 `admin-web` | B 端 `biz-web` | 约束 |
|---|---|---|---|
| 域名 | `admin.example.com` | `biz.example.com` | 与 API_STANDARDS §3.1 一致 |
| API 前缀 | `/admin/v1` | `/biz/v1` | 构建期固定；运行时断言 |
| 构建产物 | 独立 `dist/`、独立镜像/静态目录 | 同左 | 禁止一份产物多端复用 |
| Token 域 | A 端 issuer/audience | B 端独立 issuer/audience | 前端不得解码后跨端使用 |
| 存储命名空间 | `admin-frame:admin:*` | `admin-frame:biz:*` | 禁止共用 key |
| 租户上下文 | 平台级 | `merchant_id` 来自 Token/服务端 | 前端只展示，**禁止**作为请求参数或路由参数 |
| 权限来源 | `/admin/v1/iam/me/permissions` | `/biz/v1/iam/me/permissions` | 服务端快照为唯一权威 |
| 菜单可见范围 | 平台功能（含 IAM/审计/配置） | 商户内功能（含商户 IAM） | B 端不得出现 A 端权限码 |

**Channel 固定**

- `__CHANNEL__` 由 Vite `define` 在构建期注入（`admin` / `biz`）；启动时与运行期 `config.js` 的 `channel` 比对，不一致直接渲染致命错误页并上报（`loadRuntimeConfig`）。
- 禁止通过 URL、localStorage 或运行时参数切换端别；与后端“装配期固定 Channel”保持一致，防止跨端提权。
- `packages/api-client` 提供 `createAdminClient()` / `createBizClient()` 两个工厂，类型与路径在编译期分离；app 只能使用对应工厂。

**租户隔离（B 端）**

- 前端不发送 `merchant_id` 作为筛选或越权依据；列表/详情一律依赖服务端按 Principal 过滤。
- 跨商户资源按后端语义统一展示为“不存在”（`40400`），前端不得提示“无权限访问他人数据”，避免资源枚举。
- 切换商户（未来多商户账号）只能通过服务端切换会话/凭证；禁止客户端改请求头。

---

## 七、路由、菜单与权限

### 7.1 路由即单一描述源

每个 app 维护一份静态路由表，路由 `meta` 同时驱动菜单、面包屑、权限与预加载：

```ts
type RouteMeta = {
  title: string;
  permission?: string | string[];   // 任一满足即可；空数组表示仅需登录
  layout: 'full' | 'shell';
  menu?: { group: string; order: number; icon?: string };
  breadcrumb?: string[];
};
```

- 页面组件一律 `React.lazy` + 路由级 `Suspense`，按路由分包。
- 登录页、403、404 使用 `layout: 'full'`，不加载主框架与菜单数据。
- 路由路径避开 `/admin`、`/biz` 前缀（这些前缀保留给 API 与 Ingress），前端页面使用 `/login`、`/iam/roles` 等路径。

### 7.2 守卫顺序

```text
加载会话 → 未登录跳转 /login?returnTo=... → 拉取权限快照 → 路由权限判定
        → 无权限渲染 403（不跳转，保留导航上下文） → 权限通过后加载页面数据
```

- 权限快照 60s 内复用；收到 `40300` 时强制刷新一次快照并重新判定，仍失败则 403。
- 权限接口不可用（`50300`/网络失败）时 **fail-closed**：显示不可用页，不允许“先放行再补校验”。
- 菜单由静态路由表 + 权限快照过滤生成；后端 `/{channel}/v1/iam/permissions/tree` 提供权限点树（`kind` 含 `menu`/`button`），可作为权限目录与授权页的数据源，但不含图标/排序/布局等菜单元数据，因此菜单结构仍以路由表为准。

### 7.3 按钮级与高风险操作

- 统一 `usePermission(code)` 与 `<Can permission="iam:role:write">`；只做可见性控制，**不承担安全职责**。
- 高风险写操作（角色创建/删除、权限替换、绑定、破玻璃、配置修改）必须：
  1. 二次确认弹窗，展示影响对象与变更摘要；
  2. 采集操作原因（≤ 200 字），随请求提交（契约支持时）或写入前端审计提示；
  3. 生成 `Idempotency-Key`（UUIDv4），同一次提交的重试复用同一 key；
  4. 成功后刷新相关 query 并提示审计已记录。
- `iam.approval.enabled` 开启时，IAM 直写端点返回 `40900`。前端对 IAM 写操作的 `40900` 做专用映射：提示“该操作需审批”，并提供“发起审批单”入口（预填 payload 跳转审批创建页），**禁止**把 `40900` 当作普通冲突自动重试。
- 审批页必须遵守职责分离：申请人不得看到自己的“通过/拒绝”可用按钮（`requested_by === 当前 subject` 时禁用并说明）。

---

## 八、状态管理

### 8.1 状态归属矩阵

| 状态类型 | 存放位置 | 示例 | 规则 |
|---|---|---|---|
| 服务端数据 | TanStack Query | 角色列表、审批单、会话列表 | 唯一来源；禁止复制进 Zustand |
| URL 状态 | `useSearchParams` | 分页、筛选、排序、Tab | 可分享、可回退；刷新后保持 |
| 表单状态 | React Hook Form | 登录、角色编辑 | Zod 校验；提交后重置或失效缓存 |
| 局部 UI 状态 | `useState` / `useReducer` | 抽屉开关、选中行 | 不上提全局 |
| 全局客户端状态 | Zustand | 当前主体摘要、主题、侧栏折叠 | 只存非敏感、可重建数据 |
| 令牌 | `packages/auth` 内部 | access/refresh | 业务代码不可直接读写 |

### 8.2 Query Key 约定

```ts
// 按「端别 → 领域 → 资源 → 形态 → 参数」组织
['admin', 'iam', 'roles', 'list', { page, pageSize, status, keyword }]
['admin', 'iam', 'roles', 'detail', roleId]
['biz', 'iam', 'bindings', 'list', { page, pageSize }]
```

- 每个 feature 提供 `queryKeys` 工厂，禁止散落字符串。
- 参数对象必须稳定序列化（去除 `undefined`、排序 key），避免无效刷新。
- 写操作成功后按“精确失效”原则刷新：列表 + 受影响详情 + 权限快照（如涉及授权）。
- 乐观更新仅允许在低风险、可回滚场景使用，并在代码注释中写明回滚策略。

### 8.3 缓存与刷新策略

| 数据类型 | `staleTime` | 说明 |
|---|---|---|
| 权限快照 | 60s | 收到 403 时强制 `refetch` 一次 |
| 列表/详情 | 30s | 写后主动失效，不依赖过期 |
| 字典/枚举 | 10min | 权限目录等低频数据 |
| 会话列表 | 0 | 每次进入页面刷新 |

- 全局默认：`retry: 1`（仅网络错误/5xx），`refetchOnWindowFocus: false`（中后台避免误刷新表单），显式页面可开启。
- 组件卸载/路由切换时通过 `AbortSignal` 取消在途请求。

---

## 九、API 契约层与请求生命周期

### 9.1 契约生成流水线

```text
api/openapi/{admin,biz}.yaml + components.yaml   （手写源，GEN-06）
        │
        ├─ pnpm gen:api（openapi-typescript 7.4.0）
        ▼
packages/api-client/src/generated/{admin,biz}.ts  （入库；带生成头；禁止手改）
        │
        ▼
endpoints/admin|biz/*.api.ts  （手写薄封装，只做参数/返回映射）
        │
        ▼
features/*/hooks（TanStack Query）
```

- 生成命令固定版本，生成物按 GEN-02 带 `Code generated` / `DO NOT EDIT` 头并入库；CI 在 `typecheck`/`build` 前重新生成并执行 `git diff --exit-code`，发现漂移即阻断（GEN-01/GEN-03）。
- 生成目录在 [MANIFEST](./codegen/MANIFEST.md) 登记为 `frontend-api-ts`；首次产出文件时将状态由 `reserved` 改为 `active`。
- `packages/api-client` 的公共出口只导出**领域类型别名**与 endpoint 函数；业务代码禁止直接 import `generated/*`。
- 同仓即同 commit：构建时由 Vite `define` 注入 `__CONTRACT_COMMIT__`（取自 `git rev-parse --short HEAD`），在“关于/诊断”页展示；跨仓部署时以 `dist/openapi/contract-commit.txt` 核对（API_STANDARDS §3.2）。
- 契约有 breaking 时，前端升级必须与后端 major 版本同 MR 或同发布窗口，禁止“先合并前端、后补后端”。

### 9.2 HTTP 管线（唯一出口）

`packages/api-client/src/http/client.ts` 统一实现：

| 能力 | 规则 |
|---|---|
| Base URL | 运行时配置优先，默认同源；禁止在业务代码拼接域名 |
| 超时 | 普通请求 15s；导出/上传单独配置；全部通过 `AbortSignal` |
| 重试 | 仅 GET/幂等写 + 网络错误/5xx；指数退避 + 抖动，最多 2 次；4xx 不重试 |
| 信封解包 | `code === 0` 返回 `data`；非 0 抛 `AppError` |
| 错误归一 | 网络/超时/401/403/校验/业务/服务端统一为 `AppError`，保留 `trace_id` |
| 鉴权头 | 由 `auth` 注入，业务代码不可手写 `Authorization` |
| 幂等键 | 写操作的 endpoint 封装要求显式传入 key；缺失时开发环境直接抛错 |
| 日志脱敏 | 不记录 Token、密码、MFA 票据、完整手机号/证件号 |

### 9.3 统一错误模型

```ts
type AppErrorKind =
  | 'network' | 'timeout' | 'unauthorized' | 'forbidden'
  | 'validation' | 'not_found' | 'conflict' | 'rate_limited'
  | 'server' | 'unavailable' | 'unknown';

type AppError = {
  kind: AppErrorKind;
  code: number;          // 后端稳定业务码；网络错误为 0
  httpStatus?: number;
  message: string;       // 已脱敏、可展示文案
  traceId?: string;
  fieldErrors?: Record<string, string>; // 仅 40000 且契约提供时
};
```

错误码映射（与 API_STANDARDS §4.2 对齐）：

| code | 前端处理 |
|---|---|
| 0 | 成功，返回 `data` |
| 40000 | 表单字段错误优先定位；无字段信息则顶部摘要 |
| 40100 | 触发单飞刷新；刷新失败清理会话并跳登录（带 `returnTo`） |
| 40300 | 渲染 403 或按钮级提示；不自动重试 |
| 40400 | 统一“资源不存在或不可访问”，B 端不暴露跨商户语义 |
| 40900 | 普通冲突提示刷新；**IAM 写操作**转审批引导 |
| 42900 | 展示限流提示与 `Retry-After`（若提供） |
| 50000 | 通用系统错误 + 可复制 `trace_id` |
| 50300 | 依赖不可用，fail-closed，展示服务暂不可用页 |
| 未知码 | 按 `unknown` 兜底，禁止把原始 message 直接透传 |

### 9.4 分页与列表

- `page` 默认 1，`page_size` 默认 20、上限 100；前端控件不得允许超过 100。
- 分页/筛选/排序写入 URL；返回列表至少渲染 `items` 与 `total`。
- 大数据量导出走 A/B 专用导出任务，禁止前端循环翻页拉全量。

---

## 十、认证、会话与前端安全

### 10.1 登录与 MFA

- 登录调用 `POST /{channel}/v1/auth/login`；响应 `data` 可能是 `TokenPair` 或 `MFAChallenge`（`oneOf`），必须做类型窄化。
- `MFAChallenge` 的 `mfa_ticket` 仅存内存，5 分钟有效；禁止写入 URL、日志、持久化存储；过期后回到登录页。
- A 端强制绑定但未完成绑定时，登录返回 `40100`；前端引导至 MFA 绑定流程（`/auth/mfa/setup`），绑定完成前不得进入主框架。
- MFA 初始化返回的 `secret` 与 `recovery_codes` 只展示一次；页面明确提示“离开后不可再次查看”，禁止自动复制到剪贴板之外的位置，也不写入前端日志。

### 10.2 令牌存储

| 令牌 | 目标方案（推荐） | 过渡方案（当前后端契约） |
|---|---|---|
| Access Token | 仅内存 | 仅内存 |
| Refresh Token | 后端 `Set-Cookie: HttpOnly; Secure; SameSite=Lax`（需后端增强） | `sessionStorage`（按 Tab），仅在同源、严格 CSP 下使用 |
| 禁止 | `localStorage`、URL、日志、IndexedDB 长期存储 | 同左 |

- **禁止 `localStorage` 存储任何令牌**，避免长期暴露于 XSS。
- 过渡方案必须在发布说明中标注风险与到期时间；后端支持 HttpOnly Cookie 后，通过 `TokenStore` 适配器切换，业务代码零改动。
- `TokenStore` 支持 `memory` / `session` 两种模式（构造参数），A 端可在装配层切换为 `memory`，实现“刷新页面即重新登录”的最高安全模式；默认按过渡方案使用 `session`。

### 10.3 单飞刷新与多标签页

后端刷新令牌是**一次性轮换**，旧令牌再次出现会撤销整条会话链。因此：

- 任意时刻只允许一个刷新请求（**single-flight**）；其余 401 请求排队等待刷新结果。
- 多标签页跨 Tab 刷新互斥使用 `navigator.locks`（可用时），避免两个 Tab 同时轮换导致重放；`BroadcastChannel` 令牌更新与登出事件同步为 P1 落地项，当前尚未接入。
- 刷新失败、收到 `40100`（非登录接口）或检测到会话被撤销：清理内存/存储、清空 Query 缓存、广播登出、跳转登录页。
- 登出：调用 `logout`（幂等）→ 清理本地会话 → `queryClient.clear()` → 跳转；安全事件使用 `logout-all`。

### 10.4 权限与数据范围

- 权限快照来自 `GET /{channel}/v1/iam/me/permissions`；Token 中不含权限点，前端不得解码 Token 作为授权依据。
- 前端判定仅用于菜单/按钮可见性与路由提前拦截；**服务端始终是最终裁决**。
- 数据范围（`all` / `merchant` / `self` / `merchant_set`）由后端执行；前端只在 UI 上禁用明显越界的批量操作。
- 权限目录与审批状态用后端枚举，前端不复制权限码清单；菜单所需的权限码写在路由 `meta` 中，并与 `docs/iam/PERMISSIONS.md` 保持同步评审。

### 10.5 浏览器安全基线

| 项 | 要求 |
|---|---|
| CSP | `default-src 'self'`；`script-src 'self'`；`connect-src 'self'` + 监控域名；`img-src 'self' data: blob: https:`；`frame-ancestors 'none'`；`object-src 'none'`；`base-uri 'none'` |
| 内联样式 | Ant Design 5 使用 CSS-in-JS，通过 `ConfigProvider csp={{ nonce }}` 注入部署期 nonce；无法注入时才允许 `style-src 'self' 'unsafe-inline'` 并记录风险 |
| XSS | 禁止 `dangerouslySetInnerHTML`（富文本必须经白名单净化）；禁止 `eval`/`new Function`；禁止不可信第三方脚本 |
| CSRF | API 使用 `Authorization` 头；若启用 Cookie 刷新，使用 `SameSite=Lax` + 仅刷新端点，并对状态变更校验 Origin |
| 传输 | 生产强制 HTTPS/HSTS；禁止混合内容 |
| 点击劫持 | `frame-ancestors 'none'` + `X-Frame-Options: DENY` |
| 引用策略 | `Referrer-Policy: strict-origin-when-cross-origin`；`Permissions-Policy` 关闭摄像头/麦克风/定位（按需显式开启） |
| Source Map | 生产不公开；仅上传私有监控平台 |
| 敏感信息 | 构建期常量与运行期 `config.js` 全部视为公开；禁止任何密钥、内部地址、测试账号进入前端产物 |
| 依赖 | 锁文件入库；高危漏洞阻断；许可证白名单；SBOM 可选 |

### 10.6 会话与账号安全交互

- 空闲超时：A 端默认 30 分钟警告、60 分钟强制登出（可配置，需与后端 Token 有效期匹配）。
- 会话管理页展示设备/时间摘要（后端不返回 IP/UA 原文），支持撤销单会话与“登出全部”。
- 密码、MFA 票据、恢复码不得出现在错误提示、监控事件与前端日志中。
- 登录失败提示统一为“账号或密码错误”，不区分账号不存在/停用/锁定（与后端一致）。

---

## 十一、UI、设计系统与交互规范

### 11.1 设计系统边界

- `packages/ui` 封装 Ant Design 5：主题令牌、基础组件、状态组件（`PageLoading`、`EmptyState`、`ErrorState`）、页面骨架（`PageHeader`、`PageSkeleton`、`StatusTag`）、表格容器（`TableShell`）、表单布局（`FormSection`/`FormField`）、确认弹窗（`ConfirmDialog`、`DangerActionConfirm`）与全局通知（`ToastProvider`/`useToast`）。
- 业务 feature 只从 `@admin-frame/ui` 导入；**禁止**在页面中直接 `import { Table } from 'antd'`（由 ESLint 限制），以便未来统一替换或升级。
- 主题通过 `ConfigProvider` + CSS 变量实现；颜色、圆角、间距、字号只在 `packages/ui/src/tokens` 定义。
- 图标按需引入（`@ant-design/icons/es/icons/XxxOutlined`），禁止整包导入。

### 11.2 交互基线

| 场景 | 规范 |
|---|---|
| 加载 | 路由级骨架屏；表格首屏 skeleton，翻页保留旧数据（`placeholderData`） |
| 空态 | 统一 `EmptyState`，区分“无数据”与“无权限/不可访问” |
| 错误 | 页面级 `ErrorState` + 重试；操作级 toast + 可复制 `trace_id` |
| 表单 | 标签在上/左统一；必填标识；提交中禁用；离开未保存提示 |
| 危险操作 | 红色主按钮 + 二次确认 + 影响范围说明 + 操作原因（IAM 写操作必填，≤200 字） |
| 审批 | IAM 写操作返回 `40900` 转审批引导，禁止自动重试；申请人不得审批自己的申请（SoD） |
| 分页 | 服务端分页；默认 20；上限 100；切换筛选重置到第 1 页 |
| 时间 | 传输/存储 UTC（RFC3339）；展示按用户时区；统一 `dayjs` 封装 |
| 金额/数字 | 统一格式化工具，禁止页面内自行 `toFixed` 拼接 |
| 长文本 | 截断 + Tooltip；敏感字段脱敏展示 |
| 大表格 | 超过 200 行启用虚拟滚动；列设置可持久化（非敏感） |

### 11.3 可访问性（WCAG 2.1 AA）

- 所有交互控件可键盘操作，焦点可见且顺序合理；模态框焦点陷阱与 `Esc` 关闭。
- 表单控件必须有可关联的 `label`；错误信息使用 `aria-describedby`。
- 颜色对比度 ≥ 4.5:1（正文）；不仅用颜色表达状态。
- 关键页面（登录、列表、表单、审批）纳入 `axe` 自动化检查，严重级别为 0。

### 11.4 国际化

- 默认 `zh-CN`，文案统一走 i18n key；业务组件禁止硬编码中文文案（ESLint 自定义规则或 review 约束）。
- 与后端 `message` 的关系：前端以业务码映射自己的文案，后端 `message` 只作为诊断信息保留在 `trace_id` 详情中。
- 时区、数字、货币格式统一走 `packages/shared` 的格式化函数。

---

## 十二、错误处理与可观测性

### 12.1 错误边界

```text
根 ErrorBoundary（致命错误页 + 重试 + 上报）
  └─ 路由级 ErrorBoundary（保留导航与布局）
       └─ 页面/区块级错误态（局部重试，不拖垮整页）
```

- 未捕获异常必须上报（含 release/commit、路由、`trace_id`），但不得上报表单内容、Token、密码等敏感数据。
- Chunk 加载失败（发布后旧页面）自动刷新一次；仍失败展示“请刷新页面”。

### 12.2 前端可观测

| 维度 | 要求 |
|---|---|
| 错误监控 | Sentry 或 OpenTelemetry Web（自建/私有部署优先）；采样率按环境配置 |
| 发布标识 | 上报附带 `release`（版本号）与 `contract_commit` |
| Trace | 读取响应 `trace_id` 并在错误详情展示；可选注入 W3C `traceparent`，与后端 OTel 对齐 |
| 性能 | 采集 Web Vitals（LCP/INP/CLS/TTFB），上报同源或监控端点，遵循采样与隐私设置 |
| 日志 | 生产禁用 `console.*`（ESLint 规则）；统一 `logger`，默认脱敏 |
| 隐私 | 不采集输入明文、不录制 DOM 回放中的敏感字段；Cookie/存储键值不上报 |

---

## 十三、性能与容量

### 13.1 预算（合并门禁）

| 指标 | A 端 | B 端 | 测量方式 |
|---|---|---|---|
| 首屏 JS（gzip） | ≤ 300 KB | ≤ 260 KB | `scripts/check-size.mjs`（统计 index.html 直接引用的入口与 preload chunk） |
| 单路由 chunk（gzip） | ≤ 120 KB | ≤ 120 KB | `scripts/check-size.mjs`（逐 chunk gzip 校验） |
| LCP（P75，4G） | ≤ 2.5s | ≤ 2.5s | Lighthouse CI |
| INP（P75） | ≤ 200ms | ≤ 200ms | RUM |
| CLS | ≤ 0.1 | ≤ 0.1 | Lighthouse CI |

- 预算调整需在 MR 说明原因并更新本节；禁止静默放宽。

### 13.2 优化要求

- 路由级懒加载 + 组件级懒加载（重型编辑器、图表）。
- `manualChunks` 只拆分入口必然加载的 vendor（React、React Router、Zod、TanStack Query、Zustand）；Ant Design 等按需组件保持随路由懒加载自然分片，禁止为通过体积门禁把 `antd` 提前并入入口 chunk。
- 列表翻页使用 `placeholderData` 保留旧数据，避免闪烁。
- 图片走 CDN，按需 `avif/webp`、`loading="lazy"`、明确宽高。
- 预取：菜单 hover / 路由 intent 预取代码与关键查询。
- 禁止首屏加载全量权限目录、审计明细等低频大数据。

---

## 十四、测试策略

### 14.1 分层

| 类型 | 工具 | 范围 | 门禁 |
|---|---|---|---|
| 单元测试 | Vitest | 纯函数、格式化、权限判定、错误映射 | 覆盖率 ≥ 70%（行/函数/分支/语句，统计范围 `packages/*`） |
| 组件测试 | Vitest + RTL + user-event | 表单校验、表格交互、权限隐藏、错误态 | 同上 |
| 契约 Mock | MSW | 基于生成类型构造响应；覆盖成功/4xx/5xx/超时 | CI 必须通过 |
| E2E | Playwright | 登录/MFA、菜单权限、IAM 审批、B 端租户隔离、关键 CRUD | PR 跑 smoke；全量 nightly |
| 可访问性 | axe-core | 关键页面 | 严重问题为 0 |

### 14.2 必测场景（不低于）

1. 登录成功、密码错误、MFA 挑战、MFA 票据过期、强制绑定未完成。
2. 401 触发单飞刷新成功；刷新失败清会话；并发 401 只发起一次刷新。
3. 权限缺失时路由 403、菜单隐藏、按钮禁用；权限服务不可用时 fail-closed。
4. B 端列表不发送 `merchant_id`；跨商户详情展示 404。
5. IAM 直写返回 `40900` 时展示审批引导，且不自动重试。
6. 审批人不能审批自己的单据（按钮禁用）。
7. 统一错误码映射：40000/40300/40400/40900/42900/50000/50300。
8. 分页参数上限 100，切换筛选重置页码。

### 14.3 原则

- 测试不得依赖生产数据或生产凭据；E2E 使用容器化后端或契约 Mock。
- 禁止用 `test.skip` 掩盖必测场景；临时跳过须关联 issue 与到期日。
- 测试数据用 factory/builder，禁止大段复制 fixture。
- 不稳定用例隔离到 `quarantine` 并限期修复，不得长期留在主流水线。

---

## 十五、代码规范硬约束

| ID | 约束 | 门禁 |
|---|---|---|
| FE-01 | TypeScript `strict` 全开；禁止无理由的 `any`、`@ts-ignore`、非空断言 `!` | `tsc --noEmit` + ESLint |
| FE-02 | ESLint 零告警（`--max-warnings=0`）；`@typescript-eslint` type-checked 规则启用 | `pnpm lint` |
| FE-03 | Prettier 格式检查无 diff（CI 只检查不写回） | `pnpm format:check` |
| FE-04 | 分层与包边界符合第五章；禁止跨 feature 深链、apps 互引 | dependency-cruiser |
| FE-05 | 命名符合第四章；组件 PascalCase、其余 kebab-case | ESLint + Review |
| FE-06 | 只有 `api-client` 发 HTTP；禁止业务代码直接 `fetch`/`axios` | ESLint `no-restricted-*` |
| FE-07 | 只有 `auth` 管理令牌；禁止 `localStorage` 存令牌；禁止令牌进 URL/日志 | ESLint + Review + gitleaks |
| FE-08 | 生成物禁止手改；变更必须改 OpenAPI 源后重新生成 | CI 生成校验 + GEN-03 |
| FE-09 | 外部输入在边界用 Zod 校验；表单与 API 响应不信任 | Review + 测试 |
| FE-10 | 权限判断集中；默认拒绝；禁止在组件内散落权限码字符串 | Review + ESLint 自定义规则 |
| FE-11 | 服务端数据只用 TanStack Query；禁止复制进 Zustand | Review |
| FE-12 | 路由级懒加载与 ErrorBoundary；禁止单入口全量打包 | 构建检查 |
| FE-13 | 生产禁止 `console.*`、`debugger`、公开 Source Map | ESLint + 构建配置 |
| FE-14 | 禁止 `dangerouslySetInnerHTML`；必要时白名单净化并注释理由 | ESLint + Review |
| FE-15 | 可访问性：表单 label、焦点管理、对比度、键盘可达 | axe + Review |
| FE-16 | 构建期常量与运行期配置不得包含任何密钥/内部地址/测试账号 | gitleaks + Review |
| FE-17 | 依赖精确版本 + 锁文件；新增依赖须说明用途与许可证 | `pnpm-lock` + license 门禁 |
| FE-18 | 覆盖率与必测场景达标；禁止无 issue 的 skip | `pnpm test:ci` |
| FE-19 | 体积预算达标 | `pnpm size`（`scripts/check-size.mjs`） |
| FE-20 | 架构/契约/安全变更同步更新本文、ADR 与 README 引用 | Review |

> 前端门禁 ID 沿用后端“硬约束编号”风格；[CI_STANDARDS](./CI_STANDARDS.md) 已建立 FE-01～FE-20 映射，新增或调整约束时须同步更新两处，保持本地与平台一致。

---

## 十六、工程门禁与 CI

### 16.1 本地入口

```text
make frontend-install     # corepack + pnpm install --frozen-lockfile
make frontend-gen         # 生成 OpenAPI TS 类型
make frontend-lint        # eslint --max-warnings=0
make frontend-format      # prettier --check
make frontend-typecheck   # tsc --noEmit（全部 workspace）
make frontend-boundaries  # dependency-cruiser
make frontend-test        # vitest run --coverage
make frontend-build       # 两个 app 生产构建
make frontend-size        # 体积预算（scripts/check-size.mjs）
make frontend-audit       # pnpm audit --audit-level=high + 许可证白名单
make frontend-e2e         # Playwright（可选，需本地后端）
make frontend-ci          # 以上必跑项聚合
```

### 16.2 CI Job（`frontend`）

| 步骤 | 命令 | 失败即阻断 |
|---|---|---|
| 安装 | `corepack enable && pnpm install --frozen-lockfile` | 是 |
| 契约生成 | `pnpm gen:api`（openapi-typescript 7.4.0）+ `git diff --exit-code`（无漂移） | 是 |
| 格式 | `pnpm format:check` | 是 |
| Lint | `pnpm lint`（`--max-warnings=0`） | 是 |
| 边界 | `pnpm boundaries` | 是 |
| 类型 | `pnpm typecheck` | 是 |
| 测试 | `pnpm test:ci`（coverage + 阈值） | 是 |
| 构建 | `pnpm build`（admin + biz） | 是 |
| 体积 | `pnpm size`（gzip 体积预算脚本） | 是 |
| 依赖安全 | `pnpm audit --audit-level=high` | 是 |
| 许可证 | `pnpm license`（`pnpm licenses list` + `license-allowlist.json`） | 是 |
| E2E smoke | Playwright（admin 登录 + 权限 + biz 隔离） | 是（可拆独立 job） |

- Node 20（与现有 `openapi-docs` job 一致）；pnpm 版本由根 `packageManager` 字段钉扎。
- 变更 `frontend/` 时才触发该 job（路径过滤），但分支保护仍需将其列为必需检查。
- GitHub 与 Gitee 双平台检查项必须一致；落地时同 MR 更新 [CI_STANDARDS](./CI_STANDARDS.md) 映射表、`.github/workflows/*`、`.workflow/*` 与 `scripts/ci/gitee-job.sh`。
- 缓存：`pnpm store` + 构建缓存；PR 不构建/推送前端镜像。
- Dependabot 增加 npm 生态（每周），与 Go 依赖策略一致（DEP-07）。
- 提交信息沿用现有 `commitlint.config.cjs` 与 GIT_STANDARDS；落地时在 PR 模板增加 FE-01～FE-20 自检清单。

### 16.3 部署流水线（非合并门禁）

- 静态产物：部署到 CDN/对象存储或 Nginx 镜像；镜像 tag 使用 semver/sha，禁止 `latest`（RELS-01）。
- 镜像扫描：Trivy 检查 OS 包与静态文件；命中 `CRITICAL/HIGH` 失败（RELS-06）。
- 版本注入：`__APP_VERSION__` / `__CONTRACT_COMMIT__` / 构建时间（Vite `define`）；“关于”页可核对。
- Helm：`deploy/helm/admin-frame` 增加前端 Deployment/Ingress（或外部静态托管配置），与 A/B API 独立发布、独立回滚；当前模板尚无前端工作负载，P4 落地。
- 发布顺序：后端先发布兼容契约 → 前端再发布；回滚时前端回退到上一静态版本，`index.html` 禁止长缓存。

---

## 十七、配置、环境与部署

### 17.1 配置分层

| 层 | 内容 | 是否敏感 |
|---|---|---|
| 构建期常量（Vite `define`） | `__CHANNEL__`、`__APP_VERSION__`、`__CONTRACT_COMMIT__` | 否（全部公开） |
| 运行期 `/config.js` | `apiBaseUrl`、`env`、`channel`、功能开关 `features`、监控 `monitoring`（DSN/采样率） | 否 |
| 服务端注入 | CSP nonce、缓存头 | 否 |

- 禁止把环境差异打进多份构建产物；同一产物通过 `config.js` 适配 dev/staging/prod。
- `config.js` 以 `Cache-Control: no-store` 提供；加载失败时 fail-closed，展示配置错误页。
- 配置优先级：运行期 `config.js` > 构建期默认值；禁止业务代码散落读取 `import.meta.env` 或 `window.__APP_CONFIG__`，统一经 `packages/shared` 的 `loadRuntimeConfig` 校验后再使用。

### 17.2 环境矩阵

| 环境 | API 入口 | 说明 |
|---|---|---|
| local | Vite dev proxy：`/admin` → `http://localhost:8081`，`/biz` → `:8082` | 不启用 CSP 严格模式 |
| dev | `http://admin.localhost` / `http://biz.localhost` | 与 Ingress 一致 |
| staging | `https://admin.staging.example.com` / `https://biz.staging.example.com` | 生产同配缩容；开启 CSP |
| prod | `https://admin.example.com` / `https://biz.example.com` | 强制 TLS、HSTS、CSP、监控 |

### 17.3 Nginx / 静态服务

- SPA 回退：`try_files $uri $uri/ /index.html`；但 `/admin/**`、`/biz/**`、`/healthz` 必须交给 API/探针，不得被回退吞掉。
- 缓存：带 hash 的静态资源 `immutable, max-age=31536000`；`index.html` 与 `config.js` `no-store`。
- 安全头：CSP、HSTS、`X-Content-Type-Options: nosniff`、`Referrer-Policy`、`Permissions-Policy`、`frame-ancestors 'none'`。
- 镜像：多阶段构建（Node 构建 → Nginx unprivileged），非 root、只读根文件系统、固定基础镜像 digest。
- 健康检查：`/healthz` 返回 200；就绪探针校验静态目录可读。

### 17.4 同源路径约定

```text
https://admin.example.com/
├── /admin/v1/**   → admin-api（Ingress 原样转发，前端不得再拼 /admin）
├── /config.js     → 运行时配置
└── /**            → SPA（/login、/iam/roles…）
```

- 前端路由不得占用 `/admin`、`/biz` 前缀，避免与 API 冲突。
- 开发期通过 Vite proxy 复刻同一路径，禁止跨域直连不同端口（避免 CORS 与 Cookie 差异）。

---

## 十八、与后端契约的缺口

以下增强不阻塞首期，但应在对应阶段按 API 契约流程补齐；前端方案已预留适配点。

| 缺口 | 影响 | 建议 | 前端兜底 |
|---|---|---|---|
| 无 `GET /{channel}/v1/auth/me` | 页面无法获得主体名称/商户名称等展示信息 | 新增只读端点，返回 `subject_id`、显示名、`channel`、`merchant_id`、会话摘要 | 解析 Access Token 的 `sub`/`channel`/`merchant_id` 仅用于展示，不用于授权 |
| 刷新令牌通过响应体下发 | 无法使用 HttpOnly Cookie，存在 XSS 读取风险 | 增加 `Set-Cookie HttpOnly` 刷新模式（保留 body 模式一个版本） | `TokenStore` 适配器 + `sessionStorage` 过渡 + 严格 CSP |
| 权限树缺少菜单展示元数据 | `menu` 类权限点无图标/排序/分组/路由映射，菜单仍需前端静态路由表维护 | 若需服务端下发菜单，扩展 `PermissionTreeNode` 或新增菜单视图契约 | 路由 `meta` 保持单一来源，契约就绪后平滑切换 |
| IAM 审批开启仅以 `40900` 表达 | 前端难以区分“普通冲突”与“需审批” | 契约增加 `x-approval-required` 或专用业务码/202 响应 | 对 IAM 写端点做路径级映射与审批引导 |
| 审批应用失败保持 `approved` | 用户可能重复点击“应用” | 契约明确可重试语义与版本字段 | 仅允许幂等重试，成功后失效列表 |
| 无前端可用的配置/功能开关端点 | 灰度与紧急关闭依赖发版 | 提供只读 `config` 或 feature flag 端点（A 端 `config:read`） | 运行期 `config.js` 开关，默认关闭 |
| B 端无多商户切换契约 | 多商户账号无法切换租户 | 以后端会话切换端点为准 | 首期仅支持单商户上下文 |

> 以上缺口在 `api/openapi/**` 未更新前，前端不得自行约定字段或探测接口。

---

## 十九、落地路线

| 阶段 | 范围 | 完成标准 |
|---|---|---|
| **P0 工程底座** | monorepo、TS/ESLint/Prettier、Vitest、MSW、契约生成、边界检查、`frontend-ci`、CI job | 空壳应用可构建；全部门禁在本地与 CI 通过；MANIFEST 登记生成物 |
| **P1 A 端框架** | 登录/MFA、会话管理、权限快照、路由/菜单、布局、错误边界、监控、403/404 | 登录到首页全链路可用；权限与刷新场景有测试；CSP 生效 |
| **P2 A 端 IAM** | 角色、权限目录、绑定、审批单、破玻璃、审计列表 | 高风险写操作有确认/原因/幂等键；审批 SoD 与状态机 UI 完整；E2E 覆盖 |
| **P3 B 端** | 商户登录、商户 IAM、租户上下文展示、隔离用例 | 不发送 `merchant_id`；跨商户访问显示 404；B 端构建独立部署 |
| **P4 硬化** | 性能预算、a11y、安全头、监控看板、Helm/发布、回滚演练 | Lighthouse/axe/体积达标；发布与回滚 runbook 就绪 |
| **后续** | C 端（另立 ADR）、菜单树、Cookie 刷新、多商户切换 | 按 ADR 与契约变更推进 |

每个阶段合并前须同步：`README.md` 前端章节、`docs/CI_STANDARDS.md` 检查映射、`docs/codegen/MANIFEST.md`、`deploy/helm` 与发布说明。

---

## 二十、风险与缓解

| 风险 | 影响 | 缓解 |
|---|---|---|
| 刷新令牌重放检测 + 多标签页并发刷新 | 整条会话链被撤销，用户被强制登出 | 单飞刷新 + `navigator.locks`（`BroadcastChannel` 同步为 P1 项）；E2E 覆盖并发 401 |
| `sessionStorage` 存刷新令牌 | XSS 可窃取 | 严格 CSP、无第三方脚本、最短 TTL、目标切换 HttpOnly Cookie |
| AntD 体积过大 | 首屏超预算 | 路由分包、按需图标、`manualChunks` 仅切入口 vendor、体积预算脚本阻断 |
| 同源路径冲突（`/admin` 既是 API 又是页面） | 路由/接口 404 | 页面路由避开前缀；Nginx/Ingress 顺序与回退规则固化 |
| 前后端发布不同步 | 字段缺失/类型不匹配 | 同 commit 契约、兼容发布顺序、`contract_commit` 可视化 |
| 审批语义仅靠 `40900` | 误把审批当冲突重试 | 路径级映射 + 明确引导；推动契约增强 |
| 权限快照缓存过期 | 按钮可见但操作被拒 | 403 时强制刷新一次；以服务端裁决为准，不追求“完全一致” |
| 共享包膨胀成“万能包” | 边界腐化 | 包职责单一；新增能力走 ADR；dependency-cruiser 阻断反向依赖 |
| 前端覆盖率虚高 | 关键路径缺测 | 必测场景清单 + 70% 四项指标阈值 + E2E 兜底 |

---

## 二十一、Review Checklist

**结构**

- [ ] 代码位于正确层（app/pages/features/entities/shared），无跨 feature 深链
- [ ] 未新增 app 间引用或 packages 反向依赖；`pnpm boundaries` 通过
- [ ] 业务代码未直接使用 `fetch`/`axios`/`localStorage`
- [ ] 生成物来自重新生成，未手改

**契约与权限**

- [ ] 使用的字段/路径在 `api/openapi/{admin,biz}.yaml` 中存在，无自行约定
- [ ] 列表分页且 `page_size` ≤ 100；筛选/分页写入 URL
- [ ] 路由/按钮权限码存在且与 `docs/iam/PERMISSIONS.md` 一致
- [ ] 权限不可用时 fail-closed；B 端未发送 `merchant_id`

**安全**

- [ ] 无 Token/密码/MFA 票据进入 URL、日志、持久化存储
- [ ] 无 `dangerouslySetInnerHTML`、`eval`、公开 Source Map、生产 `console`
- [ ] 新增依赖已说明用途与许可证；无高危漏洞
- [ ] CSP 与安全头未被放宽

**质量**

- [ ] `pnpm lint` / `format:check` / `typecheck` / `test:ci` / `build` / `size` 全绿
- [ ] 新增/变更关键路径有测试；E2E 覆盖受影响流程
- [ ] 关键页面 axe 无严重问题；键盘可操作
- [ ] 错误码映射、`trace_id` 展示、重试/取消行为正确
- [ ] 变更同步更新文档、ADR、README 与发布说明
