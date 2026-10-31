# 前端 × 后端 接口能力对接评审

> 评审对象：`frontend/apps/web-admin`、`frontend/apps/web-biz`（源自 vue-vben-admin 的 Ant Design Vue 版本）
> 基准后端：本仓库 `admin-frame`（A 端 `admin-api`、B 端 `biz-api`、C 端 `c-api`）
> 契约来源：`api/openapi/{admin,biz,c,components}.yaml`（以契约为准，实现与契约冲突时按 §7 登记）
> 状态：评审结论待确认；§8 为实施排期建议
> 关联：`docs/API_STANDARDS.md`、`docs/identity-authn.md`、`docs/rbac-iam.md`、`docs/SECURITY_STANDARDS.md`

---

## 0. 一页结论

| 维度 | 结论 | 判定 |
|---|---|---|
| 响应信封 / 业务码 / 分页 / 关联 ID | 前后端约定一致，前端 `requestClient` 可直接复用，无需改造 | 已对齐 |
| 认证（登录 / 刷新 / 登出） | 入参名、响应字段名、响应形态、刷新协议**四处硬冲突**，当前代码无法联通真实后端 | **阻断** |
| 身份信息（用户资料） | 后端无对应端点，前端 `getUserInfoApi` 必然 404 → 定为**新增 `GET /{channel}/v1/me`**（GAP-01） | **阻断** |
| 权限码来源 | 前端 `/auth/codes` 后端不存在；`/iam/me/permissions` 是替代方案且仅需认证 | 高 |
| 授权模型（角色 / 菜单） | 角色 DTO 与前端 mock 差异较大；菜单已决策走**方案 B**（新增菜单表 + `GET /{channel}/v1/menus`，前端切 `accessMode: backend`） | 高 |
| 治理能力（MFA / 审批单 / 会话 / 数据范围 / 破玻璃） | 后端已落地并有契约，前端 0 覆盖 | 缺口 |
| 契约文档本身 | `bearerAuth` 仍描述 demo token；`PageMeta` 缺 `items` | 需修正 |

三条总体判断：

1. **信封层可以零改造**：`{code,message,data,trace_id}`、`code=0` 成功、401 触发重认证，与 `src/api/request.ts` 的拦截器链完全兼容。
2. **认证层必须重写适配**：不是"改个字段名"，而是登录三态（令牌 / MFA 挑战 / 强制绑定）+ 双令牌一次性轮换，需要引入状态机和新的 token 存储语义。
3. **业务能力严重超配**：后端已具备角色授权、审批流转、破玻璃、会话治理、数据范围等生产能力，前端目前只覆盖"角色列表 + 一个增删改查表单"，属于典型的**后端能力过剩、前端消费不足**。

> **2026-10-31 状态更新（本文其余章节为 2026-10-30 的实测快照，保留作审计记录）**
>
> - **Nitro Mock 已移除**：`frontend/apps/backend-mock` 删除，`internal/vite-config` 的 `viteNitroMockPlugin`、`VITE_NITRO_MOCK`、`nitropack` 依赖与 `turbo` 任务一并下线。前端不再有 mock 数据源，所有接口必须走真实后端。
> - **B 端已切真实契约**：`apps/web-biz` 的 `VITE_GLOB_API_URL` 改为 `/biz/v1`，dev 代理指向独立服务 `biz-api`（`configs/biz.yaml` → `:8082`，**不是 A 端的 `:8081`**）；`auth.ts` / `store/auth.ts` / `request.ts` / `user.ts` 已按 §3 的 FEINT-01～06 完成改造（`subject` 入参、`TokenPair` 双令牌、一次性轮换、`/iam/me/permissions`、`GET /me`）。
> - **FEREV-04 / FEREV-06（见架构审查文档）随之关闭**：B 端 `accessMode: backend` 现在由真实 `GET /biz/v1/iam/me/menus` 支撑。
> - 仍存在的前置条件：内存库（`db.driver: memory`）不含任何账号种子，本地要登录成功必须先有 B 端账号（MySQL 模式跑迁移，或经 A 端接口开通）；否则登录稳定返回 `40100 invalid credentials` —— 这属于后端数据准备，不是前端缺陷。

---

## 1. Findings 索引

| ID | 级别 | 主题 | 影响面 |
|---|---|---|---|
| FEINT-01 | P0 | 登录入参字段不符（`username` vs `subject`） | 无法登录 |
| FEINT-02 | P0 | 登录响应三态未处理 | 无法登录 / 静默失败 |
| FEINT-03 | P0 | 响应为 snake_case 且双令牌未落库 | 无法鉴权 / 刷新失效 |
| FEINT-04 | P0 | 刷新协议不符（body 传参 + 未启用 refresh） | 401 即踢出 |
| FEINT-05 | P0 | 无用户资料端点 | 登录后 404 |
| FEINT-06 | P1 | 权限码端点错误 | 权限过滤失效 |
| FEINT-07 | P1 | 角色 DTO / 枚举 / 乐观锁不符 | 列表渲染错误、写入失败 |
| FEINT-08 | P1 | 分页参数名与上限 | 40000、查询失效 |
| FEINT-09 | P1 | 204 空响应被误判为失败 | 登出、撤销会话报错 |
| FEINT-10 | P1 | 401 双提示、`trace_id` 未利用 | 体验差、排障难 |
| FEINT-11 | P1 | 429 未消费 `Retry-After` | 限流期体验差 |
| FEINT-12 | P1 | 审批模式下直写 40900 未引导 | 操作"莫名失败" |
| FEINT-13 | P1 | 越权对外 404 与 UI 语义 | 误提示"页面不存在" |
| FEINT-14 | P2 | 菜单资源模型缺失 → 已决策**方案 B**（新增 `iam_menu` + `/menus`） | 前端路由来源切换、新增迁移 |
| FEINT-15 | P2 | 会话 / MFA / 审批单 / 数据范围 / 破玻璃页面缺失 | 后端能力不可用 |
| FEINT-16 | P2 | 审计、配置、文件等权限点无端点 | 权限目录"空挂" |
| FEINT-17 | P3 | 契约文档与实现漂移 | 联调误判 |
| FEINT-18 | P3 | 环境默认配置导致 dev 无登录端点 | 联调踩坑 |

判定标准：

- **P0 阻断**：不改则功能完全不可用。
- **P1 高**：能联通但结果错误 / 关键流程体验断裂。
- **P2 缺口**：后端已具备、前端未消费，或缺后端端点。
- **P3 体验 / 文档**：不影响主流程。

---

## 2. 已对齐基线（可直接复用）

### 2.1 响应信封

后端统一信封（`pkg/httpx`，`api/openapi/components.yaml#Envelope`）：

```json
{ "code": 0, "message": "ok", "data": { }, "trace_id": "8f1c9a..." }
```

前端 `defaultResponseInterceptor({ codeField: 'code', dataField: 'data', successCode: 0 })`（`frontend/apps/web-admin/src/api/request.ts:79-83`）在 `code !== 0` 时抛错，`code === 0` 时返回 `data`。**结论：完全兼容，无需适配。**

两处可选优化：

