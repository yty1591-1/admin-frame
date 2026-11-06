MODULE := github.com/admin-frame/admin-frame
BIN_DIR := bin
GO ?= go
LDFLAGS := -s -w

GOIMPORTS ?= $(GO) run golang.org/x/tools/cmd/goimports@v0.30.0
GOVULNCHECK ?= $(GO) run golang.org/x/vuln/cmd/govulncheck@v1.1.4
PYTHON ?= python3

.PHONY: build build-admin build-biz build-c build-worker build-migrate \
	run-admin run-biz run-c run-worker \
	test test-ci test-integration check-test-gates lint fmt fmt-check tidy tidy-check \
	check-transport check-debug check-openapi openapi-docs openapi-client openapi-bundle openapi-changelog openapi-artifacts check-deps check-config \
	check-migrations check-reliability check-observability \
	check-git check-release check-sbom check-runbooks check-oncall check-audit \
        check-license check-codegen check-perf check-idempotency check-outbox secrets \
        check-authn check-deploy check-alerts check-backup check-doclinks check-secret-provider gitee-job ci ci-deps mock migrate-up vuln docker docker-admin docker-push ops-image supply-chain-rehearsal

build: build-admin build-biz build-c build-worker build-migrate

build-admin:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/admin-api ./cmd/admin-api

build-biz:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/biz-api ./cmd/biz-api

build-c:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/c-api ./cmd/c-api

build-worker:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/worker ./cmd/worker

build-migrate:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/migrate ./cmd/migrate

run-admin: build-admin
	./$(BIN_DIR)/admin-api -config configs/admin.yaml

run-biz: build-biz
	./$(BIN_DIR)/biz-api -config configs/biz.yaml

run-c: build-c
	./$(BIN_DIR)/c-api -config configs/c.yaml

run-worker: build-worker
	./$(BIN_DIR)/worker -config configs/worker.yaml

test:
	$(GO) test ./...

# TEST-01～05：domain 覆盖率、必测隔离/越权、CI 禁生产 secrets。
check-test-gates:
	bash scripts/ci/check-test-gates.sh

# 与 CI job test 对齐（race + coverage + TESTING_STANDARDS 门禁）。
test-ci:
	bash scripts/ci/run-tests.sh

# ENT-16：真实 MySQL / Redis / MinIO 集成测试（Tier B，不并入 make ci）。
test-integration:
	bash scripts/ci/run-integration-tests.sh

lint:
	golangci-lint run ./...

# 仅扫描 Go 源码目录：仓库根下有 frontend/node_modules（数万文件），
# 在 WSL 的 /mnt/d（9p）上整树遍历会让 fmt 门禁耗时常达分钟级。
GO_DIRS := ./cmd ./internal ./pkg ./scripts ./tests

fmt:
	gofmt -w $(GO_DIRS)
	$(GOIMPORTS) -local $(MODULE) -w $(GO_DIRS)

# 与 CI job fmt 对齐：仅检查、不写回（不依赖全局安装 goimports）。
fmt-check:
	@out="$$(gofmt -l $(GO_DIRS))"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	@out="$$($(GOIMPORTS) -local $(MODULE) -l $(GO_DIRS))"; if [ -n "$$out" ]; then echo "goimports needed:"; echo "$$out"; exit 1; fi

tidy:
	$(GO) mod tidy

# 检查 go mod tidy 是否幂等（不依赖「是否已 git commit」）。
# CI 上仍用 git diff（见 .github/workflows），要求 PR 已包含 tidy 结果。
tidy-check:
	@tmp=$$(mktemp -d); \
	cp go.mod "$$tmp/"; \
	if [ -f go.sum ]; then cp go.sum "$$tmp/"; fi; \
	$(GO) mod tidy; \
	if ! cmp -s "$$tmp/go.mod" go.mod || { [ -f go.sum ] && ! cmp -s "$$tmp/go.sum" go.sum; }; then \
		echo "error: go mod tidy changed go.mod/go.sum; commit the result and re-run"; \
		diff -u "$$tmp/go.mod" go.mod || true; \
		if [ -f go.sum ] || [ -f "$$tmp/go.sum" ]; then diff -u "$$tmp/go.sum" go.sum || true; fi; \
		rm -rf "$$tmp"; \
		exit 1; \
	fi; \
	rm -rf "$$tmp"; \
	echo "OK: go.mod tidy is idempotent"

