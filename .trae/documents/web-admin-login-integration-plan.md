# web-admin 登录页对接 admin-frame 身份域实施计划

> 状态：待评审
> 范围：`frontend/apps/web-admin` 登录链路 + 后端 identity 域新增 `GET /admin/v1/me` + A 端 OpenAPI
> 关联：`docs/FRONTEND_INTEGRATION_REVIEW.md`（FEINT-01 ~ FEINT-05）、`docs/API_STANDARDS.md` §4.5
> 决策：仅覆盖 web-admin；补齐 `/admin/v1/me`；MFA 做「两步校验 + 强制绑定」完整闭环

---

## 1. 摘要

把现有「对着 Nitro Mock 的密码登录」替换为 admin-frame 真实登录链路。核心是把登录从「拿到 accessToken 就跳转」改成一个显式状态机：

```text
密码提交
  ├─ TokenPair ──────────────► 落库 access + refresh → 拉 /me + /iam/me/permissions → 跳转
  ├─ MFAChallenge ───────────► 6 位动态码 → /auth/mfa/verify → TokenPair → 同上
  └─ MFABindingRequired ─────► /auth/mfa/setup → 扫码 + 保存恢复码
                               → /auth/mfa/verify → TokenPair → 同上
```

三态判别字段来自契约（`api/openapi/components.yaml`）：

| 形态 | 判别字段 | 票据 | 有效期 |
|---|---|---|---|
| 登录成功 | `access_token` 存在 | — | — |
| 需第二步 MFA | `mfa_required === true` | `mfa_ticket` | 5 分钟，一次性 |
| 强制绑定 MFA | `mfa_binding_required === true` | `mfa_setup_ticket` | 10 分钟，一次性 |

任何中间态都不得在本地写入令牌；票据校验失败（含动态码错误）即被消费，UI 必须能退回密码登录。

---

## 2. 现状分析

### 2.1 后端（已实现，可直接消费）

| 能力 | 端点 | 关键行为 |
|---|---|---|
| 密码登录 | `POST /admin/v1/auth/login` | 入参 `{subject, password}`，`additionalProperties:false`；三态响应 |
| 刷新 | `POST /admin/v1/auth/refresh` | body `{refresh_token}`，一次性轮换，重放撤销整条会话链 |
| 登出 | `POST /admin/v1/auth/logout` | 204 无 body；需 Bearer |
| MFA 初始化 | `POST /admin/v1/auth/mfa/setup` | 票据路径无需 Bearer；返回 `secret` / `otpauth_uri` / `recovery_codes`，仅本次可见 |
| MFA 校验 | `POST /admin/v1/auth/mfa/verify` | `mfa_ticket` 两步登录返回 TokenPair；`mfa_setup_ticket` 完成绑定后直接签发 TokenPair |
| 当前权限 | `GET /admin/v1/iam/me/permissions` | 返回 `subject_id` / `roles` / `permissions` / `data_scopes` |

### 2.2 前端（当前问题）

| 位置 | 现状 | 与契约的差距 |
|---|---|---|
| `src/api/core/auth.ts` | `/auth/login`，入参 `{username,password}`，返回 `{accessToken}` | 入参应为 `subject`；响应是三态；logout 用无 Bearer 的 `baseRequestClient` |
| `src/api/core/auth.ts` | `refreshTokenApi()` 不传 body、`withCredentials` | 后端要求 `{refresh_token}` 并轮换两个令牌 |
| `src/api/core/user.ts` | `GET /user/info` | 后端不存在，必然 404 |
| `src/api/core/auth.ts` | `getAccessCodesApi()` → `/auth/codes` | 后端为 `/iam/me/permissions`，且需同时拿 `roles` |
| `src/store/auth.ts` | `const { accessToken } = await loginApi(params); if (accessToken) …` | MFA 中间态会「点击登录后静默无反应」 |
| `src/api/request.ts` | `doRefreshToken` 只设置 access token | refresh token 丢失、不可刷新 |
| `src/preferences.ts` | 未覆盖 `enableRefreshToken`，默认 `false` | 401 直接踢登录 |
| `.env.development` / `.env.production` | `VITE_GLOB_API_URL=/api`，Mock 开启 | 与 `/admin/v1` 前缀不一致 |

