MODULE := github.com/admin-frame/admin-frame
BIN_DIR := bin
GO ?= go
LDFLAGS := -s -w

GOIMPORTS ?= $(GO) run golang.org/x/tools/cmd/goimports@v0.30.0
GOVULNCHECK ?= $(GO) run golang.org/x/vuln/cmd/govulncheck@v1.1.4

.PHONY: build build-admin build-biz build-c build-worker build-migrate \
	run-admin run-biz run-c run-worker \
	test test-ci lint fmt fmt-check tidy tidy-check \
	check-transport check-debug secrets gitee-job ci mock migrate-up vuln docker docker-admin

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

# 与 CI job test 对齐（race + coverage）。
test-ci:
	$(GO) test -race -count=1 -coverprofile=coverage.out ./...

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
check-debug:
	bash scripts/ci/check-debug-endpoints.sh

# SEC-01：gitleaks（版本见 scripts/ci/gitleaks.sh）。
secrets:
	bash scripts/ci/gitleaks.sh

# 与 Gitee Go step 相同入口。示例: make gitee-job JOB=arch
gitee-job:
	@if [ -z "$(JOB)" ]; then echo "usage: make gitee-job JOB=fmt|mod|lint|test|vuln|arch|secrets|build"; exit 2; fi
	bash scripts/ci/gitee-job.sh $(JOB)

# 本地一键对齐 CI 必跑项（含 SECURITY_STANDARDS 的 secrets / gosec / debug）。
ci: fmt-check tidy-check lint test-ci vuln check-transport check-debug secrets build

migrate-up: build-migrate
	./$(BIN_DIR)/migrate -dir migrations -action up

# 与 CI 对齐；不依赖全局安装 govulncheck。
vuln:
	$(GOVULNCHECK) ./...

docker:
	docker build --target admin-api -t admin-frame/admin-api:local .
	docker build --target biz-api -t admin-frame/biz-api:local .
	docker build --target c-api -t admin-frame/c-api:local .
	docker build --target worker -t admin-frame/worker:local .

docker-admin:
	docker build --target admin-api -t admin-frame/admin-api:local .
