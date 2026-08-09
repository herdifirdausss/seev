# Data Migration Runbook

> [Operations](../README.md) · [Runbooks](README.md)

Covers the zero-downtime data migration platform — 17-state machine, dual-write,
shadow reads, and read ramp. See [data migration reference](../../reference/data-migration.md)
for the platform design; see [ADR-0004](../../reference/decisions/0004-zero-downtime-migration-platform.md)
for the rationale.

**Global safety rules:**
1. Confirm the failure source is migration-related before any state change.
2. Never approve a repair for `shared_corruption` or `source_corruption` — investigate
   the source first.
3. Never `TRUNCATE account_balances_v2` unless the migration is first rolled back to
   `draft` state (requiring a maker+checker `rolling_back` → `rolled_back` sequence).

---

## Backfill stalled

**Trigger:** Migration has been in `backfilling` state for longer than expected
(`rows_in_source / batch_size × tick_interval`), `backfill_completed_at` is null, and
no checkpoint progress is seen.

**Safety rule:** Do not restart the Ledger service to "unstick" the backfill. The
checkpoint lease protects against concurrent workers; a restart without lease expiry
will find the checkpoint locked and immediately exit. Wait for lease expiry (default
5 minutes) or reclaim it explicitly.

### Confirm the stall

```bash
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>" \
  | jq '{state, backfill_completed_at, version}'
```

```sql
SELECT id, status, last_processed_key, lease_owner, lease_expires_at, updated_at
FROM data_migration_checkpoints
WHERE migration_id = '<MIGRATION_ID>' AND worker_kind = 'backfill';
```

| Observation | Cause | Action |
|---|---|---|
| `lease_expires_at` in the future | worker is running | wait or check Ledger logs |
| `lease_expires_at` in the past | worker crashed mid-batch | reclaim the lease |
| No checkpoint row | backfill never started | confirm env variable |
| `status = completed` | backfill done; state transition failed | manually advance |

### Reclaim an expired lease

The `lifecycleWorker` calls `ReclaimExpiredCheckpointLease` on every tick — if the
service is running and the lease is expired, it self-heals within one tick (default 30s).
If not:

```bash
grep "backfill" ledger.log | tail -20
```

If the worker is looping on an error (schema issue, out-of-disk, connection exhaustion),
fix the root cause first. The lease expires automatically and the worker retries.

If the service itself is down, the lease expires after `lease_duration` (default 5 min)
and the next startup resumes from `last_processed_key`.

### Backfill never started (no checkpoint row)

1. Confirm `DATA_MIGRATION_ENABLED=true` is set in the Ledger environment.
2. Confirm the migration is in `backfilling` state (not `target_ready` or earlier).
3. Restart the Ledger service with `DATA_MIGRATION_ENABLED=true` if it was missing.

### Checkpoint complete but state transition failed

If `status = completed` but migration remains in `backfilling`:

The `lifecycleWorker` drives the auto-transition from `backfilling` to
`dual_write_shadow` after detecting an empty page. If the transition fails (logged as
`ERROR auto-transition backfilling→dual_write_shadow`), it's usually an optimistic
concurrency conflict and the worker retries on the next tick.

If not retrying, manually advance (requires a maker):

```bash
VER=$(curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>" | jq .version)
curl -s -X POST \
  -H "Authorization: Bearer $MAKER_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"to_state\":\"dual_write_shadow\",\"reason\":\"manual advance: backfill checkpoint completed\",\"expected_version\":$VER}" \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>/transition"
```

### Slow but progressing

- Increase `DATA_MIGRATION_BACKFILL_BATCH_SIZE` (default 500 rows; safe range 100–5000
  depending on row size and replication lag tolerance).
- Check for long-running OLTP transactions holding locks on `account_balances`.
- Monitor replication lag on the standby replica — heavy scans can increase lag.

The backfill is idempotent. Re-running from a given `last_processed_key` re-processes
those rows, but the version-safe upsert prevents regression.

---

## Instant read rollback

**Trigger:** Balance API returning anomalous values after a migration read ramp step, or
any on-call alert that implicates the v2 projection as the read source.

**Safety rule:** Set `read_percentage_basis_points` to 0 before investigating. This is a
single API call that takes effect immediately with no redeploy and no traffic interruption.

### Confirm the migration and current read percentage

```bash
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  "http://ledger-internal/admin/migrations" \
  | jq '.migrations[] | {id, name, state, read_percentage_basis_points}'
```

Note the migration `id` and `version` — both are required for the rollback request.

### Set read percentage to 0

```bash
curl -s -X POST \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"basis_points":0,"reason":"on-call incident: anomalous balance values","expected_version":<VERSION>}' \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>/read-percentage"
```

A `200 OK` response with `read_percentage_basis_points: 0` confirms the rollback. All
subsequent `ReadBalance` calls are now served from the v1 source.

### Verify the rollback

```bash
# v1 balance (authoritative)
psql $LEDGER_DB -c "SELECT balance FROM account_balances WHERE account_id = '<ACCOUNT_ID>'"
# balance API (should now match v1)
curl -s -H "Authorization: Bearer $USER_TOKEN" \
  "http://ledger/api/v1/ledger/accounts/<ACCOUNT_ID>/balance"
```

### Investigate before re-ramping

With reads back on v1, investigate without time pressure:

