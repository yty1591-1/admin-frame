# 工程硬约束专文路线图

> **目的**：列出本仓库尚缺的企业级 / 生产级**硬约束专文**，按优先级逐步撰写并接入门禁。  
> **已落地**：注释（C）、风格（S）、CI、[SEC](./SECURITY_STANDARDS.md)、[API](./API_STANDARDS.md)、[TEST](./TESTING_STANDARDS.md)、[DEP](./DEPENDENCY_STANDARDS.md)、[CFG](./CONFIG_STANDARDS.md)、[DATA](./DATA_STANDARDS.md)、[REL](./RELIABILITY_STANDARDS.md)、[OBS](./OBSERVABILITY_STANDARDS.md)、[GIT](./GIT_STANDARDS.md)、[RELS](./RELEASE_STANDARDS.md)、[RB](./RUNBOOK_STANDARDS.md)、[AUD](./AUDIT_STANDARDS.md)。  
> **效力目标**：专文生效（Normative）后，与架构文档同等；违反则 MR **不得合并**。  
> **状态约定**：`待做` / `进行中` / `已完成` / `暂缓`。  
> **最后更新**：2026-10-13（AUD P3 已落地；P0～P3 专文全部完成）

---

## 一、什么叫「硬约束专文」

一份合格的硬约束专文应同时具备：

1. **独立 md**：如 `docs/SECURITY_STANDARDS.md`，含编号条目（如 `SEC-01`）。
2. **架构挂接**：`ARCHITECTURE.md` 摘要小节 + 文首「关联硬约束」链接。
3. **可执行门禁**：lint / CI job / 脚本 / Review Checklist 至少一种能**阻断合并**。
4. **例外流程**：`nolint` / 豁免须写原因与审批方式。

仅写在 `ARCHITECTURE.md` 长文中的「要求」，在未专文化 + 未门禁前，**不算本路线图的已完成项**。

---

## 二、实现优先级（建议顺序）

| 优先级 | 专文路径（建议） | ID 前缀 | 说明 | 状态 | 主要门禁 | 架构对照 |
|-------|------------------|---------|------|------|----------|----------|
| P0 | `.github/workflows/ci.yml` + `.workflow/`（Gitee Go） | CI | **流水线本身**：无 CI 则后续专文无法自动强制 | 已完成 | GitHub Actions + Gitee Go；分支保护需管理员勾选 | §18.3 |
| P0 | `docs/CI_STANDARDS.md` | CI | CI 必跑步骤、失败策略、多平台、缓存、权限最小化 | 已完成 | 与双平台流水线同批；`make ci` | §18.3 |
| P1 | `docs/SECURITY_STANDARDS.md` | SEC | 密钥、注入、三端鉴权隔离、PII/日志脱敏、调试端点 | 已完成 | gitleaks、gosec、调试端点检查、MR 模板 | §十 |
| P1 | `docs/API_STANDARDS.md` | API | 分端 OpenAPI、错误码、分页、兼容与 breaking | 已完成 | oasdiff validate / breaking；`make openapi` | §十六 |
| P1 | `docs/TESTING_STANDARDS.md` | TEST | 分层测试、覆盖率、B 隔离/C 越权必测 | 已完成 | `make test-ci` + `check-test-gates.sh` | §十三 |
| P2 | `docs/DEPENDENCY_STANDARDS.md` | DEP | go.sum、govulncheck、禁随意 replace、许可证 | 已完成 | `make vuln` + `check-deps.sh` + Dependabot | §8.5 |
| P2 | `docs/CONFIG_STANDARDS.md` | CFG | 配置优先级、Validate、example vs 密钥、多环境 | 已完成 | `check-config.sh` + Validate 单测 + gitleaks | §十七 |
| P2 | `docs/DATA_STANDARDS.md` | DATA | 迁移可回滚、禁手改生产库、备份与保留 | 已完成 | `check-migrations.sh` + Review | §十四 |
| P2 | `docs/RELIABILITY_STANDARDS.md` | REL | 超时、重试、限流、幂等、降级可观测 | 已完成 | `check-reliability.sh` + Review | §十一 |
| P2 | `docs/OBSERVABILITY_STANDARDS.md` | OBS | 日志字段、指标、追踪、健康检查、审计 | 已完成 | `check-observability.sh` + Review | §十二 |
| P3 | `docs/GIT_STANDARDS.md` | GIT | 分支策略、Commit/MR 模板、禁直推 main | 已完成 | `check-git.sh` + 分支保护（人工） | §十九 |
| P3 | `docs/RELEASE_STANDARDS.md` | RELS | 镜像 digest、禁 latest、发布/回滚、版本暴露 | 已完成 | `check-release.sh` + Checklist | §十八 |
| P3 | `docs/RUNBOOK_STANDARDS.md` | RB | 告警必须有 runbook、值班与升级路径 | 已完成 | `check-runbooks.sh` + ALERTS.md | runbooks/ |
| P3 | `docs/AUDIT_STANDARDS.md` | AUD | A/B 审计事件、字段、保留期、不可篡改要求 | 已完成 | `check-audit.sh` + audit 包单测 | §十二审计 |
| P4 | `docs/LICENSE_STANDARDS.md` | LIC | 允许/禁止许可证、引入依赖审批 | 待做 | 与 DEP-05 对齐深化 | §8.5 |
| P4 | `docs/CODEGEN_STANDARDS.md` | GEN | OpenAPI/sqlc/mockery 生成物提交策略、禁手改 | 待做 | CI diff 检查 | 工具链 |
| P4 | `docs/PERFORMANCE_STANDARDS.md` | PERF | 关键路径基线、回归门槛（可选） | 暂缓 | benchmark / 压测门禁 | §十一容量 |

