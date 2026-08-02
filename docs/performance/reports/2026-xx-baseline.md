# Performance Baseline Report Template

> **Status: Template, not measured evidence.** Copy this file to a dated
> report only after a completed run has a fixed commit, dataset hash, retained
> redacted artifacts, and an explicit decision. Placeholder values must never
> be presented as a baseline result.

> **Status:** Draft — not yet a capacity claim  
> **Repository:** `herdifirdausss/seev`  
> **Baseline commit:** `[short-sha]`  
> **Canonical profile:** `local-small`  
> **Report owner:** `[name]`  
> **Execution date:** `2026-XX-XX`

Once copied to a dated filename and completed, this document becomes the
primary, reviewable performance report for its baseline commit.

It must answer five questions:

1. What environment and dataset produced the result?
2. What product journeys were exercised?
3. What numerical result was observed?
4. Why did throughput stop increasing?
5. Which optimization tracks should be activated or rejected?

A result is valid only for the exact commit, data snapshot, resource profile, configuration, and workload definition recorded here. It must not be presented as production capacity.

---

## 0. Executive Summary

### 0.1 Headline result

| Item | Result |
|---|---:|
| Maximum sustainable load, normal P2P | `[TBD] WU/s` |
| Maximum sustainable load, hot account | `[TBD] WU/s` |
| Maximum sustainable load, webhook burst | `[TBD] WU/s` |
| Maximum sustainable load, mixed journey | `[TBD] WU/s` |
| Primary bottleneck | `[lock / CPU / pool / disk / broker / application]` |
| B1 hot-account sub-sharding | `[ACTIVATE / REJECT]` |
| B2 table partitioning | `[ACTIVATE / REJECT]` |
| B3 routing cache | `[ACTIVATE / REJECT]` |

### 0.2 One-paragraph conclusion

`[Write the conclusion after the evidence is complete. State the sustainable load, the first limiting resource, whether correctness remained intact, and why B1/B2/B3 were activated or rejected.]`

Example structure:

> Under the `local-small` profile, the mixed journey remained healthy through `[X] WU/s` and failed the sustainable-load gate at `[Y] WU/s`. Throughput stopped increasing because `[evidence]`, while `[other resources]` still had headroom. No ledger, balance, idempotency, or outbox discrepancies were detected. Based on the focused experiments, B1 is `[decision]`, B2 is `[decision]`, and B3 is `[decision]`.

---

## 1. Claim Boundary

This report may claim only:

- performance observed on the recorded local Docker profile;
- performance of the recorded commit and configuration;
- behavior of the recorded synthetic dataset and workload;
- bottlenecks supported by captured metrics;
- relative A/B differences produced under equivalent conditions.

This report must not claim:

- production TPS;
- cloud capacity;
- internet-facing latency;
- high availability;
- multi-region behavior;
- broker durability under infrastructure failure;
- capacity beyond the measured range;
- behavior on a materially different dataset distribution.

### 1.1 Terminology

Use these terms consistently:

- **WU/s:** complete workload units per second. A workload unit is one defined business journey.
- **HTTP req/s:** aggregate HTTP requests per second. One workload unit may produce several HTTP requests.
- **Offered WU/s:** load requested from the generator.
- **Achieved WU/s:** complete workload units actually finished.
- **MSSL:** maximum sustainable service load: the highest confirmed load that passes every required gate.
- **Knee:** the first load level where additional offered load gives little throughput gain or causes a disproportionate latency/error increase.

Do not label HTTP request rate as business TPS.

---

# A. Environment

## 2. Reproducibility Metadata

### 2.1 Source and configuration

| Field | Value | Collection method |
|---|---|---|
| Repository | `herdifirdausss/seev` | Fixed |
| Commit hash | `[full-sha]` | `git rev-parse HEAD` |
| Short commit hash | `[short-sha]` | `git rev-parse --short HEAD` |
| Git status | `[clean / dirty]` | `git status --porcelain` |
| Branch | `[branch]` | `git branch --show-current` |
| Run ID | `[run-id]` | Artifact manifest |
| Resource profile | `local-small` | Profile manifest |
| Resource profile hash | `[sha256]` | Artifact manifest |
| Compose/config hash | `[sha256]` | Artifact manifest |
| Dataset seed/hash | `[seed/hash]` | Seed manifest |
| Scenario revision/hash | `[sha256]` | k6 scenario files |
| Report generator version | `[version/sha]` | Tool output |

A dirty working tree invalidates a canonical run unless the diff is captured and hashed.

### 2.2 Host environment

| Field | Value | Command/evidence |
|---|---:|---|
| OS | `[TBD]` | `uname -a` |
| CPU model | `[TBD]` | `sysctl -n machdep.cpu.brand_string` or `lscpu` |
| Logical CPU available to Docker | `4` | Docker/profile inspection |
| Host logical CPU | `[TBD]` | `sysctl -n hw.logicalcpu` or `nproc` |
| Docker memory limit | `4096 MiB` | Docker Desktop/resource settings |
| Host memory | `[TBD]` | `sysctl -n hw.memsize` or `free -h` |
| Docker version | `[TBD]` | `docker version --format '{{.Server.Version}}'` |
| Docker Compose version | `[TBD]` | `docker compose version --short` |
| Storage type | `[SSD/NVMe/other]` | Host information |
| Available disk before run | `[TBD]` | `df -h` |
| Background workload | `[none/list]` | Operator declaration |
| Power mode | `[AC/battery]` | Operator declaration |

