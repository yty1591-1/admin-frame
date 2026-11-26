# SAML 2.0 SP 真实 Keycloak 联调验收计划

## Context

SAML 2.0 SP-initiated SSO 已实现并提交（`feat/saml2-sp-sso`，cc5f1c4），但尚未用真实 IdP 验证过。本次目标是在 staging 等价环境（本机 kind 集群 `af-verify`，真实 Keycloak 26.7.4）跑通
`authorize → Keycloak 登录 → ACS → ticket → exchange → 访问令牌` 全链路，并用负向用例验证失败关闭，最后只把**实测结果**回写文档。

现场已确认：
- kind 集群 `kind-af-verify` 健康；命名空间 `admin-frame` 内 `af-kind-admin-frame-admin/biz/c/worker`、mysql、redis、keycloak 均 Running；Helm release `af-kind` revision **6**。
- 当前 `values-kind.yaml` 中 C 端 `identity.enabled=false`（继承 values.yaml），SAML 路由未注册，需临时 overlay 打开。
- Keycloak：`quay.io/keycloak/keycloak:26.7.4`，`start-dev`，admin/admin，HTTPS 8443，`KC_HOSTNAME=https://keycloak.admin-frame.svc.cluster.local:8443`，自签证书；无持久卷，可建临时 realm/client/用户。

## 验收设计

### 1. SP 密钥与配置

- `openssl genrsa -out sp.key 2048` + 自签证书（CN `admin-frame-sp`，有效期 2 天）。
- 私钥只经密钥托管注入：overlay 中 `secrets.providers: [env]`，Secret `admin-frame-saml` 的 `private_key` 注入环境变量 `ADMIN_FRAME_IDENTITY_SAML_SP_PRIVATE_KEY`（命中 `secretTargets()` 的 `identity_saml_sp_private_key`）。
- 证书 PEM 放 config（公开信息）。
- 关键 config（仅 C 端临时 overlay，`env: dev` 以允许 http）：
  - `entity_id`: `http://af-kind-admin-frame-c.admin-frame.svc.cluster.local/c/v1/auth/saml/metadata`（Keycloak clientId 同值）
  - `acs_url`: `http://localhost:18080/c/v1/auth/saml/acs`（本地 port-forward，保证断言 `Destination` 与 SP 配置一致）
  - `login_redirect_url`: `http://localhost:18080/c/login`
  - `auto_provision: true`，并配置 `identity.hash_key` 与 `identity.subject.keys`（否则 `buildSubjectProtector` 为 nil，自动建号会 `50300`）
  - `providers[0]`: `name=keycloak`、`idp_entity_id` 取自 Keycloak descriptor、`metadata_xml` 内联（集群内拉取自签 https 会 TLS 校验失败，避免 `metadata_url`）
- 注意：`identity.saml.entity_id` 必须绝对 URL；`acs_url` 路径必须为 `/c/v1/auth/saml/acs`。

### 2. 部署

1. `docker build --target c-api --build-arg CMD=c-api -t admin-frame/c-api:saml-verify .`
2. `kind load docker-image admin-frame/c-api:saml-verify --name af-verify`
3. 创建 Secret `admin-frame-saml`（private_key）。
4. 临时 overlay：`workloads.c.image.tag=saml-verify`、`workloads.c.configOverrides`（env=dev、iam.mode=off、identity.enabled=true、SAML 配置、secrets.providers）。
5. `helm --kube-context kind-af-verify upgrade af-kind deploy/helm/admin-frame -f values-kind.yaml -f <临时overlay> -n admin-frame --timeout 6m`，确认 c-api Pod Ready、日志无配置校验失败。
6. 本地 port-forward：`c 18080:80`、`keycloak 18443:8443`；Keycloak 请求统一加 `curl -k --connect-to keycloak.admin-frame.svc.cluster.local:8443:127.0.0.1:18443`，避免改 URL 与 hosts。

### 3. Keycloak 侧

- Admin REST（admin-cli password grant）确认 realm `admin-frame` 存在；不存在则创建。
- 创建测试用户（若无 `saml-verify`），设置密码、email、enabled、emailVerified。
- 创建 SAML client：`clientId = SP entity_id`，`protocol=saml`，属性含 ACS URL/binding、`saml_name_id_format=persistent`、`saml.assertion.signature=true`、`saml.encrypt=true`、`saml.signing.certificate`/`saml.encryption.certificate` = SP 证书 base64、`saml.signature.algorithm=RSA_SHA256`。
- 若 REST 属性名与版本不符：以 GET 返回的 client 属性为准修正；必要时用 Keycloak Admin Console 在 Keys 页设置加密证书。

