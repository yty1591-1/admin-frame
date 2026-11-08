# IAM 鉴权异常与授权回滚

| 字段 | 内容 |
|------|------|
| 告警 ID | `iam.authz.unavailable` / `iam.authz.deny_rate_high` / `iam.cache.invalidate_failed` / `iam.shadow.mismatch` / `iam.outbox.backlog` |
| 级别 | P1（不可用、缓存删除失败）/ P2（拒绝率、延迟、shadow 差异） |
| 服务 / 端 | admin-api / biz-api（`c-api`、`worker` 不启用 IAM） |
| 最后更新 | 2026-10-22 |

## 症状

- 触发条件：
  - `iam_authz_decisions_total{result="unavailable"}` 占比 > 0.1% 持续 1m（P1）。
  - `deny / total > 5%` 持续 5m（P2），或 `iam_authz_duration_seconds` P99 > 50ms。
  - `iam_authz_cache_total{result="error"}` 或 `iam_cache_invalidate_failed_total > 0` 持续 5m（P1）。
  - `iam_shadow_mismatch_total > 0`：shadow 与静态表判定不一致。
- 用户可见表现：管理端接口集中返回 `50300`（鉴权依赖不可用）或 `40300`（已认证但无权限）；
  `/readyz` 中 `iam_db` / `iam_cache` 为 `fail`。

## 影响

- 影响端与范围：A 端（`admin-api`）与 B 端（`biz-api`）全部需要权限点的接口；
  `iam.mode=enforce` 时失败关闭（不降级为放行），因此表现为大面积 503 而不是越权放行。
- 数据正确性风险：无。失败关闭不会写入未授权数据；但**授权变更接口**在审计不可写时会全部失败（`50000`）。

## 排查

1. 打开仪表盘：IAM 面板查看 `iam_authz_decisions_total`、`iam_authz_duration_seconds`、
   `iam_authz_cache_total`、`iam_authz_load_errors_total` 四个指标的分位与分布。
2. 查日志（注意脱敏，禁止 grep Token）：
   `service=admin-api level=error`，或 `msg="iam invalidate subject cache failed"`、
   `msg="record iam change audit"`。
3. 检查 `/readyz`、依赖与近期发布：
   - `iam_db` fail → 看连接池与 DB 状态（见 [db-connection-pool.md](./db-connection-pool.md)）。
   - `iam_cache` fail → Redis 不可达；`cache.driver=memory` 时不会注册该检查。
4. 区分故障类型：
   - `stage=db` 持续增长：回源 SQL 超时或连接池耗尽。
   - `stage=cache` / `cache_write`：缓存读写失败；L1 仍可服务，但命中率会掉。
   - `iam_shadow_mismatch_total > 0`：对照 `static_allow` / `iam_allow` 标签逐条解释角色配置差异。

## 缓解与恢复

1. **Redis 故障（enforce）**：先确认 `iam_db` 健康。Redis 仅承载 L2，L1 仍生效；
   若 Redis 持续不可用且 DB 压力不可接受，临时切换 `iam.mode=shadow`（需 Owner 批准并记录变更）。
2. **DB 故障**：按 [db-connection-pool.md](./db-connection-pool.md) 处理；期间鉴权请求返回 `50300`，
   由上游重试。禁止临时把 `iam.mode=off` 作为「恢复手段」以外的常规操作。
3. **误授权回滚**：
   1. `GET /admin/v1/iam/subjects/{subject_type}/{subject_id}/permissions` 确认当前有效权限。
   2. `DELETE /admin/v1/iam/bindings/{id}` 解绑（会主动失效该主体快照）。
   3. 若是角色权限过宽：`PUT /admin/v1/iam/roles/{id}/permissions` 全量替换为正确集合（需 `version`）。
   4. 核对审计：`rbac.role_change` / `rbac.binding_change` 中 `added` / `removed` 差量与操作人。
4. **缓存删除失败**：确认失效任务是否持续报错；L1 TTL（默认 3s）与 L2 TTL（默认 5m）是最终兜底，
   最坏撤销延迟为两层 TTL 之和。恢复后确认 `iam_cache_invalidate_failed_total` 停止增长。