### 2.3 契约缺口

A 端没有 `GET /admin/v1/me`。`/iam/me/permissions` 有 `subject_id` 与 `roles`，但没有 `subject` 文本、`status`、`mfa_enabled`，不足以构造 Vben 的 `BasicUserInfo`（`userId` / `username` / `realName` / `avatar`）。本计划按 FEINT-05 补该端点，避免前端拼装或解析 JWT。

---

## 3. 目标与非目标

### 目标

1. 三类账号（无 MFA / 已绑 MFA / 强制绑定）都能完成登录。
2. 双令牌落库、刷新令牌原子轮换、登出 204 安全处理。
3. 登录成功后拿到稳定的用户资料与权限快照。
4. 失败路径明确：401/429、票据失效、绑定票据过期均有可操作提示。

### 非目标

1. B 端（web-biz）登录改造；B 端无 MFA 与会话管理路由。
2. MFA 解绑、会话管理页、登出全部会话。
3. `expiresAt` 提前静默刷新（Vben access store 无该字段，本次仅靠 401 触发刷新）。
4. 多标签页刷新令牌重放防护（后端会撤销整条会话链，属已知限制）。

---

## 4. 改动清单

### 4.1 契约（OpenAPI）

**新增** `api/openapi/paths/admin/me.yaml`

```yaml
admin-v1-me:
  get:
    tags: [auth]
    summary: 当前登录主体资料
    description: |
      仅需认证，不额外要求权限点。
      roles / permissions / data_scopes 仍由 GET /admin/v1/iam/me/permissions 提供。
    operationId: adminGetMe
    security:
      - bearerAuth: []
    responses:
      "200":
        description: ok
        content:
          application/json:
            schema:
              allOf:
                - $ref: "../../components.yaml#/components/schemas/Envelope"
                - type: object
                  properties:
                    data:
                      $ref: "../../components.yaml#/components/schemas/MeView"
      "401": { $ref: "../../components.yaml#/components/responses/Unauthorized" }
      "500": { $ref: "../../components.yaml#/components/responses/InternalError" }
      "503": { $ref: "../../components.yaml#/components/responses/ServiceUnavailable" }
```

**修改** `api/openapi/components.yaml`：在 `TokenPair` 附近新增 `MeView`，字段全部 required。

```yaml
MeView:
  type: object
  additionalProperties: false
  required: [subject_id, subject, channel, merchant_id, status, mfa_enabled, token_version]
  properties:
    subject_id: { type: string }
    subject: { type: string, description: 规范化登录主体（trim + 小写） }
    channel: { $ref: "./schemas/admin/common.yaml#/components/schemas/Channel" }
    merchant_id: { type: string, description: A 端恒为空串 }
    status: { type: string, enum: [pending, active, locked, disabled] }
    mfa_enabled: { type: boolean, description: 仅 ConfirmedAt 非空时为 true }
    token_version: { type: integer, format: int64 }
```

**修改** `api/openapi/admin.yaml`：

- `paths` 增加 `/admin/v1/me: { $ref: './paths/admin/me.yaml#/admin-v1-me' }`
- `info.version` 由 `5.0.0` 提升到 `5.1.0`（新增端点 + schema，非破坏性）
- 描述追加一行 `5.1.0：新增 GET /admin/v1/me（当前主体资料）。`

### 4.2 后端（identity 域）

**`internal/identity/app/auth_service.go`**

新增只读视图与用例：

```go
type MeView struct {
    SubjectID    string
    Subject      string
    Channel      domain.Channel
    MerchantID   string
    Status       domain.AccountStatus
    MFAEnabled   bool
    TokenVersion int64
}

func (s *AuthService) Me(ctx context.Context, actor SessionActor) (MeView, error)
```

不变量：

1. `actor.AccountID` 为空 → `40100`。
2. `FindByID` 不存在、软删、`channel` 与装配端别不一致、`status == disabled` → 统一 `40100`（与登录一致，不区分原因）。
3. `mfa_enabled` 仅当 `MFAEnrollmentRepository.FindByAccount` 命中且 `IsConfirmed()` 为 true；仓储为 nil 或未找到均返回 false，其他错误向上抛。
4. 不写审计、不碰会话。

