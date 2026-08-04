# Shared Infrastructure Deep Dive (`internal/platform/`)

> [Documentation home](../README.md) · [Reference](README.md)

> **Status: Current. Audience: contributors working below the business
> services.** Start with [Services](services.md) when the change is business
> behavior rather than infrastructure.

`internal/platform/` is the private shared-infrastructure boundary. It contains
small, domain-neutral capabilities used by more than one service. The current
tree has 25 Go packages, grouped by responsibility rather than by an
unbounded shared namespace.

## The rule

Platform code may depend on infrastructure libraries, the standard library,
and generated wire types where necessary. It must not import `services/` or
encode a service's business vocabulary, table names, event names, or workflow
states.

Dependency direction is:

```text
service cmd
    ↓
service transport / worker
    ↓
service <domain> / feature
    ↓
service repository / adapter
    ↓
internal/platform
```

Cross-service clients are a separate boundary under `contracts/clients/`.
Load tools are under `tools/load/` and are never runtime dependencies. A
capability used by only one service stays inside that service even if its
implementation looks reusable.

## Platform catalog

| Capability | Package | Responsibility |
|---|---|---|
| Configuration | `internal/platform/config` | Typed environment, Vault, and service configuration |
| Database | `internal/platform/database` | PostgreSQL pools, transactions, pagination, and pool metrics |
| Database helpers | `internal/platform/database/identifiers` | UUID generation |
| Database helpers | `internal/platform/database/nulls` | Nullable SQL arguments |
| Cache | `internal/platform/cache` | Redis cache, counters, and rate limiting |
| Errors | `internal/platform/errors` | PostgreSQL duplicate and retry classification |
| Lifecycle | `internal/platform/lifecycle/objectoutbox` | Object-delete outbox processing |
| Lifecycle | `internal/platform/lifecycle/privacy` | Bounded privacy-export paging |
| Lifecycle | `internal/platform/lifecycle/retention/policy` | Retention policy loading and rendering |
| Lifecycle | `internal/platform/lifecycle/retention/worker` | Retention execution and failure handling |
| Messaging | `internal/platform/messaging` | RabbitMQ broker, publishers, consumers, and topology |
| Migrations | `internal/platform/migration` | Migration execution mechanics |
| Money | `internal/platform/money/currency` | Currency registry and minor-unit rules |
| Observability | `internal/platform/observability/logging` | Structured logging and sensitive-value masking |
| Observability | `internal/platform/observability/metrics` | Bounded resolution metrics safe for runtime binaries |
| Observability | `internal/platform/observability/tracing` | Request and message trace context |
| Resilience | `internal/platform/resilience/alerting` | Domain-neutral webhook alert delivery |
| Resilience | `internal/platform/resilience/egressproxy` | Controlled outbound HTTP proxying |
| Scheduling | `internal/platform/scheduling` | Cron, distributed locks, shutdown, and job metrics |
| Security | `internal/platform/security/crypto` | Encryption, key rings, and lookup helpers |
| Security | `internal/platform/security/middleware` | HTTP authentication, limits, recovery, and request controls |
| Security | `internal/platform/security/tls` | Service identity, mTLS, and certificate reloads |
| Transport | `internal/platform/transport/grpc` | Shared gRPC server/client bootstrap and interceptors |
| Transport | `internal/platform/transport/http/response` | Common HTTP response envelopes and decoding |
| Transport | `internal/platform/transport/httpcontract` | HTTP contract lifecycle and versioning helpers |
| Transport | `internal/platform/transport/httpserver` | HTTP server lifecycle and graceful shutdown |

Each package owns its tests. Do not add a new subpackage merely to mirror this
table; add one when it has a separate responsibility and a useful API.

## What is deliberately outside the platform

### Service-owned code

Business decisions, persistence ports, database repositories, transport
handlers, workers, and external adapters belong under the owning service:

```text
services/<service>/internal/
├── <domain>/                        # use-case orchestration
├── repository/                      # service-owned persistence
├── transport/http/                  # HTTP translation
├── transport/grpc/                  # gRPC translation, when present
├── worker/                          # background work, when present
└── adapter/                         # external-system implementations
```

For example, Auth's KYC provider boundary is
`services/auth/internal/adapter/kycvendor/`. It is not shared infrastructure
because it owns an Auth-specific provider contract.

### Cross-service clients and contracts

`contracts/clients/ledger`, `contracts/clients/ledger/errors`, and
`contracts/clients/fraud` are explicit clients for published service
boundaries. They may translate generated RPC messages into caller-friendly
types, but they must not import another service's implementation.

The VendorService adapter contract remains under `contracts/vendorgw/` because
Payin, Payout, and VendorService use the same boundary types. Vendor-specific
implementation remains private to `services/vendor-service/`.

### Test infrastructure

`internal/testkit/` is repository-wide integration support. It is test-only
and may construct service facades to provision realistic fixtures. Production
packages must not depend on it.

### Load tooling

`tools/load/lab` and `tools/load/report` support disposable capacity runs.
Runtime-safe resolution metrics live under
`internal/platform/observability/metrics`; they are deliberately separate from
load profile tooling so service binaries do not depend on load orchestration.

## Adding or moving shared code

Before adding a package under `internal/platform/`, confirm that it:

- has at least two real service consumers, or an approved repository-wide role;
- has one infrastructure responsibility;
- contains no owner-specific workflow or persistence vocabulary;
- has independent tests and documented configuration semantics;
- does not hide a network call or a business decision behind a generic helper.

Small mechanical helpers belong with their consumer. The former generic
utility package was removed: metadata access lives with Ledger metadata,
database identifiers and nullable SQL values live under the database platform,
and query/key helpers live with Ledger repositories or handlers.

For service ownership and import enforcement, see the
[architecture registry](../../tests/architecture/registry.go),
[boundary test](../../boundary_test.go), and
[project guide](../development/project-guide.md#package-layout-conventions).