1. 后端错误 `message` 已是人读文本，前端 `errorMessageResponseInterceptor` 的 `makeErrorMessage` 已优先取 `responseData.message`（`request.ts:96-104`），无需改动；但建议追加 `trace_id` 便于反馈工单。
2. 后端从不返回 HTTP 200 + 非 0 业务码；前端的 `throw` 分支只在契约被破坏时触发，属兜底，保留。

### 2.2 业务码与 HTTP 状态

| 业务码 | 含义 | HTTP | 前端应做 |
|---|---|---|---|
| 40000 | 参数错误 | 400 | 展示后端 `message`，定位到具体字段 |
| 40100 | 未认证（不区分过期 / 无效 / 已撤销） | 401 | 先尝试刷新一次，失败则重认证 |
| 40300 | 无权限 / 跨端调用 | 403 | 提示无权限，不跳登录 |
| 40400 | 不存在（**含越权对外表现**） | 404 | 提示"不存在或无权限"，不要展示为"页面 404" |
| 40900 | 冲突（code 重复、版本冲突、审批拦截） | 409 | 按 `message` 分流：刷新数据 / 引导审批 |
| 42900 | 限流 / 配额 | 429 | 读 `Retry-After` 倒计时 |
| 50000 | 内部错误 | 500 | 展示 `trace_id` |
| 50300 | 依赖不可用 | 503 | 提示稍后重试，禁用重试风暴 |

### 2.3 分页

- 请求：`page`（≥1）、`page_size`（默认 20，**上限 100**），越界直接 `40000`，不做静默截断。
- 响应：`data = { items, total }`。

结论：前端 `page` / `pageSize` 的命名必须映射为 `page` / `page_size`（见 FEINT-08）。

### 2.4 请求头与关联 ID

| 头 | 方向 | 说明 |
|---|---|---|
| `Authorization: Bearer <token>` | 请求 | 前端 `formatToken` 已符合 |
| `Accept-Language` | 请求 | 前端已注入 `preferences.app.locale`，后端当前不消费，保留 |
| `X-Request-ID` | 双向 | 后端 `pkg/middleware/request_id.go` 入站即用、响应回写，并作为信封 `trace_id` |
| `traceparent` | 双向 | W3C 追踪，浏览器侧可选，`pkg/tracing` 已支持 |
| `Retry-After` | 响应 | 仅 429 场景（`internal/platform/pipeline.go:157-162`） |

CORS 默认放行 `Authorization`、`Content-Type`、`X-Request-ID`（`pkg/middleware/cors.go:16`），跨域部署时只需补 `AllowOrigins`。

---

## 3. P0 阻断项

### FEINT-01 登录入参字段不符

- **现状**：`LoginParams` 为 `{ username, password }`（`frontend/apps/web-admin/src/api/core/auth.ts:21-24`），登录页表单字段为 `username`。
- **后端**：`LoginRequest` 要求 `{ subject, password }`，且 `additionalProperties: false`（`api/openapi/components.yaml:96-113`），`username` 会被判为未知字段并因缺 `subject` 返回 `40000`。
- **影响**：任何真实后端环境下登录 100% 失败。
- **方案**：在 api 层做一次入参映射，不要改表单模型（避免污染 UI 与后续 Mock 兼容）：

```ts
export async function loginApi(data: { username: string; password: string }) {
  return requestClient.post<unknown>('/admin/v1/auth/login', {
    subject: data.username,
    password: data.password,
  });
}
```

- **验收**：错误密码返回 401 + `40100`；不存在账号同样 401（后端不区分原因）；连续失败触发 429 + `Retry-After`。

### FEINT-02 登录响应三态未处理

- **现状**：`LoginResult` 只有 `accessToken`，登录页拿到响应后直接 `accessStore.setAccessToken(resp.accessToken)`。
- **后端**：`POST /admin/v1/auth/login` 的 `data` 是 `oneOf` 三态（`api/openapi/admin.yaml:83-128`）：

| 形态 | 判别字段 | data 内容 |
|---|---|---|
| 登录成功 | 有 `access_token` | `TokenPair` |
| 需要第二步 MFA | `mfa_required: true` | `mfa_ticket`（5 分钟、一次性） |
| 强制绑定 MFA | `mfa_binding_required: true` | `mfa_setup_ticket`（10 分钟、一次性） |

- **影响**：MFA 账号登录会"成功返回但没有 token"，`authStore.authLogin` 里 `if (accessToken)` 为假 → 不跳转、不报错，表现为**点击登录后静默无反应**；若强行写入 `undefined`，则后续请求全部无 `Authorization` → 401 → 又回到登录页。
- **方案**：登录流程改为显式状态机：

```ts
type LoginOutcome =
  | { kind: 'mfa-challenge'; ticket: string }
  | { kind: 'mfa-binding'; ticket: string }
  | { kind: 'token'; accessToken: string; refreshToken: string; expiresIn: number };

export async function loginApi(data: { username: string; password: string }): Promise<LoginOutcome> {
  const res = await requestClient.post<Record<string, any>>('/admin/v1/auth/login', {
    subject: data.username,
    password: data.password,
  }, { responseReturn: 'raw' }); // 需要同时读取 code 与 data，故取 raw
  const d = res.data ?? {};
  if (d.mfa_binding_required) return { kind: 'mfa-binding', ticket: d.mfa_setup_ticket };
  if (d.mfa_required) return { kind: 'mfa-challenge', ticket: d.mfa_ticket };
  return {
    kind: 'token',
    accessToken: d.access_token,
    refreshToken: d.refresh_token,
    expiresIn: d.expires_in,
  };
}
```

配套 UI（`views/_core/authentication/`）：

1. `mfa-challenge` → 6 位动态码弹窗，提交 `POST /admin/v1/auth/mfa/verify { mfa_ticket, code }`，成功返回 `TokenPair`（**不是 204**）。
2. `mfa-binding` → 引导页：`POST /admin/v1/auth/mfa/setup { mfa_setup_ticket }` 拿到 `otpauth_uri` + `recovery_codes`，二维码展示恢复码下载，再 `mfa/verify { mfa_setup_ticket, code }`，成功直接签发 `TokenPair`。
3. 票据一次性：动态码输错也会消耗票据，UI 必须提供"返回重新登录"兜底。

- **验收**：三类账号（无 MFA / 已绑 MFA / 强制绑定）均能完成登录；任一中间态都不得在本地写入 token。

> 端别差异：B 端只注册 `login` / `refresh` / `logout` / `logout-all`（`internal/identity/transport/biz/router.go:24-27`），**没有 MFA 与会话管理路由**。因此 `web-biz` 的登录恒为单分支，不要照搬 `web-admin` 的三态状态机。

### FEINT-03 snake_case 与双令牌未落库

- **现状**：`AuthApi.LoginResult.accessToken`（camelCase）；`useAccessStore` 虽有 `refreshToken` 字段且已持久化（`frontend/packages/stores/src/modules/access.ts:45,94-96,104-107`），但**全仓库没有任何地方调用** `setRefreshToken`。
- **后端**：`TokenPair` 为 `{ access_token, refresh_token, token_type, expires_in }`（`api/openapi/components.yaml:123-139`）；refresh token 只出现一次，不写 cookie。
- **影响**：refresh token 永久丢失 → 刷新路径不可用；`expires_in` 丢失 → 无法做提前静默刷新。
- **方案**：在 api 层建立唯一的 DTO 适配点，页面/store 只消费 camelCase：