**`internal/identity/transport/rest/handler.go`**

新增：

```go
// Me GET /admin/v1/me
func (h *Handler) Me(w http.ResponseWriter, r *http.Request)

type meResponse struct {
    SubjectID    string `json:"subject_id"`
    Subject      string `json:"subject"`
    Channel      string `json:"channel"`
    MerchantID   string `json:"merchant_id"`
    Status       string `json:"status"`
    MFAEnabled   bool   `json:"mfa_enabled"`
    TokenVersion int64  `json:"token_version"`
}
```

复用 `actorFrom(r)` 与 `httpx.OK`，错误走 `httpx.FromError`。

**`internal/identity/transport/admin/router.go`**

在 `Register` 中增加：

```go
mux.Handle("GET /admin/v1/me", authMW(http.HandlerFunc(h.Me)))
```

包注释从「认证路由」改为「身份域 A 端路由（认证 + 当前主体）」。

**新增测试** `internal/identity/app/me_test.go`（`package app_test`，复用 `newAuthFixture`）：

1. 未绑定 MFA：`mfa_enabled=false`，`subject/subject_id/channel/status/token_version` 与种子一致。
2. 已确认绑定（`bindMFA`）后：`mfa_enabled=true`。
3. `account.Disable(now)` 后保存：`40000` 之外统一断言 `40100`（用 `requireAppCode`）。
4. 直接向仓储写入一个 `ChannelBiz` 账号：admin 服务调用 `Me` 返回 `40100`。
5. 软删账号：`DeletedAt` 非空 → `40100`。

### 4.3 前端：基础配置与请求层

**`frontend/apps/web-admin/.env.development`**

```dotenv
# baseURL 包含端别前缀；API 函数只写 /auth/...、/iam/me/... 相对路径
VITE_GLOB_API_URL=/admin/v1
VITE_NITRO_MOCK=false
```

**`frontend/apps/web-admin/.env.production`、`.env.analyze`**

```dotenv
VITE_GLOB_API_URL=/admin/v1
```

生产走同源 ingress（`/admin` 原样转发，契约路径已含前缀），不写域名。

**`frontend/apps/web-admin/vite.config.ts`**

增加代理，避免开发期 CORS：

```ts
proxy: {
  '/admin': {
    changeOrigin: true,
    target: 'http://localhost:8081',
  },
  // 保留现有 /api → Nitro Mock，供未接入后端的页面使用
}
```

**`frontend/apps/web-admin/src/api/core/auth.ts`（重写）**

类型：

```ts
export interface TokenPairDTO {
  access_token: string;
  refresh_token: string;
  token_type: 'Bearer';
  expires_in: number;
}

export interface TokenPairVM {
  accessToken: string;
  refreshToken: string;
}

export type LoginOutcome =
  | { kind: 'token'; tokens: TokenPairVM }
  | { kind: 'mfa-challenge'; ticket: string }
  | { kind: 'mfa-binding'; ticket: string };

export interface MfaSetupData {
  secret: string;
  otpauth_uri: string;
  recovery_codes: string[];
}
```

函数：

| 函数 | 请求 | 响应处理 |
|---|---|---|
| `loginApi({username,password})` | `POST /auth/login`，body `{subject, password}`，`responseReturn:'body'` | 校验 `code===0`；按 `mfa_binding_required` → `mfa_required` → TokenPair 顺序判别 |
| `refreshTokenApi(refreshToken)` | `baseRequestClient.post('/auth/refresh', {refresh_token})` | 读 AxiosResponse 的 `data.data`，返回 `TokenPairVM` |
| `logoutApi()` | `requestClient.post('/auth/logout', undefined, {responseReturn:'raw'})` | 204 无 body，原样返回 |
| `mfaSetupApi(setupTicket)` | `POST /auth/mfa/setup`，body `{mfa_setup_ticket}`，`responseReturn:'body'` | 返回 `MfaSetupData` |
| `mfaVerifyApi({mfaTicket?, mfaSetupTicket?, code})` | `POST /auth/mfa/verify`，body 只带对应票据 + `code`，`responseReturn:'body'` | 返回 `TokenPairVM` |
| `getMyPermissionsApi()` | `GET /iam/me/permissions` | 返回 `{roles, permissions, data_scopes, …}` |
| `getAccessCodesApi()` | 同上 | 仅返回 `permissions`，保留给已有调用方 |

