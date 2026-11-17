# 业务密钥托管 Runbook（M2）

| 字段 | 内容 |
|------|------|
| 告警 ID | `ConfigSecretAPIUnavailable` / `ConfigSecretPlaintextLeak` |
| 级别 | P1（明文泄露按安全优先） |
| 服务 / 端 | config-api / admin-api / 业务进程 secretclient |
| 最后更新 | 2026-11-17 |

关联：[ADR-0014](../adr/0014-secret-management-envelope-encryption.md)、[config-center-plan.md](../config-center-plan.md)、[key-ceremony.md](./key-ceremony.md)、[config-center-remediation-plan.md](../config-center-remediation-plan.md)（CC-R-03/05）。

## 症状

- resolve 返回 403 / 503，或业务进程启动期密钥补齐失败。
- 管理面审批 403（自批被拒）或轮换长期卡在 pending。
- 指标 / 告警出现 `ConfigSecretPlaintextLeak` 或列表/日志疑似含明文。

## 影响

- 依赖 `auth.jwt.private_key` / 通知等密钥的路径 fail-fast。
- 明文泄露属安全事件：需轮换受影响密钥并审计访问记录。

## 排查

1. **403**：核对 grant（env + namespace + service_identity + secret）与 `X-Service-Identity` / 证书 SAN。
2. **503**：检查 KEK（见 [key-ceremony.md](./key-ceremony.md)）与 config-api 就绪；日志不得含明文。
3. **审批 403**：提交人不得自批；确认 `config:secret:approve`。
4. **LocalKEK 在 prod**：启动拒绝；切换 `CONFIGCENTER_KEK_DRIVER=vault`。
5. **租约过期**：确认客户端是否在 TTL 内刷新；过期后须重新 resolve。

## 缓解与恢复

### 日常操作

1. **创建**：管理面一次性输入明文 → 送审 → 另一人批准。
2. **授权**：按 `env + namespace + service_identity + secret` 新增 grant；禁止通配符。
3. **轮换**：写入新版本，旧版本 grace；`dual_version=1` 时 resolve 可返回两版。
4. **吊销**：状态 → revoked；旧租约最长存活一个 TTL（默认 5m）。

### 本机联调

```bash
export CONFIGCENTER_KEK_DRIVER=local
export CONFIGCENTER_LOCAL_KEK_HEX=<64 hex>
export CONFIG_API_ADDR=:8085
export CONFIGCENTER_ENV=local
# admin-api 与 config-api 共用同一 MySQL；先执行 migrations 000033/000034
go run ./cmd/config-api
```

业务进程：

```bash
export CONFIG_API_URL=http://127.0.0.1:8085
export CONFIG_SERVICE_IDENTITY=spiffe://local/biz-api
export CONFIGCENTER_ENV=local
```

M3 起无 tracked 配置文件；口令与 JWT 走部署 Secret / secretclient。

### staging / prod 引导（CC-R-03 / CC-R-04）

- config-api：`CONFIG_API_MTLS=require` + TLS 证书/私钥/客户端 CA；`CONFIG_API_NAMESPACE_ALLOWLIST` 必填。
- 业务密钥禁止裸 `ALIYUN_SMS_*` / `ADMIN_FRAME_NOTIFICATION_*` / OAuth `client_secret_env`；须经 secretclient → config-api（完整登记见 remediation 附录 A / CC-R-05）。
- `CONFIGCENTER_ENV` 必须在 `Validate` 前注入进程身份证。

## 升级

- P1：resolve 大面积失败 15 分钟未收敛 → L2。
- 明文泄露（P1）：立即轮换密钥、吊销 grant、升级安全 Owner。

## 相关链接

- [key-ceremony.md](./key-ceremony.md)
- [ALERTS.md](./ALERTS.md)
- [ADR-0014](../adr/0014-secret-management-envelope-encryption.md)
- [config-center-plan.md](../config-center-plan.md)
