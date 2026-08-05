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

## Operational metrics (Prometheus)

Served by `analytics/reconciliation/cmd/metrics-exporter` (`make
analytics-metrics-exporter`, scraped by the app's Prometheus as jobs
`analytics-metrics-exporter`/`analytics-clickhouse`/`analytics-redpanda`),
runtime-verified 2026-08-05:

| Metric | Type | Labels | Source |
| --- | --- | --- | --- |
| `seev_analytics_connector_up` | gauge | `source` | Kafka Connect REST API (polled directly — no JMX exporter in this Debezium image build) |
| `seev_analytics_reconciliation_passed` | gauge | none | `control.reconciliation_runs` (latest) |
| `seev_analytics_reconciliation_critical_failures` | gauge | none | `control.reconciliation_runs` (latest) |
| `seev_analytics_reconciliation_warning_failures` | gauge | none | `control.reconciliation_runs` (latest) |
| `seev_analytics_data_freshness_seconds` | gauge | `source`, `table` | `mart.mart_freshness` |
| `seev_analytics_dbt_run_total` | counter | `result` | `control.dbt_invocations` (last 24h), populated by the `record_dbt_invocation` dbt `on-run-end` hook |

Plus ClickHouse's and Redpanda's own native `/metrics` endpoints (not
`seev_analytics_`-prefixed). Alert rules in
`deploy/observability/prometheus/rules/analytics.yml` link each alert to a
runbook under `docs/operations/runbooks/`. Not yet covered: Redpanda
consumer-lag-growing, replication-slot-inactive, retained-WAL-threshold, and
Metabase-query-failure — see
[c2-final-acceptance.md](../evidence/c2-final-acceptance.md) for the full
residual list.

## Ownership

LedgerService owns fee and Ledger metrics. PayinService and PayoutService own
their lifecycle metrics. Analytics owns cross-source modeled cost and platform
health. Changes require an updated contract, formula, test, and dashboard
catalog entry.
