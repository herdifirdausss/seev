# Analytics Platform Reference

> [Documentation home](../README.md) · [Reference](README.md)

The C2 analytics stack is a removable, read-only projection of Ledger, Payin,
and Payout data. No application service or money-movement transaction depends
on C2 availability. See [ADR-0003](decisions/0003-analytics-read-only-projection.md) for the design rationale.

## Architecture

```text
Ledger/Payin/Payout PostgreSQL
  -> Debezium PostgreSQL connectors (logical WAL)
  -> Redpanda CDC topics
  -> ClickHouse (raw -> staging -> core -> mart)
  -> Metabase (bi_readonly role, read-only)
  -> Reconciliation runner (read-only control evidence)
```

RabbitMQ remains the operational/domain-event bus. Redpanda is used only for
CDC transport and replay. There is no reverse arrow to PostgreSQL.

If Connect, Redpanda, or ClickHouse fails, source transactions continue. The
failure creates lag and possibly retained WAL; it does not create an OLTP
dependency. Restart uses stored offsets and the warehouse deduplication key.

## Financial semantics

- `ledger_entries` is immutable and is the financial fact source.
- Debit/credit equality is tested per transaction and period.
- Fee revenue is net movement in approved Ledger fee accounts, using posted
  entries only. Fee quotes and GPV never prove revenue.
- All money stays integer minor units and is grouped by currency.
- Business report dates are derived in `Asia/Jakarta`; source timestamps stay UTC.
- Vendor cost and contribution margin are explicitly modeled and versioned,
  not actual profitability.

## Source ownership and privacy

The allowlist in [`analytics/contracts/sources.yaml`](../../analytics/contracts/sources.yaml)
is the source contract. The Connect SMT removes approved identity fields and
replaces them with deterministic HMAC pseudonyms before topic publication.
Raw payloads, payout destinations, credentials, KYC fields, and raw callback
bodies are not captured. The correlation rules are in
[`analytics/contracts/correlation-matrix.md`](../../analytics/contracts/correlation-matrix.md).

Existing Ledger reporting and compliance views remain authoritative for their
existing purpose. C2 does not replace balances, statements, regulatory views,
settlement evidence, or authorization reads.

## Source inventory

| Service | Database | Tables captured | Owner |
|---|---|---|---|
| Ledger | `seev_ledger` | accounts, account_balances, ledger_transactions, ledger_entries, fee_quotes | LedgerService |
| Payin | `seev_payin` | payin_topup_intents, payin_webhook_events | PayinService |
| Payout | `seev_payout` | payout_requests, payout_vendor_calls | PayoutService |

Excluded: authentication/KYC/session tables, raw callback bodies, raw vendor
request/response data, payout destinations, credentials, raw error messages,
idempotency secrets, and operator identity fields.

**Known gap**: Payin's schema has a deterministic webhook-to-intent reference
but no persisted Payin-to-Ledger transaction identifier. C2 leaves that field
null and reports the gap; it does not infer a relationship from amount/time/status.

## Data contracts

Contract files:

- [`analytics/contracts/sources.yaml`](../../analytics/contracts/sources.yaml) — source allowlist
- [`analytics/contracts/privacy.yaml`](../../analytics/contracts/privacy.yaml) — privacy policy
- [`analytics/contracts/topics.yaml`](../../analytics/contracts/topics.yaml) — topic contract
- [`analytics/contracts/models.yaml`](../../analytics/contracts/models.yaml) — model contract
- [`analytics/contracts/metrics.yaml`](../../analytics/contracts/metrics.yaml) — metric contract
- [`analytics/contracts/correlation-matrix.md`](../../analytics/contracts/correlation-matrix.md) — correlation inventory

### At-least-once identity

Transport identity: `(topic, partition, offset)`. Logical current-state
identity: `(source service, table, primary key)` ordered by source LSN. Delete
markers remain observable; a delete on immutable Ledger entries is a critical
reconciliation failure.

### Change policy

