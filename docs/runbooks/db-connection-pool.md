# 数据库连接池耗尽

| 字段 | 内容 |
|------|------|
| 告警 ID | `db.pool.exhaustion` |
| 级别 | P1 |
| 服务 / 端 | 共享 DB；各 API / worker |
| 最后更新 | 2026-10-13 |

## 症状

- 获取连接超时、大量 `context deadline exceeded` 指向 DB。
- `/readyz` 中 DB 检查失败；池占用接近上限。

## 影响

- 全端或单端写读失败；可能触发级联超时与重试放大。
- 有数据正确性风险：长事务未提交时勿强杀无评估。

## 排查

1. 确认告警端与连接池配置（max open / idle）：Helm values 的 `config.db.max_open_conns` /
   `max_idle_conns` / `conn_max_lifetime`（0 表示沿用 `pkg/db` 默认 25 / 5 / 30m），
   环境变量为 `ADMIN_FRAME_DB_MAX_OPEN_CONNS` 等。池上限即 `db_pool_max_open_connections`
   指标的分母，也是 `DBPoolExhaustion` 的判定基准。
2. 查慢查询与锁等待；是否未分页大查询（违反 API/DATA 分页上限）。
3. 是否连接泄漏（未关闭 rows/tx）；近期发版 diff。
4. 数据库侧连接数、CPU、磁盘、复制延迟。

> 池打满时 `/metrics` 里的 DB 相关 Gauge（`async_outbox_*`、`iam_outbox_*`）走 2s 快照缓存，
> 抓取不会被慢查询拖成超时（ENT-06 T6 修复）。若看到 `db_pool_*` 序列突然消失，
> 先看 Prometheus 的 target 状态（`context deadline exceeded`），而不是直接判定「没有池压力」。

## 缓解与恢复

1. 临时：提高池上限须谨慎，优先限流入口（C 端尤甚）与扩容只读副本（若有）。
2. 杀掉确认安全的长查询；回滚有问题的发版。
3. 修复泄漏或慢 SQL 后观察池使用率回落与 `/readyz`。
4. 备份/恢复仅在数据损坏时按 DATA-07 与专责 DBA 流程。

## 升级

- 10～15 分钟无缓解 → DBA / 数据 Owner（L2）+ SRE（L3）。
- 怀疑磁盘满或主库宕机：立即 L3。

## 相关链接

- [ALERTS.md](./ALERTS.md)
- [DATA_STANDARDS.md](../DATA_STANDARDS.md)
- [CONFIG_STANDARDS.md](../CONFIG_STANDARDS.md)
- 故障注入演练记录：[alert-drill-ent06.md](../archive/verify/alert-drill-ent06.md)
