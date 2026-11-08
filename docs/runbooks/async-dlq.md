# 异步死信（async_dlq）积压与重放

| 字段 | 内容 |
|------|------|
| 告警 ID | `async.dlq.depth`、`async.dlq.stalled`、`async.outbox.dead`、`async.outbox.backlog` |
| 级别 | P2（单主题 DLQ 增长且业务可延迟）/ P1（核心审计 / 权限事件连续进入 DLQ） |
| 服务 / 端 | worker（投递方）、admin-api / biz-api（`async.outbox.publish_in_api=true` 时） |
| 最后更新 | 2026-11-08（补 `async.dlq.stalled` 与「重放有效性判定」；告警 ID 由「建议」改为实际 ID） |

## 症状

- 触发条件：
  - `async_dlq_depth > 0` 且在 10 分钟内未回落；
  - `min_over_time(async_dlq_depth[30m]) > 0`（`async.dlq.stalled`）：死信持续 30 分钟未减少；
  - `async_outbox_delivery_total{result="dead"}` 或 `{result="poison"}` 持续增长；
  - `async_outbox_pending` / `async_outbox_lag_seconds` 持续上升。
- 用户可见表现：业务侧异步动作（审计落库、缓存失效、通知）延迟或不生效；
  同步接口通常不受影响。

## 影响

- 影响端与范围：订阅对应 topic 的消费方；`topic=iam.audit` 时表现为审计事件未落库
  （`iam_outbox_pending` 同步增长）。
- 数据正确性风险：有。死信事件已经「业务事务提交成功但消费未执行」，
  长时间不处理会造成审计缺口或缓存不一致。

## 排查

1. 打开仪表盘：`async_outbox_delivery_total` 按 `result` 拆分、`async_outbox_pending`、
   `async_outbox_lag_seconds`、`async_dlq_depth`。
2. 查死信明细（只读，禁止直接改状态）：

   ```sql
   SELECT source_id, topic, event_type, attempts, failed_at, last_error
   FROM async_dlq
   WHERE replayed_at IS NULL
   ORDER BY failed_at DESC
   LIMIT 50;
   ```

3. 查原事件状态：

   ```sql
   SELECT id, topic, status, attempts, last_error, created_at, updated_at
   FROM async_outbox
   WHERE id = '<source_id>';
   ```

4. 结合 `last_error` 区分：
   - `no handler registered for topic`：worker 未注册该 topic（发布顺序 / 配置错误），
     或新版本未发布；补齐 handler 后重放。
   - `handler panic`：毒消息，先定位载荷（`payload` 已脱敏）再决定修复或丢弃。
   - 业务错误（下游不可用、唯一键冲突）：修复下游后重放。
5. 查 worker 日志：`service=worker msg="outbox delivery failed"`、
   `msg="async relay started"` 中的 `topics` 是否包含告警主题。

## 重放有效性判定（30 分钟未下降）

`async.dlq.stalled` 命中时，「depth 没有下降」有两种相反原因，处置方向相反，必须先分清：

| 判据 | 指向 | 处置 |
|---|---|---|
| 没有任何重放痕迹（`replayed_at` 全为 NULL） | **没人重放**，不是投递故障 | 按「缓解与恢复」先修因再重放，不必怀疑 handler |
| 已执行 `-replay-dlq`、`replayed_at` 有值，但 depth 30 分钟未降 | **重放无效**：事件重新投递后再次失败 | 比对 `last_error` 是否与上次相同：相同即 poison 循环，先定位载荷；不同则查 handler 阻塞或下游未恢复 |

判定 SQL（只读，MySQL / PostgreSQL 通用）：

```sql
-- 是否发生过重放
SELECT COUNT(*) AS total,
       SUM(CASE WHEN replayed_at IS NOT NULL THEN 1 ELSE 0 END) AS replayed,
       MAX(replayed_at) AS last_replay
FROM async_dlq;

-- 重放后是否又落回死信（failed_at 晚于 replayed_at = 重放无效）
SELECT source_id, topic, attempts, last_error, failed_at
FROM async_dlq
WHERE replayed_at IS NOT NULL AND failed_at > replayed_at
ORDER BY failed_at DESC
LIMIT 20;
```

三条禁止的判断：

- 不要把「depth 没涨」当作已缓解：depth 不下降正是本告警的触发条件；
- 不要在 `last_error` 未变化时反复执行 `-replay-dlq=all`：每次重放都会重新走指数退避，放大下游压力；
- 不要手工把 `async_dlq_depth` 改成 0：指标由表行数计算，只能通过重放或经 Owner 批准的放弃流程下降。

## 缓解与恢复

1. **先修因，再重放**：确认 handler / 下游已恢复；重放会重新进入指数退避流程，
   未修复时只会再次进入死信。
2. 单条重放：

   ```bash
   go run ./cmd/worker -config configs/worker.yaml -replay-dlq=<source_id>
   ```

3. 全量重放（DLQ 量小且同一根因）：

   ```bash
   go run ./cmd/worker -config configs/worker.yaml -replay-dlq=all
   ```

4. 重放语义：
   - 事件状态 `dead|failed → pending`，`attempts` 清零、`next_retry_at` 置空；
   - `async_dlq.replayed_at` 写入时间戳，`async_dlq_depth` 随之下降；
   - 事件仍需由 worker（或 `publish_in_api=true` 的 API）Relay 投递；
     `async.enabled=false` 时重放不会产生投递，事件会保持 pending。
5. 恢复确认：`async_dlq_depth` 回落、`async_outbox_pending` 回落、
   `async_outbox_delivery_total{result="success"}` 增长；抽查下游数据（审计表 / 缓存）。

## 禁止操作

- 禁止手工 `UPDATE async_outbox SET status='succeeded'` 跳过投递（会静默丢失副作用）。
- 禁止直接删除 `async_dlq` 行；需要放弃时由 Owner 记录变更单并保留 `last_error` 证据。
- 禁止把 `async.enabled` 关闭后重启来「清空」积压：事件不会消失，只会停止投递。

## 升级

- P1：15 分钟内无收敛 → L2 Owner → 必要时 L3 SRE
- 升级联系：见 [README.md](./README.md) 值班入口

## 相关链接

- 告警目录：[ALERTS.md](./ALERTS.md)
- 配置说明：[CONFIG_STANDARDS.md](../CONFIG_STANDARDS.md)（`async.*`）
- 指标说明：[OBSERVABILITY_STANDARDS.md](../OBSERVABILITY_STANDARDS.md)（`async_*`）
- 实施计划：[ENT-03](../production-readiness-implementation-plan.md#ent-03)
