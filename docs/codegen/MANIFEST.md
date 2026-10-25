# 代码生成清单（GEN-01）

| ID | 类型 | 源 | 命令 | 输出路径 | 入库 | status |
|----|------|----|------|----------|------|--------|
| openapi-admin | source | `api/openapi/admin.yaml` + `components.yaml` | （手写） | 同左 | yes | active |
| openapi-biz | source | `api/openapi/biz.yaml` + `components.yaml` | （手写） | 同左 | yes | active |
| openapi-c | source | `api/openapi/c.yaml` + `components.yaml` | （手写） | 同左 | yes | active |
| openapi-docs | generated | `api/openapi/{admin,biz,c}.yaml` + `components.yaml` | `make openapi-docs` | `dist/openapi/docs/` | no | active |
| openapi-ts-client | generated | `api/openapi/{admin,biz,c}.yaml` + `components.yaml` | `make openapi-client` | `dist/openapi/client/` | no | active |
| openapi-changelog | generated | 分端契约 + git 基线 | `make openapi-changelog`（或 `make openapi-artifacts`） | `dist/openapi/changelog/` + `dist/openapi/contract-commit.txt` | no | active |
| frontend-api-ts | generated | `api/openapi/{admin,biz}.yaml` + `components.yaml` | `pnpm gen:api`（openapi-typescript 7.4.0） | `frontend/packages/api-client/src/generated/` | yes | active |
| mockery-domain | generated | domain/app 接口 | `make mock`（`.mockery.yaml`） | `internal/<domain>/mocks/` | yes | reserved |
| sqlc | generated | schema + `db/queries/` | `sqlc generate` | MANIFEST 增补时填写 | yes | reserved |

**规则**

- `status=active` 且 `类型=generated` 且 `入库=yes`：输出文件必须带 `Code generated` / `DO NOT EDIT`。
- `入库=no` 的产物只允许落在 `dist/`（已 gitignore），不适用 GEN-02 生成头，但命令与工具版本必须可复现（脚本内固定版本）。
- `reserved`：尚未产生文件；一旦提交生成物须改为 `active`。
- OpenAPI 为源文件，不做「生成头」要求；变更须 `make openapi`。
