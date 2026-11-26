# Game Day 演练计划

> **频率**：每季度 1 次；发布路径、撤销链路、异步管道任一发生结构性变更后加练。  
> **形态**：`tabletop`（桌面推演，离线核对剧本与命令，不触碰集群）/ `staging`（在 staging 真实执行）/ `prod-safe`（生产低风险窗口，仅限只读与受控注入）。  
> **首次记录**：[drills/2026-11-05-tabletop.md](./drills/2026-11-05-tabletop.md)

演练的目的不是「跑一遍看看」，而是**用真实级别（P1/P2）检验检测、确认、缓解三个耗时是否达得到矩阵要求**，并把发现的缺口变成行动项。

---

## 1. 组织与安全前提

| 项 | 约定 |
|---|---|
| 参与角色 | 演练 IC、操作人、记录人（对应 `RESPONSE.md` §4 的 IC / OL / Scribe） |
| 前提 | 演练前 1 个工作日发出通知；staging 演练需确认目标集群（`kubectl config current-context`）；生产注入需 IC 与 SRE 双签 |
| 隔离 | 演练告警统一带 `drill=true` 标签；Alertmanager 对 `drill=true` 走 `ticket` 并加「演练」前缀，避免占用 P1 呼叫 |
| 中止条件 | 出现真实用户影响、跨环境误操作、数据不可恢复风险 → 立即中止并记录 |
| 禁止 | 在生产注入数据损坏、删除类故障；用演练替代真实事故复盘 |
| 产出 | 演练记录（含实际耗时）、缺口行动项（写入台账）、需要更新的 runbook / 告警 / 矩阵 |

---

## 2. 场景

### G1 金丝雀指标门禁失败 → 自动回滚

| 项 | 内容 |
|---|---|
| 目标 | 验证「金丝雀不达标不放量、失败自动回滚」，并测出检测→确认→缓解耗时 |
| 关联告警 | `api.5xx.rate_high`（P1）、`release.health_failed`（P1） |
| 注入（staging） | 部署金丝雀版本后，让金丝雀实例返回 5xx（注入错误开关或错误配置），或将 `release-canary-gate.sh` 的 5xx 阈值临时收紧到必然触发 |
| 预期观测 | `release-canary-gate.sh` 报错并退出非零 → `rollback` job 卸载 `admin-frame-canary` → `helm rollback admin-frame 0` → 生产 5xx 无抬升 |
| 判定通过 | ① 门禁退出非零且日志指明阈值；② 金丝雀 release 已卸载；③ 生产指标无变化；④ 回滚后冒烟通过 |
| 关键命令 | `bash scripts/ci/release-canary-gate.sh`；`helm history admin-frame -n admin-frame --max 5`；`helm rollback admin-frame 0 -n admin-frame --wait`；`bash scripts/ci/release-smoke.sh <base-url> <version>` |
| Runbook | [../runbooks/release-rollback.md](../runbooks/release-rollback.md) |
| 常见缺口 | 门禁零流量假绿（已由流量下限拦截）、回滚后未跑冒烟、canary release 残留 |

### G2 异步死信积压 → 定位 → 重放

| 项 | 内容 |
|---|---|
| 目标 | 验证死信可观测、可定位、可重放且不产生重复副作用 |
| 关联告警 | `async.dlq.depth`（P2）、`async.outbox.backlog`（P1） |
| 注入（staging） | 停止 worker 消费者使事件堆积；或投递一条必然失败的事件（如回调目标不可达）制造死信 |
| 预期观测 | `async_dlq_depth > 0` 触发告警；`async.outbox.backlog` 在积压超阈值时触发；重放后 `async_dlq_depth` 归零 |
| 判定通过 | ① 告警按预期级别触发；② 能按 runbook 定位到具体事件 ID；③ 重放后积压归零且下游无重复（幂等键生效）；④ 审计/通知无丢失 |
| 关键命令 | `SELECT id, … FROM async_dlq WHERE replayed_at IS NULL`；`go run ./cmd/worker -replay-dlq=<source_id>`（`all` 为重放全部）；观测 `async_dlq_depth` 回落 |
| Runbook | [../runbooks/async-dlq.md](../runbooks/async-dlq.md) |
| 常见缺口 | 重放卡住无独立告警（见行动项 AI-2026-11-05-03）、重复投递导致下游重复副作用 |

