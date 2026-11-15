# Key Ceremony：业务密钥 KEK（M2）

## 目的

为配置中心信封加密建立 / 轮换外部 KEK，保证根密钥永不入库。

## 本机 / CI（LocalKEK）

1. 生成 32 字节根密钥：`openssl rand -hex 32`
2. 注入 `CONFIGCENTER_LOCAL_KEK_HEX`；`CONFIGCENTER_KEK_DRIVER=local`
3. **禁止**在 prod 使用；启动期 `RejectLocalKEKInProd` 守护

## 生产（Vault Transit）

1. 启用 Transit 引擎并创建密钥：`vault write -f transit/keys/configcenter`
2. 拆分策略：admin-api SA 仅 `encrypt`（wrap）；config-api SA 仅 `decrypt`（unwrap）
3. 注入：
   - `CONFIGCENTER_KEK_DRIVER=vault`
   - `VAULT_ADDR` / `VAULT_TOKEN`（或 K8s auth）
   - `CONFIGCENTER_VAULT_TRANSIT_KEY=configcenter`
4. 记录 key ceremony 参与人、时间、key 版本到变更单（不写密钥材料）

## 轮换

1. Vault 创建新密钥版本
2. 后台任务逐条 `unwrap(old) → wrap(new)` 重包裹 DEK（M3 完整演练）
3. 验证 resolve 成功后再停用旧版本

## 检查清单

- [ ] KEK 不在 MySQL / git / 日志
- [ ] wrap / unwrap 权限分离
- [ ] prod 未启用 LocalKEK
- [ ] 恢复演练计划已登记（M3）
