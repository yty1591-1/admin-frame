---
name: vercel-react-best-practices
description: Adapted Vercel React performance rules for this Vite SPA. Use when writing or reviewing React components, data fetching, or bundle size. Do not use for Next.js server components.
license: MIT
---

# React 性能规则（Vite SPA 裁剪版）

源自 Vercel Engineering 的 React 规则集，已裁剪为适用于 `apps/admin-web`、`apps/biz-web`（React 19 + Vite 7 + TanStack Query + Ant Design）的客户端规则。上游来源与许可见 [ATTRIBUTION.md](ATTRIBUTION.md)。

## 何时使用

- 新增或重构页面、feature、`packages/ui` 组件
- 处理请求瀑布、列表渲染、重渲染、首屏体积
- 审查代码性能问题或 `pnpm size` 超标

不适用于 Next.js Server Components、RSC、SSR 水合、Server Actions 等场景（已移除）。

## 规则分类与优先级

| 优先级 | 分类 | 影响 | 前缀 |
|---|---|---|---|
| 1 | 消除请求瀑布 | 关键 | `async-` |
| 2 | 包体积优化 | 关键 | `bundle-` |
| 3 | 客户端数据获取 | 中高 | `client-` |
| 4 | 减少重渲染 | 中 | `rerender-` |
| 5 | 渲染性能 | 中 | `rendering-` |
| 6 | JavaScript 性能 | 低中 | `js-` |
| 7 | 进阶模式 | 低 | `advanced-` |

### 1. 消除请求瀑布

- `async-cheap-condition-before-await` — 先判断廉价的同步条件，再发起异步请求
- `async-defer-await` — 把 `await` 移到真正使用结果的分支
- `async-parallel` — 独立请求使用 `Promise.all`

### 2. 包体积优化

- `bundle-barrel-imports` — 识别无谓的聚合导出，避免把整包拖进入口（项目例外见下）
- `bundle-analyzable-paths` — 使用可静态分析的导入与路径
- `bundle-conditional` — 仅在功能启用时加载对应模块
- `bundle-preload` — 在 hover/focus 时预加载，提升感知速度

### 3. 客户端数据获取

- `client-event-listeners` — 去重全局事件监听
- `client-passive-event-listeners` — 滚动监听使用 passive
- `client-localstorage-schema` — 持久化数据需版本化并最小化（本项目仅限非敏感 UI 偏好）

### 4. 减少重渲染

- `rerender-defer-reads` — 仅在回调中使用的状态不要订阅
- `rerender-memo` — 昂贵子树抽成可记忆组件
- `rerender-memo-with-default-value` — 默认的非原始类型 props 提到组件外
- `rerender-dependencies` — effect 依赖使用原始值
- `rerender-derived-state` — 订阅派生布尔值而非原始值
- `rerender-derived-state-no-effect` — 在渲染中派生，不用 effect 同步
- `rerender-functional-setstate` — 使用函数式 `setState` 保持回调稳定
- `rerender-lazy-state-init` — 昂贵初始值用函数式 `useState`
- `rerender-simple-expression-in-memo` — 简单表达式不要滥用 `memo`
- `rerender-split-combined-hooks` — 拆分依赖相互独立的 hooks
- `rerender-move-effect-to-event` — 交互逻辑放事件处理器
- `rerender-transitions` — 非紧急更新使用 `startTransition`
- `rerender-use-deferred-value` — 昂贵渲染使用 `useDeferredValue`
- `rerender-use-ref-transient-values` — 高频临时值使用 ref
- `rerender-no-inline-components` — 不要在组件内定义组件

### 5. 渲染性能

- `rendering-content-visibility` — 长列表使用 `content-visibility`
- `rendering-hoist-jsx` — 静态 JSX 提到组件外
- `rendering-conditional-render` — 条件渲染用三元而非 `&&`
- `rendering-usetransition-loading` — loading 状态优先 `useTransition`

### 6. JavaScript 性能

- `js-cache-property-access` — 循环中缓存对象属性
- `js-cache-function-results` — 模块级缓存函数结果
- `js-combine-iterations` — 合并多次 `filter`/`map`
- `js-early-exit` — 提前返回
- `js-flatmap-filter` — `flatMap` 合并映射与过滤
- `js-hoist-regexp` — 正则提到循环外
- `js-index-maps` — 重复查找建 `Map`
- `js-length-check-first` — 先判长度再比较
- `js-min-max-loop` — 求极值用循环而非排序
- `js-set-map-lookups` — O(1) 查找用 `Set`/`Map`
- `js-tosorted-immutable` — 使用 `toSorted()` 保持不可变

### 7. 进阶模式

- `advanced-effect-event-deps` — `useEffectEvent` 结果不要放进 effect 依赖
- `advanced-event-handler-refs` — 事件处理器放 ref
- `advanced-init-once` — 应用级初始化只执行一次
- `advanced-use-latest` — 稳定的 latest callback ref

## 项目约束与例外（优先级高于本技能）

1. **公共出口优先**：FE-04 要求跨层引用走 `index.ts`。`bundle-barrel-imports` 只用于识别重复聚合与循环依赖，不得以此为理由改为 `@admin-frame/*/src/*` 深链。
2. **请求统一走 api-client**：数据获取一律使用 `@admin-frame/api-client` + TanStack Query；本技能不含 SWR 规则，也不要引入 SWR。
3. **令牌安全**：`client-localstorage-schema` 仅适用于非敏感 UI 偏好；Token 只允许 `packages/auth` 的 `memory`/`session` 模式。
4. **体积门禁**：优化后必须跑 `cd frontend && pnpm size`；不得通过把 `antd` 提前放入入口 chunk 来“优化”加载。
5. **AntD 表格**：长列表优先使用 AntD Table 虚拟滚动或 `content-visibility`，不要自建第二套表格。

## 使用方式

按需读取 `rules/<rule>.md`，每条规则包含原因、错误示例、正确示例与参考链接。只应用与当前改动相关的规则，避免一次性大面积重构。

## 验证

```bash
cd frontend && pnpm typecheck
cd frontend && pnpm test:ci
cd frontend && pnpm size
```
