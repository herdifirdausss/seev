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

Confirmed 2026-08-05: deleting a connector definition and dropping the
source's replication slot does **not** by itself force a fresh
`snapshot.mode=initial` snapshot on reapply — Kafka Connect's own
offset-storage topic still remembers the connector name as "already ran," so
it skips straight to streaming from wherever that stale offset points.
`ANALYTICS_CONFIRM_RESET=analytics-only make analytics-reset` is what
actually clears that (it removes the whole analytics Compose project,
including Redpanda's config/offset/status topics) — the step above is
necessary, not optional, for a genuine rebuild.
