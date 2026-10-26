# admin-frame

A/B/C 三端业务的 Go 模块化单体骨架，实现见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)。

| 进程 | 端口（默认） | 说明 |
|---|---|---|
| `admin-api` | `:8081` | A 平台运营 |
| `biz-api` | `:8082` | B 商家/企业 |
| `c-api` | `:8083` | C 消费者 |
| `worker` | — | 后台任务骨架（异步运行时待 ENT-03 落地） |
| `migrate` | — | 迁移脚本执行器 |

## 快速开始

```bash
# 1. 配置
cp configs/admin.yaml.example configs/admin.yaml
cp configs/biz.yaml.example   configs/biz.yaml
cp configs/c.yaml.example     configs/c.yaml
cp configs/worker.yaml.example configs/worker.yaml

# 2. 依赖与编译
go mod tidy
make build

# 3. 启动（任选一端）
make run-c
# 或 make run-admin / make run-biz
```

## 演示鉴权

当前为 **Demo Bearer Token**（生产请替换为各端独立 JWT）：

| 端 | Header |
|---|---|
| A | `Authorization: Bearer admin.staff-1` |
| B | `Authorization: Bearer biz.m100.staff-9` |
| C | `Authorization: Bearer c.user-42` |

### A 端 IAM 示例

```bash
curl -s "http://127.0.0.1:8081/admin/v1/iam/permissions?page=1&page_size=20" \
  -H "Authorization: Bearer admin.staff-1"
```

### 健康检查

```bash
curl -s http://127.0.0.1:8083/healthz
curl -s http://127.0.0.1:8083/readyz
```

## 目录要点

- `internal/iam/domain|app|infrastructure`：IAM 核心逻辑（三端共享）
- `internal/iam/transport/{admin,biz}`：按端隔离 Handler/DTO
- `internal/platform`、`internal/security`：进程生命周期、配置、鉴权中间件
- `api/openapi/{admin,biz,c}.yaml`：分端契约
- `migrations/`：可回滚 SQL
- `frontend/`：A/B 双端 Web monorepo（pnpm workspace），见 [前端架构与工程规范](docs/frontend-architecture.md)

## 前端

```bash
cd frontend
corepack enable && pnpm install --frozen-lockfile

pnpm dev:admin            # A 端 http://127.0.0.1:5171（/admin 代理到 :8081）
pnpm dev:biz              # B 端 http://127.0.0.1:5172（/biz 代理到 :8082）
pnpm gen:api              # 由 api/openapi/{admin,biz}.yaml 生成 TS 类型（入库）
```

工作区：`apps/admin-web`、`apps/biz-web`、`packages/{api-client,auth,ui,shared,config,testing}`。
运行期配置由部署注入 `public/config.js`（`window.__APP_CONFIG__`），构建期固定端别；A/B 独立 Token 域与存储命名空间。

## 工程规范