### 2.3 Runtime and dependencies

| Component | Version/config |
|---|---|
| Go | `[TBD]` |
| PostgreSQL | `[TBD]` |
| Redis | `[TBD]` |
| RabbitMQ | `[TBD]` |
| k6 | `0.52.0` or `[actual]` |
| Docker | `[TBD]` |
| Docker Compose | `[TBD]` |
| Database max connections | `[TBD]` |
| Application pool max open | `[TBD per service]` |
| Application pool max idle | `[TBD per service]` |
| Outbox polling interval | `[TBD]` |
| Outbox batch size | `[TBD]` |
| PostgreSQL shared buffers | `[TBD]` |
| PostgreSQL max connections | `[TBD]` |

Capture versions from running containers rather than relying only on image tags.

Suggested commands:

```bash
git rev-parse HEAD
git status --porcelain

docker version
docker compose version

go version
docker compose exec -T postgres psql -U postgres -Atc 'select version();'
docker compose exec -T redis redis-server --version
docker compose exec -T rabbitmq rabbitmq-diagnostics server_version
```

## 3. Resource Envelope

Record both configured limits and observed peaks.

| Component | CPU limit | Memory limit | Peak CPU | Peak memory | Restarts/OOM |
|---|---:|---:|---:|---:|---:|
| PostgreSQL | `[TBD]` | `[TBD]` | `[TBD]%` | `[TBD] MiB` | `[0/TBD]` |
| Redis | `[TBD]` | `[TBD]` | `[TBD]%` | `[TBD] MiB` | `[0/TBD]` |
| RabbitMQ | `[TBD]` | `[TBD]` | `[TBD]%` | `[TBD] MiB` | `[0/TBD]` |
| Auth service | `[TBD]` | `[TBD]` | `[TBD]%` | `[TBD] MiB` | `[0/TBD]` |
| Payment service | `[TBD]` | `[TBD]` | `[TBD]%` | `[TBD] MiB` | `[0/TBD]` |
| Ledger service | `[TBD]` | `[TBD]` | `[TBD]%` | `[TBD] MiB` | `[0/TBD]` |
| Other services | `[TBD]` | `[TBD]` | `[TBD]%` | `[TBD] MiB` | `[0/TBD]` |
| Load generator | `[TBD]` | `[TBD]` | `[TBD]%` | `[TBD] MiB` | `[0/TBD]` |

The load generator must have enough headroom. A saturated generator makes the service appear healthier than it is.

## 4. Dataset

### 4.1 Dataset summary

| Field | Value |
|---|---:|
| Seed name | `[TBD]` |
| Seed hash | `[TBD]` |
| Random seed | `[TBD]` |
| Total accounts | `[TBD]` |
| User accounts | `[TBD]` |
| System/gateway accounts | `[TBD]` |
| Initial ledger entries | `[TBD]` |
| Initial transfers | `[TBD]` |
| Initial pay-ins | `[TBD]` |
| Initial payouts | `[TBD]` |
| Initial outbox rows | `[TBD]` |
| Initial webhook rows | `[TBD]` |
| Database size before run | `[TBD]` |
| Largest table | `[TBD]` |
| Largest index | `[TBD]` |
| Balance distribution | `[uniform/skewed + parameters]` |
| Transaction age distribution | `[TBD]` |
| Hot-key distribution | `[TBD]` |

### 4.2 Dataset tiers

Keep **load rate** and **dataset size** as separate axes.

| Dataset tier | Accounts | Initial ledger entries | Purpose |
|---|---:|---:|---|
| D0 smoke | 1,000 | 100,000 | Functional smoke and harness validation |
| D1 baseline | 10,000 | 1,000,000 | Canonical baseline |
| D2 medium | 100,000 | 5,000,000 | Query/index growth curve |
| D3 large | 1,000,000 | `[planned]` | Optional capacity study on a larger resource profile |

The canonical B0 baseline should use one named tier consistently. Do not combine results from different dataset tiers into one capacity curve.

### 4.3 Pre-run integrity snapshot

Capture before every canonical run:

| Check | Expected | Actual |
|---|---:|---:|
| Sum of balances by currency | Stable expected total | `[TBD]` |
| Unbalanced journal entries | `0` | `[TBD]` |
| Duplicate idempotency records | `0` | `[TBD]` |
| Pending outbox older than threshold | `0` | `[TBD]` |
| Unexpected pending payouts | `0` | `[TBD]` |
| Broken account-to-ledger references | `0` | `[TBD]` |

---

# B. Scenarios

## 5. Scenario Contract

Each scenario must define a complete business journey. A request that merely reaches one endpoint is not automatically one workload unit.

### 5.1 Scenario matrix

