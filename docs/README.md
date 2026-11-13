# docs 文档地图

> **本文件是 `docs/` 的唯一入口。** 新增、移动、归档文档前请先读「四、文档新增与归档判据」。
> **最后整理**：2026-11-13（[config-center-plan.md](./config-center-plan.md) 升至 v0.4：M1 范围收敛为「仅全量发布」，百分比 / 租户 / 渠道 / 实例灰度整体降级 P1，服务端对非 `all` 策略返回 `40000`；同期修复 `000031` 迁移 MySQL 保留字缺陷并补齐仓储集成测试与管理面列表覆盖率）；前次 2026-11-12（[config-center-plan.md](./config-center-plan.md) 升至 v0.3 并关闭 8 个 M0 前置条件：ADR-0013 / ADR-0014 转 Accepted，CFG-04 收敛为「配置中心 > 代码默认值」，取消引导配置清单与导入清单，新增首次启动默认值初始化与密钥规则生成，完成权限点 / 审计事件等契约冻结；可进入 M1 编码）；前次 2026-11-11（IAM 计划 v1.9 关闭并归档：M0 会签通过、S6-1 安全评审转「通过」、P1-7 双评审签字、M6 生产准入通过；staging 相关项按 `STG-EQ-01` 等价环境证据放行；ADR-0012 转 Accepted；四份记录移入 `archive/reviews/` 与 `archive/plans/`；`docs/archive/` 合计 35 份）；前次 2026-11-10（S6-1 安全评审完成，IAM 计划升 v1.8；`identity-slo.md` 补 M2M / SCIM 的 SLO、容量与压测口径；补录 `identity/`、`iam/` 记录类文档与 `perf/LOAD_BASELINES.md`）；2026-11-09（归档 `production-readiness-gaps.md` 至 `archive/platform/`，`docs/archive/` 合计 31 份）

---

## 一、四类文档，效力不同

| 层 | 是什么 | 效力 | 变更方式 |
|----|--------|------|----------|
| **L1 硬约束** | `*_STANDARDS.md`、`CODE_STYLE.md`、`ARCHITECTURE.md`、`adr/`、`runbooks/` 等 | **Normative**。违反则 MR 不得合并，多数由 `make ci` 自动阻断 | 改条文须同步改门禁脚本 + Reviewer 评审 |
| **L2 设计说明** | 领域/模块的设计与选型（identity、IAM、pkg、存储、迁移等） | **实现依据**。与代码冲突时以代码为准，须尽快回写文档 | 领域模型、契约、表结构变更时同步更新 |
| **L3 活跃计划** | 尚未关闭的整改计划与差距清单 | **排期依据**。任务范围与验收标准对合入有约束力 | 关闭条目须回写状态表 |
| **L4 归档** | 已闭环的批次报告、台账、修复日志、一次性评审 | **无约束力，仅追溯用**（见 [`archive/README.md`](./archive/README.md)） | 只读，不再维护 |

判断口诀：**能阻断合并的是 L1；写「怎么设计」的是 L2；写「还差什么、什么时候做」的是 L3；写「当时怎么做的」的是 L4。**

---

## 二、L1 硬约束（门禁生效）

