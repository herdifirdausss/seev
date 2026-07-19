# Seev

Seev is a service-oriented fintech backend written in Go. It provides ledger,
authentication, pay-in, payout, fraud-screening, notification, and gateway
capabilities with separate service databases and explicit runtime boundaries.

The repository is designed for local learning and engineering validation. The
default credentials and mock vendor integrations are for local development
only; they are not production secrets or real payment integrations.

## Runtime architecture

Six deployable services are built from this repository:

| Service | Public/internal ports | Database | Primary responsibility |
|---|---:|---|---|
| Gateway | 8080, 8081 | seev_gateway | Public API composition, notifications, and ledger event consumption |
| Auth | 8082, 8083 | seev_auth | Registration, login, refresh tokens, profiles, roles, and KYC state |
| Ledger | 8090, 8091, gRPC 9091 | seev_ledger | Double-entry postings, policies, fees, reconciliation, reporting, and workers |
| Pay-in | 8092, gRPC 9092 | seev_payin | Top-up intents, signed vendor webhooks, and routing |
| Payout | 8093, gRPC 9093 | seev_payout | Withdrawal orchestration, vendor commands, recovery, and routing |
| Fraud | 8094, gRPC 9094 | seev_fraud | Synchronous screening rules and asynchronous event enrichment |

PostgreSQL stores service-owned data, Redis supports caching, rate limiting,
velocity checks, and distributed coordination, and RabbitMQ carries ledger
events through the transactional outbox flow. Inter-service request paths use
HTTP or gRPC contracts; services must not query another service's database.

## Repository layout

~~~text
.
├── api/proto/               # Protobuf service contracts
├── cmd/                     # Six service entrypoints plus local utilities
├── deploy/observability/    # Prometheus, Grafana, Loki, Tempo, and Alloy config
├── docs/                    # Event contract, plans, and operational runbooks
├── gen/                     # Committed generated protobuf bindings
├── internal/                # Service and domain implementations
├── migrations/              # Per-service SQL migrations
├── pkg/                     # Shared infrastructure packages
├── scripts/                 # CI, smoke, business journey, and chaos tests
├── docker-compose.yml       # Local infrastructure and opt-in service profiles
└── Makefile                 # Build, migration, verification, and operations targets
~~~

The most important engineering constraints are documented in
[PROJECT_GUIDE.md](PROJECT_GUIDE.md).

## Requirements

- Go 1.25.6 or a compatible newer toolchain
- Docker with Compose
- golang-migrate for direct migration targets
- golangci-lint for make lint
- buf, protoc-gen-go, and protoc-gen-go-grpc when changing protobufs

Install the pinned protobuf tools with:

~~~bash
make tools
~~~

## Local quick start

Create a local environment file and replace the placeholder secrets:

~~~bash
cp .env.example .env
~~~

Start PostgreSQL, Redis, and RabbitMQ:

~~~bash
make docker-up
~~~

Apply every service migration:

~~~bash
make migrate-up-all
~~~

Build and start all six application containers:

~~~bash
docker compose --profile app up --build -d
~~~

Useful local endpoints:

- Gateway API: http://127.0.0.1:8080
- Gateway health and metrics: http://127.0.0.1:8081
- RabbitMQ management: http://127.0.0.1:15672
- PostgreSQL host port: 5433
- Redis host port: 6380

Stop the stack with:

~~~bash
docker compose --profile app down
~~~

The Compose defaults are intentionally convenient for local development.
Before using any non-local environment, set strong values for JWT_SECRET,
INTERNAL_GRPC_TOKEN, database credentials, broker credentials, vendor
secrets, and TLS-related settings.

## Build and verification

~~~bash
make build-all       # build all six deployable services
make test            # unit tests with race detection and coverage
make vet             # static checks from the Go toolchain
make lint            # golangci-lint
make proto-lint      # protobuf lint
git diff --check     # whitespace validation
~~~

Integration tests use build tags and require Docker:

~~~bash
go test -tags=integration -race ./...
~~~

Operational verification:

~~~bash
./scripts/smoke-test.sh
./scripts/business-e2e.sh
./scripts/chaos-test.sh all
make smoke-container
~~~

make verify-full runs the complete repository gate from clean Docker volumes.
It is intentionally heavier than the normal unit-test loop.

## Protobuf workflow

Generated Go bindings under gen/ are committed:

~~~bash
make proto
make proto-lint
make proto-breaking
~~~

Run the breaking-change check from a branch that can resolve the local main
reference.

## Observability

The optional observability profile contains Prometheus, Grafana, Loki, Tempo,
Alloy, and a restricted Docker socket proxy:

~~~bash
make observability-up
make observability-down
~~~

The first command creates a local, ignored Grafana admin secret when needed.
Do not run the observability profile alongside the full testcontainers suite
on a memory-constrained Docker installation.

## Security and financial invariants

- Ledger entries are append-only; corrections use compensating transactions.
- Monetary values use decimal or integer minor-unit representations, never
  binary floating point.
- Every posting is balanced and requires an idempotency key.
- Application and migration database identities are separate.
- Service databases are private to their owning service.
- Public logs mask credentials and avoid full idempotency keys.
- Container ports are bound to loopback in the local Compose configuration.

See [PROJECT_GUIDE.md](PROJECT_GUIDE.md) before changing transaction ordering,
service boundaries, security controls, or verification scripts.

## Documentation

- [Project guide](PROJECT_GUIDE.md) — contributor rules and verification workflow
- [Plan index](docs/plan/README.md) — implementation history and future roadmap
- [Event contract](docs/events.md) — versioned ledger event payloads
- [Runbooks](docs/runbooks/) — reconciliation, integrity, disaster recovery, and reporting operations
- [Scheduler guide](pkg/scheduler/README.md) — shared cron scheduler API and behavior