```ts
// src/api/core/auth-adapter.ts
export interface TokenPairVM { accessToken: string; refreshToken: string; expiresAt: number }

export function toTokenPairVM(d: any): TokenPairVM {
  return {
    accessToken: d.access_token,
    refreshToken: d.refresh_token,
    expiresAt: Date.now() + Number(d.expires_in ?? 0) * 1000,
  };
}
```

登录成功统一走 `authStore.applyTokens(vm)`：`setAccessToken` + `setRefreshToken` +（可选）`expiresAt` 持久化。

- **验收**：刷新浏览器后自动登录态仍在；`localStorage` 中同时存在 access / refresh 两个令牌。

### FEINT-04 刷新协议不符

- **现状**：

```ts
// auth.ts:49-57
export async function refreshTokenApi() {
  return baseRequestClient.post('/auth/refresh', undefined, { withCredentials: true });
}
```

- **后端**：`POST /admin/v1/auth/refresh` 要求 body `{ refresh_token }`（`RefreshRequest`，`additionalProperties: false`），**一次性轮换**：成功后旧令牌立即失效，已轮换令牌再次出现视为重放并**撤销整条会话链**（`api/openapi/admin.yaml:129-141`）。
- **额外缺口**：`preferences.app.enableRefreshToken` 默认为 `false`（`frontend/packages/@core/preferences/src/config.ts:25`），`authenticateResponseInterceptor` 在关闭时收到 401 直接 `doReAuthenticate()` 踢登录（`preset-interceptors.ts:69-72`）。
- **影响**：access token 过期（默认寿命短）即被强制登出；即使打开开关，也会因缺 body 返回 40000。
- **方案**：

```ts
// api/core/auth.ts
export async function refreshTokenApi(refreshToken: string) {
  return baseRequestClient.post<{ code: number; data: any }>('/admin/v1/auth/refresh', {
    refresh_token: refreshToken,
  });
}

// api/request.ts
async function doRefreshToken() {
  const accessStore = useAccessStore();
  const rt = accessStore.refreshToken;
  if (!rt) throw new Error('no refresh token');
  const res = await refreshTokenApi(rt);
  const vm = toTokenPairVM(res.data);        // 必须同时替换两个令牌
  accessStore.setAccessToken(vm.accessToken);
  accessStore.setRefreshToken(vm.refreshToken);
  return vm.accessToken;
}
```

并在 `preferences.ts` 显式打开：`app: { enableRefreshToken: true }`。

三条硬性约束（否则会把用户"刷出登录"）：

1. **单飞**：同一时刻只允许一次 refresh。Vben 已有 `client.isRefreshing` + 队列（`preset-interceptors.ts:74-104`），保持不变，禁止在业务代码里再调 `refreshTokenApi`。
2. **原子落库**：两个新令牌必须在同一次赋值内完成，不能只更新 access。
3. **重放防护**：刷新失败（401）时清空双令牌并走重认证，不要重试第二次（重试会触发链撤销，影响其他标签页）。

- **验收**：access 过期后并发 5 个请求只发起 1 次 refresh，全部重放成功；手工重放旧 refresh token 后整条会话失效。

### FEINT-05 无用户资料端点

- **现状**：`getUserInfoApi()` 调 `GET /user/info`（`frontend/apps/web-admin/src/api/core/user.ts`），由 `authStore.authLogin` 与 `getAccessCodesApi` 一起 `Promise.all` 调用（`src/store/auth.ts:43-46`）。
- **后端**：不存在 `/user/info`（A 端身份域仅有 `auth/`*，IAM 仅有 `iam/`*），必然 404。
- **影响**：修好令牌映射（FEINT-03）后，`accessToken` 有值 → 进入 `Promise.all` → `/user/info` 404 直接让 `authLogin` 抛出，登录页提示失败且不跳转。也就是说**令牌问题掩盖了资料问题**，两者必须同批修复。
- **方案（直接新增 `GET /{channel}/v1/me`）**：不做"前端拼装"过渡，后端补一个「当前主体」端点，前端只消费单一来源。理由：拼装方案会让 `roles` / `subject` 永远依赖 IAM 快照，一旦某个账号尚未建绑定（`roles=[]`）前端就无法区分"没有角色"与"查询失败"，而 `/me` 能给出明确身份语义。

端点与鉴权（与 `/iam/me/permissions` 同级：仅认证，不要求权限点）：

| 端 | 路径 | 说明 |
|---|---|---|
| A | `GET /admin/v1/me` | 平台员工 |
| B | `GET /biz/v1/me` | 商户员工；`merchant_id` 由令牌推导，绝不接受请求参数 |

响应 `data`（首版字段全部来自 `identity_account`）：

```json
{
  "subject_id": "1001",
  "subject": "alice@example.com",
  "channel": "admin",
  "merchant_id": "",
  "status": "active",
  "mfa_enabled": true,
  "has_password": true,
  "phone_bound": false,
  "password_updated_at": "2026-09-30T08:00:00Z",
  "must_change_password": false,
  "token_version": 3
}
```

字段来源与约束：

| 字段 | 来源 | 说明 |
|---|---|---|
| `subject_id` | `Account.ID` | 与令牌 `sub` 一致 |
| `subject` | `Account.Subject` | 规范化登录主体（trim + 小写），前端作 `username` |
| `display_name` | `Account.DisplayName` | 昵称，可空串；为空时前端回退 `subject` 展示。A/B 两端均支持 `PATCH /{channel}/v1/me` 修改，长度 ≤ 64 rune |
| `channel` | `Account.Channel` | `admin` / `biz` |
| `merchant_id` | `Account.MerchantID` | B 端非空，A 端恒为空串（禁止用空串表示全部商户） |
| `status` | `Account.Status` | `pending` / `active` / `locked` / `disabled` |
| `mfa_enabled` | `MFAEnrollment.IsConfirmed()` | 前端据此决定是否提示绑定 |
| `has_password` | `identity_credential` 中是否存在 `password` | 安全设置页展示「已设置 / 未设置」 |
| `phone_bound` | `identity_credential` 中是否存在 `phone` | 手机号只存不可逆摘要，**任何响应都不回显号码** |
| `password_updated_at` | `Account.PasswordUpdatedAt` | 从未改密时省略该字段 |
| `must_change_password` | `Account.MustChangePassword` | 为 true 时前端须引导改密 |
| `token_version` | `Account.TokenVersion` | 只读，排查令牌失效用 |

> 2026-10-30 增补：后 4 个字段由 FEREV-03（安全设置页硬编码假数据）带出。系统无 email 与密保问题数据模型，安全设置页对这两项只能显示「暂未开通」。

