# 前端工程规则（frontend/）

适用范围：`frontend/**`。与 [前端架构与工程规范](../../docs/frontend-architecture.md) 冲突时以文档为准，并同步修订文档。

## 分层与依赖

- 依赖方向固定：`app → pages → features → entities → shared`；`packages` 不得依赖 `apps`。
- 禁止跨 feature 横向 import；组合由 `pages` 完成，共用部分下沉到 `entities`/`shared`。
- 跨层引用只走 `index.ts` 公共出口，禁止 `@admin-frame/*/src/*` 深链。
- 组件文件用 PascalCase；hooks、工具、类型、API 模块用 kebab-case。

## 请求与鉴权

- 只有 `packages/api-client` 可发起 HTTP；业务代码禁止 `fetch`/`axios`。
- 只有 `packages/auth` 可访问令牌存储；禁止 `localStorage`，禁止 Token 进入 URL、日志与持久化存储。
- 契约类型只从 `packages/api-client` 公共出口派生；`generated/*` 禁止手改，变更须改 `api/openapi/**` 后重新生成。
- 写操作必须显式传 `Idempotency-Key`；同一次提交的重试复用同一 key。

## 页面与权限

- 前端路由不得占用 `/admin`、`/biz` 前缀；分页/筛选/排序写入 URL，`page_size` ≤ 100、默认 20。
- 权限码写入路由 `meta.permission`，与 `docs/iam/PERMISSIONS.md` 一致；权限接口不可用时 fail-closed。
- IAM 写操作收到 `40900` 必须转审批引导，禁止按普通冲突自动重试。
- 审批场景遵守职责分离：申请人不得操作自己的审批按钮。
- B 端禁止发送 `merchant_id` 作为筛选或越权依据；跨商户资源统一按不存在处理。

## 安全与 UI

- 禁止 `dangerouslySetInnerHTML`、`eval`、`new Function`；富文本必须白名单净化并注释理由。
- 业务代码禁止直接 `import ... from 'antd'`，统一从 `@admin-frame/ui` 导入。
- 生产代码禁止 `console.*`、`debugger`；错误展示携带 `trace_id`。
- 禁止在生产构建开启 Source Map 或把密钥、内部地址写入构建期常量与运行期配置。

## 门禁

- 提交前执行 `make frontend-ci`；不得通过放宽 `--max-warnings=0`、删除/跳过测试、调高体积预算来通过门禁。
- 契约、架构、安全变更须同步更新 `docs/frontend-architecture.md`、ADR、README 与 `docs/codegen/MANIFEST.md`。
- 临时过程文件不得入库；构建、覆盖率、E2E 报告保持在 `frontend/` 的忽略路径内。
