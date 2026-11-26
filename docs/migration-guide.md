# 数据库迁移使用指南（`pkg/migrate` / `cmd/migrate`）

> **状态**：已落地（本仓库 schema 只提供 **MySQL 8.0**；`pkg/migrate` 另含 PostgreSQL 方言执行能力，但仓库内无 PG 迁移脚本，业务仓储也不支持 PG）  
> **关联**：[`DATA_STANDARDS.md`](./DATA_STANDARDS.md)（DATA-01～DATA-08）、[`RELEASE_STANDARDS.md`](./RELEASE_STANDARDS.md)（RELS-06）、[`ARCHITECTURE.md`](./ARCHITECTURE.md) §十四、[`pkg-roadmap.md`](./pkg-roadmap.md)、[`migrations/`](../migrations)  
> **最后更新**：2026-11-07（平台拆包后路径同步：`internal/platform/config/config.go`）  
> **变更方式**：CLI 参数、配置优先级、迁移文件规范或故障处置流程变更须同步 `cmd/migrate`、`pkg/migrate` 单测与本文，并确认与 `DATA_STANDARDS.md` / `RELEASE_STANDARDS.md` 一致  
> **目标读者**：后端开发、SRE / 发布负责人、Code Review  
> **效力说明**：本文是使用说明。硬约束以 `DATA_STANDARDS.md` / `RELEASE_STANDARDS.md` 为准；两者冲突时以规范为准。

---

## 目录