**不放进 `/me` 的字段**：`roles` / `permissions` / `data_scopes` 仍走 `/iam/me/permissions`。原因：`internal/identity/transport/rest` 当前只依赖身份用例（`handler.go:27-45`），把 IAM 快照注进来会让身份域反向依赖权限域。前端两次并行请求即可，`authStore` 已是 `Promise.all` 结构（`src/store/auth.ts:43-46`）。

**展示字段的补齐进度**：迁移 `000007_alter_identity_account_profile` 已落地 `display_name`（`VARCHAR(64) NOT NULL DEFAULT ''`），`/me` 已返回该字段，并新增 `PATCH /{channel}/v1/me` 允许本人修改昵称（`MeUpdateRequest.display_name`，成功返回更新后的 `MeView`）。`email` 与 `avatar_url` 仍无数据模型，前端继续用「空串 + 首字母兜底」而非外链默认头像。

**`PATCH /{channel}/v1/me` 约束**（A 接口 6.3.0 / B 接口 5.2.0）：

- 与 `GET /me` 同级：仅认证，不要求权限点；端别由路由决定，请求体不接受 `channel` / `merchant_id` / `subject`。
- 只接受 `display_name` 一个字段；`strings.TrimSpace` 后写入，空串表示清除昵称；> 64 rune 或含控制/不可见字符返回 `400` / `40000`。
- 不撤销会话、不递增 `token_version`（昵称不是安全属性）；审计写 `auth.profile.updated`，`attrs.changed_fields="display_name"`，**不记录昵称原文**。
- 响应为更新后的完整 `MeView`，前端无需二次 `GET`。

后端落地清单（复用现有分层，不新建域）：

1. `internal/identity/app`：新增 `Me(ctx, actor)` 用例 —— `AccountRepository.FindByID(principal.SubjectID)`（`domain/repository.go:16-23`）+ `MFAEnrollmentRepository.FindByAccount`（`domain/repository.go:61-68`）。
2. 不变量校验：`account.Channel == handler.channel`、未软删、状态非 `disabled`；任一不满足统一返回 `40100`（不区分原因，避免泄露账号是否存在）。
3. `internal/identity/transport/rest`：新增 `Me` handler + DTO，`Handler` 保持只依赖一个用例门面。
4. 路由：`transport/admin/router.go` 增 `GET /admin/v1/me`、`transport/biz/router.go` 增 `GET /biz/v1/me`，均挂 `authMW`。
5. 契约：`api/openapi/{admin,biz}.yaml` 增路径，`schemas/{admin,biz}/me.yaml` 定义 `MeView`，`make openapi` 通过。

> 本地注意：`auth.mode=demo` 时令牌形如 `admin.<id>`，`/me` 会用 `<id>` 去 `identity_account` 查账号。内存库没种子数据时一律 `40100`，所以「手工塞 demo token 调 `/me`」走不通；前端联调必须走 `identity.enabled=true` 的真实登录（见 §5）。

前端：

```ts
// api/core/user.ts：web-admin 用 /admin/v1/me，web-biz 用 /biz/v1/me
export interface MeView {
  subject_id: string;
  subject: string;
  display_name: string;
  channel: 'admin' | 'biz';
  merchant_id: string;
  status: 'active' | 'disabled' | 'locked' | 'pending';
  mfa_enabled: boolean;
  has_password: boolean;
  phone_bound: boolean;
  password_updated_at?: string;
  must_change_password: boolean;
  token_version: number;
}

// 昵称唯一可写字段；返回更新后的 MeView，前端据此同步 userStore
export function updateMyProfileApi(data: {
  display_name: string;
}): Promise<MeView> {
  return requestClient.request<MeView>('/me', { method: 'PATCH', data });
}

export async function getUserInfoApi(): Promise<UserInfo> {
  const me = await requestClient.get<MeView>('/admin/v1/me');
  return {
    userId: me.subject_id,
    username: me.subject,
    realName: me.display_name || me.subject,
    avatar: '',
    roles: [], // 由 getAccessCodesApi 返回的 roles 合并，见下
    desc: '',
    homePath: '/system/role',
  };
}
```

`roles` 若要在界面展示，建议把 `getAccessCodesApi` 升级为返回完整快照（`{ roles, permissions, data_scopes, snapshot_version }`），`authStore` 一次拿到 `roles` 与 `permissions`，不额外增加请求（见 FEINT-06）。

- **验收**：
  - 登录后 `/me` 与 `/iam/me/permissions` 均 200，`userId` 与令牌 `sub` 一致。
  - 账号停用 / 改密后，旧令牌访问 `/me` 返回 `40100`（与撤销检查一致），不得返回 200 + `status=disabled`。
  - B 端 `/biz/v1/me` 的 `merchant_id` 恒等于令牌商户；请求附带的 `merchant_id` 参数被忽略，不参与任何查询条件。
  - `roles` / `permissions` 来自同一 `snapshot_version`，不出现"菜单可见但按钮无权限"的撕裂。

---

## 4. P1 高优先项

### FEINT-06 权限码端点错误

- **现状**：`getAccessCodesApi()` → `GET /auth/codes`（`auth.ts:71-73`）。
- **后端**：不存在该端点。正确来源为 `GET /admin/v1/iam/me/permissions`（仅认证，不额外要求权限点，`internal/iam/transport/admin/router.go:71-72`），取 `data.permissions`；B 端对应 `/biz/v1/iam/me/permissions`。
- **方案**（一次调用同时产出 `permissions` 与 `roles`，避免与 `/me` 重复请求）：

```ts
export interface SubjectPermissionsView {
  channel: 'admin' | 'biz';
  subject_type: 'staff' | 'merchant_staff';
  subject_id: string;
  merchant_id?: string;
  roles: string[];
  permissions: string[];
  data_scopes: Array<{ resource: string; scope_type: string }>;
  snapshot_version: number;
  generated_at: string;
}

// api/core/auth.ts：web-admin 用 /admin/v1/...，web-biz 用 /biz/v1/...
export async function getSubjectPermissionsApi() {
  return requestClient.get<SubjectPermissionsView>('/admin/v1/iam/me/permissions');
}

export async function getAccessCodesApi() {
  const snapshot = await getSubjectPermissionsApi();
  return snapshot.permissions;
}
```

`authStore` 侧：`userStore.setUserInfo({ ...userInfo, roles: snapshot.roles })`，`accessStore.setAccessCodes(snapshot.permissions)`。若不想在 store 里多存一份快照，可只保留 `getAccessCodesApi()`，`roles` 留空（`accessMode: 'frontend'` 下路由过滤只比较 `accessCodes`，不依赖 `roles`）。

- **注意**：`web-biz` 必须使用 `/biz/v1/iam/me/permissions`，两端的 api 文件不要复用同一常量。
- **按钮显隐写法**：并集（任一权限即可见）用 `v-access:code="['iam:role:write', 'iam:role:grant']"`；
  交集（全部权限才可见）用 `v-access:code.all="['iam:role:write', 'iam:role:grant']"` 或
  `<AccessControl mode="all" :codes="[...]" />`。页面内禁止裸写 `hasAccessByCodes(['a']) && hasAccessByCodes(['b'])`。
- **验收**：无权限按钮隐藏（`v-access:code`）与后端 403 表现一致；权限变更后（`snapshot_version` 递增）刷新页面即生效；页面加载只发起一次 `/iam/me/permissions`。

