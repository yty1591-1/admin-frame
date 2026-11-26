# 本地开发环境

> **定位**：L2 开发手册（如何在本机拉起依赖与五进程）。  
> **关联**：[CONFIG_STANDARDS.md](./CONFIG_STANDARDS.md)、[ADR-0016](./adr/0016-config-service-pull-model.md)、[runbooks/config-center.md](./runbooks/config-center.md)、[migration-guide.md](./migration-guide.md)。  
> **最后更新**：2026-11-17

## 1. 进程与端口

| 进程 | 默认端口 | 说明 |
|------|----------|------|
| `config-api` | `:8085` | 必须最先启动；播种基线 + 配置/密钥拉取入口 |
| `admin-api` | `:8081` | A 端 |
| `biz-api` | `:8082` | B 端 |
| `c-api` | `:8083` | C 端 |
| `worker` | `:8090` | Outbox / 通知 / 审计等；暴露 `/healthz` `/readyz` `/metrics` |

业务进程经 config-api 拉取配置；JWT 等业务密钥经 `secretclient` resolve。基础设施仅 `ADMIN_FRAME_DB_*` / `ADMIN_FRAME_REDIS_*`。

## 2. 推荐：一键编排

### 2.1 准备

```bash
# 1) 依赖（MySQL + Redis；可选 RustFS 见 compose 文件）
make local-deps
# 等价：docker compose -f docker-compose.integration.yml up -d mysql redis

# 2) 生成本地 dotenv（已 gitignore）
make local-env
# 等价：cp scripts/local/env.example scripts/local/.env
```

默认 `.env` 对齐集成 compose：`root:root` / 库 `admin_frame_test` / Redis `127.0.0.1:6379`，并带本地 LocalKEK（**禁止用于 prod**）。

### 2.2 启动 / 重启 / 停止 / 状态

**Git Bash / Linux / macOS（Makefile）：**

```bash
make local-up       # 首次：依赖已起前提下，迁移 + 启动五进程
make local-start    # 构建并启动（不跑迁移）
make local-restart  # 停止后启动
make local-stop
make local-status
```

**Windows 原生 PowerShell：**

```powershell
Copy-Item scripts\local\env.example scripts\local\.env   # 首次
docker compose -f docker-compose.integration.yml up -d mysql redis
pwsh -File scripts/local/dev-services.ps1 start -Migrate
pwsh -File scripts/local/dev-services.ps1 status
pwsh -File scripts/local/dev-services.ps1 restart
pwsh -File scripts/local/dev-services.ps1 stop
```

脚本行为要点：

- 顺序：`config-api` 就绪 → 安装向导（`begin` → `apply` → `complete`）→ `worker` → 三端 API
- 每个进程注入独立 `CONFIG_SERVICE_IDENTITY`（密钥 grant 命中）
- PID / 日志：`bin/run/pids`、`bin/run/logs`
- 可选：`bash scripts/local/dev-services.sh start --no-build --migrate`
- 安装向导：所有环境（含 dev/test）统一走安装流程（ADR-0018）；`make local-install` / `pwsh -File scripts/local/dev-services.ps1 install` 可单独重跑，安装态为 `ready` 时为空操作

### 2.3 冒烟（破坏性，勿当日常）

`scripts/local/smoke-start-all.ps1` 会 **TRUNCATE** 配置中心密钥表后重启。仅用于密钥链路回归；日常请用 `dev-services`。

## 3. 手动逐步启动（排障）

```bash
export $(grep -v '^#' scripts/local/.env | xargs)   # 或按需 export

make migrate-up
make run-config    # 终端 1；等待 /readyz

# 安装向导（所有环境统一，ADR-0018）；config-api 未 ready 时业务进程只会以 InstallPending 兜底启动
bash scripts/local/dev-services.sh install   # 或 make local-install

# 每个业务进程必须带自己的身份：
export CONFIG_SERVICE_IDENTITY=admin-api && make run-admin
export CONFIG_SERVICE_IDENTITY=biz-api   && make run-biz
export CONFIG_SERVICE_IDENTITY=c-api     && make run-c
export CONFIG_SERVICE_IDENTITY=worker    && make run-worker
```

探测：

```bash
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8085/readyz
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8081/readyz
```

登录（首登可能强制改密）：

```bash
curl -s -X POST http://127.0.0.1:8081/admin/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"subject":"admin@example.com","password":"Admin#Bootstrap2026"}'
```

## 4. 环境变量清单

| 变量 | 用途 |
|------|------|
| `ADMIN_FRAME_DB_DRIVER` / `ADMIN_FRAME_DB_DSN` | 基础设施 DB（各进程） |
| `ADMIN_FRAME_REDIS_ADDR`（及可选 password） | 基础设施 Redis |
| `CONFIG_API_URL` | 业务进程拉取入口 |
| `CONFIG_SERVICE_IDENTITY` | 服务身份（脚本按进程注入） |
| `CONFIGCENTER_ENV` | 配置环境，本地默认 `dev` |
| `CONFIGCENTER_CACHE_DIR` | 本地缓存，默认 `var/configcenter-cache`（已 gitignore） |
| `CONFIGCENTER_KEK_DRIVER` / `CONFIGCENTER_LOCAL_KEK_HEX` | 仅本地 LocalKEK；prod 必须 Vault |
| `CONFIG_INSTALL_SETUP_TOKEN` | 安装向导凭据（所有环境必填；dev 有脚本缺省值） |
| `ADMIN_FRAME_NOTIFICATION_BLACKLIST_SALT` | worker 通知黑名单盐（装配期） |

禁止在 `.env` 中放 JWT 明文或业务配置覆盖（CFG-04）。

## 5. Windows 注意

- `make local-*` 依赖可用的 `bash`（Git Bash）与 `curl`。
- 不要从 **WSL** 去 `start` 原生 Windows 二进制：环境变量与进程生命周期会丢；请用 Git Bash 或 `dev-services.ps1`。
- 本机 **prod 演练**（Vault + mTLS）见 [prod-rehearsal.md](./prod-rehearsal.md)：`make prod-up` / `scripts/prod/prod-services.ps1`。

## 6. 相关路径

| 路径 | 说明 |
|------|------|
| `scripts/local/env.example` | 可跟踪的样例 |
| `scripts/local/.env` | 本机 dotenv（gitignore） |
| `scripts/local/dev-services.sh` | Bash 编排 |
| `scripts/local/dev-services.ps1` | PowerShell 编排 |
| `scripts/prod/` | 本机 prod 演练（见 [prod-rehearsal.md](./prod-rehearsal.md)） |
| `docker-compose.integration.yml` | MySQL / Redis / RustFS（dev） |
| `docker-compose.prod-rehearsal.yml` | MySQL / Redis / RustFS / Vault（prod 演练） |
| `var/configcenter-cache/` | 拉取缓存（gitignore） |
