# 45-1 — Track A3: Original Complete Scope

This document records the original A3 proposal for historical traceability. The reviewed execution in [45-2](45-2-a3-core-execution-reviewed.md) takes precedence where the two documents differ.

## Trigger and goals

Plan 40 completed per-process breakers, multi-candidate routing, evidence-based payout pinning, and vendor-failover chaos. The next learning step is external-dependency resilience: durable outbound commands, distributed breaker state, safe Redis degradation, and a real vendor adapter.

Business goals:

- vendor timeouts or process crashes must not lose or duplicate money;
- payout commands must survive process restarts;
- Redis outages must have explicit, observable semantics;
- real adapters must remain behind the vendor-neutral interface.

## Original scope

### K1 — Durable payout command outbox

Add `payout_vendor_commands` with payout request, vendor, attempt, status, retry schedule, lease, error, and timestamps. Enqueue the first command in the same transaction as the `held → submitted` transition. A relay claims commands with `FOR UPDATE SKIP LOCKED`, calls the provider, records the outcome, retries with backoff, reaps stuck leases, and supports bounded admin replay.

Move all vendor submission, outcome classification, breaker recording, and failover from the request path into the relay. Preserve the same payout idempotency key, vendor pinning after accepted/uncertain outcomes, and `payout_vendor_calls` as the evidence source.

### K2 — Distributed breaker

Move breaker state to Redis with atomic Lua transitions and a separate single-probe token. Keep an in-process fallback for controlled degradation, expose backend metrics, and preserve the existing `Allow`, `RecordSuccess`, `RecordFailure`, and `Snapshot` interface.

### K3 — Selective Redis hot-swap

Rate limiting, policy counters, scheduler locks, and fraud velocity must have explicit failure policies. Rate limiting and policy counters may fall back to memory with warnings and recovery probes. Scheduler locks skip a tick when Redis is unavailable. Fraud velocity must not silently fall back to memory; its callers fail closed before money movement.

### K4 — Real vendor adapter

Add a config-gated Xendit sandbox adapter behind `PayinVerifier`/`PayoutProvider`. Verify the current API contract, idempotency headers, webhook authentication, status mapping, and credential handling at execution time. Never commit credentials or make the adapter a CI dependency.

### K5 — Chaos and operational proof

Add scenarios for relay crash, vendor timeout, Redis outage, breaker convergence, failover, and recovery. Every scenario verifies ledger balance, projection consistency, one settle effect, and absence of stuck commands. Add low-cardinality metrics and runbooks.

## Original anti-scope

- no vendor production onboarding or KYB;
- no AML rules in this track;
- no Redis Cluster or multi-region deployment;
- no changes to ledger posting order, RLS, AMQP semantics, or plan 40 pinning rules;
- no claims of exactly-once network delivery;
- no credentials in source, Compose, fixtures, or logs.

## Original verification gate

The proposal required build, vet, lint, unit, integration, smoke, business, and chaos checks from an isolated Compose project, plus explicit evidence for every task. The reviewed document narrows this into the open-source core and keeps Xendit and advanced dashboards as follow-up work.