**说明**：`CI` 流水线文件本身不是「专文」，但列为 P0，否则专文无法落地为硬门禁。

---

## 三、专文内容提纲（落地时按此裁剪）

每份专文建议结构（与注释/风格专文对齐）：

1. 文首元数据（Normative、效力、范围、门禁、变更方式）
2. 目标（3 条以内）
3. **硬约束一览表**（ID / 约束 / 门禁）
4. 分层或分端细则（A/B/C 差异写清）
5. MUST NOT 禁止项
6. 例外与豁免流程
7. Review / CI 检查清单
8. 与工具链、`ARCHITECTURE.md` 的对应关系

---

## 四、各专文要点清单（实现时勾选）

### P0 — CI

- [x] `docs/CI_STANDARDS.md` + 双平台流水线
- [ ] 分支保护：GitHub / Gitee 勾选必过流水线（**需仓库管理员人工完成**）

### P1 / P2 / P3

- [x] SEC / API / TEST / DEP / CFG / DATA / REL / OBS / GIT / RELS / RB / **AUD**

### P4 — LIC / GEN / PERF

- [ ] **LIC**：许可证允许列表（与 DEP-05 深化对齐组织策略）
- [ ] **GEN**：生成代码目录约定与「禁止手改」检测
- [ ] **PERF**：按需启用，避免过早门禁

---

## 五、与已有文档的关系

| 文档 | 角色 |
|------|------|
| `ARCHITECTURE.md` | 总规范；专文生效后在文首与对应章节挂接链接 |
| 已完成硬约束专文 | C / S / CI / SEC / API / TEST / DEP / CFG / DATA / REL / OBS / GIT / RELS / RB / AUD |
| `docs/audit/` | 审计事件目录与字段字典（受 AUD 约束） |
| `pkg-roadmap.md` | 能力实现路线图（非合入硬约束） |
| `standards-roadmap.md`（本文） | **硬约束专文**待办与优先级 |
| `docs/adr/` | 选型/架构决策；硬约束变更若影响架构须补 ADR |
| `docs/runbooks/` | 运维处置；受 `RUNBOOK_STANDARDS` 约束 |
| `docs/release/` | 发版 Checklist；受 `RELEASE_STANDARDS` 约束 |

---

## 六、落地约定

1. **先专文后门禁**：可先合入 `待做→进行中` 的 md 草案，同迭代或下一迭代接 CI。
2. **一专文一 ID 前缀**：避免与 C/S 编号冲突。
3. **能自动则自动**：凡可用工具检查的条目，不得仅依赖口头 Review。
4. **状态回写**：专文合并并挂接架构后，将本表「状态」改为 `已完成`，并更新「最后更新」日期。
5. **最小切片推荐**：P4 按需；优先完成分支保护人工勾选。

---

## 七、下一迭代建议（最小切片）

1. 管理员完成 GitHub / Gitee **分支保护**（GIT-01～03 / CI-01）。
2. 业务路径接线 `audit.Emitter`，将 EVENTS 中对应行改为 `active`。
3. 按需起草 P4：`LICENSE_STANDARDS` 或 `CODEGEN_STANDARDS`。
