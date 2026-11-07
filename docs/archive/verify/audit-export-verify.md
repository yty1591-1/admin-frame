# 审计导出与到期归档验证记录（ENT-04 T5/T6）

> **归档**：本文件已于 2026-11-07 归档，只读不再维护；现行结论以 [`AUDIT_STANDARDS.md`](../../AUDIT_STANDARDS.md) 为准，归档索引见 [`archive/README.md`](../README.md)。

日期：2026-11-03　环境：本地 MySQL 8.0 + MinIO（`docker-compose.integration.yml`）+ 本地进程（admin-api / worker）

本记录覆盖 `AC-ENT-04-1`～`AC-ENT-04-4`，并列出本轮**只有真跑才暴露**的缺陷。

## 1. 异步导出（T5）

### 1.1 HTTP 端到端（真实进程 + 真实库 + 真实对象存储）

配置：`audit.store=db`、`audit.export.async=true`、`storage.driver=minio`、`auth.mode=demo`；
API 只提供路由、worker 独占导出循环（`audit export runner started poll_interval=2s`）。

| 步骤 | 请求 | 结果 |
|---|---|---|
| 查询基数 | `GET /admin/v1/audit/events?action=auth.login&from=…&to=…` | `total=7 items=7` |
| 创建任务 | `POST /admin/v1/audit/events/export`（同一过滤条件） | `202`，`status=pending`，`expires_at=+7d` |
| 轮询 | `GET /admin/v1/audit/exports/{id}` | `succeeded`，`rows=7`，返回 `download_path` |
| 下载 | `GET /admin/v1/audit/exports/{id}/download` | `200`，`text/csv; charset=utf-8`，`Content-Disposition: attachment`，8 行（表头 + 7） |
| 同步导出对照 | `GET /admin/v1/audit/events/export`（同条件） | 8 行，与异步文件一致 |
| 越权下载 | 另一账号请求同一导出 | `404`（不区分「不存在」与「非本人」） |
| 未认证 | 无 `Authorization` | `401` |

库内状态：

```
audit_export_job: 375865296516284416  succeeded  row_count=7  object_key=audit/exports/2026/11/03/…csv
audit_event:      data.export  mode=async           rows=7  filters=action
                  data.export  mode=async-download  rows=7  filters=action
```

### 1.2 集成测试（可重复执行）

`tests/integration/audit`：

- `TestAuditExportJobEndToEnd`：建任务 → 两个 worker 并发 `SKIP LOCKED` 抢占（必须拿到不同任务）→ CSV 落 MinIO → 下载行数与库内 `COUNT(*)` 对账 → 到期清理删对象并置 `expired` → 校验 `mode=async` / `async-download` 审计各就位。
- `TestAuditExportJobStaleRecovery`：running 超时任务按 `attempts` 分流——用尽重试置 `failed`，未用尽回 `pending`。
- `TestAuditRetentionArchivesThenDeletes`：12 条 400 天前事件按 5/5/2 分三批归档，归档对象行数合计 = 删除行数 = 12，未到期 3 条保留。
- `TestAuditReaderAndRetention`：查询过滤、分页总量、`Stream` 升序、`CountBefore`/`DeleteBefore`。

全量集成套件（`go test -p 1 ./tests/integration/...`）在干净卷上 6 个包全绿：`async / audit / iam / identity / multiinstance / smoke`。

## 2. 本轮修掉的缺陷

| # | 缺陷 | 后果 | 修法 |
|---|---|---|---|
| 1 | `cmd/worker` 未 blank-import 存储驱动 | worker 启动即退出：`storage: unknown driver: driver "minio"`；异步导出永远无人执行 | `cmd/worker` 导入 `pkg/storage/all` |
| 2 | 归档上传用 `bytes.Buffer` | S3 在非 TLS 下要求可定位流算校验和，`PutObject` 直接失败（真 MinIO 才报） | 改 `bytes.NewReader(payload.Bytes())` |
| 3 | 保留期原本「只删不归档」 | 违反 AUD-07「到期归档销毁」，销毁后无副本 | 新增 `ObjectArchiver`：每批先归档再按 ID 删除；归档失败即中止，一行不删 |
| 4 | 卡死任务无限重排 | 一个反复让 worker 崩溃的任务永远是最早的 pending，饿死后续导出 | `RequeueStale` 按 `attempts` 分流：用尽置 `failed` |
| 5 | `iam` 集成测试 reset 删除 `system_admin` | 后续包 `Migrate` 阶段 `bootstrap: system_admin role is missing`；整套集成测试无法全绿 | reset 保留种子角色，列表断言改为按 code 判定 |
| 6 | 三处既有测试断言写反 | `Stream` 升序却断言最后一条是 -1h 事件；会话归档后仍要求在线表能查到；角色列表按总数断言（种子角色也在列表） | 逐条改正为可证伪的断言 |

第 1～4 条都属于「写得出、跑不通」的类型：单测全绿、门禁全过，只有真进程 + 真 S3 才暴露。

## 3. 门禁

`make check-audit` 新增两项可执行断言（此前只靠 Review）：

- `scripts/ci/audit_export_check.py`：路由 ↔ OpenAPI 路径 ↔ 状态枚举三向比对；新增路由不补 spec 即失败。
- `scripts/ci/audit_immutability_check.py`：保留期仓储之外不得出现 `DELETE FROM`；涉及 `audit_event` 的文件不得出现 `UPDATE`。

## 4. 复现步骤

```
docker compose -f docker-compose.integration.yml up -d --wait mysql redis minio
docker compose -f docker-compose.integration.yml run --rm minio-init
export TEST_MYSQL_DSN="app:app@tcp(127.0.0.1:3306)/admin_frame_test?parseTime=true&loc=UTC"
export TEST_S3_ENDPOINT=http://127.0.0.1:9000 TEST_S3_ACCESS_KEY=minioadmin \
       TEST_S3_SECRET_KEY=minioadmin TEST_S3_BUCKET=admin-frame
go test -count=1 -p 1 ./tests/integration/...
```

注意：MySQL 卷必须干净（`down -v`）。identity 用例使用固定主键，复用同一卷会在归档表撞主键。
