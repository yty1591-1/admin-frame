# 前端开发规范（L1）

> **状态**：草案 v0.1（Draft，尚未生效）  
> **转正条件**：`frontend` CI job（2026-11-21 已接入并完成类型门禁红→绿验证）、`check-frontend-security.sh`（2026-11-23 已接入并完成红→绿验证）、覆盖率门槛上线后，按 [`frontend-remediation-plan.md`](./frontend-remediation-plan.md) §四 评审转 Normative  
> **效力**：转正后与 [`CODE_STYLE.md`](./CODE_STYLE.md) 同等；违反则 MR **不得合并**  
> **适用范围**：`frontend/` 下全部代码与构建产物——`apps/web-admin`、`apps/web-biz`、`packages/**`、`internal/**`（lint / tsconfig / vite 配置）  
> **上位约束**：[`ARCHITECTURE.md`](./ARCHITECTURE.md)（后端契约与领域边界）、[`API_STANDARDS.md`](./API_STANDARDS.md)（OpenAPI 与错误码）、[`SECURITY_STANDARDS.md`](./SECURITY_STANDARDS.md)（密钥、鉴权、安全基线）  
> **关联**：[`frontend-remediation-plan.md`](./frontend-remediation-plan.md)（任务包与排期）、[`CI_STANDARDS.md`](./CI_STANDARDS.md)（门禁语义）、[`TESTING_STANDARDS.md`](./TESTING_STANDARDS.md)（分层测试）  
> **执行方式**：`make frontend-check`（与 CI `frontend` job 同步骤）+ Code Review；未接入项见 §11  
> **最后更新**：2026-11-21（v0.1 草案初稿）  
> **变更方式**：改条文须同步改门禁脚本或 lint 规则，并经前端 Owner 与架构 Owner Review；例外一律走 §10

---

## 1. 总则

### 1.1 目标

1. **一致**：两个应用、所有共享包读起来像同一套代码，减少「复制一份再改」。
2. **可强制**：能靠 lint / 类型 / 构建 / 测试拦住的，不写进口头约定。
3. **可审查**：例外必须显式、有 owner、有到期日，可被脚本扫描。

### 1.2 编号与强制力

| 前缀 | 范围 | 章节 |
|------|------|------|
| `FE-00` | 总则与适用性 | §1 |
| `FE-01`～`FE-05` | 分层、目录、依赖方向 | §2 |
| `FE-10`～`FE-13` | 请求层与数据契约 | §3 |
| `FE-14`～`FE-17` | 路由、权限、菜单 | §4 |
| `FE-18`～`FE-20` | 组件、样式、资源与文案 | §5 |
| `FE-SEC-01`～`FE-SEC-08` | 安全 | §6、§7 |
| `FE-21`～`FE-24` | 构建、环境变量、发布 | §8 |
| `FE-25`～`FE-28` | 测试分层与覆盖率 | §9 |
| `FE-EXC-<id>` | 例外与豁免 | §10 |

**强制力**：`MUST` = 违反即阻断（lint / 脚本 / Review 任一）；`SHOULD` = 违反须在 MR 说明理由。本草案中标注「待接入」的条目，转正前以 Review 为准。

### 1.3 门禁图例

| 标记 | 含义 |
|------|------|
| ✅ | 当前已由工具强制，违反会红 |
| 🟡 | 规则已存在或可执行，但未进入裁决路径（如仅本地 lefthook；或 `frontend` job 已接入但该规则的检查尚未并入） |
| ⬜ | 待实现（脚本、测试或 CI 步骤尚不存在） |

---

## 2. 分层与依赖（FE-01～FE-05）

### 2.1 应用边界

| ID | 约束 | 门禁 |
|----|------|------|
| FE-01 | `apps/web-admin` 与 `apps/web-biz` **禁止互相 import**；两端共享的代码必须上移到 `packages/**`。两端差异只允许出现在「路由入口、页面、适配器」层 | eslint `no-restricted-imports`（待补规则）/ Review |

**现状说明**：两端现有约 1300 行逐字节相同代码与约 340 行同构微差，属历史债，由 `FE-K` 内核项目收敛；本规范约束**新增**代码，禁止继续复制。

### 2.2 依赖方向