### FEINT-07 角色 DTO / 枚举 / 乐观锁不符

前端 mock 与后端契约差异（`api/openapi/admin.yaml:1531-1598`）：

| 字段 | 后端 | 前端现状 | 处理 |
|---|---|---|---|
| `id` | string | number（mock） | TS 类型放宽为 `string` |
| `code` | string（端内唯一） | 无 | 新增列/表单项 |
| `name` | string | 有 | 保留 |
| `status` | `active` \| `disabled` | `0` \| `1` | 枚举映射 + Tag 渲染 |
| `role_type` | `system` \| `custom` \| `breakglass` | 无 | 增加列；`system`/`breakglass` 禁止编辑 |
| `version` | int64（乐观锁） | 无 | 必须回传 |
| `permissions` | `string[]` | 无（mock 用菜单树） | 授权弹窗改为权限点勾选 |
| `data_scopes` | `DataScope[]`（`{resource, scope_type, merchant_ids?}`） | 无 | 新增"数据范围"页签 |
| `binding_count` | number（仅详情） | 无 | 删除前提示"已绑定 N 人" |
| `created_at` / `updated_at` | RFC3339 | `createTime` | 展示层格式化 |
| `remark` | **不存在** | 有 | 改用 `description` |

写接口约束：

- `PATCH /admin/v1/iam/roles/{id}`、`PUT /admin/v1/iam/roles/{id}/permissions`、`DELETE .../{id}` 都要求版本号，`body.version` 与 `If-Match` 二选一，二者不一致返回 `40000`（`internal/iam/transport/rest/handler.go:654-672`）。
- 版本冲突返回 `40900`，语义为"数据已被他人修改"，前端应提示并刷新详情，而不是重试同一请求。
- `status=disabled` 属高风险变更，`iam.security.require_reason=true` 时 `reason` 必填，前端表单需动态注入"变更原因"。

建议封装：

```ts
export async function updateRoleApi(id: string, body: Record<string, any>, version: number) {
  return requestClient.patch(`/admin/v1/iam/roles/${id}`, { ...body, version });
}
```

- **验收**：两窗口并发改名，后提交者看到 40900 明确提示；`role_type=system` 的编辑入口禁用。

### FEINT-08 分页参数名与上限

- **现状**：Ant Design Vue 表格查询参数直传 `page` + `pageSize`。
- **后端**：`page` + `page_size`，默认 20，**>100 直接 40000**（`docs/API_STANDARDS.md` 分页约束）。
- **影响**：`pageSize` 被忽略 → 永远返回 20 条；若下拉含 200/500 → 40000。
- **方案**：在 api 层统一映射，UI 侧限制 `pageSizeOptions` ≤ 100：

```ts
function toPageQuery(q: { page?: number; pageSize?: number; [k: string]: any }) {
  const { page = 1, pageSize = 20, ...rest } = q;
  return { page, page_size: Math.min(pageSize, 100), ...rest };
}
```

同一个坑还出现在"权限点选择器"：后端一次最多返回 100 条权限点，超过需分页遍历或改用树接口，不要用一次性 `page_size=1000`。

- **验收**：切换每页条数生效；`page_size=101` 被前端夹紧而非报错。

### FEINT-09 204 空响应被误判为失败

- **现状**：`defaultResponseInterceptor` 在 `responseReturn: 'data'` 下对 204 响应执行 `responseData[codeField]`（`responseData` 为空串），`undefined !== 0` 于是抛错。
- **后端**：`POST /auth/logout`、`POST /auth/logout-all`、`POST /auth/sessions/revoke-all`、`DELETE /auth/sessions/{id}`、`POST /auth/mfa/disable`、`mfa/verify`（已登录确认绑定分支）均为 **204 无响应体**。
- **影响**：登出会抛异常并弹"服务器错误"；撤销会话同理，且未走完 `authStore.logout` 的清理流程。
- **方案**：这些调用显式声明 `responseReturn: 'raw'`，或统一改用 `baseRequestClient` 并手动带 `Authorization`（因为 `baseRequestClient` 没有请求拦截器，**不会自动加 token**）：

```ts
export async function logoutApi() {
  const accessStore = useAccessStore();
  return baseRequestClient.post('/admin/v1/auth/logout', undefined, {
    headers: { Authorization: `Bearer ${accessStore.accessToken}` },
  });
}
```

更稳妥的做法是给 `requestClient` 增加一个 `emptyBody: true` 的调用约定，在拦截器里对 204 直接 `return null`，避免每处调用点都记一遍。

- **验收**：登出无错误提示；`mfa/disable` 成功后会话被撤销（其他标签页 401）。

### FEINT-10 401 双提示与 trace_id 未利用

- **现状**：401 由 `authenticateResponseInterceptor` 处理（重认证 / 刷新），但随后 `errorMessageResponseInterceptor` 仍会 `message.error(responseData.message)`，同时 `loginExpiredMode='modal'` 会弹过期弹窗 → **双反馈**。
- **后端**：401 语义统一为 `40100`，不区分过期/无效/撤销（安全要求，`docs/API_STANDARDS.md`），前端无法也不应据此做精细分支。
- **方案**：
  1. 在 `makeErrorMessage` 中对 401（以及已被认证拦截器消费的错误）直接 `return`，跳过 toast。
  2. 错误提示统一追加 `trace_id`：`message.error(`${msg}（trace: ${trace_id}）`)`，或提供"复制错误信息"按钮，便于对照后端日志。
  3. 500/503 场景展示 `trace_id`，400/409 仅在控制台输出。
- **验收**：token 过期时只出现一次"登录已过期"弹窗；任意错误可在后端按 `trace_id` 检索到同一条访问日志。

### FEINT-11 429 未消费 Retry-After

- **后端**：限流响应为 HTTP 429 + `Retry-After: <秒>` + `42900` 信封（`internal/platform/pipeline.go:157-162`）；登录、MFA 校验、短信发送均有限流保护。
- **前端**：当前仅弹通用错误，用户会连续重试，进一步延长封禁。
- **方案**：登录 / MFA 校验 / 短信等敏感按钮在 429 时进入倒计时禁用态：

```ts
const retryAfter = Number(error?.response?.headers?.['retry-after'] ?? 30);
startCountdown(retryAfter, '尝试过于频繁，请 %s 秒后重试');
```

- **验收**：连续输错密码后按钮倒计时，倒计时结束可再次提交。

### FEINT-12 审批模式下直写 40900 未引导

- **后端**：`iam.approval.enabled=true` 时，7 个角色/绑定直写端点统一返回 `40900`，提示走 `/change-requests`（`docs/rbac-iam.md` M4.7）。这是"前置拦截"，不是并发冲突。
- **前端**：所有 40900 都按"冲突"处理，用户会误以为数据过期，反复刷新重试。
- **方案**：按 `40900` + `message` 关键字（或后端补充稳定子码）分流：

| 场景 | 识别 | UI |
|---|---|---|
| 版本冲突 | `version` 相关 message | 提示刷新详情 |
| code 重复 | `duplicate` / `已存在` | 聚焦 code 字段 |
| 审批拦截 | 含 `change-requests` | 弹窗引导"提交审批单"，并展示 `action` 映射 |

