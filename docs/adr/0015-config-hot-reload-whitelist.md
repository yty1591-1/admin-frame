# ADR-0015：配置热更新采用「白名单 + 不可变视图原子替换」

- 状态：Proposed（草案，2026-11-13，待架构 Owner 评审）
- 日期：2026-11-13
- 关联：[`../config-center-plan.md`](../config-center-plan.md)（§3.4 读取协议、§6.8 配置优先级与热更白名单、AC-CC-02）、[ADR-0013](0013-config-center-in-process-snapshot.md)（进程内快照与 MySQL 事实源）、[ADR-0009](0009-featureflag-inprocess-evaluation.md)（布尔开关边界）、[ADR-0014](0014-secret-management-envelope-encryption.md)（密钥解析）、[`../ARCHITECTURE.md`](../ARCHITECTURE.md) §5.6、[`../CONFIG_STANDARDS.md`](../CONFIG_STANDARDS.md)（CFG-07）

## 背景

M1 已完成发布 / 回滚 / 指针 CAS / Ed25519 签名快照 / 进程内 SDK / 双读采样，但**取值从未热更**：

- `cmd/internal/configcenterruntime/refresh.go` 的 `refreshLogger` 与 `clientRefreshObserver` 只写日志和指标；
- 各进程注释明确写着「热更值回写尚未启用」；
- 结果：改一个运行参数仍然要发布 + 重启，AC-CC-02「99% 客户端 ≤10s 看到新 Release」不成立；配置中心实际退化为「远程配置文件」。

`ARCHITECTURE.md` §5.6 硬性要求「热更新前必须定义白名单并补 ADR」，本 ADR 即该前置件；CFG-07 要求每个键声明 owner / 类型 / 默认值 / 校验 / `reloadable`，`internal/configcenter/spec` 已按此声明 104 个键，其中 15 个标了 `reloadable=true`。

**现状调查（决定本 ADR 的形态）**：这 15 个键的**消费方全部在装配期捕获**——

- `internal/platform/bootstrap.go` 把 `cfg.RateLimit` 整体拷进请求管线，之后不再读配置；
- logger 级别在构造时固定，没有 `slog.LevelVar`；
- outbox relay 与 consumer 用启动参数构造，`batch_size` / `concurrency` / `lease` 都是构造期常量。

所以「热更」不是加一个 `atomic.Pointer` 就完事：**必须逐个把读点从事务性的「构造期快照」改成「每次使用读当前视图」**。哪些键值得付这个改造成本，正是白名单要回答的问题。

## 决策

1. **键二分：`startup`（默认）与 `hot`（白名单）**。只有 spec 中 `reloadable=true` **且**列入本 ADR 附录 A 的键才热更；其余键即使中心有值也只走「发布 + 重启」，运行期忽略并打点。
2. **白名单准入四条**，任一不满足即不可 `hot`：
   - 读点在请求 / 轮次内可重入（不是一次装配、长期持有）；
   - 不涉及监听地址、连接串、证书、驱动、线程/协程池规模等结构参数；
   - 变更无跨组件顺序依赖，单独生效不会造成半更新状态；
   - 单次读取成本可忽略（内存读，不触发 I/O）。
3. **视图模型**：`pkg/hotconfig.View` 是不可变结构（`map[string]string` + 类型化 getter），构建完成后整体 `atomic.Pointer` 替换；读方 `view.Int("rate_limit.burst", 0)` 每次读当前指针，不做逐键锁、不做懒加载、不缓存到局部变量。
4. **同值不重建**：以 Release 条目的 `value_hash` 判断，若 `hot` 键取值全部未变则不构建新视图、不推进视图版本（避免 5s 轮询制造无谓唤醒与抖动）。
5. **失败保留旧值**：验签失败、类型 / 枚举 / 范围校验失败、视图构建 panic 一律保留上一份视图并告警，**绝不回落到代码默认值**——默认值只属于启动期语义。
6. **生效窗口**：发布后 ≤ 1 个刷新周期（默认 5s）+ 视图构建时间；不重启进程、不重建连接池、不重置计数器。
7. **首批白名单 13 键**（附录 A），并**明确排除** `async.outbox.concurrency` 与 `async.consumer.concurrency`：动态扩缩 worker 池涉及在途任务与租约，风险与 M1 收益不匹配，留待 P1 单独评估；本 ADR 通过后把这两个键的 `reloadable` 收紧为 `false`。
8. **观测**：`config_hot_view_version{namespace}`、`config_hot_reload_total{result="applied|rejected|unchanged"}`、`config_hot_key_applied_total{key}`、`config_hot_ignored_total{key}`（`startup` 键收到热更事件时计数）。日志只记 key 与版本，不记取值。
9. **边界不变**：布尔开关 / kill switch 仍走 `internal/featureflag`（ADR-0009）；灰度与租户覆盖仍在 P1；密钥解析仍只经 `cmd/config-api`（ADR-0014），`hot` 视图**不含密钥**。
10. **回滚**：回滚发布 = 指针回退 + 下一次刷新构建旧视图，等价一次普通热更；不保留历史视图，也不支持「按进程回滚到某个旧视图」。

## 附录 A：首批热更白名单（13 键）

