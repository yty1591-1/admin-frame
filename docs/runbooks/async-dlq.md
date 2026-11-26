# 异步死信（async_dlq）积压与重放

| 字段 | 内容 |
|------|------|
| 告警 ID | `async.dlq.depth`、`async.dlq.stalled`、`async.dlq.replay_ineffective`、`async.outbox.dead`、`async.outbox.backlog`、`iam.outbox.dead`、`iam.outbox.poison`、`tenant.audit.emit_failed`、`notify.audit.emit_failed` |
| 级别 | P2（单主题 DLQ 增长且业务可延迟）/ P1（核心审计 / 权限事件连续进入 DLQ、审计写入失败或投递终态死亡） |
| 服务 / 端 | worker（投递方）、admin-api / biz-api（`async.outbox.publish_in_api=true` 时） |
| 最后更新 | 2026-11-08（补「审计写入失败与投递终态死亡」：区分写入失败 / dead / poison、按 last_error 分流、合规口径；并登记 iam / tenant / notify 三条审计类告警） |

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

先看命中的是哪个告警，两者分工不同：

| 告警 | 判据 | 指向 |
|---|---|---|
| `async.dlq.replay_ineffective` | `async_dlq_depth > 0` 且 `async_dlq_replayed_30m > 0` | **重放无效**：重放确实发起过，事件又落回死信 |
| `async.dlq.stalled` | `min_over_time(async_dlq_depth[30m]) > 0` | **滞留**：30 分钟未下降，但不区分「没人重放」与「重放无效」 |

`async_dlq_replayed_30m` 是 DB 派生量（`async_dlq.replayed_at` 落在 30 分钟窗口内的行数），不是进程内计数器——重放是 `cmd/worker -replay-dlq` 的一次性 CLI，进程退出后没有指标可抓。窗口常量与告警语义绑定在同一处（`pkg/outbox/store.go` 的 `ReplayRecentWindow`）。

只有 `stalled` 命中（`replayed_30m` 为 0）时，用下表区分原因：

| 判据 | 指向 | 处置 |
|---|---|---|
| 没有任何重放痕迹（`replayed_at` 全为 NULL） | **没人重放**，不是投递故障 | 按「缓解与恢复」先修因再重放，不必怀疑 handler |
| 已执行 `-replay-dlq`、`replayed_at` 有值，但 depth 30 分钟未降 | 与 `replay_ineffective` 同义（窗口可能已过 30 分钟） | 比对 `last_error` 是否与上次相同：相同即 poison 循环，先定位载荷；不同则查 handler 阻塞或下游未恢复 |

二次确认 SQL（只读，MySQL / PostgreSQL 通用）：

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
   go run ./cmd/worker -replay-dlq=<source_id>
   ```

3. 全量重放（DLQ 量小且同一根因）：

   ```bash
   go run ./cmd/worker -replay-dlq=all
   ```

4. 重放语义：
   - 事件状态 `dead|failed → pending`，`attempts` 清零、`next_retry_at` 置空；
   - `async_dlq.replayed_at` 写入时间戳，`async_dlq_depth` 随之下降；
   - 事件仍需由 worker（或 `publish_in_api=true` 的 API）Relay 投递；
     `async.enabled=false` 时重放不会产生投递，事件会保持 pending。
5. 恢复确认：`async_dlq_depth` 回落、`async_outbox_pending` 回落、
   `async_outbox_delivery_total{result="success"}` 增长；抽查下游数据（审计表 / 缓存）。

## 审计写入失败与投递终态死亡（dead / poison）

审计类事件（`iam_outbox_delivery_total{result=dead|poison}`、`tenant_audit_emit_failed_total`、
`notify_audit_emit_failed_total`）的失败有两种，处置完全不同，先分清：

| 失败类型 | 现象 | 事实是什么 | 处置方向 |
|---|---|---|---|
| **写入失败**（`*_audit_emit_failed_total`） | 计数器增长 | 审计行**根本没进表**，且业务事务随之回滚 | 修审计写入路径（表 / 权限 / 磁盘 / DB），用户重做业务动作才会重新产生事件 |
| **投递终态失败**（`dead`） | 死信表有行，`attempts` 已耗尽 | 审计行已入表但投不出去，**审计事实在源库存在、下游缺失** | 先按 `last_error` 分类，再单条重放 |
| **毒消息**（`poison`） | 同一事件反复失败后被隔离 | 载荷或下游 schema 不兼容 | 禁止直接重放，必须先修因 |

判定命令（只读）：

```sql
-- 1) 终态失败按原因聚类，决定是下游故障还是载荷问题
SELECT last_error, COUNT(*) AS n
FROM async_dlq
WHERE replayed_at IS NULL
GROUP BY last_error
ORDER BY n DESC
LIMIT 10;

-- 2) 取单条样本核对载荷与重试次数
SELECT source_event_id, attempts, last_error, occurred_at
FROM async_dlq
WHERE replayed_at IS NULL
ORDER BY occurred_at
LIMIT 5;
```

按 `last_error` 分流的处置：

| last_error 特征 | 指向 | 动作 |
|---|---|---|
| 连接超时 / 拒绝 / DNS | 下游不可用（审计出口、MQ） | 先恢复下游，确认可用后 `-replay-dlq <source_event_id>` 单条重放验证，再批量 |
| 反序列化 / schema / 字段缺失 | 载荷与下游契约不一致 | 先改代码或迁移，**不要重放**：重放只会再进 poison |
| 权限 / 认证失败 | 凭据过期或策略变更 | 修凭据后单条重放验证 |
| 唯一键冲突 | 该事件已投递过（幂等键命中） | 属正常去重，核对下游已有该事件后按「已投递」结案，不要反复重放 |

合规口径（决定复盘怎么写）：

- 审计事件缺失**不可回补**。`AUD-06` 要求审计存储追加写，禁止用业务表或补录行冒充；确需补记录须按 `DATA-04` 双人执行并留变更单。
- 复盘必须写明影响窗口（起止时间）、丢失或延迟的事件类别与数量上界，以及当时是否触发 `*_audit_emit_failed_total`。
- `tenant` 域写入失败会让业务事务回滚——用户侧是「操作失败」，不是静默丢审计；因此该告警同时是**可用性**信号，不要只按合规事件处理。

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
