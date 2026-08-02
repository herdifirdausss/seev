# B0 load scenarios

Scenario scripts use open arrival-rate executors. The canonical set is W1–W7:
P2P, signed VendorService webhook, payout, mixed flow, hotspot, resolver, and
ledger-size read ladder. W7 is a bounded read probe and must be run against a
separately prepared 100k/1m/5m ledger-size dataset.

Business scenarios must never contain real credentials, vendor secrets, or
personal data. `smoke.js` is a non-claiming health/bootstrap check used by the
load Compose profile. `scripts/load-test.sh smoke|run` starts k6, records the
manifest and lifecycle markers under the exact run artifact directory, and
tears down the disposable project unless `SEEV_LOAD_KEEP_STACK=1` is set.
Business scenarios additionally write their redacted k6 summary there.

The Compose profile mounts the repository's mTLS identities read-only for the
services and Prometheus. Run `make certs` once before the first load smoke or
business run; the command is idempotent and does not use production
credentials. The profile keeps Redis and RabbitMQ internal and uses dedicated
load-only host ports for Postgres and Prometheus, so a development stack may
remain running.

W1, W2, W3, W4, and W5 self-seed (Phase 0 §24.1/§24.2,
`tests/load/lib/seed.js`): k6's
`setup()` logs in as the disposable admin identity
`deploy/load/compose.load.yaml` bootstraps into auth-service
(`SEEV_LOAD_ADMIN_EMAIL`/`SEEV_LOAD_ADMIN_PASSWORD`, defaulted). No
operator-supplied token is needed for any of the five.

W1 ensures the mockvendor topup route exists and registers+funds a POOL of
sender/recipient pairs (`scripts/load-postgres-init/04-load-policy-limits.sh`
below is why this is a pool, not one pair), each funded through a real
topup-intent + signed webhook — sized off the run's own offered rate/duration.

W2 registers one user and pre-creates one pending topup intent per planned
iteration. Every 10th iteration is an exact redelivery — the same
`event_id`/reference as the delivery one iteration earlier in that VU's own
history — K7's "separately tagged 10% exact webhook redelivery stream".
Settlement correctness (no duplicate financial effect from a redelivery) is
proven by the ledger integrity check `scripts/load-test.sh` already runs
after every load phase (§24.3), not by a per-iteration status poll, which
would conflate settlement-confirmation latency with webhook-handler
latency in the same measurement.

W5 (`SEEV_LOAD_HOTSPOT_VARIANT=one|two`, default `one`) is the K11 hot-account
experiment behind the B1 (hot-account sub-sharding) decision — W2-shaped
webhook bursts settling against a shared system account (`one`) versus an
evenly split second account (`two`, registers a second user pinned to
`mockvendor2` via a payin routing override). `setup()` pre-creates one
pending topup intent per planned iteration so the load phase measures
webhook/lock contention, not intent-creation cost. Actually running the
locked K11 protocol (alternating `A-B-B-A` runs, `cmd/loadprobe` lock-wait
sampling, the throughput/p95 comparison) is a Phase 3/4 execution step, not
part of this harness.

W3 KYC-approves and funds a POOL of senders, then includes terminal-status
polling in each workload unit because payout settlement is asynchronous.

W4 funds a POOL of sender/recipient pairs and runs K8's fixed action mix (35%
quote+transfer, 20% read, 20% topup+webhook, 15% payout, 5% notifications,
5% login) via `Math.random()`-weighted selection against fixed cumulative
thresholds — not a `__ITER`-based cycle: `__ITER` is per-VU in k6, and under
real concurrent load most VUs never accumulate enough of their own
iterations to reach a low-probability branch, so a modulo cycle silently
starves the categories near the end of the range.

W1/W3/W4 use a POOL of accounts, not one shared sender, for two reasons:
K7's own design intent ("W1 uses disjoint funded account pairs" — realistic
concurrent traffic across genuinely different accounts, not one pair hit
repeatedly), and because `transfer_p2p`/`withdraw_initiate` are gated by a
per-(user, transaction_type) daily count policy limit
(`migrations/ledger/000022_policy_tier_limits.up.sql` — 20/day and 5/day at
KYC level 1) that a single shared sender hits mid-run, discovered live as a
422 "policy limit exceeded (max_daily_count)". `money_in` (topup
settlement) is not gated this way — it posts through payin's internal
system path, not a public user-initiated endpoint, which is why W2/W5's
single-user pools never hit this. `scripts/load-postgres-init/04-load-policy-limits.sh`
additionally raises every KYC tier's limits to effectively unbounded for
this disposable database only (production's migration and every other
environment are untouched), so pool size above is purely a concurrency/
realism knob, not a compliance workaround — a deliberately small, fixed
pool (as W5 already does for its hot-account experiment) is how to test
the opposite end of that spectrum on purpose.

Every other business scenario still needs a disposable `SEEV_LOAD_TOKEN`;
without it the runner refuses to start, so an unauthenticated 401 cannot be
reported as successful load traffic.

After every `run`, the harness waits for the ledger outbox to drain and runs
the ledger balance, account-projection, and pending-transaction checks before
teardown. It patches the redacted k6 summary with drain time and the integrity
result. The measurement phase also samples per-container CPU/memory (`docker
stats`, `resource-timeseries.jsonl`) and PostgreSQL activity/locks/top
statements (`cmd/loadprobe`, `postgres-summary.jsonl`); right after, it reads
application connection-pool in-use/wait from the load Prometheus instance
(`pool-summary.json`) and RabbitMQ queue depth/consumers via `rabbitmqctl`
(`broker-summary.json`, RabbitMQ's own management port is deliberately not
published to the host) — Phase 0 §24.4. `gate_passed` remains false
regardless: turning that evidence into a pass/fail against
`deploy/load/thresholds.yaml` is `cmd/loadreport`'s job (`-thresholds`), not
this script's. Redis throughput/backlog is not yet collected.
