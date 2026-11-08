# P1-7 访问复核验收报告（S7-5）

| 字段 | 内容 |
|---|---|
| 范围 | P1-7 访问复核全量（S7-1 模型契约 → S7-5 演练与验收） |
| 结论 | **工具链功能验收通过；整体签收不通过**（3 项前置未完成，见 §5） |
| 日期 | 2026-11-08 |
| 依据 | [计划 §13.3 验收标准](../iam-p1-5-7-engineering-plan.md#133-p1-7-验收)、[计划 §8 性能指标](../iam-p1-5-7-engineering-plan.md#8-性能指标与容量基线) |
| 评审状态 | **未签字**：安全评审与平台评审均未发生，本报告是待评审输入 |
| 环境口径 | 集成层为真实 MySQL 8.0.46 + Redis 7 + Outbox；**staging 集群实战未做** |

## 1. 结论

分两层，避免把「功能跑通」说成「已验收」：

- **功能验收：通过。** 复核活动的创建、激活、清单生成、裁决、超期扫描、自动回收、证据导出、
  审计投递全链路在真实存储链路上逐步验证，语义与 [rbac-iam.md §10.3](../rbac-iam.md) 设计一致。
- **整体签收：不通过。** 差三件事——季度演练的运维形态（CronJob 真实调度 + staging 实战）、
  单活动规模上限的口径决策、双评审签字。这三项都不是「再测一遍代码」，而是需要窗口与人的环节。

## 2. 验收项逐条判定

| # | §13.3 验收项 | 判定 | 主要证据 |
|---|---|---|---|
| 1 | 一次完整季度复核 Campaign 演练：清单生成 → 责任人确认 → 超期回收 → 证据导出 | **部分达标** | 全链路已在集成层跑通，但责任人确认是脚本调用、CronJob 未真实调度（§3.1） |
| 2 | 复核结果全程审计可关联 Campaign；导出字段完整率 100% | **达标** | 审计事件与业务同事务、fail-closed；已裁决 100 行四列全 100%（§3.2） |
| 3 | 自动回收具备并发安全与回退开关；误操作可人工恢复 | **达标** | `expectedVersion` 乐观锁 + 指纹二次校验 + `auto_revoke` 默认关（§3.3） |
| 4 | 10k 主体清单生成达性能指标；10 万行导出口径 | **技术达标，口径待批** | 10k 529 ms；10 万档位生成 4.68 s、导出 479–595 ms（§3.4） |

## 3. 逐条说明

### 3.1 季度复核演练（部分达标）

已达成（[演练记录](../incident/drills/2026-11-08-certification-sweep.md)）：

- 演练 A（`auto_revoke=false`）：超期任务置 `expired`、绑定**未**回收、≥3 条审计事件经 `async_outbox`
  投递成功（pending=0 / DLQ=0）、无 `unbind` 事件、二轮扫描幂等（tasks=0、零新增审计）。
- 演练 B（`auto_revoke=true`）：指纹漂移场景任务 `skipped` 且重绑后的绑定不被回收；指纹一致场景
  任务 `revoked`、绑定软撤销、主体快照（L1 + Redis L2）被删除、三类审计齐备。
- 审计 fail-closed、快照失效（含共享 L2）均按设计成立。

未达成，且不能用现有证据替代：

| 缺口 | 为什么不能算已达成 |
|---|---|
| 责任人通过真实入口裁决 | 演练用脚本调 `DecideTask` 100 次，未验证 A 端 HTTP 入口 + 权限点 + 前端菜单这条真实路径的可用性 |
| CronJob 真实调度 | 只验证了 `worker -certification-sweep` 的一次性进程行为，`concurrencyPolicy: Forbid`、镜像 digest、Job 失败重试与告警收敛未实测 |
| staging 环境 | 全程本机 compose；staging 的配置、网络、权限边界未覆盖 |

判定：**功能链路闭环，运维形态未闭环**。计划里这条保持不勾选。

### 3.2 审计关联与字段完整率（达标）

- 事件覆盖：`rbac.certification` 记录创建 / 激活 / 裁决 / 自动过期 / 自动回收 / 跳过 / 导出；
  自动回收另发 `rbac.binding_change(unbind, actor=system:certification)`。
- 一致性：审计写入与业务写入同事务，审计失败则操作回滚（fail-closed 已在演练中断言）。
- 字段完整率按**状态应填口径**：已裁决 100 行 `decision` / `reason` / `decided_by` / `decided_at` 全 100%；
  10 000 行未裁决任务按语义留空。口径依据见 [S7-2 实测 §7](../incident/drills/2026-11-08-s7-2-perf.md)。
- **消费侧注意**：`auto_expire` 任务的 `decided_by` / `reason` 列本就为空（`Expire` 只写 `status` 与
  `decided_at`），操作人只存在于审计事件。证据消费方（SIEM / 等保）必须以 `rbac.certification` 为准，
  不得读任务列——此口径已在演练记录 §2 登记，不是缺陷。

### 3.3 自动回收的并发安全与回退（达标）

- 并发安全：裁决与自动回收均按 `expectedVersion` 做乐观锁（`Save(ctx, task, expectedVersion)`），
  版本冲突返回 `ErrConflict` 而不是覆盖写入。
- 误回收防护：回收前按 `subject/channel/merchant/role/expiresAt/revoked` 指纹二次校验，
  与生成任务时不一致则置 `skipped` 且**绝不回收**（演练 B 已实测）。
- 回退开关：`auto_revoke` 默认 `false`，只标记 `expired` 不动绑定；`validate` 强制
  「`auto_revoke` 必须同时启用 `certification`」，可整体关闭回到纯人工模式。

### 3.4 性能与容量（技术达标，口径待批）

| 指标 | 目标 | 实测 | 判定 |
|---|---|---|---|
| 清单生成 10k 主体 | ≤ 5 min | 529 ms | 达标 |
| 清单生成 10 万主体（抬升上限档位） | — | 4 683–4 697 ms | 达标（若采用该档位） |
| 证据导出 10 万行（抬升上限档位） | ≤ 2 min | 479–595 ms / 14.30 MB | 达标（若采用该档位） |
| 默认上限下的 10 万行导出 | ≤ 2 min | **不可达**：第 10001 条被拒 | 受上限约束 |

口径冲突已单独立项为 [ADR-0012](../adr/0012-certification-export-limit.md)（Proposed）：建议「先补配置项、
再把默认上限提到 100000」，导出保持全量读入不做流式。**生产默认值未改动**，决策待容量评审。
10 万档位的实测数据与代价见 [10 万档位实测](../incident/drills/2026-11-08-s7-2-perf-100k.md)。

## 4. 交付物清单

| 层 | 交付物 | 位置 |
|---|---|---|
| 领域与用例 | Campaign / Task / Decision 模型与状态机、决策、超期回收、证据导出、超期扫描 | `internal/iam/domain/certification.go`、`internal/iam/app/certification_service.go`、`certification_sweeper.go` |
| 持久化 | SQL 仓储（含 500 行/批多值插入）与内存仓储 | `internal/iam/infrastructure/persistence/mysql_certification.go` |
| 传输 | A 端复核路由（受 `iam.certification.enabled` 控制） | `internal/iam/transport/admin/router.go` |
| 装配 | A 端与 worker 共用的服务构造 | `cmd/internal/iamruntime/` |
| 调度 | 一次性扫描模式 `worker -certification-sweep` | `cmd/worker/` |
| 部署 | Helm `certificationSweep` CronJob（默认关、prod 开、每日 02:00、`Forbid`） | `deploy/helm/admin-frame/` |
| 告警 | `iam.certification.sweep_failed`（P2） | `deploy/observability/prometheus/rules/` |
| 测试 | 演练 2 用例（扫描 / 回收 / 审计 / 快照）、性能 5 场景、单元用例 | `tests/integration/iam/mysql_certification_sweep_test.go`、`mysql_certification_perf_test.go` |
| 文档 | 设计、运维手册、演练记录 ×2、IdP 无关；ADR-0012 | `docs/rbac-iam.md` §10.3、`docs/runbooks/iam.md`「季度访问复核」、`docs/incident/drills/` |

## 5. 未完成项与签收条件

| # | 未完成项 | 类型 | 完成判据 |
|---|---|---|---|
| 1 | CronJob 在 staging 真实调度一次（复用 AI-05 窗口） | 运维验证 | Job 按计划触发、`Forbid` 生效、失败时 `iam.certification.sweep_failed` 在 Alertmanager 收到并可收敛 |
| 2 | 责任人经 A 端真实入口完成一轮裁决 | 功能验证 | 用真实权限点与账号走 HTTP 入口，至少 1 个活动全量裁决完毕 |
| 3 | ADR-0012 容量评审 | 决策 | 评审 Accept 并落地配置项；或选择「多活动累计」口径并同步改指标 |
| 4 | 安全评审 + 平台评审签字 | 流程 | 两份签字记录归档到 `docs/archive/reviews/` |
| 5 | 季度节拍的 Owner 与首轮排期 | 运营 | `rbac-iam.md` 中「访问复核流程与季度计划已建立」一项转勾选 |

5 项全部完成后，可把 §13.3 第一条勾选并进入 M6 生产准入评审。

## 6. 证据索引

| 证据 | 文件 | 复跑方式 |
|---|---|---|
| 全流程演练（扫描 / 回收 / 审计 / 快照） | [2026-11-08-certification-sweep.md](../incident/drills/2026-11-08-certification-sweep.md) + `artifacts/2026-11-08-certification/` | 见该文 §4 |
| 10k 性能与字段完整率 | [2026-11-08-s7-2-perf.md](../incident/drills/2026-11-08-s7-2-perf.md) + `artifacts/2026-11-08-s7-2-perf/` | 见该文 §9 |
| 10 万档位性能与内存代价 | [2026-11-08-s7-2-perf-100k.md](../incident/drills/2026-11-08-s7-2-perf-100k.md) + `artifacts/2026-11-08-s7-2-perf-100k/` | 见该文 §6 |
| 集成门禁（含复核包） | `scripts/ci/run-integration-tests.sh` | `bash scripts/ci/run-integration-tests.sh` |
| 分层覆盖率 | — | `bash scripts/ci/check-iam-coverage.sh` |

## 7. 判定口径说明

- **「达标」的取证要求**：有可复跑的用例或脚本、有落盘证据、有用例自身的确定性断言。
  仅有人工观察或外推估算的不计为达标。
- **「部分达标」的用法**：功能已实现且可验证，但验收项描述的**形态**没有完整发生。
  本报告中仅 §13.3 第 1 条属此类，且已列明差在哪三处。
- **不代替评审**：本报告由实现方出具，结论是「实现了什么、验证到什么程度、差什么」，
  不构成安全或合规结论。签收须由 §5 第 4 项的两位评审人给出。
