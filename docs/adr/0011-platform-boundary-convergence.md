# ADR-0011：平台层边界收敛（域配置下沉 / 守卫端口下沉 / 显式装配）

- 状态：Accepted
- 日期：2026-11-07
- 关联：[ARCHITECTURE §5.6](../ARCHITECTURE.md)、[架构评审报告](../platform-architecture-review.md)、[check-layering.sh](../../scripts/ci/check-layering.sh)

## 背景

[架构评审报告](../platform-architecture-review.md) 指出 `internal/platform` 存在三类边界问题：

1. **P0-1**：`config.go` 超过 3000 行，47 个域结构体集中在同一文件，新增配置必改平台层。
2. **P0-2 / P1-4**：域配置结构体与租户状态 / 成员关系 / API 配额端口定义在平台层，平台事实上承担了业务语义。
3. **P0-3**：`bootstrap.Run` 是 god function，装配顺序只写在注释里，错序不会启动失败。

## 决策

### 1. 域配置归属业务域

| 包 | 负责内容 |
|---|---|
| `internal/identity/config` | `Identity*` 全部类型与身份域常量 |
| `internal/iam/config` | `IAM*` 类型、`Enabled` / `IsEnabled`、IAM 模式常量 |
| `internal/audit/config` | `Audit*` 类型、`EffectiveStore`、保留期常量 |
| `internal/tenant/config` | `Tenant*` 类型 |
| `internal/platform/config` | 根 `Config` 聚合、加载 / 绑定、默认值、跨段编排校验、脱敏与密钥解析 |

`platform/config.Config` 的字段名与结构保持不变，字段类型改为对应域包类型；默认值、归一化与跨域校验仍留在平台层（「加载 + 绑定 + 编排校验」是平台职责）。

### 2. `deps.Config` 迁移路径

- **本版**：`cmd/*` 与域 runtime 继续通过 `deps.Config.Identity` 等字段读取配置；`platform` 顶层别名（`type IdentityConfig = identityconfig.IdentityConfig` 等）保留一个版本，避免一次性改动全部调用方。
- **后续**：域 runtime 逐步自持其配置视图（构造参数或域级 `Config` 结构），`deps.Config` 只保留核心段；删除平台层域配置别名。迁移以域为单位分批完成，不做一次性破坏性切换。
- `AuditConfig.EffectiveStore` 等派生方法随域类型迁移；装配期决策（store 选择）由 `cmd` / 域 runtime 调用该方法得到，不在平台层内联。

### 3. 守卫端口下沉

- `GuardPort` / `MembershipPort` 定义在 `internal/tenant/app`；配额执行器沿用 `tenant/app.QuotaEnforcer`。
- 平台层只保留通用装配点：`httpserver.TenantGuardMiddleware` / `QuotaGuardMiddleware` 接收函数类型，`httpserver.AfterAuth` 负责按声明顺序插入鉴权链内层。
- `platform.Deps` 删除 `TenantGuard` / `QuotaGuard` / `TenantMembership` 字段，改为通用函数字段：
  - `VerifyTenant func(ctx, merchantID) (bool, error)`
  - `IsTenantMember func(ctx, tenantID, subjectID) (bool, error)`
- `platform.Options` 以 `NewAuthGuards func(*Deps) ([]func(http.Handler) http.Handler, error)` 取代 `NewTenantGuard` / `NewQuotaGuard`；数组顺序即运行顺序（`auth → guards[0] → guards[1] → handler`）。

### 4. 显式装配顺序

`platform.Run` 固定为：

```text
prepareInfra → buildAsync → buildGuards → buildAuthN → buildAuthorizer → buildRouter → serve
```

- `buildGuards` 在 `buildAuthN` 前执行，守卫构造期写入 `deps.VerifyTenant`，供 identity 登录 / 开通读取。
- `buildAuthN` 在 `NewSessionValidator` 前完成守卫构造，在 AuthMW 之后按 `deps.authGuards` 顺序插入。
- 每个步骤显式检查前置依赖（`Config` / `Logger`），缺失返回错误而非依赖注释约定。
- `httpserver/guards_test.go` 增加顺序单测：租户守卫拒绝后配额守卫不得被调用。

## 兼容策略

- 平台顶层 `aliases.go` 保留域配置类型别名与 `WithTenantGuard` / `WithQuotaGuard` 薄包装，兼容一个版本；`cmd/*` 在阶段 2 无编译改动。
- `check-layering.sh` 的 `LAYER_SEMANTIC_BASELINE` 清空，`PLATFORM_MAX_FILES` 下调到 5，基线只减不增。

## 后果

- **正面**：域配置演进不再触碰平台层；平台层不再定义业务语义接口，LAYER-09 无基线；装配顺序变成代码结构而非注释。
- **负面**：类型别名与函数包装带来一版间接层；`deps.Config` 仍是聚合对象，域配置完全自持需后续分批迁移。
- **移除计划**：调用方完成域配置自持后，删除 `platform` 顶层域配置别名；守卫包装在确认无外部使用后删除，仅保留 `httpserver` 通用装配点。
