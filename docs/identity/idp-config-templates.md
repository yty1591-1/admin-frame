# IdP 配置模板 — OIDC / SAML 接入（S6-6）

面向「把企业 IdP 接入本系统 C 端 SSO」的实施者（安全与合规协调、SRE 配合）。
本模板给出 **Keycloak 26.x 实测基线** 与通用核对清单；协议行为、错误码与轮换步骤的权威口径在
[identity-authn.md §7.3（OIDC）](../identity-authn.md) 与 [§8.2.1（SAML）](../identity-authn.md)，
故障处置在 [identity-auth-failure.md](../runbooks/identity-auth-failure.md)。

> 兼容性结论全部来自 2026-11-05/06 的真实 Keycloak 验收（staging + kind `af-verify`，
> Keycloak 26.7.4，见 §7.3 / §8.2.1 验收记录）。换 IdP 产品或大版本时，必须重跑同一验收链路。

## 1. 本方固定端点（先读）

### OIDC（RP）

| 项 | 值 | 说明 |
|---|---|---|
| authorize（发起） | `POST /c/v1/auth/oauth/{provider}/authorize` | 前端调用，返回授权地址（含 state/nonce/PKCE S256） |
| callback（API） | `POST /c/v1/auth/oauth/{provider}/callback` | 前端承接页收到 `?code&state` 后 POST 到这里 |
| redirect_uri（IdP 登记） | **前端承接页绝对地址** | 必须与 `identity.oauth.providers[].redirect_uri` 完全一致；不要登记 API 地址（API 只接受 POST） |

### SAML（SP）

| 项 | 值 | 说明 |
|---|---|---|
| SP EntityID | `identity.saml.entity_id` | 写入 AuthnRequest `Destination` 判定与元数据 |
| SP 元数据 | `GET /c/v1/auth/saml/metadata` | 含签名/解密证书；IdP 侧以此为准 |
| ACS | `POST /c/v1/auth/saml/acs`（绝对地址必须为 `/c/v1/auth/saml/acs` 结尾） | IdP POST 断言到此；成功后 303 携一次性 ticket 回 `login_redirect_url` |
| 兑换 | `POST /c/v1/auth/saml/exchange` | 前端用 ticket 换令牌 |

## 2. Keycloak OIDC 模板（客户端参数）

Realm 建好后新建 Client（Client authentication **On**，Standard flow **On**）：

| Keycloak 设置 | 值 | 原因 / 对应本方行为 |
|---|---|---|
| Client ID | 如 `admin-frame-c` | 对应 `client_id`；`audience` 缺省即用它 |
| Valid redirect URIs | 前端承接页 URL（精确匹配，禁止通配） | IdP 302 目标；与本方 `redirect_uri` 逐字符一致 |
| PKCE Code Challenge Method | S256 | 本方固定 PKCE S256；缺失/不匹配统一 `40100` |
| Web Origins | 前端域 | 前端承接页发起 POST 的 CORS 需要 |
| 签名算法（Realm → Keys） | RS256 | 本方仅接受 RS256；其他算法的 id_token 直接拒绝 |
| Client scopes | 默认即可 | 主体取 id_token `sub`，不依赖自定义 claims；OIDC 模式不回退 userinfo |

### 本方配置对应段（c.yaml → `identity.oauth.providers[]`）

```yaml
oauth:
  providers:
    - name: keycloak                      # 路由 / 映射表里的 provider 名
      client_id: admin-frame-c
      client_secret_env: ADMIN_FRAME_IDENTITY_OAUTH_KEYCLOAK_SECRET   # 密钥只走环境变量
      issuer: https://kc.example.com/realms/<realm>
      jwks_url: https://kc.example.com/realms/<realm>/protocol/openid-connect/certs
      redirect_uri: https://c.example.com/auth/callback    # 前端承接页，与 IdP 登记一致
      scopes: [openid]
      oidc_enabled: true                  # 必须显式开启；false 时填 issuer/jwks 启动即失败
      # 缺省即符合本方口径：audience=client_id、jwks_ttl=15m、min_refresh=1m、leeway=30s
```

> 开发默认值之外的项不要抄模板——`jwks_ttl` / `id_token_leeway` 等已按 [§7.3](../identity-authn.md) 冻结；改动走设计变更。

## 3. Keycloak SAML 模板（客户端参数）

新建 Client，类型选 **SAML**（可直接导入本方 SP 元数据 `GET /c/v1/auth/saml/metadata`，再按下表核对）：

