# app-kernel 迁移清单与差异裁决（FE-K-01）

- 状态：进行中（2026-11-24，随 FE-K-01 建档；FE-K-02 裁决待补）
- 关联：[ADR-0019](./adr/0019-frontend-app-kernel.md)（Accepted）、[frontend-app-kernel-brief.md](./frontend-app-kernel-brief.md)
- 基线：`feature/configcenter-m2-m3-exit` @ `654f5bc`，脚本对 `frontend/apps/web-{admin,biz}/src` 全量 sha256 + 行级 diff
- 规模：公共路径 40 个文件；逐字节相同 19 个（含 2 个测试、2 个 README）；差异 21 个

## 一、第一批：逐字节相同（17 个搬迁项）

搬迁目标：`packages/effects/app-kernel/src/`。顺序按依赖：工具函数 → 状态/请求 → 路由 → 启动壳 → 视图兜底。

| # | 文件（apps/*/src） | 行数 | 内核归属 | 回归点 |
| --- | --- | --- | --- | --- |
| 1 | `store/index.ts` | 1 | `kernel/store.ts` | 两端 store 初始化后 `useAuthStore()` 可用 |
| 2 | `api/core/index.ts` | 3 | `kernel/api.ts` | 两端 API 入口导出不变 |
| 3 | `layouts/index.ts` | 6 | `kernel/layouts.ts` | 布局注册表（BasicLayout/IFrameView）可解析 |
| 4 | `views/_core/fallback/not-found.vue` | 9 | 保留 app（视图） | 404 页渲染 |
| 5 | `views/_core/fallback/forbidden.vue` | 9 | 保留 app（视图） | 403 页渲染 |
| 6 | `layouts/auth.vue` | 25 | `kernel/layouts/AuthLayout.vue` | 登录页布局与插槽 |
| 7 | `app.vue` | 39 | `kernel/App.vue` | 根组件挂载、locale/主题 provider |
| 8 | `bootstrap.ts` | 76 | `kernel/bootstrap.ts` | `initApplication` 顺序（i18n→stores→router→mount） |
| 9 | `main.ts` | 34 | `kernel/main.ts` | 启动 `await initApplication()`（两端一致） |
| 10 | `adapter/form.ts` | 68 | `kernel/adapter/form.ts` | 表单适配器（vben-form 映射） |
| 11 | `adapter/component/index.ts` | 756 | `kernel/adapter/component/index.ts` | 上传校验/预览、Vue→AntD 映射（FE-S-05 单测覆盖 upload 逻辑） |
| 12 | `locales/index.ts` | 102 | `kernel/locales.ts` | 语言包加载、`loadMessages` |
| 13 | `router/index.ts` | 37 | `kernel/router/index.ts` | 路由实例创建、alias |
| 14 | `router/routes/index.ts` | 37 | `kernel/router/routes.ts` | 静态路由表（core routes） |
| 15 | `router/access.ts` | 42 | `kernel/router/access.ts` | `generateAccess` pageMap（access.test.ts 2 例） |
| 16 | `router/guard.ts` | 135 | `kernel/router/guard.ts` | 登录跳转/redirect 白名单/动态路由（guard.test.ts 11 例） |
| 17 | `router/__tests__/{access,guard}.test.ts` | 106 + 258 | 随内核迁移 | 迁移后仍为 13 例，`@vben/app-kernel` 导出下运行 |

说明：`views/_core/fallback/*` 虽逐字节相同，但属视图层，按 ADR-0019「视图不动」原则保留 app；因此「17」= 上表搬迁项（含 2 个测试文件、不含 2 个 fallback 视图与 2 个 README）。

## 二、第二批：近同构（11 个，FE-K-02 裁决）

