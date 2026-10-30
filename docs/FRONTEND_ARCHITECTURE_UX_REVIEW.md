# 前端架构 / 交互 / 视觉 评审

> 评审对象：`frontend/apps/web-admin`（A 端，端口 5668）、`frontend/apps/web-biz`（B 端，端口 5777）
> 基准代码：`ee515aa`（工作区存在未提交改动，见 §8）
> 评审方法：源码通读 + 代码量实测 + 页面实机渲染（真实启动 dev server 后走查登录、角色、IAM 各页，覆盖 1440×900 / 窄屏、亮色 / 暗色）
> 状态：结论待确认；§6 为可逐步执行的优化路线，每项均带验收标准
> 关联：`docs/FRONTEND_INTEGRATION_REVIEW.md`（契约对接）、`docs/ARCHITECTURE.md`、`docs/identity-authn.md`、`docs/rbac-iam.md`

---

## 0. 一页结论

| 维度 | 评分 | 结论 |
|---|---|---|
| 架构设计 | 7.5 / 10 | 分层干净、请求层与认证状态机设计成熟；但两端业务代码复制、`packages/business` 空置、组件库抽象名不副实 |
| 人机交互 | 7.0 / 10 | 认证四态与审批拦截是超出同类项目的设计；表单校验、操作密度、加载反馈拖后腿 |
| 视觉观感 | 7.0 / 10 | 设计令牌体系完整、暗色质量高；品牌化未完成、假数据穿帮、页面节奏扁平 |

四条总体判断：

1. **内核很强，业务层刚起步**。全仓 85,713 行前端代码中，Vben 内核占 74.7%，自研业务仅 11.3%。评价必须分内核与二次开发两层，否则会把内核的优点算成自建成果。
2. **架构承诺只兑现了一半**。`accessMode: backend` 动态路由、`v-access:code` 按钮级鉴权、双 requestClient 分离都做对了；但"组件库可替换""业务层可复用"两个承诺停留在目录和注释层面。
3. **交互亮点集中在认证链路**。登录四态状态机、429 `Retry-After` 倒计时、票据失效强制回退、40900 转审批单——这些是生产级设计；而列表页与表单页仍停留在"能跑"的水平。
4. **视觉问题高度集中在"没改干净"**。登录页仍展示 Vben 模板 slogan、静态资源走公网 CDN、安全设置页全是硬编码假数据。这些不是设计能力问题，是收尾问题，改动成本极低但观感收益最大。

---

## 1. 代码规模基线（实测）

复现命令（PowerShell，排除 `node_modules` / `dist` / `.turbo` / `.nitro`）：

```powershell
$roots = @('apps\web-admin\src','apps\web-biz\src','apps\backend-mock\api','apps\backend-mock\utils','packages','internal','scripts','docs')
$total=0
foreach ($r in $roots) {
  $f = Get-ChildItem $r -Recurse -File -Include *.ts,*.vue,*.tsx,*.mts |
       Where-Object { $_.FullName -notmatch '\\node_modules\\|\\dist\\|\\.turbo\\|\\.nitro\\' }
  $lines = (Get-Content $f.FullName | Measure-Object -Line).Lines
  "$r : $($f.Count) files / $lines lines"
  $total += $lines
}
"TOTAL: $total"
```

| 目录 | 文件数 | 行数 | 占比 |
|---|---:|---:|---:|
| `packages`（内核） | 799 | 63,999 | 74.7% |
| `apps/web-admin/src` | 65 | 7,605 | 8.9% |
| `docs`（VitePress） | 82 | 4,986 | 5.8% |
| `internal`（lint / ts / vite 配置） | 74 | 4,419 | 5.2% |
| `apps/web-biz/src` | 34 | 2,060 | 2.4% |
| `apps/backend-mock` | 42 | 1,683 | 2.0% |
| `scripts` | 12 | 961 | 1.1% |
| **合计** | **1,108** | **85,713** | 100% |

包级分布：

| 包 | 文件数 | 备注 |
|---|---:|---|
| `@core` | 500 | 内核主战场（shadcn-ui / ui-kit / design） |
| `effects` | 256 | access / request / layouts / plugins |
| `utils` | 14 | |
| `stores` | 13 | |
| `icons` / `locales` / `types` / `constants` / `styles` / `preferences` | 1–5 | 基本是壳 |

