# 53 — Track B0: Load Harness and Capacity Gate

> [Documentation home](../../README.md) · [Roadmap](../README.md) · [Active plans](README.md)

> Derived from track **B0** in
> [42-long-term-roadmap.md](../42-long-term-roadmap.md).
>
> **Status: ready for execution; not implemented.** The activation trigger is
> a conscious learning decision made on 2026-07-22 after the MVP and its
> observability foundation. Completing B0 does not activate B1, B2, or B3 by
> itself; only the locked evidence gates in this plan may do that.

## 1. Trigger and objective

The repository has functional, integration, smoke, business, admin, and chaos
tests, but none answers a capacity question. Existing tests prove correctness at
small concurrency; they do not establish a sustainable offered rate, identify a
saturation knee, separate load-generator limits from service limits, or prove
that a proposed scale feature addresses a measured bottleneck.

B0 creates a reproducible local load laboratory for four real product shapes:
P2P posting, signed pay-in webhook bursts, payout creation/settlement bursts,
and a mixed MVP journey. It also adds focused experiments for the three measured
scale tracks:

- B1: contention on atomic system-account delta updates;
- B2: ledger size, query-plan, maintenance, and storage growth;
- B3: repeated fee and routing-rule resolution.

The output is a bounded capacity model for one declared resource envelope, not
a production capacity promise. Every result must preserve money, idempotency,
outbox delivery, and lifecycle integrity.

### Measurable targets

1. A clean environment can reproduce the same dataset, workload mix, offered
   rate, service configuration, and measurement window from one command.
2. Every canonical scenario reports offered and achieved workload units/second,
   HTTP request rate, p50/p95/p99 latency, expected/unexpected failures, dropped
   iterations, resource saturation, database waits, queue lag, and drain time.
3. The maximum safe sustainable load (MSSL) and saturation knee are measured for
   P2P, webhook, payout, and mixed workloads.
4. Each candidate MSSL passes three independent clean-state runs and one
   60-minute soak at 70% of MSSL.
5. Every measured run ends with zero ledger imbalance, zero projection mismatch,
   zero duplicate monetary effect, zero unresolved unknown outcome, and no dead
   event/vendor command caused by load.
6. B1, B2, and B3 each receive a final `ACTIVATE` or `REJECT` decision using the
   locked formulas in section 7. An invalid experiment is rerun; `INCONCLUSIVE`
   is not an acceptable final B0 result.
7. Small committed summaries are sufficient to review every decision; large raw
   time-series artifacts remain reproducible without being committed to Git.
8. The PR gate validates the harness quickly, while canonical capacity runs stay
   manual/scheduled and never become noisy merge blockers.

## 2. Live repository facts

These facts were verified when this plan was written. T0 must recheck them
before implementation.

### 2.1 Current topology and resource constraints

- Eight deployable Go binaries exist, with six core money-path services:
  gateway, auth, ledger, pay-in, payout, and fraud. Admin BFF and assurance are
  operational extensions.
- PostgreSQL, Redis, and RabbitMQ run through Docker Compose. The app profile can
  run all service containers.
- The documented development environment has a 4 GiB constraint. Plan 43
  explicitly avoids running the complete Grafana/Loki/Tempo stack alongside
  other heavy suites.
- Default PostgreSQL pools are 10 open and 5 idle connections per service;
  assurance caps its own pool at 5.
- No CPU-normalized or production-like infrastructure baseline exists, so a
  result is meaningful only with its host/resource fingerprint.

### 2.2 Existing performance-sensitive design

- Plan 11 removed long-held `FOR UPDATE` locks from negative-capable system
  accounts and now applies atomic `balance = balance + delta` updates.
- User accounts remain row-locked for overdraft safety. Entry inserts are
  batched and account resolution has positive caches.
- Plan 13 intentionally deferred sub-sharding until lock-wait evidence and
  partitioning until `ledger_entries` approaches roughly 50 million rows.
- Fee and pay-in/payout routing rules are resolved from PostgreSQL on product
  paths. No fee/routing cache exists.
- The outbox relay polls every second with a default batch of 100. Existing
  monitoring warns only when the oldest pending event exceeds five minutes,
  which is too coarse for locating a load-test knee.

### 2.3 Existing telemetry

- HTTP route histograms, gRPC method histograms, ledger posting counters and
  histograms, RabbitMQ publish/consume metrics, outbox depth, runtime metrics,
  breaker state, and several business gauges already exist.
- Prometheus and dashboards are provisioned through the optional observability
  profile.
- `database.DBSQL.Stats()` is available, but pool open/in-use/idle/wait metrics
  are not exported.
- No load-only PostgreSQL observer, `pg_stat_statements`, lock sampler, oldest
  outbox-age gauge, resolver-duration metric, or repeatable result collector
  exists.

### 2.4 Existing automation

- `scripts/lib.sh` provides clean database setup, migrations, process lifecycle,
  generated credentials, and reusable business helpers.
- `scripts/business-e2e.sh` already exercises the complete MVP journey and is a
  useful correctness oracle, but it uses fixed assertions rather than load.
