# 前端规范与门禁整改计划（Frontend Remediation Plan）

> **状态**：规划生效（v0.1，2026-11-21）；三项决策 D1/D2/D3 已由项目 Owner 冻结
> **定位**：把「前端无规范载体、无合并门禁、构建发布不可追溯」三类缺口中转成可执行、可验收、可回滚的任务包
> **效力**：本文件的任务范围、验收标准与门禁对合入具有约束力；关闭条目须回写 §八 状态表
> **适用范围**：`frontend/`、`docs/`、`.github/`、`.workflow/`、`deploy/`、`scripts/ci/`、`Makefile`
> **不适用范围**：后端业务域功能；`app-kernel` 重构按 §5.6 **单独立项**（本文只约束其立项与验收口径）
> **关联**：[`README.md`](./README.md)、[`ARCHITECTURE.md`](./ARCHITECTURE.md)、[`CI_STANDARDS.md`](./CI_STANDARDS.md)、[`TESTING_STANDARDS.md`](./TESTING_STANDARDS.md)、[`SECURITY_STANDARDS.md`](./SECURITY_STANDARDS.md)、[`DEPENDENCY_STANDARDS.md`](./DEPENDENCY_STANDARDS.md)、[`RELEASE_STANDARDS.md`](./RELEASE_STANDARDS.md)、[`GIT_STANDARDS.md`](./GIT_STANDARDS.md)、[`release/CHECKLIST.md`](./release/CHECKLIST.md)、[`archive/reviews/FRONTEND_INTEGRATION_REVIEW.md`](./archive/reviews/FRONTEND_INTEGRATION_REVIEW.md)、[`archive/reviews/FRONTEND_ARCHITECTURE_UX_REVIEW.md`](./archive/reviews/FRONTEND_ARCHITECTURE_UX_REVIEW.md)
> **执行方式**：每个 `FE-*` 任务包独立 MR；验收标准全绿 + 对应门禁可失败 + 文档回写完成，方可标记关闭；P0 关闭须附验收命令与输出
> **最后更新**：2026-11-21（新建；冻结 D1 本仓库构建发布、D2 CI 必跑 + 本地 `make frontend-check`、D3 内核单独立项）
> **变更方式**：调整范围、排期或验收标准须同步 §八 状态表，经架构 Owner Review

---

## 目录