页面规模 Top 10（`web-admin/src/views`，共 28 个 `.vue`）：

| 文件 | 行数 |
|---|---:|
| `system/role/index.vue` | 621 |
| `iam/template/index.vue` | 503 |
| `iam/change/index.vue` | 388 |
| `iam/sod/index.vue` | 361 |
| `iam/binding/index.vue` | 326 |
| `iam/breakglass/index.vue` | 299 |
| `system/menu/index.vue` | 284 |
| `_core/profile/mfa-setting.vue` | 266 |
| `_core/authentication/login.vue` | 207 |
| `iam/components/form-modal.vue` | 162 |

结论：单文件密度健康（均值约 270 行），仅 `system/role/index.vue` 达到需要拆分的水位线。

---

## 2. 发现索引

判定标准：

- **P0**：影响产品可交付（对外可见的品牌 / 假数据 / 不可用）。
- **P1**：可用但结果错误或关键流程体验断裂。
- **P2**：体验与一致性债，不阻断主流程。
- **P3**：工程洁净度与长期可维护性。

| ID | 级别 | 维度 | 主题 | 证据定位 |
|---|---|---|---|---|
| FEREV-01 | P0 | 视觉 | 登录页仍是 Vben 模板文案 | `packages/locales/src/langs/zh-CN/authentication.json:3-4` |
| FEREV-02 | P1 | 视觉 | logo / 默认头像走公网 CDN | `packages/@core/preferences/src/config.ts:19,70`、`@core/base/shared/src/constants/vben.ts:15` |
| FEREV-03 | P1 | 视觉 | 安全设置页硬编码假数据 | `views/_core/profile/security-setting.vue:20` |
| FEREV-04 | P1 | 架构 | 两端认证实现分叉，B 端未接后端 | `apps/web-biz/src/api/core/auth.ts`、`store/auth.ts:36` |
| FEREV-05 | P1 | 架构 | `packages/business/*` 声明为空，共享业务层缺位 | `pnpm-workspace.yaml` |
| FEREV-06 | P1 | 架构 | B 端 `accessMode: backend` 但 mock 无菜单端点 | `apps/web-biz/src/preferences.ts:27`、`backend-mock/api` |
| FEREV-07 | P1 | 交互 | 操作列 5 个同权 link 按钮平铺 | `system/role/index.vue:97,537-585` |
| FEREV-08 | P1 | 交互 | 停用原因仅 placeholder 提示，无条件必填 | `system/role/index.vue:196-201` |
| FEREV-09 | P1 | 交互 | 删除审计原因硬编码 `'页面删除'` | `system/role/index.vue:458`、`iam/sod/index.vue:206`、`iam/template/index.vue:365` |
| FEREV-10 | P1 | 交互 | 列表渲染 `:key="index"` | `iam/components/data-scope-editor.vue:52` |
| FEREV-11 | P2 | 架构 | `adapter` 未收口，页面直连 `ant-design-vue` | `system/role/index.vue` 顶部 import |
| FEREV-12 | P2 | 架构 | 两套表单体系并存 | `iam/components/form-modal.vue` vs `useVbenForm` |
| FEREV-13 | P2 | 架构 | 密码策略仅一处实现，自助改密页缺失校验 | `password-change.vue:29-40` vs `password-setting.vue:25-55` |
| FEREV-14 | P2 | 架构 | `bootstrap` 初始化顺序为隐式约定 | `apps/web-admin/src/main.ts` |
| FEREV-15 | P2 | 交互 | 必填校验非内联、不聚焦 | `iam/components/form-modal.vue:63-75` |
| FEREV-16 | P2 | 交互 | 打开弹窗前拉详情无 loading | `system/role/index.vue:306,451` |
| FEREV-17 | P2 | 交互 | 密码表单未消费 `cooldown` | `login.vue:139-143`、`login.vue:220-234` |
| FEREV-18 | P2 | 交互 | 表格无骨架屏 / 无空态引导 | 各列表页 |
| FEREV-19 | P2 | 视觉 | 两端视觉人格不统一 | 宽窄屏对比 |
| FEREV-20 | P2 | 视觉 | 页面节奏扁平，缺少区段层级 | `Page` 组件 + 各页模板 |
| FEREV-21 | P3 | 视觉 | 圆角语言不统一（24px vs 6–8px） | 登录页右上角胶囊 vs 表单 |
| FEREV-22 | P3 | 架构 | `system/role/index.vue` 621 行待拆 | 见 §1 |

