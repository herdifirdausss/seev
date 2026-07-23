# 17 — Phase 3a: Policy Layer and Recovery Drill (S1, S9)

Prerequisite: plans 10–16 are complete. This document implements the independent S1 and S9 items from [plan 13](13-p1-backlog-review.md). They may be developed in parallel, although S9 is the smaller task.

The verification rules from plan 09 apply in full. S1 changes the public posting path and adds a table, so it requires integration and smoke tests. S9 changes the balance projection, so it must be verified against real PostgreSQL data produced by the posting engine rather than synthetic fixtures alone.

## T1 — Limits and velocity policy layer (S1)

### Objective

Add opt-in, per-user and per-transaction-type limits that are more precise than the global `LEDGER_MAX_AMOUNT_PER_TX` safety ceiling. The policy layer must support limits such as:

- `transfer_p2p`: a maximum of 5 million per transaction, 20 million per day, and 100 million per month per user;
- `withdraw_initiate`: a maximum of three attempts per day per user.

### Locked design

- Add a new `internal/policy` module. The ledger module must not import or depend on it. Policy checks run in the public transport layer before `ledger.Post`.
- Use the existing cache abstraction pattern: Redis when `REDIS_ENABLED=true`, with an in-memory fallback when it is false. Do not introduce a third fallback mechanism.
- Store policy configuration in PostgreSQL and cache it in-process for 60 seconds. Operators must be able to change limits without deploying the service.
- Use Asia/Jakarta for daily and monthly calendar windows so policy boundaries match snapshots and statements.
- A missing policy row means that the transaction type is unrestricted. Policy is opt-in; default-deny would unintentionally block internal transaction types that have not been configured.
- A policy rejection maps to HTTP 422 and `POLICY_LIMIT_EXCEEDED`. HTTP 429 remains reserved for infrastructure rate limiting.

### Implementation

1. Add migration `000010_policy_limits.up.sql` and its down migration. The table contains optional `max_per_tx`, `max_daily_amount`, `max_daily_count`, and `max_monthly_amount` values. `NULL` means that dimension is not limited. A partial unique index enforces one default row per transaction type when `user_id IS NULL`. The migration also adds the `app_service` and `app_readonly` grants and enables/fixes the table's RLS policies.

2. Add `internal/policy` with an engine that:

   - resolves a user-specific row before the default row;
   - checks transaction amount, daily amount/count, and monthly amount;
   - records usage only after a successful ledger post;
   - documents that concurrent requests may both pass the check before recording usage. This is an approximate business control, not a monetary invariant; ledger invariants remain authoritative.

3. Extend `pkg/cache` with a `Counter` interface and Redis and memory implementations. Store amount and count in separate keys, for example:

   ```text
   pol:<userID>:<txType>:d:<YYYY-MM-DD>:amt
   pol:<userID>:<txType>:d:<YYYY-MM-DD>:cnt
   pol:<userID>:<txType>:m:<YYYY-MM>:amt
   pol:<userID>:<txType>:m:<YYYY-MM>:cnt
   ```

   Daily keys use a 48-hour TTL and monthly keys use a 35-day TTL. Redis and memory selection happens in the composition root, following the existing lock and rate-limiter patterns.

4. Cache effective policy configuration for 60 seconds. Time-based expiry is sufficient; no pub/sub invalidation mechanism is needed.

5. Wire the policy checker into the public transaction router after request validation and before `svc.Post`. Record usage only when posting succeeds. The trusted internal router deliberately receives a nil policy checker and is not subject to user limits.

6. Add admin-gated internal endpoints:

   - `PUT /admin/policy/limits` — upsert a limit by `user_id` and transaction type;
   - `GET /admin/policy/limits?type=&user_id=` — list or inspect limits.

   Disable a policy with `enabled=false`; do not delete it, so the audit trail remains intact.

### Required tests

- Table-driven unit tests for every limit dimension, user overrides, disabled policies, and unrestricted transaction types.
- Memory-counter TTL and concurrent increment tests under `-race`.
- PostgreSQL integration tests for repository round trips, override resolution, cache expiry, and end-to-end daily velocity.
- Transport integration tests proving that rejected transactions return 422, while failed ledger posts do not consume quota.
- A full-stack smoke test that configures a limit through the admin endpoint and verifies both an allowed and a rejected transfer.

