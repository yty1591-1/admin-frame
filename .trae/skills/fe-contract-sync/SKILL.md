---
name: fe-contract-sync
description: Regenerate OpenAPI TypeScript types and update endpoint wrappers. Use when api/openapi changes, the user asks 同步契约, or generated types are stale. Do not use for backend handler changes.
---

# 契约同步

## 前置

- 契约源只改 `api/openapi/{admin,biz}.yaml` 与 `components.yaml`；生成物位于 `frontend/packages/api-client/src/generated/{admin,biz}.ts`，带 `DO NOT EDIT` 头。
- 前端不探测运行中的服务，不自造字段；缺失能力登记到 `docs/frontend-architecture.md` 第十八章。

## 步骤

1. 确认契约改动范围：`git status --short api/openapi`。
2. 重新生成：`cd frontend && pnpm gen:api`（等价 `make frontend-gen`）。
3. 审查生成物差异：`git diff -- frontend/packages/api-client/src/generated`，确认只包含预期字段变化，排除无关格式漂移。
4. 若新增或变更端点：
   - 在 `frontend/packages/api-client/src/endpoints/<admin|biz>/*.api.ts` 补薄封装，只做参数与返回映射；
   - 领域类型别名放 `frontend/packages/api-client/src/types.ts`，从 `generated/*` 派生，不得手写响应结构；
   - 写操作要求显式 `Idempotency-Key`，IAM 写操作保留 `40900` 专用映射。
5. 核对 `docs/codegen/MANIFEST.md`：`frontend-api-ts` 必须为 `active`，命令、版本（openapi-typescript 7.4.0）与输出路径一致。
6. 若为 breaking change，同一 MR 内适配所有调用方，禁止“先合并前端、后补契约”。
7. 验证：
   - `cd frontend && pnpm typecheck`
   - `cd frontend && pnpm test:ci`
   - `git status` 确认生成物已入库，且无临时文件残留。

## 禁止

- 手改 `generated/*`，或为其添加 `eslint-disable`。
- 业务代码直接 `import` `generated/*`。
- 跳过生成物漂移检查；CI 会重新生成并执行 `git diff --exit-code`。
