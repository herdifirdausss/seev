# dbt transformations

dbt owns typed staging projections, core facts/dimensions, curated marts, and
financial/data-quality tests. Incremental models use a bounded lookback and
`on_schema_change=fail`; a disposable full refresh is always supported.

The checked-in `profiles.yml` contains no password. The Compose entrypoint reads
the local ClickHouse secret file and exports it only inside the dbt container.
