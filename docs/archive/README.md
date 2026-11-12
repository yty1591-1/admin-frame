# docs/archive —— 已闭环过程文档归档区

> **归档区是只读的。** 这里存放的是「某段时间做了什么」的过程产物（批次台账、完成报告、修复日志、执行报告、已实施计划、一次性评审）。
>
> **这些文档不构成任何约束**，结论已沉淀到 `docs/*_STANDARDS.md`、`docs/ARCHITECTURE.md` 或 `docs/identity-authn.md` 等生效文档中。
> 保留目的只有一个：**追溯当时的判断依据与验证证据**。
>
> 返回文档地图：[`../README.md`](../README.md)

---

## 一、归档规则

1. **进入条件**（三条同时满足）：事项已闭环；结论已回写 L1/L2；不再作为排期或验收依据。
2. **放置规则**：`archive/<领域>/`，命名沿用原文件名，不重命名（便于从旧提交、旧 Issue、旧 MR 反查）。
3. **不删除、不改写**：移动一律用 `git mv` 保留历史；仅在文首追加一行归档状态说明，正文不改。
4. **失效链接属预期**：历史文档引用的是当时的代码路径（如已被合并的迁移 `000010`～`000012`、已删除的 `cmd/identity-maintenance`），这些链接不再修复。
5. **重新启用前须先决策**：若某项结论需要重新推进，应新建 L3 计划文档并在 `docs/README.md` 登记，而不是把归档文件搬回根目录。

---

## 二、identity 领域（16 份）

identity 域整改走完了「差距评审 → 计划 → 批次台账 → 完成报告」四轮，全部闭环。**现行结论以 [`identity-authn.md`](../identity-authn.md)、[`identity-slo.md`](../identity-slo.md) 与各 `*_STANDARDS.md` 为准。**

### 2.1 差距评审（基线来源，已被后续评审取代）

| 文档 | 时点 | 结论 |
|------|------|------|
| [identity-module-analysis-and-production-gap.md](./identity/identity-module-analysis-and-production-gap.md) | 首轮 | 自述「分析结论已合并到修复日志与实施计划，本文件保留为基线索引」 |
| [identity-authn-production-gaps.md](./identity/identity-authn-production-gaps.md) | 2026-10-29 | 静态审查：4 项 P0 / 7 项 P1 / 5 项 P2，被下一份全面评审取代 |
| [identity-enterprise-production-gap-review.md](./identity/identity-enterprise-production-gap-review.md) | 2026-10-31 | 企业级 / 生产级对标差距分析，§8.1～§8.4 已全部闭环 |

### 2.2 第一轮：P0 / P1 清理

| 文档 | 结论 |
|------|------|
| [identity-p0-p1-fix-log.md](./identity/identity-p0-p1-fix-log.md) | P0 全部完成；P1 主要项完成，P1-13 保留 staging 实测 |
| [identity-p1-remaining-implementation-plan.md](./identity/identity-p1-remaining-implementation-plan.md) | 剩余 5 项 P1 的实施计划与验收标准 |
| [identity-p1-remaining-completion-report.md](./identity/identity-p1-remaining-completion-report.md) | P1-2 / P1-4 / P1-6 / P1-14 完成 |
| [identity-p1-1-argon2-execution-report.md](./identity/identity-p1-1-argon2-execution-report.md) | Argon2 移出数据库事务，代码与回归通过 |

### 2.3 第二轮：8.x 批次（台账 + 完成报告成对）

| 批次 | 台账 | 完成报告 | 范围与结论 |
|------|------|----------|-----------|
| 8.1 | — | [completion-report](./identity/identity-p1-batch-8-1-completion-report.md) | 5 项 P1（REL-01/02、OPS-02、PERF-01/02/03）已完成 |
| 8.2 | [register](./identity/identity-p1-batch-8-2-register.md) | [completion-report](./identity/identity-p1-batch-8-2-completion-report.md) | SEC-01/02/05/07 四项 P1 闭环 |
| 8.3 | [register](./identity/identity-p2-batch-8-3-register.md) | [completion-report](./identity/identity-p2-batch-8-3-completion-report.md) | 10 项 P2 完成（M1/M2；M3 回归移交测试团队） |
| 8.3 剩余 | [register](./identity/identity-p2-batch-8-3-remaining-register.md) | [completion-report](./identity/identity-p2-batch-8-3-remaining-completion-report.md) | 24 项剩余 P2 全部完成 |
| 8.4 | [register](./identity/identity-batch-8-4-register.md) | [completion-report](./identity/identity-batch-8-4-completion-report.md) | 4 项风险 4/4 闭环，无风险接受降级 |

