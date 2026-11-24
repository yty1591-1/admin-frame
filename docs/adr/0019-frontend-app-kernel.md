# ADR-0019：前端双端公共内核（app-kernel）下沉

- 状态：Proposed（草案，待架构 Owner 评审，2026-11-24）
- 日期：2026-11-24
- 关联：[`../frontend-remediation-plan.md`](../frontend-remediation-plan.md) §5.6（FE-K-00～03）、[`../FRONTEND_STANDARDS.md`](../FRONTEND_STANDARDS.md) §2（FE-01～05 依赖方向）、[`../frontend-app-kernel-brief.md`](../frontend-app-kernel-brief.md)（立项书）

## 背景

`web-admin`（A 端）与 `web-biz`（B 端）从同一模板派生，长期各自维护：

- **约 1300 行逐字节相同**：`main.ts`、`bootstrap.ts`、`app.vue`、`adapter/form.ts`、`adapter/component/index.ts`（744 行）、`router/{access,guard}.ts`、`store/index.ts`、`api/core/*`、`locales/index.ts` 等；
- **约 340 行同构微差**：`api/core/auth.ts`（web-admin 305 行 / web-biz 101 行）、`store/auth.ts`（180 / 108），差异来自 MFA、改密、会话等 A 端能力；
- **已发生的静默漂移**：`main.ts:34` 的 `await initApplication()` 只在 A 端；`web-biz` 缺失 `Idempotency-Key` 注入；`useRequestHandler` 刷新失败后的重试头注入缺陷在两端各自修复。

结果：每个门禁修复都要写两遍（本轮 FE-S-01/05/06/07、FE-T-01 均验证了这一点），且「两端一致」只能靠人工 diff。规范（FE-01）已要求 app 互不引用，但没有提供公共内核，复制成了唯一选择。

## 决策

1. **新建 `frontend/packages/effects/app-kernel`（包名 `@vben/app-kernel`）**，作为 app 层组合包，而不是放进 `@core/*`。依赖方向固定为：`apps/* → @vben/app-kernel → @vben/{access,request,stores,locales,utils,layouts}`；`@core/*` 不得依赖 app-kernel，app-kernel 不得引用 `apps/*` 或 `#/*`。
2. **内核只承载「两端同构的运行骨架」**：应用启动（`install()`，内部 `await` 初始化）、请求客户端工厂（拦截器链：错误映射/trace、刷新单飞、幂等头）、路由与守卫（`createRouterGuard`、`generateAccess`）、登录态 store 工厂、locale 初始化。视图、业务 API 模块、路由表仍归 app。
3. **能力差异用显式 capabilities 表达**，不用复制代码：
   ```ts
   interface KernelCapabilities {
     mfa?: boolean;            // 登录后 MFA 挑战 / 绑定
     passwordChange?: boolean; // 首登强制改密
     mfaBinding?: boolean;     // 自助绑定 / 关闭 MFA
     idempotency?: boolean;    // 写请求注入 Idempotency-Key
     refresh?: boolean;        // 401 刷新单飞
   }
   ```
   A 端默认全开，B 端 `{ mfa: false, passwordChange: false, mfaBinding: false, idempotency: false }`。差异文件（`api/core/auth.ts`、`store/auth.ts`）收敛为「能力开关 + 可选扩展回调」，不允许再分叉实现。
4. **app 注入与内核解耦的边界**：`createAppKernel({ app, adapter, views, routes, capabilities })`——`views` 由 app 传 `import.meta.glob('../views/**/*.vue')` 结果，`adapter` 由 app 传组件/表单/上传适配器；内核不读 app 私有路径，保持 `#/` 深导入禁令（FE-03）。
5. **迁移分两批**：第一批 17 个逐字节相同文件（`main.ts`、`bootstrap.ts`、`app.vue`、`adapter/{form,component}`、`layouts/{auth,index}`、`locales/index.ts`、`router/{access,guard,index}`、`routes/index.ts`、`routes/modules/profile.ts`、`store/index.ts`、`api/core/{index,menu,user}`）；第二批差异文件按 §3 收敛。
6. **门禁同步**：`apps/*` 禁止直接 import 内核内部子路径（只允许 `@vben/app-kernel` 入口）；ESLint 增加 `no-restricted-imports`；`check:type` 覆盖新包；两端构建与冒烟（FE-B-04 容器冒烟）作为迁移出口。
7. **行为不回退**：迁移期间已知漂移（`await`、幂等头）必须在 FE-K-03 前归零；B 端能力差异由 capabilities 显式声明并补单测，不允许「B 端悄悄缺功能」。

## 迁移与验收（FE-K-00～03）

| 阶段 | 交付 | 验收 |
| --- | --- | --- |
| FE-K-00 | 本 ADR + 立项书 | ADR 转 Accepted；立项获批 |
| FE-K-01 | 迁移清单与逐文件基线（17 文件） | 清单可核对；每文件有归属与回归点 |
| FE-K-02 | 差异文件裁决表（`auth.ts`、`store/auth.ts`） | A/B 能力取舍明确，不丢 MFA/改密/会话 |
| FE-K-03 | 迁移执行、漂移归零、双端回归、迁移指南 | 已知漂移全部关闭；两端构建/冒烟通过；FE-K 清单打勾 |

## 后果

- 正面：一处修复两端生效；门禁只维护一份；能力差异可测试、可审计。
- 成本：一次性重构（约 1600 行搬迁 + 守卫/请求/登录态回归），需要冻结迁移期间的 app 层改动窗口。
- 风险：`adapter/component` 依赖 `ant-design-vue` 较重，若内核把它纳入，会拉高内核依赖面——FE-K-02 需裁决「适配器留 app 还是进内核」，默认留 app，内核只定义 `AppAdapter` 接口。
- 回滚：内核为独立包，回滚 = revert 迁移提交并把 app 入口改回本地实现；不涉及后端与部署形态。

## 备选方案

- **保持复制 + 强化 lint**：已证明不可行（漂移已发生两次以上），否决。
- **代码生成同一模板**：运行时仍是两份代码，测试重复，否决。
- **合并为单 app + 环境开关**：与「两端独立构建/发布/域名」冲突（FE-23/24），否决。
