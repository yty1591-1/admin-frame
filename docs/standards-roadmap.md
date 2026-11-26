# 工程硬约束专文路线图

> **目的**：列出本仓库企业级 / 生产级**硬约束专文**的优先级与落地状态。  
> **已落地**：注释（C）、风格（S）、CI、SEC、API、TEST、DEP、CFG、DATA、REL、OBS、GIT、RELS、RB、AUD、[LIC](./LICENSE_STANDARDS.md)、[GEN](./CODEGEN_STANDARDS.md)、[PERF](./PERFORMANCE_STANDARDS.md)。  
> **效力目标**：专文生效（Normative）后，与架构文档同等；违反则 MR **不得合并**。  
> **状态约定**：`待做` / `进行中` / `已完成` / `暂缓`。  
> **最后更新**：2026-11-21（补前端 FE 专文行：v0.1 草案已出且 `frontend` 门禁已接入（`make frontend-check` / CI / Gitee），待首跑取证；路线图表内其余专文仍为已完成）

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
| P0 | `.github/workflows/ci.yml` + `.workflow/`（Gitee Go） | CI | **流水线本身** | 已完成 | GitHub Actions + Gitee Go；分支保护需管理员勾选 | §18.3 |
| P0 | `docs/CI_STANDARDS.md` | CI | CI 必跑步骤、失败策略、多平台 | 已完成 | `make ci` | §18.3 |
| P1 | [docs/FRONTEND_STANDARDS.md](./FRONTEND_STANDARDS.md) | FE | 前端分层/请求/路由权限/安全/测试门禁（v0.1 草案，2026-11-21） | 草案 v0.1（门禁已接入，待 CI 首跑取证） | `frontend` CI job / Gitee step / `make frontend-check` + eslint/stylelint | — |
| P1 | `docs/SECURITY_STANDARDS.md` | SEC | 密钥、注入、三端鉴权隔离等 | 已完成 | gitleaks、gosec、调试端点 | §十 |
| P1 | `docs/API_STANDARDS.md` | API | 分端 OpenAPI、错误码、分页 | 已完成 | oasdiff | §十六 |
| P1 | `docs/TESTING_STANDARDS.md` | TEST | 分层测试、覆盖率、B/C 必测 | 已完成 | `make test-ci` | §十三 |
| P2 | `docs/DEPENDENCY_STANDARDS.md` | DEP | go.sum、govulncheck、replace | 已完成 | `check-deps` + Dependabot | §8.5 |
| P2 | `docs/CONFIG_STANDARDS.md` | CFG | 配置优先级、Validate | 已完成 | `check-no-static-config` | §十七 |
| P2 | `docs/DATA_STANDARDS.md` | DATA | 迁移可回滚、禁手改生产库 | 已完成 | `check-migrations` | §十四 |
| P2 | `docs/RELIABILITY_STANDARDS.md` | REL | 超时、重试、限流、幂等 | 已完成 | `check-reliability` | §十一 |
| P2 | `docs/OBSERVABILITY_STANDARDS.md` | OBS | 日志字段、探针、审计挂接 | 已完成 | `check-observability` | §十二 |
| P3 | `docs/GIT_STANDARDS.md` | GIT | 分支、Commit/MR、禁直推 | 已完成 | `check-git` + 分支保护（人工） | §十九 |
| P3 | `docs/RELEASE_STANDARDS.md` | RELS | 镜像 digest、禁 latest、回滚 | 已完成 | `check-release` | §十八 |
| P3 | `docs/RUNBOOK_STANDARDS.md` | RB | 告警挂 runbook、值班升级 | 已完成 | `check-runbooks` | runbooks/ |
| P3 | `docs/AUDIT_STANDARDS.md` | AUD | 审计事件、字段、保留期 | 已完成 | `check-audit` | §十二审计 |
| P4 | `docs/LICENSE_STANDARDS.md` | LIC | 允许/禁止许可证、引入审批 | 已完成 | `check-license` + DEP-05 | §8.5 |
| P4 | `docs/CODEGEN_STANDARDS.md` | GEN | 生成物策略、禁手改 | 已完成 | `check-codegen` + MANIFEST | 工具链 |
| P4 | `docs/PERFORMANCE_STANDARDS.md` | PERF | 关键路径微基准登记（默认无全链路压测） | 已完成 | `check-perf` + BASELINES | §十一容量 |

**说明**：`CI` 流水线文件本身不是「专文」，但列为 P0，否则专文无法落地为硬门禁。

---

## 三、专文内容提纲（落地时按此裁剪）

1. 文首元数据（Normative、效力、范围、门禁、变更方式）
2. 目标（3 条以内）
3. **硬约束一览表**（ID / 约束 / 门禁）
4. 分层或分端细则
5. MUST NOT
6. 例外与豁免
7. Review / CI 检查清单
8. 与工具链、`ARCHITECTURE.md` 的对应关系

---

## 四、各专文要点清单

### P0 — CI

- [x] 双平台流水线 + `CI_STANDARDS`
- [ ] 分支保护：GitHub / Gitee 勾选必过流水线（**需仓库管理员人工完成**）

### P1～P4

- [x] 表内全部专文已撰写并接入门禁（PERF 为微基准登记制，全链路压测仍可选）

---

## 五、与已有文档的关系

| 文档 | 角色 |
|------|------|
| `README.md` | 文档地图：分层、门禁对照、新增与归档判据 |
| `ARCHITECTURE.md` | 总规范 + 关联硬约束链接 |
| 各 `*_STANDARDS.md` | 合入硬约束正文 |
| `deps/*` | 许可证 / replace / NOTICE |
| `docs/codegen/` / `docs/perf/` / `docs/audit/` / `docs/runbooks/` / `docs/release/` | 专文附属清单 |
| `docs/archive/` | 已闭环过程文档（批次台账 / 完成报告 / 修复日志 / 一次性评审），**只读、无约束力** |
| `standards-roadmap.md`（本文） | 优先级与状态总表 |

---

## 六、落地约定

1. 能自动则自动；例外须原因与审批。  
2. 状态回写与「最后更新」同步。  
3. 新专文须同 MR 更新 `make ci` / `gitee-job.sh arch` / PR 模板。

---

## 七、下一迭代建议

1. 管理员完成 **分支保护**（唯一未闭环的人工项）。  
2. 业务路径接线 `audit.Emitter`；按需启用 mockery/sqlc 并更新 MANIFEST。  
3. 发版流水线接入镜像扫描（补强 RELS）。  
4. 前端规范与门禁按 [`frontend-remediation-plan.md`](./frontend-remediation-plan.md) 推进（`FE-G` / `FE-B` / `FE-S` / `FE-T`）。
