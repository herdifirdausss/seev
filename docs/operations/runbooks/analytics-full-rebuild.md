# Analytics full rebuild

Use when raw retention, schema failure, or incremental state cannot be trusted.
The warehouse is disposable; source PostgreSQL is not.

```bash
make analytics-connectors-pause
ANALYTICS_CONFIRM_RESET=analytics-only make analytics-reset
make analytics-up-core
make analytics-clickhouse-migrate
make analytics-connectors-resume
make analytics-dbt-build
make analytics-reconcile
```

Before reset, record connector offsets, source cutoffs, image versions, and
current control status. If retained CDC is insufficient, run a fresh reviewed
snapshot. Validate full-refresh totals, duplicate logical keys, debit-credit
invariants, fee-account revenue, and dashboard totals before resuming UI.
Record rebuild duration, source snapshot LSN/cutoff, and any data unavailable
during the rebuild.
