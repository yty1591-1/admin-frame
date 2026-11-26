# 企业身份接入运维手册（identity-federation-onboarding）

> 关联：IAM `P1-6` / `S6-6`、[identity-authn.md](../identity-authn.md) §7.3（OIDC）、§8.2.1（SAML）、§21（SCIM / M2M）、
> [idp-config-templates.md](../identity/idp-config-templates.md)（IdP 侧模板与验收清单）、
> [identity-auth-failure.md](./identity-auth-failure.md)（接入后的故障处置）、[ALERTS.md](./ALERTS.md)

本手册覆盖企业客户首次接入与变更：C 端 OIDC / SAML 单点登录、A 端 SCIM 目录同步、三端 M2M 服务账号。
接入属计划内变更，走变更审批与安全评审，不走事故流程；接入后的告警处置见 `identity-auth-failure.md`。

## 适用与角色

| 角色 | 职责 |
|---|---|
| 接入负责人 | 受理工单、收集材料、平台侧配置、组织联调、归档 |
| IdP 管理员（客户侧） | 提供 metadata / client 凭据、配置回调地址与断言字段 |
| 安全评审人 | 评审账号自动开通、组映射、scope 与密钥托管（S6-1 签字） |
| 值班 / 端 Owner | 启用后观察指标、处置接入期告警 |

权限要求：SCIM 令牌签发需 `identity:scim:write`；M2M 客户端需 `identity:m2m:write`，轮换 secret 需 `identity:m2m:credential`。
OIDC / SAML / SCIM / M2M 均默认关闭，`enabled=false` 时对应路由不注册（返回 `404`）。

## 接入流程

### 1. 受理与材料清单

| 能力 | 客户 / IdP 侧材料 | 平台侧产出 |
|---|---|---|
| OIDC（仅 C 端） | `issuer`、`authorize` / `token` / `userinfo` 地址、`client_id`、`client_secret`、回调地址、scopes（至少 `openid profile email`） | `identity.oauth.providers[]` 配置、Secret 注入项 |
| SAML（仅 C 端） | IdP metadata（entityID、SSO 地址、签名证书）、NameID 格式 `persistent`、ACS 地址确认 | SP metadata（`GET /c/v1/auth/saml/metadata`）、`identity.saml` 配置 |
| SCIM（仅 A 端） | SCIM Base URL 确认、组清单与 `displayName → 角色 code` 映射表、同步规模与批次计划 | `identity.scim` 配置、SCIM Bearer 令牌 |
| M2M（三端） | 客户端名称、owner、目标端别（`admin` / `biz` / `c`）、所需权限码清单、轮换周期 | M2M client 与 secret、scope 白名单 |

材料缺失不得开始联调；`client_secret` / 私钥只能经密钥托管注入（ENT-07），禁止写入 YAML 或工单正文。

### 2. 变更审批与安全评审

- 提交变更单：能力、端别、影响面、回滚方式、观察窗口。
- 安全评审关注：是否开启自动建号（`oauth.auto_provision` / `saml.auto_provision` / `scim.auto_provision`）、组映射是否放大权限、M2M scope 是否最小、密钥来源。
- 未经评审不得把 `enabled` 置为 `true`，也不得签发长期 SCIM 令牌。

### 3. 平台侧配置

| 能力 | 配置位置 | 关键字段 |
|---|---|---|
| OIDC | `configs/prod/c.yaml.example` 的 `identity.oauth` | `providers[].name` / `client_id` / `client_secret_env` / `auth_url` / `token_url` / `userinfo_url` / `redirect_uri` / `scopes` / `subject_field` / `oidc_enabled` |
| SAML | `identity.saml` | `enabled`、`entity_id`、`acs_url`（必须 `https://<c 域名>/c/v1/auth/saml/acs`）、`login_redirect_url`、`providers[]` |
| SCIM | `identity.scim` | `enabled`、`provider`、`auto_provision`、`group_role_map`、`page_size`、`token_grace` |
| M2M | `identity.m2m` | `enabled`、`token_ttl`、`rotate_grace`、`max_secret_age`、`allowed_scopes` |

- `redirect_uri` / `acs_url` 必须与 IdP 登记值逐字一致（scheme、端口、末尾斜杠）。
- `group_role_map` 的 value 必须是 A 端活跃角色 code，否则装配期启动失败。
- `identity.m2m.allowed_scopes` 必须为 IAM 活跃权限码，禁止 `*`。

### 4. 密钥注入

- OIDC：`client_secret_env` 指向的环境变量由 Secret 注入；配置只写变量名。
- SAML：`identity_saml_sp_private_key` / `identity_saml_sp_private_key_next`（PEM）由密钥托管注入。
- SCIM / M2M：令牌与 secret 由平台生成、只在创建响应中出现一次，库内仅存 SHA-256；不进入配置与日志。

### 5. 联调与验收

按 [idp-config-templates.md](../identity/idp-config-templates.md) §4 的 8 项清单逐条核对（正向链路、防护语义、NameID 稳定性、断言重放、密钥轮换、时钟、auto_provision）。

补充冒烟命令：

```bash
# OIDC：获取授权地址（返回 state 与 URL，state 一次性、TTL 10 分钟）
curl -sS -X POST "https://<c 域名>/c/v1/auth/oauth/<provider>/authorize" \
  -H 'Content-Type: application/json' -d '{"redirect_uri":"https://<前端域名>/callback"}'

# SAML：确认 SP 元数据可拉取
curl -sS "https://<c 域名>/c/v1/auth/saml/metadata"

# SCIM：令牌可用性与分页
curl -sS -H "Authorization: Bearer $SCIM_TOKEN" \
  "https://<admin 域名>/admin/scim/v2/Users?startIndex=1&count=1"

# M2M：申请令牌（scope 必须显式，且 ⊆ allowed_scopes）
curl -sS -u "$CLIENT_ID:$CLIENT_SECRET" \
  -d 'grant_type=client_credentials' -d "scope=$SCOPE" \
  "https://<端域名>/<端>/v1/auth/token"
```