| ID | Scenario | Workload unit | Main pressure |
|---|---|---|---|
| S1 | Normal P2P transfer | Quote/validation → transfer → terminal result/read | Normal transaction path, DB writes, ledger, outbox |
| S2 | Hot-account contention | Concurrent transfers sharing one constrained account | Row locks, lock queues, fairness, retry behavior |
| S3 | Webhook burst | Signed webhook delivery including duplicate/redelivery traffic | Idempotency, burst absorption, outbox/consumer drain |
| S4 | Mixed pay-in, transfer, payout | Weighted end-to-end product mix | Cross-service saturation and realistic resource competition |

## 6. S1 — Normal P2P Transfer

### Purpose

Measure the healthy transfer path without intentional key contention.

### Journey

1. Select sender and recipient from a sufficiently large, disjoint account pool.
2. Request any required fee quote or validation.
3. Submit the P2P transfer with a unique idempotency key.
4. Read or poll until the transaction reaches its expected terminal state.
5. Record journey completion only after the terminal state is observed.
6. Let the outbox and downstream projection drain.
7. Run integrity verification.

### Data rules

- Sender selection must avoid accidental hot accounts.
- The same idempotency key must be retried only in a designated retry sub-test.
- Amount distribution must be fixed and documented.
- Accounts must have sufficient funds throughout the run or be reset between runs.

### Required metrics

- offered and achieved WU/s;
- HTTP req/s;
- p50, p95, and p99 journey latency;
- endpoint latency by operation;
- application errors and business rejections separately;
- database CPU and top SQL;
- connection-pool usage and wait;
- outbox oldest age and drain time;
- integrity discrepancies.

## 7. S2 — Hot-Account Contention

### Purpose

Determine whether one shared account creates a lock-bound throughput ceiling and whether B1 hot-account sub-sharding is justified.

### Variants

- **A: One gateway/system account**
- **B: Two equivalent gateway/system accounts with deterministic routing**

All other settings must remain equivalent.

### Journey

1. Generate concurrent money movements that share the selected constrained account.
2. Complete the same correctness checks as the normal transfer path.
3. Capture PostgreSQL lock waits and blocked-session evidence.
4. Alternate A and B runs to reduce time-order bias.
5. Repeat each variant at least three times at the relevant load levels.

### Required evidence

- percentage of sampled execution time attributable to the canonical balance-update lock wait;
- concentration of waits on the same system-account dependency;
- achieved WU/s and p95 difference between A and B;
- CPU and pool headroom during the lock-bound condition;
- identical integrity outcome across variants;
- transaction retry/deadlock counts;
- lock wait histogram, not only an average.

### Valid B1 experiment

The two-account variant is evidence for B1 only when it changes the dependency being tested—not when it also changes pool size, resource limits, dataset size, or application code unrelated to routing.

## 8. S3 — Webhook Burst

### Purpose

Measure burst absorption, idempotent handling, and time to drain asynchronous work.

### Journey

1. Pre-create valid target records.
2. Deliver correctly signed webhooks.
3. Include a fixed redelivery ratio, recommended `10%`.
4. Reuse event identity only for the redelivery subset.
5. Observe accepted, duplicate, rejected, and failed outcomes separately.
6. Continue measurement until all resulting outbox and queue work drains.
7. Verify exactly one financial effect per logical event.

### Load shape

Use both:

- a steady staircase to find sustainable processing rate;
- a short burst above the steady MSSL to measure backlog growth and recovery.

### Required metrics

- ingress HTTP req/s;
- unique logical events/s;
- duplicate deliveries/s;
- accepted and rejected response rate;
- p50/p95/p99 handler latency;
- queue depth and oldest-message age;
- outbox pending count and oldest age;
- time to return to pre-burst backlog;
- duplicate financial effects;
- consumer retry/dead-letter counts.

## 9. S4 — Mixed Pay-In, Transfer, and Payout

### Purpose

Measure the system while product journeys compete for the same database, pools, broker, and background workers.

### Canonical mix

Use the B0 product mix as the source of truth. A recommended representation is:

| Share | Journey |
|---:|---|
| 35% | Fee quote plus completed P2P transfer |
| 20% | Read operations |
| 20% | Pay-in/top-up plus webhook completion |
| 15% | Payout creation plus terminal-state observation |
| 5% | Notification-related operation |
| 5% | Login/profile-related operation |

A journey share is based on completed workload units, not raw HTTP request count.

### Required metrics

In addition to the global numbers, report per-journey:

- achieved journey rate;
- p50/p95/p99 latency;
- error and rejection rate;
- downstream lag;
- resource contribution where observable.

A mixed test can hide a failing minority journey behind healthy aggregate percentiles. Therefore, the overall gate and every critical financial journey gate must pass.

---

## 10. Load Methodology

### 10.1 Run phases

Every canonical run must have explicit phases:

1. **Reset/restore** known dataset.
2. **Preflight integrity check**.
3. **Warm-up** until caches, pools, and background workers stabilize.
4. **Steady measurement window**.
5. **Stop ingress**.
6. **Drain** outbox and broker work.
7. **Post-run integrity check**.
8. **Artifact finalization and hashing**.

Warm-up data must not be mixed into steady-state percentiles.

