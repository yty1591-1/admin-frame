# S2 阶段基础设施实施方案（UI 基座与交互）

> **版本**：v0.1（评审稿）
> **状态**：待评审
> **基线**：2026-10-25，`main` 分支 `53f0488`（S1 权限与数据层已落地）
> **范围**：`frontend-infrastructure-plan.md` §5.3 + §5.5，对应 INF-UI-03～08、INF-IAM-06
> **规范依据**：[前端架构与工程规范](./frontend-architecture.md) §11、[规划与差距评估](./frontend-infrastructure-plan.md) §4.5

---

## 一、目标与退出标准

### 1.1 目标

让业务页面**不直接依赖 antd 细节**：表格、表单布局、弹窗确认、全局通知、页面骨架、状态标签、危险操作六类高频交互，全部通过 `@admin-frame/ui` 与端内 `entities`/`shared` 统一提供。

### 1.2 退出标准（S2 完成）

- [ ] 业务页面从 `@admin-frame/ui` 导入组件率为 100%，无 `antd` 直接引用（ESLint 已强制）。
- [ ] 权限目录页完成改造，成为 TableShell + PageHeader + StatusTag 的接入范例。
- [ ] 新增一个「角色删除」写操作示例：`Idempotency-Key` + 危险确认 + 原因 + `40900` 审批引导，全链路走通且 **40900 不自动重试**。
- [ ] 全局通知（成功/失败）统一出口；失败提示带可复制 `trace_id`。
- [ ] 新增组件均有 RTL 单测；`pnpm size` 路由 chunk 仍 ≤ 120 KB（gzip）、入口 ≤ 300 KB。
- [ ] 关键组件键盘可达（Enter/Space 触发、Esc 关闭、焦点回到触发元素）。

### 1.3 不在 S2 范围

- i18n 字典与 `t()`（S3）、OTel/logger（S3）、apps 测试纳入 Vitest 与 axe（S4）、大表格虚拟滚动与列设置持久化（P2）、上传/导出（P2）。

---

## 二、现状基线

| 能力 | 现状 | 缺口 |
|---|---|---|
| 设计令牌与主题 | `themeTokens` + `ThemeProvider`（L2） | 无 |
| 状态组件 | `PageLoading`、`EmptyState`、`ErrorState`（L2） | 无 |
| 表格容器 | 权限目录页直接使用 `Table` + 手写分页/错误态 | 无统一容器、无工具栏/列设置、错误与空态重复实现 |
| 表单基座 | 登录页用 RHF + Zod 但无布局组件 | 标签/必填/错误/帮助文案各页自写 |
| 弹窗与确认 | 无 | 无 Modal/Drawer/确认封装，无危险操作规范 |
| 全局通知 | 无 | 无统一 toast，失败无 `trace_id` 复制入口 |
| 页面骨架 | 仅 `AppLayout` | 无 PageHeader/面包屑/Skeleton/筛选区布局 |
| 状态与枚举展示 | 权限目录页内联 `Tag` 与中文映射 | 颜色与文案散落在页面 |
| 高风险操作 | 无 | 无确认、原因、幂等键、40900 引导 |

