# Runbook: External Reconciliation

Covers the daily settlement reconciliation flow (docs/plan/16 Task T2, decision K5): importing a gateway's settlement report, reviewing the match report, and resolving discrepancies via a governed adjustment. All endpoints below are on the **internal-only listener**, admin-gated (`isAdmin` in `internal/ledger/transport/http.go`).

Also reachable via Grafana unified alerting (docs/plan/43 Task T5, folder "Seev"): `seev-op-notification-handler-slow`, `seev-op-payout-stuck-backlog`, `seev-op-breaker-open-extended`, and the `seev-slo-webhook-*`/`seev-slo-notif-*` burn-rate alerts all carry this runbook's path in their `runbook` annotation.

## The four match statuses

| `match_status` | Meaning | Who investigates | Money movement to resolve |
|---|---|---|---|
| `matched` | Ledger and gateway report agree on amount for this `external_ref`. No action needed. | — | None |
| `amount_mismatch` | Ledger has a posted transaction for this `external_ref`, but the amount differs from the report. | Finance/ops — compare against the raw gateway dashboard/API for this ref, not just this CSV. | Suspense adjustment for the *difference*, once the correct amount is confirmed |
| `missing_internal` | The report claims this `external_ref` settled, but the ledger has no posted transaction for it at all. | Ops — check if the corresponding `money_in`/`money_out` request ever reached the API (client-side failure? webhook never fired? posted under a different `external_ref` by mistake?). | Suspense credit for the missing amount, once confirmed the money genuinely moved and the ledger just never recorded it |
| `missing_external` | The ledger has a posted transaction with this gateway/`external_ref`, but the report has no matching row. | Ops — check whether the gateway's settlement is simply delayed (re-import tomorrow's/a later report first) before assuming the ledger is wrong. | Suspense debit, only after confirming the transaction should NOT have settled (e.g. it was reversed by the gateway out-of-band) |

`missing_external` is the one most likely to be a **false positive** caused by settlement lag (the gateway hasn't reported it yet, not that it's wrong) — always re-check with a fresher report before resolving one of these.

## Step 1 — Import the settlement report

```
POST /admin/recon/batches
Content-Type: multipart/form-data

  gateway=bca
  report_date=2026-07-12
  file=<settlement.csv>   (columns: external_ref, amount, settled_at — any column order)
```

`amount` must be an integer in minor units — a fractional value is rejected outright (400), never silently truncated (same rule as every other amount in this API, docs/plan/10 Task T4). A file over 50,000 rows is rejected — split it and import in multiple batches for the same `report_date`.

The import synchronously runs the matcher (`ReconRepository.RunMatcher`) in the same DB transaction as the insert — by the time the request returns, every row is already classified. There is no separate "run matcher" step and no background job for this (decision K5: batch settlement CSVs are daily-sized, a single set-based SQL pass is enough — don't add a worker for it).

## Step 2 — Read the report

```
GET /admin/recon/batches/{id}
GET /admin/recon/batches/{id}?match_status=amount_mismatch
GET /admin/recon/batches/{id}?match_status=missing_internal&limit=50&offset=0
```

Returns the batch header, a count per `match_status`, and a paginated item list (optionally filtered to one status). Start with the counts to gauge scale before pulling every non-matched item.

## Step 3 — Investigate, then resolve

For each non-matched item worth correcting, decide:
- **Direction**: `adjustment_suspense_credit` (the gateway's suspense account should have MORE — use for `missing_internal` and for `amount_mismatch` where the report is higher) or `adjustment_suspense_debit` (suspense should have LESS — `missing_external` and `amount_mismatch` where the report is lower).
- **Amount**: defaults to the recon item's own amount if omitted; override explicitly for a partial correction.

```
POST /admin/recon/items/{id}/resolve
{
  "type": "adjustment_suspense_credit",
  "amount": "50000",
  "reason": "confirmed with BCA dashboard, ledger never received the webhook"
}
```

**This does NOT move any money.** It creates a `pending_adjustments` row (the exact same maker-checker table Task T1 uses) referencing the recon item, with `resolved_by_adjustment_id` set on the item. A **second, different identity** must separately call:

```
POST /admin/adjustments/{adjustment_id}/approve
```

before the suspense account balance actually changes — see [docs/plan/16-phase2f-governance-recon-rls.md](../plan/16-phase2f-governance-recon-rls.md) Task T1 for the full maker-checker contract (self-approval rejected, retry-safe, DB-level backstop). If you resolve an item and no one approves it, the discrepancy simply stays open — nothing rots or auto-executes.

## Step 4 — Confirm

After approval, re-`GET` the batch — the resolved item's `resolved_by_adjustment_id` is set and the suspense account's balance (`GET /accounts/{suspense_account_id}/balance`) reflects the correction. `fn_verify_ledger_balance()` must stay empty (see [ledger-integrity-alert.md](ledger-integrity-alert.md) if it isn't).

## Common mistakes

- **Resolving `missing_external` too eagerly** — always suspect settlement lag first; re-import a later report before assuming the ledger is wrong.
- **Two people resolving the same item at once** — the second resolve request still creates a pending adjustment (Create() itself can't know it's about to lose the race), but `MarkItemResolved`'s atomic guard means only the FIRST one actually attaches to the item; the second is orphaned. Reject the orphaned adjustment via `POST /admin/adjustments/{id}/reject` rather than approving it.
- **Approving your own resolve request** — rejected with 403 (`SELF_APPROVAL`), same as any other adjustment. Get a second identity.

## Related

- [docs/plan/16-phase2f-governance-recon-rls.md](../plan/16-phase2f-governance-recon-rls.md) Task T2 — full design and decisions (K5).
- [ledger-integrity-alert.md](ledger-integrity-alert.md) — what to do if `fn_verify_ledger_balance()` ever finds an unbalanced transaction, including ones from this flow.