### 4. 正向链路（脚本化）

用临时 PowerShell/Python 脚本（放 `%TEMP%`，不入库）：
1. `POST /c/v1/auth/saml/keycloak/authorize` → 取 `authorize_url`、`state`。
2. `curl -k --connect-to ... -c/-b cookie jar` 打开登录页，正则提取 form action；POST 用户名/密码。
3. 从 Keycloak 自动提交页提取 `SAMLResponse`、`RelayState` 与 ACS action。
4. `POST http://localhost:18080/c/v1/auth/saml/acs`（form-urlencoded）→ 期望 `303`，Location 含 `ticket`。
5. `POST /c/v1/auth/saml/exchange {"ticket":...}` → 期望 `200` 且有 access/refresh token。
6. `GET /c/v1/me`（Bearer）→ 期望 `200`，证明令牌可用。
7. `GET /c/v1/auth/saml/metadata` → 期望 `200` 且 `Content-Type: application/samlmetadata+xml`。
8. 证据：HTTP 状态、断言是否 `EncryptedAssertion`（解密成功由 200 隐式证明）、DB `identity_oauth_identity` 出现 `saml:keycloak` 映射、Redis 出现一次性 ticket 键、`identity_saml_*` 指标计数。

### 5. 负向用例

| 用例 | 期望 |
|---|---|
| 同一 `RelayState` + `SAMLResponse` 重放 | `40100` |
| 篡改 `SAMLResponse`（改 base64 一个字符） | `40100` |
| 缺失 `RelayState` 或 `SAMLResponse` | `40000` |
| ACS 使用 JSON Content-Type | `415` |
| 未配置 provider `authorize` | `40400` |
| 同一 ticket 二次 exchange | `40100` |
| 无效 ticket exchange | `40100` |
| 日志/审计不含 `SAMLResponse` 原文与 NameID | 无命中 |

### 6. 收尾

- `helm rollback af-kind 6 -n admin-frame`，确认 c-api 回到 identity 关闭、`/readyz` 正常。
- 删除临时 Secret `admin-frame-saml` 与 Keycloak 临时 client/user；不删除 Keycloak（保留给其他验证）。
- 确认工作区无临时文件入库（`git status`）。

### 7. 文档回写（仅实测）

- [docs/identity-authn.md](file:///d:/MyDocument/admin-frame/docs/identity-authn.md) §8.2.1：追加“验收记录（2026-11-06，kind af-verify + 真实 Keycloak 26.7.4）”，列出正向/负向实测结果与环境限制（kind 等价 staging，非真实 staging 域名）。
- [docs/archive/iam/iam-audit-report-2026-11-05.md](file:///d:/MyDocument/admin-frame/docs/archive/iam/iam-audit-report-2026-11-05.md)：把“SAML 待交付/仅预留”更新为“已实现并完成真实 Keycloak 联调”；SCIM/M2M 仍标注待交付。
- 若发现实现缺陷：只做最小修复，重跑相关单测与本次链路；若无法在本次范围内修复，则如实记录为已知问题，不虚构通过。

## 关键文件

- [internal/platform/config.go](file:///d:/MyDocument/admin-frame/internal/platform/config.go)（`IdentitySAMLConfig` / `validateIdentitySAML`）
- [internal/platform/config_secrets.go](file:///d:/MyDocument/admin-frame/internal/platform/config_secrets.go)（`secretTargets`）
- [internal/identity/transport/c/saml.go](file:///d:/MyDocument/admin-frame/internal/identity/transport/c/saml.go)
- [internal/identity/app/saml.go](file:///d:/MyDocument/admin-frame/internal/identity/app/saml.go)
- [internal/identity/infrastructure/saml/provider.go](file:///d:/MyDocument/admin-frame/internal/identity/infrastructure/saml/provider.go)
- [deploy/helm/admin-frame/values-kind.yaml](file:///d:/MyDocument/admin-frame/deploy/helm/admin-frame/values-kind.yaml)（不直接改，作为 upgrade 基线）

## 验收判定

- 正向链路全部 2xx/303，令牌可访问 `/c/v1/me`，且断言加密解密成功（或如 Keycloak 未加密则记录实测并按代码实际行为判定文档措辞）。
- 负向用例状态码/错误码与 §8.2.1 声明一致。
- 回滚后 release 恢复、c-api 正常。
- 文档只包含可复现的实测证据。

> 说明：本机无真实 staging 域名/TLS 入口，本次以 kind + 真实 Keycloak 作为 staging 等价环境；如组织有独立 staging 集群，可复用同一脚本仅替换 host/端口。