- PR CI runs lint, tests, integration, and container smoke. A scheduled workflow
  runs business and chaos suites weekly.
- No k6 scripts, deterministic large-data seeder, performance-analysis command,
  or capacity report exists.

## 3. Scope and anti-scope

### In scope

- pinned local k6 tooling and reusable JavaScript workload libraries;
- a destructive-safe, disposable load environment and deterministic seed data;
- P2P, webhook, payout, mixed, hotspot, resolver, spike, soak, and size-ladder
  experiments;
- open arrival-rate scheduling and explicit dropped-iteration accounting;
- database pool, PostgreSQL statement/lock, outbox-age, queue, resolver, CPU,
  memory, and runtime measurements;
- repeated-run analysis and a capacity model tied to a named resource profile;
- correctness verification after every measured run;
- explicit B1/B2/B3 activation or rejection evidence;
- small versioned reports, run manifests, dashboards, and operator runbooks;
- lightweight PR validation and manual/scheduled evidence collection.

### Out of scope

- implementing account sub-shards, table partitioning/archival, or a production
  fee/routing cache;
- changing indexes, pool sizes, worker batches, timeouts, or service resources
  merely to improve the baseline result;
- production, internet-facing, cloud-distributed, or multi-region load tests;
- claiming that laptop/container numbers predict production capacity;
- paid k6 cloud execution or a hosted performance platform;
- browser rendering, Admin BFF UI, Grafana/Loki/Tempo load, or Vault throughput;
- load-testing real vendors, sending real notifications, or using real personal
  data;
- dropping host filesystem caches, modifying host kernel settings, or requiring
  privileged containers;
- committing large raw time series, database dumps, tokens, or logs;
- writing B1–B3 implementation plans when their evidence gate rejects them.

If B0 exposes a correctness bug, stop the affected measurement, record the
minimal reproducer, fix it in a separately reviewed change, and restart that
experiment from a clean baseline. Do not mix a tuning change into the measured
baseline and then compare before/after results from different code.

## 4. Locked measurement vocabulary

| Term | Definition |
| --- | --- |
| Workload unit (WU) | One complete scenario iteration; it may contain multiple HTTP calls |
| Offered rate | WU/s scheduled by k6, independent of response duration |
| Achieved rate | Successfully started and completed WU/s |
| Dropped iteration | A scheduled WU that k6 could not start; always reported as generator or saturation evidence |
| Expected rejection | A deliberately generated business outcome with a named check, excluded from system-failure rate |
| Unexpected failure | Transport, 5xx, timeout, invalid state, contract mismatch, or an unplanned 4xx |
| Steady window | Measured interval after warm-up and before drain |
| MSSL | Highest offered rate that passes every safety, latency, saturation, and drain gate in all three confirmation runs |
| Saturation knee | First rate where additional offered work stops producing proportionate useful throughput or violates a locked gate |
| Headroom | `(knee_rate - MSSL) / knee_rate`, reported only inside one resource profile |
| Clean run | Fresh restored seed state, reset telemetry, stable healthy targets, and no prior test residue |

Rates always state whether they are WU/s or HTTP requests/s. Reports must never
use the ambiguous label “RPS” without saying which one.

## 5. Locked experiment environment

### K1 — One named small-host profile is canonical

Create `deploy/load/profiles/local-small.yaml` and
`deploy/load/compose.load.yaml`. The canonical profile is:

- 4 logical CPU capacity and 4 GiB Docker memory allocation;
- PostgreSQL, Redis, RabbitMQ, and the six core money-path services;
- one lightweight Prometheus process and one k6 load generator;
- tracing export, Grafana, Loki, Tempo, Vault, Admin BFF, and assurance disabled;
- normal production-like pool, timeout, outbox, and worker defaults;
- total configured container memory limit no greater than 3.25 GiB, leaving
  Docker/host headroom; no single container may exceed 768 MiB;
- loopback-only published ports and synthetic credentials.

T0 calibrates the per-container split while keeping those total limits. Once the
first baseline is committed, changing CPU, memory, topology, pool size, database
settings, or worker settings creates a new profile ID; it never overwrites
`local-small` history.

The profile records host CPU model/architecture, logical CPUs, memory, OS,
Docker engine/Desktop version, filesystem type when available, image digests,
Go and k6 versions, Git SHA, database settings, and dataset counts. Results from
different fingerprints may be viewed side by side but not presented as a
regression percentage.

### K2 — Load can target only disposable environments

`scripts/load-test.sh` refuses to run unless all of these are true:

- the selected profile is marked `disposable: true`;
- every base URL is loopback or the private Compose load network;
- every database name starts with `seev_load_`;
- the script created or restored the target state itself;
- `SEEV_LOAD_ACK=disposable-only` is present for destructive setup;
- no production-mode flag or non-test vendor adapter is enabled.

There is no override for public hostnames in this track. Cleanup removes only
the exact Compose project and result directory created for that run. It never
uses an unvalidated root, home directory, broad glob, or shared development
volume.

### K3 — k6 is pinned and uses an open workload model