| ID | 约束 | 门禁 |
|----|------|------|
| FE-02 | 依赖方向固定为：`apps/*` → `packages/effects/*`、`packages/{access,hooks,common-ui}` → `packages/@core/*` → `packages/@core/base/*`。**禁止反向依赖与循环依赖** | eslint（已配置 `@core` 禁引 `@vben/*`、`@core/base` 禁引 `@vben/*` 与 `@vben-core/*`）+ `pnpm check:circular`（✅ 2026-11-21 起超阈值非 0 退出，见 FE-G-02；CI 首跑取证） |

补充约定：

- 基础包（`packages/utils`、`types`、`icons`、`constants`、`styles`、`stores`、`preferences`、`locales`）**禁止**导入 `@vben/*` 业务包；需要共享的类型/工具先下沉。
- `packages/**` 禁止反向引用任何 `apps/*`。当前无此引用，但无规则兜底，由 FE-02 的 eslint 规则补齐。

### 2.3 应用内引用

| ID | 约束 | 门禁 |
|----|------|------|
| FE-03 | `apps/**` 内**禁止**用 `#/api/*`、`#/layouts/*`、`#/locales/*`、`#/stores/*` 别名深导入（现有 eslint error）；应用内模块用相对路径，跨应用共享走包名 `@vben/*` | eslint（✅ 规则已配置，但存量未清） |

**存量与前置**：`apps/` 下现有约 92 处 `#/` 深导入（其中 `#/api/*` 约 28 处），直接接入 CI 会立即变红。处理顺序：先把存量按 `FE-EXC` 逐条登记或改导入，再让 `frontend` job 成为必过项（对应计划 §十 R-01）。**新增**代码不再接受此类导入。

### 2.4 目录约定

| ID | 约束 | 门禁 |
|----|------|------|
| FE-04 | 页面目录固定为 `apps/<app>/src/views/<域>/<页>/index.vue`；页面私有组件放同目录 `components/`；一个页面一个目录，禁止把多个页面塞进同一 `index.vue` | Review |
| FE-05 | 命名：目录 `kebab-case`；组件文件 `PascalCase.vue`；组合式函数 `use-<name>.ts`；store 模块 `<name>.ts`；测试与被测文件同目录 `*.test.ts`；路由 `name` 与菜单 `pageMap` 的 key 一致 | Review + eslint 命名规则（待补） |

---

## 3. 请求层与数据契约（FE-10～FE-13）

| ID | 约束 | 门禁 |
|----|------|------|
| FE-10 | **唯一出口**：应用内 HTTP 请求只能经由 `apps/*/src/api/request.ts` 创建的 client；SSE 走 `sse.ts`。禁止页面、组件直接 `fetch` / `axios` / `XMLHttpRequest`（测试 mock 除外） | eslint `no-restricted-globals`（待补）/ Review |
| FE-11 | **响应契约**：`code === 0` 为成功，其余抛错；错误对象必须携带 `trace_id`。禁止在组件里手工解析 `response.data` 或重复判断 `code` | 单测（⬜） |
| FE-12 | **分页**：`page` 从 1 开始，`page_size` 默认 20、上限 100；超限由前端钳制并提示，不允许把 `page_size>100` 发到后端。列表字段统一 `list` / `total` / `page` / `page_size`，与 `API_STANDARDS.md` 对齐 | 单测（⬜） |
| FE-13 | **幂等**：后端声明支持幂等的写接口，前端必须携带 `Idempotency-Key`（两端行为一致）。**现状**：`web-biz` 未发送幂等头，与 `web-admin` 存在漂移，按 `FE-K-03` 收敛 | 单测（⬜） |

### 3.1 契约类型

- `SHOULD`：新接口优先消费 `make openapi-client` 生成的类型，禁止手抄 schema。
- 现状：`apps/web-admin/src/api/iam/types.ts`（约 361 行）、`api/configcenter/types.ts`（约 236 行）等为手写，属于已知债；在后端字段变更时不会编译失败，须在 Review 中人工比对 `api/openapi/schemas/**`。
- 后端契约变更（含字段增删）必须同 PR 更新前端类型或生成物，并在 MR 说明「已比对 OpenAPI」（对应 `API_STANDARDS.md`）。

---

## 4. 路由、权限与菜单（FE-14～FE-17）

