# 工程化 Git 协作规范（硬约束）

> **状态**：生效中（Normative）  
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**  
> **适用范围**：本仓库分支策略、提交信息、合并请求与主干保护  
> **关联**：架构 §十九；CI 见 `CI_STANDARDS.md`；发布见 `RELEASE_STANDARDS.md`  
> **执行方式**：分支保护（人工）+ PR 模板 + `check-git.sh` + Review  
> **最后更新**：2026-10-13  
> **变更方式**：修改本规范须同步更新 PR 模板、`commitlint.config.cjs`、`scripts/ci/check-git.sh` 与架构 §十九，经架构 Owner Review

---

## 1. 目标

1. **主干只进审查过的变更**：禁止直推 `main`/`master`。  
2. **历史可检索**：提交信息结构化，便于 changelog 与回溯。  
3. **合入可核对**：MR 必须走模板自检，与硬约束专文对齐。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| GIT-01 | 默认保护分支为 `main`（或组织约定的 `master`）；**禁止**无 MR 直推；**禁止**无审批 force-push | 平台分支保护（人工一次配置） |
| GIT-02 | 功能变更经 Merge Request / Pull Request 合入；至少 1 名非作者 Approve（Owner 可另定更高） | 分支保护规则 |
| GIT-03 | CI 必跑 job 全部通过后方可合并（见 `CI_STANDARDS`） | 分支保护 status checks |
| GIT-04 | 分支命名：`feature/*`、`fix/*`、`hotfix/*`、`chore/*`、`docs/*`、`dependabot/*`（或平台机器人等价） | Review |
| GIT-05 | Commit 遵循 [Conventional Commits](https://www.conventionalcommits.org/)：`<type>(optional-scope): <description>` | `commitlint.config.cjs` + Review |
| GIT-06 | 仓库必须提供 GitHub 与 Gitee（若启用）PR 模板，且含架构相关自检区（含「前端自检」节） | `check-git.sh` |
| GIT-07 | MR 标题建议同 Conventional Commits；正文勾选适用自检；禁止空描述合入 | Review + 模板 |
| GIT-08 | 合并请求须勾选 Git 自检（本专文） | PR 模板 |

---

## 3. 分支策略

```text
main (保护)
  ↑ MR
feature/order-idempotency
fix/c-list-pagination
hotfix/auth-nil-panic
```

| 分支 | 用途 | 生命周期 |
|------|------|----------|
| `main` | 唯一长期主干；始终可发布 | 永久 |
| `feature/*` | 功能 | MR 合入后删除 |
| `fix/*` | 缺陷 | 同上 |
| `hotfix/*` | 生产热修 | 同上；可走加急 Review |
| `chore/*` / `docs/*` | 工程/文档 | 同上 |

- 不维护长期 `develop`（除非 ADR 另定）。  
- 禁止个人长期分支无 MR 堆积超过 **14 天** 无更新（建议；超期应 rebase 或关闭）。

---

## 4. Conventional Commits

允许 `type`（与 `commitlint.config.cjs` 一致）：

`feat` | `fix` | `docs` | `style` | `refactor` | `perf` | `test` | `build` | `ci` | `chore` | `revert`

示例：

```text
feat(order): enforce Idempotency-Key on c create
fix(biz): scope list query by merchant_id
docs(git): add GIT_STANDARDS
ci(arch): include DATA/REL/OBS gates
```

- `BREAKING CHANGE:` 页脚或 `feat!:` / `fix!:` 表示破坏性变更，须同步 API major（见 API-07）。  
- 禁止无意义信息：`update`、`fix`、`wip`（单独作为完整 description）。

本地（可选，需 Node）：

```bash
npx commitlint --from origin/main --to HEAD
```

---

## 5. MR / PR 要求

1. 使用仓库模板（`.github/PULL_REQUEST_TEMPLATE.md` / `.gitee/PULL_REQUEST_TEMPLATE.md`）。  
2. **影响端**、相关专文自检勾选完整。  
3. 迁移 / 契约 / 依赖等变更在正文写明风险与回滚。  
4. 截图/日志仅用于说明，**禁止**含密钥与生产 PII。

---

## 6. 主干保护（管理员 Checklist）

### GitHub

- [ ] Protect `main`：Require PR；Require approvals ≥ 1  
- [ ] Require status checks：本仓库 CI 全部必跑 job  
- [ ] Restrict force push / deletion  
- [ ] （建议）Require conversation resolution  

### Gitee

- [ ] 保护默认分支；禁止直推  
- [ ] 合并前要求关联流水线通过  
- [ ] 限制管理员绕过；应急须事后补 MR 说明  

未完成前，本专文 GIT-01～03 **视为未闭环**（与 CI-01 相同）。

---

## 7. MUST NOT

- 直推 `main`/`master`（应急绕过须 24h 内补流程与说明）。  
- Force-push 保护分支。  
- 用空 MR /「略」作为唯一描述合入业务变更。  
- 在提交信息或 MR 中粘贴密钥、Token、生产连接串。  

---

## 8. 本地与 CI

```bash
make check-git    # GIT-06 等可自动项
make ci
```

| 平台 | 入口 |
|------|------|
| GitHub / Gitee | job `arch` 内跑 `check-git.sh` |
| MR | PR 模板「Git 自检」 |

---

## 9. 例外

1. **机器人合入**（Dependabot 等）：可豁免人工 Approve 若组织策略允许，但仍须 CI 全绿。  
2. **紧急热修**：Owner 可临时放宽审批人数，24h 内补全 Review 记录。  
3. **历史 squashed 提交**：rebase 后允许改写仅属于本 MR 的未推送 commits。

---

## 10. Review 清单

- [ ] 分支名与类型匹配；目标为 `main`  
- [ ] Commit / MR 标题符合 Conventional Commits  
- [ ] 模板自检已勾选；无密钥泄露  
- [ ] `make check-git` 通过  

---

## 11. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| `.github/PULL_REQUEST_TEMPLATE.md` | GitHub MR 模板 |
| `.gitee/PULL_REQUEST_TEMPLATE.md` | Gitee MR 模板 |
| `commitlint.config.cjs` | Commit type 白名单 |
| `scripts/ci/check-git.sh` | GIT-06 |
| 平台分支保护 | GIT-01～03 |
| 本文件 | 规范正文 |