check-transport:
	bash scripts/ci/check-transport-imports.sh

# SEC-08：禁止未标注的 pprof / Swagger。
# CI-09 / PLAT-12：分层依赖门禁（transport 不得依赖 platform 等）。
check-layering:
	bash scripts/ci/check-layering.sh

check-debug:
	bash scripts/ci/check-debug-endpoints.sh

# SEC-01：gitleaks（版本见 scripts/ci/gitleaks.sh）。
secrets:
	bash scripts/ci/gitleaks.sh

# API-01/07/08：分端 OpenAPI validate + breaking（见 scripts/ci/check-openapi.sh）。
check-openapi openapi:
	bash scripts/ci/check-openapi.sh

# 契约分发产物（dev/CI 专用，输出到 dist/，不入库、不进镜像；SEC-08）。
openapi-docs:
	bash scripts/openapi/build-docs.sh

openapi-client:
	bash scripts/openapi/gen-client.sh

# 多文件契约装配为单文件，供消费方离线锁定（dist/openapi/bundled）。
openapi-bundle:
	bash scripts/openapi/bundle.sh

# BASE_REF 可选；未指定时由脚本按 origin/main 等顺序解析。
openapi-changelog:
	bash scripts/openapi/changelog.sh $(BASE_REF)

# 产物附带当前 commit，供消费方锁定契约版本（API_STANDARDS §3.2）。
openapi-artifacts: openapi-bundle openapi-docs openapi-client openapi-changelog
	@mkdir -p dist/openapi
	@git rev-parse HEAD >dist/openapi/contract-commit.txt 2>/dev/null || echo unknown >dist/openapi/contract-commit.txt

# DEP-01/03/05：go.sum、replace、许可证（govulncheck 见 vuln）。
check-deps:
	bash scripts/ci/check-deps.sh

# CFG-01/02/06：配置样例与入库策略。
check-config:
	bash scripts/ci/check-config.sh

# DATA-01/02/03：迁移命名、up/down 配对、禁 API 自动 migrate。
check-migrations:
	bash scripts/ci/check-migrations.sh

# REL-01/02/05：无超时 HTTP 客户端禁令、限流码、优雅关闭。
check-reliability:
	bash scripts/ci/check-reliability.sh

# OBS-01/02/04：日志字段、healthz/readyz/version。
check-observability:
	bash scripts/ci/check-observability.sh

# GIT-05/06：PR 模板与 commitlint 配置。
check-git:
	bash scripts/ci/check-git.sh

# RELS-01/02/03/07：Dockerfile、版本注入、发布 Checklist。
check-release:
	bash scripts/ci/check-release.sh

# RELS-13/14：SBOM / 镜像签名 / 部署前验签的流水线结构（静态，无网络）。
check-sbom:
	bash scripts/ci/check-sbom.sh

# 手动：本地 registry + cosign 密钥对做签名/验签等价演练（需 docker，不进 make ci）。
supply-chain-rehearsal:
	bash scripts/ci/supply-chain-rehearsal.sh

# RB-01～04：基线 runbook 与告警目录。
check-runbooks:
	bash scripts/ci/check-runbooks.sh

# ENT-18（RB-08/09）：分级矩阵、告警路由、复盘模板、行动项、演练、交接。
check-oncall:
	bash scripts/ci/check-oncall.sh

# 安装 CI 门禁所需的本地 Python 依赖（目前仅 PyYAML，供 OC-03 解析告警路由）。
# 优先创建仓库内 .venv，避免污染受 PEP 668 管理的系统 Python；无 uv 时退回 venv + pip。
ci-deps:
	bash scripts/ci/ci-deps.sh

# AUD-01/02/04：事件目录、字段字典、audit 包抽检。
check-audit:
	bash scripts/ci/check-audit.sh

# LIC-01/02/06：许可证允许/禁止列表与 NOTICE。
check-license:
	bash scripts/ci/check-license.sh

# GEN-01/02：codegen MANIFEST 与生成头。
check-codegen:
	bash scripts/ci/check-codegen.sh

# ENT-02：幂等平台挂载、ID 配置契约、禁止固定 NodeID。
check-idempotency:
	bash scripts/ci/check-idempotency.sh

