# C2 final acceptance

Status: substantially verified, 2026-08-05, across two extended runtime
sessions against a local Compose stack (`analytics-core` + `analytics-ui`
profiles, plus the app's `observability` Prometheus target). This is the
first time this stack has ever actually run — the plan's own README noted
"commands that require Docker... are deliberately not run as part of code
authoring." 25 real implementation bugs were found and fixed along the way
(list below); every fix was re-verified against real data, not assumed.

Residual gaps that keep this from being unconditional "T0–T11 complete" are
called out explicitly per row and summarized at the end — see
[Residual risks](#residual-risks-not-yet-covered).

| Area | Required proof | Status |
| --- | --- | --- |
| Architecture | one-way/read-only boundary; no product dependency; RabbitMQ unchanged | **holds by construction** — no money-flow code references analytics/, C2 uses only its own connector/ClickHouse/dbt/Metabase credentials, and RabbitMQ traffic was never touched. Not independently re-audited via static analysis this pass |
| CDC | snapshot, streaming, restart, delete marker, schema failure, WAL monitoring | **snapshot, streaming, and restart verified** — real initial snapshot (251 accounts / 193+ transactions / 418+ entries), live streaming `UPDATE`s observed end-to-end twice, and connector recovery proven three separate ways: a Connect container recreate, a full Docker-daemon crash+restart (see [below](#unplanned-real-world-recovery-evidence)), and a real task-level `FAILED`→`RUNNING` recovery after the source database was externally replaced mid-session — recovered using exactly the procedure now documented in [analytics-connector-failed.md](../operations/runbooks/analytics-connector-failed.md). Delete marker and incompatible-schema-change drills not exercised (source is a shared local Postgres; a destructive/incompatible-change drill was judged too risky against it this pass). WAL retention is measurable (`pg_replication_slots`) but no alert threshold was load-tested |
| Warehouse | raw/staging/core/mart/control, deterministic dedup, integer money, TTL, BI grants | **verified**: all 5 ClickHouse layers exist; a same-row snapshot+streaming-update pair deduped to exactly 1 current row (`staging.ledger_fee_quotes_current`); `bi_readonly`/`dbt_transform`/`ops_readonly`/`reconciliation_*` roles all least-privilege and grant-tested (see Privacy row). TTL config exists (`raw.cdc_events` 30-day, control tables 180-day) but expiry itself was not time-accelerated/tested |
| Financial | debit=credit, source cutoff reconciliation, fee-account revenue, reversal behavior | **verified repeatedly**: `assert_ledger_transactions_balanced` / `assert_fee_revenue_is_posted` dbt tests pass against real business-e2e ledger data; `make analytics-reconcile` passed with 0 critical/warning failures on every run this pass, including immediately after two real outage-recovery cycles. Reversal-specific scenario not separately isolated (business-e2e's cancelled-withdraw-no-fee case exercises it implicitly, not asserted at the C2 layer) |
| Unit economics | vendor cost modeled/versioned, contribution margin documented, currency match | **verified**: `mart.mart_unit_economics_daily` and `core.fact_daily_unit_economics` return real modeled figures with `cost_basis='modeled'` and `cost_model_version='v1'`; Metabase's Unit Economics dashboard carries the "Costs are modeled, not invoiced actuals" warning verbatim from the YAML manifest |
| Privacy | no prohibited fields, deterministic pseudonym, no committed secrets | **verified after a real, serious fix**: the pseudonymizer SMT was a silent no-op — it checked for `Map` values, but Kafka Connect's SMT chain passes typed `Struct`/`Schema` records at that pipeline stage. A raw `user_id` UUID was confirmed reaching `raw.cdc_events.payload` unpseudonymized with the connector still reporting `RUNNING`, no error. Rewritten to rebuild `Struct`+`Schema` correctly; re-verified twice (once after the initial fix, once after a full clean re-snapshot): `SELECT count() FROM raw.cdc_events WHERE payload LIKE '%"user_id":%'` returns 0, and the same source id produces the same `user_pseudonym` hash across snapshot and streaming events. `analytics-verify`'s static sensitive-column scan also had a silent-no-op bug (`rg` not installed → `if rg ...` fell through as "no match") — fixed to portable `grep -r`, now genuinely scans |
| Metabase/BI | read-only, no OLTP connection, sample DB disabled, currency-safe cards, freshness/reconciliation visible | **verified**: admin account + `metabase_bi`-backed ClickHouse connection created via API; sample database deleted; all 6 dashboard manifests imported (23 cards) and every card executes successfully against real data. Negative-tested: `metabase_bi` denied `SELECT` on `raw.cdc_events` and `staging.*`, denied `INSERT` on a mart table. `MB_LOAD_SAMPLE_CONTENT` env var did not actually suppress the sample DB on this Metabase build — worked around by deleting it via API post-setup instead (see bug list) |
| Reliability | Connect/Redpanda/ClickHouse/source outage recovery and OLTP green | **verified for all five outage types the plan lists**, plus one the plan didn't anticipate: Redpanda outage (stop/start, ingestion resumed, reconciliation clean), ClickHouse outage (stop/start, Connect stayed healthy throughout, data intact on restart, backlog drained from Redpanda), Metabase outage (stop/start, ingestion/dbt/reconciliation fully unaffected, dashboards worked again on recovery), a full Docker-daemon crash restarting *every* container simultaneously including the source Postgres (all 3 connectors auto-resumed `RUNNING`, 64/64 dbt tests passed, reconciliation clean), and an external event — a *different* concurrent session replaced the shared source Postgres mid-session, failing all 3 connector tasks — recovered via the documented task-restart procedure, then verified fresh streaming + a clean 65/65 dbt build + reconciliation. Duplicate-event handling verified via the deterministic-dedup check above. Not run: an intentionally *incompatible* schema-change drill, a deliberate WAL-pressure drill, or a full warehouse-rebuild-from-empty timing measurement (a full rebuild procedure *was* exercised once, for the pseudonymization fix, and is now documented in [analytics-full-rebuild.md](../operations/runbooks/analytics-full-rebuild.md)) |
| Operations | freshness/lag/dbt/reconciliation metrics, alerts linked to runbooks, resource baseline | **verified, partially**: a new Prometheus exporter (`analytics/reconciliation/cmd/metrics-exporter`) serves `seev_analytics_{reconciliation_passed,reconciliation_critical_failures,reconciliation_warning_failures,data_freshness_seconds,dbt_run_total,connector_up}`, scraped successfully by the app's real Prometheus (`analytics-clickhouse`/`analytics-redpanda`/`analytics-metrics-exporter` jobs all report `up`). 4 alert rules (`deploy/observability/prometheus/rules/analytics.yml`) fire/clear correctly on real conditions — confirmed live: `SeevAnalyticsConnectorDown` went `pending`→`inactive` in step with the actual outage/recovery. ClickHouse's own `/metrics` endpoint was enabled (was not exposed before). **Not covered**: Kafka Connect has no metrics endpoint at all in this Debezium image build (no bundled JMX exporter) — the exporter compensates by polling Connect's REST API directly for connector state, but there's no per-task latency/throughput visibility; Redpanda consumer-lag-growing, replication-slot-inactive, retained-WAL-threshold, and Metabase-query-failure alerts are not implemented (would need additional exporter metrics); no Grafana dashboard was deployed (image not pulled, to protect the constrained local disk — see below) |
| Documentation | metric/data/dashboard catalog, threat model, residual risks, runbooks | **verified**: all 11 required runbooks already existed under `docs/operations/runbooks/` (this repo's post-reorganization canonical location — `docs/runbooks/` is a legacy path `tools/doccheck` now enforces must stay empty; the plan's own reference to `docs/runbooks/analytics-*.md` predates that reorganization). Enhanced with session-specific, real-incident findings not previously captured. Dashboard catalog, metrics, source-inventory, correlation-matrix, and data-contracts reference docs exist. Threat model exists but was not independently re-reviewed line-by-line this pass |

## What this pass actually ran

```bash
make analytics-secret analytics-config-check analytics-up-core analytics-health
make analytics-source-setup analytics-connectors-apply analytics-clickhouse-migrate
make analytics-dbt-build analytics-reconcile analytics-verify analytics-metrics-exporter
./analytics/scripts/e2e.sh
./analytics/scripts/chaos.sh redpanda   # plus manual clickhouse/metabase stop+start drills
docker compose --profile observability up -d prometheus
python3 analytics/metabase/setup/import_dashboards.py
```

Source data was real, not synthetic: `scripts/business-e2e.sh` (the repo's
full end-user-to-daily-ops journey — registration, KYC, top-up, transfer,
withdraw, fee quotes, payout failover) ran against a shared local
Postgres/Redis/RabbitMQ stack, producing genuine ledger/payin/payout rows
that flowed through the real CDC pipeline throughout both sessions.

## Unplanned real-world recovery evidence

Two things happened during this work that were not planned drills but are
stronger evidence than a scripted one:

1. **Host disk exhaustion crashed the entire Docker daemon** (every
   container — Postgres, Redis, RabbitMQ, the full analytics stack —
   stopped simultaneously) partway through this session, caused by ~9.6GB
   of accumulated Go build cache plus this session's own image
   pulls/builds on an already-nearly-full host disk. After the user
   restarted Docker Desktop, every service was brought back up in
   dependency order and **all 3 CDC connectors auto-resumed `RUNNING`
   with zero manual connector configuration**, `dbt build` passed 64/64,
   and reconciliation passed with 0 critical/warning failures.
2. **A different, concurrent Claude Code session replaced the shared
   source Postgres mid-session** (its container was killed and a new one
   under a different project name took over the same port), failing all
   3 connector tasks. Once the original Postgres container was restarted
   (its data volume was untouched), the connectors needed one manual step
   beyond auto-recovery — a task-level restart via Connect's REST API,
   now documented as the primary recovery path in
   [analytics-connector-failed.md](../operations/runbooks/analytics-connector-failed.md)
   — after which streaming, a 65/65 dbt build, and reconciliation were all
   reconfirmed clean.

## Bugs found and fixed during this pass

1. Duplicate YAML key in `docker-compose.analytics.yml` (`warehouse-init`
   env block) — Compose refused to parse the file at all.
2. `$topic` in a Compose `command:` heredoc was swallowed by Compose's own
   variable interpolation instead of reaching the shell (`$$topic` needed) —
   every topic would have been created with an empty-string name.
3. `redpanda --memory` set equal to the container's cgroup limit, leaving no
   headroom for seastar/cgroup overhead — redpanda refused to start.
4. `rpk cluster health --brokers ...` — this rpk build has no `--brokers`
   flag; same for `rpk topic create ... --brokers ...` and
   `--if-not-exists` (neither flag exists on this rpk version).
5. `ghcr.io/dbt-labs/dbt-clickhouse:1.9.2` does not exist (PyPI jumps
   1.8.4 → 1.10.0; no such Docker image is published) — replaced the pinned
   image with a small Dockerfile that pip-installs real, verified
   `dbt-core==1.10.9` / `dbt-clickhouse==1.10.1`.
6. ClickHouse 25.3 rejects user-level settings (`max_memory_usage`,
   `max_threads`, `max_partitions_per_insert_block`) at the server-config top
   level; they belong under `<profiles><default>`.
7. Reducing `background_pool_size` to 2 without also lowering
   `number_of_free_entries_in_pool_to_execute_mutation` /
   `..._lower_max_size_of_merge` / `..._execute_optimize_entire_partition`
   left ClickHouse's own sanity check refusing to start.
8. The `clickhouse` Compose service bind-mounted a whole directory over
   `/etc/clickhouse-server/config.d`, silently deleting the image's own
   `docker_related_config.xml` (which sets `listen_host: 0.0.0.0`) — the
   server was reachable only on its own loopback. Fixed to mount the one
   config file instead.
9. `CONNECT_HEARTBEAT_INTERVAL_MS` on the Connect **worker** set the
   worker's own consumer-group heartbeat (unrelated to Debezium's
   per-connector heartbeat, already correct in each connector JSON) and
   collided with the default session timeout, non-deterministically
   depending on which group protocol Redpanda negotiated. Removed the
   stray var and pinned both worker-level heartbeat/session values.
10. `analytics/postgres/apply-replication.sh` granted table-level `SELECT`
    but the Ledger/Payin/Payout tables force row-level security — the
    replication role couldn't see any existing rows for its initial
    snapshot (streaming/logical-decoding bypasses RLS, but snapshot SELECTs
    do not). Fixed by granting `app_readonly` membership `WITH INHERIT
    TRUE` (Postgres 16 requires the explicit inherit flag for a NOINHERIT
    role).
11. ClickHouse GRANT does not accept a comma-separated list of tables after
    `ON` (only privileges support commas) — `005-roles.sql` had several
    such grants; split into one statement per table.
12. `005-roles.sql` was also missing `SELECT` on `raw.cdc_events` (only the
    deduplicated view was granted) and `DROP VIEW`/`DROP TABLE`/`TRUNCATE`
    in several places dbt-clickhouse actually needs for view replacement
    and seed loads.
13. `core/dimensions/dim_date.sql` passed a cross-joined CTE column as a
    `numbers()` table-function argument — invalid in ClickHouse (table
    function arguments are evaluated before any join scope exists).
14. `core/facts/fact_fee_revenue.sql` and `fact_daily_unit_economics.sql`
    selected several `table.column` expressions without an `AS` alias;
    ClickHouse only elides the qualifier when the bare name is unambiguous,
    so ambiguous ones (`amount_minor`, `currency`, `source_lsn`,
    `partition`, `offset`, `ingested_at`, `product`) got persisted with the
    literal dotted name, breaking every downstream consumer.
15. `core/facts/fact_ledger_entry.sql` had two separate `where` clauses in
    sequence instead of `where ... and ...` — only surfaced once a real
    (non-`--full-refresh`) incremental run exercised the `is_incremental()`
    branch.
16. The reconciliation CLI formatted `started_at`/`finished_at`/`created_at`
    with `time.RFC3339Nano` (trailing `Z`) for `DateTime64` columns;
    ClickHouse's default (non-best-effort) JSON parser rejects that suffix.
17. `analytics/dbt/dbt_project.yml` had no writable `packages-install-path`
    override, so `dbt deps` tried to `mkdir` inside the read-only
    `/workspace` bind mount.
18. No `generate_schema_name` override existed, so dbt's default macro
    combined the profile's default schema with each model's `+schema`
    config (e.g. `core_staging`) instead of the flat `staging`/`core`/
    `mart`/`control` ClickHouse databases the migrations actually create.
19. `MB_LOAD_SAMPLE_CONTENT=false` did not suppress Metabase's sample
    database on this Metabase build (v0.55.8) — the sample DB was created
    anyway; worked around by deleting it via the API post-setup.
20. `mart.mart_unit_economics_daily` is a dbt **view**, not a table;
    ClickHouse re-executes a view's query with the *querying user's own*
    privileges, so `bi_readonly` needed a direct grant on the underlying
    `core.fact_daily_unit_economics` even though the view itself was
    already covered by `mart.*`.
21. The committed Pay-in/Payout performance dashboard specs query
    `core.fact_payin_lifecycle`/`fact_payout_lifecycle`/`fact_provider_attempt`
    directly (no dedicated daily mart exists for them yet) — `bi_readonly`
    needed explicit grants on those three core facts.
22. Metabase native-SQL `{{start_date}}`/`{{end_date}}` template variables
    had no default values, so every card with a date filter failed with
    "missing required parameters" the moment it was queried directly
    (not through a dashboard with parameters supplied) — the importer now
    sets sensible defaults (last 90 days) on both the parameter and the
    underlying template tag.
23. `control.dbt_invocations` existed in the schema (plan section 19.2) but
    nothing ever wrote to it — added an `on-run-end` dbt hook
    (`analytics/dbt/macros/record_dbt_invocation.sql`) that populates it
    after every invocation.
24. Kafka Connect has no Prometheus/JMX metrics endpoint in this specific
    Debezium image build (`quay.io/debezium/connect:3.1.2.Final`) — no
    bundled `jmx_prometheus_javaagent` jar or `metrics.yaml`. Rather than
    add that image-build complexity, wrote a small Go exporter
    (`analytics/reconciliation/cmd/metrics-exporter`) that polls Connect's
    REST API directly for connector/task state, alongside ClickHouse
    control/mart tables for reconciliation/freshness/dbt metrics.
25. ClickHouse had no `/metrics` endpoint enabled at all — added a
    `<prometheus>` block to `analytics-settings.xml` and published the
    port (loopback-only, matching every other analytics port).

## Residual risks not yet covered

- No Grafana dashboard was built for the "Data platform health" operational
  view (plan section 21.6 explicitly allows Metabase *or* Grafana here) —
  Prometheus itself has the real metrics and 4 working alert rules; the
  Grafana image was not pulled to avoid further pressure on an
  already-constrained local disk during this session.
- Kafka Connect per-task throughput/latency metrics are not available
  (no JMX exporter in this image build); connector up/down is covered via
  REST polling instead.
- Redpanda consumer-lag-growing, replication-slot-inactive,
  retained-WAL-above-threshold, and Metabase-unable-to-query-mart alerts
  are not implemented — would need additional exporter metrics beyond
  what was built this pass.
- No incompatible-schema-change drill or deliberate WAL-pressure drill was
  run against the shared local source database (judged too risky/invasive
  against data another concurrent session may depend on).
- TTL expiry (30-day raw, 180-day control) was not time-accelerated or
  observed actually deleting rows.
- No sustained-load/throughput performance baseline exists beyond the
  single-business-journey data volume measured in
  [c2-resource-baseline.md](c2-resource-baseline.md).
- Reversal/refund fee-revenue behavior is exercised implicitly by
  business-e2e's cancelled-withdraw case but not asserted as a dedicated
  C2-level test.

Plan 58's residual-risks section (45) already anticipates that a local,
disposable C2 cannot prove production-scale capacity, managed-cloud
operations, or audited revenue recognition — none of the above changes
that baseline; they are specific, currently-open follow-ups within the
local-learning scope the plan does target.