### 10.2 Discovery staircase

Start with the established sequence:

```text
1, 2, 5, 10, 20, 40, 80 WU/s
```

Continue by doubling only while the run is healthy:

```text
160, 320, 640, 1,000 WU/s, ...
```

Do not jump directly to 10,000, 100,000, or 1,000,000 WU/s on `local-small`.

Stop increasing when any of these occurs:

- achieved load is below `99%` of offered load;
- dropped iterations persist;
- error rate exceeds the gate;
- critical p95/p99 exceeds its latency gate;
- pool saturation persists;
- outbox or queue lag does not recover;
- lock wait dominates the tested path;
- host, database, service, or generator CPU is saturated;
- memory pressure, restart, swap, or OOM occurs;
- integrity verification fails.

### 10.3 Confirmation and soak

For the candidate MSSL:

- run at least three independent `15-minute` confirmations;
- restore equivalent initial state between runs;
- require all runs to pass;
- run a `60-minute` soak at approximately `70%` of the confirmed MSSL;
- run one controlled spike/burst test;
- preserve each run separately.

### 10.4 Gate template

| Gate | Required |
|---|---:|
| Achieved/offered WU/s | `>= 99%` |
| Technical error rate | `< 0.5%` |
| Dropped iterations | `0` during confirmed steady state |
| Pool in-use | `< 80%` for at least 95% of samples |
| Outbox oldest age | `<= 10 s` during healthy steady state |
| Outbox drain after ingress stops | `<= 30 s` |
| Queue drain after ingress stops | `<= 60 s` |
| Ledger/balance discrepancies | `0` |
| Duplicate financial effects | `0` |
| Unexpected container restarts/OOM | `0` |

Add scenario-specific latency gates before executing the canonical run. Do not select them after seeing the result.

---

# C. Numerical Results

## 11. Primary Results

### 11.1 Executive comparison

| Scenario | Offered WU/s | Achieved WU/s | HTTP req/s | p50 | p95 | p99 | Error | Dropped | Max outbox lag | Drain time | Gate |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|
| P2P normal | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]%` | `[TBD]` | `[TBD]` | `[TBD]` | `[PASS/FAIL]` |
| Hot account | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]%` | `[TBD]` | `[TBD]` | `[TBD]` | `[PASS/FAIL]` |
| Webhook burst | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]%` | `[TBD]` | `[TBD]` | `[TBD]` | `[PASS/FAIL]` |
| Mixed journey | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]%` | `[TBD]` | `[TBD]` | `[TBD]` | `[PASS/FAIL]` |

### 11.2 Staircase result

| Scenario | Offered WU/s | Achieved WU/s | Achievement | p95 | p99 | Error | Pool peak | DB CPU | Lock wait | Outbox oldest | Verdict |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|
| `[S1/S2/S3/S4]` | `1` | `[TBD]` | `[TBD]%` | `[TBD]` | `[TBD]` | `[TBD]%` | `[TBD]%` | `[TBD]%` | `[TBD]` | `[TBD]` | `[healthy/fail]` |
| `[S1/S2/S3/S4]` | `2` | `[TBD]` | `[TBD]%` | `[TBD]` | `[TBD]` | `[TBD]%` | `[TBD]%` | `[TBD]%` | `[TBD]` | `[TBD]` | `[healthy/fail]` |
| `[continue]` |  |  |  |  |  |  |  |  |  |  |  |

Store the complete per-run table in the evidence directory. This document should contain the decisive levels: last healthy, first unhealthy, confirmation, spike, and soak.

## 12. Per-Journey Latency