Pin the official k6 container by version and image digest. Canonical staircases
use `ramping-arrival-rate`; confirmation and soak runs use
`constant-arrival-rate`. These executors start iterations independently of
system response, matching the open-model behavior documented by
[Grafana k6](https://grafana.com/docs/k6/latest/using-k6/scenarios/executors/constant-arrival-rate/).

Pre-allocate enough VUs to reach the target rate. `maxVUs` is bounded by the
profile and never grows without limit. `dropped_iterations` is a first-class
result: a run with dropped work cannot be called sustainable unless analysis
proves the generator, rather than the system under test, was the limiting
resource and the run is repeated with a corrected generator allocation.

Do not add `sleep()` to arrival-rate iterations. Think time belongs only in a
separately labeled closed-model user-behavior experiment, which is not used to
calculate MSSL.

### K4 — Setup, warm-up, measurement, and drain are separate

Every scenario follows this lifecycle:

1. restore a deterministic clean dataset;
2. reset Prometheus test markers, PostgreSQL statistics, queue counters, and
   application counters where safe;
3. verify all targets healthy and clocks within two seconds;
4. run a 3-minute warm-up that is excluded from reported percentiles;
5. run the declared steady window;
6. stop new work and measure asynchronous drain;
7. run integrity and lifecycle verification;
8. collect the manifest, summaries, telemetry, and decision inputs;
9. tear down the exact disposable project.

User registration, KYC approval, account funding, rule creation, and fixture
generation occur before warm-up. Authentication refresh belongs in the mixed
scenario only; setup calls never contaminate measured endpoint latency.

### K5 — Datasets are deterministic, valid, and restorable

Add `cmd/loadseed` for two dataset classes:

- **Journey seed:** users, L1 policy state, JWT input credentials, funded
  accounts, routing/fee rules, top-up intents, and payout prerequisites created
  through supported APIs or owner services.
- **Ledger size seed:** balanced transaction headers, immutable entries,
  snapshots, and required projections inserted into a dedicated throwaway
  ledger database with owner privileges.

The size seeder refuses non-load database names and records seed/version/hash.
It generates balanced entries, valid UUIDv7/time ordering, currency-consistent
accounts, and deterministic distributions. The ledger verifier and projection
audit must pass before a size dataset can be snapshotted.

Create one compressed seed snapshot per profile/dataset version outside Git.
Each confirmation run restores the same snapshot into a fresh volume. Seed
manifests and checksums are committed; dumps and credentials are not.

### K6 — The generator checks semantics, not only status codes

Each k6 iteration uses unique deterministic idempotency/external references and
checks response schema, business ID, amount, currency, and state. Explicit retry
subflows resend the exact same key and payload and verify the same result ID.

Expected business rejections use a separate custom rate metric and exact code.
They do not count as successful money work and are never hidden inside the HTTP
success rate. Unknown outcomes are recorded with correlation identifiers in a
local redacted manifest and reconciled after drain.

Response bodies are discarded after required checks. Logs and summaries never
contain JWTs, webhook secrets, payout destinations, full idempotency keys, or
personal data.

## 6. Locked workload matrix

### K7 — Canonical scenarios represent distinct bottlenecks

| ID | Scenario | Measured workload unit | Primary pressure |
| --- | --- | --- | --- |
| W1 | P2P posting | quote plus one authenticated transfer and result check | user locks, fraud RPC, ledger transaction, outbox |
| W2 | Webhook burst | one valid signed callback for a pre-created intent | gateway verification, pay-in state, shared settlement account, ledger, outbox |
| W3 | Payout burst | quote, create, and terminal-status polling for one funded payout | routing, fraud, hold, command relay, mock vendor, settle |
| W4 | Mixed MVP | one weighted user action/journey from K8 | realistic shared pools and dependencies |
| W5 | System-account hotspot | W2 against one gateway versus an evenly split two-gateway control | atomic system-balance row contention for B1 |
| W6 | Resolver stress | fee quote or pay-in/payout routing resolution over controlled key cardinality | repeated rule-query cost/cacheability for B3 |
| W7 | Ledger size ladder | fixed read/verifier suite at increasing row counts | query/storage/maintenance growth for B2 |

W1 uses disjoint funded account pairs and alternates direction between rounds so
business overdrafts do not masquerade as capacity failures. W2 pre-creates all
intents and includes a separately tagged 10% exact webhook redelivery stream.
W3 uses only local mock vendors and separately reports synchronous create time
and eventual terminal time.

### K8 — Mixed workload weights are fixed

W4 schedules workload units with this deterministic mix:

| Action | Weight |
| --- | ---: |
| Fee quote plus P2P transfer | 35% |
| Balance, transaction, or statement read | 20% |
| Top-up intent plus signed webhook | 20% |
| Payout quote, create, and status | 15% |
| Notification list/read | 5% |
| Login or refresh | 5% |

The seed is large enough that no user has more than one concurrent money-moving
iteration. A fixed PRNG seed selects users/actions, and the run manifest records
it. Changing weights creates a new workload version.

### K9 — Staircase, confirmation, spike, and soak have separate purposes

- **Smoke:** 1 WU/s for 60 seconds; validates scripts and correctness only.
- **Discovery staircase:** rates `1, 2, 5, 10, 20, 40, 80` WU/s, each held for
  two minutes after warm-up. Stop early on the safety abort conditions in K12.
  If 80 WU/s remains healthy and the generator has headroom, continue by
  doubling with an explicitly recorded extension.
- **Boundary refinement:** test rates around the first failing stage until the
  candidate MSSL is within 10% of the measured knee.
- **Confirmation:** candidate MSSL for 15 minutes, three independent clean
  restores.
- **Spike/recovery:** 200% of MSSL for 30 seconds, then 50% for five minutes;
  overload may fail latency but must not violate money safety and must drain.
- **Soak:** 70% of MSSL for 60 minutes, followed by complete drain and integrity
  verification.

Do not average a warm-up, overload spike, or drain interval into steady-state
latency.

## 7. Locked pass and activation gates

### K10 — MSSL requires every service and safety gate

A candidate rate is sustainable only when all three confirmation runs satisfy:

| Area | Gate |
| --- | --- |
| Scheduling | zero unexplained `dropped_iterations`; achieved rate at least 99% of offered rate |
| Unexpected failures | less than 0.5% of WUs; no unknown monetary outcome |
| P2P latency | p95 ≤ 500 ms and p99 ≤ 1 s |
| Webhook acknowledgement | p95 ≤ 2 s, matching the existing webhook latency SLI |
| Payout create | p95 ≤ 1 s and p99 ≤ 2.5 s |
| Mixed journey action | p95 ≤ 1 s and p99 ≤ 2.5 s by action tag |
| Database pool | in-use below 80% of max for at least 95% of samples; cumulative pool wait below 5% of aggregate request wall time |
| Process resources | no OOM/restart; memory below 90% of limit; no measured process sustains above 85% of its CPU allocation for the whole steady window |
| Outbox | oldest pending age ≤ 10 s; pending rows drain to zero within 30 s |
| RabbitMQ/commands | no dead messages/commands; measured queues drain within 60 s |
| Integrity | zero ledger/projection/snapshot discrepancies and balanced expected totals |

The MSSL is the highest rate passing every row in all three runs. Report the
worst run's p95/p99 and saturation values; do not average percentiles across
runs.

The saturation knee is the first stage where at least one occurs:

- a 25% or greater offered-rate increase yields less than 10% more achieved
  successful throughput;
- p95 rises by at least 50% from the prior stage and a resource/wait signal
  identifies saturation;
- an MSSL gate fails.

### K11 — B1 activation requires isolated system-row lock contention

B1 is `ACTIVATE` only when all conditions hold in three alternating W5 runs:

1. at or below the W2 knee, at least 20% of sampled execution time for the
   canonical `UPDATE account_balances SET balance = balance + ...` statement is
   waiting on a PostgreSQL lock;
2. at least 80% of those lock-wait samples identify the same system-account
   update/transaction dependency rather than user-account locks;
3. the one-gateway case has at least 25% lower sustainable throughput **or** at
   least 30% worse p95 than the two-gateway control at equal offered rate;
4. ledger CPU remains below 70% and its database pool remains below 80%, ruling
   out general CPU/pool saturation as the primary cause;
5. every run remains monetarily correct.

Use alternating `A-B-B-A` ordering across clean restores to reduce thermal/order
bias; at least three valid runs per variant are required. If any condition is
false, B1 is `REJECT`. A rejected B1 gets no implementation plan; rerun B0 after
traffic shape, hardware, or code materially changes.

### K12 — B2 activation requires size evidence and observed degradation

W7 uses balanced datasets at `100k`, `1m`, and `5m` ledger-entry rows. A larger
step may run only while the disposable disk remains below 60% usage and seed/
verification time stays inside the declared test budget. For each size, run:

- account-entry first and deep pages;
- a 92-day JSON and CSV statement;
- current and `as_of` balance reads;
- incremental snapshot and verifier work;
- representative reconciliation/report reads;
- `EXPLAIN (ANALYZE, BUFFERS, WAL, FORMAT JSON)` for allowlisted read queries;
- table/index size, seed rate, analyze time, and estimated backup volume.

Do not call a post-restart run “cold cache”; report it as post-restart and record
buffer hits. B0 never executes host `drop_caches`.

B2 is `ACTIVATE` only when both gates hold:

1. observed staging/production-equivalent inventory has at least 40 million
   entries, **or** a documented growth forecast crosses 50 million within six
   months; synthetic extrapolation alone cannot satisfy this gate;
2. at least one observed/representative-size symptom exists: statement/history
   p95 above 500 ms at five concurrent readers, verifier/snapshot maintenance
   above a 30-minute window, or ledger table+indexes above 60% of the assigned
   database disk budget.

Otherwise B2 is `REJECT`, even if a synthetic 5-million-row curve can be
extrapolated to a large number. Keep the size model as future comparison data.

### K13 — B3 activation requires material and cacheable resolution cost

Instrument fee, pay-in routing, and payout routing separately. W6 runs a stable
finite key set and a high-cardinality control at equal offered rate. A test-only
memoized resolver may be injected into the harness to estimate an upper bound;
it is compiled only with the `loadtest` build tag, refuses to start without the
K2 disposable markers, is absent from normal binaries, and cannot change quote
authority.

B3 is `ACTIVATE` only when all conditions hold in three alternating baseline/
test-double runs:

1. resolver SQL accounts for at least 15% of total database execution time on
   its owning service at W4 MSSL or knee;
2. measured repeated-key cacheability is at least 80% under the fixed W4 mix;
3. the test-only memoized resolver improves sustainable throughput by at least
   15% **or** lowers affected operation p95 by at least 20%;
4. the improvement direction appears in every valid run and no other saturated
   resource explains it;
5. fee quote values and routing choices remain byte-for-byte equivalent to the
   database-authoritative baseline.

If any condition is false, B3 is `REJECT`. B0 does not design invalidation or
ship a cache; those belong only in an activated B3 plan.

### K14 — Safety aborts stop offered work but still verify state

Abort new iterations after the delayed warm-up evaluation when any occurs:

- unexpected failure rate exceeds 5% for one minute;
- p99 exceeds 10 seconds for one minute;
- dropped iterations exceed 5% for one minute;
- any container restarts, OOMs, or loses health;
- outbox oldest pending age exceeds five minutes;
- a dead event/vendor command, ledger discrepancy, or impossible lifecycle
  state appears.

An abort does not skip drain, reconciliation, or evidence collection. The run is
failing saturation/correctness evidence, never silently discarded.

## 8. Locked instrumentation and evidence

### K15 — Application database-pool metrics are shared and bounded

Add a collector in `pkg/database` using `sql.DBStats` and register it once per
service database. Export open, in-use, idle, max-open, wait-count,
wait-duration, max-idle-closed, max-idle-time-closed, and max-lifetime-closed
metrics. Prometheus `job` identifies the service; do not add DSN, database name,
host, query, user, or request labels.

Tests prove collector stability, monotonic counters, no duplicate registration,
and correct behavior when a database closes.

### K16 — PostgreSQL observation is load-only and read-only

The load Compose override enables `pg_stat_statements` and `track_io_timing`
only for disposable load databases. Create a `seev_load_observer` role with
`pg_monitor` and no application-table privileges.

Add `cmd/loadprobe` to sample at 100 ms during focused W5 and at one second for
other workloads:

- `pg_stat_activity` state, wait event/type, query ID, and transaction age;
- `pg_locks` blockers and blocked query IDs without parameter values;
- normalized `pg_stat_statements` calls/rows/total and mean execution time;
- database size and transaction/deadlock counters;
- service pool metrics, process CPU/memory/GC, outbox age/depth, RabbitMQ queue
  depth, and k6 generator utilization.

The probe stores normalized query IDs and controlled query classes, never SQL
parameters or table row values. It resets statistics only inside the disposable
project and records reset timestamps.

### K17 — Add the missing business-path measurements

Add bounded metrics for:

```text
seev_resolution_duration_seconds{owner,kind,result}
seev_resolution_total{owner,kind,result}
ledger_outbox_oldest_pending_age_seconds
seev_load_integrity_checks_total{check,result}
```

Allowed resolver kinds are `fee`, `payin_routing`, and `payout_routing`.
Application runtime metrics must not carry run IDs, user/account IDs, rule IDs,
gateways beyond an existing bounded registry, raw paths, or idempotency keys.

`load_integrity_checks_total` is emitted by the load verifier/collector, not
normal production request paths. The canonical result remains the verifier
output stored in the run report.

### K18 — Evidence is small, immutable, and reproducible

Each run writes:

```text
artifacts/load/<run-id>/manifest.json
artifacts/load/<run-id>/k6-summary.json
artifacts/load/<run-id>/timeseries.json.gz
artifacts/load/<run-id>/postgres-summary.json
artifacts/load/<run-id>/integrity.json
artifacts/load/<run-id>/decision-input.json
```

`artifacts/load/` is ignored by Git. Commit only redacted summaries under
`docs/performance/reports/<date>-<short-sha>/` plus the generated
`docs/performance/capacity-model.md`. A committed report records hashes of raw
artifacts so an operator can prove which source produced it.

k6 `handleSummary()` produces deterministic JSON with median, p95, p99, counts,
threshold outcomes, and dropped iterations. Granular output may use k6 JSON and
Prometheus range queries, consistent with
[k6 result-output guidance](https://grafana.com/docs/k6/latest/get-started/results-output/).

Never commit raw service logs or dumps. Failure diagnostics use the existing
masking policy and exact artifact allowlists.

### K19 — Analysis does not hide variance

Add `cmd/loadreport` to validate manifests and aggregate runs. It reports each
run plus median/min/max across valid runs, and uses the worst confirmation run
for pass/fail. It never averages percentiles or combines runs from different
profile, workload, dataset, code, or binary hashes into one baseline. A focused
A/B experiment may differ only in its declared variant (for example the W5
gateway distribution or W6 `loadtest` resolver); both binary/config hashes stay
visible and runs are aggregated within, not across, each variant.

Before/after focused comparisons alternate order and require the effect
direction in every valid run. Report relative change with the raw values. A
single outlier may be excluded only for a documented external reason such as a
host sleep or generator failure, and the run must be replaced.

Capacity forecasts may interpolate inside the measured range. Extrapolation is
shown separately, marked as a model, and may not exceed 2× the largest measured
rate or row count. Apply at least a 2× safety factor before converting MSSL into
a planning limit.

## 9. Execution tasks

Execute T0 → T1 → T2 → T3 → T4 → T5 → T6. No canonical result is collected
until T0–T3 and the normal business gate are green.

### T0 — Lock protocol, safety, profiles, and baseline inventory

**Work**

1. Re-inventory money paths, route contracts, worker intervals, pool defaults,
   current metrics, and B1–B3 deferred assumptions.
2. Add the versioned workload/profile/result JSON schemas and threshold file.
3. Create the `local-small` Compose override, explicit service list, network,
   resource preflight, and safe teardown model.
4. Pin the k6 image by version/digest and document its license/source.
5. Define run IDs from timestamp, Git SHA, profile, workload, and dataset hash;
   never use them as application metric labels.
6. Write `scripts/load-test.sh` refusal checks before any destructive seeding.
7. Record the pre-B0 route, row-count, index-size, hardware, and configuration
   baseline.

**Required checks**

- Compose config is valid and total memory limits stay within K1;
- bare `docker compose up` behavior is unchanged;
- non-loopback URL, wrong database prefix, missing acknowledgement, production
  mode, or real vendor configuration fails before mutation;
- teardown targets only the generated Compose project/volume/result paths;
- profile and result schemas reject unknown/ambiguous fields;
- k6 version and digest match the manifest;
- `git diff --check` passes.

**Definition of done:** the experiment protocol and destructive boundary are
fixed before instrumentation or load is added.

### Result

_Pending implementation._

### T1 — Add pool, lock, resolver, queue, and resource observability (K15–K17)

**Work**

1. Add and wire the shared `sql.DBStats` Prometheus collector to every service.
2. Add resolver duration/count metrics around fee and both routing repositories.
3. Export oldest pending outbox age on the existing bounded worker cadence.
4. Enable load-only `pg_stat_statements`/I/O timing and create the read-only
   observer role without changing production migrations/defaults.
5. Implement `cmd/loadprobe` with controlled query classes and safe sampling.
6. Add a lightweight load dashboard for rate, latency, resources, pools, SQL,
   locks, outbox, queues, and verifier state.
7. Verify telemetry overhead at idle and 1 WU/s.

**Required tests**

- collector unit tests and duplicate-registration tests;
- every core service exposes complete pool metrics with bounded labels;
- a deliberately exhausted test pool increments wait count/duration;
- a held test row lock appears in the sampler with query class but no values;
- resolver metrics distinguish fee/pay-in/payout and success/not-found/error;
- outbox age rises and returns to zero in a controlled relay pause/recovery;
- observer role can read monitoring views but no application table;
- collector/resolver instrumentation benchmark overhead is below 5% versus its
  no-op benchmark, and three alternating 1 WU/s runs with the deep load probe
  on/off show below 5% p95 impact.

**Definition of done:** every required saturation signal is observable without
exposing data or materially changing the baseline.

### Result

_Pending implementation._

### T2 — Build deterministic seed, restore, orchestration, and reporting tools (K1–K6, K18–K19)

**Work**

1. Implement `cmd/loadseed` for journey and ledger-size datasets with strict
   target refusal checks.
2. Add seed manifest/checksum, compressed snapshot, clean restore, and per-run
   telemetry reset support.
3. Implement `scripts/load-test.sh` lifecycle orchestration and failure-safe
   evidence collection.
4. Add shared k6 authentication, HMAC, idempotency, polling, semantic check,
   tagging, and `handleSummary()` libraries under `tests/load/lib/`.
5. Implement `cmd/loadreport` validation, aggregation, threshold evaluation,
   profile matching, and Markdown generation.
6. Ignore raw artifacts/dumps while allowing small redacted reports.

**Required tests**

- identical seed inputs produce identical manifest/hash and valid business
  counts;
- journey users are L1, funded, isolated, and use only synthetic identity data;
- ledger-size seed passes both ledger verification functions and snapshot checks;
- interrupted seed/restore cannot be mistaken for a valid dataset;
- two restores have identical logical counts/distributions and no shared volume;
- summaries reject missing metrics, mismatched hashes, mixed profiles, NaN,
  truncated data, and averaged percentiles;
- cleanup runs on success, failure, signal, and k6 abort without deleting paths
  outside the disposable project.

**Definition of done:** any contributor can create and restore the exact test
state and obtain a validated, redacted result bundle from one command.

### Result

_Pending implementation._

### T3 — Implement W1–W6 and semantic correctness checks (K6–K9)

**Work**

1. Implement P2P with disjoint pairs, quote binding, unique/retry keys, and
   post-response checks.
2. Implement pre-created signed webhook bursts and the 10% duplicate stream.
3. Implement payout quote/create/poll through local mock vendors with bounded
   terminal waiting.
4. Implement the fixed mixed workload and versioned action weights.
5. Implement the one-gateway/two-gateway hotspot variants.
6. Implement stable-key/high-cardinality resolver variants and the test-only
   memoized upper-bound double.
7. Add post-drain verification for balances, transaction counts, lifecycle
   closers, idempotency, outbox, RabbitMQ consumers, notifications, payout
   commands, fraud records, and cross-service correlations.

**Required tests**

- `k6 inspect` and syntax checks for every scenario;
- 1 WU/s smoke from a clean restore for every scenario;
- deterministic mix falls within ±1 percentage point after at least 10,000 WUs;
- no user has overlapping money-moving iterations;
- exact retries return one business result/effect;
- duplicate webhooks settle once;
- all payouts reach one legal terminal state with one hold closer;
- expected rejection metrics contain only deliberately configured cases;
- a deliberately wrong amount/status/signature makes semantic checks fail;
- all post-run integrity checks pass after valid smoke runs.

**Definition of done:** the harness generates the intended business pressure and
detects semantic corruption even when HTTP status codes look successful.

### Result

_Pending implementation._

### T4 — Measure capacity, recovery, soak, and ledger-size curves (K9–K14)

**Work**

1. Run discovery staircases and boundary refinement for W1–W4.
2. Confirm each candidate MSSL with three clean 15-minute runs.
3. Run spike/recovery and 60-minute soak for each canonical scenario or one
   explicitly justified worst-case scenario plus W4.
4. Run alternating W5 hotspot/control experiments around the webhook knee.
5. Run alternating W6 baseline/test-double experiments at W4 MSSL/knee.
6. Build and measure W7 at 100k, 1m, and 5m rows, stopping on disk/safety cap.
7. Preserve aborted runs as evidence and repeat only invalid generator/host runs.
8. Run the normal business, admin, and chaos suites after load instrumentation
   changes.

**Required evidence**

- offered/achieved WU/s and HTTP requests/s by stage;
- p50/p95/p99 by operation/action and expected/unexpected result;
- dropped iterations and generator CPU/memory;
- service CPU/memory/GC, pool utilization/waits, SQL time, lock samples;
- outbox/queue/command peak, oldest age, and drain time;
- seed/row/index/disk/query-plan/maintenance curves;
- integrity and lifecycle report after every run;
- complete manifests and raw-artifact hashes;
- three-run variation and alternation order.

**Definition of done:** the repository has valid repeated measurements for
general capacity and every B1–B3 decision input, with no hidden failed state.

### Result

_Pending implementation._

### T5 — Produce the capacity model and B1–B3 decisions (K10–K13, K19)

**Work**

1. Generate `docs/performance/capacity-model.md` with profile scope, workload
   versions, MSSL, knee, headroom, latency, saturation, and drain tables.
2. Identify the primary bottleneck at each knee using at least two independent
   signals; do not infer cause from latency alone.
3. Apply K11 to B1, K12 to B2, and K13 to B3 without changing thresholds after
   seeing results.
4. Write one signed-off decision block per track containing `ACTIVATE` or
   `REJECT`, exact evidence, failed/passed conditions, and rerun trigger.
5. Convert MSSL to a conservative planning limit using at least 2× safety
   factor and state that it applies only to `local-small`.
6. Record limitations, variance, excluded invalid runs, and unsupported
   extrapolations clearly.

**Required checks**

- model regenerates deterministically from committed summaries;
- every chart/table value traces to a run hash;
- no percentile is averaged and no mismatched profile is compared;
- bottleneck claims have both latency/throughput and resource/wait evidence;
- B1/B2/B3 decision scripts reproduce the written result;
- a rejected track has no new implementation-plan link/status;
- an activated track is only authorized for a separate future execution plan,
  not implemented in B0.

**Definition of done:** B0 answers how much the declared environment sustains,
where it bends, why, and which measured scale tracks are justified.

### Result

_Pending implementation._

### T6 — Automation, runbooks, documentation, and final gate

**Work**

1. Add `load-lint`, `load-test` (fast helper/unit checks), `load-seed`,
   `load-smoke`, `load-run`, `load-capacity`, `load-report-check`, and
   `load-clean` Make targets.
2. Add fast PR checks: k6 inspection, helper/analyzer tests, schemas, safety
   refusal tests, and one short stub/smoke path without a capacity claim.
3. Add manual/scheduled load workflow with explicit profile/scenario inputs,
   concurrency lock, timeouts, redacted artifacts, and no automatic baseline
   overwrite. Treat hosted-runner results as diagnostics unless fingerprints
   match.
4. Add runbooks for preparation, abort, invalid run, integrity failure,
   bottleneck diagnosis, baseline refresh, and result retention.
5. Document the capacity claim boundary and B1–B3 decision policy in the root
   README and [project guide](../../development/project-guide.md).
6. Run the full final gate from a clean tree and clean load volume.
7. Mark B0 complete only after T0–T6 evidence and all three track decisions are
   recorded.

**Required final gate**

```bash
GOCACHE=/tmp/seev-go-cache go build ./...
GOCACHE=/tmp/seev-go-cache go vet ./...
GOCACHE=/tmp/seev-go-cache go vet -tags=integration ./...
GOCACHE=/tmp/seev-go-cache make test
GOCACHE=/tmp/seev-go-cache make lint
make proto
make proto-lint
make proto-breaking
make load-lint
GOCACHE=/tmp/seev-go-cache make load-test
GOCACHE=/tmp/seev-go-cache go test -tags=loadtest ./...
GOCACHE=/tmp/seev-go-cache go test -tags=integration -race ./...
GOCACHE=/tmp/seev-go-cache ./scripts/smoke-test.sh all
GOCACHE=/tmp/seev-go-cache ./scripts/business-e2e.sh
GOCACHE=/tmp/seev-go-cache ./scripts/admin-e2e.sh
GOCACHE=/tmp/seev-go-cache ./scripts/chaos-test.sh all
SEEV_LOAD_ACK=disposable-only make load-smoke
make load-report-check
git diff --check
```

Canonical staircase/confirmation/soak runs are required evidence for plan
completion but are intentionally not part of every local final-gate invocation
or PR job.

**Definition of done:** the harness is safe, repeatable, documented, and
automated proportionally, while capacity evidence remains tied to controlled
manual/scheduled runs rather than noisy PR hardware.

### Result

_Pending implementation._

## 10. Acceptance checklist

### Safety and reproducibility

- [ ] The load runner cannot target public/non-disposable environments.
- [ ] Canonical topology, limits, versions, Git SHA, settings, and dataset hashes
      are recorded for every run.
- [ ] Three confirmation runs restore identical clean logical state.
- [ ] Setup, warm-up, steady state, drain, and verification are separate.
- [ ] Cleanup cannot delete shared development or unrelated paths/volumes.

### Workloads and measurement

- [ ] W1–W7 implement the locked semantics and versions.
- [ ] Open arrival-rate tests report offered, achieved, and dropped work.
- [ ] P2P, webhook, payout, and mixed MSSL/knee values are measured.
- [ ] Spike and soak runs prove recovery and bounded asynchronous drain.
- [ ] Generator saturation is measured and separated from service saturation.
- [ ] Pool, SQL, lock, resolver, process, outbox, queue, and integrity telemetry
      is complete and bounded.

### Money and lifecycle safety

- [ ] Every valid run ends with balanced ledger and projection checks.
- [ ] Idempotency retries and duplicate webhooks create one monetary effect.
- [ ] Payout holds have exactly one valid closer and no unknown outcome.
- [ ] Outbox/events/notifications/commands drain with no load-caused dead rows.
- [ ] Expected business rejections are explicit and never hide system failures.
- [ ] Aborted runs still drain, reconcile, and preserve evidence.

### Capacity and scale decisions

- [ ] MSSL satisfies every K10 gate in all three confirmation runs.
- [ ] Knee/bottleneck claims use at least two independent signals.
- [ ] B1 has a reproducible `ACTIVATE` or `REJECT` result under K11.
- [ ] B2 has a reproducible `ACTIVATE` or `REJECT` result under K12.
- [ ] B3 has a reproducible `ACTIVATE` or `REJECT` result under K13.
- [ ] Rejected tracks have no speculative implementation plan.
- [ ] Capacity planning applies a 2× safety factor and states profile limits.

### Evidence and automation

- [ ] Committed reports are small, redacted, deterministic, and traceable to raw
      artifact hashes.
- [ ] Raw time series, dumps, tokens, and service logs remain outside Git.
- [ ] PR checks validate harness logic without claiming stable capacity.
- [ ] Manual/scheduled jobs serialize runs and never overwrite a baseline.
- [ ] Full build, vet, lint, race, integration, smoke, business, admin, chaos,
      proto, load-smoke, report, and diff gates are green.

## 11. Global Definition of Done

- [ ] T0–T6 results contain commands, concise evidence, timings, and commit IDs.
- [ ] `docs/performance/capacity-model.md` names the exact environment and does
      not make a production-capacity claim.
- [ ] Every capacity number is accompanied by latency, saturation, drain, and
      integrity evidence.
- [ ] B1–B3 decisions use the original locked thresholds without result-driven
      adjustment.
- [ ] No scale optimization from B1–B3 is implemented inside B0.
- [ ] The plan index and roadmap mark B0 complete only after all canonical runs
      and decisions are recorded here.

## 12. Explicit follow-ups

The following remain outside B0:

1. B1 account sub-sharding, only if K11 returns `ACTIVATE`;
2. B2 partitioning/archival, only if K12 returns `ACTIVATE`;
3. B3 fee/routing cache and invalidation, only if K13 returns `ACTIVATE`;
4. production-like distributed load generation and cloud capacity tests;
5. B2B/merchant workload models after C1 exists;
6. CDC/analytics load after C2 exists;
7. multi-currency and FX workload models after C4 activation;
8. long-term capacity trend storage outside small Git summaries.