---

## 3. 架构设计

### 3.1 已经做对的六件事（建议保持，不要在下轮优化中破坏）

1. **apps 足够薄**。业务全部落在 `views`，内核在 `packages`，`apps` 不含框架级逻辑。这是可升级的前提。
2. `@core` **与** `effects` **边界正确**。`@core/base`、`@core/ui-kit` 无业务、无副作用；`effects/{access,request,layouts}` 才允许产生副作用。依赖方向无倒挂。
3. **双 request client 分离**。业务请求走 `requestClient`（信封解析 + 401 自动刷新），登录 / 改密 / 登出 / MFA 确认走 `baseRequestClient`（裸 axios，禁止自动刷新）。这避免了"输错旧密码触发刷新 → 用户被登出"的经典事故。
4. **认证状态机收敛在 api 层**。`LoginOutcome` 四态 + `parseLoginPayload`，store 只负责落库与跳转，页面只负责切换步骤。三层无职责串味。
5. **权限模型闭环**。`accessMode: 'backend'` → 后端 `/iam/me/menus` 下发菜单 → 动态路由；按钮级用 `v-access:code`（如 `iam:role:grant`）。菜单、路由、按钮、接口四处权限点能对上。
6. **工程化到位**。turbo + pnpm catalog 锁版本、changeset、oxlint / oxfmt、lefthook、vitest，`check:circular` / `check:dep` / `check:type` 齐全，lint 配置已包化成 workspace 包。

### 3.2 结构性问题

#### FEREV-04 两端认证实现分叉

- **现状**：`web-admin` 已按真实契约重写（`subject` 入参、四态响应、双令牌、一次性轮换）；`web-biz` 仍是 Nitro mock 版本（`accessToken` 单令牌、`/auth/codes`、`/user/info`）。
- **两侧页面清单差异**（B 端 `src` 实测 34 文件）：

  | 类别 | web-admin | web-biz |
  |---|---|---|
  | 四态认证页 | `login` / `mfa-verify` / 强制改密 | **仅 `login`**，无 MFA、无强制改密 |
  | profile | `mfa-setting` / `password-setting` / `security-setting` … | 仅 `base` / `password` / `security` / `notification`，**无 `mfa-setting`** |
  | 业务页 | `system/*`、`iam/*` 共 15 页 | 仅 `member/list` 1 页 |

  即 B 端不仅请求层是 mock，**页面层也没有承接后端的 MFA / 强制改密能力**。
- **影响**：B 端不可联调；任一处认证缺陷要修两遍，且两套实现会持续漂移。
- **建议**：抽取 `packages/business/auth`，导出 `LoginOutcome`、`loginApi`、`refreshTokenApi`、`createAuthStore` 工厂，两端只注入 `baseURL`、品牌信息与菜单数据源。
- **验收**：`grep -r "loginApi" apps/*/src` 仅剩一处定义；两端登录流程走同一份测试用例。

#### FEREV-05 `packages/business` 子包目录空置

- **现状**：`pnpm-workspace.yaml` 声明了 `packages/business` 的子包通配，但该目录下无任何包。
- **影响**：本该承载共享认证 / 请求 / 布局的位置缺位，FEREV-04 无落点。
- **建议**：先落 `packages/business/auth` 作为样板，再按需增 `request`、`layout`。宁少勿多，一个包跑通后再复制模式。
- **验收**：`pnpm list -r --depth -1` 能看到 `@vben/business-auth`，且被两个 app 依赖。

#### FEREV-06 B 端 `accessMode: backend` 与 mock 能力不匹配

- **现状**：`web-biz/src/preferences.ts:27` 与 A 端一致设为 `backend`，但 `apps/backend-mock/api` 下只有 `menu/all.ts`，没有 `/iam/me/menus`。
- **实测现象**：`POST /api/auth/login`、`GET /api/user/info`、`GET /api/auth/codes` 均成功，随后 `GET /api/iam/me/menus` 返回 mock 首页 HTML，路由生成失败，页面停留在 `/auth/login`。
- **建议**：二选一——(a) mock 补 `/iam/me/menus` 并返回与权限码一致的菜单树；(b) B 端临时改 `accessMode: frontend`，待后端就绪再切回。**不要保留"配置写 backend、数据源却是 mock"的中间态**。
- **验收**：`pnpm dev:biz` 后能以任意 mock 账号进入首页，侧边栏菜单非空。

