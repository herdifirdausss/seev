# Performance baseline — 2026-07

Status: **preliminary evidence; not a production-capacity claim**

> **Superseded for numerical claims:** [`2026-07-31-baseline.md`](2026-07-31-baseline.md)
> runs the staircase, three confirmation runs, spike, and soak this report's
> own text says B0 still requires, for S1 (P2P), S3 (webhook burst), and S4
> (mixed) — plus a real B1 hot-account experiment, a real B2 partitioning
> growth-curve study, a real B3 routing-cache experiment, and a Phase 5
> resource-elasticity check. It also root-causes and fixes the reason S2
> (hot-account)'s own soak failed. Read that report for current evidence;
> this one remains as the historical record of the 2026-07-28 harness state.

> **Historical measurement note:** This report records the harness as it ran
> on 2026-07-28. The current harness self-seeds W1/W2/W3/W5, gives W2 a pool
> of unique pending intents plus a tagged 10% exact-redelivery stream, gives
> W5 one- and two-account variants, and performs post-run outbox-drain and
> ledger-integrity checks. Those improvements do not retroactively change the
> measurements below; a new report is required for new numbers.

This report records one short, disposable local run for each requested workload.
It is useful for finding broken load paths and for making conservative roadmap
decisions, but it is not the canonical B0 MSSL. B0 still requires a staircase,
three clean confirmation runs, spike/recovery, soak, drain, and full ledger
integrity verification. See the [B0 protocol](../../roadmap/archive/53-b0-load-capacity-gate.md)
and [capacity model](../capacity-model.md).

## A. Environment

### Host and runtime fingerprint

| Field | Measured value |
| --- | --- |
| Host CPU | Apple Silicon; 8 logical CPUs (4 performance + 4 efficiency) |
| Declared load CPU envelope | `local-small`: 4 logical CPUs |
| Host memory | 8 GiB |
| Docker memory reported by daemon | 4,109,217,792 bytes (~3.92 GiB) |
| Docker Engine | `29.1.3` client/server |
| Docker Compose | `v5.0.1` |
| PostgreSQL | `16.14`, `aarch64-unknown-linux-musl` |
| Go | `go1.26.5 darwin/arm64`; service images built with `golang:1.26.5-alpine` |
| k6 | `0.52.0`, pinned image digest from `local-small` |
| Code HEAD | `8d3cd0fe29b5ed46278634198befe22396d35f81` |
| Worktree state | Dirty during measurement; harness patch fingerprint `533b8b0e6cdfc2c4e49b61caad94e52eb0ab45c1b82abd63b80e45151b158b37` |
| Profile | `local-small`, disposable Compose project `seev-load-20260728-baseline-bootstrap-71570b3` |

The worktree was already dirty with unrelated merchant changes when this run
started. The HEAD hash alone therefore cannot reproduce the exact run; the
harness fingerprint above covers the load-profile and scenario fixes used in
the measured runs.

### Dataset

The fixture was synthetic and created inside the disposable load databases. The
initial count cutoff was `2026-07-28T16:39:28Z`, immediately before the final
W1 run.

| Measure | Value |
| --- | ---: |
| PostgreSQL footprint across `seev_load_*` databases | 64 MB |
| Ledger accounts, including system accounts | 66 |
| Initial ledger transactions | 28 |
| Initial ledger entries | 56 |
| Initial ledger outbox rows | 28 |
| Auth users | 9 |
| Pay-in intents | 3 |

The account count is not a capacity-sized dataset. It contains the platform's
system accounts and a few synthetic users used to obtain valid JWT, KYC, and
funded-account journeys.

## B. Scenarios at measurement time

All rates below are workload units per second (WU/s), not ambiguous HTTP RPS.
The k6 executor used an open `constant-arrival-rate` model. Each run lasted ten
seconds at a low rate, with no warm-up or soak window; those limitations are
intentional and are called out again in the decision section.