# ENT-03：异步信封 / Outbox / Relay / 指标 / 迁移 / 配置契约。
check-outbox:
	bash scripts/ci/check-outbox.sh

# ENT-09 T-17：租户域门禁——审计动作 ↔ EVENTS.md、路由 ↔ OpenAPI 双向对账、
# 状态机迁移矩阵快照。
check-tenant:
	bash scripts/ci/check-tenant.sh

# ENT-12：数据范围契约门禁——authz 原语、越权用例、仓储 fail closed、
# 业务接线四处对账（rbac-iam.md §4.4）。
check-authz:
	bash scripts/ci/check-authz.sh

# ENT-16：三端 E2E（真实二进制 + MySQL 8 + Redis；nightly / 本地手动）。
# 不进 make ci——单轮约 15s 且依赖真实容器，PR 必跑门禁保持轻量。
check-e2e:
	@test -n "$$E2E_MYSQL_DSN" || (echo "E2E_MYSQL_DSN is required (mysql8, parseTime=true)" >&2; exit 1)
	@test -n "$$E2E_REDIS_ADDR" || (echo "E2E_REDIS_ADDR is required (redis 7)" >&2; exit 1)
	go test ./tests/e2e/ -count=1 -v

# ENT-16：全链路压测基线（真实二进制 + MySQL + Redis；手动 / 发布前对比）。
# 默认 50 rps × 30s；参数见 docs/perf/LOAD_BASELINES.md §5。不进 make ci（PERF-06）。
load-baseline:
	@test -n "$$E2E_MYSQL_DSN" || (echo "E2E_MYSQL_DSN is required (mysql8, parseTime=true)" >&2; exit 1)
	@test -n "$$E2E_REDIS_ADDR" || (echo "E2E_REDIS_ADDR is required (redis 7)" >&2; exit 1)
	E2E_LOAD_BASELINE=1 go test ./tests/e2e/ -run TestLoadBaselineAdminRead -count=1 -v

# ENT-16：压测基线登记一致性（不重跑压测）——行/执行器/产物对账、
# 孤儿产物、压测不得进默认门禁、k6 阈值与 identity-slo 一致。
check-load-baseline:
	bash scripts/ci/check-load-baseline.sh

# ENT-11：特性开关门禁——key 命名 / 审计对账 / 幽灵 key 扫描 / 到期治理
# （feature-flag-plan.md §2.G）。
check-featureflag:
	bash scripts/ci/check-featureflag.sh

# ENT-17：发布迁移门禁——expand-only / append-only / 无未完成迁移（静态检查；
# 设 MIGRATE_DSN 后追加 schema 探测）。
check-migration-gate:
	bash scripts/ci/check-migration-gate.sh

# ENT-13：依赖韧性门禁——注册表接线 / 回调模板三件套 / 裸出站 HTTP 扫描
# （RELIABILITY_STANDARDS.md §4.2 / §5.1）。
check-resilience:
	bash scripts/ci/check-resilience.sh

# ENT-10 NT-08：通知域门禁——审计动作 ↔ EVENTS.md 对账、状态机矩阵快照、
# 渠道包 internal 引用面扫描（ADR-0008 铁律）。
check-notification:
	bash scripts/ci/check-notification.sh

# PERF-01/02：关键路径 Benchmark 登记与抽检。
check-perf:
	bash scripts/ci/check-perf.sh

# ENT-01：身份认证生产配置、三端契约、撤销路径、迁移、审计与密钥。
check-authn:
	bash scripts/ci/check-authn.sh

# ENT-05：Helm Chart 结构、渲染、明文密钥与 digest（helm 缺失时仅静态检查）。
check-deploy:
	bash scripts/ci/check-deploy.sh

# ENT-06：ALERTS.md ↔ 规则 ↔ runbook ↔ 看板一致性（promtool 可选）。
check-alerts:
	bash scripts/ci/check-alerts.sh

# ENT-08：备份脚本、CronJob、RPO/RTO 与演练 runbook。
check-backup:
	bash scripts/ci/check-backup.sh

# DOC-01：docs/ 与 README.md 相对链接有效性；移动/归档文档后执行。
check-doclinks:
	bash scripts/ci/check-doclinks.sh