#### FEREV-11 `adapter` 名不副实

- **现状**：架构上宣称"组件库可替换"，但 `system/role/index.vue` 等页面直接 import 20+ 个 `ant-design-vue` 组件（`Table`、`Space`、`Drawer`、`Form`…）。
- **影响**：抽象层只存在于文档，替换成本与未抽象时相同；新人会误以为该走 adapter 而实际走了捷径。
- **建议**：给一个明确边界——「业务页面可直接使用 UI 库组件，但禁止在 `packages` 内 import 具体 UI 库」。若采纳，删掉 adapter 的"可替换"承诺并修订文档；若不采纳，须逐步把业务页迁移到 adapter 封装。
- **验收**：`docs/ARCHITECTURE.md` 中关于 adapter 的表述与实际约束一致。

#### FEREV-12 两套表单体系并存

- **现状**：`views/iam` 使用自造 `FormModal` + `FormField`；`system/role` 查询区使用 `useVbenForm` + `VbenFormSchema`。
- **影响**：同一页面内两种 schema 风格，认知负担翻倍；校验与联动能力无法复用。
- **建议**：主表单统一到 `useVbenForm`；`FormModal` 只保留"≤3 字段的快速弹窗"，并在文件头注释写清适用边界。
- **验收**：新增表单不再新增 `FormField` 用法；`FormModal` 的使用点带边界注释。

#### FEREV-13 校验实现分散且存在缺口

| 方式 | 位置 | 特点 |
|---|---|---|
| 手写必填循环 | `iam/components/form-modal.vue:63-75` | 提交后 `message.error`，无内联 |
| 计算属性 + 正则类 | `_core/authentication/password-change.vue:29-40` | 密码策略唯一实现，硬编码在页面内 |
| zod refine | `_core/profile/password-setting.vue:47-53` | 只校验"两次输入一致"，**不含策略校验** |
| zod schema | `useVbenForm` 的 `rules` | 框架能力，未全量采用 |

- **影响**：强制改密页会拦下弱密码，自助改密页不会——同一策略两页表现不一致，用户会误判"系统允许"。后端策略变更时也只有一处需要改的假设不成立。
- **建议**：抽 `passwordPolicySchema` 到 `packages/business/auth`，强制改密页与自助改密页统一引用，文案取 `authentication.passwordChangeRule`（`locales/zh-CN/authentication.json:81`）。
- **验收**：两处改密入口对同一弱密码表现一致，且 `grep -rn "length < 12"` 仅命中策略定义处。

#### FEREV-14 `bootstrap` 初始化顺序为隐式约定

- **现状**：`main.ts` 必须先 `await initPreferences` 再动态 `import('./bootstrap')`，否则 `enableRefreshToken` 读到偏好默认值 `false`。
- **影响**：一次随手的静态 import 就会静默破坏刷新链路，且无报错。
- **建议**：在 `bootstrap.ts` 顶部加运行时断言（未初始化 preferences 时抛错），把隐式约定变成显式失败。
- **验收**：故意改成静态 import 时，控制台立即报明确错误。

#### FEREV-22 `system/role/index.vue` 621 行

- **建议**：按 `query-fields.ts` / `columns.ts` / `use-role-crud.ts` / `index.vue` 拆分；`openEdit` / `openGrant` / `openConstraints` 三个 open 函数迁到组合式文件。
- **验收**：`index.vue` ≤ 250 行，模板只保留结构与事件绑定。

---

## 4. 人机交互逻辑

### 4.1 亮点（无需改动，作为后续页面的参照样板）

1. **登录四态是完整闭环**：密码 → 强制改密 → MFA 挑战 → 强制绑定，每步独立视图与返回路径，不是只画了登录框。
2. **错误分类到位**：429 读 `Retry-After` 起倒计时；票据类 400/401 判定为"票据已被消费"，强制退回密码页而非原地重试；文案优先取后端 `message`。
3. **审批模式转成了产品路径**：后端 40900 不弹红字，而是打开 `ConflictModal` 让用户提交审批单（`iam/components/conflict-modal.vue` → `createChangeRequestApi`）。这是很成熟的设计。
4. **破坏性操作有上下文**：删除角色前拉详情并展示"绑定数为 N；存在绑定时后端会拒绝删除"；MFA 解绑有明确警告；改密成功强制登出。
5. **权限不足时按钮不渲染**（`v-access:code`），而非点击后 403。
6. **权限选择器体验良好**：搜索、按资源分组、组级全选 / 半选、`deprecated` 标记。
7. **登录过期在弹窗内重登**，不丢当前页面；锁屏、水印随主题联动。

