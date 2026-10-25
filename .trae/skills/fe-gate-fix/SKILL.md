---
name: fe-gate-fix
description: Run and fix frontend quality gates. Use when make frontend-ci fails or the user asks 跑前端门禁, 修 lint, 修类型 or 修体积. Do not use for backend gates.
---

# 前端门禁修复

## 步骤

1. 先定位失败项：依赖已安装时执行 `bash scripts/ci/check-frontend.sh --skip-install`；提交前执行 `make frontend-ci`。
2. 按固定顺序逐项修复，每修一项先重跑该项：
   - 格式：`cd frontend && pnpm format`（只格式化 `frontend/`）
   - Lint：`cd frontend && pnpm exec eslint . --fix`，剩余问题手工修
   - 边界：`cd frontend && pnpm boundaries`，按规则移动代码或改从 `index.ts` 导出
   - 类型：`cd frontend && pnpm typecheck`，禁止 `any`、`@ts-ignore`、非空断言
   - 测试/覆盖率：`cd frontend && pnpm test:ci`，补用例，不扩大 `exclude`
   - 构建：`cd frontend && pnpm build`
   - 体积：`cd frontend && pnpm size`，优先路由懒加载、按需引入，避免 `antd` 进入入口 chunk
   - 许可证：`cd frontend && pnpm license`，替换依赖或经评审更新 `license-allowlist.json`
   - 依赖安全：`cd frontend && pnpm audit --audit-level=high`，升级或替换依赖
3. 全部通过后重跑 `make frontend-ci`，确认无回归再报告结果。

## 禁止

- 放宽 `--max-warnings=0`、添加无理由的 `eslint-disable`、删除或跳过测试。
- 调高 `frontend/scripts/check-size.mjs` 预算，或把 `antd` 塞进 `manualChunks` 规避体积门禁。
- 跳过契约生成漂移检查或忽略高危依赖告警。
- 自动提交；修复完成后等待用户确认。
