# Analytics dbt failure

Symptoms: dbt build/test exits non-zero, an incremental model fails, or a
schema contract changes. No source write is allowed.

```bash
make analytics-dbt-test
docker compose -f analytics/compose/docker-compose.analytics.yml logs --tail=100 dbt
```

Keep the last known-good mart available and mark freshness/staleness visible.
Classify SQL, data, or incompatible-schema failure, fix the model/contract,
and rerun the bounded lookback. Use a disposable full refresh when incremental
state may be suspect, then reconcile marts to core/source. Record invocation id,
model, failure class, and whether any dashboard was hidden.