- 架构：[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
- **注释硬约束**：[docs/COMMENT_STANDARDS.md](docs/COMMENT_STANDARDS.md)（C-01～C-10）
- **代码风格硬约束**：[docs/CODE_STYLE.md](docs/CODE_STYLE.md)（S-01～S-12）
- **CI 硬约束**：[docs/CI_STANDARDS.md](docs/CI_STANDARDS.md)（CI-01～CI-14）
  - GitHub：`.github/workflows/ci.yml`
  - Gitee：`.workflow/`（见 [.workflow/README.md](.workflow/README.md)）
- **安全硬约束**：[docs/SECURITY_STANDARDS.md](docs/SECURITY_STANDARDS.md)（SEC-01～SEC-14；gitleaks / gosec）
- **API 硬约束**：[docs/API_STANDARDS.md](docs/API_STANDARDS.md)（API-01～API-12；oasdiff）
- **测试硬约束**：[docs/TESTING_STANDARDS.md](docs/TESTING_STANDARDS.md)（TEST-01～TEST-08）
- **依赖硬约束**：[docs/DEPENDENCY_STANDARDS.md](docs/DEPENDENCY_STANDARDS.md)（DEP-01～DEP-08）
- **配置硬约束**：[docs/CONFIG_STANDARDS.md](docs/CONFIG_STANDARDS.md)（CFG-01～CFG-08）
- **数据硬约束**：[docs/DATA_STANDARDS.md](docs/DATA_STANDARDS.md)（DATA-01～DATA-08）
- **数据库迁移指南**：[docs/migration-guide.md](docs/migration-guide.md)（`pkg/migrate` / `cmd/migrate` 使用与故障处置）
- **可靠性硬约束**：[docs/RELIABILITY_STANDARDS.md](docs/RELIABILITY_STANDARDS.md)（REL-01～REL-08）
- **可观测硬约束**：[docs/OBSERVABILITY_STANDARDS.md](docs/OBSERVABILITY_STANDARDS.md)（OBS-01～OBS-08）
- **Git 硬约束**：[docs/GIT_STANDARDS.md](docs/GIT_STANDARDS.md)（GIT-01～GIT-08）
- **发布硬约束**：[docs/RELEASE_STANDARDS.md](docs/RELEASE_STANDARDS.md)（RELS-01～RELS-08）
- **Runbook 硬约束**：[docs/RUNBOOK_STANDARDS.md](docs/RUNBOOK_STANDARDS.md)（RB-01～RB-07）
- **审计硬约束**：[docs/AUDIT_STANDARDS.md](docs/AUDIT_STANDARDS.md)（AUD-01～AUD-09）
- **许可证硬约束**：[docs/LICENSE_STANDARDS.md](docs/LICENSE_STANDARDS.md)（LIC-01～LIC-07）
- **代码生成硬约束**：[docs/CODEGEN_STANDARDS.md](docs/CODEGEN_STANDARDS.md)（GEN-01～GEN-07）
- **性能硬约束**：[docs/PERFORMANCE_STANDARDS.md](docs/PERFORMANCE_STANDARDS.md)（PERF-01～PERF-07）
- **前端架构与工程规范**：[docs/frontend-architecture.md](docs/frontend-architecture.md)（A/B 双端 monorepo；FE-01～FE-20）
  - 决策记录：[ADR-0005](docs/adr/0005-frontend-monorepo-react.md)
- **硬约束专文路线图**：[docs/standards-roadmap.md](docs/standards-roadmap.md)（P0～P4 已完成；分支保护待管理员勾选）
- pkg 路线图：[docs/pkg-roadmap.md](docs/pkg-roadmap.md)

## 测试与 CI

```bash
make test
make test-ci              # -race + coverage + TEST 门禁
make check-deps           # go.sum / replace / 许可证
make check-config         # 配置样例门禁
make check-migrations     # 迁移 up/down 配对
make check-reliability    # 超时客户端 / 限流码
make check-observability  # 日志字段 / 探针
make check-git            # PR 模板 / commitlint 配置
make check-release        # Dockerfile / 版本注入
make check-runbooks       # 基线 runbook / ALERTS
make check-audit          # 审计事件目录 / audit 包
make check-license        # 许可证允许/禁止列表
make check-codegen        # 生成物 MANIFEST
make check-perf           # 关键路径 Benchmark
make lint                 # 需已安装 golangci-lint
make openapi              # 分端 OpenAPI validate + breaking
make openapi-docs         # 渲染分端 HTML 文档（dist/openapi/docs）
make openapi-client       # 生成 TypeScript 类型（dist/openapi/client）
make openapi-changelog    # 相对基线生成变更日志（dist/openapi/changelog）
make openapi-artifacts    # 一次生成以上三类契约产物（需 Node.js 20+）
make frontend-ci          # 前端全量门禁（FE-01～FE-20：格式/lint/文案/边界/类型/测试/构建/体积/许可证/审计）
make frontend-e2e         # 前端 Playwright smoke（需先构建并安装浏览器）
make ci                   # 本地对齐托管平台必跑项
```

## 说明

- 默认 `db.driver: memory`，各 API 进程内存独立；跨进程联调请接入共享 MySQL/Postgres 并实现 SQL 仓储。
- `cmd/migrate` 已接入 PostgreSQL / MySQL 执行迁移；配置、发布与故障处置见 [docs/migration-guide.md](docs/migration-guide.md)。