| 文档 | 编号 | 主要门禁 |
|------|------|----------|
| [ARCHITECTURE.md](./ARCHITECTURE.md) | §1–§20 | `check-layering` / `check-transport` / 架构 Review |
| [CODE_STYLE.md](./CODE_STYLE.md) | S-01～S-12 | `make fmt-check` + golangci-lint |
| [COMMENT_STANDARDS.md](./COMMENT_STANDARDS.md) | C-01～C-10 | golangci-lint（godot / revive） |
| [CI_STANDARDS.md](./CI_STANDARDS.md) | CI-01～CI-15 | `make ci` |
| [SECURITY_STANDARDS.md](./SECURITY_STANDARDS.md) | SEC-01～SEC-16 | `make secrets` / `make lint` / `check-debug` |
| [API_STANDARDS.md](./API_STANDARDS.md) | API-01～API-12 | `check-openapi`（oasdiff） |
| [TESTING_STANDARDS.md](./TESTING_STANDARDS.md) | TEST-01～TEST-08 | `make test-ci` / `check-test-gates` |
| [DEPENDENCY_STANDARDS.md](./DEPENDENCY_STANDARDS.md) | DEP-01～DEP-08 | `check-deps` / `make vuln` |
| [CONFIG_STANDARDS.md](./CONFIG_STANDARDS.md) | CFG-01～CFG-12 | `check-no-static-config` / `check-configcenter` |
| [DATA_STANDARDS.md](./DATA_STANDARDS.md) | DATA-01～DATA-08 | `check-migrations` |
| [RELIABILITY_STANDARDS.md](./RELIABILITY_STANDARDS.md) | REL-01～REL-08 | `check-reliability` / `check-idempotency` / `check-outbox` |
| [OBSERVABILITY_STANDARDS.md](./OBSERVABILITY_STANDARDS.md) | OBS-01～OBS-08 | `check-observability` |
| [GIT_STANDARDS.md](./GIT_STANDARDS.md) | GIT-01～GIT-08 | `check-git` + 分支保护 |
| [RELEASE_STANDARDS.md](./RELEASE_STANDARDS.md) | RELS-01～RELS-08 | `check-release` / `check-deploy` |
| [RUNBOOK_STANDARDS.md](./RUNBOOK_STANDARDS.md) | RB-01～RB-07 | `check-runbooks` / `check-alerts` |
| [AUDIT_STANDARDS.md](./AUDIT_STANDARDS.md) | AUD-01～AUD-09 | `check-audit` / `check-authn` |
| [LICENSE_STANDARDS.md](./LICENSE_STANDARDS.md) | LIC-01～LIC-07 | `check-license` |
| [CODEGEN_STANDARDS.md](./CODEGEN_STANDARDS.md) | GEN-01～GEN-07 | `check-codegen` |
| [PERFORMANCE_STANDARDS.md](./PERFORMANCE_STANDARDS.md) | PERF-01～PERF-07 | `check-perf` |
| [standards-roadmap.md](./standards-roadmap.md) | — | 专文路线图与状态总表（P0～P4 已完成） |

**附属清单**（被门禁脚本直接读取，改动需同步脚本）：

- [adr/](./adr/) — 架构决策记录（ADR-0001～0014）
- [runbooks/](./runbooks/) — 值班手册（`ALERTS.md` 与 `deploy/observability` 告警规则双向校验）
- [incident/](./incident/) — 事故定级 / 响应 / 复盘 / 演练（RB-08，`check-oncall` OC-02/OC-04）
- [oncall/ROTATION.md](./oncall/ROTATION.md) — 排班、交接与呼叫链路验证（RB-09，`check-oncall` OC-05/OC-06）
- [audit/EVENTS.md](./audit/EVENTS.md) / [audit/FIELDS.md](./audit/FIELDS.md) — 审计事件与字段字典
- [codegen/MANIFEST.md](./codegen/MANIFEST.md) — 生成物清单
- [perf/BASELINES.md](./perf/BASELINES.md) — 性能基线与微基准登记
- [perf/LOAD_BASELINES.md](./perf/LOAD_BASELINES.md) — 全链路压测基线登记（本机 / staging 实测水位，与微基准互补）
- [release/CHECKLIST.md](./release/CHECKLIST.md) — 发布检查清单
- [release/supply-chain.md](./release/supply-chain.md) — SBOM / 镜像签名 / 部署前验签与验证边界
- [iam/PERMISSIONS.md](./iam/PERMISSIONS.md) — 权限点目录（与 `internal/iam/catalog` 由单测强一致）

---

## 三、L2 设计说明 / L3 活跃计划

### 3.1 设计说明（实现依据）

| 文档 | 覆盖范围 |
|------|----------|
| [identity-authn.md](./identity-authn.md) | 账号域、凭据、会话、令牌签发与撤销、A 端 MFA、三端认证契约；SCIM 2.0 与 M2M（§21，已实现、默认关闭） |
| [rbac-iam.md](./rbac-iam.md) | IAM 域：权限模型、审批、破玻璃、Outbox、B 端自助 |
| [identity-slo.md](./identity-slo.md) | 身份域 SLO、容量公式与压测方法 |
| [notification-channels.md](./notification-channels.md) | 短信 / 邮件 / 推送渠道契约与驱动 |
| [storage-s3-oss.md](./storage-s3-oss.md) | 对象存储多云驱动选型 |
| [migration-guide.md](./migration-guide.md) | `pkg/migrate` / `cmd/migrate` 使用与故障处置 |
| [data-inventory.md](./data-inventory.md) | 数据资产清单、PII 分类分级与删除边界（ENT-15 登记） |
| [tenant-data-lifecycle.md](./tenant-data-lifecycle.md) | 租户数据保留期、导出窗口与硬删除边界 |
| [deploy-kind-verify.md](./deploy-kind-verify.md) | 用 kind 验证 Helm Chart：渲染 → 部署 → 冒烟 → 回滚 → 迁移失败阻断 |
| [pkg-roadmap.md](./pkg-roadmap.md) | `pkg/` 公共能力包规划与落地状态 |
| [identity/threat-model.md](./identity/threat-model.md) | 身份域威胁建模：四通道 STRIDE、残留风险 `RR-01`～`RR-06`、评审记录 |
| [identity/idp-config-templates.md](./identity/idp-config-templates.md) | 企业 IdP 配置模板（Keycloak 26.x 基线 + 八项验收清单） |