| Scenario/journey | Rate | p50 | p95 | p99 | Error/rejection | Notes |
|---|---:|---:|---:|---:|---:|---|
| P2P quote | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]%` | |
| P2P transfer | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]%` | |
| Transfer terminal read | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]%` | |
| Pay-in creation | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]%` | |
| Webhook completion | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]%` | |
| Payout creation | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]%` | |
| Payout terminal read | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]%` | |

Do not average percentile values from separate runs. Show each run or calculate percentiles from a valid merged raw distribution only when the aggregation method is documented.

## 13. Resource and Saturation Evidence

### 13.1 Resource-at-load table

| Scenario/load | App CPU | DB CPU | Broker CPU | App memory | DB memory | Pool in-use | Pool wait | DB active | DB waiting | Disk latency | Broker depth |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `[last healthy]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]%` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` |
| `[first unhealthy]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]%` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` |

### 13.2 PostgreSQL evidence

| Rank | Query fingerprint | Calls/s | Mean | p95/representative | Total DB time | Rows/call | Lock relation |
|---:|---|---:|---:|---:|---:|---:|---|
| 1 | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]%` | `[TBD]` | `[TBD]` |
| 2 | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]%` | `[TBD]` | `[TBD]` |

Capture at minimum:

- `pg_stat_statements`;
- active and waiting sessions;
- blocked/blocked-by relationships;
- lock type and relation;
- transaction age;
- database/table/index sizes;
- query plans for the suspected limiting SQL;
- checkpoint/write evidence when disk is suspected.

### 13.3 Asynchronous pipeline

| Load | Outbox created/s | Outbox processed/s | Pending peak | Oldest age peak | Broker depth peak | Consumer rate | Drain time |
|---:|---:|---:|---:|---:|---:|---:|---:|
| `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` |

A flat HTTP latency result is not sufficient when outbox or broker backlog grows without bound.

## 14. Correctness Result

| Invariant | Before | After drain | Difference | Verdict |
|---|---:|---:|---:|---|
| Total money by currency | `[TBD]` | `[TBD]` | `[TBD]` | `[PASS/FAIL]` |
| Unbalanced journals | `0` | `[TBD]` | `[TBD]` | `[PASS/FAIL]` |
| Duplicate financial effects | `0` | `[TBD]` | `[TBD]` | `[PASS/FAIL]` |
| Missing ledger effects | `0` | `[TBD]` | `[TBD]` | `[PASS/FAIL]` |
| Stale outbox after drain | `0` | `[TBD]` | `[TBD]` | `[PASS/FAIL]` |
| Stale pending payouts | `0` | `[TBD]` | `[TBD]` | `[PASS/FAIL]` |
| Idempotency mismatch | `0` | `[TBD]` | `[TBD]` | `[PASS/FAIL]` |

A performance result with a failed financial invariant is invalid, regardless of throughput.

---

## 15. Resource-to-Throughput Capacity Study

The question “how much TPS can a resource envelope handle?” must be answered as a controlled curve, not as one large target.

### 15.1 Separate the three dimensions

1. **Load rate:** WU/s offered to the same environment and dataset.
2. **Dataset scale:** account and transaction volume with the same load/resource profile.
3. **Resource envelope:** CPU and memory assigned while workload and dataset remain fixed.

Changing more than one dimension at a time prevents causal interpretation.

### 15.2 Proposed resource profiles

Create separate, versioned profiles rather than editing `local-small` in place.

| Profile | Docker CPU | Docker memory | Intended use |
|---|---:|---:|---|
| `local-2c-2g` | 2 | 2 GiB | Lower-bound efficiency |
| `local-small` | 4 | 4 GiB | Canonical baseline |
| `local-8c-8g` | 8 | 8 GiB | Scale-up comparison |
| `dedicated-host-*` | `[TBD]` | `[TBD]` | Optional high-load study |

The host must physically provide the advertised resources. Do not compare an `8c` profile on a host that is already CPU constrained.

### 15.3 Capacity curve

| Profile | Scenario | Dataset | MSSL WU/s | HTTP req/s | p95 | DB CPU | App CPU | Peak memory | WU/s/vCPU | WU/s/GiB | Bottleneck |
|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|
| `local-2c-2g` | Mixed | D1 | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` |
| `local-small` | Mixed | D1 | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` |
| `local-8c-8g` | Mixed | D1 | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` | `[TBD]` |

Also report:

- CPU milliseconds per completed workload unit;
- database time per workload unit;
- allocations/GC cost where available;
- pool wait per workload unit;
- lock-wait time per workload unit;
- outbox events processed per second;
- marginal throughput gained per additional vCPU/GiB.

### 15.4 Interpreting 1,000 / 10,000 / 100,000 / 1,000,000

Use these numbers for the right dimension:

| Number | Appropriate interpretation |
|---:|---|
| 1,000 | Candidate offered WU/s only after lower staircase levels pass; or small account dataset |
| 10,000 | Account count or total completed workload units; not an automatic local TPS target |
| 100,000 | Account count, initial transactions, or long-run completed workload units |
| 1,000,000 | Dataset/account/ledger scale or accumulated workload units; requires storage/time planning |
| 10,000+ WU/s | Separate dedicated-load-generator and host study, not the first local baseline |

A laptop test at one million offered TPS would primarily measure dropped generator work, host scheduling, and loopback/container overhead. It would not prove one million production TPS.

### 15.5 Forecast boundary

- Interpolate only inside the measured range.
- Treat extrapolation as a hypothesis.
- Do not extrapolate beyond approximately `2x` the largest measured healthy point.
- Apply a safety factor before turning a benchmark result into a planning limit.
- Validate the forecast on a separate profile before presenting it as a scaling trend.

---

# D. Bottleneck Explanation

## 16. Bottleneck Analysis Template

For each scenario, answer these in order.

### 16.1 Where did throughput stop scaling?

- Last healthy offered load: `[TBD] WU/s`
- Last healthy achieved load: `[TBD] WU/s`
- First unhealthy offered load: `[TBD] WU/s`
- First unhealthy achieved load: `[TBD] WU/s`
- Throughput gain from the previous step: `[TBD]%`
- p95 increase from the previous step: `[TBD]%`
- Error/drop increase: `[TBD]`

### 16.2 What resource or dependency saturated first?

Choose the primary cause only when evidence supports it:

#### Lock-bound

Evidence should include:

- blocked sessions and lock wait duration increased with load;
- waits concentrated on the canonical balance row/table/index;
- CPU and pool still had headroom;
- splitting the contended dependency materially improved throughput or p95;
- integrity remained equal.

#### CPU-bound

Evidence should include:

