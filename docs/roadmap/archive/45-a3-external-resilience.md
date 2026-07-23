# 45 — Track A3: External Dependency Resilience

Status: vendor-neutral core complete (2026-07-17). Optional real-vendor and
advanced observability work remains follow-up scope. This index is derived
from Track A3 in [plan 42](../42-long-term-roadmap.md) and extends the
anti-double-payout invariants from [plan 40](40-phase7e-vendor-resilience.md).

## Documents

1. [45-1 — Original complete scope](45-1-a3-original-complete.md) preserves the historical requirement inventory and decisions.
2. [45-2 — Reviewed core execution](45-2-a3-core-execution-reviewed.md) is the source of truth for the safe, vendor-neutral, open-source core.

The reviewed document supersedes the original only when security, durability, or verification semantics conflict. Other original requirements remain valid or are explicitly assigned to the follow-up phase.

## Decisions that supersede the original scope

| Area | Original idea | Reviewed execution decision |
|---|---|---|
| Vendor dispatch | Implied exactly-once network delivery | At-least-once delivery, the same vendor idempotency key, and one ledger effect. Network exactly-once cannot be proven after a timeout. |
| Fraud when Redis is down | Per-replica memory velocity store | Fail closed with `DEPENDENCY_UNAVAILABLE` before money movement. A memory approximation could silently weaken thresholds. |
| Xendit | Required in the core DoD | Optional follow-up after the free/open-source core. |
| Distributed breaker | Active by default | Opt in only after integration and chaos gates pass. |
| Final gate | Generic `down -v` | Use an isolated Compose project so development volumes cannot be deleted accidentally. |
| Observability | Dashboard provisioning blocks the core | Low-cardinality metrics are required; dashboards and alerts follow after the core stabilizes. |

## Core execution order

Follow 45-2:

1. T0 — contract, schema, RLS, and atomic enqueue;
2. T1 — payout relay becomes the only owner of `provider.Submit`;
3. T2 — Redis distributed breaker with local fallback;
4. T3 — selective Redis degradation and fail-closed fraud velocity;
5. T4 — chaos, regressions, and isolated final gate.

The core uses PostgreSQL, Redis, Go, Docker Compose, Prometheus, `httptest`, and testcontainers only. No vendor credentials or paid services are required.

## Follow-up scope

The original document may still supply separate optional tasks for:

- a config-gated Xendit sandbox adapter;
- advanced dashboard and alert coverage beyond the required core metrics;
- dynamic container discovery for parallel Compose projects.

These requirements are deferred, not deleted, so an external vendor cannot block the resilience core.

## Aggregate DoD

- [x] Every core DoD in 45-2 is complete.
- [x] Every original requirement is implemented, superseded by the table above, or assigned to follow-up work.
- [x] No document claims exactly-once network dispatch.
- [x] Fraud velocity cannot become a silent bypass when Redis is unavailable.
- [x] Core gates run without external credentials or paid services.
- [x] Xendit and advanced observability remain traceable through 45-1.
- [x] Executed core tasks record their results and verification evidence in 45-2.
