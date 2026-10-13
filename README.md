# admin-frame

A/B/C 三端业务的 Go 模块化单体骨架，实现见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)。

| 进程 | 端口（默认） | 说明 |
|---|---|---|
| `admin-api` | `:8081` | A 平台运营 |
| `biz-api` | `:8082` | B 商家/企业 |
| `c-api` | `:8083` | C 消费者 |
| `worker` | — | 领域事件消费（演示轮询） |
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

### C 端下单示例

```bash
curl -s http://127.0.0.1:8083/c/v1/orders \
  -H "Authorization: Bearer c.user-42" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: demo-1" \
  -d "{\"merchant_id\":\"m100\",\"amount_cent\":1990,\"currency\":\"CNY\"}"
```

### 健康检查

```bash
curl -s http://127.0.0.1:8083/healthz
curl -s http://127.0.0.1:8083/readyz
```

## 目录要点

- `internal/order/domain|app|infrastructure`：三端共享
- `internal/order/transport/{admin,biz,c}`：按端隔离 Handler/DTO
- `api/openapi/{admin,biz,c}.yaml`：分端契约
- `migrations/`：可回滚 SQL

## 工程规范

- 架构：[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
- **注释硬约束**：[docs/COMMENT_STANDARDS.md](docs/COMMENT_STANDARDS.md)（C-01～C-10）
- **代码风格硬约束**：[docs/CODE_STYLE.md](docs/CODE_STYLE.md)（S-01～S-12）
- **CI 硬约束**：[docs/CI_STANDARDS.md](docs/CI_STANDARDS.md)（CI-01～CI-14）  
  - GitHub：`.github/workflows/ci.yml`  
  - Gitee：`.workflow/`（见 [.workflow/README.md](.workflow/README.md)）
- **安全硬约束**：[docs/SECURITY_STANDARDS.md](docs/SECURITY_STANDARDS.md)（SEC-01～SEC-14；gitleaks / gosec）
- **API 硬约束**：[docs/API_STANDARDS.md](docs/API_STANDARDS.md)（API-01～API-12；oasdiff）
- **硬约束专文路线图**：[docs/standards-roadmap.md](docs/standards-roadmap.md)（待落地 TEST/DEP 等）
- pkg 路线图：[docs/pkg-roadmap.md](docs/pkg-roadmap.md)

## 测试与 CI

```bash
make test
make lint     # 需已安装 golangci-lint
make openapi  # 分端 OpenAPI validate + breaking
make ci       # 本地对齐托管平台必跑项
```

## 说明

- 默认 `db.driver: memory`，各 API 进程内存独立；跨进程联调请接入共享 MySQL/Postgres 并实现 SQL 仓储。
- `cmd/migrate` 目前校验并打印迁移文件，接入真实 DB 后执行 SQL。