# ENT-07 T4：密钥托管门禁（生产禁止 env-only、Provider 必须接入启动路径）。
check-secret-provider:
	bash scripts/ci/check-secret-provider.sh

# mockery（GEN-04）；需已安装 mockery，配置见 .mockery.yaml。
MOCKERY ?= mockery
mock:
	@command -v $(MOCKERY) >/dev/null 2>&1 || { echo "install mockery or set MOCKERY=..."; exit 1; }
	$(MOCKERY)

# 与 Gitee Go step 相同入口。示例: make gitee-job JOB=arch
gitee-job:
	@if [ -z "$(JOB)" ]; then echo "usage: make gitee-job JOB=fmt|mod|lint|test|vuln|arch|secrets|openapi|build"; exit 2; fi
	bash scripts/ci/gitee-job.sh $(JOB)

# 本地一键对齐 CI 必跑项（含 LIC/GEN/PERF 等门禁）。
ci: fmt-check tidy-check lint test-ci vuln check-deps check-transport check-layering check-debug secrets check-config check-migrations check-reliability check-observability check-git check-release check-sbom check-runbooks check-oncall check-audit check-license check-codegen check-perf check-openapi check-idempotency check-outbox check-authn check-alerts check-backup check-secret-provider check-tenant check-authz check-featureflag check-resilience check-notification check-load-baseline check-migration-gate build

migrate-up: build-migrate
	./$(BIN_DIR)/migrate -dir migrations -action up

# DEP-04：与 CI 对齐；受控例外见 deps/vuln-allowlist.txt。
vuln:
	@GOVULNCHECK='$(GOVULNCHECK)' bash scripts/ci/check-vuln.sh

# 本地镜像 tag:local（非生产）。版本可通过 VERSION/COMMIT/BUILD_TIME 覆盖。
VERSION ?= local
COMMIT ?= none
BUILD_TIME ?= unknown

docker:
	docker build --target admin-api --build-arg CMD=admin-api --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_TIME=$(BUILD_TIME) -t admin-frame/admin-api:local .
	docker build --target biz-api --build-arg CMD=biz-api --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_TIME=$(BUILD_TIME) -t admin-frame/biz-api:local .
	docker build --target c-api --build-arg CMD=c-api --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_TIME=$(BUILD_TIME) -t admin-frame/c-api:local .
	docker build --target worker --build-arg CMD=worker --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_TIME=$(BUILD_TIME) -t admin-frame/worker:local .
	docker build --target migrate --build-arg CMD=migrate --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_TIME=$(BUILD_TIME) -t admin-frame/migrate:local .

docker-admin:
	docker build --target admin-api --build-arg CMD=admin-api --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_TIME=$(BUILD_TIME) -t admin-frame/admin-api:local .

# 数据库运维镜像（ENT-08）：全量备份 / binlog 归档 / PITR 专用，含 mysqlbinlog。
# 与应用镜像分离：应用运行时不应携带数据库客户端与对象存储凭据工具。
# 上下文必须是仓库根（脚本随镜像分发）。
OPS_IMAGE ?= admin-frame-db-ops:local
ops-image:
	docker build -f deploy/backup/Dockerfile -t $(OPS_IMAGE) .

# 本地复现 image 流水线（只推 single-arch；多架构与扫描见 CI job image）。
# RELS-01/§9：:local 标签禁止推送，必须显式给 VERSION=<semver|sha>。
IMAGE_REGISTRY ?= ghcr.io
IMAGE_NAMESPACE ?= admin-frame

docker-push:
	@if [ "$(VERSION)" = "local" ]; then \
		echo "error: set VERSION=<semver|sha>; the local tag must not be pushed (RELS-01/§9)" >&2; \
		exit 1; \
	fi
	@set -e; for cmd in admin-api biz-api c-api worker migrate; do \
		img="$(IMAGE_REGISTRY)/$(IMAGE_NAMESPACE)/$$cmd:$(VERSION)"; \
		echo "==> push $$img"; \
		docker build --target $$cmd \
			--build-arg CMD=$$cmd \
			--build-arg VERSION=$(VERSION) \
			--build-arg COMMIT=$(COMMIT) \
			--build-arg BUILD_TIME=$(BUILD_TIME) \
			-t $$img . ; \
		docker push $$img ; \
	done
