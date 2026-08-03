BINARY          := gateway
BUILD_DIR       := bin
CMD_DIR         := ./cmd/gateway
GO_BUILD_FLAGS  := -trimpath -ldflags="-s -w"
SERVICE_NAMES   := gateway auth-service ledger-service payin-service payout-service fraud-service admin-bff-service assurance-service vendor-service mock-push-provider
CERT_IDENTITIES := gateway auth ledger payin payout fraud admin-bff assurance vendor dev-operator prometheus backup-agent

.DEFAULT_GOAL := help
SHELL := /bin/sh
.DELETE_ON_ERROR:

BUF_VERSION                := v1.72.0
PROTOC_GEN_GO_VERSION      := v1.36.11
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2
PROTO_MERGE_BASE_REF       ?= main
GOLANGCI_LINT_VERSION      ?= v2.12.2
GOVULNCHECK_VERSION        ?= v1.6.0

TOOLS_DIR := $(abspath $(BUILD_DIR)/tools)
PROTO_TOOLS_DIR := $(TOOLS_DIR)/proto-$(BUF_VERSION)-$(PROTOC_GEN_GO_VERSION)-$(PROTOC_GEN_GO_GRPC_VERSION)
BUF := $(PROTO_TOOLS_DIR)/buf
PROTOC_GEN_GO := $(PROTO_TOOLS_DIR)/protoc-gen-go
PROTOC_GEN_GO_GRPC := $(PROTO_TOOLS_DIR)/protoc-gen-go-grpc
GOLANGCI_LINT_DIR := $(TOOLS_DIR)/golangci-lint-$(GOLANGCI_LINT_VERSION)
GOLANGCI_LINT := $(GOLANGCI_LINT_DIR)/golangci-lint
GOVULNCHECK_DIR := $(TOOLS_DIR)/govulncheck-$(GOVULNCHECK_VERSION)
GOVULNCHECK := $(GOVULNCHECK_DIR)/govulncheck

.PHONY: build build-all run dev test test/cover clean lint modernize-check ci-lint print-golangci-lint-version print-govulncheck-version docs-check tidy tools tools-lint tools-security security-vuln proto proto-lint proto-breaking contract-generate contract-lint contract-breaking contract-test contracts load-lint load-test load-seed load-snapshot load-restore load-smoke load-run load-capacity load-report-check load-clean vet docker-up docker-down smoke-container smoke-test business-e2e admin-e2e privacy-e2e merchant-e2e verify-static verify-full verify-chaos chaos-debug migrate-up migrate-up-all migrate-down grant-app-role observability-secret observability-up observability-down certs backup-secret backup-role-bootstrap backup-checksums-enable backup-stanza-init backup-full backup-diff backup-check backup-status backup-expire cryptox-secret retention-docs retention-check help

## build: Compile the binary
build:
	@mkdir -p "$(BUILD_DIR)"
	go build $(GO_BUILD_FLAGS) -o "$(BUILD_DIR)/$(BINARY)" "$(CMD_DIR)"

## build-all: Compile all nine deployable service binaries
build-all:
	@mkdir -p "$(BUILD_DIR)"
	go build $(GO_BUILD_FLAGS) -o "$(BUILD_DIR)/" $(addprefix ./cmd/,$(SERVICE_NAMES))

## run: Run the compiled binary
run: build
	./$(BUILD_DIR)/$(BINARY)

## dev: Run with live reload (requires a pinned air binary in PATH)
dev:
	@command -v air >/dev/null 2>&1 || { echo "air is required; install a pinned version separately" >&2; exit 2; }
	air

## test: Run all tests with race detector
test:
	go test -race -cover ./...

## test/cover: Run tests and open HTML coverage report
test/cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

## clean: Remove only repository-local build and test artifacts
clean:
	@case "$(BUILD_DIR)" in \
		bin) rm -rf -- bin ;; \
		*) echo "clean: refusing non-default BUILD_DIR=$(BUILD_DIR)" >&2; exit 2 ;; \
	esac
	rm -f -- coverage.out
	rm -rf -- .smoke-container-artifacts

## lint: Install and run the repository-pinned golangci-lint
lint: tools-lint modernize-check
	"$(GOLANGCI_LINT)" run ./...

## modernize-check: Ensure Go 1.26's safe modernizers have no pending changes
modernize-check:
	# omitzero changes JSON wire behavior; apply it only with an explicit
	# contract decision, so this gate checks the safe modernizers by default.
	go fix -omitzero=false -diff ./...

