BINARY    := gateway
BUILD_DIR := bin
CMD_DIR   := ./cmd/gateway
GOFLAGS   := -trimpath -ldflags="-s -w"

.PHONY: build build-all run dev test lint ci-lint docs-check tidy tools proto proto-lint proto-breaking contract-generate contract-lint contract-breaking contract-test contracts load-lint load-test load-seed load-snapshot load-restore load-smoke load-run load-capacity load-report-check load-clean docker-up docker-down smoke-container migrate-up migrate-up-all migrate-down grant-app-role verify-full verify-chaos chaos-debug observability-secret observability-up observability-down certs backup-secret backup-role-bootstrap backup-checksums-enable backup-stanza-init backup-full backup-diff backup-check backup-status backup-expire cryptox-secret retention-docs retention-check

BUF_VERSION                := v1.72.0
PROTOC_GEN_GO_VERSION      := v1.36.11
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2
PROTO_MERGE_BASE_REF       ?= main

## build: Compile the binary
build:
	mkdir -p $(BUILD_DIR)
	go build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY) $(CMD_DIR)

## build-all: Compile all nine deployable service binaries
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
	go build $(GOFLAGS) -o $(BUILD_DIR)/vendor-service ./cmd/vendor-service

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

## ci-lint: Validate workflow syntax, shell scripts, and action pin policy
ci-lint:
	actionlint -shellcheck "$$(command -v shellcheck)"
	shellcheck --severity=error scripts/*.sh
	./scripts/ci/check-action-pins.sh

## docs-check: Validate required guides, local Markdown links, and heading anchors
docs-check:
	go run ./cmd/doccheck

## retention-docs: Regenerate docs/data/retention.md from config/data-retention.yaml
retention-docs:
	go run ./cmd/retentioncheck -write

## retention-check: Validate config/data-retention.yaml and confirm docs/data/retention.md is current (CI)
retention-check:
	go run ./cmd/retentioncheck

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

## proto-breaking: Check protobuf compatibility against an explicit merge-base ref
proto-breaking:
	buf breaking --against ".git#ref=$(PROTO_MERGE_BASE_REF)"

## contract-generate: Resolve checked-in relative OpenAPI references deterministically
contract-generate:
	go run ./cmd/contractgenerate

## contract-lint: Validate OpenAPI sources, refs, error registry, and inventory
contract-lint:
	go test ./api/contracts

## contract-breaking: Compare generated HTTP bundles with the checked-in bootstrap baseline
contract-breaking:
	go run ./cmd/contractcheck -mode breaking

## contract-test: Run route metadata and contract fixture checks
contract-test:
	go test ./pkg/httpcontract ./api/contracts

## contracts: Run the local A9 contract gate without installing tools
contracts: contract-generate contract-lint contract-breaking contract-test

## load-lint: Validate B0 profiles, safety schemas, and helper/scenario tests without Docker mutation
load-lint:
	go test ./pkg/loadlab ./pkg/loadreport ./pkg/loadmetrics ./cmd/loadcheck ./cmd/loadseed ./cmd/loadreport ./cmd/loadprobe ./tests/load
	go run ./cmd/loadcheck -profile deploy/load/profiles/local-small.yaml

## load-test: Fast B0 helper/analyzer/safety tests; this does not claim capacity
load-test: load-lint
	git diff --check

LOAD_SEED_KIND ?= journey
LOAD_SEED_COUNT ?= 100
LOAD_SEED_OUTPUT ?= artifacts/load/seed/seed.jsonl
## load-seed: Generate synthetic deterministic seed material; requires an explicit disposable acknowledgement
load-seed:
	go run ./cmd/loadseed -kind $(LOAD_SEED_KIND) -count $(LOAD_SEED_COUNT) -out $(LOAD_SEED_OUTPUT) -ack "$(SEEV_LOAD_ACK)"

## load-snapshot/load-restore: compressed state lifecycle inside one disposable project
load-snapshot:
	SEEV_LOAD_ACK=disposable-only ./scripts/load-snapshot.sh snapshot

load-restore:
	SEEV_LOAD_ACK=disposable-only ./scripts/load-snapshot.sh restore

## load-smoke: Start only a disposable load Compose project and run the one-second bootstrap smoke
load-smoke:
	SEEV_LOAD_ACK=disposable-only ./scripts/load-test.sh smoke

## load-run: Start a declared disposable load project; canonical rates remain manual inputs
load-run:
	SEEV_LOAD_ACK=disposable-only ./scripts/load-test.sh run

## load-capacity: Explicitly manual; runs must provide scenario/rate and retain raw artifacts outside Git
load-capacity:
	@test -n "$(LOAD_SCENARIO)" || (echo 'set LOAD_SCENARIO' >&2; exit 1)
	@test -n "$(LOAD_RATE)" || (echo 'set LOAD_RATE in WU/s' >&2; exit 1)
	SEEV_LOAD_ACK=disposable-only SEEV_LOAD_SCENARIO=$(LOAD_SCENARIO) SEEV_LOAD_WORKLOAD=$(LOAD_SCENARIO) SEEV_LOAD_RATE=$(LOAD_RATE) ./scripts/load-test.sh run

LOAD_RUNS ?=
## load-report-check: Validate and aggregate committed-size run summaries without averaging percentiles
load-report-check:
	@test -n "$(LOAD_RUNS)" || (echo 'set LOAD_RUNS=path1.json,path2.json' >&2; exit 1)
	go run ./cmd/loadreport -runs "$(LOAD_RUNS)" -out "$(LOAD_REPORT_OUT)"

## load-clean: Remove only the exact disposable Compose project and its run artifact path
load-clean:
	@test -n "$(SEEV_LOAD_RUN_ID)" || (echo 'set SEEV_LOAD_RUN_ID to the exact run id' >&2; exit 1)
	SEEV_LOAD_ACK=disposable-only ./scripts/load-test.sh clean

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

# This is what docs/development/project-guide.md's "Build and verification"
# section means by "the full gate" — run this instead of chaining the steps by
# hand so a volume reset is never skipped by mistake. Any ad-hoc debugging
# against the shared dev stack can leave state behind that smoke-test.sh's
# fixed-UUID fixtures will misreport as a regression. This target covers every
# repeatable non-chaos gate from a clean environment; chaos is deliberately
# isolated in verify-chaos because it kills dependencies and is an
# operator-controlled recovery drill.
## verify-full: Complete repeatable gate from clean volumes (build/vet/lint/race/integration/contracts/proto/load/docs/container+business/admin)
verify-full:
	docker compose down -v --remove-orphans
	go build ./...
	go vet ./...
	go vet -tags=integration ./...
	go mod verify
	$(MAKE) contracts
	$(MAKE) tools
	$(MAKE) proto
	$(MAKE) proto-lint
	$(MAKE) proto-breaking
	$(MAKE) load-test
	go test -tags=loadtest ./...
	$(MAKE) ci-lint
	$(MAKE) lint
	$(MAKE) docs-check
	$(MAKE) retention-check
	$(MAKE) test
	go test -tags=integration -race -timeout 25m ./...
	$(MAKE) smoke-container
	./scripts/smoke-test.sh
	./scripts/business-e2e.sh
	./scripts/admin-e2e.sh
	docker compose down -v --remove-orphans
	SEEV_LOAD_ACK=disposable-only $(MAKE) load-smoke
	git diff --check
	docker compose down -v --remove-orphans

## verify-chaos: Operator-controlled recovery gate; intentionally separate from verify-full
verify-chaos:
	./scripts/chaos-test.sh all

# Preserves /tmp/seev-chaos.*/*.log past the exit trap instead of deleting
# them, so a failing scenario can be inspected after the fact. Usage:
#   make chaos-debug SCENARIO=8
## chaos-debug: Re-run one chaos scenario (SCENARIO=1..20, default all) with logs preserved after exit
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
	@for service in gateway auth ledger payin payout fraud admin-bff assurance vendor dev-operator prometheus backup-agent; do \
		$(BUILD_DIR)/certgen issue --service $$service --out deploy/certs || exit $$?; \
	done