SCIM 与 M2M 的验收口径（10k 同步、幂等、轮换、吊销时效）见 [identity-authn.md](../identity-authn.md) §21.1.9 / §21.2.8；演练基线见 [perf/LOAD_BASELINES.md](../perf/LOAD_BASELINES.md) `LB-STG-SCIM-SYNC`。

### 6. 启用与观察

1. 先 staging 完成全流程，再改生产配置并重启对应进程（配置为启动期加载）。
2. 启用后观察 15～30 分钟：`identity_oauth_callback_total`、`identity_scim_request_total`、`identity_m2m_token_total{result="denied"}`。
3. 确认无异常后，在工单登记启用时间、配置版本、令牌指纹与观察结论。

### 7. 交接与归档

- 告知值班：新增的 IdP / client、告警路由、回滚开关。
- 登记轮换日历：SAML 证书、OIDC secret、SCIM 令牌（`token_grace` 内双令牌）、M2M secret（`max_secret_age`）。
- 归档材料：IdP metadata、验收清单结果、变更单、令牌指纹（不存原文）。

## 症状

接入阶段或启用初期的异常信号：

- OIDC / SAML 登录跳转后返回 `40100`，`identity_oauth_callback_total{result="error"}` 上升；
- 用户能完成 SSO 但无业务权限（组未同步或 `group_role_map` 未命中）；
- SCIM 返回 `401` / `403` / `409 uniqueness` / `400 invalidValue`；
- M2M 返回 `401 invalid_client` / `400 invalid_scope` / 调用业务接口 `40300`；
- 接入端点返回 `404`（能力未启用或路由未注册）。

## 影响

- 仅影响该 IdP、该 SCIM provider 或该 M2M client，不影响其他接入方与密码登录。
- `enabled=false` 时路由 `404`，存量账号、令牌与数据不受影响。
- 误开自动建号会扩大账号面；组映射错误会放大或缺失授权，属安全事件处理范围。
- SCIM 停用后已同步账号保留，但不再随 IdP 变更；M2M 停用后旧令牌 ≤60s 失效。

## 排查

1. 确认能力开关与路由：`identity.oauth.providers` 非空、`identity.saml.enabled`、`identity.scim.enabled`、`identity.m2m.enabled` 与进程端别匹配（OIDC/SAML 仅 C 端，SCIM 仅 A 端）。
2. 核对回调：`redirect_uri` / `acs_url` 与 IdP 登记值逐字一致；`acs_url` 路径必须为 `/c/v1/auth/saml/acs`。
3. 核对断言：issuer / audience / `Destination` / 签名证书 / NameID=`persistent`；服务器 NTP 偏差是否超 `clock_skew`。
4. 核对 SCIM：令牌 `provider` 与 `identity.scim.provider` 一致；`members[].value` 必须是账户 id 或 `externalId`；重复 `externalId` 属幂等冲突。
5. 核对 M2M：client 状态、端别是否在 `allowed_channels`、`scope` 是否 ⊆ `allowed_scopes`、使用的是否为 `retiring` 且已过期的 secret。
6. 指标定位：`identity_scim_group_map_total{result="unmapped"}`、`identity_scim_request_total{result="error"}`、`identity_m2m_secret_age_seconds`。

## 缓解与恢复

- 登录失败：回退配置（移除 provider 或 `enabled=false`）并重启；已签发令牌按 §6.3 撤销语义在 ≤60s 内失效。
- OIDC state / ticket 过期：重新发起 authorize，不复用旧 state。
- SCIM 令牌疑似泄露：`DELETE /admin/v1/auth/scim/tokens/{id}` 吊销并按需重签。
- M2M secret 泄露：立即 `POST /admin/v1/auth/m2m/clients/{id}/secrets` 轮换并向安全值班报告；必要时 `/{id}/disable`。
- 误建账号：停用账号（不物理删除），修正 `auto_provision` 或映射后重跑同步。
- 组映射错误：修正 `group_role_map` 并重启，重新 PUT 组以重算绑定；确认 `iam.binding.*` 审计与 `identity_scim_group_map_total{result="ok"}` 恢复。

## 升级

- 安全事件（断言重放、secret 泄露、跨租户越权、映射放大权限）：按 [SEVERITY_MATRIX.md](../incident/SEVERITY_MATRIX.md) 定级，安全类一律 P1，并按 [key-compromise.md](./key-compromise.md) 先止血。
- IdP 侧配置或证书问题：升级至客户 IdP 管理员与接入负责人。
- 代码 / 数据缺陷（同步半成品、绑定未回收）：升级至身份域 Owner；涉及权限数据同时通知 IAM Owner。
- 接入验收不通过且无法在工单内收敛：暂停启用，保留 staging 证据并回到评审。

## 相关链接

- 设计：[identity-authn.md](../identity-authn.md) §7.3 / §8.2.1 / §21
- IdP 模板与 8 项验收：[idp-config-templates.md](../identity/idp-config-templates.md)
- 接入后故障处置：[identity-auth-failure.md](./identity-auth-failure.md)
- 密钥轮换：[key-rotation.md](./key-rotation.md)；泄露应急：[key-compromise.md](./key-compromise.md)
- 告警目录：[ALERTS.md](./ALERTS.md)
- 性能基线：[perf/LOAD_BASELINES.md](../perf/LOAD_BASELINES.md)
- 工程计划：[iam-p1-5-7-engineering-plan.md](../archive/plans/iam-p1-5-7-engineering-plan.md) `S6-6`（已关闭归档）
