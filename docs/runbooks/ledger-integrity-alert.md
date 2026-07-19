# Runbook: Ledger Integrity Alert

Triggered by: `internal/ledger/worker/verifier.go` — `checkTrialBalance` (hourly) or `checkProjectionAudit` (daily), delivered via `ALERT_WEBHOOK_URL` (docs/plan/12 Task T4) if configured, and always logged at `ERROR` + counted in the `ledger_verification_discrepancies_total` Prometheus metric regardless of whether a webhook is configured.

Also reachable via Grafana unified alerting (docs/plan/43 Task T5, folder "Seev"): `seev-op-ledger-verifier-discrepancy`, `seev-op-outbox-pending-backlog`, `seev-op-outbox-dead-events`, and the three `seev-slo-posting-*` posting_availability burn-rate alerts all carry this runbook's path in their `runbook` annotation.

Two distinct alert messages map to this runbook:

- **"unbalanced transaction detected"** — `fn_verify_ledger_balance()` found a `balance_transaction`/`ledger_transactions` row where `Σdebit ≠ Σcredit`. This should be structurally impossible (`validateBalanced` in `internal/ledger/service/handle/service.go` checks this before every insert) — seeing this alert means either a genuine bug slipped past that check, or something wrote to `ledger_entries` outside the normal posting path.
- **"balance projection inconsistent"** — `v_account_balance_audit` found an account where `account_balances.balance` (the fast-read projection) disagrees with the balance computed by summing that account's `ledger_entries`. The entries are the source of truth; the projection is a cache that's supposed to always match them.

## Step 1 — Don't panic, don't touch anything yet

The system does **not** auto-repair by design (docs/plan/01 decision: `ledger_entries` is append-only, no automated corrections). Nothing will get worse in the next 15 minutes just from this alert existing. Take the time to actually understand what happened before acting.

## Step 2 — Get the details

Connect to the production database (read-only role if available) and run:

```sql
-- For "unbalanced transaction detected":
SELECT * FROM fn_verify_ledger_balance(now() - interval '2 hours', now());

-- For "balance projection inconsistent":
SELECT * FROM v_account_balance_audit WHERE is_consistent = false;
```

Widen the time window if the alert is older than 2 hours (the hourly check only looks back 2 hours by default — see `checkTrialBalance`'s query). For a specific account or transaction already named in the alert message, query it directly by id instead of scanning the whole window.

## Step 3 — Investigate the transaction/account

Pull the full transaction header and every entry it touched:

```sql
SELECT * FROM ledger_transactions WHERE id = '<transaction_id>';
SELECT * FROM ledger_entries WHERE transaction_id = '<transaction_id>' ORDER BY created_at;
```

For a projection inconsistency, also check the account's own history:

```sql
SELECT * FROM account_balances WHERE account_id = '<account_id>';
SELECT * FROM ledger_entries WHERE account_id = '<account_id>' ORDER BY created_at DESC LIMIT 50;
```

Things to determine:

1. **Is this a processor bug** (a `TxProcessor` implementation in `internal/ledger/processors/` built an unbalanced set of entries, or `applyEntries`/`ApplySystemDeltas` in `internal/ledger/service/handle/service.go` computed a balance wrong)? Check recent deploys around the transaction's `created_at` — did a processor change ship recently?
2. **Is this data corruption** (manual DB intervention, a migration that touched `ledger_entries` or `account_balances` directly, a restore from an inconsistent backup)? Check `pg_stat_activity`/audit logs for direct SQL around the time of the earliest affected row.
3. **Is this a single isolated incident or an ongoing pattern**? Re-run the query from Step 2 — if new rows keep appearing, something is actively still wrong (stop here, escalate immediately per Step 5, don't spend more time investigating solo).

## Step 4 — Correct it

**Never `UPDATE` or `DELETE` a row in `ledger_entries`.** The `trg_entries_immutable` trigger rejects this at the database level regardless of who's asking — application code, an ops script, or a manual `psql` session. This is intentional; do not attempt to bypass it (e.g. by disabling the trigger) without sign-off from whoever owns this system's compliance posture.

The only correct way to fix a ledger discrepancy is a **new reversal transaction**:

- Use the `reversal` transaction type via the **internal router** (`POST /admin/... /transactions` with `type: "reversal"`, `reference_id: <original_transaction_id>`) — this is admin-gated (`adminOnlyTypes` in `internal/ledger/transport/http.go`) and only reachable on the internal-only listener (docs/plan/10 Task T1), never from the public API.
- The `Reversal` processor (`internal/ledger/processors/reversal.go`) builds entries that exactly invert the original transaction's entries, re-establishing balance.
- For a projection-only inconsistency (entries are balanced, but `account_balances.balance` itself drifted — e.g. from a bug predating the `ApplySystemDeltas` atomic-delta redesign in docs/plan/11 Task T1), a reversal transaction against the affected account is still the right tool: it's the only sanctioned way to move `account_balances.balance`, and it leaves an audit trail explaining why.

After correcting, re-run the Step 2 query to confirm the discrepancy is gone (or, for an unbalanced transaction, confirm the reversal produced net-zero across original + reversal).

## Step 5 — Escalate

If you cannot identify the root cause within **15 minutes** of starting Step 3, or if Step 3.3 found this is an ongoing/repeating pattern, escalate to an engineer with production DB access and context on the affected processor(s). Do not spend hours alone on a money-correctness issue — a second pair of eyes is cheap compared to the cost of a wrong fix.

When escalating, hand over:
- The exact alert message and timestamp.
- The output of the Step 2 and Step 3 queries.
- Whether you've already applied a reversal (and its transaction id, if so) or are still investigating.

## Related

- [docs/plan/09-hardening-review.md](../plan/09-hardening-review.md) §B3 — why this alert hook exists.
- [docs/plan/12-phase2c-resilience-ops.md](../plan/12-phase2c-resilience-ops.md) Task T4 — implementation details (webhook payload shape, `ALERT_WEBHOOK_URL` config).
- [docs/plan/01-target-architecture.md](../plan/01-target-architecture.md) — append-only ledger rationale, locked design decisions.
