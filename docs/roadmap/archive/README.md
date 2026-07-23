# Archived Plans and Decision Records

> [Documentation home](../../README.md) · [Roadmap](../README.md) · **Archive**

> **Status: Historical index.** These files preserve completed, superseded, or
reference decisions. They are not an executable backlog and may describe an older
system shape.

Use the current [architecture](../../reference/architecture.md) and
[services](../../reference/services.md) for runtime truth. Open one archived
record only when you need the reasoning or acceptance evidence for that phase.
The directory has 50 files represented by 48 numbered rows because plan 45
links to two supporting review records.

| # | Document | Scope | Final status |
|---|---|---|---|
| 0 | [00-current-state.md](00-current-state.md) | Initial repository audit (July 2026 snapshot) | Reference |
| 1 | [01-target-architecture.md](01-target-architecture.md) | Initial modular-monolith target and locked decisions (historical) | Reference |
| 2 | [02-feature-roadmap.md](02-feature-roadmap.md) | Fintech-ledger feature research and P0–P3 priorities | Reference |
| 3 | [03-phase-0-cleanup.md](03-phase-0-cleanup.md) | Repository cleanup, migrations, `cmd/` structure, README, and CI | ✅ Done |
| 4 | [04-phase-1-schema.md](04-phase-1-schema.md) | Canonical database schema and related code changes | ✅ Done |
| 5 | [05-phase-1-core-wiring.md](05-phase-1-core-wiring.md) | Account repository, provisioning, HTTP API, and dependency wiring | ✅ Done |
| 6 | [06-phase-1-workers.md](06-phase-1-workers.md) | Outbox relay and ledger-integrity verification workers | ✅ Done |
| 7 | [07-phase-2-hardening.md](07-phase-2-hardening.md) | Reconciliation, snapshots, event contracts, and lifecycle hardening; superseded by 14–16 | Superseded |
| 8 | [08-phase-3-scale.md](08-phase-3-scale.md) | Multi-currency, limits, maker-checker, partitioning, and compliance hooks; superseded by 17–20 | Superseded |
| 9 | [09-hardening-review.md](09-hardening-review.md) | Resource, efficiency, security, and chaos review of the verified MVP | Reference |
| 10 | [10-phase2a-security-gating.md](10-phase2a-security-gating.md) | Internal router, idempotency scope, server-side fees, amount limits, JWT, and HSTS hardening | ✅ Done |
| 11 | [11-phase2b-efficiency-locking.md](11-phase2b-efficiency-locking.md) | Locking redesign, batch inserts, account-resolution caching, UUIDv7, timeouts, and pool tuning | ✅ Done |
| 12 | [12-phase2c-resilience-ops.md](12-phase2c-resilience-ops.md) | Optional Redis, outbox backoff and replay tooling, verifier alerts, optional OTel, and chaos tests | ✅ Done |
| 13 | [13-p1-backlog-review.md](13-p1-backlog-review.md) | Backlog review and locked decisions for the remaining H1–H8 and S1–S9 work | Reference |
| 14 | [14-phase2d-ledger-semantics-events.md](14-phase2d-ledger-semantics-events.md) | Source/destination semantics, atomic lifecycle guards, and versioned events | ✅ Done |
| 15 | [15-phase2e-snapshots-statements.md](15-phase2e-snapshots-statements.md) | Daily balance snapshots, statements, and CSV export | ✅ Done |
| 16 | [16-phase2f-governance-recon-rls.md](16-phase2f-governance-recon-rls.md) | Maker-checker adjustments, external reconciliation, correlation persistence, and RLS | ✅ Done |
| 17 | [17-phase3a-policy-recovery.md](17-phase3a-policy-recovery.md) | Limits, velocity policy, projection rebuilds, and disaster-recovery drills | ✅ Done |
| 18 | [18-phase3b-multi-currency.md](18-phase3b-multi-currency.md) | Currency registry, currency-aware system accounts, and FX primitives | ✅ Done |
| 19 | [19-phase3c-scheduled-accrual.md](19-phase3c-scheduled-accrual.md) | Scheduled transactions, batch disbursement, and interest accrual | ✅ Done |
| 20 | [20-phase3d-aml-reporting.md](20-phase3d-aml-reporting.md) | AML/fraud screening hooks and read-only regulatory reporting | ✅ Done |
| 21 | [21-service-topology-review.md](21-service-topology-review.md) | Long-term monolith-to-services topology and extraction triggers | Reference |
| 22 | [22-phase4a-payin-vendorgw.md](22-phase4a-payin-vendorgw.md) | Pay-in and vendor-gateway modules, webhooks, and replay | ✅ Done |
| 23 | [23-phase4b-payout-orchestration.md](23-phase4b-payout-orchestration.md) | Payout state machine, vendor dispatch, recovery, and crash-mid-flight chaos testing | ✅ Done |
| 24 | [24-extraction-playbook.md](24-extraction-playbook.md) | Module-to-service extraction checklist and internal API inventory | Reference |
| 25 | [25-phase5-business-shell.md](25-phase5-business-shell.md) | Auth, top-up intents, fees, notifications, operations fixes, and business E2E | ✅ Done |
| 26 | [26-phase6a-foundations.md](26-phase6a-foundations.md) | Microservice split master reference and shared foundations | ✅ Done |
| 27 | [27-phase6b-ledger-service.md](27-phase6b-ledger-service.md) | Ledger service extraction, gRPC contract, client, and database cutover | ✅ Done |
| 28 | [28-phase6c-auth-service.md](28-phase6c-auth-service.md) | Auth service extraction and public auth API | ✅ Done |
| 29 | [29-phase6d-payin-service-routing.md](29-phase6d-payin-service-routing.md) | Pay-in service extraction and database-driven top-up routing | ✅ Done |
| 30 | [30-phase6e-payout-service-routing.md](30-phase6e-payout-service-routing.md) | Payout service extraction and database-driven payout routing | ✅ Done |
| 31 | [31-phase6f-fraud-service.md](31-phase6f-fraud-service.md) | Fraud service extraction, gRPC seam, and event-driven velocity checks | ✅ Done |
| 32 | [32-phase6g-gateway-service.md](32-phase6g-gateway-service.md) | Gateway service formalization and final service boundaries | ✅ Done |
| 33 | [33-phase6h-fee-rules.md](33-phase6h-fee-rules.md) | Database-driven fee rules and admin CRUD | ✅ Done |
| 34 | [34-phase6i-verification.md](34-phase6i-verification.md) | Full-stack verification, business E2E, chaos, smoke tests, and documentation | ✅ Done |
| 36 | [36-phase7a-request-tracing.md](36-phase7a-request-tracing.md) | End-to-end request tracing and MVP product master reference | ✅ Done |
| 37 | [37-phase7b-fraud-seam.md](37-phase7b-fraud-seam.md) | Fraud screening outside the posting transaction | ✅ Done |
| 38 | [38-phase7c-fee-quotes.md](38-phase7c-fee-quotes.md) | Fee quotes and exact quote enforcement | ✅ Done |
| 39 | [39-phase7d-kyc-tiers.md](39-phase7d-kyc-tiers.md) | Tiered KYC, admin review, JWT claims, and policy propagation | ✅ Done |
| 40 | [40-phase7e-vendor-resilience.md](40-phase7e-vendor-resilience.md) | Vendor circuit breakers and pre-confirmation payout failover | ✅ Done |
| 41 | [41-phase7f-mvp-acceptance.md](41-phase7f-mvp-acceptance.md) | Final MVP acceptance journey and full verification suite | ✅ Done |
| 43 | [43-a1-observability.md](43-a1-observability.md) | Prometheus, Grafana, Loki, Tempo, OTel, dashboards, and SLO alerting | ✅ Done |
| 44 | [44-a2-ci-pipeline.md](44-a2-ci-pipeline.md) | Full-stack CI gates, scheduled chaos tests, images, and diagnostics | ✅ Done |
| 45 | [45-a3-external-resilience.md](45-a3-external-resilience.md) | External-dependency resilience and dependency-free recovery | ✅ Core done |
| 46 | [46-a4-compliance.md](46-a4-compliance.md) | Durable KYC retries, screening modes, sanctions, encrypted KYC documents, and re-screening | ✅ Done |
| 47 | [47-a5-admin-console.md](47-a5-admin-console.md) | Admin BFF, sessions, maker/checker roles, audit logs, and operations panels | ✅ Done |
| 48 | [48-a10-product-assurance.md](48-a10-product-assurance.md) | Durable product assurance and emergency-intake controls | ✅ Done |
| 49 | [49-a6-internal-security.md](49-a6-internal-security.md) | Threat modeling, mTLS, internal allowlists, fail-closed tokens, Vault, and security drills | ✅ Done |
