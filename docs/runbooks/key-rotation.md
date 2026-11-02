# 密钥轮换（JWT / 摘要密钥）

| 字段 | 内容 |
|------|------|
| 告警 ID | 无固定告警；由发布窗口或安全策略触发（建议每 90 天一次） |
| 级别 | P3（计划内）；计划外紧急轮换按 [key-compromise.md](./key-compromise.md) 走 P1 |
| 服务 / 端 | admin-api、biz-api、c-api、worker |
| 最后更新 | 2026-11-02 |

## 症状

- 触发条件（满足任一即执行本手册）：
  - 到达密钥轮换周期（建议 90 天，或组织合规要求的更短周期）；
  - `auth.jwt.active_kid` 需要切换到新密钥对；
  - `identity.hash_key` / `identity.previous_hash_keys` 需要滚动到新版本；
  - 安全评审要求更换签发密钥但不涉及泄露事件。
- 用户可见表现：**计划内轮换不应有任何用户可见表现**。若出现 401 激增，说明轮换顺序或重叠期配置有误，按「影响」段判断。

## 影响

- 影响端与范围：
  - 顺序错误会让**全部已签发访问令牌**在重叠期开始前失效（全端 401）；
  - `identity.hash_key` 切换错误会让主体索引查不到账号，表现为登录报「账号不存在」；
  - 轮换期间新旧密钥并存属预期，不是故障。
- 数据正确性风险：**有**。摘要密钥（`hash_key`）只允许双读、不允许双写：
  新写入一律用 active key，历史索引靠 `previous_hash_keys` 继续可读。

## 排查

1. 确认当前生效密钥与版本：
   - JWT：读取运行配置中的 `auth.jwt.active_kid`（RS256 密钥环）或 `secret` 是否存在（HS256）；
   - 摘要：`identity.hash_key_id` 与 `identity.previous_hash_keys[]` 的 id 列表。
2. 确认当前令牌版本分布：`identity_jwt_issued_total{kid=...}` 与 `identity_token_verify_total{result="failure",reason="unknown_kid"}`；
   后者突增说明还有进程在用旧 kid 签发或校验。
3. 确认所有副本配置一致：逐个 Pod 检查挂载的 Secret 版本号，而不是只看 Deployment 期望值。
4. 检查审计：`auth.token.refresh` / `auth.login` 的 `service` 与 `kid` 分布，确认切换是否已在全量副本生效。

## 缓解与恢复

轮换顺序固定，**不可跳步**：新密钥发布 → 双密钥并存 → 新签发起用 → 旧令牌自然过期 → 旧密钥移除。

### JWT（RS256 密钥环）

1. 生成新密钥对，登记新 `kid`；私钥只进 Secret，公钥可进配置。
2. 把新密钥追加到 `auth.jwt.keys`，**保留旧密钥**（校验仍需要）；把 `active_kid` 指向新 kid。
3. 发布：滚动重启全部签发进程（admin/biz/c），确认 `identity_jwt_issued_total{kid="<new>"}` 增长。
4. 观察 `identity_token_verify_total{reason="unknown_kid"}` 至少一个 `refresh_ttl` 周期（默认 168h）后仍为 0。
5. 从 `auth.jwt.keys` 移除旧 kid 并发布；移除前确认无在途令牌（见第 4 步观察窗口）。

### JWT（HS256 单密钥）

1. 新密钥写入 `ADMIN_FRAME_AUTH_JWT_SECRET`，旧密钥移入 `ADMIN_FRAME_AUTH_JWT_SECRETS`（仍可校验）。
2. 滚动重启全部端，确认新签发的令牌校验通过、旧令牌仍可用。
3. 一个 `refresh_ttl` 周期后清空 `secrets` 列表并发布。

### 摘要密钥（`identity.hash_key`）

1. 生成新 32B 密钥，设置新的 `identity.hash_key_id`；
2. 旧密钥（含 id）移入 `identity.previous_hash_keys`，保证历史索引可读；
3. 滚动重启后验证：登录、按手机号/邮箱查找账号、主体索引回填均正常；
4. 至少一个「账号不活跃窗口」后再移除旧摘要密钥（`previous_hash_keys` 清空），
   期间**禁止**只保留新密钥，否则历史主体无法索引。

### 数据库 / Redis 凭据

1. 在数据库侧创建新账号并授予同等权限（不修改旧账号）；
2. 更新 Secret（`db.dsn` / `redis.*`）并滚动重启；确认连接池错误率回落；
3. 观察一个发布周期无异常后，再回收旧账号；
4. 全过程禁止在工单、聊天或审计载荷中粘贴明文 DSN。

### 恢复确认

- 三端各做一次登录 / 刷新 / 登出冒烟；
- `identity_token_verify_total{result="failure"}` 回到基线；
- 审计中能看到轮换窗口内的 `auth.login` 正常记录；
- 记录本轮轮换时间、kid、执行人，归档到发布记录。

## 升级

- 轮换后 15 分钟内出现**全端 401**：立即回滚到上一版本 Secret（旧密钥仍在 `secrets` / `keys` 中），按 P1 处理并升级到身份域 Owner。
- 摘要密钥切换后出现「账号不存在」：立即把旧 key 恢复到 `previous_hash_keys` 并重启，属 P1（登录不可用）。
- 发布窗口内无法回滚（已删除旧密钥）：升级到安全负责人 + SRE 值班长，按 [key-compromise.md](./key-compromise.md) 处置。
- 升级联系：见 [README.md](./README.md) 值班入口。

## 相关链接

- 告警目录：[ALERTS.md](./ALERTS.md)
- 密钥泄露应急：[key-compromise.md](./key-compromise.md)
- 身份域设计：[identity-authn.md](../identity-authn.md)
- 配置项基线：[CONFIG_STANDARDS.md](../CONFIG_STANDARDS.md)、[SECURITY_STANDARDS.md](../SECURITY_STANDARDS.md)
- 发版与回滚：[release-rollback.md](./release-rollback.md)