5. **Outbox 积压（ENT-03 起）**：`iam_outbox_pending > 1000` 或 `iam_outbox_lag_seconds > 60`。
   投递由 `cmd/worker` 的平台 `AsyncRuntime` 执行（`async.outbox.poll_interval` 默认 1s）；
   先用 `SELECT status, COUNT(*) FROM async_outbox WHERE topic = 'iam.audit' GROUP BY status`
   定位是待投递、重试还是死信：
   - `pending` 增长但 `attempts` 不涨：worker 未运行（`async.enabled=false` 或进程已退出），
     检查 `worker` 启动日志与 `iam_outbox_delivery_total`。
   - `pending` + `iam_outbox_delivery_total{result="retry"}` 增长：审计出口不可用（日志 / SIEM 管道），
     修复出口后事件会在退避到期后自动补投，**禁止手工删事件或改状态**。
   - `processing` 长期不降：上一个投递者已崩溃，租约（`async.outbox.lease`，默认 30s）过期后会被自动回收重投（至少一次语义）；
     若持续堆积，检查是否有长事务锁住对应行。
   - `dead` / `failed`：重试耗尽或毒消息，对应 `async_dlq` 也会有一条未重放记录；
     保留行并按 `aggregate_type/aggregate_id` 补审计，修好 handler 后用 `cmd/worker -replay-dlq <event_id|all>` 重放
     （详见 [async-dlq.md](./async-dlq.md)）。
   若积压由单实例吞吐不足引起，优先扩大 `async.outbox.batch_size`（≤500）或缩短 `async.outbox.poll_interval`（≥100ms），
   再考虑多 worker 并行（认领是条件更新，天然幂等）。
6. **破玻璃授权（`iam_breakglass_active > 0`）**：按 P1 安全事件处理。
   1. `GET /admin/v1/iam/change-requests?action=breakglass_request&status=applied` 核对申请人与审批人。
   2. 确认原因与影响面后，`POST /admin/v1/iam/breakglass/{id}/revoke`（body 带当前 `version`）提前收回。
   3. 等待 L1（≤3s）+ L2（≤5m）过期，再用主体权限查询接口确认 `*` 已消失。
   4. 核对 `rbac.breakglass` 的 request / approve / revoke 三条审计，缺失即升级安全 Owner。
7. **写入事务与审计一致性（M4.5 起）**：业务变更与审计事件在同一个数据库事务内提交，
   正常不应出现「变更成功但审计缺失」。若怀疑 M4.4 及更早版本的历史窗口存在缺口，
   按业务行 `updated_at` 时间窗与 `async_outbox.aggregate_id`、审计目的地 `event_id` 三方对照：
   - 业务行存在、outbox 与审计目的地都没有对应记录：属历史缺口，补录审计并记录事故；
   - outbox 为 `dead` / `failed`：按第 5 条处理，**禁止直接改业务行来掩盖审计缺失**；
   - 变更请求整体返回 `50000` 且无任何副作用：通常是事务未能建立或提交失败（DB 故障），
     按 [db-connection-pool.md](./db-connection-pool.md) 排查；重试前确认请求具备幂等性
     （角色 `version`、审批单 `version`、outbox `event_id`）。
   运维注意：原子性只覆盖「业务写入 + 入队」，投递仍是至少一次，审计出口必须按 `event_id` 去重。
8. **B 端审批单（M4.6 起）**：B 端审批按 `merchant_id` 隔离，跨商户访问一律 `40400`。
   - 商户反馈「看不到自己的单据」：先确认 Token 的 `merchant_id` 与创建时的商户一致；
     `SELECT merchant_id, channel, status FROM iam_change_request WHERE id = ?` 对照，
     不一致属越权尝试（不是故障），按安全事件记录。
   - 商户无法审批：`iam:change:review` 是否授予、审批人是否等于申请人（SoD 自审 `40300`）。
   - B 端 `40300` 且错误为 `merchant id is required`：Token 缺失商户，属认证侧问题，不是 IAM 配置问题。
   - 应用失败（`approved` 不转 `applied`）：目标角色 / 绑定可能不属于该商户（`40400`）
     或已被删除；修正目标后按当前 `version` 重试，**禁止直接改审批单状态**。
   - 破玻璃不走 B 端：B 端创建 `breakglass_request` 与 B 端路由均拒绝，属预期行为。
9. **迁移回滚**：仅在非生产环境执行，按 `000005` → `000004` → `000003` → `000002` 顺序（先停 `cmd/worker` / Relay 并归档 `async_outbox`、`async_dlq`；`000005` 回滚会删除三张异步表及其中未投递的 IAM 审计事件）。
   `000003` 在权限点仍被 `iam_role_permission` 引用时会因外键 RESTRICT 失败，须先走正常撤权流程；
   `000002` 会丢失全部 IAM 授权与审批单商户归属，回滚前必须确认没有 pending / approved 的 B 端单据。
   身份域 `000001` 独立回滚。生产回滚只切配置（`iam.mode=off` + `async.enabled=false`），不回滚 schema。