### Definition of done

- [x] `internal/ledger` has no dependency on `internal/policy`.
- [x] The in-memory fallback works with `REDIS_ENABLED=false`.
- [x] Migration up/down cycles pass, and the new table carries its own grants and RLS policies.

### Result (2026-07-12)

The policy layer was implemented with a deliberately small structural interface. `Check` returns `(allowed bool, rule string, detail string, error)` instead of sharing a `Decision` type between packages. This keeps the ledger and policy modules independent while still allowing `internal/policy.Engine` to satisfy the transport interface.

Implemented components include:

- `migrations/000010_policy_limits.{up,down}.sql` with per-user overrides, default rows, grants, and RLS;
- `pkg/cache/counter.go` with `Counter`, `RedisCounter`, and `MemoryCounter`;
- policy repository, engine, cache, and admin HTTP handlers;
- public-router wiring that checks before posting and records only after success;
- an explicit fail-open path for policy repository/counter infrastructure errors, while `max_per_tx` remains enforced because it does not depend on a counter;
- unit, transport, PostgreSQL integration, and full-stack smoke tests.

The integration tests found and fixed a real default-row upsert bug: a normal unique constraint does not treat two `NULL` values as equal in PostgreSQL. Default rows now use a partial-index conflict target, while user-specific rows use the regular `(user_id, transaction_type)` conflict target.

The smoke test configured `max_per_tx=5000`, verified that 5000 succeeded and 5001 returned 422, then verified a daily amount limit and the Redis counters. The full chaos suite was rerun after policy wiring and all four scenarios passed.

Build, vet, unit tests, race tests, integration tests, and migration up/down/up verification passed.

## T2 — Point-in-time projection rebuild and disaster-recovery drill (S9)

### Objective

Prove empirically that `account_balances` can be rebuilt from `ledger_entries`, and document a tested restore procedure with a measured recovery time objective (RTO).

### Locked design

`account_balances.allow_negative` is configuration, not a value derived from ledger entries. Therefore, rebuilding must update balances in place from an aggregate; it must not truncate and recreate the table, because that would destroy account configuration.

### Implementation

1. Add `scripts/rebuild-projection.sh` and the shared SQL file `scripts/sql/rebuild_projection.sql`. The script:

   - refuses to run while the application health endpoint is live;
   - captures a pre-rebuild balance snapshot;
   - updates all balances with one set-based statement and resets accounts with no entries to zero;
   - runs under the migration/owner role as required by the database RLS design;
   - verifies every affected account with `fn_verify_account_balance` and reports pre/post differences;
   - exits non-zero if the rebuilt projection is inconsistent.

2. Add [the restore-drill runbook](../../operations/runbooks/dr-restore-drill.md). It covers backup restore, idempotent migration, projection rebuild, verification, service startup, smoke posting, and recording each drill's timing.

3. Add `TestSchemaContract_RebuildProjection`. The test posts real transactions, deliberately corrupts a balance, runs the same SQL file used by the script, and verifies that the balance is restored, `allow_negative` is unchanged, and the audit verifier is clean. A second test proves that a consistent projection is a no-op.

4. Run the complete backup, destroy, restore, rebuild, verify, and smoke-test drill against the local Docker stack.

### Definition of done

- [x] The rebuild script and shared SQL are idempotent and return meaningful exit codes.
- [x] The runbook records the first real drill and its RTO.
- [x] Integration tests explicitly prove that `allow_negative` survives the rebuild.

### Result (2026-07-12)

The real Docker drill passed. It seeded data through the posting engine, created a `pg_dump`, simulated a database loss, restored it, ran migrations, rebuilt the projection, verified the ledger, restarted the server, and completed a smoke post with the expected balances.

The drill exposed a real portability bug: `docker exec psql -f <host-path>` resolves the file path inside the container. The script was corrected to pipe the shared SQL file through `docker exec -i`, so the host and container paths cannot diverge.

The integration suite verified both the corrupted and clean cases, including preservation of `allow_negative`. The negative test also confirmed that the script refuses to run while the application is up. The recovery timing is recorded in the runbook.

### Final verification

```bash
go build ./...
go vet ./...
go vet -tags=integration ./...
make test
go test -tags=integration -race ./...
./scripts/rebuild-projection.sh
```