---

## 三、platform 领域（3 份）

| 文档 | 归档理由 |
|------|----------|
| [platform-remediation-plan.md](./platform/platform-remediation-plan.md) | `internal/platform` 整改计划，2026-10-20 已实施；约束条目已并入 `CONFIG/RELIABILITY/OBSERVABILITY/SECURITY/TESTING` 各专文与 `ARCHITECTURE.md` §5.6/§6.1 |
| [iam-permission-kind-removal-plan.md](./platform/iam-permission-kind-removal-plan.md) | IAM 权限模型精简与菜单资源改造，已按清单落地（`iam_menu` 建表、`action` 列删除、`resource` 改生成列） |
| [production-readiness-gaps.md](./platform/production-readiness-gaps.md) | 平台级能力差距评估（v0.2，2026-10-22，基线 `dad8fc0`），2026-11-09 归档；`ENT-01`～`ENT-19` 已全部关闭，`ENT-20`～`ENT-25` 的状态、触发条件与现状证据已并入 [`production-readiness-implementation-plan.md`](../production-readiness-implementation-plan.md) §六 / §十二 |

> 注意：前两份计划仍有 2 处指向 `platform-remediation-plan.md` 的引用来自活跃计划
> [`production-readiness-implementation-plan.md`](../production-readiness-implementation-plan.md)，
> 链接已更新为 `../archive/platform/...`。
> 第三份（差距评估）的上游关系已反转：实施计划 §1.1 的「差距评估」输入链接已改指本归档路径，
> 排期与验收一律以实施计划 §十二 为准。

---

## 四、一次性评审与会签（7 份）

| 文档 | 归档理由 | 遗留 |
|------|----------|------|
| [FRONTEND_INTEGRATION_REVIEW.md](./reviews/FRONTEND_INTEGRATION_REVIEW.md) | 前后端契约对接评审（GAP 清单 + 排期建议），评审时点 2026-10-31 | 部分 GAP 未闭环，见下 |
| [FRONTEND_ARCHITECTURE_UX_REVIEW.md](./reviews/FRONTEND_ARCHITECTURE_UX_REVIEW.md) | 前端架构 / 交互 / 视觉评审（评分 + 优化路线），评审时点 2026-10-31 | 优化路线未排期 |
| [platform-architecture-review.md](./reviews/platform-architecture-review.md) | `internal/platform` 架构评审（P0/P1 清单 → 整改），v0.2 复评已把 3 项「不达标」改为「达标」，度量表补 v0.2 列；结论已并入 `ARCHITECTURE.md` §5.6 / §6.1 与 `check-layering.sh` | 无（P0/P1 全关闭） |
| [platform-shared-security-review.md](./reviews/platform-shared-security-review.md) | `platform` / `shared` / `security` 三目录边界与安全评审（G1–G8），v1.0；G1–G8 全部关闭，选层决策表已并入 `ARCHITECTURE.md` §3.4 | 无（G1–G8 全关闭） |
| [m0-signoff-record-2026-11-10.md](./reviews/m0-signoff-record-2026-11-10.md) | IAM 生产化整改立项会签（M0），2026-11-11 三位批准人（研发 / 安全 / 运维，单人分角色）均签「同意」，D1～D5 冻结 | 无（结论已回写计划 §3.1 `M0` 与 §13.4） |
| [s6-1-security-review-record-2026-11-10.md](./reviews/s6-1-security-review-record-2026-11-10.md) | 身份域安全评审（S6-1），2026-11-11 由「有条件通过」转「通过」；`C1`～`C3` 闭环，staging 项按 `STG-EQ-01` 等价环境放行 | 无（结论已回写 `identity-authn.md` §21 与计划 §13.2） |
| [p1-7-acceptance-report.md](./reviews/p1-7-acceptance-report.md) | P1-7 访问复核验收（S7-5），2026-11-11 安全 / 平台双评审签字通过；ADR-0012 评审 Accept，staging 实战按等价环境放行 | 无（结论已回写 `rbac-iam.md` §10.3 与计划 §13.3） |

> **重要**：前两份前端评审里的未闭环 GAP **当前没有对应的活跃计划文档跟踪**。
> 如果要推进，请新建 `docs/frontend-remediation-plan.md`（L3 活跃计划）并在 `docs/README.md` §3.2 登记，
> 把 GAP 条目搬过去逐条给验收标准；**不要**直接修改归档文件里的清单。
>
> 后两份平台评审的整改动作已全部落地，归档只保留「当时如何判定」的依据。

---

## 五、验证与演练记录（4 份）