1. List open mismatches: `GET /admin/migrations/<ID>/mismatches?status=open`
2. If critical mismatches exist, follow the [shadow mismatch](#shadow-mismatch) procedure.
3. Re-ramp only after mismatches are resolved and the `ChecksumMatches` gate is satisfied
   for the affected cohort.

Setting to 0 does not change migration state; the migration remains in `ramping_read` or
`target_primary`. A ramp above 2500 bp (25%) requires a checker approval in the request
body (`"approve": true`). The rollback to 0 does not require a checker.

---

## Shadow mismatch

**Trigger:** Reconciliation produces open mismatches, a `checksum_mismatch` or
`target_missing` alert fires, or `Gates` snapshot shows `shadow_success_ratio` below 99.99%.

**Safety rule:** Never approve a repair for `shared_corruption` or `source_corruption` —
a repair overwrites the target with values derived from the source; if the source is also
wrong, the repair makes things worse. Investigate the source first.

### List open mismatches

```bash
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>/mismatches?status=open&limit=50" \
  | jq '.mismatches[] | {id, account_id, classification, severity, created_at}'
```

| Classification | Severity | Action |
|---|---|---|
| `target_missing` | critical | repair (if source is clean) |
| `backfill_missing` | critical | check backfill checkpoint, re-run |
| `value_mismatch` | high | repair or investigate dual-write gap |
| `checksum_mismatch` | high | repair |
| `target_stale` | medium | dual-write gap; may self-heal |
| `target_ahead` | warning | investigate unexpected write |
| `shared_corruption` | critical | **do not repair** — escalate |
| `source_corruption` | critical | **do not repair** — escalate to [ledger integrity](ledger-integrity-alert.md) |

### Confirm the source is authoritative

```sql
SELECT
  ab.account_id,
  ab.balance AS v1_balance,
  SUM(le.amount) AS ledger_sum
FROM account_balances ab
JOIN ledger_entries le ON le.account_id = ab.account_id
WHERE ab.account_id = '<ACCOUNT_ID>'
GROUP BY ab.account_id, ab.balance;
```

If `v1_balance ≠ ledger_sum`, this is source corruption — do not repair.
Follow [ledger-integrity-alert.md](ledger-integrity-alert.md).

### Rollback reads while repairing (optional)

If mismatch count is high enough to affect user-visible balances:

```bash
curl -s -X POST \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"basis_points":0,"reason":"mismatch investigation: reads rolled back to source","expected_version":<VERSION>}' \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>/read-percentage"
```

### Request a repair (maker)

```bash
curl -s -X POST \
  -H "Authorization: Bearer $MAKER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"reason":"confirmed source clean; requesting repair for <CLASSIFICATION>"}' \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>/mismatches/<MISMATCH_ID>/repair"
```

Note the returned `repair_id`.

### Approve the repair (checker — different actor)

```bash
curl -s -X POST \
  -H "Authorization: Bearer $CHECKER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"account_id":"<ACCOUNT_ID>","reason":"source verified clean; approving repair"}' \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>/repairs/<REPAIR_ID>/approve"
```

Confirm `status: verified` in the response.

### Re-verify with reconciliation

After repairing all open mismatches:

```bash
curl -s -X POST \
  -H "Authorization: Bearer $CHECKER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"backup_fresh":false}' \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>/reconcile"
```

Confirm the mismatch list is empty before re-ramping reads.

A stuck repair (worker crashed mid-repair) recovers automatically via
`ReclaimStuckRepairs` on the next tick (lease expiry). Do not manually reset repair status.

---

## Strict dual-write failure

**Trigger:** Postings returning 5xx errors at `ShadowRead` or later states. In strict
mode (`StrictDualWrite=true`) a v2 target write failure causes the whole posting
transaction to roll back. This is deliberate — v1 is never left ahead of v2 without
alerting.

**Safety rule:** Confirm the failure is from the migration target write before switching
dual-write mode. A non-migration database failure looks the same at the API surface.

### Confirm the failure source

```bash
grep "WriteForPosting" ledger.log | grep "error"
```

Look for `strict_dual_write_failure` event type. If present, the target write is the
failure source. If absent, this is not a migration issue — escalate to the database runbook.

Check migration state:

```bash
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>" \
  | jq '{state, strict_dual_write, version}'
```

Confirm `strict_dual_write: true`. If already `false`, the failure is something else.

### Switch to shadow mode

Shadow mode absorbs target write errors and records a gap instead of rejecting the
posting. This is a safety degradation but restores posting availability:

```bash
curl -s -X POST \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"strict":false,"reason":"on-call incident: posting failures; switching to shadow","expected_version":<VERSION>}' \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>/dual-write"
```

Confirm `200 OK` and `strict_dual_write: false`. Postings should resume immediately.

### Diagnose the target write failure

With postings flowing (shadow mode), investigate the root cause:

```sql
SELECT * FROM shadow_write_gaps
WHERE migration_id = '<ID>'
ORDER BY created_at DESC LIMIT 20;
```

Inspect whether the v2 schema or indexes are the cause (constraint violation, storage
pressure, lock waits). If the v2 table is corrupted or inaccessible, engage the database
failover runbook.

### Restore strict mode when the target is healthy

```bash
curl -s -X POST \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"strict":true,"reason":"target write healthy; restoring strict mode","expected_version":<VERSION>}' \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>/dual-write"
```

Then run a reconciliation pass to confirm any gaps left during shadow mode are accounted for:

```bash
curl -s -X POST \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"backup_fresh":false}' \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>/reconcile"
```

**Notes:**
- Shadow mode is not a permanent safe state — gaps accumulate and the
  `shadow_success_ratio ≥ 99.99%` gate will block the next forward transition if gaps are
  too frequent.
- Do not advance migration state while in shadow mode after a failure without reviewing
  and resolving open mismatches first.
- If gap accumulation is acceptable short-term and the target table cannot be repaired
  quickly, consider pausing the migration (`POST /admin/migrations/<ID>/pause`) and
  rolling back read percentage to 0 while the root cause is fixed.
