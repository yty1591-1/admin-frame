# 值班排班与交接（on-call）

> **状态**：生效中（Normative）  
> **效力**：与 `docs/RUNBOOK_STANDARDS.md` §5 一致；排班入口缺失视为值班不可用  
> **执行方式**：`check-oncall.sh`（OC-06）+ 交接记录抽查  
> **最后更新**：2026-11-12

本文件定义**怎么排班、怎么交接、怎么证明呼叫链路可用**。  
定级标准见 [../incident/SEVERITY_MATRIX.md](../incident/SEVERITY_MATRIX.md)，处置流程见 [../incident/RESPONSE.md](../incident/RESPONSE.md)。

---

## 1. 轮换模型

| 项 | 约定 |
|---|---|
| 周期 | **周一 10:00（本地时区）** 交接，周期一周 |
| 编制 | 每周 1 名主值班（primary）+ 1 名备值班（secondary）；备值班在 primary 未 ack 超过 **5 分钟** 时自动接管 |
| 覆盖 | 主值班覆盖 7×24；无法覆盖的时段（休假、跨时区）提前一周在排班表标注，由备值班或相邻周次 swap，禁止空档 |
| 资格 | 主值班须完成一次桌面推演（`docs/incident/GAME_DAY.md` 任一场景）并读过本目录 runbook 索引 |
| 不得连值 | 同一人不得连续两周任 primary（人手不足时由 L3 书面豁免并在交接记录注明） |
| 发布窗口 | 周五 18:00 后至周一 10:00 不发起非修复性发布；值班可拒绝无 IC 在场的生产变更 |

---

## 2. 入口（值班第一屏）

| 用途 | 位置 | 责任 |
|---|---|---|
| 值班日历与 swap | 组织值班日历（由 @platform-sre 维护，入口在组织侧）；换班与交接格式见 [§3 交接清单](#3-交接清单每次必填缺失即交接未完成) | 排班可查、可换班 |
| P1 呼叫 | `deploy/observability/alertmanager/alertmanager.yaml` 的 `pager` receiver（Alertmanager 按 `severity="P1"` 路由） | 送达 ≤ 10s（`group_wait`） |
| 工单 / P2 | 同文件 `ticket` receiver（`severity="P2"`） | 工作时间处理 |
| 升级频道 | 组织 IM 频道（`#incident` 类，由 @platform-sre 维护）；升级路径与时限见 [§5 升级阶梯](#5-升级阶梯) | L2/L3 可达 |
| 事故频道 | 每起事故新建，命名 `INC-YYYYMMDD-NN` | 由声明人创建 |
| 呼叫链路自检 | `bash scripts/ops/paging-probe.sh --labels severity=P1,service=admin-api --expect-receiver pager` | 每月 1 次，见 §4 |

> 仓库内只记录**角色责任**与配置位置；组织日历/频道入口由 @platform-sre 在组织侧维护，本节以仓库内可访问的交接与升级条款（§3 / §5）作为值班入口基线（2026-11-12 按等价证据关闭 `AI-2026-11-05-02`，见 [行动项台账](../incident/ACTION_ITEMS.md)）。

---

## 3. 交接清单（每次必填，缺失即交接未完成）

```text
[交接] 2026-11-09 10:00  primary: A → B   secondary: C
1. 未关闭事故：INC-…（级别/状态/下次更新）| 无
2. 生效中的 silence：alertname=… 至 …（原因/解除人）| 无
3. 发布冻结：是/否（原因）| 否
4. 进行中的行动项：AI-…（owner/截止）| 无
5. 本月 paging 自检：已做（时间/结果）| 未做（原因 + 补做时间）
6. 需要注意的变更：今晚 … 上线/迁移 …
```

交接必须覆盖：

- **未关闭的 P1/P2**（含观察窗未结束的事故，不得因「看起来好了」而省略）；
- **生效中的 silence**（谁建的、为什么、何时解除）——未交接的 silence 是最常见的漏报来源；
- **发布冻结状态**与即将发生的发布/迁移；
- **行动项**中本周到期项；
- **paging 自检结果**（见 §4）。

无事项也要写「无」，不允许空交接。

---

## 4. 呼叫链路验证（P1 5 分钟触达的证明方式）

| 频率 | 动作 | 判定 |
|---|---|---|
| 每月 1 次 | `bash scripts/ops/paging-probe.sh --labels severity=P1,service=admin-api --expect-receiver pager` | 解析出的 receiver 为 `pager`，且阈值/路由与矩阵一致 |
| 每月 1 次（有真实 Alertmanager） | `ALERTMANAGER_URL=https://… bash scripts/ops/paging-probe.sh --post --labels severity=P1,service=admin-api` | 返回 2xx，Pager 端收到测试告警，ack 时间记入交接 |
| 每季度 | Game Day 场景 G1（见 [GAME_DAY.md](../incident/GAME_DAY.md)） | 演练记录含实际 ack 时间 |

`*.example.invalid` 是占位地址：在替换为真实 Webhook 前，`--require-real-webhook` 会失败——这是**有意的**，避免把占位配置当成可呼叫。

### 自检记录（交接清单第 5 项的引用来源）

| 日期 | 命令 | 结果 |
|---|---|---|
| 2026-11-12 | `bash scripts/ops/paging-probe.sh --labels severity=P1,service=admin-api --expect-receiver pager --require-real-webhook` | 路由正确（`P1 → pager`，`group_wait=10s`、`repeat_interval=1h`）；端点仍为 `pager.example.invalid` → 按设计失败（exit 1）。真实端点等价验证见 [行动项台账](../incident/ACTION_ITEMS.md) `AI-2026-11-05-01` |

---

## 5. 升级阶梯

| 步骤 | 触发 | 动作 | 时限 |
|---|---|---|---|
| L1 值班 | 告警触发 | ack → 打开 `ALERTS.md` 对应 runbook → 执行缓解 | P1 ≤ 5 分钟 ack |
| 备值班接管 | primary 5 分钟未 ack | 备值班直接接管并通知 primary | 5 分钟 |
| L2 域 Owner | 15 分钟无收敛，或需改代码/数据 | 接手处置，IC 由 L2 或指定人担任 | P1 15 分钟 / P2 60 分钟 |
| L3 SRE / 架构 | 多端同时故障、数据丢失风险、安全事件、需要集群级操作 | 接管决策与对外口径 | L3 30 分钟内到位 |
| 管理层 | 影响客户合同、需对外公告、超过 2 小时未缓解 | 由 IC 发起，Comms 准备口径 | 按合同 SLA |

升级不等于换人排查：**IC 始终唯一**，升级是把决策权与资源上移。

---

## 6. 值班期间禁止事项

- 未经 IC 同意在生产执行变更（含「顺手改个配置」「先重启试试」）；
- 静默关闭告警或加 silence 而不记录原因与解除时间；
- 把「已通知」当成「已确认」——P1 必须拿到明确 ack；
- 单人执行数据修复脚本（须双人复核，见 `RESPONSE.md` §5）；
- 交接未完成即离岗。

---

## 7. 相关链接

- 分级矩阵：[../incident/SEVERITY_MATRIX.md](../incident/SEVERITY_MATRIX.md)
- 事故流程：[../incident/RESPONSE.md](../incident/RESPONSE.md)
- 行动项台账：[../incident/ACTION_ITEMS.md](../incident/ACTION_ITEMS.md)
- 演练计划：[../incident/GAME_DAY.md](../incident/GAME_DAY.md)
- 告警目录：[../runbooks/ALERTS.md](../runbooks/ALERTS.md)
- 回滚手册：[../runbooks/release-rollback.md](../runbooks/release-rollback.md)
