# Runbook：特性开关与灰度（feature-flag）

> 关联：[`../feature-flag-plan.md`](../archive/plans/feature-flag-plan.md)、[`adr/0009-featureflag-inprocess-evaluation.md`](../adr/0009-featureflag-inprocess-evaluation.md)、[`RUNBOOK_STANDARDS.md`](../RUNBOOK_STANDARDS.md)。

## 症状与告警

| 症状 | 数据来源 | 初判 |
|------|----------|------|
| kill switch 后功能仍触发 | `featureflag_eval_total{flag,result="hit"}` 持续增长 | kill switch 生效延迟 ≤5s（缓存 TTL）；>5s 仍未生效为故障 |
| 评估异常放量 `unknown` | `featureflag_eval_total{result="unknown"}` | flag key 拼写错误（幽灵 key）或仓储故障 fail closed |
| 评估直查库热点 | `featureflag_cache_total{state="miss"}` 持续高位 | 缓存穿透：flag 量异常增长或评估路径绕过缓存 |
| 到期任务停摆 | `featureflag_expired_total` 长时间零增长且存在到期 flag | worker 到期任务未运行 |

## 影响与范围

- flag 管理是平台级治理能力；kill switch 误操作影响面 = 该 flag 覆盖的全部渠道 / 租户。
- 评估服务故障 fail closed（未知 key 返回 false）——依赖 flag 的功能在故障时表现为「功能关闭」，不产生错误响应。

## 排查步骤

1. **确认 flag 状态**：`GET /admin/v1/feature-flags/{key}`——检查 `status`、`rules`、`version` 与 `updated_at`。
2. **确认变更已提交**：`audit/events?action=featureflag.*` 查最近变更；并发覆盖（409）后版本是否如预期。
3. **确认生效延迟**：kill switch 变更时间 + 5s 后再评估；规则面为 30s。
4. **检查评估观测**：`featureflag_eval_total{flag,result}` 分布——`killed` 表示开关面拦截，`miss` 表示规则未命中，`unknown` 表示 key 不存在。
5. **检查缓存健康**：`featureflag_cache_total{state}` ——`miss` 后应有 `hit`；持续 `expired` 表示评估频率低于 TTL。

## 缓解与恢复

- **紧急全量止血**（无 flag 的功能）：回滚镜像——这正是「高风险逻辑默认带 flag」的动机。
- **kill switch 误禁用**：`POST /admin/v1/feature-flags/{key}/status` `{"target":"enabled","expected_version":N}` 恢复；写 `featureflag.enable` 审计。
- **到期误禁用**：expired 是治理终态——重建 flag（新 `expires_at`）并恢复业务验证；不允许直接复活。
- **幽灵 key**（评估恒 false）：登记常量并修正调用方；`check-featureflag.sh` 门禁会拦截后续引入。

## 升级路径

- kill switch 生效超时（>5s）或评估服务故障持续 >1 个探测周期：升级平台值班；故障面为全部依赖 flag 的功能。
- 到期任务连续两轮未运行：升级平台值班（遗留 flag 台账失控风险）。

## 相关链接

- 规划：[`../feature-flag-plan.md`](../archive/plans/feature-flag-plan.md)
- 决策：[`../adr/0009-featureflag-inprocess-evaluation.md`](../adr/0009-featureflag-inprocess-evaluation.md)
- 门禁：`make check-featureflag`（key 命名 / 审计对账 / 幽灵 key / 到期治理）
