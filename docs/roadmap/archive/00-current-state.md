# 00 — Current Repository Audit

Snapshot from July 2026, branch `main`, commit `ac7d617`. Module path:
`github.com/herdifirdausss/seev`, Go 1.25.

## Existing foundations

### Infrastructure (`pkg/`)

- `pkg/database` — pgx pool in stdlib mode and the `WithTx` helper. The ledger
  service uses it through the `DatabaseSQL` interface.
- `pkg/cache` — Redis client and sliding-window rate limiter with an in-memory
  fallback.
- `pkg/messaging` — RabbitMQ broker, publisher, consumer, topology, pool,
  metrics, automatic reconnect, and DLQ support.
- `pkg/middleware` — request IDs, logging, recovery, CORS, rate limiting,
  security headers, timeouts, JWT authentication (HS256), and `WithRole`.
- `pkg/logger` — structured `slog` logging with sensitive-data masking.
- `pkg/response` — standard JSON envelope.
- `pkg/generalutil`, `pkg/generalerror` — SQL argument helpers, metadata
  parsing, and PostgreSQL error classification (retryable errors and duplicate
  keys).
- `internal/config` — environment configuration with strict validation.
- `internal/server` — graceful shutdown.
- `internal/handler` — Go 1.22 `net/http` router with health and readiness
  probes.

### Ledger module (`internal/ledger/`)

- **`service/handle/service.go`** — the posting engine. Its flow has been
  through several review iterations:
  1. Insert the idempotency record inside a savepoint so a unique violation
     does not abort the transaction.
  2. Lock all accounts with `FOR UPDATE` in deterministic
     `ORDER BY account_id` order to prevent deadlocks.
  3. Run structural validation: account existence, active status, and currency
     compatibility.
  4. Run processor-specific business validation. On failure, commit the
     transaction header with `status='failed'` for the audit trail instead of
     rolling it back.
  5. Build entries, verify that debits equal credits, and calculate the new
     balances once from a single source of truth.
  6. Insert entries, update the balance projection, mark the transaction as
     posted, and insert the outbox event in one database transaction.
  7. Retry retryable errors such as serialization failures and deadlocks with
     jitter.
- **`processors/`** — 22 transaction processors behind the `TxProcessor`
  interface and registry. They cover money in/out, the six withdrawal
  lifecycle types, P2P and pocket transfers, refunds, fees, chargebacks,
  escrow (3), freezes (3), adjustments (2), and reversals. Inline fees are
  supported atomically as three entries in one transaction. Some processors
  already have tests.
- **`repository/`** — SQL interfaces and implementations for balances
  (locking/updating), entries (insertion), transactions
  (insertion/status/idempotency lookup), and outbox batch insertion. All have
  `mockgen` mocks.
- **`apperror/`** — sentinel errors such as `ErrAlreadyPosted` and
  `ErrInsufficientBalance`.
- **`constant/`** — direction and account-type/status codes represented as
  `TEXT` strings such as `"debit"`, `"cash"`, and `"active"`.

## Problems to resolve

### M1 — Critical: no database schema matches the code

The code writes to these tables, based on the SQL used by the repositories and
services:

| Table | Columns used by the code |
|---|---|
| `ledger_transactions` | `id, idempotency_key, idempotency_scope, type, status, amount, currency, source_account_id, destination_account_id, error_message, created_at, updated_at`; status, type, and currency are `TEXT` |
| `ledger_entries` | `id, transaction_id, account_id, direction, amount, balance_after, note, created_at`; direction is `TEXT` with values such as `'debit'` and `'credit'` |
| `account_balances` | `account_id, balance, currency`; `LockBalances` also selects `a.status` and `a.type` from the `accounts` join |
| `accounts` | `id` and `status`/`type` as `TEXT` codes |
| `outbox_events` | Only `id, aggregate_type, aggregate_id, event_type, payload, created_at` are inserted; every other column needs a default |

None of the existing schema files matches the code:

- `migrations/001.sql` — an early draft containing invalid pseudo-SQL, including
  a detached `external_ref UNIQUE` line.
- `migrations/002.sql` — closest to the current code, but it uses
  `transaction_id` instead of `idempotency_key` and is missing many columns.
- `migrations/auth.sql` — empty (0 bytes).
- `migrations/ledger.sql` and `migrations/ledgernew.sql` — a different design
  based on `balance_transactions`, `SMALLINT` lookup foreign keys
  (`transaction_types`, `entry_directions`, and others), fees, and
  `balance_before`. The code does not write to this shape. These files do
  contain useful safeguards that should be ported: immutability triggers, the
  outbox lifecycle (`pending/processing/published/failed/dead` with automatic
  dead-lettering), `fn_verify_ledger_balance`,
  `fn_verify_account_balance`, the audit view, and RLS.
- `internal/ledger/001.sql` — another copy in the wrong location (it belongs
  under `migrations/`); its view references columns that do not exist, such as
  `a.user_id` and `a.is_system`.

**Locked decision (see 01):** the code's data model wins. Create one canonical
migration set that follows the tables and columns used by the code, port the
useful safeguards from `ledgernew.sql`, and archive the old schema files.

