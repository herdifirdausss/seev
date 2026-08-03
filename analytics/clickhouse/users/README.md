# ClickHouse users

Roles are created by `clickhouse/migrations/005-roles.sql`; local users are
created by `clickhouse/scripts/init-warehouse.sh` from required secret files.
`metabase_bi` receives `bi_readonly`, `analytics_dbt` receives
`dbt_transform`, and `analytics_reconciliation` receives the read/control
roles. No credentials are committed.
