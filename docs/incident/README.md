# 事故管理（incident）

> 本目录是「定级 → 处置 → 复盘 → 演练」的唯一事实来源，受 `docs/RUNBOOK_STANDARDS.md`（RB-08/RB-09）约束。  
> 门禁：`make check-oncall`（OC-01～OC-08）。

| 文档 | 回答什么问题 | 何时看 |
|---|---|---|
| [SEVERITY_MATRIX.md](./SEVERITY_MATRIX.md) | 这算 P1 还是 P2？多久必须确认、多久必须通报？ | 告警触发后第一分钟 |
| [RESPONSE.md](./RESPONSE.md) | 谁是指挥、怎么声明、多久更新一次、什么算关闭？ | 声明事故与跨班交接 |
| [POSTMORTEM_TEMPLATE.md](./POSTMORTEM_TEMPLATE.md) | 复盘要写什么，行动项怎么定义才算可验证？ | 关闭后 24 小时内 |
| [ACTION_ITEMS.md](./ACTION_ITEMS.md) | 还有哪些改进没做、谁负责、怎么验证？ | 每周例会 / 交接 |
| [GAME_DAY.md](./GAME_DAY.md) | 演练演什么、怎么注入、判定标准是什么？ | 每季度演练前 |
| [drills/](./drills/) | 历次演练记录与离线自检证据 | 演练后回写 |

## 与 runbook 的分工

- `docs/runbooks/*.md`：**技术处置步骤**（某类故障怎么查、怎么修）；
- `docs/incident/*`：**组织流程**（谁决策、多久通报、如何复盘与跟踪）。

两者通过 `runbooks/ALERTS.md` 的告警 ID 关联：每条 P1/P2 告警既有 runbook，也能按矩阵定级。

## 快速命令

```bash
make check-oncall                                   # OC-01～OC-08 结构门禁
bash scripts/ops/paging-probe.sh --labels severity=P1 --expect-receiver pager   # 呼叫链路解析（占位地址会失败）
bash scripts/ops/game-day-drill.sh --scenario all   # 演练前剧本自检（离线，不触碰集群）
```

## 维护规则

- 分级、流程、模板的修改须同 PR 更新 `check-oncall.sh` 的断言（否则门禁形同虚设）；
- 行动项只增不删：完成后改 `done` 并填验证证据，不删除行；
- 演练记录按 `drills/YYYY-MM-DD-<形态>.md` 命名，离线自检产物放 `drills/artifacts/`。
