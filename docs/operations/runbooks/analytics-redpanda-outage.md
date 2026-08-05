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

Confirmed 2026-08-05 (as part of a full Docker-daemon crash that stopped
every container at once): `docker start seev-analytics-redpanda-1` followed
by bringing Connect back up resulted in all three connectors returning to
`RUNNING` automatically, with zero reconciliation failures afterward — no
manual connector reconfiguration was needed for this outage type.