## ci-lint: Validate workflow syntax, shell scripts, and action pin policy
ci-lint:
	@command -v actionlint >/dev/null 2>&1 || { echo "actionlint is required" >&2; exit 2; }
	@command -v shellcheck >/dev/null 2>&1 || { echo "shellcheck is required" >&2; exit 2; }
	actionlint -shellcheck "$$(command -v shellcheck)"
	find scripts -name '*.sh' -print0 | xargs -0 shellcheck --severity=error
	./scripts/ci/check-action-pins.sh

## print-golangci-lint-version: Print the pinned golangci-lint version (single source of truth for CI)
print-golangci-lint-version:
	@echo $(GOLANGCI_LINT_VERSION)

## print-govulncheck-version: Print the pinned govulncheck version (single source of truth for CI)
print-govulncheck-version:
	@echo $(GOVULNCHECK_VERSION)

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

## tools: Install pinned toolchain versions into the repository-local bin/tools directory
tools: $(BUF) $(PROTOC_GEN_GO) $(PROTOC_GEN_GO_GRPC)

## tools-lint: Install the exact linter version used by the repository
tools-lint: $(GOLANGCI_LINT)

## tools-security: Install the pinned Go vulnerability scanner
tools-security: $(GOVULNCHECK)

## security-vuln: Scan reachable Go dependencies and standard-library calls
security-vuln: tools-security
	"$(GOVULNCHECK)" ./...

$(BUF):
	@mkdir -p "$(@D)"
	GOBIN="$(@D)" go install "github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)"

$(PROTOC_GEN_GO):
	@mkdir -p "$(@D)"
	GOBIN="$(@D)" go install "google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)"

$(PROTOC_GEN_GO_GRPC):
	@mkdir -p "$(@D)"
	GOBIN="$(@D)" go install "google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)"

$(GOLANGCI_LINT):
	@mkdir -p "$(@D)"
	GOBIN="$(@D)" go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)"

$(GOVULNCHECK):
	@mkdir -p "$(@D)"
	GOBIN="$(@D)" go install "golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)"

## proto: Generate committed Go protobuf bindings
proto proto-lint proto-breaking: tools

proto:
	PATH="$(PROTO_TOOLS_DIR):$$PATH" "$(BUF)" generate

## proto-lint: Lint protobuf contracts
proto-lint:
	"$(BUF)" lint

## proto-breaking: Check protobuf compatibility against an explicit merge-base ref
proto-breaking:
	"$(BUF)" breaking --against ".git#ref=$(PROTO_MERGE_BASE_REF)"

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
contract-lint contract-breaking contract-test: contract-generate

contracts: contract-lint contract-breaking contract-test

## load-lint: Validate B0 profiles, safety schemas, and helper/scenario tests without Docker mutation
load-lint:
	go test ./pkg/loadlab ./pkg/loadreport ./pkg/loadmetrics ./cmd/loadcheck ./cmd/loadseed ./cmd/loadreport ./cmd/loadprobe ./cmd/loaddataset ./tests/load
	go run ./cmd/loadcheck -profile deploy/load/profiles/local-small.yaml

## load-test: Fast B0 helper/analyzer/safety tests; this does not claim capacity
load-test: load-lint
	git diff --check

LOAD_SEED_KIND ?= journey
LOAD_SEED_COUNT ?= 100
LOAD_SEED_OUTPUT ?= artifacts/load/seed/seed.jsonl
export LOAD_SEED_KIND LOAD_SEED_COUNT LOAD_SEED_OUTPUT SEEV_LOAD_ACK SEEV_LOAD_RUN_ID
## load-seed: Generate synthetic deterministic seed material; requires an explicit disposable acknowledgement
load-seed:
	@./scripts/load-seed.sh

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
export LOAD_SCENARIO LOAD_RATE
load-capacity:
	@./scripts/load-capacity.sh

LOAD_RUNS ?=
export LOAD_RUNS LOAD_REPORT_OUT
## load-report-check: Validate and aggregate committed-size run summaries without averaging percentiles
load-report-check:
	@./scripts/load-report-check.sh

## load-clean: Remove only the exact disposable Compose project and its run artifact path
load-clean:
	SEEV_LOAD_ACK=disposable-only ./scripts/load-test.sh clean

## vet: Run go vet
vet:
	go vet $(GO_BUILD_FLAGS) ./...

## docker-up: Start infrastructure (postgres, redis, rabbitmq)
docker-up:
	docker compose up -d --wait

## docker-down: Stop infrastructure
docker-down:
	docker compose down --remove-orphans

