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

1. 新密钥写入当前密钥位：托管源启用后写 Secret 的 `auth_jwt_secret` 文件（键名与文件名一致），
   未启用时写 `ADMIN_FRAME_AUTH_JWT_SECRET`；旧密钥移入 `auth_jwt_secrets` / `ADMIN_FRAME_AUTH_JWT_SECRETS`（仍可校验）。
2. 滚动重启全部端，确认新签发的令牌校验通过、旧令牌仍可用。
3. 一个 `refresh_ttl` 周期后清空旧密钥列表并发布。

> 托管源（`secrets.providers` 含 `file`）命中时会**覆盖**环境变量，两处都改不会冲突，
> 但同时保留两份明文会扩大泄露面：迁移完成后只保留文件，见上节「迁移到托管源」。

### 摘要密钥（`identity.hash_key`）

1. 生成新 32B 密钥，设置新的 `identity.hash_key_id`；
2. 旧密钥（含 id）移入 `identity.previous_hash_keys`，保证历史索引可读；
3. 滚动重启后验证：登录、按手机号/邮箱查找账号、主体索引回填均正常；
4. 至少一个「账号不活跃窗口」后再移除旧摘要密钥（`previous_hash_keys` 清空），
   期间**禁止**只保留新密钥，否则历史主体无法索引。

### 迁移到托管源（env → 文件挂载，ENT-07）

存量部署由 `ADMIN_FRAME_*` 环境变量注入全部密钥。迁到文件挂载按以下顺序，**每步单独发布**：

1. 确认 `values-prod.yaml` 的 `secretMounts.enabled=true`，且 `mountPath` 与配置
   `secrets.file.dir` 完全一致（基线 `/var/run/secrets/admin-frame`）；
2. 配置侧设为 `secrets.providers: [file]` + `allow_env_fallback: true` 后发布。
   此阶段文件命中即覆盖环境变量，未命中的仍由环境变量兜底，业务无感；
3. 核对文件确实被读取、而非回落到 env：

   ```bash
   kubectl exec <pod> -- ls -l /var/run/secrets/admin-frame   # 应有 db_dsn / auth_jwt_secret 等
   ```

   启动日志中 `secrets` 段只输出 `providers` / `file_dir`，**不得出现任何取值**（SEC-05）；
4. 逐项移除 `values-prod.yaml` 中 `env:` 下的 `ADMIN_FRAME_DB_DSN`、`ADMIN_FRAME_REDIS_PASSWORD`、
   `ADMIN_FRAME_STORAGE_S3_*`、`ADMIN_FRAME_IDENTITY_*`，每项观察一个发布周期；
5. 全部移除后把 `allow_env_fallback` 置 `false` 并发布。此后密钥缺失直接阻断启动
   ——**这一步才算迁移完成**，保留 fallback 等于没有强制。

回滚：任一步启动失败，把 `allow_env_fallback` 置回 `true`、恢复被删的 env 项，单独发布。
不要删除 Secret 文件（迁移后它是唯一真相），也不要先删 Secret 再回滚配置。

### 托管源切到 Vault（`secrets.providers: [vault, file]`，ENT-07 T3）

Vault 适配走 HTTP API（`net/http` + `pkg/httpclient`），不含 Vault SDK，配置段为 `secrets.vault.*`。

1. **准备 KV 对象**，两种布局二选一（`path` 与 `prefix` 互斥，同时配置会在启动期报错）：
   - **单对象（推荐）**：`secret/data/admin-frame/prod` 内写 8 个字段，字段名即逻辑密钥名
     （`db_dsn`、`auth_jwt_secret`、`auth_jwt_secrets`、`identity_hash_key`、
     `identity_mfa_secret_key`、`redis_password`、`storage_s3_access_key`、`storage_s3_secret_key`）。
     启动期只发一次请求。
   - **逐键**：`secret/data/admin-frame/prod/<name>`，取值放 `value` 字段（可用 `value_field` 改名）。
2. **注入令牌**：优先 `token_file`（K8s 投射 / Vault Agent），其次 `token_env`（默认 `VAULT_TOKEN`）。
   **prod 禁止 `token` 字段**（明文进配置等于没有托管），启动校验会拒绝。
3. **切换来源**：`providers: [vault, file]`（vault 在前、file 作迁移期兜底）后单独发布。
4. **确认读到的是 Vault**：启动日志应出现
   `secrets: vault object read mount=secret path=… kv_version=2 fields=N`，
   且 `secrets` 段只输出来源结构，不出现任何取值（SEC-05）。
5. **收尾**：确认无误后把 `allow_env_fallback` 置 `false`，再逐项移除 env 注入。

排障时的失败语义（按此判定，不要靠猜）：

| 现象 | 含义 | 是否回落 |
|---|---|---|
| 403 / 5xx / 连不上 / 响应无法解析 | 依赖或鉴权故障 | **不回落**，启动失败 |
| 单对象模式下对象不存在 | `mount` / `path` 写错 | **不回落**，启动失败 |
| 逐键模式下某个键 404 | 该密钥确实不在 Vault | 该键按「未命中」继续下一个来源 |

回滚：把 `providers` 改回 `[file]`（或把 vault 移到 file 之后）单独发布。**不要先在 Vault 侧撤权限**
——那会让回滚前的每次启动都直接失败。

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