| 文件 | admin/biz 行数 | 差异行 | 裁决方向 |
| --- | --- | --- | --- |
| `api/request.ts` | 132/123 | 10 | 收敛为内核 `createRequestClient(capabilities)`；差异应只剩 baseURL/env |
| `main.ts` | 34/34 | 1 | 唯一差异为 `await initApplication()`（B 端缺失）→ 内核统一 await，FE-K-03 归零 |
| `api/core/menu.ts` | 13/13 | 1 | 探因后合并；差异若为 A 端专属接口，走 capabilities |
| `api/core/user.ts` | 78/78 | 3 | 同上（FEREV-02 字段差异） |
| `router/routes/modules/profile.ts` | 25/24 | 2 | 合并（路径/名称差异） |
| `layouts/basic.vue` | 111/111 | 2 | 差异为 `LockScreen` 挂载 → 内核布局参数化 |
| `preferences.ts` | 88/88 | 4 | 合并（app 名称/title 由 `createAppKernel` 入参） |
| `locales/langs/{en-US,zh-CN}/authentication.json` | 5/5 | 3 | 合并（文案差异） |
| `locales/langs/{en-US,zh-CN}/page.json` | 30/16 | 30 | 保留 app（菜单名称集合不同） |

## 三、差异裁决（FE-K-02，2026-11-24 定稿）

裁决原则：能力按 `KernelCapabilities` 显式开关；关闭的能力**保留方法但调用即抛** `KernelCapabilityError`（fail-closed，不静默降级）；视图与适配器留在 app。

### 3.1 能力契约

```ts
export interface KernelCapabilities {
  mfa: boolean;
  mfaBinding: boolean;
  passwordChange: boolean;
  idempotency: boolean;
  refresh: boolean;
}

export class KernelCapabilityError extends Error {
  readonly capability: keyof KernelCapabilities;
}
```

| 能力 | admin | biz | 依据 |
| --- | --- | --- | --- |
| `mfa` | true | false | B 端登录无 MFA 中间态（`web-biz/src/api/core/auth.ts` 文件头注释；`LoginOutcome` 四态仅 A 端） |
| `mfaBinding` | true | false | 强制绑定仅 A 端（`mfa_binding_required`） |
| `passwordChange` | true | false | B 端无改密接口与视图 |
| `idempotency` | true | false | B 端无 idempotency 模块（`api/idempotency.ts` 仅 A 端） |
| `refresh` | true | true | 两端 `api/request.ts` 均有 `doRefreshToken`/`refreshTokenApi` |

### 3.2 API 面裁决

| 导出 | 能力开关 | admin | biz | 说明 |
| --- | --- | --- | --- | --- |
| `loginApi(data)` | always | ✅ | ✅ | 返回 `LoginOutcome`；载荷要求的能力关闭时抛 `KernelCapabilityError`，不把中间态当令牌 |
| `refreshTokenApi` / `logoutApi` / `getMyPermissionsApi` / `getAccessCodesApi` / `toTokenPairVM` | always（refresh 由能力决定） | ✅ | ✅ | 两端现有行为不变 |
| `changePasswordApi` / `completePasswordChangeApi` | `passwordChange` | ✅ | 抛错 | B 端不展示入口 |
| `mfaSetupApi` / `mfaVerifyApi` / `mfaSetupSessionApi` / `confirmMfaBindingSessionApi` / `disableMfaApi` | `mfa` / `mfaBinding` | ✅ | 抛错 | A 端 MFA 全流程保留 |
| `listMySessionsApi` / `revokeMySessionApi` / `revokeAllMySessionsApi` | 不设开关 | ✅ | ✅ | 纯 REST；B 端暂无会话视图，需要时可直接复用 |

### 3.3 Store 面裁决

`createAuthStore({ capabilities })` 返回与 A 端相同的动作全集：`authLogin`、`completePasswordChange`、`verifyMfaChallenge`、`startMfaBinding`、`confirmMfaBinding`、`startMfaBindingForAccount`、`confirmMfaBindingForAccount`、`disableMfaForAccount`、`fetchUserInfo`、`logout`、`loginLoading`、`$reset`。关闭能力的动作调用即抛错；B 端实际调用面仍只有 `authLogin`/`fetchUserInfo`/`logout`/`loginLoading`。B 端 `authLogin` 行为不变：`loginApi` → `completeLogin`（落双令牌 → `fetchUserInfo` → redirect 白名单跳转 → 成功通知）。

