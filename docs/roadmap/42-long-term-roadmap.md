# 42 — Long-Term Roadmap: Post-MVP Tracks

> [Documentation home](../README.md) · [Roadmap](README.md)

Created: 2026-07-16. Last reviewed against the live topology: 2026-08-03.

> Reference only. Do not execute this document directly. New execution plans (43 and later) should be created only when a track's activation trigger is satisfied.

This roadmap replaces the completed P0–P3 roadmap in [plan 02](archive/02-feature-roadmap.md) and assumes plans 36–41 as the MVP baseline.

## How to use this roadmap

The repository is learning-first but business-framed: every track teaches a real engineering discipline while remaining relevant to a fintech system.

Each track must define its business goal, learning value, activation trigger, dependencies, work outline, and anti-scope. Measured tracks follow the S4/S5 rule from [plan 13](archive/13-p1-backlog-review.md): do not write speculative implementation plans before metrics prove the need.

When a trigger is met:

1. record the evidence or conscious learning decision;
2. create a self-contained numbered execution document with locked decisions, T1–Tn tasks, tests, DoD, and results;
3. use `make verify-full` for the repeatable non-chaos repository gate, then
   run `make verify-chaos` separately for recovery evidence;
4. update the plan index and this roadmap.

## Track map

| ID | Track | Horizon | Activation trigger | Status |
|---|---|---|---|---|
| A1 | Observability: dashboards, SLOs, alerts, logs, traces | H1 | Cross-service debugging takes more than 30 minutes | Complete via [43](archive/43-a1-observability.md) |
| A2 | Delivery pipeline and local Kubernetes | H1 | CI can be improved anytime; Kubernetes when useful to learn | CI complete via [44](archive/44-a2-ci-pipeline.md); kind remains optional via [35](active/35-phase6j-kubernetes.md); cloud scope begins with [64](active/64-k0-deployment-inventory-baseline.md) under [63](63-kubernetes-cloud-deployment-roadmap-v2.md) |
| A3 | External dependency resilience | H1 | Plan 40 complete and real integration is desired | Core complete via [45](archive/45-2-a3-core-execution-reviewed.md) |
| A4 | Advanced compliance | H1 | Plan 39 complete and compliance engineering is desired | Complete via [46](archive/46-a4-compliance.md) |
| A5 | Admin console | H1 | Manual operations become painful or BFF learning is desired | Complete via [47](archive/47-a5-admin-console.md) |
| A6 | Internal security and service identity | H1 | After MVP; mandatory before B2B | Complete via [49](archive/49-a6-internal-security.md) |
| A7 | Backup, PITR, and disaster recovery | H1 | Any time after MVP | Complete via [50](archive/50-a7-backup-pitr-disaster-recovery.md) |
| A8 | Data lifecycle and privacy | H1 | After MVP; quote cleanup can start earlier | Complete via [51](archive/51-a8-data-lifecycle-privacy.md) |
| A9 | API contracts and schema evolution | H1 | First silent consumer-breaking payload change; mandatory before B2B | Core done via [52](archive/52-a9-api-contracts-schema-evolution.md); manual chaos gate pending |
| A10 | Product assurance and emergency intake control | H1 | Prove consistency across payin, payout, and ledger | Complete via [48](archive/48-a10-product-assurance.md) |
| A11 | Database audit and hardening | H1 | Ongoing — repeated end-to-end schema/security/business-completeness passes | In progress via [65](active/65-a11-database-audit-and-hardening.md); Rounds 1–2 fixed, Round 3 documented and open |
| B0 | Load harness and capacity model | H2 gate | Before any measured scale work | Core done via [53](archive/53-b0-load-capacity-gate.md); canonical measurements and decisions recorded in [2026-07-31 baseline](../performance/reports/2026-07-31-baseline.md) |
| B1 | Hot-account sub-sharding | H2 | B0 proves lock contention in delta application | **REJECT** — real evidence, [2026-07-31 baseline §20](../performance/reports/2026-07-31-baseline.md#20-b1--hot-account-sub-sharding): lock-wait never exceeded 2.24% against a 20% threshold; the split-account variant was measurably worse, not better |
| B2 | Ledger-entry partitioning and archival | H2 | Approximately 50 million ledger entries or equivalent forecast | **REJECT** — real evidence, [2026-07-31 baseline §21](../performance/reports/2026-07-31-baseline.md#21-b2--partitioning): a real D0→D1→D2 growth-curve study found linear ~484 bytes/entry and sub-ms indexed queries through 5M entries, extrapolating to an unremarkable ~19.4GB at the 40M-row gate |
| B3 | Fee and routing resolution cache | H2 | B0 proves per-call resolution is a hotspot | **REJECT** — real evidence, [2026-07-31 baseline §22](../performance/reports/2026-07-31-baseline.md#22-b3--routing-cache): a live cached-vs-uncached A/B against the real fee resolver hit 99.5% cache hit rate with zero throughput gain and worse p95/p99 — the resolver was never the bottleneck |
| C1 | Merchant/B2B API | H3 | A6 and A9 complete | Complete — [plan 57](archive/57-c1-merchant-b2b-api.md) archived |
| C2 | Data platform and revenue analytics | H3 | Analytics queries affect OLTP or CDC learning is desired | Core done — [plan 58](archive/58-c2-data-platform-revenue-analytics.md) archived; Grafana dashboard, some drills/alerts, and a load baseline remain open follow-ups |
| C3 | Multi-channel notifications | H3 | User-facing delivery pipeline learning is desired | ✅ Complete — [plan 59](archive/59-c3-multi-channel-notifications.md) archived 2026-08-05 |
| C4 | End-to-end multi-currency activation | H3 | FX learning is desired; currency primitives are ready | Active via [plan 60](active/60-c4-end-to-end-multi-currency.md); implementation committed; runtime acceptance evidence pending |
| C5 | Advanced financial products | H3 | Accrual and fee quotes are complete; period-close learning is desired | Active via [plan 61](active/61-c5-advanced-financial-products-period-close.md); implementation committed; runtime acceptance evidence pending |
| C6 | Zero-downtime migration engine | H3 | A large live migration or migration practice is needed | Active via [plan 62](active/62-c6-zero-downtime-migration-engine.md); implementation committed; runtime acceptance evidence pending |

## Horizon 1 — Operational foundations

### A1 — Observability

Business goal: operate a money system with measurable SLOs and fast incident response. Learning goals include Prometheus, Grafana, Loki, Tempo, OpenTelemetry across HTTP/gRPC/AMQP, RED metrics, burn-rate alerts, and correlated structured logs. Keep observability in a separate Compose profile and avoid per-user metric cardinality. Complete via plan 43.

### A2 — Delivery pipeline

The CI portion is complete via plan 44: runtime changes run lint/tests,
integration tests, and a nine-image container smoke gate; a weekly and
manually dispatchable workflow runs the business journey and all chaos
scenarios. Locally, `make verify-full` covers the repeatable non-chaos gate and
`make verify-chaos` is the operator-controlled recovery gate. Documentation-only
changes use a fast path. Local kind work remains the optional plan 35. The
cloud-learning scope is separately reviewed in [plan 63](63-kubernetes-cloud-deployment-roadmap-v2.md),
with [plan 64 · K0](active/64-k0-deployment-inventory-baseline.md) as its
inventory-only first gate. Do not expand this into cloud CD, GitOps, or
multi-cluster operations before those plans' gates pass.

### A3 — External resilience

The core track covers durable outbound payout commands, a Redis-backed breaker with measured local fallback, selective Redis hot-swap, fraud velocity semantics, and chaos scenarios. Real vendor adapters may remain optional and config-gated. Do not require production vendor onboarding or multi-region Redis.

### A4 — Compliance

The completed track covers KYC retry and downgrade, JWT staleness controls, per-rule screening mode, durable screening events, local sanctions data, encrypted documents, periodic re-screening, and a config-gated HTTP KYC sandbox. Production provider contracts and legal licensing remain outside this learning repository.

### A5 — Admin console

Build a thin admin BFF with server-side sessions, CSRF, maker/checker roles, append-only audit logs, and a Go `html/template` + htmx UI. Keep business logic in downstream services and do not let the BFF access service databases directly. Complete via plan 47.

### A6 — Internal security

Threat-model the real topology, add service identity and mTLS, rotate certificates, move secrets into a dev Vault workflow, enforce internal token fail-closed behavior, and perform evidence-based pentest-style review. This is mandatory before exposing B2B APIs. Complete via plan 49.

### A7 — Backup and PITR

Automate backups, point-in-time restore, integrity verification, cross-database reconciliation, RPO/RTO measurement, and scheduled game-day drills. Do not expand to streaming replicas or multi-region failover. Complete via [plan 50](archive/50-a7-backup-pitr-disaster-recovery.md).

### A8 — Data lifecycle and privacy

Define retention by table, purge expired fee quotes and privacy-sensitive idempotency data, protect sensitive auth/KYC fields, provide user exports, and pseudonymize user references without modifying immutable ledger entries. Formal legal GDPR certification is out of scope. Execution is recorded in [plan 51](archive/51-a8-data-lifecycle-privacy.md).

### A9 — Contracts and schema evolution

Add HTTP contract tests, OpenAPI linting, event v1/v2 expand-contract rules, tolerant readers, deprecation policy, and sunset headers. gRPC already has Buf checks; do not create a separate schema registry unless evidence requires it. Execution is recorded in [archived plan 52](archive/52-a9-api-contracts-schema-evolution.md).

### A10 — Product assurance

Continuously compare payin and payout lifecycle state with ledger evidence without cross-database joins. Persist findings, backfill safely, deduplicate alerts, and provide durable pause/resume controls for new intake while allowing in-flight money to settle. This is not a replacement for ledger double-entry verification, compliance, fraud, or admin UI.

## Horizon 2 — Measured scale work

### B0 — Load and capacity gate

Build k6 scenarios for P2P posting, webhook bursts, payout batches, and mixed MVP journeys. Measure throughput, latency, outbox lag, database pool saturation, and lock waits. Produce numerical thresholds that either activate or reject B1–B3. Execution is recorded in [archived plan 53](archive/53-b0-load-capacity-gate.md).

### B1 — Hot-account sub-sharding — **REJECT**

Decided: [2026-07-31 baseline §16.3, §20](../performance/reports/2026-07-31-baseline.md#20-b1--hot-account-sub-sharding).
Six alternating one-account/two-account runs at the confirmed hot-account
MSSL, with `tools/loadprobe` extended to sample `wait_event_type = 'Lock'`
specifically, found lock-wait share never exceeded 2.24% against the 20%
activation threshold, and the split-account variant was measurably *worse*
on throughput and p95, not better. Do not reopen without a materially
different, larger-scale signal (e.g. real production contention data) — a
re-run of this same experiment on the same profile would not be new
evidence.

### B2 — Ledger partitioning and archival — **REJECT**

Decided: [2026-07-31 baseline §21](../performance/reports/2026-07-31-baseline.md#21-b2--partitioning).
A real bulk-loaded, balance-verified D0→D1→D2 growth-curve study (up to
5,000,000 ledger entries) found linear ~484 bytes/entry growth (table+index
combined) and stable sub-millisecond indexed queries at every scale tested —
extrapolating linearly to the ~40M-row activation gate gives an unremarkable
~19.4GB for a single table, with no measured query-degradation symptom to
independently justify partitioning. Dataset scale in this codebase's own
self-seeded scenarios also remains three-plus orders of magnitude below the
threshold. Reopen only if real production volume approaches the documented
row threshold *and* a real symptom (vacuum pressure, retention pain, a
specific degraded query) is observed — size alone at this profile's own
measured growth rate does not justify it.

### B3 — Fee and routing cache — **REJECT**

Decided: [2026-07-31 baseline §22](../performance/reports/2026-07-31-baseline.md#22-b3--routing-cache).
A real `CachingFeeRepository` test-double was built and run against the
actual fee-quote resolver: a 5s-TTL cache achieved a 99.5% cache hit rate
(far above the 80% cacheability threshold) yet produced statistically zero
throughput change and *worse* p95/p99 than the uncached baseline at the
tested load — the resolver's own cost (~0.06ms) is negligible against total
request latency (~3ms) at that load level, so eliminating 99.5% of its calls
was undetectable end to end. Reopen only with evidence the resolver is
contended at a load level where its cost is no longer negligible relative to
total request latency — this experiment does not rule that out at higher
load, only at the load actually tested.

## Horizon 3 — Business enablement

- **C1:** API keys, scopes, quotas, merchant endpoints, signed outbound webhooks, retry/DLQ, and sandbox tenants. Requires A6 and A9.
- **C2:** WAL CDC, local warehouse, revenue facts, unit economics dashboards, and reconciliation back to ledger totals. Do not move regulatory views until evidence warrants it.
- **C3:** Versioned templates, in-app/email/push channels, preferences, per-channel retries, and digest delivery. Avoid paid providers in the learning baseline.
- **C4:** Non-IDR top-up, transfer, payout, per-currency policy, FX position handling, and anti-mixing chaos tests. Use mock rates, not a real bank corridor.
- **C5:** Monthly interest capitalization, schedule failure policy, and top-up fees, with explicit review of the old scheduled-policy bypass decision.
- **C6:** Shadow reads, dual writes, reconciliation, gradual cutover, and instant rollback for a real or synthetic migration. This is the production machinery intentionally deferred by plan 24.

## Recommended next sequence

The original learning-value ranking included A1 and A3, which are now
complete. A7, A8, A9, B0, and the VendorService boundary now have archived
core foundations, with their remaining live evidence explicitly tracked in
those records. C2–C6 now have implementation committed, but each remains
active until its runtime and acceptance evidence is recorded. F0 remains
planned and C1 is archived. B1, B2, and B3 were each gated on a measured
result before implementation, per the rule that only `ACTIVATE` opens an
execution plan; all three measured `REJECT` (§B1–B3 above), so none has an
implementation plan and none should get one without new evidence.

## Global anti-goals

- No multi-region or active-active deployment; use backup, PITR, and drills.
- No real-money licensing or formal certification claims.
- No go-to-market, marketing, or pricing strategy.
- No additional service extraction without a new evidence-based trigger; the
  current nine-core-service topology is the baseline; the local mock push
  provider is an optional support process, not a new business service.

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