### G3 撤销存储故障 → fail-closed 判定与恢复

| 项 | 内容 |
|---|---|
| 目标 | 验证「撤销状态不可读时宁可拒绝」的行为符合预期，且恢复顺序正确、不长时间全量 503 |
| 关联告警 | `identity.revocation.store_error`（P1）、`identity.revocation.cache_write_error`（P2） |
| 注入（staging） | 断开 Redis 或注入读错误（`identity_revocation_store_errors_total` 增长）；保持 MySQL 可用以验证回源路径 |
| 预期观测 | 错误计数上升 → 按配置走 fail-closed（受保护端点 503）或回源 DB；恢复 Redis 后错误计数停止增长、503 归零 |
| 判定通过 | ① **不出现「撤销失效仍放行」**（安全底线）；② 503 比例与持续时间在可接受窗口；③ 恢复后无需重启进程即自愈；④ 审计有记录 |
| 关键命令 | `kubectl -n admin-frame exec … -- redis-cli -h <host> ping`；查询 `identity_revocation_store_errors_total`；按 runbook 判定阈值 |
| Runbook | [../runbooks/identity-auth-failure.md](../runbooks/identity-auth-failure.md) |
| 常见缺口 | 缺「本地降级开关」判定阈值说明（见行动项 AI-2026-11-05-04）、恢复后未验证缓存重建 |

### G4（可选）PITR 恢复到指定时点

仅在备份/恢复路径变更的季度执行，判定与命令见 [../runbooks/backup-restore.md](../runbooks/backup-restore.md)；要求「误删未被重放、备份后增量全部找回」。

---

## 3. 单场景记录表（复制到 `drills/<date>-<scenario>.md`）

| 字段 | 内容 |
|---|---|
| 场景 ID | G1 / G2 / G3 / G4 |
| 形态 | tabletop / staging / prod-safe |
| 日期与环境 | YYYY-MM-DD / 集群上下文 |
| 参与人 | IC / OL / Scribe |
| 检测用时 | 注入 → 告警触发：N 分 M 秒（目标：P1 ≤ 5 分钟） |
| 确认用时 | 告警 → 明确 ack：N 分 M 秒（目标：P1 ≤ 5 分钟） |
| 缓解用时 | 注入 → 影响消失：N 分 M 秒（对照矩阵缓解目标） |
| 结果 | 通过 / 部分通过 / 未通过（逐条对判定项） |
| 发现的缺口 | 现象 → 根因 → 行动项 ID |
| 需更新 | runbook / 告警 / 矩阵 / 脚本（附 PR 或行动项） |

---

## 4. 演练后动作

1. 缺口逐条写入 [ACTION_ITEMS.md](./ACTION_ITEMS.md)，带 owner 与截止；
2. 剧本与实际不符处，**改 runbook 而不是改记忆**；
3. 若矩阵时限在演练中达不到，调整矩阵或补自动化（不许两边都不动）；
4. 在下次交接中说明演练结论与未关闭行动项。

---

## 5. 相关链接

- 分级矩阵：[SEVERITY_MATRIX.md](./SEVERITY_MATRIX.md)
- 事故流程：[RESPONSE.md](./RESPONSE.md)
- 值班与交接：[../oncall/ROTATION.md](../oncall/ROTATION.md)
- 行动项台账：[ACTION_ITEMS.md](./ACTION_ITEMS.md)
- 剧本离线自检：`bash scripts/ops/game-day-drill.sh --scenario all`