| ID | 约束 | 门禁 |
|----|------|------|
| FE-14 | 路由来源：业务页面由后端菜单 + `pageMap` 动态生成；静态路由只保留登录、403、404、重定向等基础设施页面 | 单测（✅ 2026-11-24：`apps/*/src/router/__tests__/access.test.ts` 断言 pageMap 命中） |
| FE-15 | 菜单 `component` 必须与前端文件**双向一致**；未命中必须显式报错并提示，禁止只 `console.error` 后静默退化为 `not-found` | `catalog_test.go`（✅ 单向 + 反向 seed 扫描 2026-11-24）+ 运行时未命中显式报错（⬜） |
| FE-16 | 前端权限只做**展示控制**；每个新增或变更的接口必须由服务端 `perm()` 兜底。`v-access` 只负责隐藏，不承担鉴权；`*` 不代表全权限。按钮级权限点必须登记 `docs/iam/PERMISSIONS.md`。指令必须同时实现 `mounted` 与 `updated`：权限快照异步到达或权限被回收时要在同一 DOM 上恢复/隐藏（用 `display: none`，不得依赖移除后重挂） | 单测（✅ 2026-11-24：`packages/effects/access/src/__tests__/directive.test.ts` 3 例；去掉 `updated` 即 exit 1）+ Review checklist（服务端 `perm()`） |
| FE-17 | 守卫：未登录跳登录页并携带 `redirect`（白名单规则见 FE-SEC-07）；403 / 404 有独立页面；刷新恢复会话必须单飞，禁止并发刷新 | 单测（✅ redirect 白名单 2026-11-24；刷新单飞/403/404 走 Review） |

### 4.1 新增页面六步（缺一即 Review 不通过）

1. `apps/web-admin/src/views/<域>/<页>/index.vue`；
2. `internal/iam/menuview` 的 `component` 白名单；
3. `migrations/*_seed_iam_baseline*.up.sql` 播种菜单；
4. `internal/iam` 的权限点（`perm.go` 与白名单同步）；
5. `docs/iam/PERMISSIONS.md` 权限点目录；
6. `apps/web-admin/src/locales/langs/<locale>/page.json` 文案。

**现状**：白名单仅 6 条，已 seed 13 个 page 菜单，其中 9 个未入白名单，PATCH 校验可能返回 400；`/system/audit` 目录不存在却可被启用成死链。在 `FE-T-02` 修复前，新增页面以上述六步为准，并在 MR 附「三处一致」截图或命令输出。

### 4.2 权限分工与服务端兜底

- 前端权限快照只来自服务端（`getMyPermissionsApi` → `accessCodes`）；禁止在前端写死或硬编码「隐藏即安全」。
- `v-access`、菜单可见性、按钮显隐都只是 UX；判断放过请求的责任在服务端，每个新增/变更的接口必须补 `perm()` 与对应测试。
- 前端 `*` 视为「全部权限」仅用于展示（破玻璃/系统角色），不得据此跳过接口返回的 401/403 处理。
- 新增权限点必须同时改：`internal/iam` 权限点、`docs/iam/PERMISSIONS.md`、前端按钮 `v-access:code`；MR 勾选 PR 模板「前端自检」中的服务端兜底项。

---

## 5. 组件、样式、资源与文案（FE-18～FE-20）

| ID | 约束 | 门禁 |
|----|------|------|
| FE-18 | 组件复用优先：同一 UI 片段在两个应用出现两次即上移 `packages/effects/*` 或 `packages/@core/*`；禁止跨应用复制组件后再改；组件 props 必须声明类型，禁止 `any` 逃逸 | eslint / `check:type`（✅ 全仓 32 包 + 2 app） |
| FE-19 | 样式：优先 Tailwind 工具类；全局样式只允许在 `packages/styles`；CSS 层顺序由 `internal/vite-config` 的 `css-layer` 插件强制（✅），不得绕过；`!important` 必须注释理由 | stylelint（🟡 仅 lefthook staged）+ Review |
| FE-20 | 文案与资源：用户可见文案必须走 `$t()` + `locales`（禁止硬编码中文）；新增页面同步 `langs/<locale>/*.json`；图标走 `@vben/icons`。**上传与预览**：扩展名 + MIME 双白名单（`validateUploadFile`），SVG 默认禁止（可显式 `allowSvg`）；MIME 为空时只按扩展名判定；预览新窗口必须 `noopener,noreferrer`；非图片文件走 `downloadFile` 下载而非内联打开；服务端仍需复核类型与内容 | stylelint / cspell / 单测（✅ 2026-11-24：`packages/utils/src/helpers/__tests__/upload.test.ts` 11 例：白名单、SVG 拒绝与放行、伪 MIME、空 MIME、下载锚点） |

---

## 6. 安全（一）：存储、凭据与身份（FE-SEC-04～FE-SEC-08）

### 6.1 令牌与 Web Storage