| Scenario | Workload unit | Main pressure |
| --- | --- | --- |
| Normal P2P transfer (`W1`) | One fee quote plus one authenticated P2P transfer | Gateway, fraud/ledger RPC, account balance update, ledger outbox |
| Hot-account contention (`W5` proxy) | One authenticated top-up-intent creation against the same user | Gateway/pay-in routing and one-user request concentration |
| Webhook burst (`W2`) | One signed VendorService callback for the same pre-settled reference | mTLS callback verification, VendorService inbox, pay-in callback path |
| Mixed journey (`W4`) | One deterministic weighted action: quote, top-up intent, payout create, notification read, or auth profile read | Shared Gateway, Ledger, Payin, Payout, notification, and Auth paths |

Two scenario limitations mattered at measurement time. W2 used repeated
redelivery of one reference because that harness accepted one
`LOAD_TOPUP_REFERENCE`; it was not a unique-intent settlement benchmark. W5
was a top-up-intent proxy, not the locked B0 one-gateway versus two-gateway
system-account lock experiment. Consequently neither historical run can
activate B1.

## C. Numerical results

| Scenario | Offered WU/s | Achieved WU/s | p50 | p95 | p99 | HTTP/business error | Outbox lag* |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Normal P2P (`W1`) | 1.000 | 1.085 | 13.04 ms | 76.94 ms | 92.47 ms | 0 / 11 | 0 s |
| Hot-account proxy (`W5`) | 5.000 | 4.999 | 9.90 ms | 64.40 ms | 132.74 ms | 0 / 50 | 0 s |
| Webhook burst (`W2`) | 5.000 | 5.094 | 0.58 ms | 39.05 ms | 45.95 ms | 0 / 51 | 0 s |
| Mixed journey (`W4`) | 1.000 | 1.099 | 11.15 ms | 29.83 ms | 35.03 ms | 0 / 11 | 0 s |

Percentiles are k6 `http_req_duration` values from the individual run; they are
not averaged across runs. The achieved rate is iteration rate, not a claim that
every iteration produced a settled monetary outcome.

\* The outbox value is the post-campaign ledger snapshot: `0` pending,
processing, or failed ledger outbox rows. It is not a per-run time-series
maximum. The final pay-in snapshot separately contained `52` webhook rows not
in `posted` state, with oldest age approximately `450.068 s`, and `50` pending
top-up intents from W5, with oldest age approximately `36.465 s`. Those facts
prevent this report from claiming end-to-end webhook settlement health.

### Run artifacts

Raw artifacts remain outside Git under the ignored `artifacts/load/` directory.
The committed-size summaries and their SHA-256 hashes are:

| Artifact | SHA-256 |
| --- | --- |
| `20260728-baseline-w1-final/summary.json` | `7b36eb97560fb39692f721711e2b14f447c70b33729a0713c66d50574c8a7e20` |
| `20260728-baseline-w2b/summary.json` | `0f5eaedce354457d7c04c56d3a8aa49cb9051c7c60819a83d00e320b02641979` |
| `20260728-baseline-w4b/summary.json` | `dcea1174edf1d497b6d42664bdfbf01d0022363d8b5160e4c32215763777e59d` |
| `20260728-baseline-w5b/summary.json` | `80e55d3ae719eba421e7fb511e9b290535dbfc3326c620f5b79967b6ce26ea69` |
| bootstrap `manifest.json` | `e3728ca3798392f55e0ba31020ea613cfc4ef6d43354cae88ed72dc3eb7411fb` |

The summaries report `integrity_passed=false` and `gate_passed=false` because
the disposable direct-run path does not perform the post-run ledger verifier
and drain gate. This is a deliberate fail-closed representation, not a hidden
green result.

## D. Bottleneck explanation

### What the evidence supports

The preliminary runs do not show a CPU, connection-pool, or PostgreSQL lock
wait knee at these offered rates:

