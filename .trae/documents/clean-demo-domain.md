# 清理订单/文件演示域并同步文档

## Context

`internal/order` 与 `internal/files` 是本仓库的演示业务域，后续业务开发不需要。它们目前仍被三个 API 进程、worker、OpenAPI 契约、权限种子、CI 门禁与多份规范文档引用。若只删目录会导致编译失败、迁移 checksum/目录一致性测试失败、OpenAPI 破坏性门禁失败。

目标：完整移除演示域代码与运行时装配，保留平台能力（`internal/platform`、`internal/security`、`internal/iam`、`pkg/*`），并让所有门禁与文档回到自洽状态。

## 关键约束（来自现有实现）

1. **迁移只追加**：`pkg/migrate` 会拒绝“已应用但源文件缺失”和 checksum 漂移，因此 `000001_create_orders`、`000003_seed_iam_permissions` 不能删改，必须新增迁移。
2. **权限四处一致**：`internal/iam/catalog/catalog_test.go` 强制 `catalog.go` ↔ `perm.go` ↔ `migrations/*_seed_*.up.sql` ↔ `docs/iam/PERMISSIONS.md` 一致。演示权限码必须**保留**并改为 `StatusDeprecated`（保留既有授权、禁止新授），不能直接删常量或种子行。
3. **OpenAPI 删除路径 = 破坏性变更**：三个 spec 的 `info.version` 必须提升 major（2.0.0 → 3.0.0），否则 `check-openapi.sh` 失败。
4. **保留 `cmd/worker`**：`ARCHITECTURE.md`、`CONFIG_STANDARDS.md`（CFG-01）、`RELEASE_STANDARDS.md` 都以 worker 进程为前提；仅移除 order 事件消费依赖，改为生命周期骨架。
5. **TEST-03 必测函数只在演示域**：`TestMerchantIsolation` / `TestUserOwnership` 仅存在于 `internal/order/app/service_test.go`，删除后必须重新指定 IAM 中真实存在的 B 端隔离用例，并同步规范/PR 模板。

## 变更清单

### 1. 删除演示域代码

- 删除 `internal/order/**`（31 个文件）与 `internal/files/**`（7 个文件）。
- 保留 `pkg/storage`（worker 仍用 `platform.OpenStorage`）与 `internal/platform.Deps.Storage` 字段。

### 2. 进程装配与静态回滚表

