BINARY    := gateway
BUILD_DIR := bin
CMD_DIR   := ./cmd/gateway
GOFLAGS   := -trimpath -ldflags="-s -w"

.PHONY: build build-all run dev test lint tidy tools proto proto-lint proto-breaking docker-up docker-down smoke-container migrate-up migrate-up-all migrate-down grant-app-role verify-full chaos-debug observability-secret observability-up observability-down

BUF_VERSION                := v1.47.2
PROTOC_GEN_GO_VERSION      := v1.36.11
PROTOC_GEN_GO_GRPC_VERSION := v1.5.1

## build: Compile the binary
build:
	mkdir -p $(BUILD_DIR)
	go build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY) $(CMD_DIR)

## build-all: Compile all seven deployable service binaries
build-all:
	mkdir -p $(BUILD_DIR)
	go build $(GOFLAGS) -o $(BUILD_DIR)/gateway ./cmd/gateway
	go build $(GOFLAGS) -o $(BUILD_DIR)/auth-service ./cmd/auth-service
	go build $(GOFLAGS) -o $(BUILD_DIR)/ledger-service ./cmd/ledger-service
	go build $(GOFLAGS) -o $(BUILD_DIR)/payin-service ./cmd/payin-service
	go build $(GOFLAGS) -o $(BUILD_DIR)/payout-service ./cmd/payout-service
	go build $(GOFLAGS) -o $(BUILD_DIR)/fraud-service ./cmd/fraud-service
	go build $(GOFLAGS) -o $(BUILD_DIR)/admin-bff-service ./cmd/admin-bff-service

## run: Run the compiled binary
run: build
	./$(BUILD_DIR)/$(BINARY)

## dev: Run with live reload (requires air: go install github.com/cosmtrek/air@latest)
dev:
	air

## test: Run all tests with race detector
test:
	go test -race -cover ./...

## test/cover: Run tests and open HTML coverage report
test/cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

## lint: Run golangci-lint (requires golangci-lint installed)
lint:
	golangci-lint run ./...

## tidy: Tidy go.mod and go.sum
tidy:
	go mod tidy
	go mod verify

## tools: Install pinned protobuf toolchain versions
tools:
	go install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)

## proto: Generate committed Go protobuf bindings
proto:
	buf generate

## proto-lint: Lint protobuf contracts
proto-lint:
	buf lint

## proto-breaking: Check protobuf compatibility against main
proto-breaking:
	buf breaking --against '.git#branch=main'

## vet: Run go vet
vet:
	go vet ./...

## docker-up: Start infrastructure (postgres, redis, rabbitmq)
docker-up:
	docker compose up -d

## docker-down: Stop infrastructure
docker-down:
	docker compose down

## smoke-container: Full-container round-trip (docs/plan/44 K4) — real Docker images via `docker compose --profile app`, not host binaries
smoke-container:
	./scripts/smoke-container.sh

# This is what PROJECT_GUIDE.md's "Build and verification" section means by "the
# full gate" — run this instead of chaining the steps by hand so a volume
# reset is never skipped by mistake. Any ad-hoc debugging against the
# shared dev stack (manual curl against a running service, a one-off
# scenario re-run) leaves state behind that smoke-test.sh's fixed-UUID
# fixtures will misreport as a regression — this target always starts from
# zero so its PASS/FAIL is trustworthy.
## verify-full: Full doc-completion gate from a CLEAN volume (build/vet/lint/test + smoke + business-e2e + chaos-all)
verify-full:
	go build ./...
	go vet ./...
	go vet -tags=integration ./...
	$(MAKE) lint
	$(MAKE) test
	docker compose down -v
	./scripts/smoke-test.sh
	./scripts/business-e2e.sh
	./scripts/chaos-test.sh all

# Preserves /tmp/seev-chaos.*/*.log past the exit trap instead of deleting
# them, so a failing scenario can be inspected after the fact. Usage:
#   make chaos-debug SCENARIO=8
## chaos-debug: Re-run one chaos scenario (SCENARIO=1..11, default all) with logs preserved after exit
SCENARIO ?= all
chaos-debug:
	KEEP_WORK_DIR=1 ./scripts/chaos-test.sh $(SCENARIO)

# Migrations run as the schema OWNER (POSTGRES_MIGRATE_USER), never as the
# app's restricted POSTGRES_USER (docs/plan/16 Task T3) — DDL and DML
# identities stay separate on purpose.
# Port default (5433) matches docker-compose.yml's own default — see its
# comment on the postgres service's `ports:` mapping.
SERVICE ?= ledger
SERVICE_DATABASE = seev_$(SERVICE)
POSTGRES_MIGRATE_BASE := postgres://$(or $(POSTGRES_MIGRATE_USER),seev):$(or $(POSTGRES_MIGRATE_PASSWORD),seev)@$(or $(POSTGRES_HOST),localhost):$(or $(POSTGRES_PORT),5433)
SERVICE_OWNER_DSN = $(POSTGRES_MIGRATE_BASE)/$(SERVICE_DATABASE)?sslmode=$(or $(POSTGRES_SSL_MODE),disable)
SERVICE_MIGRATE_DSN = $(SERVICE_OWNER_DSN)&x-migrations-table=schema_migrations_$(SERVICE)

## migrate-up: Run one service's pending migrations (default SERVICE=ledger)
migrate-up:
	migrate -path migrations/$(SERVICE) -database "$(SERVICE_MIGRATE_DSN)" up

## migrate-up-all: Run every service migration folder against the current database
migrate-up-all:
	$(MAKE) migrate-up SERVICE=ledger
	@for path in migrations/*; do \
		[ -d "$$path" ] || continue; \
		service=$${path##*/}; \
		[ "$$service" = ledger ] && continue; \
		$(MAKE) migrate-up SERVICE="$$service" || exit $$?; \
	done

## migrate-down: Roll back the selected service's last migration
migrate-down:
	migrate -path migrations/$(SERVICE) -database "$(SERVICE_MIGRATE_DSN)" down 1

## grant-app-role: Grant the app_service DB role to POSTGRES_USER (run once per environment, after the first migrate-up creates app_service — docs/plan/16 Task T3)
grant-app-role:
	psql "$(SERVICE_OWNER_DSN)" -c "GRANT app_service TO $(POSTGRES_USER);"

# docs/plan/43 K1: a strong Grafana admin password generated locally, mode
# 0600, gitignored — never a default/committed credential. Idempotent: does
# nothing if the secret already exists, so re-running is safe.
## observability-secret: Generate the local Grafana admin password (run once per machine)
observability-secret:
	@mkdir -p deploy/observability/secrets
	@if [ ! -f deploy/observability/secrets/grafana_admin_password ]; then \
		openssl rand -base64 24 > deploy/observability/secrets/grafana_admin_password; \
		chmod 600 deploy/observability/secrets/grafana_admin_password; \
		echo "generated deploy/observability/secrets/grafana_admin_password"; \
	else \
		echo "deploy/observability/secrets/grafana_admin_password already exists, leaving it alone"; \
	fi

## observability-up: Start app + observability profiles (Prometheus/Grafana/Loki/Tempo/Alloy) — do NOT run alongside the testcontainers integration suite (PROJECT_GUIDE.md RAM budget)
observability-up: observability-secret
	OTEL_EXPORTER_OTLP_ENDPOINT=tempo:4317 docker compose --profile app --profile observability up --build -d

## observability-down: Stop app + observability profiles
observability-down:
	docker compose --profile app --profile observability down

## help: Print this help message
help:
	@sed -n 's/^## //p' Makefile | column -t -s ':' | sed -e 's/^/  /'