- **建议**：请后端在审批拦截的 `message` 中稳定包含可解析标识（如 `approval required`），前端不基于中文文案做判断。
- **验收**：开启审批的实例上，编辑角色后弹"已提交审批"而非"保存失败"。

### FEINT-13 越权对外 404 与 UI 语义

- **后端**：`40400` 同时表示"不存在"与"越权对外表现"（`docs/API_STANDARDS.md:98`；B 端跨商户优先 404）。
- **前端**：全局 404 处理会跳"页面不存在"，对"无权限访问某个角色详情"给出错误心智模型。
- **方案**：详情 / 操作类请求的 404 提示统一为"资源不存在或无权限访问"，不跳全局 404 页；仅路由级 404 才展示页面。
- **验收**：B 端用另一个商户的角色 ID 访问详情，提示文案正确且不泄露存在性。

---

## 5. 环境能力矩阵（联调必读）

配置来源：`deploy/helm/admin-frame/values{,-dev,-staging,-prod}.yaml`

| 环境 | `auth.mode` | `identity.enabled` | `iam.mode` | 前端能否真实登录 | 权限判定 |
|---|---|---|---|---|---|
| local（`configs/*.yaml`） | `demo` | 未声明 → 默认 `false`（`internal/platform/config.go:73-78`） | 未声明 → 默认 `off`（`config.go:1043-1050`） | **不能**：`/auth/`* 路由未注册（`cmd/admin-api/register.go:21-23`） | 静态权限表（`internal/security/authz.go:127-129`） |
| dev（`values-dev.yaml`） | `demo` | `false` | `off` | **不能**，同上 | 静态权限表 |
| staging | `jwt` | 需显式开启 | `shadow` | 可以 | 快照判定 + shadow 差异观测（不改变结果） |
| prod | `jwt` | 需显式开启 | `enforce` | 可以 | 快照强制判定 |

三种联调方式（按成本从低到高）：

1. ~~**保持 Nitro Mock**（`VITE_NITRO_MOCK=true`）：只验证 UI，不验证契约~~ —— **已于 2026-10-31 移除**：`apps/backend-mock` 删除、`VITE_NITRO_MOCK` 下线，该形态不再可用，前端必须对接真实后端。
2. **demo token 直连**：`auth.mode=demo` 下通过 `useAccessStore().setAccessToken('admin.u-1')` 写入演示令牌，可验证 IAM 只读接口，但**无法验证登录/刷新**。
3. **本地全量**：`identity.enabled=true` + `auth.mode=jwt` + `iam.mode=off`（内存库即可），这是唯一能覆盖登录三态、刷新轮换、MFA 的形态，建议作为前端联调基线。

补充：dev 默认 `iam.mode=off` 时 `/iam/me/permissions` 仍返回真实快照（它只做认证不做权限点校验），但写入接口的授权判定走静态表 —— 这会造成"本地能改、prod 403"的错觉，联调时需知悉。

---

## 6. P2 能力缺口

### 前端待建（后端已就绪）

| ID | 能力 | 后端现状 | 前端现状 | 建议 |
|---|---|---|---|---|
| GAP-02 | MFA 绑定 / 解绑 | A 端 `auth/mfa/*` 三端点完整（B 端不提供） | 无 | 仅 `web-admin`：用户设置页 + 登录态强制绑定引导 |
| GAP-03 | 会话管理 | A 端 `GET /auth/sessions`、`POST /sessions/revoke-all`、`DELETE /sessions/{id}`（B 端不提供） | 无 | 仅 `web-admin`："登录设备"页，支持踢下线 |
| GAP-04 | 审批单 | `iam/change-requests`（创建/列表/详情/审批/取消） | 无 | 审批中心页，`action` 驱动动态表单 |
| GAP-05 | 破玻璃 | `iam/breakglass/*` | 无 | 高权限应急入口，需二次确认 + 原因 |
| GAP-06 | 数据范围编辑 | `DataScope[]` 契约完整 | 无 | 角色授权页签，`merchant_set` 需商户选择器（≤100） |
| GAP-07 | 权限目录 | `iam/permissions`（分页 / 按端过滤） | 无 | 授权弹窗数据源，按 `resource` 分组（目录已平铺） |
| GAP-08 | 绑定管理 | `iam/bindings` CRUD + 过期时间 | 无 | 成员授权页 |

### 后端待补（前端已有消费位）

| ID | 缺口 | 现状 | 建议契约 |
|---|---|---|---|
| GAP-01 | 用户资料 | **已完成**：`GET /admin/v1/me`（A）/ `GET /biz/v1/me`（B）；`display_name` 与 `PATCH` 已于 2026-10-30 补齐（A 6.3.0 / B 5.2.0） | `GET /{channel}/v1/me` → `{subject_id, subject, display_name, channel, merchant_id, status, mfa_enabled, has_password, phone_bound, password_updated_at, must_change_password, token_version}`；仅认证、不含 roles/permissions。`PATCH /{channel}/v1/me` 仅接受 `display_name`。`avatar_url` 仍无数据模型，前端走首字母兜底。详见 §3 FEINT-05 |
| GAP-09 | 菜单资源 | 无菜单表、无菜单端点 | **已决策方案 B**：新增 `iam_menu` + `GET /{channel}/v1/menus`，前端切 `accessMode: backend`。详见 §6「GAP-09 菜单：方案 B 设计」 |
| GAP-10 | 审计查询 | 权限点 `audit:read` 已存在，无端点 | `GET /admin/v1/audit/events?action&subject&from&to` |
| GAP-11 | 配置中心 | 权限点 `config:read/write` 已存在，无端点 | `GET/PUT /admin/v1/configs/{key}`，写入走审批 |
| GAP-12 | 文件上传 | `pkg/storage`（S3/OSS）已就绪，`api/openapi` 无上传端点 | `POST /{channel}/v1/files` 预签名直传，前端已有 `useUpload` 能力可接 |

> 说明：`order:*`、`data:export` 等权限点属于业务示例，不计入缺口。

### GAP-09 菜单：方案 B 设计（已选）

已决策：**新增菜单资源 + `GET /{channel}/v1/menus`，前端切 `accessMode: 'backend'`**。

**为什么不能复用 `iam_permission` 承载菜单**：权限目录已收敛为平铺结构，只保留 `code / name / channel / resource / data_bearing / sort / status`，没有 `component`、`icon`、`route_name`、`redirect` 等路由必需字段；一个权限点也可能对应多个菜单。强行复用会让权限目录承担两种语义。

**数据模型（新增迁移 `000006_create_iam_menu`，与模块基线 `000005` 之后顺延；若资料列先合入则本迁移顺延为 `000007`，编号按合入顺序分配）**：

