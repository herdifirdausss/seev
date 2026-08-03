# Metabase setup

1. Start `analytics-ui` after `analytics-core` and warehouse/dbt have reached a
   known state.
2. Configure one ClickHouse database connection to `clickhouse:8123` using the
   `metabase_bi` user; do not add any PostgreSQL connection.
3. Disable the sample database and anonymous tracking.
4. Create the collections documented in `../collections/README.md`.
5. Import the six manifests under `../dashboards/` as versioned questions and
   dashboards.
6. Verify the ClickHouse user cannot read `raw` or restricted `staging`, and
   cannot create/insert/update/drop.
7. Verify every money question groups or filters by `currency`, and every
   dashboard displays freshness and reconciliation status.

The local Metabase application database is disposable. Dashboard definitions
are the versioned source of truth; a Metabase outage must not affect Connect,
ClickHouse ingestion, dbt, reconciliation, or OLTP.
