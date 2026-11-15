# Key Ceremony：业务密钥 KEK（M2）

| 字段 | 内容 |
|------|------|
| 告警 ID | `ConfigSecretAPIUnavailable`（KEK / Transit 不可用时常伴随） |
| 级别 | P1 |
| 服务 / 端 | config-api / admin-api |
| 最后更新 | 2026-11-16 |

## 症状

- 新密钥无法 seal，或既有密文 unwrap 失败（resolve 503）。
- prod 误配 `CONFIGCENTER_KEK_DRIVER=local` 导致启动拒绝。
- Vault Transit 密钥版本切换后部分密文仍指向旧 `kek_key_id`。

## 影响

- 业务密钥 resolve 中断；依赖密钥的登录 / 通知 / HMAC 路径 fail-fast。
- 轮换窗口若提前吊销旧 KEK，存量密文不可恢复（RPO 依赖 MySQL 密文 + 外部 KEK 备份）。

## 排查

1. 确认驱动：`CONFIGCENTER_KEK_DRIVER`（本机 `local` / 生产 `vault`）。
2. LocalKEK：检查 `CONFIGCENTER_LOCAL_KEK_HEX` 是否 64 hex；确认非 prod。
3. Vault：`vault read transit/keys/configcenter`；核对 SA 策略（admin 仅 encrypt，config-api 仅 decrypt）。
4. 对比密文行的 `kek_key_id` 与当前可 unwrap 版本；查看 config-api 日志（不得含明文）。

## 缓解与恢复

### 本机 / CI（LocalKEK）

1. 生成 32 字节根密钥：`openssl rand -hex 32`
2. 注入 `CONFIGCENTER_LOCAL_KEK_HEX`；`CONFIGCENTER_KEK_DRIVER=local`
3. **禁止**在 prod 使用；启动期 `RejectLocalKEKInProd` 守护

### 生产（Vault Transit）

1. 启用 Transit 并创建密钥：`vault write -f transit/keys/configcenter`
2. 拆分策略：admin-api SA 仅 `encrypt`（wrap）；config-api SA 仅 `decrypt`（unwrap）
3. 注入 `CONFIGCENTER_KEK_DRIVER=vault`、`VAULT_ADDR` / token、`CONFIGCENTER_VAULT_TRANSIT_KEY`
4. 记录 ceremony 参与人、时间、key 版本到变更单（不写密钥材料）

### 轮换

1. Vault 创建新密钥版本（本机用双 LocalKEK `local/1`→`local/2` 模拟）
2. 逐条 `unwrap(old) → wrap(new)` 重包裹 DEK
3. 验证 resolve 成功后再停用旧版本

本机取证：[`docs/incident/drills/2026-11-16-configcenter-kek-rotation.md`](../incident/drills/2026-11-16-configcenter-kek-rotation.md)。

## 升级

- P1：KEK 不可用超过 15 分钟 → 端 Owner（L2）→ 安全 / SRE（L3）。
- 怀疑 KEK 材料泄露：立即轮换 Transit 密钥并 rewrap；禁止把 LocalKEK 带入 prod。

## 相关链接

- [secret-management.md](./secret-management.md)
- [ALERTS.md](./ALERTS.md)
- [ADR-0014](../adr/0014-secret-management-envelope-encryption.md)
- [CONFIG_STANDARDS.md](../CONFIG_STANDARDS.md)

## 检查清单

- [x] KEK 不在 MySQL / git / 日志（门禁 + LocalKEK env-only）
- [x] wrap / unwrap 权限分离（admin seal / config-api open）
- [x] prod 未启用 LocalKEK（`RejectLocalKEKInProd`）
- [x] 恢复演练已登记（M3 CC-F-55 / AC-CC-10）