`toTokenPairVM` 只做一次 `snake_case → camelCase` 映射；页面/store 不再接触 DTO 字段。

**`frontend/apps/web-admin/src/api/core/user.ts`（重写）**

```ts
export async function getUserInfoApi(): Promise<UserInfo> {
  const me = await requestClient.get<MeView>('/me');
  return {
    userId: me.subject_id,
    username: me.subject,
    realName: me.subject,          // identity_account 无 display_name，回退 subject
    avatar: preferences.app.defaultAvatar,
    roles: [],                     // 由 authStore 合并权限快照
  };
}

export async function getMeApi(): Promise<MeView> { ... }
```

**`frontend/apps/web-admin/src/api/request.ts`**

```ts
async function doRefreshToken() {
  const accessStore = useAccessStore();
  const rt = accessStore.refreshToken;
  if (!rt) throw new Error('missing refresh token');
  try {
    const vm = await refreshTokenApi(rt);
    accessStore.setAccessToken(vm.accessToken);
    accessStore.setRefreshToken(vm.refreshToken);   // 原子替换两个令牌
    return vm.accessToken;
  } catch (error) {
    accessStore.setAccessToken(null);
    accessStore.setRefreshToken(null);
    throw error;                                    // 交给 authenticateResponseInterceptor 重认证
  }
}
```

**`frontend/apps/web-admin/src/preferences.ts`**

```ts
app: {
  enableRefreshToken: true,
  // 其余覆盖项保持不变
}
```

### 4.4 前端：auth store

**`frontend/apps/web-admin/src/store/auth.ts`（重写登录部分）**

```ts
async function authLogin(params: Recordable<any>): Promise<LoginOutcome> {
  loginLoading.value = true;
  try {
    const outcome = await loginApi(params);
    if (outcome.kind === 'token') {
      await completeLogin(outcome.tokens);
    }
    return outcome;
  } finally {
    loginLoading.value = false;
  }
}

async function verifyMfaChallenge(ticket: string, code: string) {
  await completeLogin(await mfaVerifyApi({ mfaTicket: ticket, code }));
}

async function startMfaBinding(ticket: string): Promise<MfaSetupData> {
  return await mfaSetupApi(ticket);
}

async function confirmMfaBinding(ticket: string, code: string) {
  await completeLogin(await mfaVerifyApi({ mfaSetupTicket: ticket, code }));
}

async function completeLogin(tokens: TokenPairVM) {
  accessStore.setAccessToken(tokens.accessToken);
  accessStore.setRefreshToken(tokens.refreshToken);
  const userInfo = await fetchUserInfo();
  accessStore.setLoginExpired(false);
  const redirect = router.currentRoute.value.query.redirect as string | undefined;
  await router.push(redirect ? decodeURIComponent(redirect) : preferences.app.defaultHomePath);
}

async function fetchUserInfo(): Promise<UserInfo> {
  const [info, permission] = await Promise.all([
    getUserInfoApi(),
    getMyPermissionsApi(),
  ]);
  const userInfo = { ...info, roles: permission.roles };
  userStore.setUserInfo(userInfo);
  accessStore.setAccessCodes(permission.permissions);
  return userInfo;
}
```

要点：

- 旧实现里 `if (accessToken)` 的静默分支删除；三态由调用方显式处理。
- `logout()` 保持现有路由逻辑，`logoutApi()` 改走 Bearer + raw。
- 不再在 store 中直接调用 `/auth/codes`。

### 4.5 前端：登录页 UI

**`frontend/apps/web-admin/src/views/_core/authentication/login.vue`（重构）**

```text
step: 'password' | 'mfa-challenge' | 'mfa-binding'
```

