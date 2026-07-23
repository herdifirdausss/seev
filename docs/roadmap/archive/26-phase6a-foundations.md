# 26 — Phase 6a: Microservices Split — Master Reference and Foundations

This document has two roles: it is the master reference for plans 26–35, and it defines the foundation work in T1–T4. Read the master decisions before executing any later phase.

## Context

Plans 03–25 describe a mature modular monolith. Plans 26–35 split it into microservices for learning and operational practice, not for an existing production workload. Breaking changes are allowed: migrations may be renumbered, development data may be wiped with `docker compose down -v`, and URLs may change.

The required gate is stronger than documentation alone: every phase must end with a working system and green `make test`, smoke, and business end-to-end checks.

Two business capabilities are introduced during the split:

1. Database-driven routing for top-ups and payouts (plans 29–30), replacing hard-coded gateway maps.
2. Database-driven per-user, per-route fees (plan 33), replacing static `FEE_*` environment variables.

## Locked master decisions

| Area | Decision |
|---|---|
| Database | One PostgreSQL container with six databases: `seev_ledger`, `seev_auth`, `seev_payin`, `seev_payout`, `seev_fraud`, and `seev_gateway`; each service has its own login role. |
| Synchronous calls | gRPC using Buf-managed protobufs in `api/proto/`; generated code is committed under `gen/`. |
| Repository | One monorepo and one `go.mod`; each service has a `cmd/<service>` entry point and reuses the existing module package. |
| Runtime | Docker Compose is the primary runtime. Local Kubernetes with kind is optional in plan 35. |
| Public services | `gateway-service` (frontend-facing BFF) and `auth-service`. |
| Internal services | `ledger-service`, `payin-service`, `payout-service`, and `fraud-service`; none is directly reachable by end users. |
| Notifications | Move with `gateway-service`, including the `notif_notifications` database table and RabbitMQ consumer. |
| Policy | Remains inside `ledger-service` with `policy_limits` in `seev_ledger`. |
| Auth provisioning | Synchronous `LedgerService.ProvisionUser`, retaining lazy repair during login. |
| Ledger user API | Gateway reverse-proxies `/api/v1/ledger/*` to the ledger user-HTTP listener. Service-to-service calls still use gRPC. |
| Webhooks | Gateway owns the public webhook edge and forwards vendor, headers, and raw body bytes to payin. |
| Fee rules | `fee_rules` stays in `seev_ledger` because ledger posts the fee leg. “Route” is the existing ledger gateway string. |
| Routing rules | Payin rules live in `seev_payin`; payout rules live in `seev_payout`. Do not alter ledger disbursement, which has no vendor concept. |
| Fraud | New `internal/fraud` module and `seev_fraud` database. Ledger keeps a 500 ms timeout, fail-open gRPC pre-post hook. Velocity is counted from posted events in Redis DB 1. |
| Admin HTTP | Every service owns its internal admin listener. An admin BFF is future work. |
| JWT | All services share `JWT_SECRET` and verify tokens locally. |
| Internal auth | Static `INTERNAL_GRPC_TOKEN` in Bearer metadata; mTLS is future work. |
| Outbox | Only ledger owns an outbox initially. Payout status events may require a payout-owned outbox later. |
| Migrations | Store migrations under `migrations/<service>/`, starting at 000001 per service. When databases are temporarily shared, use a service-specific migration table. |
| Extraction order | Ledger → auth → payin → payout → fraud → gateway → fee rules → verification → optional Kubernetes. |

## Target architecture

```text
Internet
  ├── gateway-service :8080  (public BFF, webhook edge, notifications)
  └── auth-service    :8082  (public register/login/refresh/me)

gateway ── HTTP reverse proxy ── ledger user API :8090
gateway ── gRPC ── payin :9092 ── gRPC ── ledger :9091
gateway ── gRPC ── payout :9093 ── gRPC ── ledger :9091
ledger  ── gRPC hook ── fraud :9094

ledger :8091, payin :8092, payout :8093, fraud :8094
  internal admin listeners

RabbitMQ ledger.events
  ├── audit
  ├── notifications → gateway consumer
  └── fraud → fraud velocity consumer
```

Compose ports are gateway 8080/8081, auth 8082/8083, ledger gRPC 9091/user HTTP 8090/admin 8091, payin 9092/8092, payout 9093/8093, and fraud 9094/8094. Host-run scripts may use their existing 1xxxx mappings.

## gRPC contract principles

All protobufs live under `api/proto/seev/<service>/v1/` and generate into `gen/<service>/v1`.