# docs/roadmap/archive/50 K3/K5: two independent secrets — the pgBackRest repository
# encryption passphrase and the seev_backup role's own password — generated
# locally, gitignored, mirrors the observability-secret pattern.
# Idempotent: does nothing to a secret that already exists.
#
# Mode 0644, not 0600: docker compose (non-swarm) mounts a file-based
# secret as a bind mount that PRESERVES the host file's own owner/mode —
# it does not normalize permissions the way Swarm secrets do. A 0600 file
# is only readable by whichever host uid owns it; inside the container,
# the entrypoint wrapper reads it as root (fine either way), but
# /docker-entrypoint-initdb.d/04-backup-role.sh runs later as the
# unprivileged "postgres" user, a DIFFERENT uid on any host where the
# secret wasn't generated by that same uid — which is every CI runner.
# Found live: `cat: can't open '/run/secrets/seev_backup_password':
# Permission denied` crashed the postgres container outright in CI
# (smoke-container job), silently masked on macOS/Docker Desktop's own
# bind-mount implementation, which does not enforce this the same way.
# These are local/CI-ephemeral dev secrets with no other host-side access
# control depending on 0600 — world-readable-on-this-machine is an
# acceptable trade for the container actually being able to start.
## backup-secret: Generate the pgBackRest repository passphrase and seev_backup role password (run once per machine)
backup-secret:
	@mkdir -p deploy/backup/secrets deploy/backup/repo
	@if [ ! -f deploy/backup/secrets/pgbackrest_repo_passphrase ]; then \
		openssl rand -base64 32 > deploy/backup/secrets/pgbackrest_repo_passphrase; \
		chmod 644 deploy/backup/secrets/pgbackrest_repo_passphrase; \
		echo "generated deploy/backup/secrets/pgbackrest_repo_passphrase"; \
	else \
		echo "deploy/backup/secrets/pgbackrest_repo_passphrase already exists, leaving it alone"; \
	fi
	@if [ ! -f deploy/backup/secrets/seev_backup_password ]; then \
		openssl rand -base64 24 > deploy/backup/secrets/seev_backup_password; \
		chmod 644 deploy/backup/secrets/seev_backup_password; \
		echo "generated deploy/backup/secrets/seev_backup_password"; \
	else \
		echo "deploy/backup/secrets/seev_backup_password already exists, leaving it alone"; \
	fi