## smoke-container: Full-container round-trip (docs/roadmap/archive/44 K4) — real Docker images via `docker compose --profile app`, not host binaries
smoke-container:
	./scripts/smoke-container.sh

## smoke-test: Core host-binary smoke journey (ledger, payin, payout)
smoke-test:
	./scripts/smoke-test.sh all

## business-e2e: Full end-user and operator business journey
business-e2e:
	./scripts/business-e2e.sh

## admin-e2e: Admin BFF session, CSRF, mutation, and audit journey
admin-e2e:
	./scripts/admin-e2e.sh

## privacy-e2e: Privacy export, retention hold, and closure journey with managed host stack
privacy-e2e:
	./scripts/privacy-e2e-host.sh

## merchant-e2e: Merchant/B2B onboarding, payin, transfer, isolation, and kill-switch journey (Plan 57 T10)
merchant-e2e:
	./scripts/merchant-e2e.sh

## verify-static: Repeatable non-Docker build, static, contract, security, and safety gate
verify-static:
	go build $(GO_BUILD_FLAGS) ./...
	go vet ./...
	go mod verify
	$(MAKE) ci-lint
	$(MAKE) lint
	$(MAKE) security-vuln
	$(MAKE) docs-check
	$(MAKE) retention-check
	$(MAKE) contracts
	$(MAKE) load-test
	go test -tags=loadtest ./...
	git diff --check

# This is what docs/development/project-guide.md's "Build and verification"
# section means by "the full gate" — run this instead of chaining the steps by
# hand so a volume reset is never skipped by mistake. Any ad-hoc debugging
# against the shared dev stack can leave state behind that smoke-test.sh's
# fixed-UUID fixtures will misreport as a regression. This target covers every
# repeatable non-chaos gate from a clean environment; chaos is deliberately
# isolated in verify-chaos because it kills dependencies and is an
# operator-controlled recovery drill.
## verify-full: Complete repeatable gate from clean volumes (build/vet/lint/race/integration/contracts/proto/load/docs/container+business/admin/privacy)
verify-full:
	@set -eu; \
	project=seev-verify; \
	export COMPOSE_PROJECT_NAME="$$project"; \
	cleanup() { status=$$?; trap - EXIT INT TERM; docker compose --profile app down -v --remove-orphans >/dev/null 2>&1 || true; exit "$$status"; }; \
	trap cleanup EXIT INT TERM; \
	docker compose --profile app down -v --remove-orphans; \
	$(MAKE) --no-print-directory verify-static; \
	go vet -tags=integration ./...; \
	$(MAKE) --no-print-directory tools; \
	$(MAKE) --no-print-directory proto; \
	$(MAKE) --no-print-directory proto-lint; \
	$(MAKE) --no-print-directory proto-breaking; \
	$(MAKE) --no-print-directory test; \
	go test -tags=integration -race -timeout 25m ./...; \
	$(MAKE) --no-print-directory smoke-container; \
	$(MAKE) --no-print-directory smoke-test; \
	$(MAKE) --no-print-directory business-e2e; \
	$(MAKE) --no-print-directory admin-e2e; \
	$(MAKE) --no-print-directory privacy-e2e; \
	$(MAKE) --no-print-directory merchant-e2e; \
	docker compose --profile app down -v --remove-orphans; \
	SEEV_LOAD_ACK=disposable-only $(MAKE) --no-print-directory load-smoke; \
	git diff --check

## verify-chaos: Operator-controlled recovery gate; intentionally separate from verify-full
verify-chaos:
	./scripts/chaos-test.sh all

# Preserves /tmp/seev-chaos.*/*.log past the exit trap instead of deleting
# them, so a failing scenario can be inspected after the fact. Usage:
#   make chaos-debug SCENARIO=8
## chaos-debug: Re-run one chaos scenario (SCENARIO=1..20, default all) with logs preserved after exit
SCENARIO ?= all
export SCENARIO
chaos-debug:
	@case "$${SCENARIO}" in \
		all|1|2|3|4|5|6|7|8|9|10|11|12|13|14|15|16|17|18|19|20) ;; \
		*) echo "chaos-debug: SCENARIO must be all or 1..20" >&2; exit 2 ;; \
	esac; \
	KEEP_WORK_DIR=1 ./scripts/chaos-test.sh "$${SCENARIO}"

