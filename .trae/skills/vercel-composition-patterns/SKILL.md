---
name: vercel-composition-patterns
description: React composition patterns for reusable components. Use when designing packages/ui APIs or refactoring boolean prop proliferation. Do not use for page-level state or business logic.
license: MIT
---

# React 组件组合模式

源自 Vercel 的 composition patterns，适用于 `packages/ui` 以及跨 feature 复用的复杂组件。上游来源与许可见 [ATTRIBUTION.md](ATTRIBUTION.md)。

## 何时使用

- 设计或重构 `packages/ui` 组件 API
- 组件布尔 props 膨胀、条件分支难以维护
- 需要 compound components、context provider、显式变体
- 审查共享组件架构

不适用于页面级业务状态、数据请求编排与权限逻辑。

## 规则分类

| 优先级 | 分类 | 影响 | 前缀 |
|---|---|---|---|
| 1 | 组件架构 | 高 | `architecture-` |
| 2 | 状态管理 | 中 | `state-` |
| 3 | 实现模式 | 中 | `patterns-` |
| 4 | React 19 API | 中 | `react19-` |

### 1. 组件架构

- `architecture-avoid-boolean-props` — 不要用布尔 props 定制行为，改用组合
- `architecture-compound-components` — 复杂组件用共享上下文组织子组件

### 2. 状态管理

- `state-decouple-implementation` — 只有 Provider 知道状态如何管理
- `state-context-interface` — 用统一的 state/actions/meta 接口做依赖注入
- `state-lift-state` — 兄弟组件共享的状态提升到 Provider

### 3. 实现模式

- `patterns-explicit-variants` — 用显式变体组件代替布尔模式
- `patterns-children-over-render-props` — 优先 children 而非 `renderX` props

### 4. React 19 API

- `react19-no-forwardref` — 不再使用 `forwardRef`，`useContext` 改为 `use()`

## 项目约束

1. 基础组件文件名保持 kebab-case、导出名 PascalCase（FE-05 例外）。
2. 业务代码只能从 `@admin-frame/ui` 公共出口导入，禁止直接 `import antd`。
3. 不要把 AntD 封装成通用低代码组件框架；抽象只解决当前仓库重复出现的需求。
4. Provider 不承载请求与权限逻辑；权限使用 `packages/auth` 原语，数据使用 TanStack Query。
5. 变更 `packages/ui` 公共 API 时，同步检查两个 app 的调用方并跑 `pnpm typecheck`。

## 使用方式

按需读取 `rules/<rule>.md`。一次只重构一个组件族，并补充或更新对应测试。

## 验证

```bash
cd frontend && pnpm typecheck
cd frontend && pnpm test:ci
```
