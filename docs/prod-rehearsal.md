# 本机 prod 演练

> **定位**：在本机以 `CONFIGCENTER_ENV=prod` 拉起五进程，验证生产门禁（Vault KEK、mTLS、prod-like 播种）。
> **禁止**：把演练口令 / root token / 自签证书用于任何真实环境。
> **关联**：[local-development.md](./local-development.md)、[runbooks/config-center.md](./runbooks/config-center.md)、[runbooks/key-ceremony.md](./runbooks/key-ceremony.md)。

## 1. 与 local/dev 的差异

| 项 | local (`scripts/local`) | prod 演练 (`scripts/prod`) |
|----|-------------------------|----------------------------|
| `CONFIGCENTER_ENV` | `dev` | `prod` |
| KEK | LocalKEK（`CONFIGCENTER_LOCAL_KEK_HEX`） | Vault Transit（禁止 local） |
| config-api TLS | 明文 HTTP | mTLS `require` |
| 播种 overlay | KeySpec.Default | prod-like overlay（mysql/s3/jwt…） |
| 编排 PID/日志 | `bin/run/` | `bin/run-prod/` |
| Vault | 一般不需要 | **file 存储**（非 `-dev`）；`vault-init` 解封并把 root token 写入 `var/prod-vault/` |

> Vault 容器重启后须再跑 `make prod-vault-init` 解封（`make prod-start` / `prod-restart` 已自动依赖该目标）。Transit 密钥材料在 volume `prod-rehearsal-vault` 中，**不要**在仍保留 MySQL 密文时重建该 volume。若已丢失 Vault 数据：先 `bash scripts/prod/reset-config-secrets.sh`，再 init + 启动 config-api 重新播种。

## 2. 一键流程

```bash
# 依赖：MySQL + Redis + RustFS + Vault（独立 compose project，--wait 等健康检查）
make prod-up
```

等价拆步：

```bash
make prod-deps          # docker compose ... up -d --wait
make prod-env           # cp scripts/prod/env.example scripts/prod/.env
make prod-certs         # var/prod-mtls/{ca,server,client}.*
make prod-vault-init    # transit/keys/configcenter
make prod-migrate       # 可选；prod-up 已带 -Migrate / --migrate
make prod-start
make prod-status
```

**Windows / WSL：** `make prod-start` / `prod-restart` / `prod-stop` / `prod-status` 走 PowerShell `prod-services.ps1`（可保活、正确识别 PID）。也可直接：

```powershell
Copy-Item scripts\prod\env.example scripts\prod\.env
docker compose -f docker-compose.prod-rehearsal.yml up -d --wait
# 证书 / Vault：用 Git Bash 或「已安装 Git 的」bash，不要用会把 Windows 二进制当 Linux 跑的裸 WSL 流程去 start
& "C:\Program Files\Git\bin\bash.exe" scripts/prod/gen-mtls-certs.sh
& "C:\Program Files\Git\bin\bash.exe" scripts/prod/vault-init.sh
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/prod/prod-services.ps1 start -Migrate
```

> **禁止**：在 WSL 里用 `nohup` 拉 Windows `.exe`；脚本退出后进程会被带走。`prod-services.sh` 在 Windows/WSL 下会自动转调 `.ps1`。

## 3. 探测

```bash
curl -s -o /dev/null -w "%{http_code}\n" --cacert var/prod-mtls/ca.crt \
  --cert var/prod-mtls/client.crt --key var/prod-mtls/client.key \
  https://127.0.0.1:8085/readyz

curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8081/readyz
```

> Git Bash 自带 curl 多为 Schannel，可能无法加载 PEM 客户端证书；可用文档外的 python ssl，或安装支持 OpenSSL 后端的 curl。

## 4. 关键变量（见 `scripts/prod/env.example`）

- 基础设施：`ADMIN_FRAME_DB_*` / `ADMIN_FRAME_REDIS_*`（DSN 须加引号，含 `tcp(...)` 与 `&`）
- 对象存储部署侧：`ADMIN_FRAME_STORAGE_S3_*`（RustFS，宿主机 `19000`）
- 配置中心：`CONFIGCENTER_ENV=prod`、`CONFIGCENTER_KEK_DRIVER=vault`、`VAULT_ADDR`（`VAULT_TOKEN` 优先读 `var/prod-vault/root.token`，**仅注入 config-api**）
- mTLS：`CONFIG_API_MTLS=require`、服务端/客户端证书路径、`CONFIG_API_NAMESPACE_ALLOWLIST`
- 密钥播种授权：`CONFIG_BOOTSTRAP_GRANT_IDENTITY`（prod 必填，CC-R-05）
- 每进程唯一 `ADMIN_FRAME_ID_NODE_ID`（脚本按角色覆盖 1～5）

## 5. 停止

```bash
make prod-stop
docker compose -f docker-compose.prod-rehearsal.yml down
```

## 6. 相关路径

| 路径 | 说明 |
|------|------|
| `scripts/prod/env.example` | 可跟踪样例 |
| `scripts/prod/.env` | 本机 dotenv（gitignore） |
| `scripts/prod/prod-services.sh` / `.ps1` | 编排主脚本（`migrate` 子命令可用） |
| `scripts/prod/start-services.sh` | 兼容转调入口 |
| `docker-compose.prod-rehearsal.yml` | MySQL/Redis/RustFS/Vault（file 存储；端口绑 `127.0.0.1`） |
| `scripts/prod/vault.hcl` | Vault file 存储配置 |
| `scripts/prod/reset-config-secrets.sh` | 清空 `config_secret*` 以便换 KEK 后重播 |
| `var/prod-mtls/` | 自签证书（gitignore via `/var/`）；`FORCE_REGEN=1` 后须重启五进程 |
| `var/prod-vault/` | unseal key / root token（gitignore via `/var/`） |
| `bin/run-prod/` | PID 与日志 |
