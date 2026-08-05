# ClickHouse ingestion stalled

Symptoms: ClickHouse health/query failure, raw ingestion stops, or consumer lag
grows. Impact is warehouse/dashboard staleness only.

```bash
make analytics-health
docker compose -f analytics/compose/docker-compose.analytics.yml logs --tail=100 clickhouse
```

Keep Connect/Redpanda running while safe retention permits. Restart ClickHouse,
check the Kafka Engine consumer group, and let retained topics replay. Check
raw transport identity and logical keys after recovery; run dbt full/incremental
tests and reconciliation. If retention expires, re-snapshot the source. Record
the maximum lag and duplicate assessment.

Confirmed 2026-08-05, two config bugs that present as this symptom and are
worth checking before assuming a plain restart will fix it: (1) the
`clickhouse` Compose service must bind-mount only
`config.d/analytics-settings.xml` as a single file, not the whole `config.d`
directory — a directory mount silently drops the image's own
`docker_related_config.xml` (`listen_host: 0.0.0.0`), leaving the server
reachable only on its own loopback; (2) `background_pool_size` and the
`number_of_free_entries_in_pool_to_*` merge-tree settings in
`analytics/clickhouse/config/config.d/analytics-settings.xml` must stay
consistent with each other or ClickHouse refuses to start at all.