### M2 — Critical: `AccountRepository` has no implementation

`internal/ledger/repository/account_repository.go` contains only the
`GetAccountID`, `GetPocketAccountID`, `GetAccountCurrency`, and
`GetSystemAccountID` interfaces plus a mock. Every processor depends on these
methods. Without a SQL implementation, no processor can run outside unit tests.

### M3 — The ledger module is not wired into the application

- `cmd/server/main.go` wires only the database, Redis, and RabbitMQ into the
  router. It does not construct `ledger.Service`, the processor registry, or
  the repositories.
- `internal/handler/router.go` contains only 501 placeholders for
  `/auth/login`, `/users/me`, and similar routes. There is no ledger endpoint.
- There is no user-account provisioning mechanism. The schema comment says
  that accounts are created explicitly by a service, but that service does not
  exist yet.

### M4 — Misplaced or dead files

- `cmd/scheduler/scheduler_final.go` — a 1,221-line cron library with extended
  cron syntax, a distributed Redis lock, and a heap scheduler, currently under
  `cmd/` as `package main`. Move it to `pkg/scheduler`; move its 582-line and
  727-line test files with it.
- `cmd/rabbitmq/rabbitmq.go` — a 204-line example/demo that duplicates
  `pkg/messaging`; remove it.
- `internal/ledger/service/migration.go` — defines the unused `SMALLINT`
  enums `AccountCash = 1` and `CurrencyIDR = 1` from the `ledgernew.sql`
  design. Remove it, or rewrite it only if it contains provisioning logic
  worth preserving after inspection.
- `internal/ledger/service/transfer/transfer_service.go` — check whether this
  obsolete posting engine has any references with
  `grep -rn "service/transfer"`. If not, remove it.
- `internal/ledger/001.sql` — remove after the canonical migrations exist.
- `internal/handler/dependencties.go` — rename the misspelled file to
  `dependencies.go`.

### M5 — The README is inaccurate

The README describes paths that do not exist, including `internal/database`,
`internal/middleware`, `docker-compose.yml`, `.air.toml`, and
`.env.example`. Some belong under `pkg/`, and some do not exist at all.
Rewrite the README after Phase 0.

### M6 — Duplicate `Command` types

`processors.Command` and `model.Command` are identical. Keep
`processors.Command` as the single source of truth and remove `model.Command`
after checking its users with `grep -rn "model.Command"`.

## Addendum — Build bugs found and fixed during Phase 0

The initial audit did not run `go build ./...`; it only inspected the code.
During Phase 0, the repository failed to compile because of several additional
bugs. All were fixed. They are recorded here so that Phase 1a/1b does not make
incorrect assumptions about the interfaces.

1. **`TxProcessor.ValidateCommand` was declared in the interface, but none of
   the 22 processors implemented it, and `Service.Handle()` never called it.**
   A no-op `ValidateCommand(_ context.Context, _ Command) error { return nil }`
   was added to every processor and is called immediately after
   `registry.Get()`, before `ResolveAccounts()`. A processor that needs real
   pre-database metadata validation can implement it there.
2. **`internal/ledger/processors/reversal.go` called missing or incompatible
   transaction-repository methods.** The following changes were made:
   - `TransactionRepository.GetAccountIDs` now accepts `(ctx, transactionID)`
     and reads through the `DatabaseSQL` stored in the repository.
   - `TransactionRepository.GetStatus(ctx, tx, transactionID)` was added for
     validation inside the posting transaction.
   - `reversal.go` now calls `UpdateStatus(ctx, tx, id, "reversed", nil)`
     instead of the nonexistent `MarkReversed`.
   - The Phase 1 schema must allow `ledger_transactions.status = 'reversed'`
     in addition to `pending`, `posted`, and `failed`.
   - `NewTransactionRepository()` now accepts a `database.DatabaseSQL`; update
     its callers during Phase 1b wiring.
   - The old `GetAccountIDs` query did not pass `transactionID` as its `$1`
     argument. This was fixed; the query could fail against real PostgreSQL
     even when some mocks passed.
   - The `TransactionRepository` mock was regenerated with `mockgen`.
3. **`internal/ledger/service/transfer/transfer_service.go` was removed.** It
   was an unused prototype already replaced by `service/handle/service.go` and
   did not compile because it referenced a missing `domain` package and missing
   repository methods (`InsertPending`, `MarkFailed`, and `MarkPosted`).
4. **The handle-service test was updated.**
   `TestHandle_ResolveError_Propagated` now expects `ValidateCommand`, and
   `TestHandle_ValidateCommandError_Propagated` verifies that
   `ResolveAccounts` is not called when validation rejects a command.

After this addendum, `go build ./...`, `go vet ./...`, and
`go test ./... -race` all passed.

## Quick verification commands

```bash
# Tables actually used by the code:
grep -rhoE 'FROM [a-z_]+|INTO [a-z_]+|UPDATE [a-z_]+' internal/ --include="*.go" | sort -u

# Users of files suspected to be dead:
grep -rn "service/transfer\|model.Command\|cmd/rabbitmq" --include="*.go" .

# Current test status:
make test
```