### 3.2 活跃计划（有未关闭条目）

| 文档 | 状态 | 未关闭项 |
|------|------|----------|
| [production-readiness-implementation-plan.md](./production-readiness-implementation-plan.md) | 规划生效 v0.1 | 见 §十二 状态表：`ENT-01`～`ENT-19` 已关闭（`ENT-10`/`ENT-11` 主体完成，遗留为渠道凭据等环境配置项）；`ENT-20`～`ENT-24` 待做（触发条件未满足，现状证据已并入 §六），`ENT-25` 暂缓 |
| [config-center-plan.md](./config-center-plan.md) | 已通过 v0.4（L2 设计 + L3 计划） | 8 个 M0 前置条件已满足；M1 编码进行中（表 / 领域 / 管理面 / SDK / 双读 / 观测 / 门禁已落地，前端 4 页与演练待完成）；**M1 发布能力收敛为仅全量**，灰度整体 P1（§10.5）；§10.2 `Q1`～`Q14` 中 M2 专项待决 |

> 当前有两份「有未关闭事项」的计划类文档：生产就绪实施计划与配置中心规划。IAM P1-5/6/7 计划已于 2026-11-11 关闭并移入 [`archive/plans/`](./archive/plans/)；其余整改计划（platform、IAM 权限模型、identity 各批次、通知中心、特性开关、租户域 B1～B4、平台级差距评估）均已闭环，位于 [`archive/`](./archive/)。
>
> **输入依据（已归档）**：
> - [iam-audit-report-2026-11-05.md](./archive/iam/iam-audit-report-2026-11-05.md) 是 2026-11-05 的一次性 IAM 审计，结论已被 [iam-p1-5-7-engineering-plan.md](./archive/plans/iam-p1-5-7-engineering-plan.md) 引用；它不构成排期，已于 2026-11-08 移入 `archive/iam/`。
> - [production-readiness-gaps.md](./archive/platform/production-readiness-gaps.md) 是 2026-10-22 的平台能力差距评估（v0.2，基线 `dad8fc0`），各 `ENT` 状态与排期已由实施计划 §十二 承接；它不构成排期，已于 2026-11-09 移入 `archive/platform/`。

### 3.3 评审与验收记录（已全部闭环，2026-11-11 归档）

原 4 份待签 / 待评审记录已全部闭环并按判据归档，结论已沉淀到 L1 / L2：

| 文档 | 最终结论 | 归档位置 |
|------|----------|----------|
| [m0-signoff-record-2026-11-10.md](./archive/reviews/m0-signoff-record-2026-11-10.md) | 已签：三位批准人均「同意」，无附条件 | `archive/reviews/` |
| [s6-1-security-review-record-2026-11-10.md](./archive/reviews/s6-1-security-review-record-2026-11-10.md) | 通过：`C1`～`C3` 全部闭环（`C1` 按 `STG-EQ-01` 等价环境放行） | `archive/reviews/` |
| [p1-7-acceptance-report.md](./archive/reviews/p1-7-acceptance-report.md) | 已签字通过：安全 / 平台双评审，4 条验收项达标 | `archive/reviews/` |
| [staging-2026-11-10-identity-drill.md](./incident/drills/staging-2026-11-10-identity-drill.md) | 已执行并通过：`D1`～`D5` 全通过（kind 等价环境口径） | 留 `incident/drills/`（实测记录，与 `2026-11-08-*` 同列） |

> 无未闭环的评审 / 会签记录；新增记录按 §四 判据落位。

---

## 四、文档新增与归档判据

新增文档前先回答三个问题，按答案落位：

