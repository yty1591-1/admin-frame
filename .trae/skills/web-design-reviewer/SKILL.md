---
name: web-design-reviewer
description: Inspect and fix layout, responsive, and accessibility issues in running web apps. Use when asked to review a page UI or fix layout problems. Do not use for backend or API review.
license: MIT
---

# 页面设计审查（项目裁剪版）

源自 `github/awesome-copilot` 的 web-design-reviewer，已裁剪为只服务本仓库的 A/B 两端页面。上游来源与许可见 [ATTRIBUTION.md](ATTRIBUTION.md)。

## 适用范围

- `frontend/apps/admin-web`、`frontend/apps/biz-web` 的页面与共享组件
- Ant Design 5 + `@admin-frame/ui` 设计令牌 + Vite 开发服务器
- 只做视觉、响应式、可访问性缺陷修复，不做重新设计

不适用于后端接口、契约、权限逻辑与业务流程评审。

## 前置

1. 页面已运行：`pnpm dev:admin` 或 `pnpm dev:biz`，使用 `http://127.0.0.1:5173` / `5174`（不要用 `localhost`，避免 IPv6 解析问题）。
2. 浏览器能力可用：优先使用 `TRAE-browseruse` 技能完成导航、快照、截图、控制台检查。
3. 需要修复时必须能读写源码并搜索组件。

## 工作流

1. **信息收集**：确认 URL、端别、页面路由、目标视口；从 `frontend/package.json` 与 `src/pages` 定位页面与组件。
2. **视觉检查**：按 [references/visual-checklist.md](references/visual-checklist.md) 检查布局溢出、重叠、对齐、间距、文本截断、响应式、对比度、焦点态。
3. **视口验证**：375 / 768 / 1280 / 1920 四个宽度各截图一次。
4. **定位源码**：优先在 `src/pages`、`src/features`、`packages/ui` 中按组件名或文案搜索。
5. **最小修复**：一次只修一个问题，修复后重新截图对比。
6. **回归与收尾**：确认其他视口未破坏；运行下方验证命令；输出报告。

## 修复红线

- 禁止直接 `import antd`，必须经 `@admin-frame/ui`。
- 禁止修改设计令牌、品牌色与全局主题来“顺手统一”。
- 禁止引入新依赖、全局 CSS 覆盖或大规模重构。
- 禁止改动业务逻辑、权限判断、请求参数与路由 `meta.permission`。
- 禁止为通过视觉检查而删除内容、隐藏溢出或降低可访问性。
- 组件文件名保持项目约定：apps 内 PascalCase，`packages/ui` 基础组件 kebab-case 例外。

## 验证

```bash
cd frontend && pnpm typecheck
cd frontend && pnpm test:ci
```

涉及路由或交互变更时补跑对应 Playwright 用例：`cd frontend && pnpm e2e`。

## 输出报告

```markdown
# 页面设计审查

## 概要
- 目标 URL / 端别：
- 视口：
- 问题数 / 已修复：

## 问题
### [P1] 标题
- 页面 / 元素：
- 问题：
- 修复文件：
- 修复说明：
- 截图：修复前 / 修复后

## 未修复项
- 原因与建议
```

## 原则

- 先截图留证，再改代码。
- 一次一个问题，逐项验证。
- 遵循现有设计系统与页面模式。
- 超过 3 次仍无法修复同一问题时停止并询问用户。