- the limiting process stayed near its CPU quota;
- run queue/throttling increased;
- throughput scaled with additional CPU under an otherwise equivalent profile;
- pool, locks, disk, and broker did not saturate first.

#### Connection-pool-bound

Evidence should include:

- pool in-use remained near the configured maximum;
- pool wait/timeout increased;
- database still had capacity;
- a controlled pool-size experiment changed the result without merely moving the bottleneck.

A full pool can be a symptom of slow database work rather than the root cause.

#### Disk/checkpoint-bound

Evidence should include:

- storage latency or I/O wait increased;
- write/checkpoint evidence aligned with latency spikes;
- top SQL or WAL volume explains the writes;
- CPU, locks, and pool do not better explain the knee.

#### Broker/consumer-bound

Evidence should include:

- publish/consume rate ceiling;
- queue depth and oldest-message age grew during steady ingress;
- HTTP success remained deceptively healthy while drain time failed;
- consumer or broker resource saturation was observed.

#### Application-bound

Evidence may include:

- profiles showing hot code or serialization cost;
- internal worker queue saturation;
- GC/allocation pressure;
- expensive network fan-out;
- a controlled code-path A/B result.

### 16.3 Evidence chain

Write the conclusion as an evidence chain:

> At `[load]`, achieved throughput flattened from `[A]` to `[B]` while p95 rose from `[C]` to `[D]`. PostgreSQL lock-wait samples increased to `[E]%` of observed execution time and `[F]%` targeted the same system account. Database CPU remained `[G]%`, application CPU `[H]%`, and pool use below `[I]%`. The two-account variant improved sustainable throughput by `[J]%` with zero integrity differences. Therefore the first limiting factor was hot-account row contention, not CPU or connection capacity.

Avoid wording such as “PostgreSQL is slow” without a causal chain.

## 17. Per-Scenario Explanation

### 17.1 Normal P2P

`[Explain the knee, top SQL, pool behavior, outbox behavior, and correctness evidence.]`

### 17.2 Hot account

`[Explain lock concentration, A/B results, CPU/pool headroom, and whether B1 criteria were met.]`

### 17.3 Webhook burst

`[Explain ingress capacity, duplicate behavior, backlog peak, consumer rate, and recovery time.]`

### 17.4 Mixed journey

`[Explain which journey degraded first and which shared dependency caused it. Include per-journey evidence.]`

## 18. What Cannot Be Concluded from This Local Benchmark

At minimum, state that this test does not establish:

- production throughput;
- behavior under real network latency;
- cloud block-storage and managed-database characteristics;
- multi-node coordination cost;
- failover behavior;
- noisy-neighbor behavior;
- regional or multi-region consistency;
- production traffic distribution;
- security perimeter and rate-limit behavior at internet scale;
- long-term table/index growth beyond tested datasets;
- vendor latency and failure patterns;
- performance on a different CPU architecture or Docker runtime.

Local results are still valuable for:

- detecting regressions on the same profile;
- finding obvious bottlenecks;
- comparing equivalent A/B designs;
- validating correctness under concurrency;
- producing a defensible optimization decision.

---

# E. Decisions

## 19. Decision Table

A published baseline must end with `ACTIVATE` or `REJECT`, not `PENDING`.

| Track | Decision | Confidence | Decisive evidence |
|---|---|---|---|
| B1 hot-account sub-sharding | `[ACTIVATE / REJECT]` | `[low/medium/high]` | `[TBD]` |
| B2 partitioning | `[ACTIVATE / REJECT]` | `[low/medium/high]` | `[TBD]` |
| B3 routing cache | `[ACTIVATE / REJECT]` | `[low/medium/high]` | `[TBD]` |

## 20. B1 — Hot-Account Sub-Sharding

### Activation criteria

Activate only when all required evidence is present:

- lock wait is at least `20%` of sampled execution time on the canonical balance-update path;
- at least `80%` of relevant waits share the same system-account dependency;
- the equivalent two-account experiment produces at least:
  - `25%` higher sustainable throughput, **or**
  - `30%` better p95;
- CPU remains below `70%` at the decisive point;
- pool usage remains below `80%`;
- integrity is unchanged;
- the result is consistent across at least three alternating A/B runs.

### Decision

**`[ACTIVATE / REJECT]`**

### Rationale

`[Fill with direct evidence. If criteria are not met, reject rather than keeping the optimization because it sounds scalable.]`

### Consequence

If activated:

- write a separate design ADR;
- define routing determinism and migration behavior;
- preserve double-entry and idempotency invariants;
- add operational observability and rollback;
- rerun the same baseline after implementation.

If rejected:

- keep the simpler single-account model;
- record the observed margin and the signal that would justify reopening B1.

## 21. B2 — Partitioning

### Activation criteria

Activate only when real or near-term scale supports it, for example:

- observed equivalent inventory is at least approximately `40 million` rows, or
- a defensible six-month forecast exceeds approximately `50 million` rows;

and at least one measured symptom exists:

- unacceptable query/index growth;
- maintenance or vacuum pressure;
- retention/deletion pain;
- clearly improved representative query behavior with partitioning;
- operational need that outweighs partition-management complexity.

