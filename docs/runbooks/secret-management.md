# 业务密钥托管 Runbook（M2）

关联：[ADR-0014](../adr/0014-secret-management-envelope-encryption.md)、[config-center-plan.md](../config-center-plan.md)、[key-ceremony.md](./key-ceremony.md)。

## 范围

- 业务密钥密文托管于 `config_secret*`；KEK 外部（本机 LocalKEK / 生产 Vault Transit）。
- 解析仅经 `cmd/config-api`：`POST /internal/v1/configs/secrets/resolve`。
- 明文禁止出现在列表、详情、导出、日志、快照与前端回显。

## 日常操作

1. **创建**：管理面「密钥管理」一次性输入明文 → 送审 → 另一人批准。
2. **授权**：按 `env + namespace + service_identity + secret` 新增 grant；禁止通配符。
3. **轮换**：写入新版本，旧版本 grace；`dual_version=1` 时 resolve 同时返回两版。
4. **吊销**：状态 → revoked；旧租约最长存活一个 TTL（默认 5m）。

## 本机联调

```bash
export CONFIGCENTER_KEK_DRIVER=local
export CONFIGCENTER_LOCAL_KEK_HEX=<64 hex>
export CONFIG_API_ADDR=:8085
# admin-api 与 config-api 共用同一 MySQL；先执行 migrations 000033/000034
go run ./cmd/config-api -config configs/dev/admin.yaml
```

业务进程：

```bash
export CONFIG_API_URL=http://127.0.0.1:8085
export CONFIG_SERVICE_IDENTITY=spiffe://local/biz-api
```

## 故障处置

| 现象 | 动作 |
|------|------|
| resolve 403 | 检查 grant 与 `X-Service-Identity` / 证书 SAN |
| resolve 503 | 检查 KEK（Vault / LocalKEK）；查看 config-api 日志（不得含明文） |
| 审批 403 | 提交人不得自批；确认 `config:secret:approve` |
| LocalKEK 在 prod | 启动拒绝；切换 `CONFIGCENTER_KEK_DRIVER=vault` |

## 告警

见 Prometheus `ConfigSecretAPIUnavailable` / `ConfigSecretPlaintextLeak`（附录 C）。
