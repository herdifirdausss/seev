# C2 Ledger vertical-slice evidence

Status: code and fixtures committed; runtime proof pending.

The first slice now contains:

```text
allowlisted Ledger tables
  -> explicit publication and least-privilege replication role
  -> Debezium snapshot/WAL connector with heartbeat and restart-safe slot
  -> Redpanda topics with one partition and seven-day retention contract
  -> ClickHouse raw transport identity and deduplicated staging views
  -> typed Ledger entry/transaction facts
  -> fee-account revenue fact and daily marts
  -> debit-credit dbt test and read-only reconciliation CLI
  -> curated dashboard manifest with freshness/status labels
```

The remaining proof is the snapshot, streaming, restart, duplicate, immutable
delete, ClickHouse role, dbt full-refresh, reconciliation, and OLTP-outage
drills listed in Plan 58 T4–T11. No runtime result is represented as passed by
this artifact.