| 列 | 类型 | 说明 |
|---|---|---|
| `id` | BIGINT | 雪花 ID |
| `channel` | VARCHAR(16) | `admin` / `biz` / `both` |
| `code` | VARCHAR(128) | 菜单唯一标识，如 `system.role` |
| `parent_code` | VARCHAR(128) | 父菜单 code，根为空串 |
| `permission_code` | VARCHAR(128) | 可见所需权限点，空串=登录即可见；引用 `iam_permission.code` |
| `name` | VARCHAR(128) | 菜单名（前端 `meta.title`） |
| `route_path` | VARCHAR(256) | 路由 path，如 `/system/role` |
| `component` | VARCHAR(256) | 视图相对路径，如 `/system/role/index`（不含 `.vue`，需与前端 `pageMap` 键一致） |
| `icon` | VARCHAR(64) | 图标名（前端图标注册表 key） |
| `sort` | INT | 同级升序 |
| `status` | VARCHAR(16) | `active` / `disabled` |
| `created_at` / `updated_at` | DATETIME(3) | UTC |

**端点**：`GET /admin/v1/menus`、`GET /biz/v1/menus`，仅认证（返回结果本身已按权限裁剪，不再额外要求权限点）。

响应直接对齐 vben 的 `RouteRecordStringComponent`，避免前端再做一次结构转换：

```json
[
  {
    "name": "System",
    "path": "/system",
    "component": "BasicLayout",
    "redirect": "/system/role",
    "meta": { "title": "系统管理", "icon": "lucide:settings", "order": 100 },
    "children": [
      {
        "name": "SystemRole",
        "path": "/system/role",
        "component": "/system/role/index",
        "meta": { "title": "角色管理", "authority": ["iam:role:read"] }
      }
    ]
  }
]
```

后端裁剪规则（必须由服务端保证，前端不做二次过滤）：

1. 只返回 `channel in (当前端, 'both')` 且 `status='active'` 的菜单；
2. `permission_code` 非空时，要求该码出现在当前主体有效权限中（复用 IAM 快照，缓存键带 `snapshot_version`）；
3. 任一子节点可见则保留父节点，全部不可见的子树整体剔除，不返回空壳父级；
4. `component` 只能取库内值，不接受客户端传入；后端不做路径拼接，存在性校验放前端（见下）。

**前端切换要点**：

- `preferences.ts`：`accessMode: 'backend'`，并注入 `fetchMenuListAsync: () => getMenusApi()`。
- `src/router/routes/index.ts`：只保留 `core.ts`（Root + Login），**删除** `modules/system.ts`、`modules/member.ts`，否则与后端菜单重复注册同一 path。
- `defaultHomePath` 必须指向菜单里真实存在的 path，建议后端在根菜单上给 `redirect`，避免登录后落到空白页。
- **静默降级风险**：`generateRoutesByBackend` 在 `pageMap` 找不到 component 时只 `console.error` 并回退 `not-found` 页（`frontend/packages/utils/src/helpers/generate-routes-backend.ts:85-89`），线上表现就是"菜单点进去 404"。必须加一道开发期强校验：逐条确认 component 命中 `pageMap`，未命中直接 `throw`。
- `accessCodes` 仍取 `/iam/me/permissions`：菜单决定"能不能看到"，权限码决定"按钮能不能点"，两套数据都要有。

**成本与拆分**：迁移 + 端点 + 种子（A 端 2 条示例菜单）约 3 天；前端切换 + 校验 + 清理静态模块约 1 天；菜单 CRUD 管理页后置（先靠种子迁移维护）。

**验收**：

- 无 `iam:role:read` 的账号登录后，`/menus` 不返回"角色管理"，且不返回空的"系统管理"父节点。
- 把 DB 中某条 `component` 改成不存在的路径，开发环境启动即报错，而不是静默 404。
- 切换后 `web-admin` 仍只有 1 个业务菜单可正常进出，`web-biz` 同理（两端菜单数据隔离，B 端不得出现 A 端菜单）。
- 菜单请求结果随 `snapshot_version` 变化失效，角色调整后刷新页面即生效。

---

## 7. 契约一致性缺陷

**决策：CON-01、CON-02、CON-04 本次一并修正（已落地到 `api/openapi` 与 `docs/API_STANDARDS.md`）；CON-03 只涉及前端类型建模，不改契约。**

| ID     | 位置                                                      | 问题                                                                         | 修正                                                                         |
| ------ | ------------------------------------------------------- | -------------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| CON-01 | `api/openapi/admin.yaml:1406-1409`、`biz.yaml:1015-1018` | `bearerAuth` 描述仍为"演示 Token：admin."，但 staging/prod 使用 JWT（`auth.mode: jwt`） | 改为"A 端 JWT（Bearer），权限以 IAM 快照为准"，demo 形态仅在 description 备注                  |
| CON-02 | `api/openapi/components.yaml:87-95`                     | `PageMeta` 只声明 `total`，缺 `items`                                           | 补 `items: {type: array}` 或删掉该 schema（各端已自建 `XxxListData`），避免代码生成器产出不完整分页类型 |
| CON-03 | `api/openapi/admin.yaml:83-95`                          | 登录成功响应用 `oneOf` 三态，生成器通常退化为 `any`                                          | 前端显式定义判别联合（FEINT-02），并把"三态判别"写入契约注释（已具备，保持）                                |
| CON-04 | `docs/API_STANDARDS.md`                                 | 未说明 204 端点清单，前端易误用 `responseReturn: 'data'`                                | 增补"空响应端点清单"与前端调用约定                                                         |

---

## 8. 实施路线图

### 阶段 0：契约与决策（0.5 天）

- §7 四项契约修正：**已确认**（CON-01/02/04 本次落地，CON-03 仅前端类型）。
- **已决策**：GAP-01 走「新增 `GET /{channel}/v1/me`」，不做前端拼装 → 阶段 1 含后端改动，需后端先合入 `/me`（A/B 两端各 1 个端点 + `MeView` schema）。
- **已决策**：GAP-09 走**方案 B**（新增 `iam_menu` + `GET /{channel}/v1/menus` + 前端切 `accessMode: backend`），见 §6「GAP-09 菜单：方案 B 设计」。

### 阶段 1：打通登录与鉴权（2～3 天前端 + 1 天后端，P0）

0. **后端前置**：按 §3 FEINT-05 落地 `GET /admin/v1/me`、`GET /biz/v1/me`（`Me` 用例 + handler + 路由 + OpenAPI），前端可并行开发。
1. `web-admin` / `web-biz` 各自的 `src/api/core/auth.ts` 按通道拆分（`/admin/v1` vs `/biz/v1`）。
2. `loginApi` 入参映射 + 三态返回（FEINT-01/02）。
3. `toTokenPairVM` 适配 + `authStore` 双令牌写入（FEINT-03）。
4. `refreshTokenApi` 改 body 传参 + 打开 `enableRefreshToken` + `doRefreshToken` 原子替换（FEINT-04）。
5. `getUserInfoApi` 改 `GET /{channel}/v1/me`（FEINT-05）；`getAccessCodesApi` 升级为返回完整快照（FEINT-06）。
6. `logoutApi` 修 204（FEINT-09）。

**验收（必须全部通过）**：真实后端（`identity.enabled=true` + `auth.mode=jwt`）下

