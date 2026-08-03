# Analytics Compose profiles

The file in this directory is intended to be used directly or alongside the
repository application Compose file.

| Profile | Components | Purpose |
| --- | --- | --- |
| `analytics-core` | Redpanda, Kafka Connect/Debezium, ClickHouse, init jobs, dbt runner | Headless CDC and warehouse learning |
| `analytics-ui` | ClickHouse dependency, Metabase | Curated local dashboards |
| `analytics-ops` | Redpanda Console, ClickHouse dependency | Optional diagnostics |

The default profile does not start any analytics component. All host bindings
are loopback-only and every core service has a bounded CPU/memory setting.
