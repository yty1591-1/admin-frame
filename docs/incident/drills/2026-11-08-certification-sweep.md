# 访问复核超期扫描全流程演练记录 — 2026-11-08（S7-5）

| 字段 | 内容 |
|---|---|
| 场景 | S7-5：访问复核（P1-7）「创建 → 激活 → 超期扫描 → 自动回收 → 审计投递 → 证据归档」全流程 |
| 形态 | **集成演练（真实 MySQL / Redis / Outbox）**，非 staging 集群；CronJob 真实调度与 Alertmanager 路由不在本次范围 |
| 日期 | 2026-11-08 |
| 环境 | 本机 `docker-compose.integration.yml`：MySQL 8.0.46（`admin_frame_test` 库，全部迁移已应用）、Redis 7（快照共享 L2）、通用 Outbox 真表 `async_outbox`；Go 1.26.10；被测代码含 2026-11-08 交付的 `SweepOverdue` / `worker -certification-sweep` |
| 执行方式 | `tests/integration/iam/mysql_certification_sweep_test.go` 两个用例按演练剧本逐步断言；审计「投递」用 ClaimBatch → `AuditEventHandler` → MarkSucceeded 模拟常驻 Relay 的单轮行为 |
| 证据 | [artifacts/2026-11-08-certification/](./artifacts/2026-11-08-certification/test-output.txt)（测试输出）、[evidence.csv](./artifacts/2026-11-08-certification/certification-sweep-evidence.csv) / [evidence.json](./artifacts/2026-11-08-certification/certification-sweep-evidence.json)（证据导出归档） |
| 结论 | **演练通过**。auto_revoke 关 / 开两条路径、指纹漂移防护、审计 fail-closed 契约、快照失效（含共享 L2）全部符合 rbac-iam.md §10.3 设计；2 个测试基建缺口已当场修复 |

## 1. 实测数据

| 用例 | 场景 | 关键断言 | 结果 |
|---|---|---|---|
| 演练 A | `auto_revoke=false` 全流程 | 超期任务 1 条 → `expired`；绑定未被回收；创建/激活/自动过期 ≥3 条审计事件全部经 `async_outbox` 投递成功（pending=0，DLQ=0）；无 `unbind` 事件；整仓 `SweepOverdue` 二轮幂等（tasks=0，零新增审计）；证据 CSV/JSON 可导出且含 `expired` 行 | PASS |
| 演练 B | `auto_revoke=true` 双场景 | 指纹漂移（解绑重绑后扫描）→ 任务 `skipped`、重绑后的绑定**未被回收**；指纹一致 → 任务 `revoked`、绑定在 MySQL 中软撤销；主体 9003 快照（L1+Redis L2）被删除；审计含 `rbac.binding_change(unbind, actor=system:certification)`、`rbac.certification(auto_revoke)`、`rbac.certification(auto_skip)`；扫描汇总 scanned=2 / overdue=2 / expired=2 / failed=0 / tasks=2 | PASS |

演练 B 日志摘要（`l2_redis=true` 表示快照断言覆盖共享 Redis L2）：

```text
cert drill summary: l2_redis=true scanned=2 overdue=2 expired=2 tasks=2 audit_events=11
```

## 2. 演练发现与处置

| # | 发现 | 定性 | 处置 |
|---|---|---|---|
| 1 | `auto_expire` 任务的 `decided_by` / `reason` 列为空：`domain.CertificationTask.Expire` 只推进 `status` 与 `decided_at`，操作人与原因只存在于审计事件 | 口径事实（非缺陷） | 已在本记录与测试注释写明：证据消费方（SIEM / 等保）对「谁做的」必须以 `rbac.certification` 审计事件为准，不得读任务列 |
| 2 | 集成测试共用 `reset()` 不清复核表，导致其他用例清 `iam_role_binding` 时被 `iam_certification_task` 外键卡死 | 测试基建缺口 | 已修：`mysql_repository_test.go` 的 `reset()` 增加复核两表且置于最前（外键 RESTRICT 顺序） |
| 3 | `Bind` / `Unbind` 都会写撤权墓碑（`InvalidateGuard`，默认 3s），墓碑存活期内快照回填被静默跳过 | 设计如此（防脏回填） | 演练用例把 `InvalidateGuard` 压到 50ms 并重试预置快照；生产不受影响 |
| 4 | 活动默认复核范围是「端内全部角色」，演练初期两场景共用一个角色导致清单互相污染 | 操作口径 | 演练改为按 `RoleIDs` 限定范围；运维发起多场景复核时应逐活动指定角色范围 |

## 3. 本次不声称完成的部分（留 staging，复用 AI-05 窗口）

- CronJob `certification-sweep` 的真实调度、`concurrencyPolicy: Forbid` 行为与镜像 digest 校验；
- 常驻 worker Relay 的真实投递（本次用单轮 ClaimBatch 模拟）与 `iam_outbox_delivery_total` 指标观测；
- `iam.certification.sweep_failed` 告警的 Alertmanager 路由与收敛（历史失败 Job 清理步骤见 `docs/runbooks/iam.md` §季度访问复核）；
- 开启 `auto_revoke=true` 的 staging 演练（本演练已在 MySQL 层验证回收语义，但生产开启仍按手册要求先走 staging）。

## 4. 复跑方式

```bash
docker compose -f docker-compose.integration.yml up -d --wait mysql redis
export IAM_TEST_MYSQL_DSN="app:app@tcp(127.0.0.1:3306)/admin_frame_test?parseTime=true&loc=UTC"
export TEST_REDIS_ADDR="127.0.0.1:6379"
# 可选：指定证据归档目录；未设置时写入测试临时目录
export CERT_DRILL_ARTIFACT_DIR="$(pwd)/docs/incident/drills/artifacts/2026-11-08-certification"
go test ./tests/integration/iam/ -run TestCertificationSweep -v -count=1
```

该用例为常驻回归资产：后续对扫描 / 回收 / 审计链路的任何改动，都会在集成层重新执行本剧本。