- 无 MFA 账号：登录 → 首页 → 刷新页面 → 仍在线 → 登出（无报错）。
- MFA 账号：登录 → 动态码 → 首页；错误码提示"动态码错误或票据已失效"。
- 强制绑定账号：登录 → 扫码绑定 → 直接进首页。
- 过期场景：手工把 access token 改坏 → 自动刷新一次 → 请求成功且只刷新一次。
- 越权场景：无权限账号访问角色页 → 403 提示，不跳登录。
- 上述 MFA / 强制绑定三条仅适用于 `web-admin`；`web-biz` 只需验证「单步登录 → 首页 → 刷新 → 登出」。

### 阶段 2：业务模块对齐（3～5 天，P1）

- 角色列表 / 表单按 FEINT-07 重做（字段、枚举、`version`、`reason`）。
- 分页与查询参数统一走 `toPageQuery`（FEINT-08）。
- 错误处理统一：401 去重、`trace_id` 展示、429 倒计时、40900 三分流、404 语义（FEINT-10～13）。
- B 端成员模块按同一套规范落地（`/biz/v1/iam/*`）。

### 阶段 3：治理能力补齐（按需排期，P2）

- 授权弹窗（权限目录树）+ 数据范围页签 → 优先生命周期闭环。
- 审批中心 + 破玻璃入口（取决于 `iam.approval.enabled` / `breakglass.enabled` 是否在目标环境开启）。
- 会话管理页、MFA 设置页。

### 阶段 4：后端补端点（与阶段 3 并行）

- GAP-01 已前移至阶段 1（阻塞登录闭环）；资料增强部分**已完成**：迁移 `000007` 增加 `display_name`，`/me` 返回 `display_name` 并新增 `PATCH /{channel}/v1/me`。`avatar_url` 无数据模型，本期不做（个人中心不上传入口）。
- GAP-09 按 §6「GAP-09 菜单：方案 B 设计」落地（独立阶段，见下）。
- GAP-10～12 按业务优先级。

### 阶段 5：菜单方案 B（可与阶段 2 并行）

1. 迁移 `000006_create_iam_menu`（up/down 配对）+ 种子：A 端 `system.role`、B 端 `member.list` 各 1 条示例菜单，`component` 与前端 `pageMap` 键严格一致。
2. `internal/iam`（或独立 menu 包）：菜单查询用例 —— 按 `channel` + 有效权限裁剪、按 `parent_code` 组树、结果带 `snapshot_version` 供缓存失效。
3. transport：`GET /admin/v1/menus`、`GET /biz/v1/menus`，仅认证；OpenAPI 增 `MenuTreeNode` schema。
4. 前端：`accessMode: 'backend'` + `fetchMenuListAsync`，删除 `modules/system.ts` / `modules/member.ts`，补 component 存在性强校验。
5. 菜单 CRUD 管理页后置（先用种子迁移维护枚举），避免阶段 5 无限膨胀。

---

## 9. 联调自检清单（PR 用）

- [ ] 登录请求体只有 `subject` / `password`，无 `channel` / `roles` / `merchant_id`（传了会 40000）
- [ ] 三个登录中间态都不会写入 token
- [ ] access / refresh 令牌成对写入、成对清除
- [ ] 刷新只在 `doRefreshToken` 内发生，业务代码不直接调用
- [ ] 分页参数为 `page` / `page_size` 且 ≤ 100
- [ ] 写接口携带 `version`，40900 有明确引导
- [ ] 204 端点使用 `responseReturn: 'raw'`（或统一空响应约定）
- [ ] 401 只提示一次；500/503 提示附带 `trace_id`
- [ ] 429 有倒计时，未做无脑重试
- [ ] `userId` 来自 `GET /{channel}/v1/me` 的 `subject_id`，与令牌 `sub` 一致；`roles` / `permissions` 走 `/iam/me/permissions`，不重复请求
- [ ] 不存在"前端硬编码后端不存在的端点"（对照 `api/openapi`）
- [ ] `accessMode: backend` 时已删除 `router/routes/modules/*.ts` 静态业务路由，且每条菜单的 `component` 都能命中 `pageMap`（未命中即报错，不落到 `not-found`）
- [ ] 环境变量指向目标环境（`VITE_NITRO_MOCK=false`），未误连 Mock

---

## 10. 附录：字段映射速查

### 认证

| 前端（VM） | 后端（契约） | 备注 |
|---|---|---|
| `username` | `subject` | 请求映射 |
| `accessToken` | `access_token` | 响应映射 |
| `refreshToken` | `refresh_token` | 一次性轮换，必须回写 |
| `expiresIn` → `expiresAt` | `expires_in`（秒） | 建议存绝对时间 |
| — | `token_type` | 恒为 `Bearer`，可忽略 |
| `mfaTicket` | `mfa_ticket` | 5 分钟、一次性 |
| `mfaSetupTicket` | `mfa_setup_ticket` | 10 分钟、一次性 |

### 主体资料（`GET /{channel}/v1/me`）

| 前端 `UserInfo` 字段 | `/me` 字段 | 备注 |
|---|---|---|
| `userId` | `subject_id` | 与令牌 `sub` 一致 |
| `username` | `subject` | 规范化登录主体（trim + 小写） |
| `realName` | `display_name`（空串回退 `subject`） | 昵称可经 `PATCH /me` 修改，见 FEINT-05 |
| `avatar` | —（空串） | 需迁移 `000007` 才有 `avatar_url` |
| `roles` | 取自 `/iam/me/permissions` 的 `roles` | 不由 `/me` 返回 |
| `desc` | —（空串） | 后端无简介字段 |
| `homePath` | — | 前端按应用静态配置（`preferences.app.defaultHomePath`） |
| — | `merchant_id` | B 端由令牌推导，只读 |
| — | `status` | `pending`/`active`/`locked`/`disabled` |
| — | `mfa_enabled` | 决定是否提示绑定 MFA |
| — | `token_version` | 排查令牌失效 |

### IAM

| 前端字段 | 后端字段 | 备注 |
|---|---|---|
| `status: 0/1` | `status: 'active'/'disabled'` | 枚举映射 |
| `createTime` | `created_at` | RFC3339 |
| `remark` | `description` | 后端无 `remark` |
| `menuIds` | `permissions: string[]` | 后端是权限码，不是菜单 ID |
| — | `role_type` | `system`/`custom`/`breakglass` |
| — | `version` | 写接口必传 |
| — | `data_scopes` | 数据范围 |

### 权限码清单（种子，`migrations/000003`）

A 端：`iam:permission:read`、`iam:role:read`、`iam:role:write`、`iam:role:grant`、`iam:binding:read`、`iam:binding:write`、`iam:check`、`iam:change:read`、`iam:change:approve`、`iam:breakglass:request`、`iam:breakglass:approve`、`audit:read`、`config:read`、`config:write`

B 端：`iam:role:read`、`iam:role:write`、`iam:role:grant`、`iam:binding:read`、`iam:binding:write`、`iam:change:request`、`iam:change:review`

两端共用：`order:read`、`order:write`、`file:read`、`file:write`、`data:export`

> 前端按钮权限点必须取自该清单；`v-access:code` 与后端 `Require()` 使用同一字符串，禁止前端自造。