- [cmd/admin-api/register.go](file:///d:/MyDocument/admin-frame/cmd/admin-api/register.go)：删除 order/files 的 import 与装配（含 `memory_repo` 健康检查），仅保留 `iamadmin.Register`。
- [cmd/biz-api/register.go](file:///d:/MyDocument/admin-frame/cmd/biz-api/register.go)：删除 order 的 import、`memory_repo` 健康检查与 `mux.Handle("/", ...)`，保留 `iambiz.Register`。
- [cmd/c-api/register.go](file:///d:/MyDocument/admin-frame/cmd/c-api/register.go)：清空为 `return nil` 的最小实现（保留 `platform.Deps`/`http.ServeMux` 参数与“暂无 C 端业务路由”注释）。
- [cmd/biz-api/main.go](file:///d:/MyDocument/admin-frame/cmd/biz-api/main.go)：删除 `internal/order` import；`staticChecker` 不再授予业务权限（`rbac.New()`），注释说明 mode=off 回滚表不放业务授权。
- [cmd/admin-api/iam.go](file:///d:/MyDocument/admin-frame/cmd/admin-api/iam.go)：`staticChecker` 的 `ops` 角色改授平台只读权限（使用 `internal/iam` 常量：`PermPermissionRead`、`PermRoleRead`、`PermBindingRead`、`PermChangeRead`、`PermAuditRead`、`PermConfigRead`）。
- [cmd/worker/main.go](file:///d:/MyDocument/admin-frame/cmd/worker/main.go)：删除 `eventbus` import 与 `bus.Drain()` 消费；保留配置加载、日志、storage 打开/关闭、`signal.NotifyContext`，ticker 改为 Debug 级“idle，等待 ENT-03 异步运行时接入”。

### 3. 迁移与权限目录

- 新增 `migrations/000007_drop_orders.up.sql`（`DROP TABLE IF EXISTS orders;`，注意先删或随表删除不存在的 FK；本表无外键）与 `000007_drop_orders.down.sql`（按 `000001` 原样重建表与索引）。
- 新增 `migrations/000008_retire_demo_permissions.up.sql`：
  ```sql
  UPDATE iam_permission SET status='deprecated', updated_at=UTC_TIMESTAMP()
  WHERE code IN ('order:read','order:write','file:read','file:write');
  ```
  同文件 down 恢复为 `active`。文件名不含 `seed`，因此不会被 `catalog_test.go` 的种子解析逻辑扫描。
- [internal/iam/catalog/catalog.go](file:///d:/MyDocument/admin-frame/internal/iam/catalog/catalog.go#L249-L285)：4 条 order/file 条目的 `Status` 由 `StatusActive` 改为 `StatusDeprecated`，名称/排序不变。
- [internal/iam/perm.go](file:///d:/MyDocument/admin-frame/internal/iam/perm.go#L49-L56)：保留常量，注释标注“演示域已移除；保留为 deprecated，禁止新授”。
- [docs/iam/PERMISSIONS.md](file:///d:/MyDocument/admin-frame/docs/iam/PERMISSIONS.md)：目录表新增第 5 列“状态”（`docRow` 正则为前缀匹配，追加列不影响测试）；4 条演示权限标“已弃用”，其余“活跃”；把 `order:read` 等示例改为 `iam:role:read`；更新第 107/110 行回滚表与 B 端角色示例。

### 4. OpenAPI 契约

- [admin.yaml](file:///d:/MyDocument/admin-frame/api/openapi/admin.yaml)、[biz.yaml](file:///d:/MyDocument/admin-frame/api/openapi/biz.yaml)、[c.yaml](file:///d:/MyDocument/admin-frame/api/openapi/c.yaml)：
  - 删除 `orders` tag、全部 `/…/v1/orders…` path、`OrderID` 参数、`Order` / `OrderListData` schema；
  - `info.version` → `3.0.0`，并在 description 追加“3.0.0：移除订单演示域契约（破坏性）”；
  - 数据范围示例 `order` → `iam`，鉴权示例 `order:read` → `iam:role:read`。
- [components.yaml](file:///d:/MyDocument/admin-frame/api/openapi/components.yaml#L144)：示例消息 `order already cancelled` → 通用文案。

### 5. CI 门禁

- [check-transport-imports.sh](file:///d:/MyDocument/admin-frame/scripts/ci/check-transport-imports.sh)：改为遍历 `internal/*/transport`，对每个域的 `admin|biz|c` 子目录禁止互引，避免硬编码 order 后检查失效。
- [check-layering.sh](file:///d:/MyDocument/admin-frame/scripts/ci/check-layering.sh)：LAYER-02/03/04 的 `(order|files|iam)` 改为动态发现 `internal/*` 业务域（排除 `platform|security|shared`），新增业务域时自动覆盖。
- [check-test-gates.sh](file:///d:/MyDocument/admin-frame/scripts/ci/check-test-gates.sh)：`REQUIRED_TESTS` 改为 IAM 中已存在的 `TestBizRoleTenantIsolation`、`TestBizChangeRequestTenantIsolation`。
- [.github/PULL_REQUEST_TEMPLATE.md](file:///d:/MyDocument/admin-frame/.github/PULL_REQUEST_TEMPLATE.md#L31) 与 [.gitee/PULL_REQUEST_TEMPLATE.md](file:///d:/MyDocument/admin-frame/.gitee/PULL_REQUEST_TEMPLATE.md#L31)：同步新的必测函数名。

### 6. 文档同步

- [README.md](file:///d:/MyDocument/admin-frame/README.md)：worker 描述改为“后台任务骨架（异步运行时待 ENT-03）”；删除 C 端下单 curl 示例，改为健康检查/IAM 调用示例；目录树改为 `internal/iam`、`internal/platform` 等实际结构。
- [docs/TESTING_STANDARDS.md](file:///d:/MyDocument/admin-frame/docs/TESTING_STANDARDS.md#L51-L56)：TEST-03 表格改为 B 端 IAM 隔离用例；注明 C 端当前仅 probe，首个 C 端资源落地时恢复 `TestUserOwnership`；同步第 127 行示例路径。
- [docs/PERFORMANCE_STANDARDS.md](file:///d:/MyDocument/admin-frame/docs/PERFORMANCE_STANDARDS.md#L43)：示例行改为 `iam.authorize.CacheHit`；第 57 行命令改为 `BenchmarkAuthorize_CacheHit`。
- [docs/perf/BASELINES.md](file:///d:/MyDocument/admin-frame/docs/perf/BASELINES.md)：删除 `order.domain.NewOrder` 行（其余 5 个 benchmark 已存在，`check-perf.sh` 仍通过）。
- [docs/CODE_STYLE.md](file:///d:/MyDocument/admin-frame/docs/CODE_STYLE.md#L86)：import 示例改 `internal/iam/domain`。
- [.mockery.yaml](file:///d:/MyDocument/admin-frame/.mockery.yaml)：注释示例改 `internal/iam/app`。
- [docs/storage-s3-oss.md](file:///d:/MyDocument/admin-frame/docs/storage-s3-oss.md)：标注“files 演示 API 已随演示域移除”，保留 `pkg/storage` 能力说明。
- [docs/rbac-iam.md](file:///d:/MyDocument/admin-frame/docs/rbac-iam.md)：权限常量来源改为 `internal/iam/perm.go`（演示权限标注 deprecated）；第 1361/1585 行的必测函数改为新名称；角色授权示例改为平台权限。
- [docs/platform-remediation-plan.md](file:///d:/MyDocument/admin-frame/docs/platform-remediation-plan.md)：顶部加“历史整改记录，文中演示域路径已移除”状态说明，不改写历史 AC。
- [docs/production-readiness-gaps.md](file:///d:/MyDocument/admin-frame/docs/production-readiness-gaps.md)：更新“演示域处理”措辞、执行记录、状态跟踪与附录 C（由“移除清单”改为“已移除清单/完成情况”）。

## 验证

按顺序执行，全部必须通过：

1. `gofmt -l cmd internal pkg`（无输出）
2. `go build ./...`
3. `go vet ./...`
4. `go test ./...`
5. `bash scripts/ci/check-layering.sh`
6. `bash scripts/ci/check-transport-imports.sh`
7. `bash scripts/ci/check-migrations.sh`
8. `bash scripts/ci/check-config.sh`
9. `bash scripts/ci/check-test-gates.sh`
10. `bash scripts/ci/check-perf.sh`
11. `bash scripts/ci/check-openapi.sh`（若首次 `go run oasdiff` 因无网络失败，记录为环境限制并至少完成本地 `oasdiff validate`；CI Linux 仍会强制执行）
12. 全局检索确认无代码引用：`internal/order`、`internal/files` 仅允许出现在历史文档说明与迁移文件名中。
13. 条件允许时执行 `make ci` 作为最终汇总门禁。

## 风险与处理

- **迁移校验**：只新增 000007/000008，绝不修改 000001/000003。
- **权限目录测试**：保留 4 个演示权限码与种子行，只改 catalog 状态与文档状态列。
- **OpenAPI 版本**：三端同步升 major；任一漏改都会让 breaking 门禁红。
- **TEST-03**：新必测函数均位于 `internal/iam/transport/biz/router_test.go`，已在当前仓库存在。