### 4.2 待改进

#### FEREV-07 操作列 5 个同权按钮平铺

- **现状**：`system/role/index.vue:97` 操作列固定 320px，内含授权 / 编辑 / 约束 / 克隆 / 删除五个 `type="link"` 按钮。
- **影响**：同色同权重并列，扫视成本高；窄屏换行后误点风险上升；危险操作只靠位置区分。
- **建议**：改为「主操作（授权 / 编辑）+ 更多（约束 / 克隆 / 删除）」，危险项在更多菜单内独立分组并加二次确认。
- **验收**：操作列宽度 ≤ 160px；删除入口与编辑入口视觉分组隔离。

#### FEREV-08 停用原因缺条件必填

- **现状**：`reason` 字段仅有 `placeholder: '停用角色时必填'`，无 `rules`；用户提交后才被后端拒绝。
- **建议**：`status === 'disabled'` 时动态追加 required 规则并内联提示；`status === 'active'` 时可空。
- **验收**：停用且未填原因时，提交按钮前即在字段下方提示，不发请求。

#### FEREV-09 删除原因硬编码

- **现状**：三处删除均写死 `reason: '页面删除'`（`system/role/index.vue:458`、`iam/sod/index.vue:206`、`iam/template/index.vue:365`）。
- **影响**：审计流水全部同因，事后无法区分是误删、清理还是迁移。
- **建议**：删除确认弹窗内提供原因输入（预填"页面删除"但可改），或从 `Modal.confirm` 升级为带表单的 Modal。
- **验收**：审计日志中删除原因呈多样性；`grep -rn "'页面删除'"` 仅存默认值常量。

#### FEREV-10 列表 `:key` 使用索引

- **现状**：`iam/components/data-scope-editor.vue:52` 用 `:key="index"` 渲染可增删的规则行。
- **影响**：删除中间行后，输入框内部状态复用导致内容错位；这是典型 Vue 列表坑。
- **建议**：给每行分配稳定 `uid`（`crypto.randomUUID()` 或自增计数器），作为 `:key`。
- **验收**：删除中间行后其余行输入值不串位。

#### FEREV-15 必填校验非内联

- **现状**：`form-modal.vue:63-75` 遍历字段，缺失即 `message.error('请填写X')` 并 `return`。
- **影响**：无字段高亮、无聚焦、无滚动定位；长表单里用户需自行寻找。
- **建议**：改用 antd `Form` + `rules`，或在现有实现上补 `scrollToField` 与字段错误态。
- **验收**：多字段缺失时一次性展示全部错误，并定位到首个错误字段。

#### FEREV-16 打开弹窗前拉详情无 loading

- **现状**：`openEdit`（`:306`）、`openGrant`（`:347`）、`openConstraints`（`:384`）、`openDelete`（`:451`）均先 `await getRoleApi()` 再开弹窗，期间无任何反馈。
- **影响**：慢网下点击像没反应，用户重复点击会并发多次请求。
- **建议**：按钮进入 `loading` 态，或先开弹窗再骨架加载。
- **验收**：模拟 1s 延迟时，点击后有明确的进行中状态。

#### FEREV-17 密码表单未消费 `cooldown`

- **现状**：`login.vue` 已有 `cooldown` / `startCooldown` / `stopCooldown`，并传给 `MfaVerify`、`MfaBinding`（`:220-234`），但密码表单未接收。
- **影响**：密码阶段被 429 限流后，提交按钮仍可点击，用户继续触发 429，体验恶化。
- **建议**：密码表单同样接收 `cooldown`，按钮展示"重试 (Ns)"并禁用。
- **验收**：密码阶段 429 后按钮倒计时禁用。

#### FEREV-18 表格无骨架屏 / 无空态引导

- **现状**：各列表页仅 `loading` 转圈；空数据时显示默认 `empty` 文案。
- **建议**：首屏用骨架屏；空态区分"无数据"与"筛选无结果"，后者提供"清空筛选"按钮。
- **验收**：空结果页有可操作引导，而非空白表格。

---

