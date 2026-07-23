BINARY    := gateway
BUILD_DIR := bin
CMD_DIR   := ./cmd/gateway
GOFLAGS   := -trimpath -ldflags="-s -w"

.PHONY: build build-all run dev test lint docs-check tidy tools proto proto-lint proto-breaking docker-up docker-down smoke-container migrate-up migrate-up-all migrate-down grant-app-role verify-full chaos-debug observability-secret observability-up observability-down certs backup-secret backup-role-bootstrap backup-checksums-enable backup-stanza-init backup-full backup-diff backup-check backup-status backup-expire

BUF_VERSION                := v1.72.0
PROTOC_GEN_GO_VERSION      := v1.36.11
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2

## build: Compile the binary
build:
	mkdir -p $(BUILD_DIR)
	go build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY) $(CMD_DIR)

## build-all: Compile all eight deployable service binaries
build-all:
	mkdir -p $(BUILD_DIR)
	go build $(GOFLAGS) -o $(BUILD_DIR)/gateway ./cmd/gateway
	go build $(GOFLAGS) -o $(BUILD_DIR)/auth-service ./cmd/auth-service
	go build $(GOFLAGS) -o $(BUILD_DIR)/ledger-service ./cmd/ledger-service
	go build $(GOFLAGS) -o $(BUILD_DIR)/payin-service ./cmd/payin-service
	go build $(GOFLAGS) -o $(BUILD_DIR)/payout-service ./cmd/payout-service
	go build $(GOFLAGS) -o $(BUILD_DIR)/fraud-service ./cmd/fraud-service
	go build $(GOFLAGS) -o $(BUILD_DIR)/admin-bff-service ./cmd/admin-bff-service
	go build $(GOFLAGS) -o $(BUILD_DIR)/assurance-service ./cmd/assurance-service

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

## docs-check: Validate required guides, local Markdown links, and heading anchors
docs-check:
	go run ./cmd/doccheck

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

## smoke-container: Full-container round-trip (docs/roadmap/archive/44 K4) — real Docker images via `docker compose --profile app`, not host binaries
smoke-container:
	./scripts/smoke-container.sh

# This is what docs/development/project-guide.md's "Build and verification" section means by "the
# full gate" — run this instead of chaining the steps by hand so a volume
# reset is never skipped by mistake. Any ad-hoc debugging against the
# shared dev stack (manual curl against a running service, a one-off
# scenario re-run) leaves state behind that smoke-test.sh's fixed-UUID
# fixtures will misreport as a regression — this target always starts from
# zero so its PASS/FAIL is trustworthy.
## verify-full: Full doc-completion gate from a CLEAN volume (build/vet/lint/test + smoke + business/admin e2e + chaos-all)
verify-full:
	go build ./...
	go vet ./...
	go vet -tags=integration ./...
	$(MAKE) lint
	$(MAKE) docs-check
	$(MAKE) test
	docker compose down -v
	./scripts/smoke-test.sh
	./scripts/business-e2e.sh
	./scripts/admin-e2e.sh
	./scripts/chaos-test.sh all

# Preserves /tmp/seev-chaos.*/*.log past the exit trap instead of deleting
# them, so a failing scenario can be inspected after the fact. Usage:
#   make chaos-debug SCENARIO=8
## chaos-debug: Re-run one chaos scenario (SCENARIO=1..14, default all) with logs preserved after exit
SCENARIO ?= all
chaos-debug:
	KEEP_WORK_DIR=1 ./scripts/chaos-test.sh $(SCENARIO)

# Migrations run as the schema OWNER (POSTGRES_MIGRATE_USER), never as the
# app's restricted POSTGRES_USER (docs/roadmap/archive/16 Task T3) — DDL and DML
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

## grant-app-role: Grant the app_service DB role to POSTGRES_USER (run once per environment, after the first migrate-up creates app_service — docs/roadmap/archive/16 Task T3)
grant-app-role:
	psql "$(SERVICE_OWNER_DSN)" -c "GRANT app_service TO $(POSTGRES_USER);"

# docs/roadmap/archive/43 K1: a strong Grafana admin password generated locally, mode
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

## observability-up: Start app + observability profiles (Prometheus/Grafana/Loki/Tempo/Alloy) — do NOT run alongside the testcontainers integration suite (docs/development/project-guide.md RAM budget)
observability-up: observability-secret
	OTEL_EXPORTER_OTLP_ENDPOINT=tempo:4317 docker compose --profile app --profile observability up --build -d

## observability-down: Stop app + observability profiles
observability-down:
	docker compose --profile app --profile observability down

# docs/roadmap/archive/49 K3: mTLS CA + one leaf cert per service identity, generated
# locally into ./deploy/certs (gitignored, mirrors the observability-secret
# pattern above). `docker compose --profile app` paths that don't go through
# scripts/lib.sh (manual dev, smoke-container.sh, nightly.yml) mount this
# directory read-only, so it must exist before those containers start.
# certgen itself is idempotent (init-ca skips an existing CA, issue skips a
# leaf that's still fresh), so re-running this is always safe.
## certs: Generate the local mTLS CA + per-service leaf certs (run before `docker compose --profile app up`)
certs:
	mkdir -p $(BUILD_DIR)
	go build $(GOFLAGS) -o $(BUILD_DIR)/certgen ./cmd/certgen
	$(BUILD_DIR)/certgen init-ca --out deploy/certs
	@for service in gateway auth ledger payin payout fraud admin-bff assurance dev-operator prometheus backup-agent; do \
		$(BUILD_DIR)/certgen issue --service $$service --out deploy/certs || exit $$?; \
	done

