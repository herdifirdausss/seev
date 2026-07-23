# 42 — Long-Term Roadmap: Post-MVP Tracks

> [Documentation home](../README.md) · [Roadmap](README.md)

Created: 2026-07-16. Last reviewed against the live topology: 2026-07-22.

> Reference only. Do not execute this document directly. New execution plans (43 and later) should be created only when a track's activation trigger is satisfied.

This roadmap replaces the completed P0–P3 roadmap in [plan 02](archive/02-feature-roadmap.md) and assumes plans 36–41 as the MVP baseline.

## How to use this roadmap

The repository is learning-first but business-framed: every track teaches a real engineering discipline while remaining relevant to a fintech system.

Each track must define its business goal, learning value, activation trigger, dependencies, work outline, and anti-scope. Measured tracks follow the S4/S5 rule from [plan 13](archive/13-p1-backlog-review.md): do not write speculative implementation plans before metrics prove the need.

When a trigger is met:

1. record the evidence or conscious learning decision;
2. create a self-contained numbered execution document with locked decisions, T1–Tn tasks, tests, DoD, and results;
3. use the full repository gate: lint, tests, both vet modes, smoke, business journey, and chaos;
4. update the plan index and this roadmap.

## Track map

| ID | Track | Horizon | Activation trigger | Status |
|---|---|---|---|---|
| A1 | Observability: dashboards, SLOs, alerts, logs, traces | H1 | Cross-service debugging takes more than 30 minutes | Complete via [43](archive/43-a1-observability.md) |
| A2 | Delivery pipeline and local Kubernetes | H1 | CI can be improved anytime; Kubernetes when useful to learn | CI complete via [44](archive/44-a2-ci-pipeline.md); kind remains optional via [35](active/35-phase6j-kubernetes.md) |
| A3 | External dependency resilience | H1 | Plan 40 complete and real integration is desired | Core complete via [45](archive/45-2-a3-core-execution-reviewed.md) |
| A4 | Advanced compliance | H1 | Plan 39 complete and compliance engineering is desired | Complete via [46](archive/46-a4-compliance.md) |
| A5 | Admin console | H1 | Manual operations become painful or BFF learning is desired | Complete via [47](archive/47-a5-admin-console.md) |
| A6 | Internal security and service identity | H1 | After MVP; mandatory before B2B | Complete via [49](archive/49-a6-internal-security.md) |
| A7 | Backup, PITR, and disaster recovery | H1 | Any time after MVP | Planned via [50](active/50-a7-backup-pitr-disaster-recovery.md) |
| A8 | Data lifecycle and privacy | H1 | After MVP; quote cleanup can start earlier | Planned via [51](active/51-a8-data-lifecycle-privacy.md) |
| A9 | API contracts and schema evolution | H1 | First silent consumer-breaking payload change; mandatory before B2B | Planned via [52](active/52-a9-api-contracts-schema-evolution.md) |
| A10 | Product assurance and emergency intake control | H1 | Prove consistency across payin, payout, and ledger | Complete via [48](archive/48-a10-product-assurance.md) |
| B0 | Load harness and capacity model | H2 gate | Before any measured scale work | Planned via [53](active/53-b0-load-capacity-gate.md) |
| B1 | Hot-account sub-sharding | H2 | B0 proves lock contention in delta application | Future |
| B2 | Ledger-entry partitioning and archival | H2 | Approximately 50 million ledger entries or equivalent forecast | Future |
| B3 | Fee and routing resolution cache | H2 | B0 proves per-call resolution is a hotspot | Future |
| C1 | Merchant/B2B API | H3 | A6 and A9 complete | Future |
| C2 | Data platform and revenue analytics | H3 | Analytics queries affect OLTP or CDC learning is desired | Future |
| C3 | Multi-channel notifications | H3 | User-facing delivery pipeline learning is desired | Future |
| C4 | End-to-end multi-currency activation | H3 | FX learning is desired; currency primitives are ready | Future |
| C5 | Advanced financial products | H3 | Accrual and fee quotes are complete; period-close learning is desired | Future |
| C6 | Zero-downtime migration engine | H3 | A large live migration or migration practice is needed | Future |

## Horizon 1 — Operational foundations

### A1 — Observability

Business goal: operate a money system with measurable SLOs and fast incident response. Learning goals include Prometheus, Grafana, Loki, Tempo, OpenTelemetry across HTTP/gRPC/AMQP, RED metrics, burn-rate alerts, and correlated structured logs. Keep observability in a separate Compose profile and avoid per-user metric cardinality. Complete via plan 43.

### A2 — Delivery pipeline

The CI portion is complete via plan 44: runtime changes run lint/tests,
integration tests, and an eight-image container smoke gate; a weekly and
manually dispatchable workflow runs the business journey and all chaos
scenarios. Documentation-only changes use a fast path. Local kind work remains
the optional plan 35. Do not expand this into cloud CD, GitOps, or
multi-cluster operations.

### A3 — External resilience

The core track covers durable outbound payout commands, a Redis-backed breaker with measured local fallback, selective Redis hot-swap, fraud velocity semantics, and chaos scenarios. Real vendor adapters may remain optional and config-gated. Do not require production vendor onboarding or multi-region Redis.

### A4 — Compliance

The completed track covers KYC retry and downgrade, JWT staleness controls, per-rule screening mode, durable screening events, local sanctions data, encrypted documents, periodic re-screening, and a config-gated HTTP KYC sandbox. Production provider contracts and legal licensing remain outside this learning repository.

### A5 — Admin console

