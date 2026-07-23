# 19 — Phase 3c: Scheduled Transactions and Accrual (S4, S5, S6)

Prerequisite: plans 17 and 18 are complete. This phase adds deterministic scheduled operations, batch disbursement, and daily interest accrual.

## Deterministic operation keys

Every automated operation must have a stable key so retries are safe:

```text
sched:<schedule_id>:<run_date>
batch:<batch_id>:<item_no>
accrue:<account_id>:<date>
```

The key is part of the operation's idempotency contract, not an implementation detail of the worker.

## T1 — Scheduled transactions (S4)

### Scope

The MVP supports scheduled `transfer_p2p` and `pocket` operations. The schedule service owns CRUD and due-item execution; the ledger remains the source of truth for posting and idempotency.

### Implementation

1. Add migration `000014_scheduled_transactions.up.sql` and its down migration, including grants and RLS.

2. Add the schedule service with:

   - create, update, pause, resume, and list operations;
   - `RunDue` for the worker;
   - deterministic keys based on schedule ID and run date;
   - explicit handling for missed schedules and failed executions.

3. Run the daily job at 00:30 Asia/Jakarta. Add public schedule endpoints for the supported user operations and an admin-only internal `RunNow` endpoint for operational recovery.

4. Keep execution idempotent. A retry must inspect the existing operation rather than create another ledger transaction.

### Result

The migration, service, worker, public endpoints, and internal admin trigger were implemented. Tests cover CRUD, due-item selection, pause/resume, deterministic keys, retries, and timezone boundaries. Unit, integration, and smoke tests passed.

## T2 — Batch disbursement (S5)

### Scope

Batch disbursement is an internal admin operation driven by CSV input. It is capped at 50,000 items per batch and resolves the settlement platform account by currency.

### Implementation

1. Add migration `000015_batch_disbursement.up.sql` and its down migration.

2. Add an admin-only CSV endpoint with validation for required columns, account identifiers, currency, amount, and duplicate item numbers.

3. Post each item through the disbursement processor using the deterministic `batch:<batch_id>:<item_no>` key. A batch may process at most 500 items per worker run.

4. Resume a partial batch by rerunning the worker. Do not add a separate resume endpoint; idempotency makes already completed items no-ops.

5. Produce a result report containing successful, failed, and skipped items. Any item error is recorded as failed and requires an explicit retry decision.

### Result

Batch creation, CSV validation, per-currency settlement lookup, bounded execution, retry-safe keys, and reporting were implemented. Tests cover malformed input, duplicate items, partial failures, reruns, and the 50,000-item cap. Integration, smoke, and chaos tests passed.

## T3 — Daily interest accrual (S6)

### Scope

Accrual is calculated from account-balance snapshots, not from a live balance that may change during the run. Each account receives at most one accrual per calendar day.

### Implementation

1. Add migration `000016_savings_config.up.sql` and its down migration, including savings configuration and currency-specific interest-expense accounts.

2. Add the `interest_accrue` processor. It uses the deterministic key `accrue:<account_id>:<date>` and posts the resulting entry through the normal ledger path.

3. Run the daily accrual job at 00:45 Asia/Jakarta, after the snapshot required for that date is available.

4. Add admin-gated settings endpoints and validate rate, account type, currency, and effective dates.

5. Isolate errors per account. One invalid account or failed posting must not prevent other accounts from being processed, and a rerun must not double-accrue a successful account.

### Result

Savings configuration, interest-expense accounts, snapshot-based calculation, the processor, scheduler, admin settings, and deterministic idempotency were implemented. Tests cover rate validation, snapshot selection, rounding, account isolation, timezone boundaries, and replay. Full unit, integration, race, and chaos verification passed.

## Final verification

```bash
go build ./...
go vet ./...
go vet -tags=integration ./...
make test
go test -tags=integration -race ./...
```

Also verify migration 000014–000016 up/down/up cycles, scheduled execution through the worker, batch reruns, and an accrual replay against the Docker stack.