| ID | 约束 | 门禁 |
|----|------|------|
| FE-SEC-04 | **禁止新增**任何以 Web Storage（`localStorage` / `sessionStorage` / SecureLS）持久化令牌的代码。存量 `accessToken` / `refreshToken` 为已知风险（R01），迁移到 `httpOnly` Cookie 需后端配合，另立 ADR。**SecureLS 是混淆不是加密**；`VITE_APP_STORE_SECURE_KEY` 随构建进入 bundle，**不是密钥**，禁止当作秘密管理 | `check-frontend-security.sh`（⬜）+ Review |

说明（回答「前端为什么不加密」）：构建期注入的任何密钥都必然出现在产物中，XSS 可读取同一 store；令牌防线的有效层是 CSP、XSS 出口管控、短 TTL 与 HttpOnly，不是前端对称加密。

### 6.2 用户输入凭据

| ID | 约束 | 门禁 |
|----|------|------|
| FE-SEC-05 | **禁止持久化用户输入的账号口令**，包括锁屏密码（`lockScreenPassword`）。锁屏改为「重新认证」或仅内存态；禁止把口令写入任何 Storage、日志或埋点 | 单测（✅ 2026-11-24：`packages/stores/src/__tests__/access.test.ts` 断言 `ACCESS_STORE_PERSIST_KEYS` 不含 `lockScreenPassword`；把该键加回即 exit 1） |

锁屏实现约定：`lockScreenPassword` 只存内存；`isLockScreen` 仍持久化。刷新后口令丢失，锁屏页提交即 `emit('toLogin')` 走重新认证（登出回登录页），不得再用本地口令比对放行。

### 6.3 其他持久化与登出

| ID | 约束 | 门禁 |
|----|------|------|
| FE-SEC-06 | 用户资料、权限缓存允许持久化，但必须可一键清空；**登出必须清空全部本地状态**，即使登出接口失败（现状已满足，保持） | 单测（⬜） |

### 6.4 登录重定向

| ID | 约束 | 门禁 |
|----|------|------|
| FE-SEC-07 | `redirect` 参数必须走白名单（仅站内相对路径），并容错 `decodeURIComponent`（畸形编码不得白屏）；越界地址回落到首页 | 单测（✅ 2026-11-24：`packages/utils/src/helpers/__tests__/redirect.test.ts` 9 例——畸形编码/协议相对/绝对 URL/伪协议/反斜杠/控制字符/双次编码全部回退；去掉 origin 校验即 exit 1）；两 app 的 `router/guard.ts` 与 `store/auth.ts` 接入 `resolveSafeRedirect` |

### 6.5 供应链

| ID | 约束 | 门禁 |
|----|------|------|
| FE-SEC-08 | 生产依赖不得存在 `high` 及以上已知漏洞；`minimumReleaseAge` 禁止为 0；`engineStrict: true`；npm 生态纳入 Dependabot | `pnpm audit --prod --audit-level=high`（✅ CI 第 2/12 步，npmjs registry）/ dependabot（✅）/ `engineStrict`（✅） |

---

## 7. 安全（二）：渲染与响应（FE-SEC-01～FE-SEC-03）

### 7.1 XSS 出口

| ID | 约束 | 门禁 |
|----|------|------|
| FE-SEC-01 | 禁止 `v-html` 渲染未净化内容；确需富文本时调用 `DOMPurify.sanitize` 并限制允许标签。禁止 `innerHTML`、`eval`、`new Function`、`document.write`。新增例外必须走 §10（含到期日与 owner） | eslint `vue/no-v-html: error`（✅ 已设；去掉 disable 即 exit 1）/ `check-frontend-security.sh`（✅ 第 4/12 步，allowlist 5 条带到期日） |

**现状**：`preview.vue` 有 2 处 `v-html` 且以 eslint-disable 关闭告警（TipTapPreview 当前未被 apps 引用）。转正前必须完成净化或登记例外，禁止永久 disable。

### 7.2 安全响应头与 CSP

