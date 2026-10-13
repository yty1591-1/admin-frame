# 代码生成清单（GEN-01）

| ID | 类型 | 源 | 命令 | 输出路径 | 入库 | status |
|----|------|----|------|----------|------|--------|
| openapi-admin | source | `api/openapi/admin.yaml` + `components.yaml` | （手写） | 同左 | yes | active |
| openapi-biz | source | `api/openapi/biz.yaml` + `components.yaml` | （手写） | 同左 | yes | active |
| openapi-c | source | `api/openapi/c.yaml` + `components.yaml` | （手写） | 同左 | yes | active |
| mockery-domain | generated | domain/app 接口 | `make mock`（`.mockery.yaml`） | `internal/<domain>/mocks/` | yes | reserved |
| sqlc | generated | schema + `db/queries/` | `sqlc generate` | MANIFEST 增补时填写 | yes | reserved |

**规则**

- `status=active` 且 `类型=generated`：输出文件必须带 `Code generated` / `DO NOT EDIT`。  
- `reserved`：尚未产生文件；一旦提交生成物须改为 `active`。  
- OpenAPI 为源文件，不做「生成头」要求；变更须 `make openapi`。
