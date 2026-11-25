# 前端公共内核（app-kernel）立项书

- 状态：Draft（2026-11-24，待架构 Owner 批准；批准后 FE-K 项目启动）
- 定位：L3 执行计划，承接 [frontend-remediation-plan.md](./frontend-remediation-plan.md) §5.6 与 [ADR-0019](./adr/0019-frontend-app-kernel.md)
- 效力：批准后项目排期与验收口径以本文 + ADR-0019 为准；ADR 未 Accepted 前不进入编码
- 不适用范围：视图/业务 API 重构、UI 改版、后端契约变更

## 一、目标

把 `web-admin` 与 `web-biz` 约 1300 行逐字节相同的启动/请求/路由/登录态骨架下沉为 `@vben/app-kernel`，以 capabilities 显式表达 A/B 能力差异，使一处修复两端生效，并把已发生的静默漂移（`main.ts` await、`web-biz` 幂等头）归零。

## 二、范围

| 进范围 | 出范围 |
| --- | --- |
| `main.ts`/`bootstrap.ts`/`app.vue` 启动链 | 视图页面（`apps/*/src/views/**`） |
| 请求客户端与拦截器（错误映射、刷新单飞、幂等头） | 业务 API 模块（`api/iam`、`api/configcenter` 等） |
| 路由守卫与菜单→路由生成（pageMap 注入） | 布局/主题视觉改动 |
| 登录态 store 工厂（A 端 MFA/改密/会话能力开关） | `@core/*` 行为调整 |
| locale 初始化、组件适配器接口 | 后端契约、部署形态 |

## 三、里程碑与验收

| 编号 | 交付 | 验收标准 | 依赖 |
| --- | --- | --- | --- |
| FE-K-00 | ADR-0019（Proposed）+ 本立项书 | 架构 Owner 批准，ADR 转 Accepted | — |
| FE-K-01 | 迁移清单（17 文件）与逐文件基线 | 清单逐文件核对；每文件标注归属、回归点、首次迁移顺序 | FE-K-00 |
| FE-K-02 | 差异裁决表：`api/core/auth.ts`（305/101）、`store/auth.ts`（180/108） | A/B 能力取舍明确；MFA/改密/会话能力不丢；补 capabilities 单测 | FE-K-01 |
| FE-K-03 | 执行迁移 + 漂移归零 + 迁移指南 | `await`/幂等头/错误处理一致；两端 `frontend-check` 14 步绿；两端构建与容器冒烟通过；删除迁移清单 | FE-K-02 |

## 四、执行约束

- 迁移窗口内 `apps/*` 的 `src/{router,store,api/core,adapter,main.ts,bootstrap.ts,app.vue}` 冻结；业务改动只允许落在 `views/` 与业务 `api/` 模块。
- 每个迁移批次必须两端同时构建 + `pnpm check:type` + 相关单测；不允许「先迁 A 端、B 端稍后」。
- 内核不引入新运行时依赖；`ant-design-vue` 适配器默认留 app（内核只定义接口），除非 FE-K-02 明确裁决。
- 迁移完成后 `apps/*` 对内核只允许 `import '@vben/app-kernel'` 单入口；`#/` 深导入禁令保持不变（FE-03）。

## 五、风险

| 风险 | 影响 | 缓解 |
| --- | --- | --- |
| 大爆炸式迁移破坏登录/守卫 | A/B 端不可用 | 按文件分批；每批两端构建 + 登录冒烟；FE-K-03 前保持旧实现可 revert |
| capabilities 设计不足，B 端能力被误删 | B 端功能缺失 | FE-K-02 显式裁决 + 两端策略单测（登录四态/redirect/幂等） |
| 内核依赖膨胀（ant-design-vue） | 包体积/耦合 | 适配器留 app；内核只定义接口与请求/路由/状态工厂 |
| 迁移期间与并行业务改动冲突 | 合并冲突/回归 | 冻结窗口 + 迁移清单文件级 ownership |

## 六、待决问题

1. B 端是否需要 MFA/改密（capabilities 默认值）——由产品在 FE-K-02 前确认。
2. 组件适配器是否进内核——默认不进；若进，需评估 `ant-design-vue` 版本对齐成本。
3. 迁移窗口长度与并行需求排期——需项目负责人确认。

## 七、退出标准

- FE-K-00～03 全部完成并在 [frontend-remediation-plan.md](./frontend-remediation-plan.md) §八 状态表标记完成；
- 已知漂移全部关闭，`grep -R "await initApplication" frontend/apps` 两端一致；
- 两端 `bash scripts/ci/frontend-check.sh` 14 步绿，容器冒烟（FE-B-04）通过。
