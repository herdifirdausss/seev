# 45-2 — Track A3: Reviewed Core Execution

This is the implementation source of truth for the safe, open-source portion of Track A3. It supersedes conflicting semantics in [45-1](45-1-a3-original-complete.md).

## Goal and anti-scope

Make outbound payout commands durable, make breaker state shareable across replicas, and make Redis degradation explicit without silently weakening fraud controls.

The core does not require proprietary vendors, paid services, Redis Cluster, multi-region deployment, or changes to ledger balancing, RLS, AMQP semantics, `execTransfer`, or plan 40 pinning. It does not claim exactly-once network delivery.

## Locked decisions

### K1 — Durable command, at-least-once dispatch

Migration `payout/000006_vendor_commands` adds `payout_vendor_commands` with a unique command key, payout request, vendor, attempt, pending/processing/failed/completed/dead state, retry budget, lease timestamps, next-attempt time, and error. Add claim indexes, a partial unique live-command constraint, grants, and RLS.

Enqueue the first command atomically with `held → submitted`. The command key is `payout:<request-id>:submit:<attempt>`; the provider still receives the payout request ID as its idempotency key. Amount, currency, destination, and user remain immutable in `payout_requests`.

### K2 — Relay owns `Submit`

The payout relay claims with `FOR UPDATE SKIP LOCKED`, calls the vendor with an explicit timeout, persists `payout_vendor_calls` before changing payout state, retries with exponential backoff and jitter, reaps expired leases, and dead-letters after the retry budget. Admin replay is bounded.

`provider.Submit` must not be called outside the relay. Resume owns recovery of `created`/`held`, `vendor_pending` queries, and stuck metrics; it only ensures commands exist. A rejected call may enqueue the next candidate when `mayFailover` allows it. Accepted or uncertain outcomes pin the payout to that vendor.

### K3 — Distributed breaker

Keep the existing breaker interface but add a Redis backend with atomic Lua transitions, cooldown, and a cross-replica single-probe token. On Redis errors, fall back to the existing local tracker with warning and low-cardinality backend metrics. Redis becomes authoritative again after recovery; local state is not merged.

`BREAKER_DISTRIBUTED=false` preserves the previous local behavior. The breaker is an availability optimization, never the monetary safety control.

### K4 — Selective Redis degradation

Rate limiting and policy counters may move between Redis and memory through a lifecycle-managed wrapper with health probes and hysteresis. Scheduler locks continue to skip a tick when Redis is unavailable. Fraud velocity never falls back to memory: unavailable Redis returns a dependency error, and all three screening callers fail closed before money movement.

### K5 — Vendor-neutral core

Use mockvendor and `httptest` as the conformance harness for timeout, rejection, pending, duplicate idempotency, and authentication behavior. Xendit or another proprietary adapter is a separate, default-off follow-up and is not part of CI or the core DoD.

### K6 — Required metrics

Add only low-cardinality metrics for command states and attempts, reaped commands, breaker backend, Redis backend per primitive, and scheduler skips. Dashboard and alert provisioning follow the plan 43 observability stack and do not block the core implementation.

## Execution tasks

### T0 — Contract and schema

Implement the command model, migration, repository claim/complete/fail/reap/replay operations, and rollback tests. The database transaction must never commit a state transition without its command, or a command without its transition.

**Result:** migration, constraints, grants, RLS, repository operations, and rollback/race tests passed against real PostgreSQL.

### T1 — Relay and orchestration refactor

Move all vendor submission and outcome classification into the relay. Refactor create, resume, and failover to enqueue atomically. Update business and chaos scripts from synchronous assumptions to status polling.

**DoD:** no `provider.Submit` exists outside the relay; crashes after enqueue, claim, audit, and vendor calls remain safe.

**Result:** command relay, lease reaper, retries, dead-letter behavior, failover, and idempotent ledger settlement passed the full regression suite.

### T2 — Distributed breaker

Implement Redis Lua state transitions, cross-replica half-open probing, configuration wiring, snapshots, and local fallback. Test two real service instances sharing breaker state.

**Result:** open state converges across replicas, one probe wins, Redis outages degrade without crashing, and recovery returns to Redis without a restart.

### T3 — Safe Redis degradation

Add backend-switching wrappers for the rate limiter and policy counter. Make fraud-service start degraded and recover its Redis velocity store. Map unavailable fraud velocity to 503/`DEPENDENCY_UNAVAILABLE` before money movement. Add scheduler skip metrics.

**Result:** Redis stop/start tests prove limiter and policy recovery without process restart, while fraud remains fail closed. No memory fallback is used for fraud velocity or multi-replica scheduler locking.

### T4 — Chaos and final gate

Add three scenarios:

1. Redis outage with limiter/policy memory fallback and fraud fail-closed;
2. distributed breaker state across two payout replicas and local degradation;
3. crashes after command enqueue and after an uncertain vendor call.

Use an isolated Compose project for the gate so cleanup cannot remove the default development volume. Verify at-least-once command delivery, one ledger effect, and vendor pinning after uncertainty.

**Result:** scenarios 9–11 were added, along with hardened helpers and metrics assertions. Real execution found and fixed test-harness issues involving rate-limit connection reuse, invalid zero policy counts, cross-replica force-fail setup, stale Redis breaker state, and gRPC reconnect timing. The pre-existing scenario-5 timing flake was reproduced in isolation and not changed.

## Required test matrix

| Area | Evidence |
|---|---|
| Atomic enqueue and rollback | Database integration tests |
| Claim and lease ownership | Concurrent relay tests |
| Timeout/unknown result | Pinned vendor and same-key retry |
| Rejection | Candidate failover or idempotent cancellation |
| Dead-letter/replay | Bounded retry and admin replay |
| Breaker | Cross-replica single probe |
| Redis outage | Degrade, recover, and no flapping |
| Fraud outage | 503 before money movement |
| Metrics | Enum-only label audit |

## Definition of done

- [x] First payout command and `held → submitted` transition are atomic.
- [x] One live command exists per payout request.
- [x] The relay is the only owner of vendor submission.
- [x] Delivery is documented and tested as at-least-once with one ledger effect.
- [x] Accepted and uncertain calls pin the vendor; audit failures fail closed.
- [x] Breaker state converges across replicas and degrades locally when Redis is unavailable.
- [x] Rate limiter and policy counter recover without restart; fraud never silently bypasses screening.
- [x] Unit, integration, race, lint, vet, business, smoke, and chaos gates pass using local open-source components.
- [x] No proprietary credential or paid service is required by the core gate.

## Final result

The isolated final gate passed with build, vet, lint, race tests, smoke, business-E2E, and 11 chaos scenarios. The core A3 track is complete. Real vendor adapters, advanced dashboards, and dynamic container discovery remain follow-up work from the original scope.