Synthetic extrapolation from a small dataset is not sufficient by itself.

### Expected baseline decision

**`REJECT`**, unless the measured inventory/forecast and query evidence cross the stated threshold.

### Final decision

**`[ACTIVATE / REJECT]`**

### Rationale

`[State current rows, growth forecast, size-curve evidence, and why partitioning complexity is or is not justified.]`

## 22. B3 — Routing Cache

### Activation criteria

Activate only when:

- routing/resolver SQL accounts for at least `15%` of database time;
- repeated-key cacheability is at least `80%`;
- an equivalent memoized/test-double experiment improves:
  - sustainable throughput by at least `15%`, **or**
  - p95 by at least `20%`;
- output equivalence is verified;
- results are repeatable.

### Decision

**`[ACTIVATE / REJECT]`**

### Rationale

`[State resolver DB share, key reuse, A/B performance, invalidation requirements, and correctness evidence.]`

### Consequence

If activated:

- define key, value, TTL, invalidation, stale-read behavior, fallback, and metrics;
- keep the database as source of truth;
- add cache correctness tests and failure-mode tests.

If rejected:

- retain the direct resolver path and avoid new invalidation complexity.

---

## 23. Evidence Index

The narrative report remains:

```text
docs/performance/reports/2026-xx-baseline.md
```

Small machine-generated summaries may be kept under:

```text
docs/performance/reports/2026-xx-<short-sha>/
```

Raw evidence remains ignored under:

```text
artifacts/load/<run-id>/
```

### 23.1 Run evidence

| Run | Scenario | Load/profile | Raw artifact hash | Small summary |
|---|---|---|---|---|
| `[run-id]` | `[S1]` | `[TBD]` | `[sha256]` | `[relative path]` |
| `[run-id]` | `[S2-A]` | `[TBD]` | `[sha256]` | `[relative path]` |
| `[run-id]` | `[S2-B]` | `[TBD]` | `[sha256]` | `[relative path]` |
| `[run-id]` | `[S3]` | `[TBD]` | `[sha256]` | `[relative path]` |
| `[run-id]` | `[S4]` | `[TBD]` | `[sha256]` | `[relative path]` |

Expected evidence per canonical run:

```text
manifest.json
k6-summary.json
k6-timeseries.*
resource-timeseries.*
postgres-summary.*
pool-summary.*
broker-summary.*
outbox-summary.*
integrity-before.*
integrity-after.*
decision-input.*
```

Do not commit credentials, tokens, account identifiers, full payloads, or large raw time series.

---

# 24. Benchmark Readiness Plan

The report should not be populated with numbers until the harness proves that it measures the intended journeys.

## Phase 0 — Close Measurement Gaps

### 24.1 Canonical seed and reset

- Replace or extend emit-only seed generation with an owner-service/API adapter that creates valid business state.
- Produce a machine-readable seed manifest with counts, random seed, schema version, and content hash.
- Add deterministic reset/restore between canonical runs.
- Make D0, D1, and D2 datasets reproducible.
- Verify balances, journals, outbox, webhooks, and payout state before starting load.

**Exit criterion:** one command produces or restores a valid named dataset and all preflight checks pass.

### 24.2 Scenario fidelity

- Make S1 complete transfer and terminal read, not only request submission.
- Make S2 support explicit one-gateway and two-gateway variants.
- Make S3 use pre-created valid records and a fixed redelivery ratio.
- Make S4 execute the fixed B0 weights as complete journeys.
- Ensure payout journeys observe terminal status.
- Ensure all scenarios use unique, traceable idempotency keys.
- Emit metrics per journey and operation.

**Exit criterion:** each workload unit maps to a documented business journey and produces the expected financial effect.

### 24.3 Integrity and drain automation

- Invoke pre-run and post-drain integrity checks from the runner.
- Fail the run when any invariant fails.
- Measure outbox oldest age, broker depth, and drain duration.
- Record pending work at the end of the steady window and after drain.
- Separate technical failure, expected business rejection, duplicate delivery, and integrity failure.

**Exit criterion:** a run cannot be marked valid when backlog remains or financial correctness fails.

### 24.4 Evidence collector

Add or verify collection for:

- container CPU, throttling, memory, restart, and OOM;
- application pool in-use, idle, wait, and timeout;
- PostgreSQL activity, waits, locks, top SQL, and sizes;
- Redis and RabbitMQ throughput/backlog;
- outbox creation/processing/oldest age;
- per-operation latency and counts;
- generator CPU, VU usage, and dropped work.

**Exit criterion:** every possible bottleneck category has at least one direct signal.

### 24.5 Report generator

Extend the current small report tooling to:

- validate matching commit/profile/data/scenario hashes;
- reject invalid aggregation;
- identify last healthy and first unhealthy levels;
- preserve per-run percentiles;
- generate tables used by this document;
- calculate B1/B2/B3 decision inputs;
- leave the final narrative and decision rationale for review.

**Exit criterion:** generated summaries can populate Sections A–C and the decision inputs without manual transcription errors.

## Phase 1 — Harness Qualification

Run on D0:

