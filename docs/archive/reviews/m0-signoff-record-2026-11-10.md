> **归档**：本单已于 2026-11-11 完成会签并归档，只读不再维护；立项结论已沉淀到
> [`iam-p1-5-7-engineering-plan.md`](../plans/iam-p1-5-7-engineering-plan.md)，归档索引见 [`archive/README.md`](../README.md)。

# IAM 生产化整改立项会签单（M0）

| 字段 | 内容 |
|---|---|
| 会签对象 | [IAM 生产化整改工程规划](../plans/iam-p1-5-7-engineering-plan.md)（IAM-PLAN-2026-11-001）v1.9 |
| 会签类型 | 立项评审（M0）：范围、资源、阈值冻结 |
| 发起日期 | 2026-11-10 |
| 当前状态 | **已签（2026-11-11）**：三位批准人结论均为「同意」，无附条件；本单已按 §6 回写计划与文档地图，并按文档规则归档 |
| 依据 | 计划 §3.1 `M0` 行、§2 范围边界、§5 验收阈值、§11 风险台账 |
| 归档位置 | `docs/archive/reviews/`（与 [`p1-7-acceptance-report.md`](./p1-7-acceptance-report.md) 同级） |

> 说明：本单只解决「立项会签是否存在且可查」。交付状态、验收结论以计划 §3 与 §13 为准，
> 签字不改变任何已交付能力，也不替代 `S6-1` 安全评审与 `M5` 验收签字。

## 1. 会签范围

| 项 | 取值 | 本次是否可变更 |
|---|---|---|
| 工程范围 | P1-5（覆盖率与集成门禁）、P1-6（OIDC / SAML / SCIM / M2M）、P1-7（访问复核） | 否；扩大范围须另走 CCB |
| 范围外 | LDAP/AD 直连、Kerberos、OAuth2 授权服务器、前端视觉、P2 项、Redis 集群切换 | 否 |
| 生效版本 | 计划 v1.9（含 v1.0～v1.8 修订记录） | 否 |
| 里程碑区间 | M0 2026-11-06 ~ M6 2027-01-15 | 顺延需 CCB |

## 2. 待冻结决议（5 项）

| 编号 | 决议事项 | 计划中的取值 | 冻结后影响 |
|---|---|---|---|
| D1 | 交付范围 | P1-5 / P1-6 / P1-7 三项，范围外项见 §1 | 后续新增需求进 backlog，不挤占里程碑 |
| D2 | 质量阈值 | 分层覆盖率：`domain ≥80`、`app ≥75`、`catalog ≥60`、`cache ≥75`、`menuview ≥90`、`observability ≥45`、`transport/rest ≥60`、`transport/admin` 与 `transport/biz ≥90`；变更行覆盖率 ≥70% | 阈值下调须 CCB，且需在 PR 中说明理由 |
| D3 | 性能阈值 | SCIM 10k 用户 + 50 组 ≤ 达标基线（实测 151.7s）；10k 主体清单生成 ≤ 5 min（实测 403–767 ms）；单活动 1 万行导出 ≤ 基线（实测 48–81 ms） | 抬升单活动上限属容量决策，走 [ADR-0012](../../adr/0012-certification-export-limit.md) |
| D4 | 里程碑与责任人 | M0 会签 → M1 门禁 → M2 OIDC → M3 SAML → M4 SCIM/M2M → M5 复核闭环 → M6 准入；责任人矩阵见计划 §4 | 里程碑顺延须 CCB 记录 |
| D5 | 风险台账 | `R-01`～`R-08`，其中 R-03（IdP 差异）、R-04（密钥轮换）、R-05（SCIM 冲突）、R-08（安全漏洞）为高影响 | 高影响风险触发即进入升级路径 |

## 3. 证据索引（评审前请先自查）

