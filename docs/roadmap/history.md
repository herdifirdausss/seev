# Roadmap History

Concise record of completed and superseded plans. Each row names the outcome;
detailed implementation history lives in Git. Active plans are in
[`active/`](active/README.md).

| Plan | Title | Outcome | Completed | Durable reference |
|---|---|---|---|---|
| 03 | Phase 0 cleanup | Repository cleanup and baseline established | — | `reference/architecture.md` |
| 04–06 | Phase 1 (schema, core wiring, workers) | Core ledger schema and service wiring complete | — | `reference/services.md` |
| 07 | Phase 2 hardening | Hardening pass complete | — | `security/threat-model.md` |
| 08 | Phase 3 scale | Scale foundations complete | — | `reference/architecture.md` |
| 09 | Hardening review | Review triaged and closed | — | `security/threat-model.md` |
| 10 | Phase 2a security gating | Security gating implemented | — | `security/threat-model.md` |
| 11 | Phase 2b efficiency locking | Efficiency and locking implemented | — | `reference/services.md` |
| 12 | Phase 2c resilience ops | Resilience and ops implemented | — | `operations/README.md` |
| 13 | P1 backlog review | Backlog reviewed and closed | — | `roadmap/README.md` |
| 14 | Phase 2d ledger semantics + events | Ledger outbox and event model implemented | — | `reference/events.md` |
| 15 | Phase 2e snapshots + statements | Snapshots and statements implemented | — | `reference/services.md` |
| 16 | Phase 2f governance, recon, RLS | Governance, reconciliation, RLS implemented | — | `reference/services.md` |
| 17 | Phase 3a policy recovery | Policy recovery implemented | — | `reference/services.md` |
| 18 | Phase 3b multi-currency | Multi-currency foundations implemented | — | `reference/services.md` |
| 19 | Phase 3c scheduled accrual | Scheduled accrual implemented | — | `reference/services.md` |
| 20 | Phase 3d AML reporting | AML/compliance reporting implemented | — | `reference/services.md` |
| 21 | Service topology review | Service topology finalized | — | `reference/decisions/0001-service-centric-repository-layout.md` |
| 22 | Phase 4a payin + vendor gateway | Payin and vendor gateway implemented | — | `reference/services.md` |
| 23 | Phase 4b payout orchestration | Payout orchestration implemented | — | `reference/services.md` |
| 24 | Extraction playbook | Service extraction complete | — | `reference/decisions/0001-service-centric-repository-layout.md` |
| 25 | Phase 5 business shell | Business shell implemented | — | `reference/services.md` |
| 26–34 | Phase 6a–6i (foundations → verification) | Service-centric layout and nine-service topology complete | — | `reference/decisions/0001-service-centric-repository-layout.md` |
| 36–41 | Phase 7a–7f (tracing → MVP acceptance) | MVP feature set complete | — | `reference/architecture.md` |
| 43 · A1 | Observability | Observability stack implemented | — | `operations/slo.md` |
| 44 · A2 | CI pipeline | CI pipeline implemented | — | `.github/workflows/ci.yml` |
| 45 · A3 | External resilience | External resilience implemented | — | `operations/runbooks/` |
| 46 · A4 | Compliance | Compliance controls implemented | — | `operations/runbooks/compliance-a4.md` |
| 47 · A5 | Admin console | Admin BFF implemented | — | `reference/services.md` |
| 48 · A10 | Product assurance | Assurance service implemented | — | `reference/services.md` |
| 49 · A6 | Internal security | mTLS, allowlists, RLS, and security controls implemented | 2026-08-04 | `security/threat-model.md` (TM-01–TM-14) |
| 50 · A7 | Backup, PITR, and disaster recovery | DR workflow and restore drill implemented | 2026-08-04 | `operations/runbooks/backup-restore.md`, `operations/runbooks/dr-restore-drill.md` |
| 51 · A8 | Data lifecycle and privacy | Privacy controls, field encryption, closure workflow complete | 2026-08-04 | `reference/services.md` |
| 52 · A9 | API contracts and schema evolution | Contract validation and evolution workflow complete | 2026-08-04 | `reference/api-contracts.md` |
| 53 · B0 | Load and capacity gate | Load test and capacity model established | 2026-08-04 | `performance/capacity-model.md` |
| 54 | VendorService boundary | Vendor callback boundary finalized | 2026-08-04 | `reference/decisions/0002-vendorservice-callback-boundary.md` |
| 57 · C1 | Merchant/B2B API | Merchant API, tenant isolation, webhook relay complete | 2026-08-04 | `reference/api-contracts.md`, `security/threat-model.md` (TM-15–TM-19) |
| 58 · C2 | Data platform and revenue analytics | Analytics CDC pipeline and BI stack implemented | 2026-08-04 | `reference/decisions/0003-analytics-read-only-projection.md` |
| 59 · C3 | Multi-channel notifications | Email, push, and in-app notification platform complete | 2026-08-04 | `reference/notifications.md`, `security/threat-model.md` (TM-20–TM-22) |
| 60 · C4 | End-to-end multi-currency | FX, multi-currency accounts, and conversion complete | 2026-08-05 | `reference/services.md` |
| 61 · C5 | Advanced financial products and period close | Savings products, accrual, and period-close complete | 2026-08-05 | `reference/services.md` |
| 62 · C6 | Zero-downtime migration engine | Migration platform and account_balances_v2 complete | 2026-08-09 | `reference/decisions/0004-zero-downtime-migration-platform.md`, `security/threat-model.md` (TM-23–TM-26) |
| 64 · K0 | Deployment inventory baseline | Kubernetes readiness baseline and deployment inventory complete | 2026-08-09 | `deployment/README.md` |
| 65 · A11 | Database audit and hardening | Three-round schema-safety, security, and business-completeness audit complete; bounded entry posting (SECURITY DEFINER), KYC maker/checker, DELETE-grant revocations, dispute/disbursement guards, and HTTP transport split all implemented and verified by `make integration` (EXIT:0) | 2026-08-10 | `services/ledger/migrations/`, `services/auth/migrations/` |

| 56 · F0 | Frontend platform foundation | Next.js 15 / pnpm workspace with Wallet and Operator app shells, exact money/domain/ui/api-client primitives, fixture-backed reference screens, `make web-check` gate, frontend CI job, bundle scan, security headers (including Permissions-Policy), and all T10 acceptance items verified (a11y, keyboard, responsive, dependency audit, docs) — `make verify-static` EXIT:0 | 2026-08-10 | `web/README.md`, `docs/roadmap/55-frontend-long-term-roadmap.md` |

Plans 35 (optional local Kubernetes) and 55 (frontend long-term roadmap) are strategy/reference documents, not task lists.
Plan 63 (Kubernetes cloud deployment roadmap) is the active cloud deployment strategy.