- `password`：沿用 `AuthenticationLogin`（表单字段仍是 `username` / `password`，不改 UI 模型），`@submit="handlePasswordSubmit"`。
- 提交结果：
  - `token`：store 已跳转，无需页面处理。
  - `mfa-challenge`：`challengeTicket = outcome.ticket`，切换到 `mfa-challenge`。
  - `mfa-binding`：`setupTicket = outcome.ticket`，调用 `authStore.startMfaBinding` 拿 `MfaSetupData`，切换到 `mfa-binding`；失败则回密码步并提示。
- 进入 MFA 步后密码表单卸载，避免用户重复提交。

**新增** `frontend/apps/web-admin/src/views/_core/authentication/mfa-verify.vue`

- 6 位数字输入（`maxlength=6`，只允许数字，回车提交）。
- props：`loading`、`cooldown`、`errorText`。
- emits：`submit(code)`、`back`。
- 票据失效：`errorText` 非空时禁用提交，只保留「返回重新登录」。

**新增** `frontend/apps/web-admin/src/views/_core/authentication/mfa-binding.vue`

- props：`setup`（`MfaSetupData`）、`loading`、`cooldown`、`errorText`。
- 展示：
  - 二维码：`qrcode.toDataURL(setup.otpauth_uri)`（`onMounted` 生成，`<img>` 渲染）；
  - 密钥文本（便于手动录入）；
  - 恢复码列表 + 「下载恢复码」（Blob `text/plain`）；
  - 6 位动态码输入 + 「完成绑定并登录」。
- 提示「密钥与恢复码只显示一次」。

**错误与限流处理（login.vue）**

| 场景 | 处理 |
|---|---|
| 密码错误 / 40100 | `request.ts` 已 toast 后端 `message`；页面仅复位 loading |
| 429 | 读 `error.response.headers['retry-after']`，设置 `cooldown` 秒数并禁用提交；`onUnmounted` 清理定时器 |
| MFA 动态码错误（400/401） | 票据已被消费：设置 `errorText = '验证票据已失效，请重新登录'`，禁用提交，保留「返回重新登录」 |
| 绑定 setup 失败（409/401） | 回密码步并提示（票据可能过期或已绑定） |
| 5xx / 503 / 网络错误 | toast 后停留在当前步；票据可能仍有效，允许重试 |

### 4.6 文案与依赖

**文案**：`frontend/packages/locales/src/langs/{zh-CN,en-US,zh-TW}/authentication.json` 增加 MFA 相关 key，统一通过 `$t('authentication.*')` 使用：

```json
{
  "mfaTitle": "两步验证",
  "mfaSubtitle": "请输入身份验证器中的 6 位动态码",
  "mfaCode": "动态码",
  "mfaCodeTip": "请输入 6 位数字",
  "mfaVerify": "验证",
  "mfaBackToLogin": "返回重新登录",
  "mfaTicketExpired": "验证票据已失效，请重新登录",
  "mfaBindingTitle": "绑定身份验证器",
  "mfaBindingSubtitle": "管理员账号必须绑定 MFA 后才能登录",
  "mfaScanTip": "使用身份验证器扫描二维码，或手动输入密钥",
  "mfaSecret": "密钥",
  "mfaRecoveryCodes": "恢复码（仅显示一次，请妥善保存）",
  "mfaDownloadRecoveryCodes": "下载恢复码",
  "mfaConfirmBinding": "完成绑定并登录"
}
```

**依赖**：`frontend/apps/web-admin/package.json`

```json
"dependencies": { "qrcode": "catalog:" },
"devDependencies": { "@types/qrcode": "catalog:" }
```

执行 `pnpm install` 更新锁文件。

---

## 5. 关键约定

### 5.1 `responseReturn` 选择

| 场景 | 模式 | 原因 |
|---|---|---|
| 登录 / MFA / 权限 / `/me` | `body` 或默认 `data` | 需要信封，正常 200 |
| 登出 204 | `raw` | 无 body，不能被 `code !== 0` 判定为失败 |
| 刷新 | `baseRequestClient` 默认 `raw` | 该客户端无信封拦截器，手动读 `AxiosResponse.data.data` |

### 5.2 令牌映射（唯一适配点）

| DTO | VM |
|---|---|
| `access_token` | `accessToken` |
| `refresh_token` | `refreshToken` |
| `expires_in` | 本期不持久化，仅可用于日志/后续扩展 |

