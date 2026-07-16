BINARY    := gateway
BUILD_DIR := bin
CMD_DIR   := ./cmd/gateway
GOFLAGS   := -trimpath -ldflags="-s -w"

.PHONY: build build-all run dev test lint tidy tools proto proto-lint proto-breaking docker-up docker-down migrate-up migrate-up-all migrate-down grant-app-role

BUF_VERSION                := v1.47.2
PROTOC_GEN_GO_VERSION      := v1.36.11
PROTOC_GEN_GO_GRPC_VERSION := v1.5.1

## build: Compile the binary
build:
	mkdir -p $(BUILD_DIR)
	go build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY) $(CMD_DIR)

## build-all: Compile all six deployable service binaries
build-all:
	mkdir -p $(BUILD_DIR)
	go build $(GOFLAGS) -o $(BUILD_DIR)/gateway ./cmd/gateway
	go build $(GOFLAGS) -o $(BUILD_DIR)/auth-service ./cmd/auth-service
	go build $(GOFLAGS) -o $(BUILD_DIR)/ledger-service ./cmd/ledger-service
	go build $(GOFLAGS) -o $(BUILD_DIR)/payin-service ./cmd/payin-service
	go build $(GOFLAGS) -o $(BUILD_DIR)/payout-service ./cmd/payout-service
	go build $(GOFLAGS) -o $(BUILD_DIR)/fraud-service ./cmd/fraud-service

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

## help: Print this help message
help:
	@sed -n 's/^## //p' Makefile | column -t -s ':' | sed -e 's/^/  /'