| Keycloak 设置 | 值 | 原因（26.7.4 实测） |
|---|---|---|
| Client ID | 本方 `entity_id` | AuthnRequest `Destination` 校验基准 |
| Force POST binding | **ON** | 置 false 且 AuthnRequest 带 `ProtocolBinding=HTTP-POST` 时，Keycloak 以 `invalid_redirect_uri` 拒绝（400） |
| Sign documents / Sign assertions | ON（RSA_SHA256） | 本方验签要求 |
| Encrypt assertions | ON | 断言含主体标识，传输态加密；解密失败统一 `40100` |
| Encryption algorithm | **AES-256-CBC 等 CBC 模式** | `AES-256-GCM` 不受支持（crewjam 侧未注册）；选 GCM 必然解密失败 |
| Key encryption algorithm | 保持 Keycloak 26 默认（XML Encryption 1.1：`rsa-oaep`，DigestMethod sha256） | SP 侧已在 `internal/identity/infrastructure/saml/keys.go` 补注册这两项；改成 `rsa-1_5` 反而偏离实测基线 |
| NameID format | **persistent** | 本方 `name_id_format: persistent`；不用 transient——每次登录主体变化会让 auto_provision 重复建号 |
| NameID 来源 mapper | username（persistent） | 保证同一用户多次登录 NameID 稳定（实测两次登录 `sub` 一致、映射表不新增行） |
| Valid redirect URIs | 本方 ACS 绝对地址 | POST 断言目的地 |
| IDP-initiated SSO URL name | 留空 | 本方仅支持 SP-initiated |

### 本方配置对应段（c.yaml → `identity.saml`）

```yaml
saml:
  enabled: true
  entity_id: https://c.example.com/saml/sp
  acs_url: https://c.example.com/c/v1/auth/saml/acs
  login_redirect_url: https://c.example.com/auth/saml/done     # ACS 303 的落点（前端页，禁止拼接用户输入）
  name_id_format: persistent
  certificate_pem: ...        # SP 签名/解密证书（可公开）；私钥经密钥托管注入，禁止明文 YAML
  providers:
    - name: keycloak
      idp_entity_id: https://kc.example.com/realms/<realm>    # 必须与 IdP 元数据 entityID 一致
      metadata_url: https://kc.example.com/realms/<realm>/protocol/saml/descriptor
  # 缺省即符合口径：clock_skew=2m、assertion_ttl=5m、request_ttl=10m、ticket_ttl=60s、
  # metadata_ttl=15m；replay_ttl 必须覆盖 assertion_ttl + clock_skew
```

> `Destination` 必须与 Keycloak 的 `KC_HOSTNAME` 完全一致：不一致返回 `Invalid Request`（400）；
> 缺失 `Destination` 虽可通过，但不符合规范，**不得作为接入方案**。

## 4. 接入验收清单（全部通过才算接入完成）

| # | 核对项 | 判定依据 |
|---|---|---|
| 1 | OIDC 正向链路 | authorize 200 → IdP 登录 → 前端承接页 POST callback 200；`identity_oauth_callback_total{result="ok"}` 递增 |
| 2 | OIDC 防护语义 | state 重放 / nonce 不匹配 / audience 不符均 `40100`；state 过期同样拒绝 |
| 3 | SAML 正向链路 | authorize 200 → IdP 登录 → ACS 303 返回 ticket → exchange 200（`expires_in=900`）→ `/c/v1/me` 200 |
| 4 | NameID 稳定性 | 同一 IdP 用户连续两次完整登录，JWT `sub` 一致，`identity_oauth_identity` 行数不新增 |
| 5 | 断言重放 | 同一断言第二次 ACS 被拒（重放防护属预期，见 [§8.2.1](../identity-authn.md)） |
| 6 | 密钥轮换 | OIDC 按 [§7.3](../identity-authn.md) 轮换演练（JWKS 新旧 kid 并行 → 切 active → 未知 kid 限频刷新）；SAML 按基线 → 切 active → 回滚三步，令牌头 `kid` 随之切换 |
| 7 | 时钟与有效期 | IdP 服务器 NTP 就绪；`clock_skew`（默认 2m）内断言可接受，超窗拒绝 |
| 8 | auto_provision | 未映射主体的行为与 `identity.oauth.auto_provision` / `saml.auto_provision`（默认关闭）一致，未开启时一律拒绝 |

## 5. 与运维衔接

- 密钥与证书的注入方式：生产一律密钥托管（ENT-07），`client_secret_env` / 私钥不落 YAML；
- 故障与轮换处置：[identity-auth-failure.md](../runbooks/identity-auth-failure.md)（含失败类告警）；
- 独立接入 Runbook（含租户侧材料清单与时间表）为 S6-6 剩余项，本模板不替代它。
