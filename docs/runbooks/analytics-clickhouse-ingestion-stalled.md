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
