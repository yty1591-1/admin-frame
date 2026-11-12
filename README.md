# admin-frame

A/B/C 三端业务的 Go 模块化单体骨架，实现见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)。

| 进程 | 端口（默认） | 说明 |
|---|---|---|
| `admin-api` | `:8081` | A 平台运营 |
| `biz-api` | `:8082` | B 商家/企业 |
| `c-api` | `:8083` | C 消费者 |
| `worker` | — | 后台任务：Outbox Relay、通知投递、审计导出等（已有独立异步运行时） |
| `migrate` | — | 迁移脚本执行器（唯一 schema 变更入口） |

## 快速开始

```bash
# 1. 依赖：MySQL 8.0 + Redis 7（本地可用 compose 一键起）
docker compose -f docker-compose.integration.yml up -d mysql redis

# 2. 配置：无需复制配置文件；首次启动按代码默认值自动初始化（CFG-04 / CFG-09）
#    业务密钥由配置中心密钥子域托管，明文不入库、不落盘
# 3. 建库与迁移（驱动只支持 MySQL；见 docs/migration-guide.md）
make migrate-up
# 等价于：go run ./cmd/migrate -driver mysql -dsn "$ADMIN_FRAME_DB_DSN" -action up

# 4. 编译并启动
make build
make run-admin   # 或 make run-biz / run-c / run-worker
```

## 鉴权

三端统一使用 JWT（`auth.mode: jwt`，HS256 或 RS256），不存在 Demo Token：

```bash
# A 端登录；引导账号 admin@example.com / Admin#Bootstrap2026 首登强制改密
curl -s -X POST http://127.0.0.1:8081/admin/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"subject":"admin@example.com","password":"Admin#Bootstrap2026"}'
```

刷新、登出、会话撤销链路见 [docs/identity-authn.md](docs/identity-authn.md)；
`tests/e2e` 覆盖「登录 → 刷新轮换 → 登出撤销 → 刷新令牌重放拒绝」。

### A 端 IAM 示例

```bash
curl -s "http://127.0.0.1:8081/admin/v1/iam/permissions?page=1&page_size=20" \
  -H "Authorization: Bearer $ACCESS_TOKEN"
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

## 工程规范

- **文档地图**：[docs/README.md](docs/README.md)（文档分层、门禁对照、新增与归档判据）
- 架构：[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
- **注释硬约束**：[docs/COMMENT_STANDARDS.md](docs/COMMENT_STANDARDS.md)（C-01～C-10）
- **代码风格硬约束**：[docs/CODE_STYLE.md](docs/CODE_STYLE.md)（S-01～S-12）
- **CI 硬约束**：[docs/CI_STANDARDS.md](docs/CI_STANDARDS.md)（CI-01～CI-14）
  - GitHub：`.github/workflows/ci.yml`
  - Gitee：`.workflow/`（见 [.workflow/README.md](.workflow/README.md)）
- **安全硬约束**：[docs/SECURITY_STANDARDS.md](docs/SECURITY_STANDARDS.md)（SEC-01～SEC-16；gitleaks / gosec）
- **API 硬约束**：[docs/API_STANDARDS.md](docs/API_STANDARDS.md)（API-01～API-12；oasdiff）
- **测试硬约束**：[docs/TESTING_STANDARDS.md](docs/TESTING_STANDARDS.md)（TEST-01～TEST-08）
- **依赖硬约束**：[docs/DEPENDENCY_STANDARDS.md](docs/DEPENDENCY_STANDARDS.md)（DEP-01～DEP-08）
- **配置硬约束**：[docs/CONFIG_STANDARDS.md](docs/CONFIG_STANDARDS.md)（CFG-01～CFG-12）
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
- **硬约束专文路线图**：[docs/standards-roadmap.md](docs/standards-roadmap.md)（P0～P4 已完成；分支保护待管理员勾选）
- pkg 路线图：[docs/pkg-roadmap.md](docs/pkg-roadmap.md)

## 测试与 CI

```bash
make test
make test-ci              # -race + coverage + TEST 门禁
make check-deps           # go.sum / replace / 许可证
make check-no-static-config # CFG-01 防回退（禁止新增 configs 文件）
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
make ci                   # 本地对齐托管平台必跑项
```

## 说明

- 数据库只支持 **MySQL 8.0**：SQL 仓储与迁移脚本均为 MySQL 方言（`DATETIME(3)`、`AUTO_INCREMENT`、`InnoDB`），`db.driver` 仅允许 `memory` / `mysql`；`postgres` 已被启动校验拒绝。
- 跨进程联调必须使用共享 MySQL；`memory` 仅用于单进程测试与本地快速验证。
- `cmd/migrate` 仍保留多方言执行能力，但仓库内迁移脚本只提供 MySQL 版本，生产禁止使用其它方言。
- 通知渠道在 `notification.channels` 中显式配置（SMTP / 阿里云 / APNs / FCM / disabled）；prod/staging 禁止 memory，见 [docs/notification-channels.md](docs/notification-channels.md)。
