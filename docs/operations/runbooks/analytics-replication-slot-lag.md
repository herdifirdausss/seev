# Analytics replication-slot lag

Symptoms: connector freshness is above target or a slot is inactive. Impact is
stale analytical data and retained source WAL.

```bash
psql "$SOURCE_ADMIN_DSN" -f analytics/postgres/source-summary.sql
make analytics-connectors-status
```

Confirm Redpanda/Connect/ClickHouse health, then resume the consumer and watch
the slot catch up. Do not mutate application tables. If lag cannot be drained
within the WAL bound, pause/delete the disposable connector and use the source
WAL-pressure runbook. Run dbt and reconciliation after catch-up; record the
safe cutoff and any re-snapshot requirement.
