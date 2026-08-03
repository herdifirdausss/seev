# C2 resource baseline

Status: target guardrails committed; measurements intentionally not run during
code-only authoring.

| Component | Local guardrail | Measured usage |
| --- | ---: | --- |
| Redpanda | 1 vCPU, 768 MiB default; low-memory 512 MiB | pending |
| Kafka Connect/Debezium | 1 vCPU, 768 MiB | pending |
| ClickHouse | 1 vCPU, 768 MiB container; 1 GiB query cap | pending |
| dbt | 1 thread | pending |
| Metabase | optional, 1 vCPU, 768 MiB, JVM 512 MiB | pending |

The profile is opt-in and every host port is loopback-only. A future measured
baseline must record host CPU, memory, disk, image versions, row counts, CDC
lag, and the normal/peak query targets before any capacity claim is made.
