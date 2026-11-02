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

## 三、platform 领域（2 份）

| 文档 | 归档理由 |
|------|----------|
| [platform-remediation-plan.md](./platform/platform-remediation-plan.md) | `internal/platform` 整改计划，2026-10-20 已实施；约束条目已并入 `CONFIG/RELIABILITY/OBSERVABILITY/SECURITY/TESTING` 各专文与 `ARCHITECTURE.md` §5.6/§6.1 |
| [iam-permission-kind-removal-plan.md](./platform/iam-permission-kind-removal-plan.md) | IAM 权限模型精简与菜单资源改造，已按清单落地（`iam_menu` 建表、`action` 列删除、`resource` 改生成列） |

> 注意：这两份计划仍有 2 处指向 `platform-remediation-plan.md` 的引用来自活跃计划
> [`production-readiness-gaps.md`](../production-readiness-gaps.md) 与
> [`production-readiness-implementation-plan.md`](../production-readiness-implementation-plan.md)，
> 链接已更新为 `../archive/platform/...`。

---

## 四、一次性评审（2 份）

| 文档 | 归档理由 | 遗留 |
|------|----------|------|
| [FRONTEND_INTEGRATION_REVIEW.md](./reviews/FRONTEND_INTEGRATION_REVIEW.md) | 前后端契约对接评审（GAP 清单 + 排期建议），评审时点 2026-10-31 | 部分 GAP 未闭环，见下 |
| [FRONTEND_ARCHITECTURE_UX_REVIEW.md](./reviews/FRONTEND_ARCHITECTURE_UX_REVIEW.md) | 前端架构 / 交互 / 视觉评审（评分 + 优化路线），评审时点 2026-10-31 | 优化路线未排期 |

> **重要**：这两份评审里的未闭环 GAP **当前没有对应的活跃计划文档跟踪**。
> 如果要推进，请新建 `docs/frontend-remediation-plan.md`（L3 活跃计划）并在 `docs/README.md` §3.2 登记，
> 把 GAP 条目搬过去逐条给验收标准；**不要**直接修改归档文件里的清单。