Compatible additive nullable fields may be added to the allowlist. Drops,
renames, narrowing, money-unit changes, timestamp-semantic changes,
primary-key changes, and enum meaning changes fail visibly and require a new
contract version.

## Metric catalog

Every monetary metric is an integer minor-unit value grouped by currency. No
card combines currencies without an explicit FX model.

| Metric | Formula | Source of truth | Not this |
|---|---|---|---|
| Pay-in GPV | sum of successful Pay-in amounts | `fact_payin_lifecycle` | revenue |
| Payout GPV | sum of successful Payout amounts | `fact_payout_lifecycle` | revenue |
| Successful transaction count | count of successful terminal facts | lifecycle facts | money |
| Recognized fee revenue | fee-account credits minus debits for posted Ledger entries | `fact_fee_revenue` | quote or amount × rate |
| Quote conversion | consumed quotes / created quotes | `fact_fee_quote` | recognized revenue |
| Modeled vendor cost | effective schedule fixed/variable calculation | `control.vendor_cost_schedule` | vendor invoice |
| Modeled contribution margin | recognized fee revenue − modeled variable vendor cost | `mart_unit_economics_daily` | net profit |

### Revenue recognition

`fact_fee_revenue` joins immutable Ledger entries to current Ledger accounts
whose `type = 'fee'`, then requires the Ledger transaction to be `posted`.
Credits are positive revenue movement; debits are negative reversal/refund
movement.

### Time and freshness

Facts preserve source, connector, ingestion, and modeled timestamps. Report
dates use `toDate(toTimeZone(timestamp, 'Asia/Jakarta'))`. Dashboard cards show
`data_updated_at_utc`, freshness seconds, and the latest control reconciliation
status. Stale analytics never blocks OLTP or claims transactional correctness.

## Dashboard catalog

Dashboard manifests live in [`analytics/metabase/dashboards/`](../../analytics/metabase/dashboards/)
and are imported via [`analytics/metabase/setup/import_dashboards.py`](../../analytics/metabase/setup/import_dashboards.py).

| Dashboard | Audience | Approved sources | Required warning |
|---|---|---|---|
| Executive overview | business | curated marts + control summaries | revenue/volume/cost semantics and freshness |
| Pay-in performance | Payin owner | Payin lifecycle fact | lifecycle metrics are not revenue |
| Payout performance | Payout owner | Payout lifecycle/attempt facts | destination data is not available |
| Fee and quote conversion | Ledger owner | quote and revenue marts | quote does not prove recognition |
| Modeled unit economics | finance learning | unit economics mart | modeled, not invoiced actuals or net profit |
| Data platform health | operators | freshness/control summaries | analytical health is separate from business health |

Ordinary BI users receive `bi_readonly`, which can query approved marts and
limited dimensions/control summaries only. No dashboard target is an OLTP,
raw CDC, or restricted staging table.

## Operational metrics (Prometheus)

Served by `analytics/reconciliation/cmd/metrics-exporter` (`make analytics-metrics-exporter`):

| Metric | Type | Labels | Source |
|---|---|---|---|
| `seev_analytics_connector_up` | gauge | `source` | Kafka Connect REST API |
| `seev_analytics_reconciliation_passed` | gauge | none | `control.reconciliation_runs` (latest) |
| `seev_analytics_reconciliation_critical_failures` | gauge | none | `control.reconciliation_runs` (latest) |
| `seev_analytics_reconciliation_warning_failures` | gauge | none | `control.reconciliation_runs` (latest) |
| `seev_analytics_data_freshness_seconds` | gauge | `source`, `table` | `mart.mart_freshness` |
| `seev_analytics_dbt_run_total` | counter | `result` | `control.dbt_invocations` (last 24h) |

Alert rules: `deploy/observability/prometheus/rules/analytics.yml`.
Runbooks: `docs/operations/runbooks/analytics-*.md`.

## Ownership

LedgerService owns fee and Ledger metrics. PayinService and PayoutService own
their lifecycle metrics. Analytics owns cross-source modeled cost and platform
health. Changes require an updated contract, formula, test, and dashboard
catalog entry.