| # | key | 类型 | 默认 | 当前读点 | 改造要点 |
|---|-----|------|------|----------|----------|
| 1 | `log.level` | enum(debug/info/warn/error) | `info` | 构造期固定 | 改为 `slog.LevelVar`，热更直接 `Set` |
| 2 | `worker.poll_interval` | duration | `5s` | worker 主循环 | 每轮读视图，下一轮生效 |
| 3 | `idempotency.ttl` | duration | `24h` | 幂等中间件构造期 | 写入幂等记录时读当前 TTL |
| 4 | `rate_limit.rps` | string(数字) | `0` | 管线构造期 | 每次限流决策读当前值 |
| 5 | `rate_limit.burst` | int | `0` | 管线构造期 | 同上；变更时不清空已有令牌桶 |
| 6 | `rate_limit.idle_ttl` | duration | `10m` | 桶回收任务 | 每轮回收读当前值 |
| 7 | `rate_limit.degrade_on_failure` | bool | `false` | 限流失败判定 | 判定时读当前值 |
| 8 | `async.outbox.poll_interval` | duration | `1s` | relay 构造期 | 每轮休眠读当前值 |
| 9 | `async.outbox.batch_size` | int | `100` | relay 构造期 | 每轮认领读当前值（上限 500） |
| 10 | `async.outbox.lease` | duration | `30s` | relay 构造期 | 认领时读当前值；**加租约随读取值走**，避免新旧租约混用 |
| 11 | `async.outbox.max_attempts` | int | `10` | relay 构造期 | 判定重试上限时读当前值 |
| 12 | `async.consumer.visibility_timeout` | duration | `60s` | consumer 构造期 | 认领时读当前值 |
| 13 | `async.consumer.retry_backoff` | enum(exponential/fixed) | `exponential` | consumer 构造期 | 计算退避时读当前值 |

**排除项**（保持 `startup`）：

- `async.outbox.concurrency` / `async.consumer.concurrency`：动态调整池规模需要优雅缩容与在途任务迁移，M1 不做。
- 所有 `addr` / `db.*` / `redis.*` / `storage.driver` / `auth.mode` / `tracing.*` / `iam.mode`：影响进程装配，热更即等于隐式重启。
- 密钥类：不在 `hot` 视图内（ADR-0014）。

## 备选方案

| 方案 | 不选理由 |
|------|----------|
| 全键热更（不做白名单） | 结构参数热更等于隐式重启，且无法保证跨组件顺序；与 ARCH §5.6 冲突 |
| 逐键 `atomic.Value` + 回调注册 | 每键一个回调，注册顺序与失败处理难以审计；视图整体替换更易验证 |
| 每次读配置都回源中心 | 把 5s 轮询变成请求级 DB 查询，违背 ADR-0013 读路径设计 |
| 用 featureflag 承载热更参数 | 布尔开关与结构化值是两套语义（ADR-0009），混用会让灰度与热更互相污染 |
| Sidecar / 配置注入重启 | 引入新工作负载，且仍是重启语义，不满足 AC-CC-02 |

## 后果

**正面**

- AC-CC-02 可达成：发布后一个轮询周期内生效，无需重启，滚动发布窗口缩短。
- 白名单使「哪些参数能热更」成为可审计的契约，而不是散落在各处的隐式行为。
- 视图整体替换避免了「一半键新值、一半旧值」的撕裂。

**负面 / 风险**

- 需要逐个改造消费点，且每个点都要 Review「读当前视图」是否引入并发或语义问题（例如限流桶在 `burst` 变更时的处理）。
- 视图替换失败若处理不当会让进程停在旧值却以为已生效——用 `config_hot_reload_total{result="rejected"}` 与告警兜底。
- `async.outbox.lease` 属高风险键：调小会让在途消息被他人接管，需在 runbook 标注变更窗口。

## 后续行动

1. 架构 Owner 评审本 ADR；通过后状态改 `Accepted` 并回写 §10.5 范围变更记录。
2. 通过后实施顺序：`pkg/hotconfig` 视图与指标 → 四进程接线（复用 `configclient.RefreshEvent`）→ 逐键改造读点 → AC-CC-02 用例（改 hot 键 ≤5s 生效、改 startup 键不生效且打点、回滚 ≤30s）。
3. 同步收紧 `async.*.concurrency` 的 `reloadable`，并更新 `CONFIG_STANDARDS.md` / `ARCHITECTURE.md` §5.6 的白名单指向本 ADR。
4. `docs/runbooks/config-center.md` 增「热更生效验证」：改 `rate_limit.burst` → 观察 `config_hot_view_version` 递增与 `config_hot_key_applied_total` 计数。

## 未决问题

| ID | 问题 | 建议 |
|----|------|------|
| H1 | `log.level` 用全局 `LevelVar` 会影响所有 logger，是否可接受？ | 接受；这是运维最常用的热更键，且级别变更本就应该全局一致 |
| H2 | `async.outbox.lease` 进首批是否过于激进？ | 建议保留但加变更窗口约束；若评审认为风险高，可移到 P1，首批降为 12 键 |
| H3 | 视图构建 panic 的兜底策略？ | `recover` 后保留旧视图并计 `rejected`，同时触发告警；不做进程退出 |
| H4 | 是否需要在 M1 提供「热更 explain」？ | 不提供；P1 与灰度 explain 一起做 |
