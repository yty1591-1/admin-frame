---
name: fe-slice-scaffold
description: Scaffold a vertical feature slice in apps/admin-web or apps/biz-web with layer boundaries. Use when the user asks 新增 feature or 生成页面骨架. Do not use for packages-only utilities.
---

# Feature 切片脚手架

## 前置

- 端点与类型已完成（参见 `fe-contract-sync`）。
- 确认端别与 feature 名：`frontend/apps/admin-web` 或 `frontend/apps/biz-web`，目录使用 kebab-case。

## 目录结构

```text
apps/<app>/src/features/<feature>/
├── index.ts                # 唯一公共出口
├── components/             # 组件文件 PascalCase
├── hooks/                  # use-*.ts，内部使用 TanStack Query
├── <feature>.api.ts        # 可选，调用 @admin-frame/api-client 的端点封装
└── query-keys.ts           # ['admin'|'biz', 领域, 资源, 形态, 参数]
```

页面组合放 `pages/`；实体类型与展示放 `entities/`；无业务语义的工具放 `shared/`。

## 步骤

1. 创建目录与 `index.ts`，只导出页面可用的 API，禁止导出内部实现。
2. 用 `queryKeys` 工厂组织缓存键，参数对象去除 `undefined` 并稳定排序。
3. 列表页把分页、筛选、排序写入 URL；`page_size` ≤ 100、默认 20，切换筛选重置到第 1 页。
4. 路由与按钮权限码与 `docs/iam/PERMISSIONS.md` 对齐；权限接口失败 fail-closed。
5. 高风险写操作：二次确认 + 操作原因 + `Idempotency-Key`；`40900` 转审批引导。
6. 测试：
   - 组件/纯函数 `*.test.ts(x)` 与被测文件同目录；
   - MSW handler 放 `frontend/packages/testing/src/handlers.ts`；
   - E2E 放 `frontend/e2e/<channel>/`。
7. 验证：`cd frontend && pnpm boundaries && pnpm typecheck && pnpm test:ci`。

## 禁止

- 跨 feature 横向 import；组合由 `pages` 完成，共用逻辑下沉到 `entities`/`shared`。
- 直接 `fetch`/`axios`、直接 `import antd`、直接访问令牌存储。
- 深链 `features/x/internal/*`，所有跨层引用必须走 `index.ts`。