# docs/roadmap/archive/50 K5: 04-backup-role.sh only runs automatically via
# /docker-entrypoint-initdb.d on a FRESH volume's first boot. An existing
# volume (like this repo's own dev seev_postgres_data, provisioned before
# Track A7 existed) never re-runs first-boot scripts, so this target
# re-invokes the EXACT SAME script inside the running container — never a
# hand-copied variant that could drift from the first-boot behavior.
# docs/roadmap/archive/51 T2.2: dev-only pkg/cryptox key material — shared
# cluster-wide (K2's own deliberate choice, same as JWT_SECRET/
# INTERNAL_GRPC_TOKEN, see scripts/vault-seed.sh's own comment), so ONE
# key pair is generated here, not one per service. 32-byte keys hex-encoded
# (64 hex chars) — internal/config.CryptoxConfig.Ring/Lookup decode hex,
# never base64, unlike backup-secret's own base64 passphrases above.
## cryptox-secret: Generate the dev pkg/cryptox KEK (v1) and lookup key (run once per machine)
cryptox-secret:
	@mkdir -p deploy/cryptox/secrets
	@if [ ! -f deploy/cryptox/secrets/cryptox_key_v1 ]; then \
		openssl rand -hex 32 > deploy/cryptox/secrets/cryptox_key_v1; \
		chmod 644 deploy/cryptox/secrets/cryptox_key_v1; \
		echo "generated deploy/cryptox/secrets/cryptox_key_v1"; \
	else \
		echo "deploy/cryptox/secrets/cryptox_key_v1 already exists, leaving it alone"; \
	fi
	@if [ ! -f deploy/cryptox/secrets/cryptox_lookup_key ]; then \
		openssl rand -hex 32 > deploy/cryptox/secrets/cryptox_lookup_key; \
		chmod 644 deploy/cryptox/secrets/cryptox_lookup_key; \
		echo "generated deploy/cryptox/secrets/cryptox_lookup_key"; \
	else \
		echo "deploy/cryptox/secrets/cryptox_lookup_key already exists, leaving it alone"; \
	fi
	@if [ ! -f deploy/cryptox/secrets/ledger_idempotency_key_v1 ]; then \
		openssl rand -hex 32 > deploy/cryptox/secrets/ledger_idempotency_key_v1; \
		chmod 644 deploy/cryptox/secrets/ledger_idempotency_key_v1; \
		echo "generated deploy/cryptox/secrets/ledger_idempotency_key_v1"; \
	else \
		echo "deploy/cryptox/secrets/ledger_idempotency_key_v1 already exists, leaving it alone"; \
	fi
	@if [ ! -f deploy/cryptox/secrets/export_kek_v1 ]; then \
		openssl rand -hex 32 > deploy/cryptox/secrets/export_kek_v1; \
		chmod 644 deploy/cryptox/secrets/export_kek_v1; \
		echo "generated deploy/cryptox/secrets/export_kek_v1"; \
	else \
		echo "deploy/cryptox/secrets/export_kek_v1 already exists, leaving it alone"; \
	fi
	@if [ ! -f deploy/cryptox/secrets/closure_kek_v1 ]; then \
		openssl rand -hex 32 > deploy/cryptox/secrets/closure_kek_v1; \
		chmod 644 deploy/cryptox/secrets/closure_kek_v1; \
		echo "generated deploy/cryptox/secrets/closure_kek_v1"; \
	else \
		echo "deploy/cryptox/secrets/closure_kek_v1 already exists, leaving it alone"; \
	fi

## backup-role-bootstrap: Create/refresh the seev_backup role on an ALREADY-INITIALIZED volume (run once per environment after `make backup-secret`)
backup-role-bootstrap:
	docker compose exec postgres sh /docker-entrypoint-initdb.d/04-backup-role.sh

# docs/roadmap/archive/50 K2: --data-checksums (POSTGRES_INITDB_ARGS) only takes effect
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

# docs/roadmap/archive/50 K3: `docker compose exec` starts a fresh process attached to
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