The ledger service exposes `Post`, `GetTransactionByIdempotencyKey`, `GetUserCurrency`, `ResolveFee`, and `ProvisionUser`. Posting amounts are decimal strings representing integer minor units; UUIDs are strings, with an empty string representing `uuid.Nil`; metadata uses `google.protobuf.Struct`.

Error mapping is part of the business contract:

- `LedgerError` → `FailedPrecondition` plus `ErrorInfo` containing its code;
- `ErrAlreadyClosed` → `Aborted` plus `ALREADY_CLOSED`;
- not found → `NotFound`;
- all other errors → `Internal`;
- unmapped client statuses, including `Unavailable`, are retryable infrastructure failures.

Payin accepts vendor, headers, and raw body bytes and returns an explicit business-failure result so the gateway can preserve the 200-versus-503 webhook contract. Payout accepts user, amount, destination, and creator; routing chooses the vendor. Fraud exposes `Screen(tx_type, user_id, amount, currency)` and returns `block` plus `reason`.

## Execution gotchas

1. `go build ./...` does not compile `_test.go`; run both normal and integration `go vet` after signature changes.
2. Always normalize RabbitMQ defaults. A zero publish-concurrency semaphore blocks forever.
3. Local PostgreSQL is normally exposed on host port 5433; preserve the auto-detection in `scripts/lib.sh`.
4. Distroless images have no shell or curl. Use the binary's `-healthcheck` mode.
5. Multiple migration folders sharing one database require distinct migration-table names.
6. Services connect with limited roles; migrations run as schema owners. Provision each service role and grants for every new database.
7. gRPC status and `ErrorInfo` semantics are load-bearing for payin and payout retry behavior.
8. Never represent money as a numeric protobuf field.
9. Avoid typed-nil interface values when wiring optional clients.
10. Declare RabbitMQ exchanges and queues before publishing; consumers should declare their own queues so startup order remains flexible.
11. Until plan 33 removes it, call `SetFeeRules` before the ledger starts serving because it rebuilds the public router.
12. Every service must use the same `JWT_SECRET`.
13. Boundary tests intentionally skip `_test.go`; cross-module integration tests remain legal.
14. With 3.9 GB of development RAM, do not run the full Compose app, Jaeger, and testcontainers at the same time.
15. Do not modify `internal/ledger/service/disbursement` for vendor routing.
16. Existing accrual snapshot-test flakiness is tracked separately and is not a split regression.

## Phase gate

Each phase is complete only when these checks are green:

```bash
make lint && make test
go vet ./... && go vet -tags=integration ./...
./scripts/smoke-test.sh
./scripts/business-e2e.sh
```

Development database cutovers may use `docker compose down -v`. After each task, update its DoD and result; after each phase, update the plan index.

## Foundations work

The foundations phase must not change monolith behavior. It removes the grandfathered `pkg → internal/config` dependency, installs the Buf and gRPC toolchain, adds shared gRPC plumbing, and reorganizes migrations by service.

### T1 — Local config types in `pkg`

Create local config structs for database, cache, logger, and messaging packages. Add conversion methods in `internal/config` and use them only at the composition root. Remove all `internal/config` imports from `pkg/`, empty the grandfathered exception map, and prove runtime configuration remains equivalent.

**DoD:** `grep -rn "internal/config" pkg/` returns no results and the full test and boundary suites pass.

Status: not started.

### T2 — Buf and the first protobuf

Add `buf.yaml`, `buf.gen.yaml`, the test-only `PingService`, pinned tool targets, lint and breaking-change checks, committed generated code, and boundary exceptions for `api` and `gen`.

**DoD:** a fresh clone can run `make tools proto`, produce deterministic generated code, and pass `make proto-lint`.

Status: not started.

### T3 — Shared gRPC and error packages

Add `pkg/grpcx` with recovery, logging, token authentication, health serving, and client dialing. Add `pkg/ledgererr` with typed ledger errors, `ErrAlreadyClosed`, and status round-trip decoding. Test through bufconn with the Ping service and keep both packages independent from `internal/*`.

**DoD:** gRPC services can use the shared plumbing without importing internal modules.

Status: not started.

### T4 — Migration and multi-database groundwork

Move migrations into service directories, add service-specific migration tables, provision six databases and login roles on a fresh PostgreSQL volume, and update the Makefile and scripts. The monolith still runs against the shared development database until each service phase performs its cutover.

**DoD:** all service migration folders apply cleanly, all six databases and roles exist, and smoke and business journeys remain green.

Status: not started.

## Final verification for this document

Run the master phase gate plus the fresh-volume database checks, then continue to [plan 27](27-phase6b-ledger-service.md).
