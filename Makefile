MODULE := github.com/admin-frame/admin-frame
BIN_DIR := bin
GO ?= go
LDFLAGS := -s -w

GOIMPORTS ?= $(GO) run golang.org/x/tools/cmd/goimports@v0.30.0
GOVULNCHECK ?= $(GO) run golang.org/x/vuln/cmd/govulncheck@v1.1.4

.PHONY: build build-admin build-biz build-c build-worker build-migrate \
	run-admin run-biz run-c run-worker \
	test test-ci test-integration check-test-gates lint fmt fmt-check tidy tidy-check \
	check-transport check-debug check-openapi check-deps check-config \
	check-migrations check-reliability check-observability \
	check-git check-release check-runbooks check-audit \
        check-license check-codegen check-perf check-idempotency check-outbox secrets \
        check-authn check-deploy check-alerts check-backup gitee-job ci mock migrate-up vuln docker docker-admin docker-push

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

fmt:
	gofmt -w .
	$(GOIMPORTS) -local $(MODULE) -w .

# 与 CI job fmt 对齐：仅检查、不写回（不依赖全局安装 goimports）。
fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	@out="$$($(GOIMPORTS) -local $(MODULE) -l .)"; if [ -n "$$out" ]; then echo "goimports needed:"; echo "$$out"; exit 1; fi

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

# RB-01～04：基线 runbook 与告警目录。
check-runbooks:
	bash scripts/ci/check-runbooks.sh

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
ci: fmt-check tidy-check lint test-ci vuln check-deps check-transport check-layering check-debug secrets check-config check-migrations check-reliability check-observability check-git check-release check-runbooks check-audit check-license check-codegen check-perf check-openapi check-idempotency check-outbox check-authn check-alerts check-backup build

migrate-up: build-migrate
	./$(BIN_DIR)/migrate -dir migrations -action up

# DEP-04：与 CI 对齐；不依赖全局安装 govulncheck。
vuln:
	$(GOVULNCHECK) ./...

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