1. lint and validate all scripts;
2. run one low-load sample of every scenario;
3. deliberately send a duplicate webhook;
4. deliberately trigger one invalid request;
5. confirm metric classification;
6. confirm pre/post integrity;
7. confirm raw artifact hashes;
8. confirm no secret or PII is written to reports.

**Exit criterion:** all four scenarios pass at low load and the evidence is internally consistent.

## Phase 2 — Discovery Runs

On `local-small` and D1:

1. run the staircase for S1;
2. run the staircase for S2 one-account;
3. run the staircase for S3 steady load;
4. run the staircase for S4;
5. identify the likely knee for each;
6. inspect the first bottleneck before increasing further.

Use the last healthy and first unhealthy levels to narrow the boundary when useful.

**Exit criterion:** each scenario has a defensible candidate MSSL and suspected bottleneck.

## Phase 3 — Confirmation, Spike, and Soak

For each candidate MSSL:

- three independent 15-minute confirmations;
- one controlled spike;
- one 60-minute soak at approximately 70% MSSL;
- complete drain and integrity verification.

**Exit criterion:** stable repeatability and zero correctness discrepancies.

## Phase 4 — Focused B1/B2/B3 Experiments

### B1

- alternate one-account and two-account runs;
- capture lock concentration;
- keep all other variables equal;
- apply the locked thresholds.

### B2

- run the read/verifier suite at D0, D1, and D2;
- collect database/table/index sizes and representative plans;
- calculate actual growth slope;
- compare to real six-month forecast;
- reject unless both scale and symptoms justify activation.

### B3

- measure resolver SQL share and key cardinality;
- run baseline and equivalent memoized/test-double variants;
- verify outputs;
- apply the locked thresholds.

**Exit criterion:** every track ends in ACTIVATE or REJECT with direct evidence.

## Phase 5 — Resource Elasticity Study

After the canonical baseline is complete:

1. hold scenario and D1 dataset constant;
2. test `local-2c-2g`, `local-small`, and a genuinely available larger profile;
3. rediscover MSSL on each profile;
4. compare WU/s/vCPU, WU/s/GiB, latency, and bottleneck migration;
5. do not merge these runs into the canonical `local-small` regression baseline.

Then hold `local-small` and load rate constant while changing D0/D1/D2 to measure dataset sensitivity.

**Exit criterion:** resource scaling and dataset scaling are explained as separate curves.

## Phase 6 — Publish

- populate this document;
- attach small redacted summaries;
- link raw artifact hashes;
- review all claims against the claim boundary;
- review decisions with another engineer;
- commit only after every mandatory field is complete.

---

# 25. Recommended Pull-Request Sequence

## PR 1 — Canonical Seed and Integrity

Scope:

- owner-service/API seed adapter;
- deterministic dataset manifest;
- reset/restore;
- pre/post invariant verifier;
- runner integration.

Why first: without valid reproducible state and correctness checks, higher load produces numbers but not evidence.

## PR 2 — Scenario Fidelity and Metrics

Scope:

- complete S1–S4 journeys;
- W5/B1 A/B variants;
- W6/B3 baseline/test-double variants;
- per-operation metrics;
- duplicate/redelivery behavior;
- terminal-state observation.

Why second: the load generator must measure the business behavior named in the report.

## PR 3 — Saturation and Evidence Pipeline

Scope:

- pool metrics;
- lock/wait collection;
- outbox/broker drain;
- resource time series;
- generator saturation checks;
- report aggregation and validation.

Why third: a throughput knee without saturation evidence cannot identify a bottleneck.

## PR 4 — Baseline Execution and Decision Report

Scope:

- discovery;
- confirmation;
- spike;
- soak;
- B1/B2/B3 decisions;
- this final report.

Why last: optimization decisions should follow evidence, not precede it.

---

# 26. Definition of Done

The baseline is complete only when:

- [ ] Commit, profile, configuration, and dataset hashes are recorded.
- [ ] The Git working tree is clean or its diff is preserved.
- [ ] CPU, memory, Docker, PostgreSQL, Go, and dependency versions are recorded.
- [ ] Account and initial transaction counts are recorded.
- [ ] S1–S4 represent complete documented journeys.
- [ ] Offered WU/s, achieved WU/s, and HTTP req/s are distinguished.
- [ ] p50/p95/p99 and errors are available per critical journey.
- [ ] Pool, CPU, memory, lock, disk, broker, and outbox evidence exists.
- [ ] Last healthy and first unhealthy load are identified.
- [ ] Candidate MSSL has three successful confirmations.
- [ ] Spike and 60-minute soak are complete.
- [ ] Outbox and broker drain within the required gates.
- [ ] Financial integrity checks report zero discrepancy.
- [ ] The load generator was not the limiting component.
- [ ] Local-benchmark limitations are stated.
- [ ] B1 is ACTIVATE or REJECT.
- [ ] B2 is ACTIVATE or REJECT.
- [ ] B3 is ACTIVATE or REJECT.
- [ ] Raw artifacts are hashed and remain outside Git.
- [ ] Committed reports contain no credentials, PII, or large raw artifacts.
- [ ] Every conclusion points to supporting evidence.