1. [背景与基线](#一背景与基线)
2. [决策记录](#二决策记录)
3. [目标与成功判据](#三目标与成功判据)
4. [规范载体](#四规范载体)
5. [任务包](#五任务包)
6. [门禁接入设计](#六门禁接入设计)
7. [里程碑](#七里程碑)
8. [状态跟踪](#八状态跟踪)
9. [验收清单](#九验收清单)
10. [风险与回滚](#十风险与回滚)
11. [待决事项](#十一待决事项)
12. [附录：现状证据索引](#附录现状证据索引)

---

## 一、背景与基线

前端工具链是存在的（pnpm workspace、oxlint/eslint/stylelint、vitest、turbo、lefthook），问题不在「没有工具」，而在**没有任何一条前端检查进入裁决路径**，且两条自带检查永不失败。

| 维度 | 基线（2026-11-21 静态核实） | 证据 |
|------|------------------------------|------|
| 规范载体 | 17 篇 `*_STANDARDS.md` 无前端；`ARCHITECTURE.md` 明确不覆盖前端 | `docs/README.md` L1 表；`docs/ARCHITECTURE.md:134` |
| 合并门禁 | `make ci` 串联 40+ 检查、CI 12 个 job，均无前端；`ci.yml` 无 `paths` 过滤 | `Makefile` `ci:`；`.github/workflows/ci.yml` |
| 假绿检查 | `check:circular` / `check:dep` 发现问题后静默 `return`，退出码恒 0；`test:e2e` 无任何包定义 | `frontend/scripts/vsh/src/check-circular/index.ts`、`check-dep/index.ts`、`frontend/package.json` |
| 测试覆盖 | 单测全部在 `packages/` 与 `internal/`；`apps/` 为 0；无覆盖率门槛；无 E2E 依赖 | 磁盘枚举；`frontend/vitest.config.ts` |
| 类型门禁 | 27 个 `packages/**/package.json` 无 `typecheck`；`turbo run typecheck` 仅两 app | `frontend/turbo.json` |
| 契约一致性 | 前端契约类型手写；`make openapi-client` 产物未被前端消费；菜单白名单 6 条 vs 已 seed 13 个 page 菜单 | `frontend/apps/web-admin/src/api/iam/types.ts`；`internal/iam/menuview/catalog.go`；`migrations/000003_seed_iam_baseline.up.sql` |
| 双端内核 | 两 app 启动/请求/路由/适配层约 1300 行逐字节相同、约 340 行同构微差；已知行为漂移 ≥2 处（`web-biz` 丢 `await`；`web-biz` 无 `Idempotency-Key`） | `frontend/apps/web-admin/src/main.ts:34` vs `frontend/apps/web-biz/src/main.ts:34` |
| 安全出口 | 令牌存 Web Storage（SecureLS 仅是混淆）；无 CSP/安全头；锁屏密码持久化；上传预览不限扩展名；错误 `message` 直出 | 见 §附录 |
| 交付边界 | Dockerfile 只发 `web-admin`；`image.yml` 无前端镜像；`deploy/` 无前端 workload；产物只注入 package 版本、无 git SHA | `frontend/scripts/deploy/Dockerfile`；`.github/workflows/image.yml`；`frontend/internal/vite-config/src/plugins/inject-metadata.ts` |
| 计划跟踪 | 归档中两份前端评审有未闭环 GAP，此前无活跃计划承接 | `docs/archive/README.md` §四 |

> 本计划承接上述 GAP；归档文件保持只读，不在归档清单里改状态。

---

## 二、决策记录

### D1 前端在本仓库构建发布（2026-11-21）

- **决策**：CI 负责产出 `web-admin`、`web-biz` 两个前端镜像；镜像 tag 必须含 `git SHA`，发布清单记录 digest；Helm 以独立 workload 部署；发布与回滚步骤写入 `docs/release/CHECKLIST.md`，并做一次回滚演练。
- **理由**：产物必须能回指 commit 才能回滚；双端都要发布，不能只发 A 端；手工替换 `dist` 无法审计。
- **落地任务**：`FE-B-01`～`FE-B-06`。
- **禁止**：手工 `scp dist` 上线；无 SHA 的浮动 tag；用 `latest`。

### D2 CI 必跑 + 本地 `make frontend-check`，不并入 `make ci`

- **决策**：新增单一 `frontend` CI job 并设为分支保护必过项；本地提供 `make frontend-check`（同步骤）；**不**把前端加进 `make ci`。
- **理由**：`make ci` 是后端日常入口，`pnpm install` 依赖网络且耗时数分钟；CI-16 只要求「CI job ↔ 本地 make 入口」成对，不要求同一条链。
- **约束**：`check-ci-parity.sh` 登记 `frontend → frontend-check`；Gitee 流水线同步；`frontend` 必须进入 required checks（人工配置，与 `standards-roadmap.md` §四 的未闭环项合并处理）。
- **落地任务**：`FE-G-01`、`FE-G-08`、§六。

### D3 `app-kernel` 单独立项

- **决策**：公共内核下沉不做「顺手重构」，按独立项目立项：先出立项书 + ADR + 迁移清单，评审通过后再编码。
- **理由**：属结构性改造，影响两端全部页面；与规范/门禁补全不属于同一变更节奏。
- **落地任务**：§5.6 `FE-K-00`～`FE-K-03`；本文只约束验收口径，不承诺排期。

---

## 三、目标与成功判据

| 阶段 | 判据 | 目标 |
|------|------|------|
| **E1 门禁闭环** | 纯前端 PR 至少会红一次（lint / type / build 任一）；`pnpm check` 遇到依赖/循环问题退出码非 0；`frontend` 为 required check | M1 |
| **E2 交付闭环** | 两镜像可构建、可追溯（SHA）、可回滚；staging 完成一次发布 + 回滚演练 | M2 |
| **E3 质量闭环** | `apps/` 有冒烟用例与覆盖率门槛；27 个包纳入类型门禁；供应链扫描进入 CI | M3 |
| **E4 内核收敛** | 立项获批；迁移后漂移归零、双端回归通过 | M4（独立项目） |

---

## 四、规范载体

### 4.1 定位

新建 `docs/FRONTEND_STANDARDS.md` 作为**唯一**前端 L1 载体，编号 `FE-xx`（与后端 `S-xx` 不冲突）。`ARCHITECTURE.md:134` 的排除条款改为一行指针，不把前端内容塞回架构文。

### 4.2 章节与门禁映射

| 章节 | 内容 | 编号 | 门禁 |
|------|------|------|------|
| 1 | 适用范围、强制力、例外流程 | FE-00 / FE-EXC | Review |
| 2 | 分层、目录、依赖方向、`#/` 别名与跨层深导入 | FE-01～FE-05 | eslint（`custom-config.ts`）+ `check-frontend-arch.sh` |
| 3 | 请求层唯一出口、错误码映射、`trace_id`、分页与幂等头 | FE-10～FE-13 | `check:type` + 单测 |
| 4 | 路由、权限、菜单（`pageMap` 双向、服务端 `perm()` 兜底） | FE-14～FE-17 | `catalog_test` + 单测 |
| 5 | 状态与持久化（令牌/凭据禁令） | FE-SEC-04～06 | `check-frontend-security.sh` |
| 6 | 组件、样式、i18n、上传与预览 | FE-18～FE-20 | stylelint + 单测 |
| 7 | 安全（XSS 出口、CSP/安全头、错误提示、MFA 页） | FE-SEC-01～03 | eslint `vue/no-v-html` + 头检查脚本 |
| 8 | 测试分层与覆盖率 | FE-25～FE-28 | `frontend` job + `check-frontend-gates.sh` |
| 9 | 构建、环境变量、发布可追溯与回滚 | FE-21～FE-24 | `check-release` / `check-deploy` |
| 10 | 豁免（`FE-EXC-<id>`：理由 + owner + 到期日 + allowlist） | FE-EXC | `check-frontend-security.sh` |

**写法**：沿用 `CODE_STYLE.md` 的「规则 / 门禁 / 验收」三列表格；每条规则必须能对应到一条可失败的检查，否则不进 L1。

---

## 五、任务包

> 状态列在 §八 统一维护；本节的「验收」是关闭判据。

### 5.1 P0 门禁（FE-G）

| ID | 动作 | 落点 | 验收 |
|----|------|------|------|
| FE-G-01 | 新增单一 `frontend` CI job（lint + `check:dep` + `check:circular` + `check:type` + `test:unit` + `build:admin` + `build:biz`；`test:unit` 先按现有用例（85 文件 610 条）接入，apps 用例见 FE-T-01）；新增 `make frontend-check`；`check-ci-parity.sh` PARITY 表登记；Gitee 三个流水线加 `build@nodejs` step；`gitee-job.sh` 增 `frontend` 子命令 | `.github/workflows/ci.yml`、`Makefile`、`scripts/ci/check-ci-parity.sh`、`.workflow/`、`scripts/ci/gitee-job.sh` | ① 在 `.vue` 制造 lint 错误 → PR 红；② `main.ts` 制造类型错误 → PR 红；③ 删 PARITY 行 → `make check-ci-parity` 红；④ Gitee 侧存在同名 step |
| FE-G-02 | 修两条恒 0：`check-dep` / `check-circular` 改 `process.exitCode = 1`（或抛错），补退出码用例 | `frontend/scripts/vsh/src/{check-dep,check-circular}/index.ts` | 加未使用依赖 → 退出码 1；造 A→B→A 循环 → 退出码 1；干净树 → 0 |
| FE-G-03 | 两份 PR 模板加「前端自检」节；`check-git.sh` 断言该节存在 | `.github/PULL_REQUEST_TEMPLATE.md`、`.gitee/PULL_REQUEST_TEMPLATE.md`、`scripts/ci/check-git.sh` | 删任一模板的前端节 → `make check-git` 红 |
| FE-G-04 | `check:cspell` glob 覆盖 `**/*.vue`；CI 跑全量 lint/stylelint（非 staged） | `frontend/package.json`、CI job | `.vue` 模板拼写错误 → job 红；`.scss` 非法属性 → job 红 |
| FE-G-05 | 32 个包（`packages` 26 + `internal` 6）补 `typecheck` 脚本（含 `.vue` 用 `vue-tsc --noEmit`，纯 TS 用 `tsc --noEmit`），`turbo run typecheck` 覆盖全部包 | `frontend/packages/**/package.json`、`frontend/internal/**/package.json`、`frontend/turbo.json` | 在任一包引入类型错误 → `pnpm check:type` 红；全量 34 任务通过 |
| FE-G-06 | `apps/*` 冒烟用例（FE-T-01 首批）落在各自 `vitest.config.ts`（`#/` 别名隔离 + v8 覆盖率阈值 30%）；`pnpm test:unit` 链式执行根 + 两 app；新增 `check-frontend-gates.sh` 并作为 `frontend-check.sh` 第 9/10 步 | `frontend/apps/*/vitest.config.ts`、`frontend/apps/*/src/**/__tests__/*.test.ts`、`scripts/ci/check-frontend-gates.sh`、`scripts/ci/frontend-check.sh` | ✅ 2026-11-23：admin 38 条、biz 20 条通过；覆盖率 admin 89.47%/82.35%（语句/分支）、biz 93.5%/78.26%；阈值临时调 99% 实测 exit 1（Coverage ... does not meet global threshold），恢复 30% 后绿；`grep -rl "v-access\|pageMap"` 非空 |
| FE-G-07 | `engineStrict: true`（pnpm-workspace.yaml；`.npmrc` 的 `engine-strict` 对 pnpm 11 不生效）；dependabot 加 `npm`（`/frontend`）；CI 增加 `pnpm audit --prod --audit-level=high`（`--registry=https://registry.npmjs.org`，npmmirror 无 audit 端点）；`minimumReleaseAge` 恢复非 0 | `frontend/.npmrc`、`.github/dependabot.yml`、`frontend/pnpm-workspace.yaml`、`scripts/ci/frontend-check.sh` | ✅ 2026-11-23：`engineStrict: true` + Node 临时改 `>=99` 实测 `ERR_PNPM_UNSUPPORTED_ENGINE` exit 1；`minimumReleaseAge: 1440`；11 个 overrides 修 high/critical → audit exit 0（余 3 low/12 moderate）；dependabot npm `/frontend`；门禁第 2/10 步 |
| FE-G-08 | 索引收敛：`docs/README.md` 的 CI 编号、`ci.yml` 头注释、`check-ci-paths.sh` PATTERN 加 `frontend/`；`CI_STANDARDS.md` 增「前端 job」条文 | `docs/README.md`、`docs/CI_STANDARDS.md`、`scripts/ci/check-ci-paths.sh` | `make check-ci-paths` 能校验前端脚本引用；文档编号不再漂移 |

### 5.2 P0 构建发布（FE-B）

| ID | 动作 | 落点 | 验收 |
|----|------|------|------|
| FE-B-01 | 产物注入 `VITE_APP_GIT_SHA` / `VITE_APP_BUILD_TIME` / `VITE_APP_VERSION`，写入 `index.html` meta 与运行时全局 | `frontend/internal/vite-config/src/plugins/inject-metadata.ts` | 构建产物可 grep 到 40 位 SHA；页面「关于」可见版本 |
| FE-B-02 | Dockerfile 改 multi-target（`web-admin` / `web-biz`），钉死 Node/pnpm 版本，构建参数白名单校验，禁止任何密钥入 build arg | `frontend/scripts/deploy/Dockerfile` | `docker build --target web-biz` 成功；git SHA 标签正确 |
| FE-B-03 | `image.yml` 增加前端 matrix：两镜像构建推送、digest 写入发布清单 | `.github/workflows/image.yml` | Release 清单含两个前端镜像 digest；无 `latest` |
| FE-B-04 | Helm 增加前端 workload（Deployment/Service/Ingress + nginx ConfigMap），A/B 端独立入口 | `deploy/helm/` | `helm template` 渲染通过；kind 冒烟可访问两端 |
| FE-B-05 | 新增 `frontend/.env.example`（无密钥）与环境变量白名单；构建期检出未声明变量即失败 | `frontend/.env.example`、`frontend/internal/vite-config/src/utils/env.ts` | 干净 clone 可依模板构建；未声明变量导致构建失败 |
| FE-B-06 | 发布与回滚：`release/CHECKLIST.md` 前端节、`RELEASE_STANDARDS.md` 条文、回滚 runbook、staging 演练记录 | `docs/release/CHECKLIST.md`、`docs/RELEASE_STANDARDS.md`、`docs/runbooks/`、`docs/incident/drills/` | 演练记录含「发布 N → 回滚 N-1 → 页面可用」；回滚步骤可被他人照做 |

### 5.3 P0 安全（FE-S）

| ID | 动作 | 落点 | 验收 |
|----|------|------|------|
| FE-S-01 | XSS 出口管控：`vue/no-v-html = error`；现存 3 处 `v-html` 进 allowlist（带到期日）；新增 `check-frontend-security.sh` 扫 `v-html`/`innerHTML`/`eval(`/`new Function`/`window.open` 缺 `noopener` | eslint vue 配置、`scripts/ci/check-frontend-security.sh`、`docs/FRONTEND_STANDARDS.md` §7 | ✅ 2026-11-23：`vue/no-v-html: error` 生效（去掉 preview.vue 的 disable 即 eslint exit 1）；`check-frontend-security.sh` 扫 837 文件、5 命中全在 allowlist（4 条路径，均带到期日 2027-06-30）；注入 `v-html=` 即 exit 1；3 处 `window.open` 已补 `noopener,noreferrer`；`frontend-check.sh` 第 4/12 步 |
| FE-S-02 | 安全头 + CSP 先 `Report-Only`：`nosniff`、`X-Frame-Options: DENY`、`Referrer-Policy`、`Permissions-Policy`、HSTS（TLS 终止层）；处理内联脚本（`VITE_INJECT_APP_LOADING`） | `frontend/scripts/deploy/nginx.conf`、Helm Ingress 注记 | 进行中 2026-11-23：`nginx.conf` 下发 5 头 + CSP Report-Only、移除 `ACAO:*`；`check-frontend-headers.sh` 静态校验入第 5/12 步（删 `X-Frame-Options` 实测 exit 1）；⬜ `curl -sI` 实机取证与 staging 一周观察待部署 |
| FE-S-03 | 令牌/凭据条规：禁新增 Web Storage 持久化令牌；锁屏改「重新认证」或仅内存态；写明「SecureLS 是混淆不是加密」 | `docs/FRONTEND_STANDARDS.md` §6、`frontend/packages/stores/src/modules/access.ts`、`frontend/packages/stores/src/setup.ts` | ✅ 2026-11-24：`persist.pick` 移除 `lockScreenPassword`（抽为 `ACCESS_STORE_PERSIST_KEYS`），单测 3 例（加回键即 exit 1）；锁屏口令仅内存，刷新后提交走 `toLogin` 重新认证；SecureLS 注释写明混淆非加密、`VITE_*` 密钥进 bundle |
| FE-S-04 | 权限分工成文：前端权限仅展示控制，每个新增/变更接口必须服务端 `perm()` 兜底；补齐 `v-access` 的 `updated` 语义 | `docs/FRONTEND_STANDARDS.md` §4、`v-access` 指令实现、PR 模板 | Review checklist 含「服务端 perm」；`v-access` 有更新用例 |
| FE-S-05 | 上传/预览收紧：扩展名 + MIME 双白名单（SVG 默认关）；预览 `noopener,noreferrer`；非图片走下载 | `frontend/apps/*/src/adapter/component/index.ts` | 上传 `.html/.svg` 被拒；所有 `window.open` 带 `noopener` |
| FE-S-06 | 错误呈现：按 `code` 映射固定文案，禁止直出服务端 `message`；展示 `trace_id`；MFA 页 `no-store` + `frame-ancestors 'none'` | `frontend/apps/web-admin/src/api/request.ts`、MFA 页面、nginx | mock `{code:50000,message:"pq: ..."}` → UI 不显示原文；`curl -I` 见 `no-store` |
| FE-S-07 | 登录 `redirect` 白名单 + `decodeURIComponent` 容错；登出/401/429/204 行为用例 | `frontend/apps/web-admin/src/store/auth.ts`、`router/guard.ts`、单测 | 构造畸形 `redirect` 不白屏；越界地址被拒 |

### 5.4 P1 质量与契约（FE-T）

| ID | 动作 | 落点 | 验收 |
|----|------|------|------|
| FE-T-01 | 首批回归用例：已覆盖登录四态、redirect 回跳、登出清理、MFA 中间态不落令牌、幂等键注入/保留、错误消息映射、pageMap 与守卫 redirect、Web Storage 令牌禁令（`v-access`/`pageMap` 断言）；429/204/401 清令牌在 axios 拦截器层，随 FE-T-02 扩展 | `frontend/apps/web-admin/src/**/__tests__/*.test.ts`、`frontend/apps/web-biz/src/**/__tests__/*.test.ts` | ✅ 2026-11-23：`pnpm test:unit` 含 apps 用例通过（admin 38 + biz 20）；禁 `--passWithNoTests` |
| FE-T-02 | 菜单契约双向：`catalog_test.go` 反向校验（页面文件/seed → 白名单）；先修复 9 个未入白名单菜单（PATCH 400 风险）与 `/system/audit` 死链 | `internal/iam/menuview/catalog.go`、`catalog_test.go`、`migrations/` | 双向测试通过；`EnableMenu` 对不存在的 component 报错 |
| FE-T-03 | 覆盖率门槛落地：首版 ≥30%，半年内 ≥50%；阈值写入 `apps/*/vitest.config.ts`（v8，语句/分支/函数/行），`frontend-check.sh` 第 11/12 步校验 apps 用例存在 | `frontend/apps/*/vitest.config.ts`、`check-frontend-gates.sh`、CI | ✅ 2026-11-23：30% 生效；当前语句 89%/93%，可随时上调至 50% |
| FE-T-04 | E2E 放 nightly（不进 PR 必跑）：5 条冒烟（登录、菜单、权限按钮、分页、错误提示） | `.github/workflows/`、`frontend/e2e/` | nightly 有结果；`test:e2e` 不再空转 |

### 5.5 文档写回（FE-D）

| ID | 动作 | 落点 |
|----|------|------|
| FE-D-01 | 撰写 `FRONTEND_STANDARDS.md` v0.1（先落 §1/§2/§7/§9，其余随任务补） | `docs/FRONTEND_STANDARDS.md` |
| FE-D-02 | L1 表加行（待 FE-D-01 完成后）；§3.2 登记、§5 目录树、文首「最后整理」已完成（2026-11-21） | `docs/README.md` |
| FE-D-03 | `ARCHITECTURE.md:134` 改指针；`ARCHITECTURE.md` 摘要节挂「关联硬约束」 | `docs/ARCHITECTURE.md` |
| FE-D-04 | 路线图加 FE 行与状态（2026-11-21 已完成：FE 行 + 状态 + 「最后更新」+ §七 推进项） | `docs/standards-roadmap.md` |

### 5.6 单独立项：app-kernel（FE-K）

> 独立项目，需先立项评审；未获批前不进入编码排期。

| ID | 交付 | 验收 |
|----|------|------|
| FE-K-00 | 立项书 + ADR（`packages/effects/app-kernel` 依赖方向、对外 API、与 `@core` 边界） | 架构 Owner 批准；ADR 状态 Accepted |
| FE-K-01 | 迁移清单与逐文件基线（第一批约 17 个逐字节相同文件） | 清单可核对；每文件有归属与回归点 |
| FE-K-02 | 差异文件裁决（`api/core/auth.ts` 305 vs 101 行、`store/auth.ts` 180 vs 108 行） | A/B 端能力差异有明确取舍，不丢 MFA/改密/会话能力 |
| FE-K-03 | 执行与验收：漂移归零（`main.ts` await、幂等头、错误处理一致）、双端回归、迁移指南 | 已知漂移全部关闭；两端构建与冒烟通过 |

---

## 六、门禁接入设计

### 6.1 CI job

- job 名：`frontend`；`pnpm/action-setup@v4` 读取 `frontend/package.json` 的 `packageManager`（11.16.0），`setup-node@v4` 用 `node-version-file: frontend/.node-version` + pnpm 缓存；实际命令全部收敛到 `bash scripts/ci/frontend-check.sh`（脚本内 `cd frontend`）。
- 步骤与 `make frontend-check` **一一对应**（12 步）：`pnpm install --frozen-lockfile` → `pnpm audit --prod --audit-level=high`（npmjs registry，FE-G-07）→ `pnpm lint` → `bash scripts/ci/check-frontend-security.sh`（FE-S-01）→ `bash scripts/ci/check-frontend-headers.sh`（FE-S-02）→ `pnpm check:cspell` → `pnpm check:dep` → `pnpm check:circular` → `pnpm check:type` → `pnpm test:unit`（根 610 + web-admin 38 + web-biz 20）→ `bash scripts/ci/check-frontend-gates.sh` → `pnpm build:admin` → `pnpm build:biz`。
- **不新增 `paths` 过滤**：required check 被 skip 会产生「跳过是否算通过」的歧义（CI-17 语义），先保持全量触发；时长用缓存与 `--frozen-lockfile` 控制。

### 6.2 本地入口

```text
make frontend-check   # 与 CI job 同步骤；只在前端改动时由开发者主动执行
```

`check-ci-parity.sh` 的 PARITY 表登记 `frontend → frontend-check`；不修改 `make ci` 的依赖链。

### 6.3 分支保护

`frontend` 加入 required checks（GitHub / Gitee 双平台，需仓库管理员），与 `standards-roadmap.md` §四 的人工待办合并完成。

---

## 七、里程碑

| 里程碑 | 时间盒 | 内容 | 出口条件 |
|--------|--------|------|----------|
| **M0** | 第 1 周 | 本计划评审；`FRONTEND_STANDARDS.md` 骨架（FE-D-01 前四章）；`frontend` job 骨架（先只跑 lint/type） | 计划获 Review；job 能红能绿 |
| **M1** | 第 2～3 周 | FE-G-01～04、FE-G-08、FE-S-01 | 纯前端 PR 至少一类错误被拦；恒 0 检查可失败；PR 模板生效 |
| **M2** | 第 4～6 周 | FE-B-01～06、FE-S-02 | 两镜像可发；SHA 可追溯；staging 发布 + 回滚演练通过 |
| **M3** | 第 7～10 周 | FE-G-05～07、FE-S-03～07、FE-T-01～03 | 包类型门禁、apps 用例、覆盖率、供应链扫描生效 |
| **M4** | 第 90 天后 | FE-K 立项执行、FE-T-04 | 内核立项获批；E2E nightly 有结果 |

---

## 八、状态跟踪

| ID | 负责人 | 状态 | 关闭判据 / 备注 |
|----|--------|------|------------------|
| FE-G-01 | 待指派 | 进行中 | 2026-11-21：ci.yml `frontend` job、`make frontend-check`、PARITY 登记、Gitee `build@nodejs` step、gitee-job 子命令已落地；待 CI 首跑与 lint 红灯取证 |
| FE-G-02 | 待指派 | 已完成 | 2026-11-23：`hasUnusedDependencies` / `exceedsThreshold` + 退出码已改，单测已加；本地 `frontend-check` 实测：存量未用依赖/循环依赖时 exit 1，清理后 exit 0 |
| FE-G-03 | 待指派 | 已完成 | 2026-11-23：两份 PR 模板新增「前端自检」节；`check-git.sh` 断言标题与 `make frontend-check` 命令（改标题实测 exit 1，恢复后 0） |
| FE-G-04 | 待指派 | 已完成 | 2026-11-23：`check:cspell` 增加 `**/*.vue`（943 文件 0 问题）；`frontend-check.sh` 增加 `check:cspell` 步（第 6/12），lint/stylelint 本就全量非 staged；`.vue` 注入 `zzqqxx` 实测 exit 1 |
| FE-G-05 | 待指派 | 已完成 | 2026-11-23：32 包（26 packages + 6 internal）补 `typecheck`；`pnpm check:type` 34/34 通过（约 1m8s）；修掉暴露的 21 处既有类型红（typings 声明路径、RouteMeta augmentation、Vite/Rollup 插件类型、测试非空断言等）；红灯实测：`packages/utils` 注入 `const __redLightProbe: string = 1` → turbo 退出 2（TS2322/TS6133），撤销后 FULL TURBO 0 |
| FE-G-06 | 待指派 | 已完成 | 2026-11-23：admin 38 / biz 20 条冒烟用例；两 app 独立 `vitest.config.ts`（`#/` 别名 + 覆盖率阈值 30%）；`test:unit` 链式跑三次；`check-frontend-gates.sh` 纳入第 11/12 步；阈值 99% 实测红、恢复后绿 |
| FE-G-07 | 待指派 | 已完成 | 2026-11-23：`engineStrict: true` 生效（Node `>=99` 实测 exit 1）、`minimumReleaseAge: 1440`、11 个 overrides 修复 33 high + 2 critical → audit 0 high（余 3 low/12 moderate）、dependabot npm `/frontend`、`frontend-check.sh` 第 2/12 步 |
| FE-G-08 | 待指派 | 已完成 | 2026-11-23：`docs/README.md`、根 `README.md`、`ci.yml` 头注释统一 CI-01～CI-18；`CI_STANDARDS.md` CI-18 与 §3.1 映射；`check-ci-paths.sh` PATTERN 含 `frontend` 并扩到 ts/tsx/vue/js/mjs/cjs（151 refs）；`ARCHITECTURE.md`、`rbac-iam.md` 旧编号更正；`make check-ci-paths` exit 0，注入 `frontend/__nonexistent__/probe.ts` 实测 make exit 2 |
| FE-B-01 | 待指派 | 未开始 | SHA 注入 |
| FE-B-02 | 待指派 | 未开始 | multi-target 镜像 |
| FE-B-03 | 待指派 | 未开始 | 前端 matrix + digest |
| FE-B-04 | 待指派 | 未开始 | Helm 前端 workload |
| FE-B-05 | 待指派 | 未开始 | env 模板 + 白名单 |
| FE-B-06 | 待指派 | 未开始 | 发布/回滚演练 |
| FE-S-01 | 待指派 | 已完成 | 2026-11-23：出口管控 + 豁免；`check-frontend-security.sh`（第 4/12 步）、`vue/no-v-html=error`、allowlist 带到期日 |
| FE-S-02 | 待指派 | 进行中 | 2026-11-23：静态件完成（5 头 + CSP Report-Only，`ACAO:*` 移除，第 5/12 步红灯 exit 1）；⬜ 实机 `curl -sI` 与 staging 观察待部署 |
| FE-S-03 | 待指派 | 已完成 | 2026-11-24：`ACCESS_STORE_PERSIST_KEYS` 去 `lockScreenPassword`、单测 3 例、锁屏刷新走重新认证、SecureLS 注释 |
| FE-S-04 | 待指派 | 未开始 | 权限分工 |
| FE-S-05 | 待指派 | 未开始 | 上传/预览 |
| FE-S-06 | 待指派 | 未开始 | 错误呈现 |
| FE-S-07 | 待指派 | 未开始 | redirect 白名单 |
| FE-T-01 | 待指派 | 已完成 | 2026-11-23：首批 8 类场景已覆盖（admin 38 + biz 20）；429/204/401 随拦截器扩展 |
| FE-T-02 | 待指派 | 未开始 | 菜单双向 |
| FE-T-03 | 待指派 | 已完成 | 2026-11-23：30% 阈值生效（语句 89%/93%），半年后上调 50% |
| FE-T-04 | 待指派 | 未开始 | E2E nightly |
| FE-D-01 | 待指派 | 进行中 | v0.1 草案已出（`docs/FRONTEND_STANDARDS.md`，2026-11-21）；§11 已回写本轮现状；待安全脚本、覆盖率门槛、CI 首跑红灯后转正 |
| FE-D-02 | 待指派 | 进行中 | §3.2 + §5 + 文首已完成；L1 行待 FE-D-01 |
| FE-D-03 | 待指派 | 未开始 | ARCHITECTURE 指针 |
| FE-D-04 | 待指派 | 已完成 | 2026-11-21：路线图 FE 行 + 状态 + §七 推进项 |
| FE-K-00 | 待指派 | 未开始 | 立项书 + ADR |
| FE-K-01 | 待指派 | 未开始 | 迁移清单 |
| FE-K-02 | 待指派 | 未开始 | 差异裁决 |
| FE-K-03 | 待指派 | 未开始 | 漂移归零验收 |

---

## 九、验收清单

- [ ] `frontend` job 存在且为 required check；`make frontend-check` 可复现同一套步骤（FE-G-01）
- [x] `check:dep` / `check:circular` 遇到问题返回非 0（FE-G-02）
- [x] 两份 PR 模板有「前端自检」节，且 `check-git` 能断言（FE-G-03）
- [x] cspell/stylelint 覆盖 `.vue`，CI 跑全量（FE-G-04）
- [x] 32 个包有 `typecheck`，`turbo run typecheck` 全量 34 任务通过（FE-G-05）
- [x] `apps/*` 有冒烟用例，覆盖率阈值生效（FE-G-06 / FE-T-01 / FE-T-03；2026-11-23）
- [x] `engineStrict`、dependabot npm、`pnpm audit` 入 CI（FE-G-07）
- [x] 文档索引收敛（FE-G-08 / FE-D-02～04；2026-11-23）
- [ ] 产物含 git SHA；双端镜像可构建（FE-B-01～03）
- [ ] Helm 前端 workload 渲染通过，kind 冒烟两端可访问（FE-B-04）
- [ ] `.env.example` 存在且无密钥；未声明变量构建失败（FE-B-05）
- [ ] staging 完成一次发布 + 回滚演练（FE-B-06）
- [x] `vue/no-v-html = error`，allowlist 均带到期日（FE-S-01）
- [ ] 安全头 5 项生效，CSP 先 Report-Only（FE-S-02；静态件已绿，待 `curl -sI` 实机）
- [x] 锁屏密码不再持久化（FE-S-03；单测 `packages/stores/src/__tests__/access.test.ts`）
- [ ] 权限分工、上传、错误呈现、redirect 白名单均有条文与用例（FE-S-04～07）
- [ ] 菜单白名单与 seed 双向一致，`/system/audit` 死链关闭（FE-T-02）
- [ ] `FRONTEND_STANDARDS.md` 进入 L1 表（FE-D-01/02）
- [ ] `app-kernel` 立项获批（FE-K-00）

---

## 十、风险与回滚

| ID | 风险 | 触发 | 影响 | 处置 |
|----|------|------|------|------|
| R-01 | `pnpm install` 拖慢 CI | 首次接入 | PR 等待变长 | pnpm store 缓存 + `--frozen-lockfile`；先量化 install 与 build 时长再调 |
| R-02 | 接入 lint 即全红（92 处深导入 vs `custom-config.ts` 禁令） | FE-G-01 直接跑全量 lint | 阻塞所有前端 MR | 先做例外裁决（FE-D-01 §2 写明允许的深导入矩阵），再开启规则 |
| R-03 | `pnpm test:unit` 当前状态未实测 | 决定是否首版纳入 job | 可能误红或误绿 | 已消解（2026-11-23）：根 610 + 两 app 58 条实测通过；仍禁 `--passWithNoTests` |
| R-04 | 构建期变量泄密 | 把 secret 塞进 `VITE_*` | 密钥进 bundle | FE-B-05 白名单 + FE-S-03 条规 + CI 扫描 |
| R-05 | 双端镜像维护成本 | 两套 Dockerfile | 漂移 | 单 Dockerfile multi-target + 单一 nginx 模板 |
| R-06 | B 端首次生产发布 | 无历史 | 回滚无参照 | 先 staging 发 B 端 + 冒烟，再定生产节奏 |
| R-07 | Helm 前端 workload 与现网 Ingress 冲突 | 新增模板 | 路由冲突 | 单独评审 chart values + 域名/路径清单 |
| R-08 | 回滚演练失败 | 配置/镜像不可逆 | 故障时长扩大 | 保留上一版 digest；演练记录进 `incident/drills/` |

---

## 十一、待决事项

1. **Node 基线**：`engines` 允许 22.18 / 24.12，需定一个作为 `frontend/.node-version`（建议 24.x LTS）。
2. **web-biz 生产入口**：域名或路径前缀未定，影响 FE-B-04 的 Ingress 与 CSP `connect-src`。
3. **SBOM / 镜像签名**：是否复用后端镜像的供应链流程（需运维确认现有覆盖范围）。
4. **`test:unit` 首版是否纳入**：取决于 R-03 的实测结果。
5. **`frontend` job 是否也跑 `pnpm build` 的 `web-biz`**：建议跑（build-only 错误只有构建能发现），代价是时长。

---

## 附录：现状证据索引

| 事实 | 位置 |
|------|------|
| `make ci` 无前端、CI 无前端 job | `Makefile` `ci:`；`.github/workflows/ci.yml` |
| `check:circular` 恒 0 | `frontend/scripts/vsh/src/check-circular/index.ts`（发现循环仅 `console.warn` 后 `return`） |
| `check:dep` 恒 0 | `frontend/scripts/vsh/src/check-dep/index.ts`（`knip` exitCode 1 后 `return`） |
| `test:e2e` 空转 | `frontend/package.json`（`turbo run test:e2e`，无包定义该任务） |
| apps 零测试 | `frontend/apps/**/*.test.ts` 枚举为空 |
| 包无类型门禁 | 27 个 `frontend/packages/**/package.json` 无 `typecheck`；`frontend/turbo.json` |
| 深导入冲突 | `frontend/internal/lint-configs/eslint-config/src/custom-config.ts`（禁用 `#/api/*` 等） |
| 菜单白名单 6 vs seed 13 | `internal/iam/menuview/catalog.go`；`migrations/000003_seed_iam_baseline.up.sql` |
| 菜单未命中静默 404 | `frontend/packages/utils/src/helpers/generate-routes-backend.ts` |
| 契约类型手写 | `frontend/apps/web-admin/src/api/iam/types.ts`；`api/configcenter/types.ts` |
| 令牌 Web Storage | `frontend/apps/web-admin/src/setup.ts`；`frontend/packages/stores/src/modules/access.ts` |
| 锁屏密码持久化 | `frontend/packages/stores/src/modules/access.ts`（`lockScreenPassword`） |
| 无安全头 | `frontend/scripts/deploy/nginx.conf`（且含 `Access-Control-Allow-Origin: *`） |
| 上传/预览 | `frontend/apps/*/src/adapter/component/index.ts` |
| 错误直出 | `frontend/apps/web-admin/src/api/request.ts` |
| 只发 A 端 | `frontend/scripts/deploy/Dockerfile`（只 COPY `apps/web-admin/dist`） |
| 无前端镜像 | `.github/workflows/image.yml` |
| 产物无 SHA | `frontend/internal/vite-config/src/plugins/inject-metadata.ts` |
| 本地钩子未生效 | `.git/hooks` 仅 `prepare-commit-msg`；`frontend/lefthook.yml` |
| 归档 GAP 无跟踪 | `docs/archive/README.md` §四 |
