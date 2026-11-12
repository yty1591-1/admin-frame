> **归档**：本报告已于 2026-11-11 完成双评审签字并归档，只读不再维护；验收结论已沉淀到
> [`iam-p1-5-7-engineering-plan.md`](../plans/iam-p1-5-7-engineering-plan.md) §13.3 与 [`rbac-iam.md`](../../rbac-iam.md) §10.3，归档索引见 [`archive/README.md`](../../README.md)。

# P1-7 访问复核验收报告（S7-5）

| 字段 | 内容 |
|---|---|
| 范围 | P1-7 访问复核全量（S7-1 模型契约 → S7-5 演练与验收） |
| 结论 | **工具链与验证链路全部闭环；原剩余 3 项已于 2026-11-11 闭环并签收通过**（见 §5） |
| 日期 | 2026-11-08（§3.1 补真实入口裁决与完整演练；§3.5 补集群调度；2026-11-11 补签收） |
| 依据 | [计划 §13.3 验收标准](../plans/iam-p1-5-7-engineering-plan.md#133-p1-7-验收)、[计划 §8 性能指标](../plans/iam-p1-5-7-engineering-plan.md#8-性能指标与容量基线) |
| 评审状态 | **已签字通过（2026-11-11）**：安全评审与平台评审均已完成，结论「通过」；本报告已归档 |
| 环境口径 | 本地真实进程（HTTP 全链路）+ 真实 MySQL/Redis/Outbox；kind 集群 CronJob 真实调度；staging 业务集群未执行，按 `STG-EQ-01` 等价环境证据放行 |

## 1. 结论

分三层，避免把「功能跑通」或「本地演练过」当成「已验收」：

- **功能与验证链路：闭环。** 复核活动的创建、激活、清单生成、裁决、超期扫描、自动回收、证据导出、
  审计投递全链路已验证到位；其中「责任人裁决」走 A 端真实 HTTP 入口，「超期扫描」由真实
  `worker -certification-sweep` 进程执行，「集群按计划触发」在 kind 上实测。
- **验收项：4 条全部达标**（§2），其中第 1 条的「技术链路」已由一次组合演练覆盖；
  组织层面的「真实季度执行」（真实责任人、真实周期）仍需 Owner 排期。
- **整体签收：通过。** 原剩三项（staging 环境实战与告警收敛、ADR-0012 容量评审、双评审签字）
  已于 2026-11-11 全部闭环：容量评审 Accept、双评审签字完成，staging 实战按 `STG-EQ-01` 等价环境证据放行。

## 2. 验收项逐条判定

| # | §13.3 验收项 | 判定 | 主要证据 |
|---|---|---|---|
| 1 | 一次完整季度复核 Campaign 演练：清单生成 → 责任人确认 → 超期回收 → 证据导出 | **达标（技术链路）** | 同一活动内四段串联：A 端 API 建活动与激活 → 真实入口裁决 → 真实 worker 进程扫描 → 证据导出含两种终态（§3.1） |
| 2 | 复核结果全程审计可关联 Campaign；导出字段完整率 100% | **达标** | 审计事件与业务同事务、fail-closed；已裁决 100 行四列全 100%（§3.2） |
| 3 | 自动回收具备并发安全与回退开关；误操作可人工恢复 | **达标** | `expectedVersion` 乐观锁 + 指纹二次校验 + `auto_revoke` 默认关（§3.3） |
| 4 | 10k 主体清单生成达性能指标；10 万行导出口径 | **达标** | 10k 529 ms；10 万档位生成 4.68 s、导出 479–595 ms（§3.4）；口径由 ADR-0012 Accept（2026-11-11） |

## 3. 逐条说明

### 3.1 完整季度复核演练（达标，技术链路）

演练用例 `TestCertificationQuarterlyDrillEndToEnd`（`tests/e2e/certification_test.go`）在**同一个活动**
内串起四段，且全程走真实入口与真实进程：

| 段 | 怎么做的 | 断言 |
|---|---|---|
| 清单生成 | A 端 API 建角色 + 2 条绑定 → 建活动（`due_at` = +8s）→ 激活 | `task_count=2` |
| 责任人确认 | A 端 API 裁决任务 1（`decision=confirmed` + 原因） | 状态 `confirmed`、`decided_by` 非空 |
| 超期回收 | 等过截止时间，起**真实 worker 进程** `-certification-sweep` | 退出码 0、日志 `tasks=1`、另一条任务 `expired` |
| 证据导出 | A 端 API 导出 CSV | 3 行（表头 + 2），`confirmed` 与 `expired` 两种终态并存、`decided_at` 均非空 |
| 活动关闭 | A 端 API `close` | `status=closed`、任务不删除 |

为什么四段要放在一个活动里：分开测会漏掉「同一活动内混合终态」的导出语义——证据包必须能同时
承载「人工确认保留」和「超期自动过期」两类结论，这才是一次真实季度复核的样子。

补充证据（同一链路的另一组验证）：

- 集成层双路径演练（`auto_revoke` 关 / 开、指纹漂移防护、审计 fail-closed、Redis L2 快照失效）：
  [2026-11-08-certification-sweep.md](../../incident/drills/2026-11-08-certification-sweep.md)。
- 另一条「纯粹由责任人点完」的链路（建活动 → 激活 → 裁决 → 重复裁决被拒 → 导出 → 关闭）：
  `TestCertificationReviewOnAdminEntry`，覆盖 A 端权限点 `iam:certification:read/write/decide`。

**边界（不声称完成的部分）**：

- 真实责任人是脚本账户（guided admin），不是组织中的人；「季度」周期与 Owner 排期属组织流程（§5 第 5 项）。
- 环境为本地真实进程 + 真实依赖，不是 staging（§5 第 1 项）。

### 3.2 审计关联与字段完整率（达标）

- 事件覆盖：`rbac.certification` 记录创建 / 激活 / 裁决 / 自动过期 / 自动回收 / 跳过 / 导出；
  自动回收另发 `rbac.binding_change(unbind, actor=system:certification)`。
- 一致性：审计写入与业务写入同事务，审计失败则操作回滚（fail-closed 已在演练中断言）。
- 字段完整率按**状态应填口径**：已裁决 100 行 `decision` / `reason` / `decided_by` / `decided_at` 全 100%；
  10 000 行未裁决任务按语义留空。口径依据见 [S7-2 实测 §7](../../incident/drills/2026-11-08-s7-2-perf.md)。
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

### 3.4 性能与容量（达标）

| 指标 | 目标 | 实测 | 判定 |
|---|---|---|---|
| 清单生成 10k 主体 | ≤ 5 min | 529 ms | 达标 |
| 清单生成 10 万主体（抬升上限档位） | — | 4 683–4 697 ms | 达标（若采用该档位） |
| 证据导出 10 万行（抬升上限档位） | ≤ 2 min | 479–595 ms / 14.30 MB | 达标（若采用该档位） |
| 默认上限下的 10 万行导出 | ≤ 2 min | **不可达**：第 10001 条被拒 | 受上限约束 |

口径冲突已单独立项为 [ADR-0012](../../adr/0012-certification-export-limit.md)，**2026-11-11 评审 Accept**：
采纳「配置项已就绪、默认上限抬到 100000」的决策，默认值变更随下一发布窗口实施（当前代码默认仍为 10000）。
10 万档位的实测数据与代价见 [10 万档位实测](../../incident/drills/2026-11-08-s7-2-perf-100k.md)。

### 3.5 集群调度与失败行为（达标，kind）

[CronJob 真实调度演练](../../incident/drills/2026-11-08-cronjob-sweep-kind.md) 在 kind `af-verify`（K8s v1.34.3）
上开启 `certificationSweep`，用每分钟调度实测：

| 验证点 | 结果 |
|---|---|
| 按 `schedule` 真实触发（非手动 `create job`） | 两轮连续触发，均 `Complete 1/1` |
| 扫描语义（`auto_revoke=false`） | `scanned=1 overdue=1 expired=1 tasks=2`，绑定未撤销 |
| 幂等 | 第二轮 `tasks=0` |
| 审计跨进程投递 | 2 条 `auto_expire` 入 `async_outbox`，常驻 worker 投递 `succeeded` |
| 失败路径 | 注入坏 DSN → 3 个 Pod（`backoffLimit=2`）→ Job `Failed`，日志为装配期连接错误 |

演练还暴露两个运维陷阱，已写入 [runbooks/iam.md](../../runbooks/iam.md)「季度访问复核」：
`--reuse-values` 会丢弃 `certificationSweep` 段默认值（镜像名渲染为空、并发策略退化为 `Allow`）；
`kubectl set env` 改过 CronJob 后直接 `helm upgrade` 会因 `value`/`valueFrom` 并存被拒。

未覆盖：Alertmanager 告警路由与收敛（集群内无 Prometheus/Alertmanager 实例）、staging 环境、
`Forbid` 的重叠抑制（需单次扫描超过调度间隔）、镜像 digest 校验。

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

| # | 事项 | 类型 | 状态 | 完成判据 |
|---|---|---|---|---|
| 1 | 责任人经 A 端真实入口完成一轮裁决 | 功能验证 | **已完成**（2026-11-08） | `TestCertificationReviewOnAdminEntry`：真实权限点 + HTTP 入口，含重复裁决被拒、CSV 导出、活动关闭 |
| 2 | CronJob 真实调度 | 运维验证 | **已完成（kind）**（2026-11-08） | 两轮按分钟触发、失败路径 Job `Failed`；见 [演练记录](../../incident/drills/2026-11-08-cronjob-sweep-kind.md) |
| 3 | ADR-0012 的配置通道 | 实现 | **已完成**（2026-11-08） | `iam.certification.max_tasks_per_campaign` 已接入配置与封装（0=默认 10000，封顶 500000，负值/超封顶拒绝）；默认值未改 |
| 4 | ADR-0012 容量评审 | 决策 | **已完成（2026-11-11）** | 评审 Accept「默认上限抬到 100000」，配置通道已就绪，默认值变更随下一发布实施 |
| 5 | staging 实战与告警收敛 | 运维验证 | **已完成（等价环境放行，2026-11-11）** | kind `af-verify` 按计划触发 Job、`iam.certification.sweep_failed` 规则经 `check-alerts` 校验；真实 staging 窗口未排到，按项目负责人决定以等价环境证据放行 |
| 6 | 安全评审 + 平台评审签字 | 流程 | **已完成（2026-11-11）** | 两份签字见 §5.1，记录与本报告一同归档到 `docs/archive/reviews/` |
| 7 | 季度节拍的 Owner 与首轮排期 | 运营 | **已完成（2026-11-11）** | Owner：@identity-owner；首轮周期 2027-Q1，`rbac-iam.md` 中「访问复核流程与季度计划已建立」已勾选 |

原剩余 4 项（4–7）已于 2026-11-11 全部闭环，具备 M6 生产准入条件。

## 5.1 签字（2026-11-11）

| 角色 | 结论 | 意见 | 签字 | 日期 |
|---|---|---|---|---|
| 安全评审人 | 通过 | 与 `S6-1` 结论一致，无高危/严重问题 | @security-owner | 2026-11-11 |
| 平台评审人 | 通过 | 验收项 4/4 达标，剩余环境项按等价证据放行 | @sre-owner | 2026-11-11 |

## 6. 证据索引

| 证据 | 文件 | 复跑方式 |
|---|---|---|
| 完整季度演练（真实入口 + 一次性扫描 + 混合终态导出） | `tests/e2e/certification_test.go` `TestCertificationQuarterlyDrillEndToEnd` | `E2E_MYSQL_DSN=… E2E_REDIS_ADDR=… go test ./tests/e2e/ -run TestCertificationQuarterlyDrillEndToEnd -v` |
| 纯人工裁决链路（权限点 / 重复裁决 / 导出 / 关闭） | 同上 `TestCertificationReviewOnAdminEntry` | 同上，改 `-run` 用例名 |
| 集群 CronJob 真实调度与失败路径 | [2026-11-08-cronjob-sweep-kind.md](../../incident/drills/2026-11-08-cronjob-sweep-kind.md) | 见该文 §6 |
| 全流程演练（扫描 / 回收 / 审计 / 快照） | [2026-11-08-certification-sweep.md](../../incident/drills/2026-11-08-certification-sweep.md) + `artifacts/2026-11-08-certification/` | 见该文 §4 |
| 10k 性能与字段完整率 | [2026-11-08-s7-2-perf.md](../../incident/drills/2026-11-08-s7-2-perf.md) + `artifacts/2026-11-08-s7-2-perf/` | 见该文 §9 |
| 10 万档位性能与内存代价 | [2026-11-08-s7-2-perf-100k.md](../../incident/drills/2026-11-08-s7-2-perf-100k.md) + `artifacts/2026-11-08-s7-2-perf-100k/` | 见该文 §6 |
| 集成门禁（含复核包） | `scripts/ci/run-integration-tests.sh` | `bash scripts/ci/run-integration-tests.sh` |
| 分层覆盖率 | — | `bash scripts/ci/check-iam-coverage.sh` |

## 7. 判定口径说明

- **「达标」的取证要求**：有可复跑的用例或脚本、有落盘证据、有用例自身的确定性断言。
  仅有人工观察或外推估算的不计为达标。
- **「部分达标」的用法**：功能已实现且可验证，但验收项描述的**形态**没有完整发生。
  本报告中仅 §13.3 第 1 条属此类，且已列明差在哪三处。
- **不代替评审**：本报告由实现方出具，结论是「实现了什么、验证到什么程度、差什么」，
  不构成安全或合规结论。签收结论由 §5.1 的两位评审人给出（2026-11-11 通过）。
