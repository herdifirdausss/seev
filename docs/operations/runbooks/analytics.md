# Analytics Runbook

> [Operations](../README.md) · [Runbooks](README.md)

Analytics stack failures affect warehouse/dashboard freshness only. OLTP and
money movement are never blocked by analytics state.

---

## ClickHouse ingestion stalled

**Symptoms:** ClickHouse health/query failure, raw ingestion stops, or consumer lag grows.

```bash
make analytics-health
docker compose -f analytics/compose/docker-compose.analytics.yml logs --tail=100 clickhouse
```

Keep Connect/Redpanda running while retention permits. Restart ClickHouse, check the Kafka Engine
consumer group, and let retained topics replay. Check raw transport identity and logical keys
after recovery; run dbt full/incremental tests and reconciliation. If retention expires,
re-snapshot the source. Record the maximum lag and duplicate assessment.

**Known config issues (confirmed 2026-08-05):**
- The `clickhouse` Compose service must bind-mount only `config.d/analytics-settings.xml` as a
  single file, not the whole `config.d` directory — a directory mount silently drops the image's
  own `docker_related_config.xml` (`listen_host: 0.0.0.0`).
- `background_pool_size` and `number_of_free_entries_in_pool_to_*` settings must stay consistent
  or ClickHouse refuses to start.

---

## Connector failed

**Symptoms:** Connect task state is `FAILED`, no new topic records, or source WAL retention grows.

```bash
make analytics-connectors-status
docker compose -f analytics/compose/docker-compose.analytics.yml logs --tail=100 connect
```

Pause the failed connector, fix the allowlist/configuration, and resume from the existing
slot/offset. Assess missing/duplicate rows with `make analytics-reconcile`. Do not drop a slot
during ordinary recovery.

**Known issue (confirmed 2026-08-05):** After source DB recovery, a connector may stay `RUNNING`
at the top level while its task stays `FAILED`. Recovery requires an explicit task restart:

```bash
curl -s -X POST http://127.0.0.1:18083/connectors/<name>/tasks/0/restart
```

Also: row-level security can make a snapshot silently return zero rows if the replication role
lacks `app_readonly` membership with `INHERIT TRUE`.

---

## dbt failure

**Symptoms:** dbt build/test exits non-zero, incremental model fails, or schema contract changes.

```bash
make analytics-dbt-test
docker compose -f analytics/compose/docker-compose.analytics.yml logs --tail=100 dbt
```

Keep the last known-good mart available and mark freshness visible. Fix the model/contract and
rerun the bounded lookback.

**Frequent failure categories (confirmed 2026-08-05):**
1. `dbt_transform` grant gaps — `ACCESS_DENIED` on `raw.cdc_events` itself, or missing `DROP VIEW`/`TRUNCATE` on a schema.
2. Ambiguous unaliased `table.column` selected across a join — ClickHouse only elides the qualifier when unambiguous.
3. A bug only inside `{% if is_incremental() %}` branch — test both full-refresh and incremental paths after editing.

---

## Full rebuild

Use when raw retention, schema failure, or incremental state cannot be trusted.

```bash
make analytics-connectors-pause
ANALYTICS_CONFIRM_RESET=analytics-only make analytics-reset
make analytics-up-core
make analytics-clickhouse-migrate
make analytics-connectors-resume
make analytics-dbt-build
make analytics-reconcile
```

**Important (confirmed 2026-08-05):** Deleting a connector definition and dropping the replication
slot does **not** force a fresh snapshot on reapply — Kafka Connect's offset-storage topic
remembers the connector as "already ran." `ANALYTICS_CONFIRM_RESET=analytics-only make analytics-reset`
is required to clear that state.

---

## Metabase outage

**Symptoms:** dashboards or the Metabase health endpoint fail. CDC, ClickHouse, dbt, and OLTP continue.

```bash
docker compose -f analytics/compose/docker-compose.analytics.yml ps metabase
docker compose -f analytics/compose/docker-compose.analytics.yml logs --tail=100 metabase
```

Restart or recreate only the Metabase service. Verify the ClickHouse read-only role and dashboard
manifests; no source replay is needed.

---

## Analytics reconciliation failed

**Symptoms:** control run status is failed/stale, critical money delta, or debit-credit invariant fails.

```bash
make analytics-reconcile
```

Confirm the safe cutoff, connector offsets, and dbt invocation. A critical mismatch exits
non-zero and must not be repaired by writing source data. Classify unlinked legacy Payin rows
explicitly, then rerun after catch-up.

---

## Redpanda outage

**Symptoms:** broker health fails, Connect cannot publish, topic lag/WAL retention grows.

```bash
docker compose -f analytics/compose/docker-compose.analytics.yml ps
docker compose -f analytics/compose/docker-compose.analytics.yml logs --tail=100 redpanda
```

Restore the single-node broker or reset only a disposable analytics volume if corrupt. Never
change RabbitMQ topology or application transactions as a recovery action.

**Confirmed 2026-08-05:** After a full Docker-daemon crash, `docker start seev-analytics-redpanda-1`
followed by bringing Connect back up resulted in all three connectors returning to `RUNNING`
automatically with zero reconciliation failures — no manual connector reconfiguration needed.

---

## Replication slot lag

**Symptoms:** connector freshness above target or slot inactive.

```bash
psql "$SOURCE_ADMIN_DSN" -f analytics/postgres/source-summary.sql
make analytics-connectors-status
```

Confirm Redpanda/Connect/ClickHouse health, then resume and watch the slot catch up. If lag
cannot be drained within the WAL bound, use the [WAL pressure](#wal-pressure) procedure.

---

## Schema change

**Symptoms:** Connect schema-history error, prohibited-column detection, or dbt `on_schema_change=fail`.

```bash
make analytics-config-check
make analytics-connectors-status
```

Pause the affected connector if the change is not approved. Additive nullable changes may be
reviewed; drops, renames, narrowing, money-unit, timestamp-semantic, key, or enum changes block.
Do not silently default a missing financial field.

---

## Sensitive data incident

**Symptoms:** prohibited field in a connector config/topic/raw table, secret printed, or dashboard
exposes identity/destination.

```bash
make analytics-connectors-pause
make analytics-verify
```

Stop downstream exposure, preserve only redacted evidence, rotate the affected secret/salt,
remove the analytical topic/warehouse data through the disposable reset path, and re-snapshot
from the reviewed allowlist.

**Real incident (confirmed 2026-08-05):** The `PseudonymizeField` SMT was a silent no-op because
it checked for `Map` values but Connect passes typed `Struct`/`Schema` records — `user_id`
reached `raw.cdc_events.payload` unpseudonymized. Verify with a live streaming event, not just
a snapshot. Also: `make analytics-verify`'s static scan had its own silent-no-op bug — confirm
`grep` (not `rg`) is what actually runs.

---

## WAL pressure

**Symptoms:** retained WAL alert, source disk growth, or inactive C2 replication slot.

```bash
psql "$SOURCE_ADMIN_DSN" -f analytics/postgres/source-summary.sql
make analytics-connectors-pause
```

Stop analytics consumers and protect the source. If the bound is still unsafe, use the explicitly
confirmed `analytics/postgres/drop-replication.sh` procedure after recording the slot LSN. This
loses analytical continuity, not source money. Reconcile before exposing dashboards.