| ID | 约束 | 门禁 |
|----|------|------|
| FE-SEC-02 | 所有前端入口必须下发：`Content-Security-Policy`、`X-Content-Type-Options: nosniff`、`X-Frame-Options: DENY`（或 `frame-ancestors 'none'`）、`Referrer-Policy: strict-origin-when-cross-origin`、`Permissions-Policy`；HSTS 在 TLS 终止层配置。CSP 首版用 `Content-Security-Policy-Report-Only`，staging 观察一周无 `script-src` 违规后切强制。静态站禁止 `Access-Control-Allow-Origin: *` 与凭据请求混用；SPA 入口 `/index.html` 必须 `Cache-Control: no-store` | `check-frontend-headers.sh`（✅ 第 5/12 步：静态校验 5 头 + CSP 仅 Report-Only + 禁 `ACAO: *` + `index.html` no-store + 两处 include；删任一即 exit 1）/ `curl -sI`（⬜ 待部署取证） |

注意：nginx 子块内的 `add_header` 会覆盖父块（不继承），因此安全头集中在 `security-headers.conf`，由 server 与 `location = /index.html` 两处 include；Dockerfile 需一并复制该片段。`VITE_INJECT_APP_LOADING=true` 会注入内联脚本，CSP 需为其产出哈希或关闭该开关。

### 7.3 错误呈现与敏感页

| ID | 约束 | 门禁 |
|----|------|------|
| FE-SEC-03 | 按 `code` 映射**固定**用户文案（`resolveRequestErrorMessage`），禁止直出服务端 `message` / `error`；服务端返回 `trace_id` 时附在文案末尾便于报障（清洗控制字符并截断）。MFA 绑定/设置页必须 `Cache-Control: no-store` 且不可被 iframe 嵌入；离开页面清除 secret | 单测（✅ 2026-11-24：`packages/effects/request/src/request-client/error-message.test.ts` 11 例；改坏映射即 exit 1）/ `check-frontend-headers.sh`（✅ `index.html` no-store）/ `curl -I`（⬜） |

---

## 8. 构建、环境与发布（FE-21～FE-24）

| ID | 约束 | 门禁 |
|----|------|------|
| FE-21 | 工具链唯一：pnpm（`packageManager` 固定 11.16.0）+ turbo；Node 版本以 `frontend/.node-version` 为准（基线建议 24 LTS，待决）；禁止 npm / yarn 混用；仓库根不得执行 `pnpm install` | `engine-strict`（✅ 2026-11-23：`engineStrict: true`，Node `>=99` 实测 exit 1） |
| FE-22 | 环境变量只允许 `VITE_*`；**构建期变量等于公开信息**，禁止存放任何密钥/口令。新增变量必须登记 `frontend/.env.example` 与白名单；`internal/vite-config/src/utils/env.ts` 对缺失必填项必须**构建失败**，禁止沿用「全默认值静默降级」 | 构建脚本（✅ 2026-11-24：`assertDeclaredEnv`，未声明变量 exit 1；4 例单测；`.env.example` 三份） |
| FE-23 | 产物必须可追溯：注入 `VITE_APP_GIT_SHA`、`VITE_APP_BUILD_TIME`、`VITE_APP_VERSION`（meta 与运行时均可读）；镜像 tag 含 git SHA；禁止 `latest`；禁止手工 `scp dist` 上线 | `check-frontend-build-meta.sh`（✅ 2026-11-24，第 14/14 步）+ `check-frontend-dockerfile.sh`（✅ 第 12/14 步）+ 镜像 tag 含 SHA（FE-B-02 双 target + FE-B-03 流水线 `sha-<short>`/semver，✅ 配置就位，⬜ 远端首跑） |
| FE-24 | 发布：`web-admin` / `web-biz` 各自独立镜像与 workload，Dockerfile 支持 multi-target；发布走 `release/CHECKLIST.md` 前端节；回滚步骤进 runbook 并至少演练一次 | `check-frontend-dockerfile.sh` 第 12/14 步（✅ 2026-11-24：双 target + chart 渲染 + nginx ConfigMap）；`release/CHECKLIST.md` 前端节 ✅；helm 回滚演练 ⬜ |

---

## 9. 测试分层与覆盖率（FE-25～FE-28）

| ID | 约束 | 门禁 |
|----|------|------|
| FE-25 | 分层：`packages/**` 有单元测试；`apps/*` 至少覆盖登录四态、刷新单飞、401 清令牌、403/404、429 倒计时、204、分页钳制、错误码映射、菜单未命中；E2E 放 nightly，不进 PR 必跑 | `frontend` job（✅ apps 冒烟 2026-11-23；拦截器层 401/429/204 + 分页钳制 2026-11-24：`preset-interceptors.test.ts`、`retry-after.test.ts`、`pagination.test.ts`） |
| FE-26 | 覆盖率：`apps/*` 关键路径首版 ≥30%，半年内 ≥50%；`packages/**` 不得下降。阈值写入 `vitest.config.ts` 并由脚本校验 | `apps/*/vitest.config.ts` + `check-frontend-gates.sh`（✅ 30% 生效；admin 89.47%、biz 93.5%） |
| FE-27 | 新增页面/接口必须带用例；缺陷修复必须附回归用例（对应 `TESTING_STANDARDS.md` 的例外体例） | Review |
| FE-28 | 禁止 `--passWithNoTests`、禁止跳过用例凑绿、禁止依赖外网后端；测试必须可离线运行（mock 接口） | Review + 测试运行 |