## 5. 视觉观感

### 5.1 亮点

- **设计令牌体系完整**：HSL 变量 + light / dark 双套，圆角 `0.5rem`，主色 `212 100% 45%`（≈ `#0b6fff`），间距与字号成体系（`packages/@core/base/design/src/design-tokens/{default,dark}.css`）。
- **暗色不是简单反色**：背景 `#14161a`、输入底色 `#1c1e23`、正文 `#f2f2f2`，层次经过设计。
- **登录页构图克制**：左 60% 品牌区 / 右 40% 表单，暗色 `#070709` + `linear-gradient(154deg, rgba(0,107,230,0.2))` + SVG 插画，未堆特效。
- **主题切换无闪烁**：`initPreferences` 先于挂载，`--background-deep` 等变量一次到位。

### 5.2 待改进

#### FEREV-01 登录页仍是 Vben 模板文案（P0）

- **现状**：文案不在页面里，而在公共语言包 `packages/locales/src/langs/zh-CN/authentication.json:3-4`：
  - `pageTitle`: "开箱即用的大型中后台管理系统"
  - `pageDesc`: "工程化、高性能、跨组件库的前端模版"
- **影响**：直接向最终用户宣告"这是开源模板"，是当前观感上最掉分的一处；改动成本极低。
- **建议**：在 app 级 locale 覆盖（而非改公共包），替换为业务定位文案。需一并检查 `loginSubtitle`、页脚版权、`signUpSubtitle` 等同源文案。
- **验收**：`grep -rn "开箱即用的大型中后台管理系统\|跨组件库的前端模版" apps/` 无命中，且未修改 `packages/locales` 的默认值。

#### FEREV-02 静态资源走公网 CDN（P1）

- **现状**：默认 logo / 头像来自 `@vben-core/preferences` 默认配置——`packages/@core/preferences/src/config.ts:19`（`avatar-v1.webp`）与 `:70`（`logo-v1.webp`），均指向 `https://unpkg.com/@vbenjs/static-source@0.1.7/...`；常量兜底见 `packages/@core/base/shared/src/constants/vben.ts:15`。app 级 `preferences.ts` 未覆盖这两项。
- **影响**：内网 / 受限网络下首屏裂图；引入外部可用性依赖；也是 FEREV-01 的一部分。
- **建议**：在 `apps/*/src/preferences.ts` 覆盖 `app.logo.source` 与 `app.defaultAvatar`，指向本地 `public/` 资源；PWA 图标同理（`internal/vite-config/src/options.ts:14,19`）。
- **验收**：DevTools 切 offline 后刷新登录页，logo 与头像正常显示。
- **PWA 部分结论（2026-10-30 核实）**：`internal/vite-config/src/options.ts:14,19` 的 manifest 图标确实指向 `unpkg.com`，但两端 `.env.production` 均为 `VITE_PWA=false`，`internal/vite-config/src/utils/env.ts:10` 的 `getBoolean` 仅在值为字符串 `'true'` 时返回真，dev 环境也未设置该项——因此 `VitePWA` 插件在两端实际**从未启用**，manifest 不会被生成，该 CDN 引用属于不可达配置。本轮未改造它：若未来要开 PWA，需先生成本地 `192/512` 图标再替换，否则离线安装会失败。

#### FEREV-03 安全设置页硬编码假数据（P1）

- **现状**：`views/_core/profile/security-setting.vue:20` 起，"已绑定手机：138****8293""备用邮箱 ant***sign.com""密码强度：强"均为写死字符串。B 端同文件同样存在（`web-biz/src/views/_core/profile/security-setting.vue:18`）。
- **影响**：视觉完整度很高，一旦用户查看即露馅，且会让安全相关信息失去可信度。
- **建议**：能接后端就接（`/me` 返回脱敏手机 / 邮箱、`password_updated_at` 换算"上次修改"）；短期无法接入则改为"未绑定 / 待接入"的中性占位，**不要展示虚构的具体值**。
- **验收**：页面所有安全字段均来自接口或显式占位。

#### FEREV-19 两端视觉人格不统一

- **现状**：A 端宽屏有品牌区，B 端窄屏下左侧整块消失、只剩表单。
- **建议**：B 端窄屏保留精简品牌标识（logo + 一句 slogan 横排），避免"像另一个产品"。
- **验收**：同一账号在两端的登录页能识别出是同一产品。

