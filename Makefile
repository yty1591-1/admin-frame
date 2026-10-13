MODULE := github.com/admin-frame/admin-frame
BIN_DIR := bin
GO ?= go
LDFLAGS := -s -w

.PHONY: build build-admin build-biz build-c build-worker build-migrate \
	run-admin run-biz run-c run-worker \
	test test-ci lint fmt fmt-check tidy tidy-check \
	check-transport ci mock migrate-up vuln docker docker-admin

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
	goimports -local $(MODULE) -w .

# 与 CI job fmt 对齐：仅检查、不写回。
fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	@out="$$(goimports -local $(MODULE) -l .)"; if [ -n "$$out" ]; then echo "goimports needed:"; echo "$$out"; exit 1; fi

tidy:
	$(GO) mod tidy

tidy-check:
	$(GO) mod tidy
	git diff --exit-code go.mod go.sum

check-transport:
	bash scripts/ci/check-transport-imports.sh

# 本地一键对齐 CI 必跑项（CI_STANDARDS.md）。
ci: fmt-check tidy-check lint test-ci vuln check-transport build

migrate-up: build-migrate
	./$(BIN_DIR)/migrate -dir migrations -action up

vuln:
	govulncheck ./...

docker:
	docker build --target admin-api -t admin-frame/admin-api:local .
	docker build --target biz-api -t admin-frame/biz-api:local .
	docker build --target c-api -t admin-frame/c-api:local .
	docker build --target worker -t admin-frame/worker:local .

docker-admin:
	docker build --target admin-api -t admin-frame/admin-api:local .
