# 01 — Target Architecture and Locked Decisions

## Modular-monolith principles

One binary and one database, with the code organized into modules whose
boundaries are enforced:

```text
cmd/
  server/main.go            # the only application entry point; wiring only, <150 lines
internal/
  config/                   # existing environment configuration
  server/                   # HTTP server and graceful shutdown
  handler/                  # HTTP router and composition root
  ledger/                   # MODULE 1: the current focus
    ledger.go               # public module API: interfaces and constructor
    constant/ apperror/ model/ processors/ repository/
    service/                # posting engine and account provisioning
    transport/              # ledger-owned HTTP handlers
    worker/                 # outbox relay and verification jobs
  <next-module>/            # auth/, payment/, notification/ — the same pattern later
pkg/                        # domain-neutral libraries (database, cache, messaging,
                            # middleware, logger, response, scheduler, generalutil, generalerror)
migrations/                 # golang-migrate files with paired numbered up/down migrations
docs/roadmap/                  # these planning documents
docs/design/legacy-schemas/ # archived schemas for reference; never executed
```

### Boundary rules (enforced by code and review)

1. Other modules may import only the root `internal/ledger` package, which
   exposes public interfaces and DTO types. Code outside the ledger module must
   not import `internal/ledger/repository`, `internal/ledger/processors`, or
   other ledger subpackages. The only exception is
   `internal/<mod>/events`, which contains event-payload contracts; see 14 T3.
2. Modules communicate either through synchronous calls to public module
   interfaces or through asynchronous outbox events published to RabbitMQ.
   Modules must not query another module's tables.
3. `pkg/` must not import `internal/`. Dependencies flow one way:
   `cmd` → `internal` → `pkg`.
4. Each module owns its tables. Ledger tables do not need a prefix because their
   ownership is already clear; future modules should use prefixes such as
   `auth_users`.

> **Update 2026-07-12:** Rules 1–3 are now enforced automatically by
> `boundary_test.go`, which runs through `make test`, rather than by review
> alone. The long-term module-to-service map (pay-in, payout, vendorgw, fraud,
> admin, and user-facing services) and its locked decisions are in
> [21-service-topology-review.md](21-service-topology-review.md). The
> `<next-module>` placeholder above now has a concrete name and order there.

## Locked decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | **The database schema follows the Go code** (`ledger_transactions`, `TEXT` codes such as `'debit'`, `'cash'`, and `'active'`), rather than the `ledgernew.sql` design with `SMALLINT` lookup foreign keys. Port the useful safeguards from `ledgernew.sql`. | The code, posting engine, 22 processors, and tests already exist and have gone through review. Adapting all of them to another schema is riskier than writing matching DDL. Lookup-table normalization can be added later without changing semantics. |
| D2 | **Amounts are `BIGINT` minor units in the database and `decimal.Decimal` in Go.** Validators must reject non-integer amounts. MVP supports only IDR (`minor_unit = 0`). | Exact arithmetic, compact indexes, and no `NUMERIC` precision surprises. |
| D3 | **Currency is stored on `accounts`, not `account_balances`.** Update `LockBalances` to select `a.currency`. | This removes two competing sources of truth for currency. |
| D4 | **Idempotency uses `UNIQUE INDEX (idempotency_key, COALESCE(idempotency_scope, ''))`** on `ledger_transactions`. | PostgreSQL treats `NULL` values as distinct in an ordinary unique index, so two requests with a null scope would otherwise both succeed. |
| D5 | **System accounts use `accounts.system_qualifier TEXT`**, such as settlement per gateway (`'bca'`), platform fees (`'platform'`), and escrow per currency (`'IDR'`). `GetSystemAccountID(type, qualifier)` looks up this column. | Processors already call `GetSystemAccountID(ctx, type, gateway)`. Reusing `pocket_code` would be a confusing hack; an explicit column is clearer. |
| D6 | **`ledger_entries.balance_after` is the account balance after the complete transaction**, not a running balance after each entry. Do not port the per-entry `chk_balance_math` constraint from `ledgernew.sql`. | `applyEntries` and `InsertEntries` write the same final value for every entry belonging to an account in one transaction. Changing this would touch the posting engine's core for little MVP value. Integrity is enforced by `validateBalanced` and the verification functions. |
| D7 | **Port the complete outbox lifecycle** (`pending/processing/published/failed/dead`, `retry_count`, `max_retries`, and automatic dead-lettering) from `ledgernew.sql`. | The relay worker needs this state machine; the existing six-column insert can rely on defaults for the remaining columns. |
| D8 | **Publish events through RabbitMQ via `pkg/messaging`**, using the `ledger.events` topic exchange and `event_type` as the routing key. | The infrastructure already includes the broker and DLQ support. |
| D9 | **Move `cmd/scheduler/scheduler_final.go` to `pkg/scheduler` as a library.** For MVP, run the outbox and verification workers inside the `cmd/server` process as goroutines, protected by the scheduler's distributed Redis lock. | Start with one modular-monolith process. A separate worker binary is a deployment decision, not a code-architecture decision, and can be introduced later because the worker is already a package. |
| D10 | **Use one generic API endpoint:** `POST /api/v1/ledger/transactions` with `type` in the request body, mapped directly to the processor registry, plus read endpoints. Admin transaction types (`adjustment_*`, `freeze_*`, and `reversal`) require the `admin` role. | The registry already exists; one handler per type would create 22 boilerplate handlers. |
| D11 | **Defer RLS to Phase 2.** For MVP, use a non-superuser application database role and `REVOKE CREATE ON SCHEMA public`. | The RLS design in `ledgernew.sql` adds local setup complexity and becomes more valuable once read-only or analytics connections exist. |
| D12 | **User management is outside the ledger scope.** MVP uses the existing JWT middleware and reads `user_id` from the claim. The `auth` module comes after the ledger MVP. | Keep the initial scope focused. |

## Code conventions

- Logging: use `log/slog`, inject the logger, and add request context through
  middleware.
- Errors: use sentinels in `apperror`, wrap with `%w`, and map to HTTP status in
  the transport layer (400 validation, 402 insufficient funds, 404 not found,
  409 idempotency conflict, 422 business error, 500 internal error).
- Mocks: use `//go:generate mockgen` from `go.uber.org/mock`; keep each
  `*_mock.go` file beside its interface.
- Tests: keep unit tests with their package. Integration tests use
  `testcontainers-go` and the `//go:build integration` tag.
- SQL: use parameterized raw SQL in repositories (`$1`); do not add an ORM.
- Router: use the Go 1.22 `net/http` pattern, for example
  `mux.HandleFunc("POST /path", ...)`; do not add a third-party router.

## MVP observability target

- Prometheus metrics (the dependency already exists):
  `ledger_transactions_total{type,status}`, `ledger_post_duration_seconds`,
  `outbox_pending_gauge`, `outbox_publish_failures_total`, and
  `ledger_verification_discrepancies_total` (alert when greater than zero).
  Expose them at `GET /metrics`.
- OTel tracing (the dependency already exists): create a span for each
  `Handle()` call and each publish. Simple manual instrumentation is enough;
  configure the exporter through environment variables.