#### FEREV-20 页面节奏扁平

- **现状**：iam 各页统一为「`bg-card rounded-lg p-4` 查询区 + 裸 Table」，`Page` 仅提供 title / description。
- **影响**：连续翻几页会觉得是同一个空白页，视觉信息密度均匀、无重点。
- **建议**：`Page` 增加可选 `extra`（页级主操作）与 `tabs` 插槽；列表页统一「标题区（含主操作）→ 筛选卡 → 结果卡（含总数与批量操作）」三段式。
- **验收**：随机截图三张列表页，可在 3 秒内区分页面归属。

#### FEREV-21 圆角语言不统一

- **现状**：登录页右上角主题 / 语言胶囊 24px，表单与按钮 6–8px。
- **建议**：全站圆角只用两档——控件 8px、容器 12–16px；胶囊类控件单独定义 token。
- **验收**：设计令牌中存在 `--radius-control` / `--radius-surface` / `--radius-pill` 三档，页面不再出现字面量。

---

## 6. 优化路线（按依赖与收益排序，可逐项勾选）

### 阶段一：零风险观感修复（预计 0.5 人日，无逻辑改动）

- [x] **FEREV-01** 登录页文案品牌化（app 级 locale 覆盖 `pageTitle` / `pageDesc` / `loginSubtitle`，不动公共包默认值）
- [x] **FEREV-02** 在 app `preferences.ts` 覆盖 logo / 头像为本地资源，移除 `unpkg.com` 依赖（含历史缓存幂等迁移）
- [ ] **FEREV-03** 安全设置页「未绑定 / 接入中」占位替换虚构值
- [ ] **FEREV-09** 删除原因三处改为可输入（或至少从常量集中管理）

> 这四项不改动任何请求逻辑与权限，可独立提交、独立回滚，收益 / 风险比最高。

### 阶段二：修通 B 端（预计 1–2 人日）

- [ ] **FEREV-06** mock 补 `/iam/me/menus`，或 B 端临时切 `accessMode: frontend`
- [ ] **FEREV-04** `web-biz` 认证对齐真实契约（四态 + 双令牌）
- [ ] 补齐 B 端缺失页面的路由登记

> 依赖阶段一的成果无，但依赖契约（见 `docs/FRONTEND_INTEGRATION_REVIEW.md`）。两项应同批完成，避免中间态长期存留。

### 阶段三：抽共享业务层（预计 2–3 人日）

- [ ] **FEREV-05** 新建 `packages/business/auth`，导出 `LoginOutcome` / `loginApi` / `refreshTokenApi` / `passwordPolicySchema`
- [ ] 两端改为依赖该包，删除各自 `api/core/auth.ts` 中的重复实现
- [ ] **FEREV-13** `passwordPolicySchema` 落地，强制改密页与自助改密页统一引用
- [ ] **FEREV-14** `bootstrap` 增加 preferences 未初始化断言

### 阶段四：交互质量（预计 2–3 人日）

- [ ] **FEREV-07** 操作列收敛为「主操作 + 更多」
- [ ] **FEREV-08** 停用原因条件必填
- [ ] **FEREV-10** `data-scope-editor` 改稳定 key
- [ ] **FEREV-15** 表单校验内联化 + 错误聚焦
- [ ] **FEREV-16** 详情预加载补 loading
- [ ] **FEREV-17** 密码表单接入 `cooldown`
- [ ] **FEREV-18** 骨架屏 + 空态引导

### 阶段五：架构与视觉纵深（按需）

- [ ] **FEREV-12** 表单体系统一到 `useVbenForm`，`FormModal` 限定 ≤3 字段
- [ ] **FEREV-11** adapter 边界落定并同步文档
- [ ] **FEREV-20** `Page` 组件升级为三段式布局
- [ ] **FEREV-21** 圆角 token 三档化
- [ ] **FEREV-22** `system/role/index.vue` 拆分
- [ ] **FEREV-19** 两端登录页品牌一致性

---

## 7. 复测方法