### 5.3 用户资料映射

| `MeView` | `BasicUserInfo` |
|---|---|
| `subject_id` | `userId` |
| `subject` | `username`、`realName`（无昵称列，回退） |
| `mfa_enabled` | 暂不映射（后续安全设置页可用） |
| `roles`（来自 `/iam/me/permissions`） | `roles` |

### 5.4 权限与菜单

- 权限码：`GET /admin/v1/iam/me/permissions` → `accessStore.setAccessCodes(permissions)`。
- 菜单：现有 `getAllMenusApi()` → `/iam/me/menus` 不变；`accessMode: 'backend'` 保持。
- 登录成功后 `fetchUserInfo` 与权限请求并行，避免串行等待。

---

## 6. 假设与决策

1. **后端补 `/admin/v1/me`**：按 FEINT-05 落地，避免解析 JWT 或前端拼装；`info.version` 升 `5.1.0`。
2. **类型来源**：本次仍在 API 层手写窄 DTO，不直接 import `dist/openapi/client`。原因：该产物「入库=no」且位于前端 workspace 之外，前端构建不应依赖 Go 工具链；待类型分发方案确定后另开任务切换到 `openapi-typescript` 产物。
3. **MFA 票据不持久化**：票据是 5/10 分钟一次性凭证，刷新页面即回到密码登录，符合安全预期。
4. **昵称/头像**：`identity_account` 无 `display_name` / `avatar_url`，本期回退 `subject` 与默认头像。
5. **B 端不照搬**：`web-biz` 登录恒为单分支，后续如需接入，只复用 `loginApi` + `completeLogin`，不引入 MFA 面板。
6. **多标签页刷新**：不做跨标签页单飞；后端已设计为「重放即撤销会话链」，属可接受的安全行为。

---

## 7. 验证

### 7.1 后端

```bash
go test ./internal/identity/...
make openapi                 # validate + 相对基线 breaking
make openapi-changelog       # 可选：确认新增端点无 breaking
```

预期：`/admin/v1/me` 出现在 `dist/openapi/bundled/admin.yaml`；`oasdiff` 无 breaking。

### 7.2 前端

```bash
pnpm -F @vben/web-admin typecheck
pnpm -F @vben/web-admin build
```

### 7.3 手工验收矩阵

启动后端（`make run-admin`，本地 8081）与前端（`pnpm dev:admin`）：

| # | 场景 | 预期 |
|---|---|---|
| 1 | 无 MFA 账号登录 | 直接进入首页；localStorage 同时存在 access / refresh |
| 2 | 已绑 MFA 账号 | 进入两步验证；输对动态码进入首页 |
| 3 | 已绑 MFA 账号输错动态码 | 提示票据失效；返回密码步；不写入任何令牌 |
| 4 | 强制绑定账号 | 展示二维码、密钥、10 个恢复码；输对首码后直接进入首页 |
| 5 | 强制绑定页刷新 | 回到密码登录；重新登录会拿到新票据 |
| 6 | 刷新浏览器 | 保持登录态；`/me` 与 `/iam/me/permissions` 均成功 |
| 7 | access token 过期后并发 5 个请求 | 只发起 1 次 refresh；两个令牌同时更新 |
| 8 | 登出 | 204 不报错；回登录页；令牌被清空 |
| 9 | 连续输错密码触发限流 | 429 后按 `Retry-After` 倒计时，按钮禁用 |
| 10 | 跨端账号（biz 账号登录 admin） | 40100 统一提示，不泄露账号是否存在 |

---

## 8. 风险与后续

1. **后端 `/me` 是新增依赖**：若后端未随前端一起发布，登录后仍会 404；需同批次上线。
2. **刷新令牌轮换**：任何绕过 `requestClient` 手动调用 `/auth/refresh` 的行为都可能触发会话链撤销；本期只保留拦截器内一处调用。
3. **恢复码丢失**：后端只存哈希，用户未下载恢复码将无法自助找回，只能管理员解绑；UI 必须强制提示。
4. **后续可选项**：切换 `openapi-typescript` 生成类型、补齐 `expiresAt` 提前刷新、MFA 解绑与会话管理页。
