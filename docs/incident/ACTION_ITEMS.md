# 事故行动项台账

> 单一事实来源：复盘、演练、审计发现的可执行改进都登记在此。  
> 表格由 `check-oncall.sh`（OC-04/OC-08）校验字段完整性；**逾期不等于自动关闭**。

## 字段与状态

- **类型**：`prevent`（消除根因）/ `detect`（更早发现）/ `mitigate`（缩短影响）/ `process`（流程与入口）
- **状态**：`open` → `in_progress` → `done`；`waived` 仅在有 ADR 且写明替代观测手段时可用
- **验证证据**：必须可执行——测试名、`make` 目标、脚本命令、指标表达式、演练场景 ID；「已加强意识」类不可验证项不予登记
- **逾期**：超过截止日期仍非 `done`/`waived` 的条目，在周会逐条说明并改期（改期须更新截止列，保留原因为备注）

## 台账

| ID | 来源 | 类型 | 行动项 | 负责人 | 截止 | 状态 | 验证证据 |
|---|---|---|---|---|---|---|---|
| AI-2026-11-05-01 | 桌面推演 G1 | process | 把 `deploy/observability/alertmanager/alertmanager.yaml` 的 `pager` / `ticket` receiver 占位地址（`*.example.invalid`）替换为组织真实的 Pager 与工单 Webhook | @platform-sre | 2026-11-19 | open | `bash scripts/ops/paging-probe.sh --labels severity=P1,service=admin-api --expect-receiver pager --require-real-webhook` 通过 |
| AI-2026-11-05-02 | 桌面推演 G1 | process | 值班入口落地：`docs/oncall/ROTATION.md` §2 的 `OWNER:` 条目补齐组织值班日历与升级频道链接 | @platform-sre | 2026-11-19 | open | `docs/oncall/ROTATION.md` §2 每行含可访问链接，且 `grep -c 'OWNER:'` 为 0 |
| AI-2026-11-05-03 | 桌面推演 G2 | detect | 区分「未开始重放」与「重放卡住」：当前仅 `async_dlq_depth > 0`（`repeat_interval=4h`），重放发起后无进展最长 4 小时才会再次提醒；需新增「重放已发起但 N 分钟 depth 未下降」判定 | @async-owner | 2026-11-26 | open | `deploy/observability/prometheus/rules/async.yaml` 新增规则 + `make check-alerts` 通过 + `docs/runbooks/async-dlq.md` 同步 |
| AI-2026-11-05-04 | 桌面推演 G3 | mitigate | Redis 撤销存储故障的 fail-closed 处置步骤缺少「本地降级开关」说明：目前只能重启 Redis 或接受全量 503，需在 runbook 里写清判定阈值与恢复顺序 | @identity-owner | 2026-11-26 | open | `docs/runbooks/identity-auth-failure.md` 新增「fail-closed 处置」小节，且 `make check-runbooks` 通过 |
| AI-2026-11-05-05 | 首次演练结论 | process | 在 staging 执行一次**真实** G1（金丝雀指标门禁失败 → 自动回滚），当前仅完成离线剧本自检 | @sre | 2026-12-05 | open | staging 演练记录 `docs/incident/drills/<date>-g1-staging.md` + `helm history admin-frame-canary` 输出 |
| AI-2026-11-05-06 | 首次演练结论 | process | 每月 1 次 paging 链路测试纳入值班例行（`scripts/ops/paging-probe.sh --post`），结果记入当月值班交接 | @platform-sre | 2026-12-31 | open | 连续 2 个月交接记录含 paging-probe 输出 |

## 已关闭

| ID | 关闭日期 | 验证证据 | 关闭人 |
|---|---|---|---|
| — | — | — | — |
