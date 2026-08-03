# Analytics metric catalog

Every monetary metric is an integer minor-unit value and is grouped by
currency. No card combines currencies without an explicit FX model; C2 v1 has
no FX model.

| Metric | Formula | Source of truth | Not this |
| --- | --- | --- | --- |
| Pay-in GPV | sum of successful Pay-in amounts | `fact_payin_lifecycle` | revenue |
| Payout GPV | sum of successful Payout amounts | `fact_payout_lifecycle` | revenue |
| Successful transaction count | count of successful terminal facts | lifecycle facts | money |
| Recognized fee revenue | fee-account credits minus fee-account debits for posted Ledger entries | `fact_fee_revenue` | quote or amount × rate |
| Quote conversion | consumed quotes / created quotes | `fact_fee_quote` | recognized revenue |
| Modeled vendor cost | effective schedule fixed/variable calculation | `control.vendor_cost_schedule` | vendor invoice |
| Modeled contribution margin | recognized fee revenue − modeled variable vendor cost | `mart_unit_economics_daily` | net profit |

## Revenue recognition

`fact_fee_revenue` joins immutable Ledger entries to current Ledger accounts
whose `type = 'fee'`, then requires the Ledger transaction to be `posted`.
Credits are positive revenue movement; debits are negative reversal/refund
movement. The mart must reconcile exactly to the same fee-account entry set at
the safe cutoff.

## Time and freshness

Facts preserve source, connector, ingestion, and modeled timestamps. Report
dates use `toDate(toTimeZone(timestamp, 'Asia/Jakarta'))`. Dashboard cards show
`data_updated_at_utc`, freshness seconds, and the latest control reconciliation
status. Stale analytics never blocks OLTP or claims transactional correctness.

## Ownership

LedgerService owns fee and Ledger metrics. PayinService and PayoutService own
their lifecycle metrics. Analytics owns cross-source modeled cost and platform
health. Changes require an updated contract, formula, test, and dashboard
catalog entry.