# docs/roadmap/active/50 K3/K5: two independent secrets — the pgBackRest repository
# encryption passphrase and the seev_backup role's own password — generated
# locally, mode 0600, gitignored, mirrors the observability-secret pattern.
# Idempotent: does nothing to a secret that already exists.
## backup-secret: Generate the pgBackRest repository passphrase and seev_backup role password (run once per machine)
backup-secret:
	@mkdir -p deploy/backup/secrets deploy/backup/repo
	@if [ ! -f deploy/backup/secrets/pgbackrest_repo_passphrase ]; then \
		openssl rand -base64 32 > deploy/backup/secrets/pgbackrest_repo_passphrase; \
		chmod 600 deploy/backup/secrets/pgbackrest_repo_passphrase; \
		echo "generated deploy/backup/secrets/pgbackrest_repo_passphrase"; \
	else \
		echo "deploy/backup/secrets/pgbackrest_repo_passphrase already exists, leaving it alone"; \
	fi
	@if [ ! -f deploy/backup/secrets/seev_backup_password ]; then \
		openssl rand -base64 24 > deploy/backup/secrets/seev_backup_password; \
		chmod 600 deploy/backup/secrets/seev_backup_password; \
		echo "generated deploy/backup/secrets/seev_backup_password"; \
	else \
		echo "deploy/backup/secrets/seev_backup_password already exists, leaving it alone"; \
	fi

# docs/roadmap/active/50 K5: 04-backup-role.sh only runs automatically via
# /docker-entrypoint-initdb.d on a FRESH volume's first boot. An existing
# volume (like this repo's own dev seev_postgres_data, provisioned before
# Track A7 existed) never re-runs first-boot scripts, so this target
# re-invokes the EXACT SAME script inside the running container — never a
# hand-copied variant that could drift from the first-boot behavior.
## backup-role-bootstrap: Create/refresh the seev_backup role on an ALREADY-INITIALIZED volume (run once per environment after `make backup-secret`)
backup-role-bootstrap:
	docker compose exec postgres sh /docker-entrypoint-initdb.d/04-backup-role.sh

# docs/roadmap/active/50 K2: --data-checksums (POSTGRES_INITDB_ARGS) only takes effect
# on a fresh initdb. An existing volume needs Postgres fully STOPPED and
# pg_checksums run offline directly against the data directory — this
# target does exactly that, then restarts and verifies. Never run this
# against a volume with the server still accepting connections; pg_checksums
# refuses an active data directory by design, so a lock-file check backs
# that up rather than relying on the refusal alone.
## backup-checksums-enable: Enable data page checksums on the EXISTING seev_postgres_data volume (postgres must be stopped first)
backup-checksums-enable:
	docker compose stop postgres
	docker compose run --rm --no-deps -v seev_postgres_data:/var/lib/postgresql/data postgres \
		sh -c 'pg_checksums --enable --pgdata=/var/lib/postgresql/data && pg_checksums --check --pgdata=/var/lib/postgresql/data'
	docker compose up -d postgres

# docs/roadmap/active/50 K3: `docker compose exec` starts a fresh process attached to
# the container, NOT a child of the entrypoint's own shell — it does not
# inherit the PGBACKREST_REPO1_CIPHER_PASS the entrypoint exported for
# archive_command's benefit (that export only reaches the postgres server
# process's own children). Every manual pgbackrest invocation below reads
# the passphrase from the host-side secret file (the Makefile always runs
# on the host) and passes it via `exec -e` instead — never printed, never
# passed as a CLI argument (which would leak into `ps`/process listings).
PGBACKREST_ENV = -e PGBACKREST_REPO1_CIPHER_PASS="$$(cat deploy/backup/secrets/pgbackrest_repo_passphrase)"

## backup-stanza-init: Create the pgBackRest stanza (run once, after backup-secret and backup-role-bootstrap)
backup-stanza-init:
	docker compose exec $(PGBACKREST_ENV) postgres pgbackrest --stanza=seev --config=/etc/pgbackrest/pgbackrest.conf stanza-create

## backup-full: Run a full backup
backup-full:
	docker compose exec $(PGBACKREST_ENV) postgres pgbackrest --stanza=seev --config=/etc/pgbackrest/pgbackrest.conf --type=full backup

## backup-diff: Run a differential backup
backup-diff:
	docker compose exec $(PGBACKREST_ENV) postgres pgbackrest --stanza=seev --config=/etc/pgbackrest/pgbackrest.conf --type=diff backup

## backup-check: Verify the backup repository and WAL archive are consistent
backup-check:
	docker compose exec $(PGBACKREST_ENV) postgres pgbackrest --stanza=seev --config=/etc/pgbackrest/pgbackrest.conf check

## backup-status: Show backup/repository info (oldest/latest restorable point, backup set list)
backup-status:
	docker compose exec $(PGBACKREST_ENV) postgres pgbackrest --stanza=seev --config=/etc/pgbackrest/pgbackrest.conf info --output=json

## backup-expire: Expire backups/WAL outside the retention policy (K4: run only after a successful backup + check)
backup-expire:
	docker compose exec $(PGBACKREST_ENV) postgres pgbackrest --stanza=seev --config=/etc/pgbackrest/pgbackrest.conf expire

## help: Print this help message
help:
	@sed -n 's/^## //p' Makefile | column -t -s ':' | sed -e 's/^/  /'