`packages/ui` 现有公共出口：[index.ts](file:///d:/MyDocument/admin-frame/frontend/packages/ui/src/index.ts)（`AppShell`、`EmptyState`、`ErrorState`、`PageLoading`、`ThemeProvider`、`themeTokens`、primitives）。

---

## 三、组件清单与 API 设计

> 文件沿用 `packages/ui` 既有例外：文件名 kebab-case，导出名 PascalCase（架构 §4 注）。
> 所有组件只依赖 `antd`、`react` 与 `@admin-frame/ui` 内部模块；**不得**依赖 `api-client`/`auth`（`ui-not-on-api` 门禁）。

### 3.1 PageHeader / PageSkeleton（INF-UI-07）

`page-header.tsx`

```ts
type PageHeaderProps = {
  title: string;
  description?: string;
  breadcrumb?: readonly { label: string; href?: string }[];
  extra?: ReactNode;          // 主操作按钮区
};
```

- 标题/描述/面包屑/操作区固定布局；面包屑只渲染，不解析路由（避免 ui 依赖 router）。
- `page-skeleton.tsx`：`PageSkeleton({ rows?: number })` 用于路由级首屏；列表区域首屏再用 `TableShell` 的 `loading`。

**验收**：标题层级为 `h1`（页面内唯一）；面包屑为 `nav[aria-label="面包屑"]`。

### 3.2 StatusTag（INF-UI-08）

`status-tag.tsx`

```ts
type StatusTone = 'success' | 'processing' | 'warning' | 'error' | 'default';
type StatusTagProps = { tone: StatusTone; label: string };
```

- ui 只提供「语义 tone → 颜色」的映射；**业务枚举与中文文案留在 entities**。
- 权限目录页改造：`entities/permission/status.ts` 提供 `permissionChannelMeta` / `permissionKindMeta` / `permissionStatusMeta`，页面仅做查表。

**验收**：颜色不单独承载语义（保留文本）；页面零硬编码颜色。

### 3.3 TableShell（INF-UI-03）

`table-shell.tsx`

```ts
type TableShellProps<T> = {
  columns: TableColumnsType<T>;
  rows: readonly T[] | undefined;
  rowKey: keyof T | ((row: T) => string);
  total: number;
  page: number;
  pageSize: number;
  loading: boolean;
  error?: { message: string; traceId?: string } | null;
  emptyText?: string;
  toolbar?: ReactNode;                       // 筛选区/批量操作
  onPageChange: (page: number, pageSize: number) => void;
  onRetry?: () => void;
};
```

- 受控组件：分页与数据由调用方（feature + `useListParams`）提供，ui 不感知 Query/URL。
- 内置：首屏 `Skeleton`（`loading && rows === undefined`）、翻页保留旧数据由调用方 `placeholderData` 保证、错误 `ErrorState` + 重试、空态 `EmptyState`、服务端分页（默认 20，上限 100，`showSizeChanger`）。
- `toolbar` 放在表格上方，同一容器内，避免各页自建卡片 + 间距。

**验收**：权限目录页迁移后删除所有内联 Table 分页/错误逻辑；RTL 覆盖「空 / 错 / 翻页回调参数」。

### 3.4 表单基座（INF-UI-04）

`form-layout.tsx`

```ts
type FormSectionProps = { title: string; description?: string; children: ReactNode };
type FormFieldProps = {
  label: string;
  htmlFor: string;
  required?: boolean;
  hint?: string;
  error?: string;
  children: ReactNode;      // 表单控件
};
```

- 只做布局与可访问性：`label[for]`、必填标识、`hint` 用 `aria-describedby`、`error` 用 `role="alert"`。
- RHF 绑定留在页面/feature（`useForm` + `register`/`Controller`），ui 不引入 `react-hook-form`。
- 提供 `useZodForm` 约定文档（不新增包）：`zodResolver` 在 feature 内使用。

**验收**：示例表单提交中禁用、字段错误可见、未保存离开提示由 `useBlocker` 在 feature 侧演示（可选，若路由 API 需额外验证则延后到 S3）。

### 3.5 弹窗与确认（INF-UI-05）

`dialog.tsx`

```ts
type ConfirmDialogProps = {
  open: boolean;
  title: string;
  content: ReactNode;
  confirmText?: string;
  danger?: boolean;
  loading?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
};
```

- 基于 antd `Modal` 薄封装：焦点陷阱、Esc 关闭、`destroyOnHidden`，避免页面自建 `Modal` 细节。
- Drawer 同理：`SidePanel({ open, title, width, onClose, children, footer })`（P1，若 S2 内无使用场景可延后）。

**验收**：Esc 关闭、确认按钮 loading 时不可重复提交、取消不触发 action。

### 3.6 全局通知（INF-UI-06）

`toast.tsx`

```ts
type ToastApi = {
  success: (message: string) => void;
  error: (input: { message: string; traceId?: string }) => void;
};
function ToastProvider({ children }: { children: ReactNode }): ReactElement;
function useToast(): ToastApi;
```

- 基于 antd `App.useApp()` 的 `message`，在 `ThemeProvider` 内再包一层 `App` 上下文。
- 失败 toast 使用 `duration: 6` 并在描述中提供可复制 `trace_id`；**禁止**页面直接使用 `message`/`notification`（它们不在 ui 白名单出口中）。
- 装配位置：`App.tsx` → `ThemeProvider` → `ToastProvider` → `QueryClientProvider`（顺序见 §4）。

**验收**：错误上报 `trace_id` 可复制；RTL 断言成功/失败文案。

### 3.7 DangerActionConfirm 与 40900 审批引导（INF-UI-05 + INF-IAM-06）

`danger-action.tsx`

```ts
type DangerActionProps = {
  open: boolean;
  title: string;                 // 如「删除角色」
  target: string;                // 对象名称/标识
  impacts: readonly string[];    // 影响范围逐条说明
  reasonRequired?: boolean;      // IAM 写操作默认 true
  reasonMaxLength?: number;      // 默认 200
  loading?: boolean;
  error?: { message: string; traceId?: string } | null;
  onConfirm: (reason: string) => void;
  onCancel: () => void;
};
```

- 红色主按钮、二次确认、影响说明逐条展示、原因输入（≤200 字计数）、错误内联展示并带 `trace_id`。
- 提交由调用方负责生成一次 `Idempotency-Key`（`createIdempotencyKey()`），**重试复用同一 key**，成功后 `invalidateAfterWrite([列表 key, 详情 key, 权限快照 key])`。

`apps/admin-web/src/shared/approval.ts`

```ts
function isApprovalRequired(error: unknown): boolean;  // AppError.code === 40900
```

- 收到 `40900`：关闭危险确认弹窗 → 打开 `ApprovalNotice`（说明「该操作需审批」+ 预填 payload 摘要）→ **不自动重试、不按普通冲突处理**。
- 申请人不得操作自己的审批按钮（SoD）：审批入口若出现在同一页面，按 `snapshot.subject_id` 与申请人比对后禁用。

**示例写操作**：角色列表（`GET /admin/v1/iam/roles`）+ 删除角色（`DELETE /admin/v1/iam/roles/{id}`，需 `iam:role:write`）。若后端未开启审批，此示例以 MSW `40900` 夹具验证引导分支。

**api-client 增补**：`adminListIAMRoles` 分页包装、`adminDeleteIAMRole`（显式 `idempotencyKey`）、`isApprovalRequired` 导出。

**验收**：40900 用例有单测与组件测试；不带 `Idempotency-Key` 的写调用在开发期直接抛错（`HttpClient` 已支持 `requireIdempotencyKey`）。

---

## 四、装配与依赖

```text
App.tsx
  AppErrorBoundary
    ThemeProvider
      ToastProvider            ← S2 新增（antd App 上下文）
        QueryClientProvider
          AuthProvider
            PermissionProvider
              RouterProvider
```

- `ui-not-on-api`：TableShell 的错误信息通过 `{ message, traceId }` 传入，不引用 `AppError` 类型。
- `features` 仍不得 import `app`：写操作示例由 `pages/iam-roles/RoleListPage.tsx` 取 `useAuth().client` 后注入 feature。
- 新增 ui 组件全部从 [index.ts](file:///d:/MyDocument/admin-frame/frontend/packages/ui/src/index.ts) 导出；业务代码禁止深链。

---

## 五、实施批次

| 批次 | 内容 | 退出标准 |
|---|---|---|
| **S2-1 骨架与状态** | `PageHeader`、`PageSkeleton`、`StatusTag` + `entities/permission/status.ts`；改造权限目录页 | 页面无内联 Tag 映射；RTL 通过 |
| **S2-2 表格容器** | `TableShell`（工具区/空/错/加载/分页）；迁移权限目录页 | 页面删除手写分页与错误态；`pnpm size` 通过 |
| **S2-3 表单基座** | `FormSection`/`FormField`；登录页或新增筛选表单迁移 | label/`aria-describedby`/错误可访问；提交中禁用 |
| **S2-4 弹窗与通知** | `ConfirmDialog`、`ToastProvider`/`useToast`；App 装配 | 错误 toast 带 `trace_id`；Esc 可关闭 |
| **S2-5 高风险操作** | `DangerActionConfirm`、`isApprovalRequired`、角色列表/删除示例、40900 引导 | 幂等键复用；40900 不自动重试；审批引导预填 payload |

每批次独立提交，按 `git-commit-message.md` 拆分；先 `packages/ui` 组件与单测，再接页面改造。

---

## 六、测试与门禁

- **单测（packages/ui）**：每个新组件至少 2 个用例（正常渲染 + 关键交互/错误分支）；分页回调断言 `(page, pageSize)`。
- **门禁**：`pnpm format:check`、`pnpm lint`、`pnpm typecheck`、`pnpm test:ci`、`pnpm boundaries`、`pnpm build`、`pnpm size`、`pnpm license`、`pnpm e2e`。
- **体积风险**：新增组件会引入 `Modal`、`message`、`Table` 的 antd 依赖，均随使用页面 lazy chunk 加载；若某路由 chunk 逼近 120 KB（gzip），优先拆「危险操作」为独立页/独立弹窗 chunk，不放宽预算。
- **不做**：不新增第二套组件库；不引入 Storybook（D-06 延后）；不引入 moment/dayjs 之外的日期库。

---

## 七、待评审决策点

| 编号 | 决策 | 建议（默认采纳） |
|---|---|---|
| D2-01 | TableShell 是否内置 URL 状态 | **否**：分页/筛选仍由 feature 的 `useListParams` 控制，ui 保持无路由依赖 |
| D2-02 | 表单基座是否引入 RHF 依赖到 `packages/ui` | **否**：ui 仅提供布局与可访问性，RHF 留在页面/feature |
| D2-03 | toast 选型 | **antd `App.useApp().message`** 薄封装，失败保留 6s 且可复制 `trace_id` |
| D2-04 | 危险操作「原因」是否必填 | **必填**（IAM 写操作），≤200 字；非 IAM 可配置为非必填 |
| D2-05 | 40900 交互形态 | **弹窗告知 + 审批引导**，不跳转、不自动重试；申请人自审按钮禁用 |
| D2-06 | 角色删除示例是否在 S2 接入真实端点 | **是**：复用已生成契约；后端审批开启时以 40900 分支验证 |

---

## 八、Review Checklist

- [ ] 新组件是否只依赖 `@admin-frame/ui` 内部与 antd，无 `api-client`/`auth`/router 依赖
- [ ] 是否有任一页面仍直接使用 antd 的 Modal/message/Table 细节
- [ ] 危险操作是否覆盖「确认 + 影响说明 + 原因 + 幂等键 + 40900 引导」
- [ ] 新组件是否有单测，错误态是否带 `trace_id`
- [ ] 体积门禁与分层门禁是否本地通过
- [ ] 文档是否同步：`frontend-architecture.md` §11（若组件名/边界变化）、`frontend-infrastructure-plan.md` INF 状态
