# 11 — Phase 2b: Efficiency and Locking Redesign

> Prerequisite: read section C of [09-hardening-review.md](09-hardening-review.md).
> Every task here changes raw SQL, locking, or the posting pipeline. Before
> marking a task complete, run `go test -tags=integration -race ./...` in Docker
> and perform an HTTP smoke test. sqlmock unit tests alone are not sufficient:
> three real bugs (table aliases in `FOR UPDATE`, `CASE` placeholder casts, and
> `AccountIDs` sorting) were found only with PostgreSQL.

## T1 — Separate user locks from system-account delta application

**Problem:** `LockBalances` locks every account involved in a transaction,
including `settlement[gateway]` and `fee[gateway]` accounts with
`allow_negative=true`. Every `money_in`/`money_out` through the same gateway is
therefore serialized across the entire validation pipeline.

**Design (K6):**

- **User accounts** (`allow_negative=false`) remain under `SELECT ... FOR
  UPDATE`. The pre-read is required for a consistent overdraft check; the
  database floor constraint is only the final safety net.
- **System accounts** (`allow_negative=true`) are not locked or pre-read. Apply
  signed deltas near the end of the transaction with:

  ```sql
  UPDATE account_balances
  SET balance = balance + $delta, updated_at = now()
  WHERE account_id = $1
  RETURNING balance;
  ```

  Use the returned balance as `ledger_entries.balance_after`. The database
  constraint permits a negative balance for these accounts.

### Implementation

1. Add `AllowNegative bool` to `internal/ledger/model/account_balance.go`.
2. Add repository support:
   - `GetAccountFlags(ctx, tx, ids)` reads `allow_negative` without a lock.
     These flags are immutable after provisioning.
   - `ApplySystemDeltas(ctx, tx, deltas)` applies one atomic update per system
     account and returns the post-update balances. A transaction has only a
     small number of system accounts, so one round trip per account is fine.
   - Keep `LockBalances` generic. The service, not the repository, decides which
     IDs need locking.
   - Keep `UpdateBalances` for locked user accounts; it continues to replace
     balances from the values calculated in memory.
3. Update `Service.execTransfer`:
   - read flags and split IDs into user and system accounts;
   - lock only user accounts;
   - validate status and currency for both groups, but run overdraft checks only
     for user accounts;
   - keep processor validation and entry construction unchanged;
   - calculate user balances with `applyEntries`;
   - calculate system deltas as `Σcredit - Σdebit` per system account;
   - call `ApplySystemDeltas` before `InsertEntries`, so every entry has its
     final `balance_after`;
   - call `UpdateBalances` only for user accounts.
4. Record this exact order in the function comment and do not reorder it without
   reading this document:

   `idempotency gate → lock user accounts → structural validation → business validation → build entries → validate balanced → calculate user balances → apply system deltas → insert entries → update user projections → mark posted → outbox`

The atomic `balance + $delta` update is required to prevent lost updates when
concurrent `money_in` requests hit one settlement account.

### Tests and definition of done

- [ ] PostgreSQL integration test: run 50 concurrent `money_in` operations
      through one gateway, verify settlement balance equals `-Σamount`, and
      confirm `fn_verify_ledger_balance` is empty.
- [ ] Unit test: `LockBalances` receives only user accounts;
      `ApplySystemDeltas` receives system and fee accounts, in the right order.
- [ ] All existing processor tests remain green.
- [ ] Build, unit tests, integration tests, smoke tests, and both verification
      functions pass without discrepancies.

## T2 — Batch `InsertEntries`

**Problem:** `ledger_entry_repository.go` executes one `ExecContext` per entry
while locks are held. Follow the multi-row pattern in
`outbox_event_repository.go:89-119`.

1. Build one `INSERT INTO ledger_entries (...) VALUES (...)` statement with
   dynamic placeholders and arguments generated in entry order. Use a safety
   cap such as `maxEntriesBatch = 50`.
2. Do not sort entries: insertion order is irrelevant, while sorting would
   change the semantic ordering used by the posting service.

Tests:

- sqlmock: one entry creates one `VALUES` row; three entries create three rows
  with the correct argument count.
- Integration: a three-entry fee transaction inserts exactly three rows with
  correct balances.

Definition of done: one database round trip per transaction, with unit and
integration tests passing.

## T3 — Cache account resolution

**Problem:** `GetAccountID`, `GetSystemAccountID`, and `GetAccountCurrency` are
called repeatedly before each posting transaction.

1. Cache system-account IDs in `account_repository.go` with a `sync.Map` keyed
   by `type:qualifier`. They are immutable after seeding; a process restart is
   sufficient invalidation for the MVP.
2. Cache successful user-account lookups with a `sync.Map` keyed by owner,
   account type, and pocket. Do not cache not-found results, so provisioning
   does not require invalidation.
3. Record the limitation: a positive-only cache has unbounded growth. Add LRU
   eviction before scaling beyond roughly one million distinct accounts.

Tests must prove that repeated system and user lookups perform one database
query, and that a newly created pocket is immediately discoverable.

## T4 — UUIDv7 for insert-heavy tables

Replace random `uuid.New()` IDs with `uuid.Must(uuid.NewV7())` for
`ledger_transactions`, `ledger_entries`, and `outbox_events` after confirming
`github.com/google/uuid` is at least v1.6. Do not change account IDs: accounts
are created rarely, and UUIDv7 is still the standard 128-bit PostgreSQL type,
so no schema migration is required.

Add a test showing that sequentially generated IDs are lexicographically
non-decreasing, and keep all UUID-type regression tests green.

## T5 — PostgreSQL timeouts and small-host pool tuning

**Problem:** no statement, lock, or idle-in-transaction timeout exists, and the
25/25 pool default is too large for a small VPS.

Add configurable defaults:

- `PG_STATEMENT_TIMEOUT_MS=5000`
- `PG_LOCK_TIMEOUT_MS=2000`
- `PG_IDLE_IN_TX_TIMEOUT_MS=10000`

Apply them through PostgreSQL connection options or session settings. Lower the
defaults to `MaxOpenConns=10` and `MaxIdleConns=5`, while preserving environment
overrides. Document connection sizing and ensure the total connection budget is
shared across all services, workers, and migration tools.

Wrap each outbox repository call in a five-second `context.WithTimeout`, not
only the long-lived worker context.

Test a deliberately held user lock with a short lock timeout and confirm the
second request fails promptly. Keep integration tests tolerant of
testcontainer startup time by using test-only overrides rather than weakening
production defaults.

## T6 — Combine outbox gauge queries

Replace the two `CountByStatus` calls in the 15-second gauge loop with one
`CountAllStatuses(ctx)` query:

```sql
SELECT status, COUNT(*) FROM outbox_events GROUP BY status;
```

Set pending and dead gauges from the returned map. A unit regression test is
enough because this is a low-risk read-only change.

## Execution order

Run T5 first so later tests have timeout protection. T1 must precede T2 because
T2 uses the combined user/system balances introduced by T1. T3, T4, and T6 are
independent after T1.

## Phase 2b final verification

```bash
go build ./...
make lint
make test
go test -tags=integration -race ./...   # Docker must be running
```

Repeat the HTTP flows from [10](10-phase2a-security-gating.md) and confirm that
`fn_verify_ledger_balance()` and `v_account_balance_audit` report no
discrepancies.
