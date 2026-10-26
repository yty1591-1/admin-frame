# S3 阶段基础设施实施方案（错误、可观测与 i18n）

> **版本**：v1.1
> **状态**：S3-1～S3-4 已全部落地；全量门禁通过；待提交
> **基线**：2026-10-25，`main` 分支 `90da8b9`（S2 UI 基座与交互已落地）
> **范围**：`frontend-infrastructure-plan.md` §5.4 + §5.6，对应 INF-DATA-02、INF-OBS-01～03、INF-I18N-01，以及 INF-UI-04 遗留的「未保存离开提示」
> **规范依据**：[前端架构与工程规范](./frontend-architecture.md) §11.4、§12，[规划与差距评估](./frontend-infrastructure-plan.md) §4.2/§4.6/§4.10
> **前置结论**：D-01 监控选型已定为 OpenTelemetry Web + 自有 collector；D-02 i18n 维持 P0，不降级

---

## 一、目标与退出标准

### 1.1 目标

让错误提示、诊断信息与界面文案从「各页面自行决定」变成「统一映射、统一出口、统一字典」：

1. **错误可解释**：8 个业务错误码与网络/超时/未知错误各有唯一用户文案与展示位置，页面不再透传后端 `message`。
2. **错误可定位**：所有错误展示保留可复制 `trace_id`；前端异常经统一 `logger` 记录并按采样上报，生产环境无 `console.*`。
3. **文案可治理**：建立 `zh-CN` 字典与类型安全的 `t()`，新增页面不得硬编码中文，且有脚本门禁防退化。

### 1.2 退出标准（S3 完成）

- [x] `40000/40100/40300/40400/40900/42900/50000/50300` + `network/timeout/unknown` 均有 `messageKey`、`action`、`retryable` 定义，并由单测覆盖。
- [x] `40900` 映射为 `approval` 且 `retryable=false`，不会进入自动重试或普通冲突提示。
- [x] 列表、表单、登录、危险操作、路由守卫、根/路由错误边界全部通过统一映射取文案；错误展示可复制 `trace_id`。
- [x] `packages/observability` 提供生产可用的 `logger`（默认脱敏、环形缓冲），`AppErrorBoundary` 不再 `console.error`。
- [x] OTel Web 在 A 端首屏后动态加载；`monitoring.dsn` 为空时不初始化、不产生网络请求；上报字段白名单有单测。
- [x] `packages/i18n` 提供 `zh-CN` 字典、`t()`、`I18nProvider`，现有 A 端页面与 `packages/ui` 内置文案完成迁移。
- [x] `pnpm lint:i18n` 进入 `frontend-ci`，扫描范围内无未豁免的中文硬编码。
- [x] `format/lint/lint:i18n/boundaries/typecheck/test:ci/build/size/license/audit/e2e` 全部通过，体积预算不放宽。

### 1.3 不在 S3 范围

| 项 | 原因 |
|---|---|
| 完整多语言切换、语言包懒加载、`en-US` 字典 | D-02 已定：首期仅 `zh-CN` 字典 + `t()` |
| Web Vitals（LCP/INP/CLS/TTFB）采集 | INF-OBS-04 为 P1，随 RUM 采样策略另行实施 |
| 会话管理页、MFA 绑定/解绑、空闲超时、跨标签页同步 | S5 范围 |
| apps 组件测试纳入 Vitest、axe 断言 | S4 范围（`useUnsavedChangesGuard` 的测试随之补） |
| `traceparent` 注入与后端链路串联 | D-06 可选能力，留待 S4/S5 与后端采样策略一起评估 |
| B 端 Toast/OTel 接线 | 首屏预算所限，见 §6.3；B 端本轮只接 i18n 与错误映射 |
| Source Map 私有化、缓存头、镜像/Helm | S4 发布硬化范围 |

---

## 二、现状基线（代码事实）

