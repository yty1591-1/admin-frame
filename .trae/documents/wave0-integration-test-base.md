# Wave 0 实施计划：集成测试基座（ENT-16 前置切片）

## Context

`docs/production-readiness-implementation-plan.md` 已批准，Wave 0 的目标是先让后续任务「可验证」：本地一条命令拉起 MySQL / Redis / MinIO，集成测试可运行，新增门禁可空跑且不阻塞 `make ci`。

已确认现状：

- `tests/integration/iam/*` 已有 3 个 MySQL 用例，通过 `IAM_TEST_MYSQL_DSN` 开关；未配置时显式 `t.Skipf`，与 `TESTING_STANDARDS.md` TEST-07 一致。
- `pkg/db`、`pkg/redis`、`pkg/storage`、`pkg/migrate` 均已存在，可直接复用；`pkg/storage/s3` 注册 `minio` 驱动。
- `docker-compose.storage.yml` 只有 MinIO，端口 9000/9001；`docker-compose.integration.yml` 尚不存在。
- `.github/workflows/ci.yml` 的 `test` job 通过 GitHub service 提供 MySQL，未覆盖 Redis / MinIO。
- `Makefile` 无 `test-integration` 入口；`make ci` 不包含重依赖集成测试。
- `.golangci.yml` 启用 revive（`blank-imports`、`exported`、`package-comments`），新增代码须满足 `CODE_STYLE.md` S-01～S-12。
- `pkg/*` 不得 import `internal/*`；`scripts/ci/check-layering.sh` 不检查 pkg，但仍须遵守分层原则。

## 实施内容

### 1. 新增 `pkg/testkit`

| 文件 | 内容 |
|---|---|
| `pkg/testkit/doc.go` | 包注释：只读 `TEST_*` 环境变量、无固定端口与真实凭据、缺失时显式跳过 |
| `pkg/testkit/env.go` | 环境变量常量与 `requireEnv`（去空格；空值时 `t.Skipf` 说明原因） |
| `pkg/testkit/repo.go` | `RepoRoot()`：用 `runtime.Caller(0)` 从 `pkg/testkit` 回溯两级，不依赖工作目录 |
| `pkg/testkit/mysql.go` | `MySQL(t)`：读取 `TEST_MYSQL_DSN`，校验 `parseTime=true`，`db.Open` 后执行 `migrate.LoadDir` + `Runner.Up`；`t.Cleanup` 关闭连接池 |
| `pkg/testkit/redis.go` | `Redis(t)`：读取 `TEST_REDIS_ADDR` / `TEST_REDIS_PASSWORD` / `TEST_REDIS_DB`，`redis.Open` 后 `t.Cleanup` 关闭 |
| `pkg/testkit/storage.go` | `Storage(t)`：读取 `TEST_S3_*`，`storage.Open` 使用 `minio` 驱动（`ForcePathStyle=true`，http 端点自动 `DisableSSL=true`） |

约束：

- 仅 import `pkg/*` 与标准库，不 import `internal/*`。
- MySQL 驱动、MinIO 驱动使用空白导入并写注释（S-06）。
- 环境变量缺失一律 `t.Skipf`，禁止静默通过（TEST-06 / TEST-07）。
- 不在代码中内置端口、口令或真实云凭据。

### 2. 新增 `docker-compose.integration.yml`

- `mysql:8.0`：测试库 `admin_frame_test`，用户 `app/app`，端口 `${TEST_MYSQL_PORT:-3307}:3306`，healthcheck `mysqladmin ping`。
- `redis:7-alpine`：端口 `${TEST_REDIS_PORT:-6380}:6379`，healthcheck `redis-cli ping`。
- `minio`：端口 `${TEST_S3_PORT:-9010}:9000`、`${TEST_S3_CONSOLE_PORT:-9011}:9001`。
- `minio-init`：`minio/mc` 一次性创建 `admin-frame` bucket，带重试循环。
- 所有口令均为测试默认值，文件头注释明确「禁止用于真实环境」（TEST-05 / CI-12）。

### 3. 新增 `scripts/ci/run-integration-tests.sh`

- `docker` 或 `docker compose` 不可用时打印 `SKIP` 并以 0 退出（门禁可空跑）。
- 导出 `TEST_*` 环境变量，同时设置 `IAM_TEST_MYSQL_DSN`（复用既有 IAM 集成用例，不改测试文件）。
- `docker compose -f docker-compose.integration.yml up -d --wait --wait-timeout 120 mysql redis minio`。
- `docker compose run --rm minio-init` 创建 bucket。
- `go test -count=1 ./tests/integration/...`。
- `trap` 清理：默认 `down -v`；`KEEP_INTEGRATION_STACK=1` 时保留现场便于排障。
- 不注入任何生产凭据。

### 4. 新增 `tests/integration/smoke/smoke_test.go`

- `TestSmokeMySQLMigration`：`testkit.MySQL(t)` 后查询 `schema_migrations` 最大 version > 0，证明迁移可执行。
- `TestSmokeRedis`：`testkit.Redis(t)` + `Ping`。
- `TestSmokeStorage`：`testkit.Storage(t)` + `storage.Ready`。
- 未配置环境变量时全部跳过；`go test ./...` 不会失败。

### 5. `Makefile`

- `.PHONY` 增加 `test-integration`。
- 新增 target：

```make
# ENT-16：真实 MySQL / Redis / MinIO 集成测试（Tier B，不并入 make ci）。
test-integration:
	bash scripts/ci/run-integration-tests.sh
```

### 6. `.github/workflows/ci.yml`

- 新增 `integration` job（Tier B）：checkout + setup-go + `bash scripts/ci/run-integration-tests.sh`。
- 不使用 `secrets.*`，不并入 `test` job；超时 25 分钟。

## 验证

1. `gofmt -l pkg/testkit tests/integration/smoke` 为空。
2. `go build ./...` 通过。
3. `go test ./...` 通过（无 Docker/环境变量时 smoke 全跳过）。
4. `make test-ci` 通过（含 `-race` 与 TEST-01～05 门禁）。
5. `bash scripts/ci/check-layering.sh` 通过。
6. 有 Docker 时执行 `bash scripts/ci/run-integration-tests.sh`，3 个 smoke 用例 + 既有 IAM 集成用例全绿。
7. `make secrets`：若 gitleaks 将 compose 测试默认口令判为泄露，在 `.gitleaks.toml` 增加仅限 `docker-compose.integration.yml` 的 allowlist 并注释「测试专用默认值」。
8. `git status` 确认无 `tmp_*`、`coverage.out`、容器数据目录等临时产物入库。

## 不做

- 不修改 `tests/integration/iam/*`（通过脚本兼容既有 `IAM_TEST_MYSQL_DSN`）。
- 不把集成测试并入 `make ci`。
- 不提前实现 ENT-01 身份域或 ENT-03 异步底座。
- 不引入 testcontainers / helm / K8s 依赖。
