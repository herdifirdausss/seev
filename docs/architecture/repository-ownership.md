# Repository ownership map

This is the contributor-facing ownership map for the service-centric monolith.
The executable service registry lives in
[`tests/architecture/registry.go`](../../tests/architecture/registry.go); this
page explains what each boundary owns and where a change should start.

## Deployable services

| Logical service | Source root | Entrypoint | Owns |
|---|---|---|---|
| gateway | `services/gateway/` | `cmd/gateway/` | Merchant API, tenants, quotas, webhooks, notifications |
| auth | `services/auth/` | `cmd/auth/` | Identity, sessions, KYC, privacy, closure |
| ledger | `services/ledger/` | `cmd/ledger/` | Accounts, balances, postings, fees, reconciliation |
| payin | `services/payin/` | `cmd/payin/` | Top-up intents, routing, intake, callbacks |
| payout | `services/payout/` | `cmd/payout/` | Payout requests, routing, vendor dispatch, recovery |
| fraud | `services/fraud/` | `cmd/fraud/` | Screening, sanctions, velocity, fraud events |
| adminbff | `services/adminbff/` | `cmd/adminbff/` | Operator sessions, proxying, audit |
| assurance | `services/assurance/` | `cmd/assurance/` | Read-only cross-service assurance and findings |
| vendor | `services/vendor-service/` | `cmd/vendor/` | Vendor callbacks, attempts, retries, boundary adapters |

Every service owns its `migrations/` directory and has a short `README.md`.
The physical `vendor-service` name is intentional; the logical service name
and database remain `vendor` / `seev_vendor`.

## Service-internal layout

The first directory under a service's `internal/` must describe a bounded
context or technical boundary:

```text
services/<service>/
├── cmd/<binary>/             # process composition only
├── internal/
│   ├── <domain>/              # domain/use-case orchestration
│   ├── repository/            # service-owned persistence ports/adapters
│   ├── transport/http/        # HTTP inbound adapter
│   ├── transport/grpc/        # gRPC inbound adapter, when present
│   ├── worker/                # background processing
│   └── adapter/<provider>/     # outbound provider adapter
├── migrations/
└── README.md
```

The domain roots are deliberately named after their owner (`internal/auth`,
`internal/payin`, `internal/ledger`, and so on). Context-specific model and
error packages live below those roots (`internal/auth/model`,
`internal/ledger/errors`). Provider-specific adapters live under the owning
service's `internal/adapter/<provider>` path; for example, VendorService's
synthetic provider is at `services/vendor-service/internal/adapter/mockvendor`.
An unqualified `internal/model` is not allowed.
Generic `service`, `handler`, `server`, `application`, `common`, `utils`, and
`helpers` buckets are rejected by the architecture tests. A feature gets its
own package only when it has a real boundary—independent invariants,
persistence port, lifecycle, or a meaningful change surface—not to make the
tree artificially symmetrical.

## Shared and repository-wide boundaries

| Boundary | Ownership rule |
|---|---|
| `internal/platform/` | Domain-neutral runtime infrastructure reused by services |
| `internal/testkit/` | Test-only repository-wide integration fixtures |
| `contracts/` | Versioned HTTP, protobuf, event, and cross-service client contracts |
| `gen/go/` | Generated Go contract bindings; never hand-edit |
| `tools/` | Developer, CI, generation, load, and validation utilities |
| `operations/` | Support agents and recovery workflows |
| `tests/` | Repository-wide architecture, contract, E2E, chaos, and load tests |

Platform code cannot import services or testkit. Services cannot import other
services' `internal/` packages; they use public facades, generated clients, or
contracts instead. CODEOWNERS mirrors these boundaries for review routing.

## Change navigation

1. Start at the owning service README.
2. Follow `cmd/` to the composition root.
3. Follow the domain package for decisions, `repository/` for persistence,
   `transport/` for inbound mapping, and `worker/` for background work.
4. Check the service's migrations and focused tests before changing behavior.
5. Run `make architecture-check` when moving ownership or paths.
6. Use [`repository-structure-metrics.md`](repository-structure-metrics.md) to
   find packages that have crossed the size-review threshold.
