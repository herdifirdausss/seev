# Migration: instant read rollback

**Trigger:** Balance API returning anomalous values after a migration read ramp
step, or any on-call alert that implicates the v2 projection as the read
source.

**First safety rule:** Set `read_percentage_basis_points` to 0 before
investigating. This is a single API call that takes effect immediately with no
redeploy and no traffic interruption.

## 1. Confirm the migration and current read percentage

```bash
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  "http://ledger-internal/admin/migrations" | jq '.migrations[] | {id, name, state, read_percentage_basis_points}'
```

Note the migration `id` and `version`. Both are required for the rollback
request.

## 2. Set read percentage to 0 (instant rollback)

```bash
curl -s -X POST \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"basis_points":0,"reason":"on-call incident: anomalous balance values","expected_version":<VERSION>}' \
  "http://ledger-internal/admin/migrations/<MIGRATION_ID>/read-percentage"
```

A `200 OK` response with `read_percentage_basis_points: 0` confirms the
rollback. All subsequent `ReadBalance` calls are now served from the v1 source.

## 3. Verify the rollback

Call the balance API for any recently-affected account and confirm the value
matches the v1 projection:

```bash
# v1 balance (authoritative)
psql $LEDGER_DB -c "SELECT balance FROM account_balances WHERE account_id = '<ACCOUNT_ID>'"
# balance API (should now match v1)
curl -s -H "Authorization: Bearer $USER_TOKEN" \
  "http://ledger/api/v1/ledger/accounts/<ACCOUNT_ID>/balance"
```

## 4. Investigate before re-ramping

With reads back on v1, investigate without time pressure:

1. Load mismatches: `GET /admin/migrations/<ID>/mismatches?status=open`
2. If critical mismatches exist, follow [migration-shadow-mismatch.md](migration-shadow-mismatch.md).
3. Re-ramp only after mismatches are resolved and the `ChecksumMatches` gate
   is satisfied for the affected cohort.

## Notes

- Setting to 0 does not change the migration state; the migration remains in
  `ramping_read` or `target_primary`. It is safe to leave it at 0 while
  investigating.
- A ramp above 2500 bp (25%) requires a checker approval in the same request
  body (`"approve": true`). The rollback to 0 does not require a checker.
- If the anomaly appears to involve the dual-write path rather than reads,
  see [migration-strict-dual-write-failure.md](migration-strict-dual-write-failure.md).
