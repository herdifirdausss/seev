# C2 Performance Baseline

Status: implementation targets defined; runtime benchmark evidence is pending.

This document is the evidence location for the C2 throughput, freshness, and
recovery baseline. It deliberately contains no fabricated measurements. The
benchmark must be run against the bounded local profile and a production-like
environment before the C2 gate is closed.

## Measurement scope

Measure the complete one-way path:

```text
PostgreSQL WAL -> Debezium Connect -> Redpanda -> ClickHouse raw/staging -> dbt -> marts
```

The benchmark should use representative Ledger, Payin, and Payout change
volumes, including duplicate delivery, deletes, and a schema-compatible
change. It must record:

- source WAL retained bytes and replication-slot lag;
- connector task health, source-to-Redpanda latency, and records per second;
- Redpanda consumer lag and raw-ingest rows per second;
- ClickHouse raw, staging, core, and mart query/insert latency;
- dbt run duration and model freshness;
- reconciliation runtime and the number of rows/amounts covered by its safe
  cutoff;
- recovery time after a connector, broker, ClickHouse, or dbt interruption;
- resource usage against the limits in `analytics/compose/profiles.md`.

## Acceptance targets

The environment owner must fill in the measured values and attach the command
output or dashboard export before marking these targets as passed:

| Area | Target | Evidence |
| --- | --- | --- |
| Delivery | At-least-once replay produces no duplicate business facts after deduplication | Pending |
| Freshness | Normal steady-state source-to-mart lag is within the agreed reporting SLA | Pending |
| Backlog recovery | A bounded connector outage drains within the agreed recovery window | Pending |
| WAL safety | Retained WAL stays below the configured source safety threshold during the run | Pending |
| Reconciliation | The run completes at a safe common cutoff and reports zero unexplained critical deltas | Pending |
| Resource guardrail | Services remain within the local profile's CPU, memory, and disk bounds | Pending |

Until this table is populated with runtime evidence, this document is an
implementation record rather than an acceptance claim.