| 能力 | 现状 | 证据 |
|---|---|---|
| 错误模型 | `AppError` 有 `kind/code/httpStatus/traceId/fieldErrors`，无用户文案 | [app-error.ts](file:///d:/MyDocument/admin-frame/frontend/packages/api-client/src/errors/app-error.ts) |
| 错误归一 | 信封解包、超时/网络/HTTP 状态映射、401 重放、403 通知装配层 | [client.ts](file:///d:/MyDocument/admin-frame/frontend/packages/api-client/src/http/client.ts#L249-L323) |
| 40900 判定 | `isApprovalRequired` 已存在，角色删除示例已按审批引导处理 | [app-error.ts](file:///d:/MyDocument/admin-frame/frontend/packages/api-client/src/errors/app-error.ts#L102-L107)、[RoleListPanel.tsx](file:///d:/MyDocument/admin-frame/frontend/apps/admin-web/src/features/iam-roles/RoleListPanel.tsx#L92-L104) |
| 错误展示 | `ErrorState`/`TableShell`/`DangerActionConfirm`/`useToast` 支持 `{ message, traceId }` 并复制 trace_id | [error-state.tsx](file:///d:/MyDocument/admin-frame/frontend/packages/ui/src/error-state.tsx)、[toast.tsx](file:///d:/MyDocument/admin-frame/frontend/packages/ui/src/toast.tsx) |
| 文案来源 | 页面与组件内联中文字符串；后端 `message` 在非 40100 场景被直接展示 | [LoginPage.tsx](file:///d:/MyDocument/admin-frame/frontend/apps/admin-web/src/pages/LoginPage.tsx#L51-L60)、[PermissionCatalogPanel.tsx](file:///d:/MyDocument/admin-frame/frontend/apps/admin-web/src/features/permission-catalog/PermissionCatalogPanel.tsx#L92-L99) |
| 日志 | 仅根错误边界 `console.error`；无 logger、无脱敏、无缓冲 | [AppErrorBoundary.tsx](file:///d:/MyDocument/admin-frame/frontend/apps/admin-web/src/app/AppErrorBoundary.tsx#L20-L23) |
| 上报 | 无 | 根 `package.json` 无任何 OTel 依赖 |
| 运行期配置 | 已预留 `monitoring.dsn`、`monitoring.sampleRate`，无消费方 | [runtime-config.ts](file:///d:/MyDocument/admin-frame/frontend/packages/shared/src/runtime-config.ts#L8-L17) |
| i18n | 无框架、无字典；antd 默认 locale 为 `zh-CN` | 各 `package.json` 无 i18n 依赖 |
| 装配顺序 | `AppErrorBoundary → ThemeProvider → ToastProvider → QueryClientProvider → AuthProvider → PermissionProvider → Router` | [App.tsx](file:///d:/MyDocument/admin-frame/frontend/apps/admin-web/src/app/App.tsx#L27-L41) |
| B 端装配 | 无 ToastProvider、无 AuthProvider（S6 再启动） | [biz App.tsx](file:///d:/MyDocument/admin-frame/frontend/apps/biz-web/src/app/App.tsx#L25-L33) |
| 门禁 | `no-console` 全仓库 error；`fetch` 仅 api-client；`sessionStorage` 仅 auth；`@opentelemetry/*` 无约束 | [eslint-base.mjs](file:///d:/MyDocument/admin-frame/frontend/packages/config/src/eslint-base.mjs#L9-L24) |
| 依赖边界 | 已有 packages/apps 分层规则，`shared` 为叶子包；无 i18n/observability 规则 | [dependency-cruiser.cjs](file:///d:/MyDocument/admin-frame/frontend/dependency-cruiser.cjs) |
| 测试范围 | Vitest 仅收集 `packages/*/src`；apps 测试不执行 | [vitest-base.ts](file:///d:/MyDocument/admin-frame/frontend/packages/config/src/vitest-base.ts#L12) |
| 体积门禁 | admin 首屏 300KB、biz 260KB、单 chunk 120KB（gzip） | [check-size.mjs](file:///d:/MyDocument/admin-frame/frontend/scripts/check-size.mjs) |

**已确认的空白**

1. 无「错误码 → 文案 key / 展示位置 / 是否可重试」的单一事实源。
2. 无 `logger`，生产环境没有可用的诊断出口；前端异常无法与后端 `trace_id` 关联。
3. `monitoring.dsn`/`sampleRate` 配置存在但未使用，监控通道实际不可用。
4. 没有 i18n 字典，文案与业务逻辑混合在 JSX 内，无法门禁。

---

## 三、总体设计

### 3.1 错误展示链路

```text
HttpClient（AppError：code/kind/traceId）
  └─ describeError(error)            ← packages/i18n
       ├─ action=login     → 40100：清会话并跳登录（现有 Auth 行为不变）
       ├─ action=approval  → 40900：ApprovalNotice，禁止自动重试
       ├─ action=page      → 40300/50300：Forbidden / 不可用页（fail-closed）
       ├─ action=inline    → 列表/表单/危险操作错误内联 + 重试入口
       └─ action=toast     → 网络/超时/42900 等瞬时错误，toast + trace_id
  └─ logger.error({ event, code, traceId, route })  ← packages/observability
       └─ OTel span event（采样）→ 自有 collector
```

**原则**

- `describeError` 只做「分类 + 文案 key + 建议动作」，**不**弹窗、不跳转、不重试；由页面/装配层执行动作。
- 后端 `message` 仅作为诊断信息进入日志与 `trace_id` 详情，不再直接展示给用户（40100 除外，仍需统一文案）。
- 所有代码只允许使用 `traceId` 标识，界面统一展示为 `trace_id`。

### 3.2 i18n 结构

```text
packages/i18n（零运行时依赖，React 仅 peer）
  ├─ messages/zh-CN.ts      zhCN 字典（as const）→ MessageKey
  ├─ translate.ts           createTranslator(messages) → t(key, params)
  ├─ i18n-provider.tsx      I18nProvider / useI18n（无 Provider 时回退 zh-CN）
  └─ error-presentation.ts  describeError(input) → ErrorPresentation
```

- 字典按域分组前缀：`common.*`、`nav.*`、`auth.*`、`error.*`、`iam.*`、`permission.*`。
- `t()` 类型参数为 `MessageKey`，插值仅支持 `{name}` 形式，禁止函数式/富文本插值。
- 缺失 key 返回 key 本身，并记录到 `Translator.missingKeys`（供单测与开发诊断，不 `console.warn`，遵守 `no-console`）。

### 3.3 可观测结构

```text
packages/observability（零业务依赖，不得依赖 api-client/auth/ui）
  ├─ logger.ts       级别、字段白名单、脱敏、环形缓冲（50 条）
  ├─ trace.ts        从错误对象结构化读取 trace_id；缺失时生成 client_trace_id
  ├─ report.ts       reportError() + setReportTransport()（默认 no-op）
  ├─ chunk-reload.ts chunk 加载失败识别与「仅刷新一次」去重
  └─ otel.ts         initObservability()：首屏后动态导入 OTel SDK
```

### 3.4 目标装配（A 端）

```text
App.tsx
  AppErrorBoundary                ← 改为 logger.error + reportError，移除 console
    ThemeProvider
      I18nProvider                ← S3 新增（内层需要字典的 Provider 先于 Toast）
        ToastProvider
          ObservabilityProvider   ← S3 新增：配置 logger / 惰性初始化 OTel
            QueryClientProvider   ← QueryCache.onError 记录日志（不弹 toast）
              AuthProvider
                PermissionProvider
                  RouterProvider
                    RouteErrorBoundary（包 AppLayout 的 Outlet）
```

> `I18nProvider` 必须早于 `ToastProvider`/`RouterProvider`，否则 ui 组件无法取到字典；`ObservabilityProvider` 不包裹 UI，避免影响首屏体积。

---

## 四、批次拆分

| 批次 | 内容 | 退出标准 |
|---|---|---|
| **S3-1 i18n 基座与错误码映射** | 新增 `packages/i18n`；`describeError`；8 码文案与展示位置；A/B 端接入 `I18nProvider` | 映射单测通过；列表/登录/危险操作错误态改用统一文案 |
| **S3-2 logger、trace_id 与错误边界** | 新增 `packages/observability`（logger/trace/chunk-reload）；装配 Provider；路由级错误边界；QueryCache 日志 | 生产无 `console.*`；异常日志含 trace_id；chunk 失败仅刷新一次 |
| **S3-3 OTel Web 接入** | `otel.ts` 动态加载；运行期 `monitoring` 消费；错误 span 上报；隐私白名单 | dsn 为空零请求；上报字段白名单单测；动态 chunk ≤120KB |
| **S3-4 文案迁移、离开提示与门禁** | 页面/组件/枚举文案迁移；`useUnsavedChangesGuard`；`check-i18n.mjs` + CI | 扫描零违规；hook 有使用说明；体积与全部门禁通过 |

每批次独立提交；提交信息按 `.trae/rules/git-commit-message.md`，scope 建议分别取 `i18n`、`observability`、`admin-web`、`scripts`。

---

## 五、批次详细设计

### 5.1 S3-1 i18n 基座与错误码映射（INF-DATA-02 / INF-I18N-01）

**新增文件**

```text
frontend/packages/i18n/package.json
frontend/packages/i18n/tsconfig.json
frontend/packages/i18n/src/index.ts
frontend/packages/i18n/src/messages/zh-CN.ts
frontend/packages/i18n/src/translate.ts
frontend/packages/i18n/src/translate.test.ts
frontend/packages/i18n/src/i18n-provider.tsx
frontend/packages/i18n/src/i18n-provider.test.tsx
frontend/packages/i18n/src/error-presentation.ts
frontend/packages/i18n/src/error-presentation.test.ts
```

**公共 API**

```ts
export type MessageKey = keyof typeof zhCN;
export type TranslateParams = Readonly<Record<string, string | number>>;

export type Translator = ((key: MessageKey, params?: TranslateParams) => string) & {
  readonly missingKeys: readonly string[];
};

export function createTranslator(messages: Readonly<Record<string, string>>): Translator;

export type I18nValue = { locale: 'zh-CN'; t: Translator };
export function I18nProvider(props: { children: ReactNode }): ReactElement;
export function useI18n(): I18nValue; // 无 Provider 时回退默认 zh-CN 翻译器

export type ErrorAction = 'inline' | 'toast' | 'page' | 'approval' | 'login';

export type ErrorPresentation = {
  code: number;
  messageKey: MessageKey;
  action: ErrorAction;
  retryable: boolean;
  traceId: string | undefined;
};

/** 结构化读取 { code, traceId }，不 import api-client，保持 i18n 为叶子包。 */
export function describeError(error: unknown): ErrorPresentation;
```

**错误码映射表（文档与单测共同事实源）**

| code / 场景 | messageKey | action | retryable | 展示位置 |
|---|---|---|---|---|
| `40000` 参数校验 | `error.validation` | inline | 否 | 表单字段错误 / 危险操作内联 |
| `40100` 未认证 | `error.unauthorized` | login | 否 | 登录页（清会话跳转由 Auth 负责） |
| `40300` 无权限 | `error.forbidden` | page | 否 | `ForbiddenPage`；快照刷新沿用现有 `onForbidden` |
| `40400` 资源不存在 | `error.notFound` | inline | 否 | 列表/详情内联 |
| `40900` 需审批 | `error.approvalRequired` | approval | **否** | `ApprovalNotice`；禁止自动重试 |
| `42900` 限流 | `error.rateLimited` | toast | 是 | 全局 toast |
| `50000` 服务端错误 | `error.server` | inline | 是 | 列表/表单内联 + 重试 |
| `50300` 服务不可用 | `error.unavailable` | page | 是 | fail-closed 不可用页 |
| `kind=network` | `error.network` | toast | 是 | 全局 toast |
| `kind=timeout` | `error.timeout` | toast | 是 | 全局 toast |
| 未知 | `error.unknown` | toast | 否 | 全局 toast |

- 映射优先级：显式 `code` → `kind` → `unknown`；`code` 缺失时 `traceId` 仍原样返回。
- 字典同时提供 `common.retry`、`common.cancel`、`common.confirm`、`common.search`、`common.reset`、`common.delete`、`common.empty`、`common.loading` 等基础 key。

**接入点（本批次）**

| 文件 | 改动 |
|---|---|
| `apps/admin-web/src/app/App.tsx` | 增加 `I18nProvider`；顺序见 §3.4 |
| `apps/biz-web/src/app/App.tsx` | 增加 `I18nProvider`（不接 Toast/OTel） |
| `features/iam-roles/RoleListPanel.tsx` | 列表错误与删除失败改用 `describeError` + `useI18n` |
| `features/permission-catalog/PermissionCatalogPanel.tsx` | 列表错误改用 `describeError` |
| `pages/LoginPage.tsx` | 40100 与通用失败改用 `describeError`；移除后端 `message` 直出 |

**验收**

- 单测覆盖 11 个场景的 `messageKey/action/retryable`，并断言 `40900.retryable === false`。
- 页面中不再出现 `cause.message` 直出；错误态仍展示可复制 `trace_id`。

### 5.2 S3-2 logger、trace_id 与错误边界（INF-OBS-01 / INF-OBS-03）

**新增文件**

```text
frontend/packages/observability/package.json
frontend/packages/observability/tsconfig.json
frontend/packages/observability/src/index.ts
frontend/packages/observability/src/logger.ts
frontend/packages/observability/src/logger.test.ts
frontend/packages/observability/src/trace.ts
frontend/packages/observability/src/trace.test.ts
frontend/packages/observability/src/chunk-reload.ts
frontend/packages/observability/src/chunk-reload.test.ts
frontend/packages/observability/src/report.ts
frontend/packages/observability/src/report.test.ts
frontend/apps/admin-web/src/app/observability/ObservabilityProvider.tsx
frontend/apps/admin-web/src/app/RouteErrorBoundary.tsx
```

**公共 API**

```ts
export type LogLevel = 'debug' | 'info' | 'warn' | 'error';

export type LogFields = {
  event: string;          // 例：'ui.uncaught'、'query.error'、'http.forbidden'
  traceId?: string;
  route?: string;
  code?: number;
  kind?: string;
  source?: string;
  durationMs?: number;
};

export type LogRecord = {
  level: LogLevel;
  event: string;
  time: string;           // ISO 8601
  fields: Readonly<Record<string, string | number | boolean>>;
};

export type LogSink = (record: LogRecord) => void;

export function configureLogger(options: { sink?: LogSink; level?: LogLevel; bufferSize?: number }): void;
export function resetLogger(): void;                 // 仅测试使用
export function getBufferedLogs(): readonly LogRecord[];
export const logger: {
  debug(fields: LogFields & { event: string }): void;
  info(fields: LogFields & { event: string }): void;
  warn(fields: LogFields & { event: string }): void;
  error(fields: LogFields & { event: string }): void;
};

export function getTraceId(error: unknown): string | undefined;
export function ensureTraceId(error: unknown): { traceId: string; clientGenerated: boolean };

export function reportError(error: unknown, context?: { route?: string; source?: string }): void;
export function setReportTransport(transport: ReportTransport | null): void;

export function isChunkLoadError(error: unknown): boolean;
export function reloadOnceForChunkError(): boolean;   // 已刷新过返回 false
```

**行为约定**

- 脱敏：键名匹配 `password|passwd|token|secret|cookie|authorization|refresh|mfa|credential` 直接丢弃；字符串截断 200 字符；非原始类型统一 `[object]`；循环引用不报错。
- 生产环境默认不输出到控制台（`no-console` 本就禁止）；`LogSink` 默认仅写入环形缓冲，OTel transport 在 S3-3 注入。
- `ensureTraceId` 在无后端 `trace_id` 时生成 `crypto.randomUUID()`，日志字段区分 `traceId` 与 `clientGenerated`，避免与后端链路混淆。
- `chunk-reload`：`sessionStorage` 键 `admin_frame_chunk_reload_guard`（非敏感、非令牌），仅记录「本次会话是否已为 chunk 失败刷新过」；命中后返回 false 并展示「请刷新页面」。
- 路由级边界：`RouteErrorBoundary` 包裹 `AppLayout` 的 `Outlet`，保留导航；「重试」调用 `resetError`；识别到 chunk 错误时先尝试一次性刷新。

**接入点**

| 文件 | 改动 |
|---|---|
| `app/AppErrorBoundary.tsx` | `componentDidCatch` 改 `logger.error` + `reportError`，删除 `console.error` |
| `app/observability/ObservabilityProvider.tsx` | `configureLogger`；读取 `config.monitoring`；S3-3 时调度 OTel 初始化 |
| `app/App.tsx` | 装配 `ObservabilityProvider`；`QueryClient` 增加 `QueryCache.onError`（记录 `event:'query.error'`、`route`、`code`、`traceId`），不弹 toast |
| `app/AppLayout.tsx` | 用 `RouteErrorBoundary` 包裹 `Outlet` |

**验收**

- `pnpm lint` 后 apps 内 `console.*` 为 0（`eslint-base.mjs` 的诊断豁免仅保留给配置/测试脚本）。
- `AppErrorBoundary` 触发时环形缓冲有 `ui.uncaught` 记录且含 `traceId`（无后端 trace 时为 `client_trace_id`）。
- chunk 错误单测：首次 `true`、第二次 `false`；非 chunk 错误恒 `false`。

### 5.3 S3-3 OTel Web 接入（INF-OBS-02）

**新增文件**

```text
frontend/packages/observability/src/otel.ts
frontend/packages/observability/src/otel.test.ts
frontend/packages/observability/src/privacy.ts
frontend/packages/observability/src/privacy.test.ts
```

**新增依赖（仅 observability 包）**

| 包 | 版本策略 | 用途 |
|---|---|---|
| `@opentelemetry/api` | 钉扎 | API 类型（若全部动态加载则仅在 `otel.ts` 内） |
| `@opentelemetry/sdk-trace-web` | 钉扎 | Web TracerProvider |
| `@opentelemetry/exporter-trace-otlp-http` | 钉扎 | OTLP HTTP 上报 |
| `@opentelemetry/resources` | 钉扎 | `service.name/version` |
| `@opentelemetry/semantic-conventions` | 钉扎 | 属性语义 |

均为 Apache-2.0，实施时先与 `license-allowlist.json` 核对。

**公共 API**

```ts
export type ObservabilityOptions = {
  endpoint: string | null;   // monitoring.dsn；空值 = 关闭
  sampleRate: number;        // 0～1
  release: string;           // 版本号
  channel: string;           // 'admin' | 'biz'
  env: string;
};

/** 幂等；endpoint 为空或已初始化时直接返回。首屏后 idle 调度，内部动态 import。 */
export function initObservability(options: ObservabilityOptions): void;
export function isObservabilityReady(): boolean;
```

**行为约定**

- 动态导入：`otel.ts` 被 `ObservabilityProvider` 动态引入，或在其内部 `await import(...)`；**任何 OTel SDK 代码不得进入入口 chunk**。
- 上报内容：span 名固定 `frontend.error`，属性仅含 `service.name`、`service.version`、`deployment.environment`、`page.route`、`error.code`、`error.kind`、`trace.id`、`client.trace.id`、`error.source`。
- 禁止上报：表单值、Token、Cookie、存储键值、URL query 原文、DOM 文本、用户标识。
- 采样：`TraceIdRatioBasedSampler(sampleRate)`；`sampleRate=0` 等价关闭。
- 失败静默：初始化异常只写 `logger.warn({ event:'otel.init_failed' })`，不得影响页面渲染。
- `reportError` 在 OTel 就绪前仅入缓冲；就绪后补发最近 20 条错误（不含普通日志）。

**部署前置（写入风险）**

- CSP `connect-src` 必须允许 collector 源；`config.js` 与 collector 地址需按环境配置，禁止写入构建期常量。
- collector 端点必须为同源反代或受信域名，避免第三方数据出境。

**验收**

- `endpoint=null` 时无任何网络请求、无动态 chunk 加载（单测 mock import）。
- 隐私单测：构造含 `password/token` 字段的错误上下文，上报对象不含这些键。
- `pnpm size`：OTel 动态 chunk ≤120KB，首屏预算不变。

### 5.4 S3-4 文案迁移、离开提示与文案门禁（INF-I18N-01 / INF-UI-04）

**迁移范围**

| 区域 | 文件 |
|---|---|
| 页面 | `pages/LoginPage.tsx`、`pages/HomePage.tsx`、`pages/ForbiddenPage.tsx`、`pages/NotFoundPage.tsx` |
| 装配 | `app/AppLayout.tsx`、`app/FatalConfigError.tsx`、`app/routes.tsx`（菜单标题） |
| feature | `features/iam-roles/*`、`features/permission-catalog/*` |
| entities | `entities/iam-role/status.ts`、`entities/permission/status.ts` |
| ui | `error-state.tsx`、`table-shell.tsx`、`empty-state.tsx`、`danger-action.tsx`、`dialog.tsx`、`page-header.tsx`、`form-layout.tsx`、`toast.tsx` |

- `packages/ui` 改为通过 `useI18n()` 取文案；无 Provider 时 `useI18n` 回退默认字典，既有组件测试无需改造。
- 业务枚举 `label` 改为 `MessageKey`，页面 `t(meta.labelKey)`；`tone` 保持不变。
- antd 组件属性（`placeholder`、`aria-label`）同样走 `t()`，避免门禁例外。

**未保存离开提示**

```ts
// apps/admin-web/src/shared/use-unsaved-changes-guard.ts
export function useUnsavedChangesGuard(
  when: boolean,
  options?: { messageKey?: MessageKey },
): { blocked: boolean; proceed: () => void; reset: () => void };
```

- 基于 `react-router` 的 `useBlocker` + `window.beforeunload`；仅提供能力与用法注释，本轮不改造登录页。
- 测试随 S4「apps 测试纳入 Vitest」补齐；S3 仅做 typecheck 与手工验证。

**文案门禁**

```text
frontend/scripts/check-i18n.mjs
  - 扫描 apps/*/src/**/*.{ts,tsx}、packages/ui/src/**/*.{ts,tsx}
  - 排除 *.test.*、packages/i18n/src/messages/**
  - 命中：字符串字面量与 JSX 文本中的 CJK 字符
  - 豁免：同一行含 `// i18n-ignore`（必须写理由）
  - 退出码非 0 时列出文件:行号
```

- `frontend/package.json` 增加 `"lint:i18n": "node scripts/check-i18n.mjs"`，并加入 `ci` 串联。
- `scripts/ci/check-frontend.sh` 在 eslint 之后执行。
- 已知边界：脚本不解析注释块，仅整行豁免；不做英文硬编码检查（超出 S3）。

---

## 六、契约、依赖与门禁影响

### 6.1 后端契约

- **无 OpenAPI 变更**：错误码集合已稳定；S3 只做前端映射。
- 运行期配置扩展：`AppRuntimeConfig.monitoring` 增加可选 `release: string | null`（缺省 `null`），用于上报标识；`config.js` 为部署期产物，不涉及公开 API 契约。
- OTLP 上报走自有 collector，不经业务 API 网关，不新增业务端点。

### 6.2 依赖边界与 ESLint

| 文件 | 改动 |
|---|---|
| `dependency-cruiser.cjs` | 新增 `i18n-is-leaf`：`packages/i18n` 不得依赖其他 packages；新增 `observability-is-leaf`：不得依赖 api-client/auth/ui；`ui → i18n` 允许（现有规则已放行，需在注释中明确） |
| `eslint-base.mjs` | 新增 `no-restricted-imports` 规则：`@opentelemetry/*` 仅允许 `packages/observability` 引入；为 `packages/observability/src/chunk-reload.ts` 增加 `sessionStorage` 单文件例外并注释理由 |
| `frontend/package.json` | 新增 `lint:i18n`；`ci` 改为 `... && pnpm lint:i18n && ...` |
| `scripts/ci/check-frontend.sh` | eslint 后追加 i18n 扫描 |
| `license-allowlist.json` | 核对 OTel 依赖许可证（预期无需新增条目） |

### 6.3 体积

| 风险点 | 处理 |
|---|---|
| i18n 字典进入首屏 | 当前字典规模预计 3～6KB gzip；若 admin 逼近 300KB 或 biz 逼近 260KB，优先按路由拆分子字典（`iam.*`/`permission.*` 仅随对应路由加载） |
| OTel SDK | 只允许动态 chunk；若单 chunk >120KB，拆分 exporter 与 sdk 两个动态 chunk |
| B 端偏差 | B 端本轮不接 Toast/OTel；S2 记录的「接 Toast 后 260.9KB 超 260KB」偏差继续保留，S6 与首个业务页面一并处理，禁止放宽预算 |
| `packages/ui` 依赖 i18n | i18n 零依赖且体积小，允许进入 ui 所在 chunk |

### 6.4 测试

| 包 | 用例 |
|---|---|
| `packages/i18n` | 插值、缺失 key 回退与 `missingKeys`、11 个错误场景映射、40900 不重试、结构化读取 `traceId` |
| `packages/observability` | 脱敏键名/截断/非原始类型、级别过滤、缓冲上限 50、`getTraceId`/`ensureTraceId`、chunk 只刷新一次、隐私白名单、endpoint 为空跳过 |
| 既有包 | 迁移文案后保持 97 个用例全绿；覆盖率 ≥70% |

apps 内新增边界与 hook 的组件测试列入 S4，S3 通过 typecheck、lint 与手工验证兜底。

---

## 七、实施顺序与提交

| 顺序 | 批次 | 建议提交信息 |
|---|---|---|
| 1 | S3-1 | `feat(i18n): 新增 zh-CN 字典与错误码文案映射` |
| 2 | S3-2 | `feat(observability): 新增脱敏日志与路由级错误边界` |
| 3 | S3-3 | `feat(observability): 接入 OTel Web 错误上报` |
| 4 | S3-4 | `refactor(frontend): 迁移界面文案并加入 i18n 门禁` |

- 依赖新增与代码变更同提交（`build(deps)` 仅在单独升级时使用）。
- 每批次先跑 `make frontend-ci`（含 `lint:i18n`），再提交。

---

## 八、风险与回滚

| 风险 | 影响 | 缓解 |
|---|---|---|
| 文案迁移遗漏 | 页面出现 key 或空白 | `t()` 缺失回退 key；`lint:i18n` 阻断新增硬编码；S3-4 单测/手工巡检 |
| 字典进入首屏导致预算超标 | 门禁失败 | 字典按路由拆分；OTel 强制动态；禁止放宽预算 |
| OTel 上报泄露敏感信息 | 合规风险 | 字段白名单 + 单测；`dsn` 空默认关闭；CSP 限制 `connect-src` |
| chunk 刷新循环 | 用户无法进入页面 | `sessionStorage` 一次性标记；第二次仅提示；单测覆盖 |
| `sessionStorage` 例外被滥用 | 令牌规则弱化 | 仅单文件例外 + ESLint 注释；Review 检查键名非敏感 |
| B 端长期缺少 Toast | 交互不一致 | 偏差登记在本文与 S2 文档，S6 收敛 |
| 后端 `trace_id` 缺失 | 定位困难 | 生成 `client_trace_id` 并明确标注来源 |

**回滚**

- S3-1、S3-2、S3-3 均为增量包与装配，移除 Provider/依赖即可回滚，无数据变更。
- S3-4 文案迁移可整体回滚到硬编码文案；无迁移脚本、无状态变更。
- OTel 可运维关闭：将 `config.js` 的 `monitoring.dsn` 置空，无需发版。

---

## 九、待评审决策点

| 编号 | 决策 | 建议（默认采纳） |
|---|---|---|
| D3-01 | i18n 实现形态 | **自研零依赖 `t()` + 类型化字典**，不引 i18next/react-intl；后续多语言再评估 |
| D3-02 | 上报传输归属 | **`packages/observability` 独占 OTel 依赖**，作为「仅 api-client 发 HTTP」的显式例外，并在架构 §12.2 注明 |
| D3-03 | release 来源 | 运行期配置新增可选 `monitoring.release`；缺省回退构建期 `__APP_VERSION__`，不写入内部地址 |
| D3-04 | chunk 刷新去重 | **单文件 `sessionStorage` 例外**（键名非敏感）；若拒绝，则退回「仅提示不自动刷新」 |
| D3-05 | 文案门禁实现 | **CI 脚本扫描 CJK**，不自写 ESLint 插件；整行 `// i18n-ignore` 豁免 |
| D3-06 | `traceparent` 注入 | **本轮非目标**，留待 S4/S5 与后端采样策略一并评估 |

---

## 十、Review Checklist

- [x] 错误码映射是否覆盖 8 个业务码与网络/超时/未知，且 40900 明确不可重试
- [x] 是否所有错误展示都携带可复制 `trace_id`，且不再直出后端 `message`
- [x] `packages/i18n` 是否零运行时依赖，`useI18n` 无 Provider 时是否可安全回退
- [x] `packages/observability` 是否未依赖 api-client/auth/ui，日志字段是否脱敏
- [x] OTel 是否仅动态加载，`dsn` 为空是否零请求
- [x] 体积预算是否未放宽，B 端偏差是否登记
- [x] `lint:i18n` 是否进入 `frontend-ci`，豁免是否都有理由（当前零豁免）
- [x] 文档是否同步：`frontend-architecture.md` §5.2/§11.4/§12/§15/§16、`frontend-infrastructure-plan.md` 状态

---

## 十一、实施落地记录

| 批次 | 落地内容 | 关键文件 |
|---|---|---|
| **S3-1** | `packages/i18n`（`zh-CN` 约 160 键、`t()`、`I18nProvider`、`describeError`）；A/B 端接入 `I18nProvider`；错误码映射单测 | `packages/i18n/src/**`、`apps/*/src/app/App.tsx` |
| **S3-2** | `packages/observability`（`logger`/`trace`/`report`/`chunk-reload`）；`AppErrorBoundary` 移除 `console`；`RouteErrorBoundary` 包 `Outlet`；QueryCache 日志；ESLint/边界规则 | `packages/observability/src/**`、`apps/admin-web/src/app/RouteErrorBoundary.tsx`、`packages/config/src/eslint-base.mjs`、`frontend/dependency-cruiser.cjs` |
| **S3-3** | `privacy.ts` 白名单、`otel.ts` 动态加载、错误 span；`monitoring.release` 运行期配置；`ObservabilityProvider` 首屏后 1s 初始化 | `packages/observability/src/otel.ts`、`packages/shared/src/runtime-config.ts`、`frontend/apps/*/public/config.js` |
| **S3-4** | 页面/组件/枚举文案迁移（`titleKey`、`labelKey`）；`useUnsavedChangesGuard`；`scripts/check-i18n.mjs` + `pnpm lint:i18n` 入 CI | `apps/admin-web/src/shared/use-unsaved-changes-guard.ts`、`frontend/scripts/check-i18n.mjs`、`frontend/package.json`、`scripts/ci/check-frontend.sh` |

**与方案的偏差**

1. `ToastProvider` 未接入 B 端、B 端不接 OTel：按 §6.3 保留，S6 与首个业务页面一并处理。
2. `lint:i18n` 未改 ESLint 规则，按 D3-05 以独立脚本实现；`make frontend-lint` 同步串上该脚本。
3. `traceparent` 未注入：按 D3-06 列为非目标。

**验证结果**

- `pnpm lint` / `lint:i18n` / `boundaries` / `typecheck` 通过；`pnpm test:ci` 31 文件 151 用例通过，覆盖率 91.24%。
- `pnpm build` + `pnpm size`：admin 首屏 277.3KB/300KB，biz 244.4KB/260KB；全部单 chunk ≤120KB（OTel 动态 chunk 约 38KB）。
- `format:check`、`license`（461 packages）、`audit` 通过；`pnpm e2e` 4 用例通过。

---

## 附录：变更记录

| 日期 | 版本 | 说明 |
|---|---|---|
| 2026-10-25 | v1.0 | 初稿：基于 S2 落地后的基线，提出 S3-1～S3-4 四批实施与 D3-01～D3-06 决策点 |
| 2026-10-25 | v1.1 | S3-1～S3-4 全部落地：补实施记录、勾选退出标准与 Checklist、同步架构文档 |