10. 恢复后：`/readyz` 全部 `ok`、`unavailable` 归零、冒烟一次「授权 → 生效 → 撤销」闭环。

## 演练与验证

- 本节承诺的降级行为已固化为可执行测试（`internal/iam/app/fault_injection_test.go`，随 `make test-ci` 运行，
  矩阵见 [docs/rbac-iam.md §13.7](../rbac-iam.md)）：
  - Redis 不可用 → 回源 DB 仍可鉴权（`TestAuthorizeServiceDegradesWhenL2Unavailable`）；
  - 缓存失效失败 → 授权写入仍成功，由 TTL 兜底（`TestRoleServiceWriteSurvivesInvalidationFailure`）；
  - 审计出口抖动 → 积压可见并在退避后自动追平（`TestOutboxRelayDrainsBacklogAfterTransientFailure`）。
- 每季度至少一次 staging 演练：停 Redis、停 worker / 审计出口、注入 DB 超时，确认告警按「症状」触发并在 15 分钟内收敛。
- 演练必须在变更窗口内执行，结束后确认 `iam_outbox_pending = 0`、`iam_shadow_mismatch_total` 无新增。

## 备份与恢复

- **范围**：`iam_permission`、`iam_role`、`iam_role_permission`、`iam_role_binding`、`iam_role_data_scope`、
  `iam_change_request`，以及平台异步表 `async_outbox`、`async_dlq`、`async_consumed` 和迁移历史表
  `schema_migrations`（ENT-03 起 IAM 审计 / 缓存事件写入 `async_outbox`）。备份频率、保留期与演练要求遵循
  [DATA_STANDARDS §5](../DATA_STANDARDS.md)（DATA-07：每日全量、保留 ≥ 7 天、恢复演练 ≥ 每 6 个月一次）。
- **备份**：优先随业务库整库备份；单独备份 IAM 表时必须在停写窗口执行并记录备份点：

  ```bash
  mysqldump --single-transaction --routines --triggers <db> \
    iam_permission iam_role iam_role_permission iam_role_binding \
    iam_role_data_scope iam_change_request async_outbox async_dlq async_consumed schema_migrations
  ```

- **恢复步骤**：
  1. 先切 `iam.mode=off`（或摘除 admin / biz 流量），并停掉 `cmd/worker`（Relay 投递者），避免恢复期间继续认领事件；
  2. 恢复备份。仅恢复 IAM 相关表时按依赖顺序导入：`iam_permission` → `iam_role` → `iam_role_permission` →
     `iam_role_data_scope` → `iam_role_binding` → `iam_change_request` → `async_outbox` → `async_dlq` →
     `async_consumed` → `schema_migrations`；
  3. 校验：行数与备份点一致；启动期权限目录校验通过（`iam_permission` 与 `internal/iam/catalog` 一致）；
     `SELECT status, COUNT(*) FROM async_outbox WHERE topic IN ('iam.audit','iam.cache') GROUP BY status` 无异常堆积；
  4. 恢复后 `pending` / `processing` 事件可能重复投递，审计出口必须按 `event_id` 去重；
     `dead` / `failed` 行保留并按「缓解与恢复」第 5 条处理，**禁止手工改状态或删事件**；
  5. 清空 IAM 缓存（L1 随进程重启；L2 删除 IAM key 前缀或等待 TTL），再切回原 `iam.mode`；
  6. 冒烟：主体权限查询 → 授权 → 生效 → 撤销 闭环，并核对 `rbac.role_change` / `rbac.binding_change` 审计。
- **记录与限制**：每次恢复演练记录备份点、起止时间、校验结果与偏差，写入变更记录；
  生产备份禁止未脱敏恢复到共享开发环境（DATA-07）。

## 季度访问复核

- **频率与 Owner**：每季度一次，建议在季度末两周执行，由安全 Owner 发起、IAM / SRE 配合；
  首次执行由安全 Owner 排期，`iam.mode=enforce` 灰度前应完成一次全量复核。
- **范围**：A 端高权限角色（权限码含 `iam:*`、`data:export`、`config:*`）及其全部绑定（含已过期与即将过期）、
  本季度破玻璃使用记录、B 端商户管理员绑定。
