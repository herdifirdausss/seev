# Analytics connector failed

Symptoms: Connect task state is `FAILED`, no new topic records arrive, or
source WAL retention grows. Impact is stale analytics only; OLTP remains the
priority and must not be blocked.

```bash
make analytics-connectors-status
docker compose -f analytics/compose/docker-compose.analytics.yml logs --tail=100 connect
```

Pause the failed connector if it is retrying aggressively, capture the
connector name/error classification without copying credentials, fix the
allowlist/configuration, and resume from the existing slot/offset. Assess
missing/duplicate logical rows with `make analytics-reconcile`. Do not drop a
slot during ordinary recovery. If source disk is threatened, stop the
disposable connector and follow [WAL pressure](analytics-source-wal-pressure.md);
accept a future snapshot rather than risk OLTP. Record connector state, lag,
slot LSN, recovery time, and reconciliation result.

Confirmed 2026-08-05: after the source database itself came back up
following an outage, the connector stayed `RUNNING` at the top level but its
task stayed `FAILED` — Debezium does not auto-retry a task past its error
threshold. Recovery needed one explicit task restart, not just source
recovery:

```bash
curl -s -X POST http://127.0.0.1:18083/connectors/<name>/tasks/0/restart
```

Also confirmed: row-level security on the Ledger/Payin/Payout tables can make
a snapshot silently return zero rows (not an error) if the replication role
lacks `app_readonly` membership `WITH INHERIT TRUE` — logical
decoding/streaming bypasses RLS, but the initial snapshot's regular SELECT
does not.
