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

Confirmed 2026-08-05, the real failure categories seen so far, roughly in
order of frequency: (1) `dbt_transform` grant gaps — `ACCESS_DENIED` on
`raw.cdc_events` itself (only the deduplicated view was granted), or missing
`DROP VIEW`/`DROP TABLE`/`TRUNCATE` on a schema; (2) an ambiguous unaliased
`table.column` selected across a join — ClickHouse only elides the qualifier
when the bare name is unambiguous, so an ambiguous one gets persisted with
the literal dotted name and breaks every downstream reference; (3) a bug
that only lives inside a model's `{% if is_incremental() %}` branch, which
`--full-refresh` never exercises (the table doesn't exist yet, so the branch
is skipped) — test both paths after editing an incremental model. `control.dbt_invocations`
is now actually populated (an `on-run-end` hook was missing before) —
`SELECT * FROM control.dbt_invocations ORDER BY started_at DESC LIMIT 5`.