1. [定位与边界](#1-定位与边界)
2. [快速开始](#2-快速开始)
3. [配置](#3-配置)
4. [CLI 参考](#4-cli-参考)
5. [迁移文件规范](#5-迁移文件规范)
6. [编写迁移](#6-编写迁移)
7. [迁移历史表](#7-迁移历史表)
8. [咨询锁与并发](#8-咨询锁与并发)
9. [一致性校验与错误码](#9-一致性校验与错误码)
10. [发布流程](#10-发布流程)
11. [故障处置](#11-故障处置)
12. [安全与合规](#12-安全与合规)
13. [在 Go 代码中使用 `pkg/migrate`](#13-在-go-代码中使用-pkgmigrate)
14. [测试与 CI](#14-测试与-ci)
15. [限制与非目标](#15-限制与非目标)
16. [FAQ](#16-faq)
17. [附录 A：命令与 SQL 速查](#附录-a命令与-sql-速查)
18. [附录 B：端到端示例](#附录-b端到端示例)
19. [附录 C：相关文件索引](#附录-c相关文件索引)

---

## 1. 定位与边界

### 1.1 能力一览

| 能力 | 说明 |
|------|------|
| 文件加载 | 从本地目录或 `io/fs.FS`（含 `embed.FS`）加载 `migrations/*.up.sql` / `.down.sql` |
| 命名校验 | 强制 `NNNNNN_desc.up.sql` / `.down.sql`（DATA-01），up/down 必须成对（DATA-02） |
| SQL 切分 | 按 `;` 切分，正确跳过字符串、标识符、注释与 PostgreSQL dollar-quote |
| 版本追踪 | 历史表 `schema_migrations` 记录版本、描述、SHA-256、时间、耗时、dirty |
| 前向 / 回滚 | `up` / `down -steps N` / `down -to VERSION` |
| 状态查询 | `status` / `version`，报告 pending、drift、missing、dirty |
| 并发保护 | PostgreSQL `pg_advisory_lock`、MySQL `GET_LOCK` |
| 一致性保护 | checksum 漂移、dirty、乱序、源文件缺失 |
| 可观测 | `Observer` 事件（plan/start/done/error），CLI 文本输出 |
| 演练 | `-dry-run` 只计划不执行迁移 SQL |

### 1.2 组件与执行链路

```text
migrations/
  000001_create_orders.up.sql
  000001_create_orders.down.sql
        │
        │ LoadDir / LoadFS（命名 + 配对 + 非空校验，计算 SHA-256）
        ▼
pkg/migrate.Runner ──► pkg/db（连接池 / Ping / DSN 脱敏）
        │                     │
        │                     ▼
        │              database/sql
        │                     │
        │            ┌────────┴────────┐
        │            ▼                 ▼
        │      pgx（PostgreSQL）   go-sql-driver（MySQL）
        ▼
schema_migrations（version / description / checksum / applied_at / execution_ms / dirty）
```

### 1.3 职责边界（MUST）

- **唯一执行入口**：`cmd/migrate` 或组织批准的发布流水线（DATA-03）。
- **禁止**在 `cmd/admin-api`、`cmd/biz-api`、`cmd/c-api`、`cmd/worker` 的启动路径调用迁移，也禁止 ORM `AutoMigrate`。
- **不做 schema 差异推断**：不是 ORM AutoMigrate，只执行显式 SQL 文件。
- **不做备份与恢复**：备份策略、保留期、恢复演练见 `DATA_STANDARDS.md` §5。
- **不做 DDL 审核**：锁表评估、在线 DDL 选型由 MR Review 负责。
- `pkg/migrate` 只依赖 `database/sql` 与标准库，**不导入 `internal/`**；驱动由 `cmd/migrate` 空白导入注册。

---

## 2. 快速开始

### 2.1 前置条件

| 项 | 要求 |
|----|------|
| Go | 与 `go.mod` 一致（当前 `go 1.26.11`） |
| 数据库 | PostgreSQL 或 MySQL / MariaDB 可连接 |
| 权限 | 可 `CREATE TABLE`（历史表）、可执行迁移中的 DDL/DML |
| 目录 | 仓库根 `migrations/` 已存在至少一对 up/down 文件 |
| 凭据 | 通过环境变量注入，**禁止**写入仓库（CFG-06 / SEC-01） |

### 2.2 构建

```bash
make build-migrate          # 产物：bin/migrate
# 等价于
go build -ldflags "-s -w" -o bin/migrate ./cmd/migrate
```

### 2.3 配置数据库连接

本仓库只支持 MySQL 8.0：

Linux / macOS：

```bash
export ADMIN_FRAME_DB_DRIVER=mysql
export ADMIN_FRAME_DB_DSN='app:CHANGE_ME@tcp(127.0.0.1:3306)/admin_frame?parseTime=true&loc=UTC'
```

Windows PowerShell：

```powershell
$env:ADMIN_FRAME_DB_DRIVER = "mysql"
$env:ADMIN_FRAME_DB_DSN = "app:CHANGE_ME@tcp(127.0.0.1:3306)/admin_frame?parseTime=true&loc=UTC"
```

> `pkg/migrate` 保留了 PostgreSQL 方言执行能力，但仓库内 `migrations/` 全部是 MySQL DDL，
> 业务 SQL 仓储与 `db.driver` 启动校验也只支持 MySQL；不要把 PG 用于本项目。

### 2.4 三步走

```bash
./bin/migrate -action status        # 1. 查看当前版本、pending、dirty、drift
./bin/migrate -action up -dry-run   # 2. 演练：只打印计划，不执行迁移 SQL
./bin/migrate -action up            # 3. 正式执行（获取咨询锁）
```

预期输出（示例）：

```text
# status
current version: 000001
dirty: false
applied: 1
  000001_create_orders applied_at=2026-10-20T03:00:00Z
pending:
  000002_add_orders_name

# up -dry-run
==> would applying 000002_add_orders_name (1 statements)
planned up: 1 migration(s)

# up
==> applying 000002_add_orders_name (1 statements)
    OK 000002_add_orders_name (4ms)
up: 1 migration(s)
```

> **注意**：`up` 与 `down` 均可以直接重复执行：已应用版本会被跳过，不会重复执行 SQL。

---

## 3. 配置

### 3.1 优先级

`cmd/migrate` 只读取配置中的 `db` 段，优先级由高到低：

| 优先级 | 来源 | 示例 |
|--------|------|------|
| 1 | CLI 参数 | `-driver postgres -dsn '...'` |
| 2 | 环境变量 | `ADMIN_FRAME_DB_DRIVER` / `ADMIN_FRAME_DB_DSN` |
| 3 | 配置文件 `-config` 的 `db.driver` / `db.dsn` | `./bin/migrate -config configs/dev/admin.yaml` |
| 4 | 内置默认 | 无默认驱动、无默认 DSN（缺失即报错） |

规则说明：`cmd/migrate` 是一次性运维工具，不是业务进程；上表仅描述迁移二进制的连接参数来源，**不属于 CFG-04 的业务配置优先级**。业务进程固定为「配置中心 > 代码默认值」。

### 3.2 支持的驱动

| 配置值（`db.driver` / `-driver`） | 规范化方言 | 实际注册的 `database/sql` 驱动 |
|-----------------------------------|------------|--------------------------------|
| `postgres` / `postgresql` / `pg` / `pgx` | `postgres` | `pgx`（`jackc/pgx/v5/stdlib`） |
| `mysql` / `mariadb` | `mysql` | `mysql`（`go-sql-driver/mysql`） |
| `memory` | — | 明确拒绝：不是 `database/sql` 驱动 |
| 其他（如 `sqlite3`、`oracle`） | — | 明确报错：本二进制未注册该驱动 |

`pkg/migrate` 本身还支持 `sqlite` / `generic` 方言（供库调用方自行注册驱动），但 **CLI 只注册 PostgreSQL 与 MySQL**。

### 3.3 配置文件示例

可复用任一业务配置（只读取 `db` 段，其余字段忽略）：

```yaml
# configs/migrate.yaml（本地文件，不入库）
db:
  driver: mysql
  dsn: ""          # 留空，由 ADMIN_FRAME_DB_DSN 注入
```

```bash
./bin/migrate -config configs/migrate.yaml -action up
```

**禁止**在仓库文件或迁移命令历史中写真实口令 / 生产主机（CFG-06）；迁移 DSN 通过 CLI 参数或环境变量一次性注入，不落盘。

### 3.4 DSN 示例

PostgreSQL（pgx，仅 `pkg/migrate` 通用能力；本仓库 schema 与业务仓储不支持）：

```text
postgres://app:CHANGE_ME@db.internal:5432/admin_frame?sslmode=verify-full
postgres://app:CHANGE_ME@127.0.0.1:5432/admin_frame?sslmode=disable
host=db.internal port=5432 user=app password=CHANGE_ME dbname=admin_frame sslmode=disable
```

MySQL：

```text
app:CHANGE_ME@tcp(db.internal:3306)/admin_frame?parseTime=true&loc=UTC&timeout=5s
```

建议：

- MySQL 加 `parseTime=true&loc=UTC`，保证 `applied_at` 解析与 UTC 语义一致。
- 仅在 `NoSplit` 执行多语句文件时，MySQL 才需要 `multiStatements=true`（见 §5.4）。
- 生产启用 TLS；DSN 由密钥管理系统注入，日志与错误信息已由 `pkg/db.RedactDSN` 脱敏（SEC-05）。

### 3.5 连接池与超时

CLI 固定使用以下连接参数（`pkg/db`）：

| 参数 | 值 | 说明 |
|------|----|------|
| `MaxOpenConns` | 4 | 咨询锁占用 1 条独占连接，迁移事务再占 1 条 |
| `MaxIdleConns` | 2 | |
| `PingTimeout` | 5s | 启动时连通性探测 |

> PostgreSQL / MySQL 的咨询锁必须占用**独立连接**，因此 `pkg/migrate.New` 会拒绝 `MaxOpenConns=1` 的连接池。库调用方自建 `*sql.DB` 时请保证上限 ≥ 2。

| CLI 超时 | 默认 | 作用 |
|----------|------|------|
| `-lock-timeout` | 30s | 获取咨询锁的最长等待 |
| `-statement-timeout` | 0（不限） | 单条 SQL 的执行超时 |
| `-timeout` | 30m | 整条命令的总超时；0 表示不限 |

超时触发时 context 被取消：事务型迁移回滚；非事务迁移可能留下 `dirty` 记录（见 §11）。

---

## 4. CLI 参考

### 4.1 语法

```text
migrate [flags]
```

`-action` 选择子命令，其余参数为执行选项。

### 4.2 参数表

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-action` | `up` | `up` \| `down` \| `status` \| `version` |
| `-dir` | `migrations` | 迁移目录（相对当前工作目录） |
| `-config` | 空 | 可选配置文件，读取 `db.driver` / `db.dsn` |
| `-driver` | 空 | 覆盖驱动；`postgres` \| `pgx` \| `mysql` \| `mariadb` 等 |
| `-dsn` | 空 | 覆盖 DSN；优先使用 `ADMIN_FRAME_DB_DSN`，避免进入 shell 历史 |
| `-steps` | `1` | `down`：回滚最近 N 条 |
| `-to` | `-1` | `up`：只应用到该版本（含），用于需要外部回填的分段升级；`down`：回滚到该版本（保留 ≤ `-to`），`>=0` 时覆盖 `-steps` |
| `-table` | `schema_migrations` | 历史表名（必须是简单标识符） |
| `-schema` | 空 | 历史表所在 schema（PostgreSQL）；空表示连接默认 schema |
| `-dry-run` | `false` | 只计划、不执行迁移 SQL、不取锁；**仍会创建并读取历史表** |
| `-no-split` | `false` | 整个文件作为一条语句执行（见 §5.4） |
| `-allow-out-of-order` | `false` | 允许执行版本号低于当前最大版本的待执行迁移（不推荐） |
| `-lock-timeout` | `30s` | 咨询锁获取超时 |
| `-statement-timeout` | `0` | 单条语句超时；0 表示由总超时/调用方控制 |
| `-timeout` | `30m` | 命令总超时；0 表示不限 |
| `-version` | `false` | 打印迁移器版本并退出（不连库） |

### 4.3 子命令

#### `status`：查看状态

```bash
./bin/migrate -action status
```

输出字段：

| 字段 | 含义 |
|------|------|
| `current version` | 历史表最大版本号（`%06d`，0 表示无记录） |
| `dirty` | 是否存在未完成的迁移（**必须为 false 才能继续**） |
| `applied` | 已应用记录，逐条列出 `version_desc` 与 `applied_at`（UTC） |
| `pending` | 源目录中尚未应用的迁移 |
| `drift (checksum mismatch)` | 已应用但 up 文件内容已变化（仅 status 报告，up/down 会拒绝执行） |
| `missing from source` | 历史表存在、源目录已删除的版本 |

> `status` 会幂等执行 `CREATE TABLE IF NOT EXISTS schema_migrations`，因此需要 **CREATE 权限**。只读账号无法执行 status。

#### `version`：只输出当前版本号

```bash
./bin/migrate -action version
# 000002
```

适合脚本判断与发布门禁。

#### `up`：应用全部待执行迁移

```bash
./bin/migrate -action up
```

- 按版本升序执行所有 pending；
- 已应用版本跳过（可重复执行）；
- 遇到错误立即停止，之前成功的迁移保持已应用；
- 默认每条迁移一个事务，历史写入与 SQL 执行同事务提交。

#### `down`：回滚

```bash
./bin/migrate -action down                 # 回滚最近 1 条
./bin/migrate -action down -steps 2        # 回滚最近 2 条
./bin/migrate -action down -to 0           # 全部回滚
./bin/migrate -action down -to 100         # 保留 <= 000100 的迁移
```

- 按版本降序回滚；
- `-to` 的版本无需已应用，只按版本边界计算回滚集合；
- 缺少 down 文件或 down 为空时直接失败（`ErrMissingDown`）；
- 回滚前会把记录标记为 `dirty`，成功后删除记录。

#### `-dry-run`：演练

```bash
./bin/migrate -action up -dry-run
./bin/migrate -action down -steps 1 -dry-run
```

- 不执行迁移 SQL、不获取咨询锁；
- 输出 `would applying ...` / `planned up: N migration(s)`；
- **会创建并读取历史表**，用于正确计算 pending；
- 因此需要 DDL 权限，且不能替代真实回滚演练（建议在 staging 实跑）。

### 4.4 退出码与信号

| 情况 | 行为 |
|------|------|
| 成功 | 退出码 `0` |
| 参数 / 配置 / 校验 / 执行失败 | `log.Fatalf` → 退出码 `1`，错误输出到 stderr |
| `SIGINT` / `SIGTERM` | 取消 context、释放锁（尽力）、事务回滚；非事务迁移可能留下 `dirty` |
| `-timeout` 到期 | 同信号取消语义 |

合并前建议在 CI 中对 ephemeral 库执行 `-action up -dry-run` 做文件级演练；`status` 需要数据库连接与 DDL 权限，不适用于纯静态检查。

### 4.5 典型命令

| 场景 | 命令 |
|------|------|
| 本地首次建库 | `./bin/migrate -action up` |
| 发布前预检 | `./bin/migrate -action status` |
| 计划评审 | `./bin/migrate -action up -dry-run` |
| 发布执行 | `./bin/migrate -action up -lock-timeout 2m` |
| 回滚一步 | `./bin/migrate -action down -steps 1` |
| 回到基线 | `./bin/migrate -action down -to 0` |
| 查询当前版本 | `./bin/migrate -action version` |
| Makefile | `make migrate-up`（等价 `./bin/migrate -dir migrations -action up`） |

---

## 5. 迁移文件规范

### 5.1 目录

```text
migrations/
  000001_create_orders.up.sql
  000001_create_orders.down.sql
  000002_add_orders_name.up.sql
  000002_add_orders_name.down.sql
```

- 目录由 `-dir` 指定，默认仓库根 `migrations/`；
- 不同数据库（若需多库）使用**不同目录**，分别执行，例如 `migrations/mysql`、`migrations/postgres`；
- 迁移文件不区分方言，SQL 必须与目标库匹配。

### 5.2 命名（DATA-01）

```text
^(\d{6})_([a-z0-9]+(?:_[a-z0-9]+)*)\.(up|down)\.sql$
```

| 规则 | 要求 | 示例 |
|------|------|------|
| 序号 | 六位十进制，单调递增，> 0 | `000001` |
| 描述 | 小写字母/数字，单词间单下划线 | `create_orders` |
| 方向 | `.up.sql` / `.down.sql` | `000001_create_orders.up.sql` |
| 配对 | 每个 up 必须有同名 down（DATA-02） | 见上 |
| 唯一 | 同一版本只能有一个 up 与一个 down，描述必须一致 | — |

不合规示例：`1_init.up.sql`（非六位）、`000001_Create.up.sql`（大写）、`000001_create.sql`（缺方向）、`000001_create.up.sql.bak`。

### 5.3 加载期校验

`LoadDir` / `LoadFS` 会在**执行前**拒绝：

- 文件名不匹配命名规则；
- 只有 down 没有 up（孤儿 down）；
- 只有 up 没有 down（`ErrMissingDown`；库调用方可显式 `AllowMissingDown`，CLI 不允许）；
- 同版本 up/down 描述不一致；
- 重复版本 / 重复方向文件；
- up 或 down 内容为空白；
- 目录中存在其他非迁移文件（`README.md`、`.gitkeep` 除外；子目录被忽略）。

校验失败返回 `ErrInvalidSource` / `ErrMissingDown`，且不会连接数据库执行任何 SQL。

### 5.4 SQL 切分规则

默认按 `;` 切分为多条语句后逐条执行。

| 语法 | 支持 | 说明 |
|------|------|------|
| 行注释 `-- ...` | 是 | 到行尾 |
| 块注释 `/* ... */` | 是 | 支持嵌套 |
| MySQL `#` 行注释 | 是（MySQL 方言） | ANSI 方言下 `#` 不视为注释 |
| 单引号字符串 `'...'` | 是 | `''` 转义；MySQL 额外支持反斜杠转义 |
| 双引号标识符 / 字符串 `"..."` | 是 | `""` 转义 |
| 反引号标识符 `` `...` `` | 是 | `` `` `` 转义 |
| PostgreSQL dollar-quote `$$...$$` / `$tag$...$tag$` | 是 | 函数体、`DO` 块 |
| 末尾无分号 | 是 | 最后一段作为语句 |
| MySQL `DELIMITER` | **否** | 见下 |
| 未闭合引号 / 注释 / dollar-quote | 报错 | `ErrMalformedSQL`，绝不静默截断 |

注意事项：

- 语句前的注释会保留在该语句中（例如 `-- IRREVERSIBLE: ...`）；
- 空白语句被丢弃；
- **存储过程 / 触发器**：不要依赖 `DELIMITER`。可把整个 `CREATE PROCEDURE` 写成单条语句（内部 `;` 位于 `BEGIN...END` 中会被误切），此时使用 `-no-split` 将整份文件作为一条语句；
- `-no-split` 依赖驱动多语句能力：
  - PostgreSQL（pgx）可用；
  - MySQL 需在 DSN 加 `multiStatements=true`，否则报语法错误；
  - 多语句模式下错误定位与部分回滚能力变弱，除非必要不要开启。

### 5.5 事务模型

默认：**每条迁移一个事务**，顺序为

```text
BEGIN
  INSERT INTO schema_migrations (..., dirty = true)
  执行 up/down 的全部语句
  UPDATE / DELETE schema_migrations
COMMIT
```

| 数据库 | DDL 事务性 | 失败结果 |
|--------|-----------|----------|
| PostgreSQL | DDL 可回滚 | 回滚，历史无记录（或回滚到 dirty=false 的旧状态） |
| MySQL / MariaDB | DDL 隐式提交 | 可能部分生效；历史保留 `dirty = true`，阻断后续 `up`/`down` |

非事务迁移：在 `.up.sql` 中单独一行声明

```sql
-- +migrate no-transaction
```

- 只识别 **`.up.sql`** 中的指令；该标记对 up 与 down 两个方向都生效（down 文件无需重复声明）；
- 指令行必须**独占一行**，允许前后空白；
- 适用于无法在事务中执行的语句，例如 PostgreSQL `CREATE INDEX CONCURRENTLY`、`VACUUM`；
- 非事务迁移失败后会留下 `dirty` 记录，必须按 §11.1 人工确认。

### 5.6 checksum 语义

- `checksum` = **up 文件原始字节**的 SHA-256 小写十六进制；
- 注释、空格、换行、大小写变化都会改变 checksum；
- down 文件**不参与** checksum（修改 down 不会被检测，Review 需重点看 down）；
- 已应用迁移一旦被修改，`up` / `down` 都会以 `ErrChecksumMismatch` 拒绝执行；
- 手工计算校验和（与迁移器一致）：

```bash
sha256sum migrations/000001_create_orders.up.sql
```

```powershell
(Get-FileHash -Algorithm SHA256 migrations\000001_create_orders.up.sql).Hash.ToLower()
```

**正确做法**：需要变更时新增一条迁移，而不是修改已应用文件。若只是误改，用版本库恢复原文件：

```bash
git checkout <已发布 commit> -- migrations/000001_create_orders.up.sql
```

---

## 6. 编写迁移

### 6.1 原则

1. **小步**：一次迁移尽量只做一件事，便于定位失败与回滚。
2. **可回滚**：down 必须真实可执行，且在 staging 验证过（DATA-02）。
3. **幂等**：重复执行不产生副作用（`IF NOT EXISTS` / `IF EXISTS` / `ON CONFLICT` 等）。
4. **expand → contract**：先加结构，再发兼容代码，最后删旧结构（DATA-05）。
5. **不依赖应用代码**：迁移只描述数据与结构，不调用业务逻辑。
6. **不锁大表**：大表变更评估在线 DDL，必要时拆分（DATA_STANDARDS §3）。
7. **多租户**：涉及商户表时必须有 `merchant_id`（或等价列）与索引（DATA-06）。
8. **敏感数据**：迁移中不得写入真实 PII / 生产导出数据（SEC / DATA-07）。

### 6.2 PostgreSQL 模板

`migrations/000003_add_orders_status_index.up.sql`

```sql
-- expand：为订单状态列表加复合索引（不改变既有列）
CREATE INDEX IF NOT EXISTS idx_orders_merchant_status_created
    ON orders (merchant_id, status, created_at DESC);
```

`migrations/000003_add_orders_status_index.down.sql`

```sql
DROP INDEX IF EXISTS idx_orders_merchant_status_created;
```

并发建索引（不阻塞写入）：

```sql
-- +migrate no-transaction
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_orders_user_created
    ON orders (user_id, created_at DESC);
```

```sql
-- down
-- +migrate no-transaction
DROP INDEX CONCURRENTLY IF EXISTS idx_orders_user_created;
```

### 6.3 MySQL 模板

`migrations/000004_add_orders_channel.up.sql`

```sql
ALTER TABLE orders
    ADD COLUMN channel VARCHAR(16) NOT NULL DEFAULT '' AFTER currency;

CREATE INDEX idx_orders_channel_created ON orders (channel, created_at);
```

`migrations/000004_add_orders_channel.down.sql`

```sql
DROP INDEX idx_orders_channel_created ON orders;
ALTER TABLE orders DROP COLUMN channel;
```

注意：

- MySQL 8.0 支持 `ADD COLUMN ... ALGORITHM=INSTANT`（8.0.29 起列位置调整也可 instant），但需按版本、列位置与索引情况评估；
- DDL 隐式提交，失败后必须检查 `dirty`；
- `DROP INDEX ... ON table` 与 PostgreSQL 语法不同，需按目标库编写。

### 6.4 加列、回填、收紧（expand / contract 三段式）

以“新增非空列”为例，禁止一步 `ADD COLUMN NOT NULL` 锁大表或导致旧代码写入失败：

```text
000010_add_orders_region.up.sql      -- expand：可空列 + 索引
000011_backfill_orders_region.up.sql -- 分批回填（可重复执行）
（发布兼容新旧 schema 的代码）
000012_require_orders_region.up.sql  -- contract：收紧约束
```

回填迁移示例（分批、幂等、可中断）：

```sql
-- 每批 5000 行；不追求一次完成，允许重复执行
UPDATE orders
SET region = 'unknown'
WHERE region IS NULL
LIMIT 5000;
```

生产建议由受控脚本或任务分批执行；迁移中只保留可重复执行的小批量语句。

当回填必须由应用工具执行（例如 PII 加密依赖应用密钥，无法写成 SQL）时，优先把「结构」与「数据」分开：
迁移一次把最终结构建好，不在中间版本留下明文列或可空中转列；迁移全部执行完成后由 A 端服务在启动期写入加密数据。

```bash
# 1) 迁移一次跑完（最终结构即加密态）
./bin/migrate -action up
# 2) 启动 admin-api：身份模块自动幂等创建系统默认平台管理员
#    已存在则只校验凭据与绑定，不覆盖、不重复审计
./bin/admin-api -config configs/dev/admin.yaml
```

身份域即按此实现：`migrations/000001_create_identity_tables` 直接建最终加密结构，
`migrations/000005_seed_bootstrap_admin` 只建角色与授权；首个管理员由 A 端 `identityruntime`
启动期调用 `bootstrap.EnsureAdmin` 幂等创建（含账号、密码凭据、角色绑定与审计），不再依赖独立 CLI。
只有结构本身也必须分阶段（例如大表新增非空列）时才使用 `-to` 分段，并预先确认中断后的
`dirty` 处置方式。

### 6.5 大表与在线 DDL

| 场景 | 建议 |
|------|------|
| PostgreSQL 建索引 | `CREATE INDEX CONCURRENTLY` + `-- +migrate no-transaction` |
| PostgreSQL 加约束 | 先 `NOT VALID`，再 `VALIDATE CONSTRAINT`（分两步迁移） |
| MySQL 加列 | 评估 `ALGORITHM=INSTANT/INPLACE`，避开业务高峰 |
| 修改列类型 / 重建表 | 使用在线 DDL 工具（gh-ost / pt-online-schema-change），并把最终结构变更补成迁移 |
| 大批量数据订正 | 独立数据迁移任务 + 限速 + 断点续跑；不要放在长事务里 |

> 迁移器不替你判断锁表风险；MR 必须写明影响窗口与回滚方案。

### 6.6 不可逆变更（DATA-02 / §4.3）

物理上无法安全回滚时：

1. 先提交 ADR（原因、影响、回滚替代方案、审批人）；
2. `.down.sql` 必须以**失败语句**开头并注明 ADR，禁止静默成功：

PostgreSQL：

```sql
-- IRREVERSIBLE: ADR-00XX — do not run in production without Owner approval
DO $$ BEGIN RAISE EXCEPTION 'IRREVERSIBLE: ADR-00XX'; END $$;
```

MySQL：

```sql
-- IRREVERSIBLE: ADR-00XX — do not run in production without Owner approval
SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IRREVERSIBLE: ADR-00XX';
```

这样误执行 `down` 会立刻失败，而不是假装回滚成功。

### 6.7 反例清单

| 反例 | 问题 |
|------|------|
| 修改已应用的 up 文件 | checksum 漂移，阻断所有后续执行 |
| up 没有 down | 违反 DATA-02，CI 直接失败 |
| down 写成 `SELECT 'skip'` | 假回滚，事故时不可用 |
| 一次迁移做多域大改 | 失败难定位，回滚粒度粗 |
| `DELETE FROM orders;` 无条件数据变更 | 不可逆且影响全租户 |
| 在 API / worker `main` 调迁移 | 违反 DATA-03，多副本并发执行 |
| 在生产手工 `ALTER` 后不补迁移 | 违反 DATA-04，环境漂移 |
| 迁移里写真实手机号 / 身份证 | 违反 SEC / DATA-07 |

---

## 7. 迁移历史表

### 7.1 表结构

首次执行 `up` / `down` / `status` 时幂等创建（`-table` / `-schema` 可改）：

```sql
CREATE TABLE IF NOT EXISTS "schema_migrations" (
    version      BIGINT       PRIMARY KEY,
    description  VARCHAR(255) NOT NULL,
    checksum     VARCHAR(64)  NOT NULL,
    applied_at   TIMESTAMP    NOT NULL,
    execution_ms BIGINT       NOT NULL,
    dirty        BOOLEAN      NOT NULL
);
```

| 字段 | 语义 |
|------|------|
| `version` | 六位序号对应的整数（`000001` → `1`） |
| `description` | 文件名描述，用于审计与排障 |
| `checksum` | 应用时 up 文件的 SHA-256 |
| `applied_at` | 应用时间（迁移器按 UTC 写入；MySQL `TIMESTAMP` 会受会话时区影响，建议 DSN `loc=UTC`） |
| `execution_ms` | 执行耗时（毫秒）；失败记录为 0 或上次值 |
| `dirty` | `true` = 迁移未完成，需人工确认；阻断后续执行 |

### 7.2 常用查询

```sql
-- 当前版本与状态
SELECT version, description, checksum, applied_at, execution_ms, dirty
FROM schema_migrations
ORDER BY version;

-- 最近 5 条
SELECT version, description, applied_at, execution_ms
FROM schema_migrations
ORDER BY version DESC
LIMIT 5;

-- 是否存在未完成迁移
SELECT version, description FROM schema_migrations WHERE dirty = true;
```

### 7.3 红线

- **不要**手工插入 / 删除历史记录来“跳过”迁移；
- **不要**在 `dirty=true` 时直接改成 `false`（先确认数据库实际状态，见 §11.1）；
- **不要**随意修改 `checksum`（会掩盖源文件漂移，破坏审计）；
- 允许的例外只有 §11 的恢复流程，且需工单 + 双人复核 + 备份。

---

## 8. 咨询锁与并发

### 8.1 实现

| 方言 | 锁 | 释放 | 说明 |
|------|----|------|------|
| PostgreSQL | `pg_advisory_lock(key)` | `pg_advisory_unlock(key)` | 会话级锁，独占连接 |
| MySQL | `GET_LOCK(name, seconds)` | `RELEASE_LOCK(name)` | 命名锁，独占连接 |
| SQLite / generic | 无锁 | — | 须部署侧保证单执行者 |

锁键派生：`FNV-1a 64(schema + "." + table)`。

- 不同 `schema` / 历史表 → 不同锁，可并行迁移不同库；
- 锁在会话结束时自动释放（进程被 kill 也不会永久残留）；
- 同一库的多个 `migrate` 进程会串行等待，不会并发执行迁移。

### 8.2 超时与排障

- PostgreSQL：`pg_advisory_lock` 本身阻塞；`-lock-timeout` 到期会取消查询并返回 `ErrLockTimeout`。
- MySQL：`GET_LOCK` 使用向上取整的秒数等待；未获取返回 `ErrLockTimeout`。

排查占用者（需 DBA 权限）：

```sql
-- PostgreSQL：谁持有 advisory lock
SELECT l.pid, l.locktype, l.granted, a.state, a.query
FROM pg_locks l
JOIN pg_stat_activity a ON a.pid = l.pid
WHERE l.locktype = 'advisory';

-- MySQL：命名锁被哪个连接持有（name 见下方计算说明）
SELECT IS_USED_LOCK('admin_frame_migrate_0123456789abcdef');
```

- 确认是残留进程后，可经审批 `pg_cancel_backend(pid)` / `KILL <id>`；
- 不要为了“抢锁”直接重启数据库；
- 发布流水线应保证同一环境同一时刻只有**一个** migrate Job。

### 8.3 连接数要求

锁占用 1 条连接，迁移事务占用 1 条：

- CLI 固定 `MaxOpenConns=4`，满足要求；
- 自行构造 `*sql.DB` 时，`MaxOpenConns=1` 会被 `migrate.New` 拒绝（`ErrInvalidConfig`）；
- `MaxOpenConns=0`（无上限）不会被拒绝，但生产应显式设置。

---

## 9. 一致性校验与错误码

### 9.1 错误对照表

所有错误均可用 `errors.Is` 判断。

| 错误 | 触发条件 | 处置 |
|------|----------|------|
| `ErrInvalidConfig` | 驱动/方言为空或未知、表名/schema 非简单标识符、负超时、`steps<=0`、`MaxOpenConns=1` | 修正参数 |
| `ErrInvalidSource` | 文件名不合规、孤儿 down、描述不一致、重复版本、空 SQL、目录非法 | 修正文件（§5） |
| `ErrMissingDown` | 缺少 down 或 down 为空 | 补 down（DATA-02） |
| `ErrMalformedSQL` | 引号、注释、dollar-quote 未闭合 | 修正 SQL |
| `ErrChecksumMismatch` | 已应用版本的 up 文件被修改 | §11.2 |
| `ErrDirty` | 历史表存在 `dirty=true` | §11.1 |
| `ErrOutOfOrder` | 存在版本号小于当前最大已应用版本的 pending | §11.3 |
| `ErrMissingMigration` | 历史表版本在源目录缺失 | §11.4 |
| `ErrLockTimeout` | 锁等待超时或未获取 | §11.5 |
| `ErrStatementFailed` | 某条 SQL 执行失败（含包装的数据库错误） | §11.6 |
| `ErrCommitFailed` | 提交失败，数据库状态不确定 | 立即停手，检查库状态 |
| `ErrNotApplied` | `down` 时目标版本已不在历史表 | 重新 `status` 后重试 |

### 9.2 `Status` 字段

| 字段 | 含义 | 是否阻断执行 |
|------|------|--------------|
| `Current` | 最大已应用版本 | 否 |
| `Dirty` | 存在未完成迁移 | 是（`ErrDirty`） |
| `Applied` | 历史记录 | 否 |
| `Pending` | 待执行迁移 | 否 |
| `Drift` | checksum 不一致版本 | 是（`ErrChecksumMismatch`） |
| `Missing` | 源文件缺失版本 | 是（`ErrMissingMigration`） |

`status` 本身**不报错**，只报告问题；`up` / `down` 会拒绝执行。这是有意的：允许运维先诊断。

### 9.3 执行顺序

```text
获取咨询锁
  └─ 幂等建历史表
      └─ 读取历史
          ├─ dirty?         → ErrDirty
          ├─ 版本缺失?      → ErrMissingMigration
          ├─ checksum 漂移? → ErrChecksumMismatch
          └─ 有待执行?
              ├─ 乱序?      → ErrOutOfOrder
              └─ 逐条执行（事务 / 非事务），失败即停
```

---

## 10. 发布流程

### 10.1 推荐时序（expand → 代码 → contract）

```text
开发             CI                staging             生产
 │                │                   │                 │
 ├─ 新增 up/down ─┤                   │                 │
 │                ├─ check-migrations │                 │
 │                ├─ 单元测试          │                 │
 │                └─ up -dry-run ──────┤                 │
 │                                    ├─ up（真实演练）  │
 │                                    ├─ down（验证）    │
 │                                    └─ 冒烟            │
 │                                                      ├─ 1. migrate up（expand）
 │                                                      ├─ 2. 发布兼容代码
 │                                                      ├─ 3. 冒烟 / 指标
 │                                                      └─ 4. contract 迁移（可选，确认无旧读）
```

### 10.2 各环境步骤

| 环境 | 要求 |
|------|------|
| 本地 | `up` → `status` → `down` 验证 down 可执行 |
| CI | `make check-migrations`；对 ephemeral 库 `up -dry-run` 或完整 `up` + `down` |
| staging | 与生产同版本引擎；完整执行 up/down，记录耗时与锁等待 |
| 生产 | 独立 Job、单实例、指定 `-lock-timeout` 与 `-timeout`；发布窗口内监控 |

发布 Job 示例：

```bash
# 凭据由 CI Secret 注入，禁止回显
./bin/migrate -config configs/migrate.yaml -action status
./bin/migrate -config configs/migrate.yaml -action up \
  -lock-timeout 2m -statement-timeout 10m -timeout 30m
./bin/migrate -config configs/migrate.yaml -action status
```

### 10.3 发布前 Checklist

- [ ] up/down 成对，命名符合 DATA-01（`make check-migrations`）
- [ ] down 在 staging 实跑通过（不是占位）
- [ ] 无 API/worker 启动路径调用迁移（DATA-03）
- [ ] expand / contract 顺序已写入 MR
- [ ] 大表 / 锁表风险已评估，影响窗口已写明
- [ ] 涉及商户表时 `merchant_id` 与索引已覆盖（DATA-06）
- [ ] 不可逆变更已挂 ADR，down 显式失败（§6.6）
- [ ] 备份可用、恢复路径明确（DATA-07）
- [ ] 发布 Job 单实例、凭据来自 Secret、日志脱敏

### 10.4 回滚决策

```text
迁移已执行，业务异常
├─ 代码问题，schema 兼容旧版 → 回滚应用镜像（不 down）
├─ schema 与旧代码不兼容
│   ├─ down 已在 staging 验证 → 先回滚应用，再 migrate down
│   └─ 不可逆 / down 不可用 → 走数据修复预案 + 工单，禁止盲目 down
└─ 数据已损坏 → 停写、备份、按 runbook 恢复
```

详见 [`docs/runbooks/release-rollback.md`](./runbooks/release-rollback.md) 与 [`RELEASE_STANDARDS.md`](./RELEASE_STANDARDS.md) §5.2。

---

## 11. 故障处置

> 以下操作均属于生产变更：须工单 + 双人复核，执行前确认已有可用备份。  
> 通用第一步：**停止所有 migrate Job**，避免边修边跑。

### 11.1 `dirty = true`

**现象**：`up` / `down` 报 `migrate: dirty migration: version ... 未完成`。

**原因**：非事务迁移或 MySQL DDL 隐式提交导致中途失败；回滚过程中失败也会留下 dirty。

**处置**：

1. 查看记录：

```sql
SELECT version, description, checksum, applied_at, execution_ms, dirty
FROM schema_migrations
WHERE dirty = true;
```

2. 对照该版本 SQL，逐条确认数据库实际状态（表/列/索引是否存在，数据是否已变更）。
3. 二选一：
   - **前滚**：人工补完剩余语句，确认结构正确后清除 dirty：

```sql
UPDATE schema_migrations
SET dirty = false, execution_ms = <实际耗时毫秒>
WHERE version = <N>;
```

   - **回退**：人工撤销已生效部分，然后删除记录：

```sql
DELETE FROM schema_migrations WHERE version = <N>;
```

4. 重新 `-action status`，确认 `dirty: false`、pending 正确，再执行 `up`。

**禁止**：不看库状态直接 `UPDATE ... SET dirty=false`。

### 11.2 checksum 漂移

**现象**：`ErrChecksumMismatch: versions 000001 的 up SQL 已被修改`。

**原因**：已应用迁移文件被修改（包括注释/空格）。

**处置**：

1. 用版本库恢复原文件（首选）：

```bash
git log -- migrations/000001_create_orders.up.sql
git checkout <已发布 commit> -- migrations/000001_create_orders.up.sql
```

2. 如果变更确实必要：**新增**一条迁移实现变更，不要改旧文件。
3. 仅当确认历史记录有误且经 DBA 审批时，才允许改 checksum：

```sql
UPDATE schema_migrations SET checksum = '<sha256>' WHERE version = <N>;
```

该操作会破坏审计可信度，必须记录工单原因。

### 11.3 乱序迁移

**现象**：`ErrOutOfOrder: version 000003_x < applied 000005`。

**原因**：在已应用更高版本后，才合入低版本迁移（常见于多分支并行）。

**处置**：

1. 首选：把新迁移重命名为当前最大版本 + 1（保持六位）：

```bash
git mv migrations/000003_add_x.up.sql migrations/000006_add_x.up.sql
git mv migrations/000003_add_x.down.sql migrations/000006_add_x.down.sql
```

2. 不建议：`-allow-out-of-order`。仅在 DBA 确认该迁移与更高版本无依赖、且必须补跑时使用，并记录原因。

### 11.4 历史记录在源目录缺失

**现象**：`ErrMissingMigration: version 000004 (...)`。

**原因**：迁移文件被删除/重命名，而数据库已应用该版本。

**处置**：

- 从对应发布 tag 恢复文件（首选）；
- 恢复后 `status` 应不再显示 `missing from source`；
- CLI 不提供 `AllowMissingApplied`；库调用方可显式开启，但生产不建议。

### 11.5 锁等待 / `ErrLockTimeout`

**现象**：命令卡住后报 `migrate: lock timeout`。

**处置**：

1. 确认是否有其他 migrate 进程 / 发布 Job 正在执行；
2. 按 §8.2 查询持有者；
3. 等待其完成，或经审批终止残留会话；
4. 必要时提高 `-lock-timeout`（如长迁移），但不要无限等待。

### 11.6 语句失败 / 提交失败

**`ErrStatementFailed`**：

- 事务型：已自动回滚，历史无该版本记录；修正 SQL 后可重跑（此时文件尚未应用，允许修改）；
- 非事务型（MySQL DDL / `no-transaction`）：按 §11.1 检查 dirty 与库状态；
- 错误信息包含 `version_desc` 与语句序号，定位到具体语句。

**`ErrCommitFailed`**：

- 数据库状态**不确定**（可能已提交也可能未提交）；
- 立即停止后续操作，检查连接、磁盘、主从状态；
- 若 `schema_migrations` 有记录但结构不完整 → 按 §11.1；
- 必要时从备份恢复并复盘。

### 11.7 历史表丢失 / 被清空

**现象**：`status` 显示 `current version: 000000`，全部迁移变成 pending；但表结构实际已存在。

**原因**：误删 `schema_migrations`，或恢复到不含该表的备份。

**处置**：

1. **不要**直接 `up`（会重复执行 DDL/DML）；
2. 若数据库结构完好，重建历史表并补录已应用版本（需 DBA 审批）：

```sql
CREATE TABLE IF NOT EXISTS schema_migrations (
    version      BIGINT       PRIMARY KEY,
    description  VARCHAR(255) NOT NULL,
    checksum     VARCHAR(64)  NOT NULL,
    applied_at   TIMESTAMP    NOT NULL,
    execution_ms BIGINT       NOT NULL,
    dirty        BOOLEAN      NOT NULL
);

INSERT INTO schema_migrations
    (version, description, checksum, applied_at, execution_ms, dirty)
VALUES
    (1, 'create_orders', '<sha256-of-up-file>', CURRENT_TIMESTAMP, 0, false);
```

3. 更稳妥：从备份恢复整库（历史表随备份恢复）；
4. 完成后 `status` 必须显示正确的 current 与 pending。

---

## 12. 安全与合规

| 要求 | 说明 |
|------|------|
| 凭据不入库 | 仓库只保留 `*.yaml.example`；DSN 来自环境变量 / Secret（CFG-06、SEC-01） |
| 日志脱敏 | `pkg/db` 的错误与日志使用 `RedactDSN`，不输出完整口令（SEC-05） |
| 不回显 DSN | 优先 `ADMIN_FRAME_DB_DSN`，避免 `-dsn` 进入 shell 历史 / 进程列表 |
| 最小权限 | 迁移账号只授予目标库所需 DDL/DML；不与业务只读账号共用 |
| 双人变更 | 生产迁移与手工修数须工单 + 双人（DATA-04） |
| 禁止手工改生产 | 常规变更必须走迁移文件；应急后 24h 内补迁移 |
| 备份 | 迁移前确认可恢复备份（DATA-07） |
| 审计 | 历史表即最小审计记录；重要变更另记工单与发布记录 |
| 测试数据 | 不得把未脱敏生产数据带入开发 / CI（TEST-05、DATA-07） |

最小权限示例（按实际 SQL 裁剪）：

PostgreSQL：

```sql
GRANT CONNECT ON DATABASE admin_frame TO app_migrator;
GRANT USAGE, CREATE ON SCHEMA public TO app_migrator;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO app_migrator;
```

MySQL：

```sql
GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, ALTER, DROP, INDEX, REFERENCES
    ON admin_frame.* TO 'app_migrator'@'%';
```

> `status` 也会建历史表；只读账号无法执行 `status`。若需只读检查，请使用 DBA 提供的只读副本查询 SQL。

---

## 13. 在 Go 代码中使用 `pkg/migrate`

仅建议在**受控工具**（`cmd/migrate`、发布 Job、测试）中使用，**禁止**在 API/worker 启动路径调用。

### 13.1 API 一览

| 符号 | 作用 |
|------|------|
| `LoadDir(dir, opts)` | 从本地目录加载并校验 |
| `LoadFS(fsys, dir, opts)` | 从 `io/fs.FS`（含 `embed.FS`）加载 |
| `ParseName(name)` | 解析文件名 → 版本、描述、方向 |
| `ValidateMigrations([]Migration)` | 校验手工构造的迁移 |
| `SplitStatements(script)` | 按 ANSI 语义切分 SQL |
| `New(*sql.DB, Config)` | 构造 Runner（校验 + 规范化） |
| `(*Runner).Up(ctx)` | 应用全部 pending |
| `(*Runner).Down(ctx)` / `DownSteps(ctx, n)` / `DownTo(ctx, v)` | 回滚 |
| `(*Runner).Status(ctx)` | 状态快照（会幂等建表） |
| `(*Runner).Config()` | 返回生效配置 |
| `TextReporter(io.Writer)` | 文本事件输出 |

### 13.2 示例（`embed.FS`）

```go
package release

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"os"

	"github.com/admin-frame/admin-frame/pkg/migrate"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Apply 在发布 Job 中执行迁移；不得从 API / worker 启动路径调用（DATA-03）。
func Apply(ctx context.Context, sqlDB *sql.DB) error {
	migs, err := migrate.LoadFS(migrationsFS, "migrations", migrate.LoadOptions{})
	if err != nil {
		return err
	}

	r, err := migrate.New(sqlDB, migrate.Config{
		Migrations:  migs,
		Dialect:     migrate.DialectPostgres,
		Table:       "schema_migrations",
		LockTimeout: 0, // 0 → 默认 30s
		Observer:    migrate.TextReporter(os.Stdout),
	})
	if err != nil {
		return err
	}

	if _, err := r.Up(ctx); err != nil {
		switch {
		case errors.Is(err, migrate.ErrDirty):
			// 人工确认数据库状态后再继续（见迁移指南 §11.1）
		case errors.Is(err, migrate.ErrChecksumMismatch):
			// 恢复被修改的迁移文件（§11.2）
		}
		return err
	}
	return nil
}
```

### 13.3 `Config` 字段

| 字段 | 默认 | 说明 |
|------|------|------|
| `Migrations` | 必填 | `LoadDir` / `LoadFS` 的结果；`New` 会复制并补全 checksum / NoTx，不修改调用方切片 |
| `Dialect` | 必填 | `DialectPostgres` / `DialectMySQL` / `DialectSQLite` / `DialectGeneric` |
| `Table` | `schema_migrations` | 必须是 `[A-Za-z_][A-Za-z0-9_]*` |
| `Schema` | 空 | 历史表 schema；空表示连接默认 |
| `LockTimeout` | 30s | `0` 表示默认；负数非法 |
| `StatementTimeout` | 0 | 单条语句超时；0 不限 |
| `DryRun` | false | 只计划，不执行迁移 SQL、不取锁；**仍建/读历史表** |
| `NoSplit` | false | 整份文件作为一条语句 |
| `AllowOutOfOrder` | false | 允许乱序（慎用） |
| `AllowMissingApplied` | false | 允许历史版本在源目录缺失（CLI 未暴露） |
| `Observer` | nil | 同步回调，禁止阻塞 / panic / 重入 Runner |

### 13.4 关键类型

| 类型 | 关键字段 |
|------|----------|
| `Migration` | `Version`、`Description`、`Up`、`Down`、`Checksum`、`NoTx` |
| `Record` | `Version`、`Description`、`Checksum`、`AppliedAt`、`ExecutionMS`、`Dirty` |
| `Status` | `Current`、`Dirty`、`Applied`、`Pending`、`Drift`、`Missing` |
| `Result` | `Direction`、`DryRun`、`Steps` |
| `Step` | `Version`、`Description`、`Direction`、`Statements`、`Duration` |
| `Event` | `Kind`（plan/start/done/error）、`Direction`、`Version`、`Statements`、`Duration`、`Err` |

并发约束：

- `Runner` 创建后只读，`Status` 可并发调用；
- 同一个 `Runner` 的 `Up` / `Down` 不应并发调用（虽然数据库锁会串行化，但结果与日志会混杂）；
- `Observer` 在调用方 goroutine 同步执行，不得阻塞或 panic，也不得在回调内重入 `Up`/`Down`。

---

## 14. 测试与 CI

### 14.1 仓库门禁

```bash
make check-migrations   # DATA-01/02/03：命名、配对、禁止业务进程自动迁移
make build-migrate
make migrate-up         # 需要 ADMIN_FRAME_DB_* 指向本地库
```

`check-migrations.sh` 会：

- 校验 `migrations/*.up.sql` / `*.down.sql` 命名与配对；
- 拒绝目录中的非迁移文件；
- 扫描 `cmd/admin-api`、`cmd/biz-api`、`cmd/c-api`、`cmd/worker`，禁止 `AutoMigrate` / `migrate.Up` 等模式。

### 14.2 单元测试

```bash
go test ./pkg/migrate/... -count=1
go test ./cmd/migrate/... -count=1
```

- `pkg/migrate` 使用内存伪 `database/sql` 驱动，不依赖外置数据库（TEST-05）；
- 覆盖加载/校验、SQL 切分、Up/Down/Status、checksum 漂移、dirty、乱序、缺失、锁调用、事务回滚、非事务失败等；
- 修改切分器、锁、历史表 SQL 时必须补用例。

### 14.3 CI 建议

1. 启动 ephemeral PostgreSQL / MySQL（testcontainers 或 service container）；
2. 执行 `migrate -action up -dry-run` 做文件级校验；
3. 执行 `migrate -action up`，再 `-action status` 确认 `dirty:false`、`pending: none`；
4. 执行 `-action down -to 0` 验证 down 可执行；
5. 销毁容器，不复用共享库（TEST-07）。

---

## 15. 限制与非目标

| 限制 | 说明 |
|------|------|
| CLI 仅 PG / MySQL | 库支持 `sqlite` / `generic` 方言，但需调用方自行注册驱动 |
| 无 schema diff | 不比较模型与库结构，不生成 SQL（非 AutoMigrate） |
| 无 `DELIMITER` | MySQL 存储过程需单语句写法或 `-no-split` + `multiStatements=true` |
| down 不校验 checksum | 修改 down 文件不会被检测；Review 必须覆盖 |
| 无自动生成 down | 必须手写并在 staging 验证 |
| 无 SQL lint / 影子库 | 需人工 Review；可选接入第三方 linter |
| 无自动备份 / 恢复 | 依赖 DATA-07 的备份体系 |
| 无并行执行 | 单库串行（锁保证），不同库可用不同历史表并行 |
| 无跨库事务 | 一次运行一个 DSN；多库分别执行 |
| `-table` / `-schema` 受限 | 只允许简单标识符，不支持带引号/点号的复杂名 |
| 历史表名变更 | 需人工迁移历史记录，不能自动识别旧表 |
| `status` 需 DDL 权限 | 因为会幂等创建历史表 |
| 无分布式锁（Redis 等） | 依赖数据库自身咨询锁 |

---

## 16. FAQ

| 问题 | 结论 |
|------|------|
| 可在 API / worker 启动时自动迁移？ | 不能。唯一入口是 `cmd/migrate` 或发布流水线（DATA-03）。 |
| 可重复执行 `up`？ | 可以，已应用版本按版本号跳过。 |
| 只改注释为何报 checksum 漂移？ | checksum 覆盖 up 文件全部原始字节；恢复原文件或新增迁移。 |
| 可修改已应用的 down 文件？ | 技术可行（不参与 checksum），但破坏可回滚性；须 staging 重验并走 Review。 |
| 两个 CI Job 同时跑 `up`？ | 咨询锁会串行化，后来者等待；仍建议流水线保证单实例。 |
| 支持 SQLite / Oracle / SQL Server？ | CLI 不支持；库提供 `DialectSQLite` / `DialectGeneric`，需自行注册驱动并验证锁与 DDL 事务语义。 |
| `-dry-run` 会改数据库？ | 不执行迁移 SQL，但会创建并读取历史表，需要 DDL 权限。 |
| CI 如何检查迁移已对齐？ | `status` 后确认 `dirty: false`、无 pending / drift / missing；或用 `version` 与期望版本比较。 |
| 迁移失败一半能直接重跑？ | 事务型自动回滚，可修正后重跑；非事务型先按 §11.1 处理 `dirty`。 |
| 如何只回滚某一条？ | 只能按版本顺序 `-steps` / `-to`；需跳过中间版本时写补偿迁移。 |
| 数据库口令会被打印？ | 不会，`pkg/db` 使用 `RedactDSN` 脱敏；仍建议用环境变量而非 `-dsn`。 |
| 分库怎么迁移？ | 每个库单独执行，使用不同 `-dir` / `-table` / DSN；锁键含 schema 与表名。 |

---

## 附录 A：命令与 SQL 速查

### A.1 命令

```bash
make build-migrate
./bin/migrate -action status
./bin/migrate -action up -dry-run
./bin/migrate -action up
./bin/migrate -action down -steps 1
./bin/migrate -action down -to 0
./bin/migrate -action version
./bin/migrate -version
```

### A.2 校验和

```bash
sha256sum migrations/000001_create_orders.up.sql
```

```powershell
(Get-FileHash -Algorithm SHA256 migrations\000001_create_orders.up.sql).Hash.ToLower()
```

### A.3 历史表

```sql
SELECT version, description, checksum, applied_at, execution_ms, dirty
FROM schema_migrations
ORDER BY version;

SELECT version, description FROM schema_migrations WHERE dirty = true;
```

### A.4 环境变量

```bash
ADMIN_FRAME_DB_DRIVER=postgres|mysql
ADMIN_FRAME_DB_DSN=<dsn>
```

---

## 附录 B：端到端示例

### B.1 新功能：为订单加 `channel` 列（MySQL）

1. 新建文件：

```text
migrations/
  000002_add_orders_channel.up.sql
  000002_add_orders_channel.down.sql
```

```sql
-- 000002_add_orders_channel.up.sql（expand：可空列，旧代码可继续写入）
ALTER TABLE orders ADD COLUMN channel VARCHAR(16) NULL;
CREATE INDEX idx_orders_channel_created ON orders (channel, created_at);
```

```sql
-- 000002_add_orders_channel.down.sql
DROP INDEX idx_orders_channel_created ON orders;
ALTER TABLE orders DROP COLUMN channel;
```

2. 本地验证：

```bash
make check-migrations
export ADMIN_FRAME_DB_DRIVER=mysql
export ADMIN_FRAME_DB_DSN='app:CHANGE_ME@tcp(127.0.0.1:3306)/admin_frame?parseTime=true&loc=UTC'

./bin/migrate -action status
./bin/migrate -action up -dry-run
./bin/migrate -action up
./bin/migrate -action down -steps 1   # 验证 down
./bin/migrate -action up              # 重新应用
```

3. 提 MR：写明 expand/contract 顺序、锁表评估、down 验证结果。

4. 生产发布：

```bash
./bin/migrate -config configs/migrate.yaml -action status
./bin/migrate -config configs/migrate.yaml -action up -lock-timeout 2m -timeout 30m
./bin/migrate -config configs/migrate.yaml -action status   # pending: none, dirty: false
```

### B.2 加非空列（三段式）

```text
000010_add_orders_region.up.sql       -- 可空列
000011_backfill_orders_region.up.sql  -- 分批回填（可重复）
（发布兼容代码）
000012_require_orders_region.up.sql   -- 收紧为 NOT NULL / 默认值
```

每步都可独立发布、独立回滚；禁止一步到位锁表。

### B.3 不可逆脱敏

```sql
-- 000020_mask_orders_phone.up.sql
UPDATE orders SET phone = CONCAT('***', RIGHT(phone, 4)) WHERE phone IS NOT NULL;
```

```sql
-- 000020_mask_orders_phone.down.sql
-- IRREVERSIBLE: ADR-0021 — 原手机号已不可恢复，禁止回滚
DO $$ BEGIN RAISE EXCEPTION 'IRREVERSIBLE: ADR-0021'; END $$;
```

---

## 附录 C：相关文件索引

| 路径 | 作用 |
|------|------|
| [`pkg/migrate/`](../pkg/migrate) | 迁移引擎（加载、切分、历史、锁、执行） |
| [`cmd/migrate/`](../cmd/migrate) | CLI 入口，注册 pgx / mysql 驱动 |
| [`migrations/`](../migrations) | 迁移 SQL 文件 |
| [`internal/platform/config/config.go`](../internal/platform/config/config.go) | `LoadDBConfig`：复用 `db.driver` / `db.dsn` 与环境变量 |
| [`pkg/db/`](../pkg/db) | 连接池、Ping、`RedactDSN` |
| [`scripts/ci/check-migrations.sh`](../scripts/ci/check-migrations.sh) | DATA-01/02/03 门禁 |
| [`docs/DATA_STANDARDS.md`](./DATA_STANDARDS.md) | 迁移硬约束与备份要求 |
| [`docs/RELEASE_STANDARDS.md`](./RELEASE_STANDARDS.md) | 发布顺序与回滚（RELS-06） |
| [`docs/runbooks/release-rollback.md`](./runbooks/release-rollback.md) | 发布回滚 runbook |
| [`docs/pkg-roadmap.md`](./pkg-roadmap.md) | pkg 能力路线图 |

---

## 变更记录

| 日期 | 变更 |
|------|------|
| 2026-10-20 | 首版：`pkg/migrate` + `cmd/migrate` 使用说明（PG/MySQL、锁、checksum、dirty、发布与故障处置） |