Build a thin admin BFF with server-side sessions, CSRF, maker/checker roles, append-only audit logs, and a Go `html/template` + htmx UI. Keep business logic in downstream services and do not let the BFF access service databases directly. Complete via plan 47.

### A6 — Internal security

Threat-model the real topology, add service identity and mTLS, rotate certificates, move secrets into a dev Vault workflow, enforce internal token fail-closed behavior, and perform evidence-based pentest-style review. This is mandatory before exposing B2B APIs. Complete via plan 49.

### A7 — Backup and PITR

Automate backups, point-in-time restore, integrity verification, cross-database reconciliation, RPO/RTO measurement, and scheduled game-day drills. Do not expand to streaming replicas or multi-region failover. Execution is defined in [plan 50](active/50-a7-backup-pitr-disaster-recovery.md).

### A8 — Data lifecycle and privacy

Define retention by table, purge expired fee quotes and privacy-sensitive idempotency data, protect sensitive auth/KYC fields, provide user exports, and pseudonymize user references without modifying immutable ledger entries. Formal legal GDPR certification is out of scope. Execution is defined in [plan 51](active/51-a8-data-lifecycle-privacy.md).

### A9 — Contracts and schema evolution

Add HTTP contract tests, OpenAPI linting, event v1/v2 expand-contract rules, tolerant readers, deprecation policy, and sunset headers. gRPC already has Buf checks; do not create a separate schema registry unless evidence requires it. Execution is defined in [plan 52](active/52-a9-api-contracts-schema-evolution.md).

### A10 — Product assurance

Continuously compare payin and payout lifecycle state with ledger evidence without cross-database joins. Persist findings, backfill safely, deduplicate alerts, and provide durable pause/resume controls for new intake while allowing in-flight money to settle. This is not a replacement for ledger double-entry verification, compliance, fraud, or admin UI.

## Horizon 2 — Measured scale work

### B0 — Load and capacity gate

Build k6 scenarios for P2P posting, webhook bursts, payout batches, and mixed MVP journeys. Measure throughput, latency, outbox lag, database pool saturation, and lock waits. Produce numerical thresholds that either activate or reject B1–B3. Execution is defined in [plan 53](active/53-b0-load-capacity-gate.md).

### B1 — Hot-account sub-sharding

Only after B0 proves system-account lock contention, design account shards, aggregate reads, backfill safely, and compare before/after verifier and snapshot behavior. Never shard user accounts without evidence.

### B2 — Ledger partitioning and archival

Only near the documented row threshold, validate the old partitioning guide against the split ledger schema, perform time-range partitioning and archival, preserve snapshot/as-of queries, and drill restore. This is not horizontal database sharding.

### B3 — Fee and routing cache

Measure first. If resolution is a proven hotspot, add a short-lived cache with explicit invalidation and hit-rate metrics. Fee quotes remain authoritative, so cached rules can never reprice an already quoted transaction.

## Horizon 3 — Business enablement

- **C1:** API keys, scopes, quotas, merchant endpoints, signed outbound webhooks, retry/DLQ, and sandbox tenants. Requires A6 and A9.
- **C2:** WAL CDC, local warehouse, revenue facts, unit economics dashboards, and reconciliation back to ledger totals. Do not move regulatory views until evidence warrants it.
- **C3:** Versioned templates, in-app/email/push channels, preferences, per-channel retries, and digest delivery. Avoid paid providers in the learning baseline.
- **C4:** Non-IDR top-up, transfer, payout, per-currency policy, FX position handling, and anti-mixing chaos tests. Use mock rates, not a real bank corridor.
- **C5:** Monthly interest capitalization, schedule failure policy, and top-up fees, with explicit review of the old scheduled-policy bypass decision.
- **C6:** Shadow reads, dual writes, reconciliation, gradual cutover, and instant rollback for a real or synthetic migration. This is the production machinery intentionally deferred by plan 24.

## Recommended next sequence

The original learning-value ranking included A1 and A3, which are now
complete. Among prepared but unfinished work, the recommended sequence is A7
→ A8 → A9 → B0. After B0, create B1, B2, or B3 execution plans only for gates
whose measured result is `ACTIVATE`; a `REJECT` result closes that candidate
without implementation.

## Global anti-goals

- No multi-region or active-active deployment; use backup, PITR, and drills.
- No real-money licensing or formal certification claims.
- No go-to-market, marketing, or pricing strategy.
- No additional service extraction without a new evidence-based trigger; the
  current eight-service topology is the baseline.

## Traceability

| Existing debt or deferral | Destination |
|---|---|
| Admin BFF, mTLS, outbound payout outbox, real adapter | A5, A6, A3 |
| Top-up fees, real KYC provider, document storage, tier retry | C5, A4 |
| Distributed breaker, Redis semantics, quote purge | A3, A8 |
| Non-IDR E2E and currency refresh | C4 |
| OTel, dashboards, SLOs, alerts | A1 |
| Smoke/E2E/chaos CI gap | A2 |
| API/event versioning | A9 |
| Load measurement and lock evidence | B0–B3 |
| CDC, B2B, multi-channel notifications, privacy | C2, C1, C3, A8 |
| Dual-write and shadow traffic | C6 |
| kind/Kubernetes | A2 / plan 35 |

## Checklist for future execution documents

- [ ] Trigger evidence or conscious learning decision is written at the top.
- [ ] Design decisions are locked before implementation tasks.
- [ ] Tasks, migrations, tests, DoD, and results are self-contained.
- [ ] Full repository gate is defined and passed.
- [ ] Anti-scope is copied and honored.
- [ ] Plan index and roadmap status are updated after completion.