---

## 10. 例外与豁免（FE-EXC）

### 10.1 形式

代码内：

```ts
// FE-EXC-001: 存量 #/api 深导入，随 FE-K 迁移清理 | owner=@frontend-lead | expires=2026-12-31
```

脚本 allowlist（`scripts/ci/frontend-allowlist.json`，转正时创建）：

```json
{ "id": "FE-EXC-001", "pattern": "frontend/apps/web-admin/src/views/**", "rule": "FE-03", "owner": "@frontend-lead", "expires": "2026-12-31" }
```

### 10.2 约束

1. 例外必须**最小范围**（文件或目录，不接受整仓）；  
2. 必须有 **owner 与到期日**，到期未清理即门禁红；禁止永久豁免；  
3. 与 `CODE_STYLE.md` 的 `nolint`、`TESTING_STANDARDS.md` 的例外体例保持一致：原因 + 范围 + 到期 + 审批人；  
4. 安全类（`FE-SEC-*`）例外需安全 Owner 额外批准，且不得豁免 FE-SEC-04/05 的「新增」禁令。

---

## 11. 当前执行现状（2026-11-23）

> 本节只描述**工具实际行为**，不描述目标。转正前，标 ⬜/🟡 的规则以 Code Review 为准。

| 能力 | 现状 | 关联 |
|------|------|------|
| 本地 pre-commit（oxlint / oxfmt / eslint / stylelint / 全量 `check:type`） | 🟡 仅 `frontend/` 安装 lefthook 时生效；本机 `.git/hooks` 无 pre-commit；可 `--no-verify` 绕过 | `frontend/lefthook.yml` |
| `pnpm check:type` | ✅ 2026-11-23：`turbo run typecheck` 覆盖 32 个包 + 2 个 app（34 任务，约 1m8s）；包内 `.vue` 用 `vue-tsc --noEmit`、纯 TS 用 `tsc --noEmit` | `frontend/packages/**/package.json`、`frontend/internal/**/package.json`、`frontend/turbo.json` |
| 类型基线（2026-11-21） | ✅ 两 app `vue-tsc --noEmit` 均 0 错误；修掉 9 处既有红（`secrets/index.vue` 8 处模板断言、`releases.ts:72` payload 默认值） | `apps/web-admin/src/views/system/config/secrets/index.vue`、`apps/web-admin/src/api/configcenter/releases.ts` |
| 包级类型基线（2026-11-23） | ✅ 32 个包（26 packages + 6 internal）全 0 错误；修掉 21 处既有红（`typings/vue-router.d.ts` 声明路径、`RouteMeta` augmentation、Vite/Rollup 插件 hook 类型、测试非空断言等） | `packages/@core/base/typings/vue-router.d.ts`、`internal/vite-config/src/plugins/*.test.ts` |
| 门禁红灯验证（2026-11-23） | ✅ 类型门禁：在 `main.ts` 注入 `const __redLightProbe: string = 1` → `vue-tsc` 退出 2（TS2322/TS6133），撤销后退出 0；包级：在 `packages/utils/src/helpers/generate-menus.ts` 注入同句 → `pnpm check:type` 退出 2（turbo `Failed: @vben/utils#typecheck`），撤销后 0；`check:dep` / `check:circular` 见下一行 | 本轮实施记录 | `turbo.json` |
| 应用级覆盖率红灯（2026-11-23） | ✅ 临时把 `web-admin` 阈值调 99% → `vitest run` exit 1（Coverage for statements/branches/functions/lines does not meet global threshold）；恢复 30% 后 38 条用例全绿 | `apps/web-admin/vitest.config.ts` |
| `pnpm check:dep` / `check:circular` | ✅ 2026-11-23 本地实测：存量未用依赖 / 10 个循环时 exit 1，清理后 exit 0；单测随 `pnpm test:unit` 通过 | `frontend/scripts/vsh/src/check-*/` |
| PR 模板「前端自检」 | ✅ 双模板含该节；`check-git.sh` 断言标题与 `make frontend-check` 命令（改标题即 exit 1） | `.github/PULL_REQUEST_TEMPLATE.md`、`.gitee/PULL_REQUEST_TEMPLATE.md`、`scripts/ci/check-git.sh` |
| `pnpm check:cspell` | ✅ 2026-11-23 覆盖 `**/*.ts`、`**/*.vue`、README、changeset（943 文件 0 问题）；`.vue` 注入错词即 exit 1 | `frontend/package.json`、`frontend/cspell.json` |
| `pnpm test:unit` | ✅ 2026-11-23：根 610 + web-admin 38 + web-biz 20；两 app 覆盖率 89.47% / 93.5%（阈值 30%） | `vitest.config.ts`、`apps/*/vitest.config.ts` |
| 上传类型双白名单（FE-20） | ✅ 2026-11-24：`@vben/utils` 新增 `validateUploadFile`（扩展名 + MIME，SVG 默认禁）与 `downloadFile`；两 app Upload 适配器接入，非图片预览改为下载；11 例单测 | `frontend/packages/utils/src/helpers/upload.ts`、`frontend/apps/*/src/adapter/component/index.ts` |
| v-access 更新语义（FE-16） | ✅ 2026-11-24：指令同时实现 `mounted` + `updated`，用 `display: none` 隐藏/恢复（权限快照异步到达与回收均生效）；3 例单测，删掉 `updated` 即 exit 1 | `frontend/packages/effects/access/src/directive.ts`、`frontend/packages/effects/access/src/__tests__/directive.test.ts` |
| 锁屏口令不落盘（FE-SEC-05） | ✅ 2026-11-24：`persist.pick` 移除 `lockScreenPassword`（改为 `ACCESS_STORE_PERSIST_KEYS`）；单测 3 例（加回该键即 exit 1）；刷新后锁屏页走 `toLogin` 重新认证 | `frontend/packages/stores/src/modules/access.ts`、`frontend/packages/stores/src/__tests__/access.test.ts`、`frontend/packages/effects/layouts/src/widgets/lock-screen/lock-screen.vue` |
| 登录回跳白名单（FE-SEC-07） | ✅ 2026-11-24：`resolveSafeRedirect`（仅站内相对路径、解码容错、origin 校验）；两 app guard/store 接入；9 例单测（去掉 origin 校验即 exit 1） | `frontend/packages/utils/src/helpers/redirect.ts`、`frontend/apps/*/src/router/guard.ts`、`frontend/apps/*/src/store/auth.ts` |
| 拦截器回归（FE-25） | ✅ 2026-11-24：401 重认证与刷新单飞（含重试注入新令牌、刷新失败清队列）、204 无内容成功、429 `Retry-After` 解析、分页 `page_size` 钳制共 22 例；刷新漏注入 `Authorization` 的缺陷由测试先红后绿暴露 | `frontend/packages/effects/request/src/request-client/preset-interceptors.test.ts`、`frontend/packages/utils/src/helpers/__tests__/retry-after.test.ts`、`frontend/apps/web-admin/src/api/__tests__/pagination.test.ts` |
| XSS 出口巡检（FE-SEC-01） | ✅ 2026-11-23：`check-frontend-security.sh` 扫 837 个文件，5 处 `v-html`/`innerHTML`/`window.open` 命中全部 allowlist（带到期日）；注入 `v-html=` 即 exit 1；`vue/no-v-html=error`（去掉 preview.vue disable 即 exit 1）；3 处 `window.open` 已补 `noopener,noreferrer` | `scripts/ci/check-frontend-security.sh`、`scripts/ci/frontend-security-allowlist.txt`、`internal/lint-configs/eslint-config/src/configs/vue.ts` |
| `pnpm test:e2e` | ⬜ `turbo run test:e2e` 无任何包定义该脚本，空转 | `frontend/package.json` |
| CI | 🟡 job `frontend` 已接入（`pnpm` + Node 24.16.0 + `bash scripts/ci/frontend-check.sh`，无 `paths` 过滤）；Gitee 三个流水线已加 `build@nodejs` step（插件语法待控制台核实）；远端首跑未取证 | `.github/workflows/ci.yml`、`.workflow/*.yml` |
| `make ci` | ⬜ 40+ 检查仍无前端；引入独立目标 `make frontend-check`（刻意不并入，pnpm install 依赖网络） | `Makefile` |
| 安全头 / CSP | 🟡 2026-11-23：`security-headers.conf` 下发 5 个安全头 + `Content-Security-Policy-Report-Only`，移除 `ACAO:*`；`/index.html` `no-store` + 两处 include；`check-frontend-headers.sh` 静态校验入第 5/14 步（删头/去 no-store 实测 exit 1）；⬜ `curl -sI` 实机取证待部署 | `frontend/scripts/deploy/nginx.conf`、`frontend/scripts/deploy/security-headers.conf`、`scripts/ci/check-frontend-headers.sh` |
| 错误呈现（FE-SEC-03） | ✅ 2026-11-24：`resolveRequestErrorMessage` 按 `code` 映射本地文案 + 透出 `trace_id`（清洗/截断），不再直出服务端 `message`；11 例单测（改坏映射即 exit 1）；两 app `request.ts` 接入 | `frontend/packages/effects/request/src/request-client/error-message.ts`、`frontend/apps/*/src/api/request.ts` |
| 产物 SHA | ✅ 2026-11-24：`index.html` 注入 `x-app-version` / `x-app-git-sha` / `x-app-build-time`（`__VBEN_ADMIN_METADATA__` 与 `VITE_APP_*` 同源）；关于页展示版本与提交号；`check-frontend-build-meta.sh` 第 14/14 步断言两 app 产物 SHA 为 40 位十六进制；`VITE_APP_GIT_SHA` 纳入 turbo `globalEnv`，缓存命中不会复用旧 SHA。镜像双 target 见 FE-B-02 | `frontend/internal/vite-config/src/plugins/inject-metadata.ts`、`scripts/ci/check-frontend-build-meta.sh` |
| 环境变量白名单（FE-22） | ✅ 2026-11-24：根与两 app 各一份 `.env.example`（无密钥）；`DECLARED_ENV_KEYS` + `assertDeclaredEnv`，未声明 `VITE_BOGUS_FLAG` 实测构建 exit 1；4 例单测；`apps/*/.env` 已 gitignore | `frontend/internal/vite-config/src/utils/env.ts`、`frontend/.env.example`、`frontend/apps/*/.env.example` |
| 发布 | ⬜ Dockerfile 只 COPY `web-admin/dist`；`deploy/` 无前端 workload | `frontend/scripts/deploy/Dockerfile` |
| 供应链 | ✅ `engineStrict: true` + `minimumReleaseAge: 1440` + audit 0 high（11 个 overrides）+ dependabot npm `/frontend` | `frontend/pnpm-workspace.yaml`、`.github/dependabot.yml` |

