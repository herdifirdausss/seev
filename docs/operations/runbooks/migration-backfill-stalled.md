# Migration: backfill stalled

**Trigger:** Migration has been in `backfilling` state for longer than the
expected duration (estimate: rows_in_source / batch_size × tick_interval),
`backfill_completed_at` is not set, and no checkpoint progress is seen.

**First safety rule:** Do not restart the Ledger service to "unstick" the
backfill. The checkpoint lease protects against concurrent workers; a restart
without lease expiry will find the checkpoint locked and immediately exit.
Wait for lease expiry (default 5 minutes) or reclaim it explicitly.

## 1. Confirm the migration is stuck

```bash
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>" \
  | jq '{state, backfill_completed_at, version}'
```

If `state` is `backfilling` and `backfill_completed_at` is null, check the
checkpoint:

```sql
SELECT id, status, last_processed_key, lease_owner, lease_expires_at, updated_at
FROM data_migration_checkpoints
WHERE migration_id = '<MIGRATION_ID>' AND worker_kind = 'backfill';
```

A stall has one of three causes:

| Observation | Cause | Action |
|---|---|---|
| `lease_expires_at` in the future | worker is running | wait or check Ledger logs |
| `lease_expires_at` in the past | worker crashed mid-batch | step 2 |
| No checkpoint row | backfill never started | step 3 |
| `status = completed` | backfill done; state transition failed | step 4 |

## 2. Reclaim an expired lease

The `lifecycleWorker` calls `ReclaimExpiredCheckpointLease` on every tick.
If the service is running and the lease is expired, it should self-heal within
one tick (default 30 s). If not:

```bash
# Check Ledger logs for errors on the backfill tick
grep "backfill" ledger.log | tail -20
```

If the worker is looping on an error (schema issue, out-of-disk, connection
exhaustion), fix the root cause first. The lease will expire and the worker
will retry automatically once healthy.

If the service itself is down, the lease expires after `lease_duration`
(default 5 min) and the next startup resumes from `last_processed_key`.

## 3. Backfill never started (no checkpoint row)

The `lifecycleWorker` creates the checkpoint on the first `BackfillOnce` call.
If there is no row:

1. Confirm `DATA_MIGRATION_ENABLED=true` is set in the Ledger environment.
2. Confirm the migration is in `backfilling` state (not `target_ready` or
   earlier).
3. Restart the Ledger service with `DATA_MIGRATION_ENABLED=true` if it was
   missing.

## 4. Checkpoint complete but state transition failed

If the checkpoint `status = completed` but the migration is still in
`backfilling`:

```bash
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>"
```

The `lifecycleWorker` drives the auto-transition from `backfilling` to
`dual_write_shadow` after the empty page is detected. If the transition failed
(logged as `ERROR auto-transition backfilling→dual_write_shadow`), the error
is usually an optimistic concurrency conflict (another operator changed the
migration version concurrently). The worker will retry on the next tick.

If the worker is not retrying, manually advance the state (requires a
maker):

```bash
VER=$(curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>" | jq .version)
curl -s -X POST -H "Authorization: Bearer $MAKER_TOKEN" -H "Content-Type: application/json" \
  -d "{\"to_state\":\"dual_write_shadow\",\"reason\":\"manual advance: backfill checkpoint completed\",\"expected_version\":$VER}" \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>/transition"
```

## 5. Slow but progressing

If the checkpoint `updated_at` is recent but progress is slow:

- Increase `DATA_MIGRATION_BACKFILL_BATCH_SIZE` in the Ledger environment and
  restart. Default is 500 rows per tick; safe range is 100–5000 depending on
  row size and replication lag tolerance.
- Check that source table reads are not blocked by a long-running OLTP
  transaction holding a lock on `account_balances`.
- Monitor `replication_lag` on the standby replica — backfill reads are
  routed to the primary by default; heavy scans can increase lag.

## Notes

- The backfill is idempotent. Re-running from a given `last_processed_key`
  will re-process those rows, but the version-safe upsert prevents regression.
- Do not `TRUNCATE account_balances_v2` to restart the backfill manually
  unless the migration is first rolled back to `draft` state (which requires
  a maker+checker `rolling_back` → `rolled_back` transition sequence).