一次性的实测 / 故障注入记录。验证对象均已交付，结论与门禁留在 L1 / L2，
本组只保留「当时怎么测、测出什么缺陷」的证据。

| 文档 | 验证对象 | 归档理由 |
|------|----------|----------|
| [alert-drill-ent06.md](./verify/alert-drill-ent06.md) | ENT-06 AC-2 告警故障注入（2026-11-02） | 三类故障在 kind 实测触发并自动恢复；规则已入 `deploy/observability`，由 `check-alerts` 保活 |
| [observability-dashboard-verify.md](./verify/observability-dashboard-verify.md) | ENT-06 AC-3 看板与 PromQL（2026-11-03） | 35 个看板面板全部有指标定义，由 `check-observability` 的 OBS-07 保活 |
| [trace-chain-verify.md](./verify/trace-chain-verify.md) | ENT-06 AC-4 入口 → DB 链路（2026-11-03） | trace 父子关系与 `db.system=mysql` 实测贯通；要求已写入 `OBSERVABILITY_STANDARDS.md` |
| [audit-export-verify.md](./verify/audit-export-verify.md) | ENT-04 T5/T6 异步导出与到期归档（2026-11-03） | `AC-ENT-04-1`～`4` 达成；导出、归档、下载行为由 `check-audit` 与集成用例覆盖 |

---

## 六、已实施计划（3 份）

领域规划的落地凭证。规划期约束已转化为 ADR 与设计文档，因此不再作为排期依据。

| 文档 | 归档理由 | 结论去向 |
|------|----------|----------|
| [feature-flag-plan.md](./plans/feature-flag-plan.md) | ENT-11 特性开关规划（FF-01～FF-08 全部落地） | [`adr/0009`](../adr/0009-featureflag-inprocess-evaluation.md)（进程内评估决策）、`ARCHITECTURE.md` §17.3（flag 硬约束）、`check-featureflag.sh` |
| [notification-center-plan.md](./plans/notification-center-plan.md) | ENT-10 通知中心规划（NT-01～NT-10 全部落地） | [`adr/0008`](../adr/0008-notification-event-driven.md)（编排决策）、[`notification-channels.md`](../notification-channels.md)（渠道驱动契约）、`check-notification.sh` |
| [iam-p1-5-7-engineering-plan.md](./plans/iam-p1-5-7-engineering-plan.md) | IAM P1-5/6/7 生产化整改执行基线（M0～M6 全部完成，2026-11-11 关闭） | [`adr/0012`](../adr/0012-certification-export-limit.md)（容量与导出口径）、`identity-authn.md` §21（SCIM / M2M 契约）、`rbac-iam.md` §10.3（访问复核）、`runbooks/identity-federation-onboarding.md`、`check-iam-coverage.sh` / `check-authn.sh` |

> 两份计划的「生产渠道驱动装配」仍依赖渠道凭据，属环境配置项，不构成计划未闭环；
> 若后续要改编排语义，按规则 §一.5 新建 L3 计划，不要搬回归档文件。

---

## 七、租户与 IAM 领域（2 份）

两份的结论均已回写 L1 / L2 / L3，不再作为排期或验收依据。

| 文档 | 归档理由 | 结论去向 |
|------|----------|----------|
| [tenant-enterprise-review.md](./tenant/tenant-enterprise-review.md) | 租户域企业级差距评审 + B1～B4 分批计划，2026-11-05 全部收口（评审时的 3 个 P0 缺陷已修复，6 项文档回写清单全部落地） | `ENT-09` 状态（[`production-readiness-implementation-plan.md`](../production-readiness-implementation-plan.md) §十二）、[`tenant-data-lifecycle.md`](../tenant-data-lifecycle.md)、[`adr/0006`](../adr/0006-tenant-as-isolation-boundary.md) / [`adr/0007`](../adr/0007-tenant-member-iam-binding.md)、`scripts/ci/check-tenant.sh` |
| [iam-audit-report-2026-11-05.md](./iam/iam-audit-report-2026-11-05.md) | 2026-11-05 一次性 IAM 审计（总覆盖率 57.2%、门禁与架构评估），结论已被下游引用 | [`iam-p1-5-7-engineering-plan.md`](./plans/iam-p1-5-7-engineering-plan.md)（P1-5/6/7 的输入依据）、[`rbac-iam.md`](../rbac-iam.md) |

> 租户评审的 B1～B4 执行记录留在归档正文 §6.2～§6.4；若租户域再起整改，按 §一.5 新建 L3 计划并在
> [`docs/README.md`](../README.md) §3.2 登记，不要搬回本目录。