- The profile exposed a ten-connection database pool per service. The final
  mTLS metric snapshots showed zero in-use connections at scrape time and
  `seev_database_pool_wait_count_total=0` plus zero wait duration for Gateway,
  Ledger, Auth, Payin, Payout, and VendorService.
- A PostgreSQL snapshot showed `0` active sessions waiting on a lock.
  `pg_stat_statements` recorded the two-account balance update 36 times for
  14.182 ms total and the delta update three times for 5.576 ms total. These
  totals are too small to establish contention, but they do not support a lock
  bottleneck at this load.
- Achieved throughput tracked the offered rate for W1, W2, W4, and W5, with
  zero dropped iterations in every reported run. That is a harness-health
  signal, not a saturation knee.

The strongest negative signal is asynchronous state, not resource saturation.
RabbitMQ ended with `ledger.events.audit` at 25 messages and zero consumers,
while notification and fraud queues were empty or actively consumed. Pay-in
also retained 52 non-posted webhook rows. Because W2 intentionally redelivered
one already-settled reference and W5 intentionally created pending intents,
these observations identify an end-to-end evidence gap and possible consumer
backlog, but do not isolate whether the cause is broker topology, worker
ownership, idempotent-state semantics, or test-fixture design.

### What the benchmark cannot conclude

This run cannot establish:

- a maximum sustainable throughput or production SLO;
- a PostgreSQL CPU, disk, WAL, or buffer-cache bottleneck, because those
  time-series were not retained for the run;
- lock-wait percentage for the canonical account-delta statement, because the
  required alternating W5 one-gateway/two-gateway experiment was not run;
- unique webhook settlement capacity, because this W2 run reused one external
  reference;
- payout terminal-state latency at scale, because W4 only exercised a small
  mixed sample and did not run the B0 payout confirmation protocol;
- a safe extrapolation from this Apple Silicon laptop to production hardware.

The low p95/p99 values therefore mean only that these small, fixture-limited
requests completed quickly when accepted. They do not prove that asynchronous
money movement drained correctly or that the system has spare capacity beyond
the offered rates.

## E. Decision

| Track | Decision | Reason |
| --- | --- | --- |
| B1 hot-account sub-sharding | **REJECT** | B0 K11 requires three alternating lock-isolated runs, same-system-account lock-wait evidence, and a one-gateway versus two-gateway delta. This historical run has zero sampled lock waits and only a W5 top-up proxy. |
| B2 ledger partitioning | **REJECT** | The fixture had 28 initial transactions and 56 entries; the ledger database was about 9.7 MB. This is far below the B0 gate of 40 million observed entries or a documented six-month forecast crossing 50 million. |
| B3 routing cache | **REJECT** | W6 resolver stress and the required database-authoritative versus test-double comparison were not run. No 15% database-time, 80% repeated-key, or 15% throughput-improvement gate is established. |

`REJECT` means “do not activate the optimization from this evidence.” It does
not mean the hypothesis is impossible forever. A future B0 run may change a
decision after the exact gate, dataset, traffic shape, and resource evidence
are satisfied.

### Required follow-up before calling B0 complete

1. Run the current self-seeded scenarios against a declared dataset and record
   its hash before warm-up.
2. Run the locked staircase and three confirmation runs for W1–W4, retaining
   CPU, memory, pool, PostgreSQL wait/lock, RabbitMQ, outbox, drain, and ledger
   verifier artifacts.
3. Run W5's existing one-account/two-account variants in the locked alternating
   protocol with lock-wait sampling and a throughput/p95 comparison.
4. Preserve the harness's post-run drain and integrity evidence alongside the
   scenario-specific measurements; a 2xx callback alone is not financial
   correctness evidence.
5. Resolve the observed audit queue/non-posted webhook state before publishing
   any green capacity claim.

Until those follow-ups are complete, this document is a useful baseline and a
decision record, not a promise of system capacity.
