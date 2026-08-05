# C2 resource baseline

Status: measured across two sessions on 2026-08-05: first the
`analytics-core` profile only, then `analytics-ui` (Metabase) plus the app's
Prometheus. Single local samples on a shared/contended host, not a
statistically robust capacity claim — see caveats below.

| Component | Local guardrail | Measured usage (steady state) |
| --- | ---: | ---: |
| Redpanda | 1 vCPU, 640 MiB (lowered from 768 MiB — see note) | ~180-200 MiB / 768 MiB cap, ~2-17% of 1 vCPU |
| Kafka Connect/Debezium (3 connectors running) | 1 vCPU, 768 MiB | ~320-554 MiB / 768 MiB cap, ~9-17% of 1 vCPU |
| ClickHouse | 1 vCPU, 768 MiB container; 1 GiB query cap | ~420-587 MiB / 768 MiB cap, ~6-17% of 1 vCPU |
| dbt | 1 thread | not separately measured (one-shot `run --rm` invocations, sub-second each) |
| Metabase | optional, 1 vCPU, 768 MiB, JVM 512 MiB | **~694 MiB / 768 MiB cap (~90%), ~26% of 1 vCPU** — running close to its configured ceiling with 6 dashboards/23 cards loaded; worth revisiting `ANALYTICS_METABASE_JAVA_OPTS` if more dashboards are added |
| metrics-exporter (new) | none set (plain `go run`, not containerized) | negligible — a few MB, single HTTP handler polling ClickHouse + Connect REST on each scrape |
| Prometheus (app's, scraping analytics targets too) | 384 MiB (existing app guardrail) | ~100 MiB / 384 MiB cap, ~1% of 1 vCPU |

Data volume at measurement time: 251+ accounts, 193+ ledger transactions,
418+ ledger entries, 15+ fee quotes, 18+ pay-in intents, 17+ pay-in
webhooks, 24+ payout requests, 39+ payout vendor calls (1400+ raw CDC
events total, growing across two sessions plus real concurrent activity
from another session sharing the same source database) — real business
journeys, not a load-test volume.

## Note: Redpanda's `--memory` flag had to be lowered

The originally-committed config passed `--memory 768M` to redpanda while
also capping the container's cgroup memory at 768M — seastar needs its full
`--memory` budget actually available *inside* the container, and cgroup/
seastar bookkeeping overhead ate into that ceiling (redpanda logged
`insufficient physical memory: needed 805306368 available 759169024` and
refused to start). Lowered to 640M default via `ANALYTICS_REDPANDA_MEMORY`
so real usage stays safely under the container's 768M cap.

## Note: host disk, not just container memory, is a real constraint

Mid-session, the host's actual disk filled to the point of causing
intermittent write failures and then a full Docker daemon crash — not
caused by any single container's data volume, but by the cumulative weight
of Docker image pulls/builds across this session (Debezium, ClickHouse,
Redpanda, Maven, Metabase — several hundred MB to a few GB each) landing on
top of an already end-to-end Go build cache that had grown to 9.6GB
(`~/Library/Caches/go-build`, unrelated to analytics specifically — grown
from repeated `scripts/business-e2e.sh` binary builds across many
sessions). Clearing that cache alone freed the host from ~0.6GB to ~14GB
free. This is a real operational lesson for local C2 usage on a
disk-constrained laptop, not an analytics-specific defect: **local Go
build-cache growth and Docker image accumulation both compete for the same
host disk**, and neither is bounded by the resource guardrails in this
file (which only cover container memory/CPU, not host disk).

## Caveats

- Single/few samples, not a percentile/distribution.
- The host had other concurrent Docker workloads throughout both sessions
  (a separate session's own Postgres/Redis/RabbitMQ stacks, at one point
  even replacing the shared source database this repo's connectors were
  reading from) and load spikes above 13 on an 8-CPU/~5.8 GiB-Docker-VM
  machine — absolute numbers may be pessimistic versus an idle host, though
  relative headroom against the configured caps still held except for
  Metabase (see above).
- No load/throughput test was run — this reflects real but small business
  journeys, not production-scale volume, sustained streaming rate, or
  dashboard query concurrency (plan section 37's performance targets are
  still unmeasured — e.g. the <=2s executive-mart / <=3s lifecycle-dashboard
  targets were never timed against a realistic row count).

A future measured baseline should add: host CPU/memory/disk under
sustained synthetic load, CDC lag under write pressure, dashboard query
latency at scale, and a dedicated Metabase memory tuning pass.