# Migrations run as the schema OWNER (POSTGRES_MIGRATE_USER), never as the
# app's restricted POSTGRES_USER (docs/roadmap/archive/16 Task T3) — DDL and DML
# identities stay separate on purpose.
# Port default (5433) matches docker-compose.yml's own default — see its
# comment on the postgres service's `ports:` mapping.
SERVICE ?= ledger
POSTGRES_MIGRATE_USER ?= seev
POSTGRES_MIGRATE_PASSWORD ?= seev
POSTGRES_HOST ?= localhost
POSTGRES_PORT ?= 5433
POSTGRES_SSL_MODE ?= disable
export SERVICE POSTGRES_USER POSTGRES_MIGRATE_USER POSTGRES_MIGRATE_PASSWORD POSTGRES_HOST POSTGRES_PORT POSTGRES_SSL_MODE

## migrate-up: Run one service's pending migrations (default SERVICE=ledger)
migrate-up:
	@./scripts/migrate.sh up

## migrate-up-all: Run every service migration folder against the current database
migrate-up-all:
	$(MAKE) --no-print-directory migrate-up SERVICE=ledger || exit $$?
	@for path in migrations/*; do \
		[ -d "$$path" ] || continue; \
		service=$${path##*/}; \
		[ "$$service" = ledger ] && continue; \
		$(MAKE) --no-print-directory migrate-up SERVICE="$$service" || exit $$?; \
	done

## migrate-down: Roll back the selected service's last migration
migrate-down:
	@./scripts/migrate.sh down

## grant-app-role: Grant the app_service DB role to POSTGRES_USER (run once per environment, after the first migrate-up creates app_service — docs/roadmap/archive/16 Task T3)
grant-app-role:
	@./scripts/grant-app-role.sh

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
	@mkdir -p "$(BUILD_DIR)"
	go build $(GO_BUILD_FLAGS) -o "$(BUILD_DIR)/certgen" ./cmd/certgen
	"$(BUILD_DIR)/certgen" init-ca --out deploy/certs
	@for service in $(CERT_IDENTITIES); do \
		"$(BUILD_DIR)/certgen" issue --service "$$service" --out deploy/certs || exit $$?; \
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
	@if [ ! -f deploy/cryptox/secrets/merchant_api_key_pepper ]; then \
		openssl rand -hex 32 > deploy/cryptox/secrets/merchant_api_key_pepper; \
		chmod 644 deploy/cryptox/secrets/merchant_api_key_pepper; \
		echo "generated deploy/cryptox/secrets/merchant_api_key_pepper"; \
	else \
		echo "deploy/cryptox/secrets/merchant_api_key_pepper already exists, leaving it alone"; \
	fi

## backup-role-bootstrap: Create/refresh the seev_backup role on an ALREADY-INITIALIZED volume (run once per environment after `make backup-secret`)
backup-role-bootstrap:
	docker compose exec -T postgres sh /docker-entrypoint-initdb.d/04-backup-role.sh

# docs/roadmap/archive/50 K2: --data-checksums (POSTGRES_INITDB_ARGS) only takes effect
# on a fresh initdb. An existing volume needs Postgres fully STOPPED and
# pg_checksums run offline directly against the data directory — this
# target does exactly that, then restarts and verifies. Never run this
# against a volume with the server still accepting connections; pg_checksums
# refuses an active data directory by design, so a lock-file check backs
# that up rather than relying on the refusal alone.
## backup-checksums-enable: Enable data page checksums on the EXISTING seev_postgres_data volume (postgres must be stopped first)
backup-checksums-enable:
	@./scripts/backup-checksums-enable.sh

# docs/roadmap/archive/50 K3: pgBackRest commands use scripts/pgbackrest.sh.
# The wrapper reads the mounted secret inside the postgres container, so the
# passphrase never appears in Make output or host-side process arguments.
## backup-stanza-init: Create the pgBackRest stanza (run once, after backup-secret and backup-role-bootstrap)
backup-stanza-init:
	@./scripts/pgbackrest.sh stanza-create

## backup-full: Run a full backup
backup-full:
	@./scripts/pgbackrest.sh --type=full backup

## backup-diff: Run a differential backup
backup-diff:
	@./scripts/pgbackrest.sh --type=diff backup

## backup-check: Verify the backup repository and WAL archive are consistent
backup-check:
	@./scripts/pgbackrest.sh check

## backup-status: Show backup/repository info (oldest/latest restorable point, backup set list)
backup-status:
	@./scripts/pgbackrest.sh info --output=json

## backup-expire: Expire backups/WAL outside the retention policy (K4; run only after a successful backup + check)
backup-expire:
	@./scripts/pgbackrest.sh expire

## help: Print this help message
help:
	@sed -n 's/^## //p' Makefile | column -t -s ':' | sed -e 's/^/  /'