### 3.4 类型与通道

`SubjectPermissions.channel` 内核取 `'admin' | 'biz'`；A 端消费处可窄化为 `'admin'`（现有代码不按 channel 做运行时分支）。B 端保留自身注释语义。

### 3.5 适配器

维持 ADR-0019：adapter 留 app；内核只定义 `AppAdapter` 接口（component/form），apps 注入实现；内核零 `ant-design-vue` 依赖，避免把视图库带进内核。

### 3.6 对迁移顺序的影响

`api/core/auth.ts` 与 `store/auth.ts` 在 FE-K-03 第 4 步迁移；B 端迁移后删除本文件的 MFA 类型段，`completeLogin` 保留；A/B 行为对照单测在 FE-K-03 补（A：四态含 `mfa-binding`/`mfa-challenge`/`password-change`；B：恒 `token`）。

## 四、回归矩阵（迁移每批必跑）

| 范围 | 现有覆盖 | 命令 |
| --- | --- | --- |
| 路由守卫/菜单 | admin+biz `router/__tests__` 13 例 | `pnpm test:unit`（含两 app config） |
| 登录态 | admin 13 例 / biz 7 例；401/204/429 拦截器 10 例 | 同上 |
| 请求/刷新/幂等 | `preset-interceptors.test.ts`、`idempotency.test.ts` 8 例 | 同上 |
| 上传/预览 | `upload.test.ts` 11 例 | 同上 |
| 类型基线 | 34 个 turbo 任务 | `pnpm check:type` |
| 端到端 | 14 步门禁 + 容器冒烟 | `bash scripts/ci/frontend-check.sh` |

## 五、执行顺序与冻结窗口

1. 建包 `packages/effects/app-kernel`（`package.json`、`tsconfig`、`tsdown`、`typecheck`），先空壳 + 契约测试。
2. 迁移第一批 #1–#11（低风险工具/状态/请求），两端同时改 import 并构建。
3. 迁移 #12–#17（路由/启动/测试），两端登录与守卫冒烟。
4. FE-K-02 裁决 `auth.ts`/`store/auth.ts`，补 capabilities 单测。
5. FE-K-03 归零漂移（`await`、幂等头、错误处理），删除迁移清单。

冻结窗口：迁移期间 `apps/*/src/{main.ts,bootstrap.ts,app.vue,adapter,router,store,api/core,locales/index.ts}` 不接受业务改动；业务只允许 `views/` 与业务 `api/`。

## 六、风险与回滚

- 每批一个提交、两端同时可用；任一批回归失败即 revert 该提交（内核为独立包，app 入口改回本地实现即可）。
- 最大风险在 #11 适配器（756 行、依赖 `ant-design-vue`）与 #16 守卫（登录链路）；对应单测必须先在迁移前绿，迁移后原样通过。

## 七、执行进度（FE-K-03）

| 日期 | 批次 | 内容 | 状态 |
| --- | --- | --- | --- |
| 2026-11-24 | 批次 A | 新建 `packages/effects/app-kernel`（`@vben/app-kernel`）；迁移 `adapter/form.ts`、`adapter/component/index.ts`（756 行）；两 app 删除本地 adapter、`bootstrap.ts` 改从内核导入、12 个视图改用 `@vben/app-kernel/adapter/form`；能力类型 `KernelCapabilities` 与 `ADMIN_/BIZ_CAPABILITIES` 入内核 | ✅ 35 turbo 任务、14 步门禁绿 |
| 2026-11-24 | 批次 B（待） | `store/index.ts`、`api/core/index.ts`、`layouts/*`、`app.vue`、`bootstrap.ts` 收敛为 `createAppKernel` | 未开始 |
| 2026-11-24 | 批次 C（待） | router 四文件 + 两组测试、`locales/index.ts` | 未开始 |
| 2026-11-24 | 批次 D（待） | auth api/store capabilities 化、`main.ts` 漂移归零 | 未开始 |