1. **它能否阻断合并？** 能 → `docs/<域>_STANDARDS.md`，同时补 `scripts/ci/check-*.sh` 与 `make ci`，并回写 [standards-roadmap.md](./standards-roadmap.md)。
2. **它描述「系统现在应该是什么样」？** 是 → L2 设计说明，放 `docs/` 根目录（领域设计）或 `docs/adr/`（不可逆决策）。
3. **它只记录「某段时间做了什么」？** 是 → 直接写进 `docs/archive/`，不要放根目录。

**归档条件**（三条同时满足才移入 `docs/archive/`）：

- 事项已闭环（批次报告、台账、fix log、执行报告、已实施计划）；
- 结论已沉淀到 L1 或 L2（否则先回写，再归档）；
- 不再作为排期或验收依据（仍有未关闭条目的计划**不得归档**）。

**命名约定**：

- 硬约束：`<域>_STANDARDS.md` 或 `CODE_STYLE.md`；
- 设计说明：`<领域>-<主题>.md`（如 `identity-authn.md`）；
- 过程产物：`<领域>-<批次>-<类型>.md`（`register` 台账 / `completion-report` 完成报告 / `fix-log` 修复日志），归档后一律进 `archive/<领域>/`。

---

## 五、目录结构

```text
docs/
├── README.md                  # 本文件：文档地图与判据
├── ARCHITECTURE.md            # 架构总纲（L1）
├── *_STANDARDS.md             # 17 篇硬约束专文（L1）
├── CODE_STYLE.md              # 代码风格（L1）
├── standards-roadmap.md       # 专文路线图与状态总表（L1）
├── identity-authn.md          # 身份域设计（L2）
├── rbac-iam.md                # IAM 域设计（L2）
├── identity-slo.md            # SLO 与容量（L2）
├── notification-channels.md   # 通知渠道（L2）
├── storage-s3-oss.md          # 对象存储（L2）
├── migration-guide.md         # 迁移使用指南（L2）
├── deploy-kind-verify.md      # 用 kind 验证 Helm Chart：部署 / 冒烟 / 回滚（L2）
├── data-inventory.md          # 数据资产清单与 PII 分类分级（L2）
├── tenant-data-lifecycle.md   # 租户数据生命周期：保留 / 导出 / 硬删除边界（L2）
├── pkg-roadmap.md             # pkg 路线图（L2）
├── production-readiness-implementation-plan.md # 平台能力实施计划（L3）
├── config-center-plan.md      # 配置中心规划与评审（L2 设计 + L3 计划）
├── adr/                       # 架构决策记录（L1）
├── runbooks/                  # 值班手册（L1）
├── incident/                  # 事故定级 / 响应 / 复盘 / 演练（L1）
├── oncall/                    # 值班排班与交接（L1）
├── audit/ codegen/ perf/ release/ iam/   # 硬约束附属清单（L1）
├── identity/                  # 身份域威胁建模、IdP 配置模板（L2）
└── archive/                   # 已闭环过程文档（L4，只读）
    ├── README.md
    ├── identity/              # identity 差距评审 → 批次台账 → 完成报告全链路
    ├── platform/              # platform 整改计划、IAM 权限模型改造清单、平台能力差距评估
    ├── reviews/               # 架构评审，以及 M0 会签单 / S6-1 评审记录 / P1-7 验收报告
    ├── verify/                # 一次性验证与演练记录（ENT-04 / ENT-06）
    ├── plans/                 # 已实施领域计划（通知中心、特性开关、IAM P1-5/6/7）
    ├── tenant/                # 租户域差距评审与 B1～B4 分批计划（ENT-09）
    └── iam/                   # IAM 一次性代码审计（P1-5/6/7 输入依据）
```

---

## 六、维护约定

1. **一处真相**：同一结论只在一个文档里写全，其余位置用链接，避免多处维护后互相矛盾。
2. **状态字段**：L1/L2 文首必须有 `状态`、`最后更新`、`变更方式`；状态变更与内容变更同一次提交。
3. **链接用相对路径**：`./xxx.md` 或 `../xxx.md`，禁止写绝对路径；移动 / 归档文档后跑 `make check-doclinks`（校验 `docs/` 与 `README.md` 全部相对链接，未并入 `make ci`）。
4. **归档不删文件**：用 `git mv` 保留历史，不直接 `rm`。
5. **归档文档内指向代码的链接可能失效**：历史文档引用的是当时的文件路径（如已被合并的迁移），属预期现象；`make check-doclinks` 只保证当前链接可达，不追溯历史事实。