- **数据来源**（只读）：`GET /admin/v1/iam/roles`、`GET /admin/v1/iam/bindings`（按 `subject` / `role` 过滤）、
  `GET /admin/v1/iam/subjects/{subject_type}/{subject_id}/permissions`；批量导出可对 `iam_*` 表执行只读 SQL。
- **步骤**：
  1. 导出角色、绑定、有效期及最近变更审计（`rbac.role_change` / `rbac.binding_change` / `rbac.breakglass`）；
  2. 安全 Owner 逐条复核：是否在职 / 是否仍需要、是否最小权限、`expires_at` 是否合理、是否存在长期未使用的绑定；
  3. 需回收的绑定 / 权限走审批单（`iam.approval.enabled=true` 时禁止直写），应用后确认快照已失效；
  4. 复核结论、差异与处置结果归档到审计存储或工单，保留期按审计策略执行；
  5. 破玻璃使用后 24h 内由申请人提交复盘说明，纳入当季复核。
- **离职 / 转岗**：24h 内解绑并依赖 `expires_at` 兜底；复核发现的越权或异常按安全事件升级。

### 工具链操作（P1-7，替代纯人工导出）

- **发起复核**：`POST /admin/v1/iam/certifications` 创建活动（`due_at` 必须为未来时间）→
  `POST /admin/v1/iam/certifications/{id}/activate` 从当前有效绑定生成任务
  （按 `(campaign_id, subject_id, role_id)` 幂等，重复激活不产生重复待办）。
- **裁决**：`POST /admin/v1/iam/certifications/{id}/tasks/{taskId}/decision`，
  `decision=confirm|revoke` 且原因必填；重复提交幂等，相反裁决返回 409。
- **单个活动补扫**：`POST /admin/v1/iam/certifications/{id}/auto-expire`（写权限点），
  与定时扫描共用同一段状态机，可重入。
- **超期扫描**：Chart 的 `certificationSweep.enabled=true` 会渲染 CronJob
  `<release>-admin-frame-certification-sweep`（默认 `0 2 * * *`，生产已开启），
  执行 `worker -certification-sweep` 一次性进程；工作日可在 Job 日志中确认
  `certification sweep finished` 与 `scanned/overdue/expired/failed/tasks` 计数。
  - 手动补跑：`kubectl -n <ns> create job --from=cronjob/<CronJob 名> cert-sweep-manual`。
  - 告警 `iam.certification.sweep_failed`（P2，`kube_job_status_failed`）或 `failed>0`：
    先看 `certification sweep campaign failed` 日志中的 `campaign_id`，常见原因是审计写入失败
    （outbox/DB 不可用）或活动在扫描期间被人工关闭（关闭属正常竞争，不计失败）。
    修复后重跑即可，扫描本身幂等；确认补跑成功后删除历史失败 Job，否则该告警会因
    失败对象仍存在而持续（`kubectl -n <ns> delete job <失败 Job 名>`）。
  - `auto_revoke=false`（默认）时扫描只把超期任务标记为 `expired`，不回收绑定；
    开启回收前必须在 staging 演练一次，并确认指纹不一致的任务落 `skipped` 而非被回收。
- **证据归档**：`GET /admin/v1/iam/certifications/{id}/export?format=csv|json` 导出任务级
  完整字段，作为等保 / SOC 2 周期证据；导出本身也写 `rbac.certification` 审计。
- **收敛判定**：活动关闭（`POST .../{id}/close`）后本轮复核才算完成；关闭前应确认
  `pending` 任务为 0——扫描只处理 `active` 活动，关闭后残留的待办不会再被自动过期。

## 升级

- P1：15 分钟内无收敛 → L2 Owner → 必要时 L3 SRE；同时通知安全 Owner（授权面故障属安全事件）。
- P2：30 分钟无收敛或影响面扩大 → 升级为 P1。
- 升级联系：见 [README.md](./README.md) 值班入口。

## 相关链接

- 告警目录：[ALERTS.md](./ALERTS.md)
- 专文：[docs/rbac-iam.md](../rbac-iam.md)（§8 鉴权决策、§11 可观测性、§15 发布与回滚）
- ADR：[docs/adr/0004-rbac-iam-domain.md](../adr/0004-rbac-iam-domain.md)
- 权限目录：[docs/iam/PERMISSIONS.md](../iam/PERMISSIONS.md)
- 发版回滚：[release-rollback.md](./release-rollback.md)
