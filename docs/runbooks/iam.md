# IAM 鉴权异常与授权回滚

| 字段 | 内容 |
|------|------|
| 告警 ID | `iam.authz.unavailable` / `iam.authz.deny_rate_high` / `iam.cache.invalidate_failed` / `iam.shadow.mismatch` |
| 级别 | P1（不可用、缓存删除失败）/ P2（拒绝率、延迟、shadow 差异） |
| 服务 / 端 | admin-api / biz-api（`c-api`、`worker` 不启用 IAM） |
| 最后更新 | 2026-10-21 |

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
5. **Outbox 积压（M4 起）**：`iam_outbox_pending > 1000` 或 `iam_outbox_lag_seconds > 60`
   时先确认 worker 消费位点，再决定扩容消费者；禁止手工删事件。
6. **迁移回滚**：`migrate down 000003` → `migrate down 000002`，仅在非生产环境执行；
   生产回滚只切配置（`iam.mode=off`），不回滚 schema。
7. 恢复后：`/readyz` 全部 `ok`、`unavailable` 归零、冒烟一次「授权 → 生效 → 撤销」闭环。

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