| 证据 | 位置 | 证明什么 |
|---|---|---|
| 覆盖率与门禁实测 | 计划 §13.1 证据块 | D2 阈值已在 CI 脚本中生效 |
| 门禁失败演练 | [`drills/2026-11-08-s5-5-integration-gate.md`](../../incident/drills/2026-11-08-s5-5-integration-gate.md) | 阈值注入与删除用例均能阻断合并 |
| 协议验收 | 计划 §13.2 与 [`identity-authn.md`](../../identity-authn.md) §7.3 / §8.2.1 / §21 | OIDC / SAML / SCIM / M2M 已实现并按开关默认关闭 |
| SCIM 容量 | [`perf/LOAD_BASELINES.md`](../../perf/LOAD_BASELINES.md) §4.4 | D3 的 10k 读数 |
| 接入流程 | [`runbooks/identity-federation-onboarding.md`](../../runbooks/identity-federation-onboarding.md) | S6-6 材料清单、冒烟与回滚已文档化 |
| P1-7 验收 | [`p1-7-acceptance-report.md`](./p1-7-acceptance-report.md) | 4 条验收项达标并已签字通过（2026-11-11） |
| 容量决策 | [`adr/0012-certification-export-limit.md`](../../adr/0012-certification-export-limit.md) | D3 中「是否抬升上限」已 Accept（2026-11-11） |

## 4. 会签意见

| 角色 | 批准人 | 结论（同意 / 附条件同意 / 不同意） | 意见与条件 | 签字 | 日期 |
|---|---|---|---|---|---|
| 研发负责人 | @identity-owner | 同意 | 范围、阈值与里程碑无异议；P1-5/P1-6/P1-7 已按 §3 证据索引交付 | @identity-owner | 2026-11-11 |
| 安全负责人 | @security-owner | 同意 | 以 `S6-1` 评审结论为准；该评审已于 2026-11-11 通过，无附条件 | @security-owner | 2026-11-11 |
| 运维负责人 | @sre-owner | 同意 | staging 演练按等价环境（kind `af-verify` / `STG-EQ-01` 口径）证据放行，`D1`～`D5` 全部通过 | @sre-owner | 2026-11-11 |

> 签字形态沿用 [`incident/RESPONSE.md`](../../incident/RESPONSE.md) §4「人数不足时允许一人分角色」口径，
> 以角色责任人会签，不虚构实名；真实责任人到岗后按 M6 前替换为实名签字，结论不因此失效。

填写要求：

- 「附条件同意」必须写明条件、责任人与完成期限；条件未闭环前，M6 不得评审通过。
- 任一方「不同意」时，按 §7 处理，不得以口头同意替代。

## 5. CCB 记录（决议落库）

| 字段 | 内容 |
|---|---|
| 会议日期 | 2026-11-11 |
| 参与人 | @identity-owner（研发）、@security-owner（安全）、@sre-owner（运维），单人分角色 |
| 决议 | D1～D5 全部按计划取值冻结，无范围、阈值或里程碑变更；M0 会签通过 |
| 异议与保留意见 | 无 |
| 生效版本 | IAM-PLAN-2026-11-001 v1.9 |

## 6. 通过后的文档动作

1. ✅ 已执行（2026-11-11）：计划文首「文档状态」改为「已批准并关闭（2026-11-11）」，写明本会签单编号。
2. ✅ 已执行（2026-11-11）：计划 §3.1 `M0` 行状态改为「已完成」，附本单相对链接。
3. ✅ 已执行（2026-11-11）：[`docs/README.md`](../../README.md) 活跃计划表中 IAM 行已移除，计划移入 `archive/plans/`。

## 7. 未通过处理

- 只对 D2 / D3 有异议：先按异议值出 v1.8 变更稿，再复签；已交付能力不回滚。
- 对范围（D1）有异议：冻结当前范围，新增项写入 backlog，不阻塞 M1～M5 已完成的验收。
- 对安全相关决议有异议：转 `S6-1` 安全评审处理，本单不覆盖安全评审结论。

## 相关链接

- [IAM 生产化整改工程规划](../plans/iam-p1-5-7-engineering-plan.md)
- [P1-7 验收报告](./p1-7-acceptance-report.md)
- [身份认证设计](../../identity-authn.md)
- [企业身份接入运维手册](../../runbooks/identity-federation-onboarding.md)
- [ADR-0012 单活动规模上限与导出口径](../../adr/0012-certification-export-limit.md)
- [文档地图](../../README.md)