门禁接入顺序与验收见 [`frontend-remediation-plan.md`](./frontend-remediation-plan.md) §五 / §六；本文条目与 `FE-G`、`FE-B`、`FE-S`、`FE-T` 任务包一一对应。

---

## 附录 A：新增页面自检表

- [ ] `views/<域>/<页>/index.vue` 已建，目录与路由 name 一致；
- [x] `pageMap` 与后端 `component` 双向命中（用 `FE-T-02` 的反向用例验证）；
- [ ] 白名单、seed、权限点、`PERMISSIONS.md`、`page.json` 五处同步；
- [ ] 请求全部走 `request.ts`，分页 `page_size ≤ 100`；
- [ ] 按钮权限用 `v-access`，且对应接口服务端有 `perm()`；
- [ ] 文案无硬编码中文；无新增 `v-html` / `innerHTML`；
- [ ] 至少 1 条用例（页面或请求层）；
- [ ] MR 勾选「前端自检」节（模板已提供，`check-git.sh` 断言该节存在）。

## 附录 B：关联文档

- 架构与领域边界：[`ARCHITECTURE.md`](./ARCHITECTURE.md)（前端结构不在该文；`FE-D-03` 将把该文 `:134` 的排除条款改为指向本文的指针）；
- 接口契约：[`API_STANDARDS.md`](./API_STANDARDS.md)、`api/openapi/schemas/**`；
- 安全：[`SECURITY_STANDARDS.md`](./SECURITY_STANDARDS.md)；
- 测试：[`TESTING_STANDARDS.md`](./TESTING_STANDARDS.md)；
- 发布：[`RELEASE_STANDARDS.md`](./RELEASE_STANDARDS.md)、[`release/CHECKLIST.md`](./release/CHECKLIST.md)；
- 计划与验收：[`frontend-remediation-plan.md`](./frontend-remediation-plan.md)。