| 项 | 命令 / 操作 | 期望 |
|---|---|---|
| 启动 A 端 | `pnpm dev:admin`（`frontend/`） | 登录页文案与品牌已替换；断网无裂图 |
| 启动 B 端 | `pnpm dev:biz` | 登录后进入首页，侧边栏非空 |
| 权限码一致性 | `grep -rn "v-access:code" apps/*/src`，与后端 `/iam/me/permissions` 返回值比对 | 无"页面有、后端无"的权限码 |
| 契约定型 | 对照 `api/openapi/{admin,biz}.yaml` | 入参 / 响应字段无猜测命名 |
| 工程门禁 | `pnpm check`（circular / dep / type / cspell） | 全绿 |
| 视觉回归 | 亮暗双主题 × 1440 / 768 / 375 三档截图 | 无溢出、无裂图、圆角一致 |

---

## 8. 变更记录

| 日期 | 变更 | 说明 |
|---|---|---|
| 2026-10-30 | 初版 | 基于 `ee515aa` 与工作区未提交改动评审；工作区修改的文件（`api/core/auth.ts`、`store/auth.ts`、`login.vue`、`security-setting.vue` 等）已纳入本次评审基线 |
| 2026-10-30 | 修订 | 修正 FEREV-01/02 的证据定位（文案与静态资源在 `packages` 默认配置，非页面文件）；改写 FEREV-13 为真实缺口（自助改密页缺策略校验）；FEREV-04 补充两端页面清单对比 |
| 2026-10-30 | 完成 FEREV-01 + FEREV-02 | 两端新增 `src/locales/langs/{zh-CN,en-US}/authentication.json` 覆盖 `pageTitle`/`pageDesc`/`loginSubtitle`；`public/brand/` 提供本地 logo 与头像，`preferences.ts` 覆盖并新增 `migrateLegacyBrandAssets()` 处理历史缓存。实测：双端中文/英文、亮/暗均生效，图片 `currentSrc` 为本机源，页面无 `unpkg.com` 引用；`pnpm check` 的 cspell 失败属既有 `api/core/auth.ts`、`store/auth.ts` 的 TOTP 词条，与本次无关 |
| 2026-10-30 | 更换品牌 logo | 取 `favicon.ico` 的配色做渐变（`#BE7BEE` → `#A26BEB` → `#5A77F2` → `#2ADEFF`，约 135°，由 favicon 采样得出），图形改为「域括号 + 主体」：两侧对称圆角弧表示受控域边界，中心实心圆点表示域内主体。48 网格、squircle 圆角 14、描边 3.8、圆点 r3.5，16/20/24/32/42/64 六档实测可辨 |
| 2026-10-30 | 同步 favicon.ico | 两端 `public/favicon.ico` 重制，沿用 logo 的几何参数与四段渐变，内嵌 16/32/48/64 四档 PNG（Vista+ 规范，`planes=1`、`bpp=32`）。校验：`ICONDIR` 计数与目录偏移自洽，圆角外像素 alpha=0，中线扫描左右弧与圆点位置左右对称于 x=24 且与 `logo.svg` 网格坐标一致；dev server 返回 5684 字节、`image/x-icon`。未改动 `index.html`（仍指向 `/favicon.ico`） |
| 2026-10-30 | 补 SVG 图标 | 两端新增 `public/favicon.svg`（与 `brand/logo.svg` 同一套几何与渐变，逐字节一致），`index.html` 改为 `<link rel="icon" type="image/svg+xml" href="/favicon.svg">` 在前、`type="image/x-icon"` 的 `.ico` 在后，旧浏览器自动回退。校验：两个 dev server 均返回 `200 image/svg+xml`；浏览器实渲染中线扫描为 `15..18 / 28..35 / 45..48`（64px）、`8..9 / 14..17 / 22..23`（32px），与 ICO 内嵌 PNG 扫描结果完全相同；四角 `alpha=0`。**维护不变式**：`favicon.svg`、`favicon.ico`、`brand/logo.svg` 共用同一几何，改其一须同步另两处 |
| 2026-10-30 | 图标一致性收敛 | 逐像素比对浏览器渲染的 `favicon.svg` 与 `favicon.ico` 内嵌 PNG：`32×32` 平均通道差 `2.59`、`16×16` 为 `5.17`；差异全部落在圆角与描边边缘（Skia 与 GDI+ 抗锯齿 gamma 不同），中线白色笔画区间**完全一致**，无结构性偏移。`oxfmt --check` 通过；两端 dev server 的 `/favicon.svg` 与 `/favicon.ico` 均 `200` 且 MIME 正确 |

> 后续每完成一项，请在 §6 勾选，并在本节追加一行记录（日期 + 完成项 ID + 遗留问题）。
