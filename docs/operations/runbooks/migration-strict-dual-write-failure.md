# Migration: strict dual-write failure

**Trigger:** Postings returning 5xx errors at `ShadowRead` or later states.
In strict mode (`StrictDualWrite=true`) a v2 target write failure causes the
whole posting transaction to roll back. This is deliberate; the v1 projection
is never left ahead of v2 without alerting.

**First safety rule:** Confirm the failure is from the migration target write
before switching dual-write mode. A non-migration database failure looks the
same at the API surface.

## 1. Confirm the failure source

Check Ledger structured logs for the posting request:

```
grep "WriteForPosting" ledger.log | grep "error"
```

Look for `strict_dual_write_failure` event type. If present, the target write
is the failure source. If absent, this is not a migration issue — escalate to
the database runbook.

## 2. Check migration state

```bash
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>" \
  | jq '{state, strict_dual_write, version}'
```

Confirm `strict_dual_write: true`. If it is already `false`, the failure is
coming from something else.

## 3. Switch to shadow mode

Shadow mode absorbs target write errors and records a gap instead of
rejecting the posting. This is a safety degradation but restores posting
availability.

```bash
curl -s -X POST \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"strict":false,"reason":"on-call incident: posting failures; switching to shadow","expected_version":<VERSION>}' \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>/dual-write"
```

Confirm `200 OK` and `strict_dual_write: false`. Postings should resume
immediately. The migration drops from `ShadowRead` effective behavior back to
shadow-mode tolerance.

## 4. Diagnose the target write failure

With postings flowing (shadow mode), investigate the root cause:

1. Check `shadow_write_gaps` for recent gap records:
   ```sql
   SELECT * FROM shadow_write_gaps
   WHERE migration_id = '<ID>'
   ORDER BY created_at DESC LIMIT 20;
   ```
2. Inspect whether the v2 schema or indexes are the cause (constraint
   violation, storage, lock waits).
3. If the v2 table is corrupted or inaccessible, engage the database
   failover runbook.

## 5. Restore strict mode when the target is healthy

Once the target write succeeds reliably:

```bash
curl -s -X POST \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"strict":true,"reason":"target write healthy; restoring strict mode","expected_version":<VERSION>}' \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>/dual-write"
```

Then run a reconciliation pass to confirm any gaps left during shadow mode are
accounted for:

```bash
curl -s -X POST \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"backup_fresh":false}' \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>/reconcile"
```

## Notes

- Shadow mode is not a permanent safe state — gaps accumulate and the
  reconciliation gate (`shadow_success_ratio ≥ 99.99%`) will block the next
  forward transition if gaps are too frequent.
- Do not advance the migration state while in shadow mode after a failure
  event without reviewing and resolving open mismatches first.
- If posting volume means gap accumulation is acceptable short-term and the
  target table cannot be repaired quickly, consider pausing the migration
  (`POST /admin/migrations/<ID>/pause`) and rolling back the read percentage
  to 0 while the root cause is fixed.
