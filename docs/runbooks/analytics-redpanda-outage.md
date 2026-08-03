# Analytics Redpanda outage

Symptoms: broker health fails, Connect cannot publish, and topic lag/WAL
retention grows. OLTP must continue normally.

```bash
docker compose -f analytics/compose/docker-compose.analytics.yml ps
docker compose -f analytics/compose/docker-compose.analytics.yml logs --tail=100 redpanda
```

Restore the single-node broker or reset only a disposable analytics volume if
the local log is corrupt. Never change RabbitMQ topology or application
transactions as a recovery action. Verify Connect resumes from offsets,
ClickHouse dedupes any replay, then run dbt/reconciliation. Record outage
window, retained WAL, topic offsets, and logical totals.
