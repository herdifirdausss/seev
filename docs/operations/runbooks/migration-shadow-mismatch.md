# Migration: shadow read mismatch

**Trigger:** Reconciliation produces open mismatches, a `checksum_mismatch`
or `target_missing` alert fires, or the `Gates` snapshot shows
`shadow_success_ratio` below 99.99%.

**First safety rule:** Never approve a repair for a mismatch classified as
`shared_corruption` or `source_corruption`. A repair overwrites the target
with values derived from the source; if the source is also wrong the repair
makes things worse. Investigate the source first.

## 1. List open mismatches

```bash
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>/mismatches?status=open&limit=50" \
  | jq '.mismatches[] | {id, account_id, classification, severity, created_at}'
```

Severity and classification determine the response:

| Classification | Severity | Action |
|---|---|---|
| `target_missing` | critical | repair (if source is clean) |
| `backfill_missing` | critical | check backfill checkpoint, re-run |
| `value_mismatch` | high | repair or investigate dual-write gap |
| `checksum_mismatch` | high | repair |
| `target_stale` | medium | dual-write gap; may self-heal |
| `target_ahead` | warning | investigate unexpected write |
| `shared_corruption` | critical | **do not repair**; escalate |
| `source_corruption` | critical | **do not repair**; escalate to Ledger integrity runbook |

## 2. Confirm the source is authoritative

For each critical or high mismatch, verify the source (`account_balances`) is
consistent with the ledger entries:

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

If `v1_balance ≠ ledger_sum`, this is a source corruption. Do not proceed
with a repair. Follow [ledger-integrity-alert.md](ledger-integrity-alert.md).

## 3. Rollback reads while repairing (optional)

If the mismatch count is high enough to affect user-visible balances:

```bash
curl -s -X POST -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"basis_points":0,"reason":"mismatch investigation: reads rolled back to source","expected_version":<VERSION>}' \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>/read-percentage"
```

## 4. Request a repair (maker)

For each mismatch where the source is confirmed clean:

```bash
curl -s -X POST -H "Authorization: Bearer $MAKER_TOKEN" -H "Content-Type: application/json" \
  -d '{"reason":"confirmed source clean; requesting repair for <CLASSIFICATION>"}' \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>/mismatches/<MISMATCH_ID>/repair"
```

Note the returned `repair_id`.

## 5. Approve the repair (checker — different actor)

```bash
curl -s -X POST -H "Authorization: Bearer $CHECKER_TOKEN" -H "Content-Type: application/json" \
  -d '{"account_id":"<ACCOUNT_ID>","reason":"source verified clean; approving repair"}' \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>/repairs/<REPAIR_ID>/approve"
```

Confirm `status: verified` in the response. The target row has been
overwritten with a value derived from the canonical source.

## 6. Re-verify with reconciliation

After repairing all open mismatches, run a targeted reconciliation:

```bash
curl -s -X POST -H "Authorization: Bearer $CHECKER_TOKEN" -H "Content-Type: application/json" \
  -d '{"backup_fresh":false}' \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>/reconcile"
```

Confirm mismatches list is empty before re-ramping reads.

## Notes

- A stuck repair (worker crashed mid-repair) recovers automatically via
  `ReclaimStuckRepairs` on the next worker tick (lease expiry). Do not
  manually reset the repair status.
- `shadow_success_ratio` is a lagging gate — it reflects the ratio of
  shadow reads that matched within the last N samples. Small numbers of
  test-account mismatches in dev/staging will not show up here, but will
  accumulate in the mismatch table.
