# Metabase outage

Symptoms: dashboards or the Metabase health endpoint fail. Impact is UI-only;
CDC, ClickHouse, dbt, reconciliation, and OLTP must continue.

```bash
docker compose -f analytics/compose/docker-compose.analytics.yml ps metabase
docker compose -f analytics/compose/docker-compose.analytics.yml logs --tail=100 metabase
```

Restart or recreate only the Metabase service/application volume. Verify the
ClickHouse read-only role and dashboard manifests; no source replay is needed.
Show dashboard data as unavailable/stale until freshness and reconciliation
cards work again. Record outage duration and confirmation that no analytics or
application write path depended on Metabase.
