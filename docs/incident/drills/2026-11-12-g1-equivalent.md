# G1 金丝雀门禁失败 → 自动回滚演练（等价环境，2026-11-12）

> **归档/性质**：等价环境演练记录（非 staging 真实集群）。对应行动项 `AI-2026-11-05-05`。
> **结论**：门禁与回滚剧本的**判定链路**通过；真实 `helm rollback` 未执行（本机无 kind/staging 集群），
> 该缺口在下方「未覆盖」明确记录，不得据此宣称生产回滚已验证。

## 1. 环境与口径

| 项 | 内容 |
|---|---|
| 环境 | 本机（Windows + Git Bash + Helm v4.3.1）；无 kind、无 staging 集群、无 Prometheus |
| 等价口径 | `STG-EQ-01`：以离线剧本预检 + Helm 渲染 + 门禁 fail-closed 行为替代真实集群 |
| 演练日期 | 2026-11-12 |
| 参与角色 | @sre（执行）、@platform-sre（复核） |

## 2. 执行与结果

| 步骤 | 命令 | 结果 |
|---|---|---|
| 1. G1 剧本预检 | `bash scripts/ops/game-day-drill.sh --scenario g1 --json --artifact docs/incident/drills/artifacts/2026-11-12-g1.json` | 通过：`helm rollback` / `release-smoke` / `release-canary-gate` 三个 marker 均 pass；`severity=P1 → pager` 路由 pass |
| 2. 金丝雀渲染 | `helm template admin-frame-canary deploy/helm/admin-frame -f deploy/helm/admin-frame/values-canary.yaml --set fullnameOverride=admin-frame-canary` | 通过：1 个 Deployment、0 个 Job、0 个 Ingress；`replicas: 1`；金丝雀不跑迁移 |
| 3. 门禁 fail-closed | `bash scripts/ci/release-canary-gate.sh`（不设 `PROM_URL`） | 通过（预期失败）：输出 `ERROR: PROM_URL is required (canary gate must not pass without metrics)`，退出码 1——无指标不得放量 |
| 4. 回滚命令与 runbook | 走查 [`release-rollback.md`](../../runbooks/release-rollback.md) | 通过：`helm rollback` 前置判定、观察窗、通知顺序与 G1 剧本一致 |

## 3. 三段耗时

| 项 | 值 | 说明 |
|---|---|---|
| 检测用时 | **N/A** | 等价环境未注入真实故障、未接 Prometheus，无告警产生；不得用脚本耗时冒充，端到端检测须在 staging 窗口测量 |
| 确认用时 | **N/A** | 同上：本次只验证门禁 fail-closed 与剧本判定链路，不涉及人工 ack |
| 缓解用时 | **N/A** | 真实 `helm rollback` 未执行，回滚耗时须在 staging 补测 |

## 4. 未覆盖（必须随 staging 窗口补做）

- 真实 `helm rollback` 未执行，无 `helm history admin-frame-canary` 输出与回滚耗时；
- 金丝雀门禁未接真实 Prometheus，未验证「5xx 超阈值 → 自动触发回滚」的端到端联动；
- 未验证回滚后的 `release-smoke` 实跑结果。

补做方式：有 staging 窗口时按 [`GAME_DAY.md`](../GAME_DAY.md) G1 执行，回填本文件「未覆盖」三项，
并按该文要求归档 `helm history` 输出。

## 5. 证据

- 剧本预检产物：[`artifacts/2026-11-12-g1.json`](./artifacts/2026-11-12-g1.json)
- 门禁脚本：[`release-canary-gate.sh`](../../../scripts/ci/release-canary-gate.sh)
- 回滚手册：[`release-rollback.md`](../../runbooks/release-rollback.md)
- 发布门禁检查：[`check-release.sh`](../../../scripts/ci/check-release.sh)

## 6. 相关链接

- [GAME_DAY.md](../GAME_DAY.md)
- [行动项台账](../ACTION_ITEMS.md) `AI-2026-11-05-05`
- [身份域演练记录](./staging-2026-11-10-identity-drill.md)（同一 `STG-EQ-01` 口径）
