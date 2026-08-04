# World-class engineering improvement tracker

This tracker is the repository-local execution record for
[`seev-world-class-engineering-improvement-plan-en.md`](../improvement/seev-world-class-engineering-improvement-plan-en.md).
It is intentionally more explicit than a project-management checklist: each
item names the implementation surface and the evidence required to close it.

## Status vocabulary

| Status | Meaning |
|---|---|
| `implemented` | The repository contains the implementation and a repeatable verification path. |
| `partially_implemented` | A real capability exists, but one or more controls, consumers, or production proofs remain. |
| `evidence_required` | The repository is ready for the check, but the result depends on a deployed environment, credential, or independent party. |
| `decision_required` | A product, regulatory, or ownership decision is required before implementation can be safely selected. |
| `not_started` | No implementation evidence exists yet. |

The distinction is deliberate. A passing unit test does not prove a live
vendor sandbox, a cloud deployment, a GitHub milestone, or an independent
security review.

## Phase 0 — scope, inventory, and risk

| Action | Status | Implementation/evidence |
|---|---|---|
| Freeze non-critical feature development during P0–P2 | `implemented` | The freeze policy and change gate are documented in `docs/engineering/change-freeze.md`. |
| Capability inventory with code/schema/unit/integration/E2E/chaos/runtime/production columns | `implemented` | `docs/engineering/capability-inventory.md`. |
| Risk register with owner, mitigation, trigger, and evidence | `implemented` | `docs/engineering/risk-register.md`. |
| Production-readiness checklist | `implemented` | `docs/operations/production-readiness-checklist.md`. |
| GitHub labels and milestones | `implemented` | Remote labels and milestones are reconciled from `.github/roadmap/metadata.yml` in `herdifirdausss/seev`; verified on 2026-08-04 with `gh label list` and `gh api repos/herdifirdausss/seev/milestones`. |
| Capability status distinguishes implemented, tested, production-ready, and accepted | `implemented` | Status vocabulary above and the inventory columns enforce the distinction. |

## Phase 1 — P0 correctness and trust boundaries

| Action | Status | Implementation/evidence |
|---|---|---|
| One money-movement command pipeline for API, scheduler, recurring jobs, admin corrections, retries, recovery, bulk, reversals, and refunds | `implemented` | `services/ledger/internal/ledger/command`; transport and durable scheduling use the execution boundary. |
| Execution context carries actor, tenant, source, correlation ID, claims, effective time, and origin | `implemented` | `services/ledger/internal/ledger/command/context.go`. |
| Policy/authorization outside transports; policy is re-evaluated at execution time | `implemented` | `services/ledger/internal/ledger/command/executor.go`; transport only builds context. |
| Policy decisions are audited and denial metrics include reason/source | `implemented` | `money_movement_policy_decisions` migration, repository sink, and command metrics. |
| Low-level posting is restricted and bypasses are tested | `implemented` | Execution-boundary adapter plus `services/ledger/internal/ledger/architecture_boundary_test.go`. |
| Schedule expiry, changed limits, disabled account/tenant, crash/retry, duplicate/conflicting idempotency, privilege, and admin-bypass tests | `implemented` | Command and schedule tests plus schema-contract coverage listed in the acceptance documents. |
| Fee rules are immutable, versioned, maker-checker controlled, bounded, and deterministic | `implemented` | Version/status columns, constraints, overlap trigger, approval routes, and tests in `feepolicy`. |
| Disbursement processing requires approval at the database boundary | `implemented` | Migration `000045_disbursement_processing_requires_approval`; raw-SQL contract test. |
| Dispute lifecycle, deadlines, evidence, bounded amount, retries, notifications, and audit trail | `implemented` | Lifecycle, deadline expiry, evidence, amount bounds, retry-safe transitions, and audit history are covered by the Ledger service/repository and schema contract. `ledger.dispute.lifecycle.v1` is emitted transactionally, consumed and deduplicated by Gateway, and proven through real Postgres/RabbitMQ delivery in `services/gateway/internal/notification/inbox/notify_integration_test.go`; retention/legal-hold evidence is in `services/gateway/internal/notification/inbox/retention_integration_test.go`. |
| Canonical money type and currency-safe arithmetic | `implemented` | `internal/platform/money/currency.Money`, exact rate conversion, boundary tests, and command validation. |
| Database/auth security findings closed | `implemented` | Database/RLS grants, immutable audit and financial controls, tenant-scoped access, privileged closure maintenance, and fail-closed execution subject/KYC/tenant gates are implemented with repeatable evidence in [`docs/acceptance/security.md`](../acceptance/security.md). The independent review remains a separate `evidence_required` gate below. |

## Phase 2 — golden route and failure testing

| Action | Status | Implementation/evidence |
|---|---|---|
| Registration → auth → KYC → pay-in → vendor → callback → P2P → payout → recovery → recon → statement → dispute/reversal route | `implemented` | `docs/engineering/golden-route.md`; executable test and chaos commands are linked there. |
| Explicit aggregate state machines | `implemented` | Golden-route state diagrams and domain model references. |
| Failure matrix with detection, retry, age threshold, escalation, operator action, and replay path | `implemented` | `docs/engineering/golden-route-failure-matrix.md`. |
| Critical failure tests and retained artifacts | `implemented` | [`docs/acceptance/critical-failures.md`](../acceptance/critical-failures.md); integration JSONL/status/manifest and scheduled business/privacy/23-scenario chaos evidence are retained in run-specific CI artifacts for 30 days. The deployed production-shaped run remains the separate `evidence_required` gate below. |

## Phase 3 — acceptance and runtime truth

| Action | Status | Implementation/evidence |
|---|---|---|
| Acceptance documents for scheduled transactions, disputes, payout recovery, and reconciliation | `implemented` | `docs/acceptance/*.md`. |
| Reachability, auth, tenant safety, integration, E2E, metrics/logs/traces, alerts, retry/recovery, migration/rollback, and owner checks | `implemented` | Shared acceptance template and capability-specific checklists. |
| Runtime acceptance requires evidence, not only code | `implemented` | `docs/engineering/production-readiness-scorecard.md`. |
| Production-shaped critical-failure run | `evidence_required` | The repository and CI evidence contract are ready; attach a deployed run, artifact URL, commit/image digests, invariant reports, alert/runbook results, and owner sign-off. |

## Phase 4 — production platform

| Action | Status | Implementation/evidence |
|---|---|---|
| Terraform/OpenTofu private network, managed Postgres, Redis, broker, secrets/KMS, workload identity, containers, object storage, monitoring, DNS, and certificates | `partially_implemented` | Existing AWS/GCP foundations plus the production platform module and environment contract are documented in `deploy/terraform/README.md`; applying them needs cloud credentials and provider-specific sizing decisions. |
| Environment separation and fail-fast configuration | `implemented` | `internal/platform/config`, deployment validation scripts, and `docs/deployment/environment-contract.md`. |
| Migrations as a separate job with startup guard | `implemented` | Helm migration job and deployment contract; acceptance check included. |
| Multi-replica deployment and readiness | `implemented` | Kubernetes deployment values/probes and platform checklist. |
| Real vendor sandbox | `evidence_required` | `docs/deployment/vendor-sandbox.md` and config validator are ready; credentials and vendor access are external. |

## Phase 5 — SLO, supply chain, security, and recovery

| Action | Status | Implementation/evidence |
|---|---|---|
| SLOs, metrics, alerts, and actionable runbooks | `implemented` | `docs/operations/slo.md`, `deploy/observability/prometheus/rules/improvement.yml`, and runbooks. |
| Secret identity and workload identity | `implemented` | ExternalSecret examples, cloud IAM modules, and deployment contract. |
| Pinned dependencies/actions, vulnerability scan, container scan, SBOM, signing, provenance, minimal non-root images, read-only runtime | `implemented` | `scripts/ci/supply-chain-check.sh`, CI Trivy/SBOM artifacts, digest-pinned Dockerfiles/Compose/Helm infrastructure, the keyless Cosign protected-publish workflow, and `docs/acceptance/supply-chain.md`. |
| Protected-registry signing, live attestation verification, and runtime admission proof | `evidence_required` | The release workflow invokes Cosign signing and verification and checks OCI attestation descriptors; `scripts/ci/verify-supply-chain-evidence.sh` validates the retained bundle; attach a real protected-registry tag run and deployed admission/runtime verification. |
| Independent security review | `evidence_required` | Scope, review methods, finding schema, and exit criteria are recorded in `docs/security/independent-review-scope.md`; `docs/acceptance/independent-security-review.md` is the evidence packet. Attach the independent dated report, reproduction artifacts, remediation/risk decisions, and retest statement before changing this status. |
| Disaster recovery and restore drill | `partially_implemented` | Existing DR workflow/runbook plus an evidence template; a recent cloud restore result is required. |

## Phase 6 — go/no-go

| Action | Status | Implementation/evidence |
|---|---|---|
| Correctness, reliability, security, operations, and capacity go/no-go checklist | `implemented` | `docs/engineering/production-readiness-scorecard.md`. |
| No unresolved P0/P1 correctness or security risk | `evidence_required` | [`docs/acceptance/p0-p1-risk-gate.md`](../acceptance/p0-p1-risk-gate.md) enumerates R-001–R-015 and the release approval fields; `scripts/ci/verify-p0-p1-risk-gate.sh` validates the inventory and gate contract. Attach current commit/environment evidence and signed `GO` approval, or explicit time-bounded risk acceptance for an allowed P1, before changing this status. |

## Phase 7 — simplification and portfolio proof

| Action | Status | Implementation/evidence |
|---|---|---|
| Service justification for each service | `implemented` | `docs/engineering/service-justification.md`. |
| Remove unjustified services | `decision_required` | Requires product and ownership decisions; no safe deletion is inferred. |
| Five-minute portfolio proof | `implemented` | `docs/portfolio/engineering-proof.md`, with this tracker as the audit index. |

## Backlog closure index

The plan's P0–P3 backlog is represented below. A checkbox is closed only when
the linked evidence is present.

### P0

- [x] Unified command execution boundary and context.
- [x] Policy decision audit and source/reason metrics.
- [x] Execution-time subject/KYC/tenant gate.
- [x] Fee-rule maker-checker and immutable version controls.
- [x] Disbursement approval invariant at the database boundary.
- [x] Dispute amount/deadline/audit controls.
- [x] Canonical `Money` and exact FX conversion.
- [x] Architecture and bypass tests.
- [x] P0 acceptance evidence documents.

### P1

- [x] Golden route, state machines, and failure matrix.
- [x] Stuck-state scanner contract and alert/runbook path.
- [x] Reconciliation and payout-recovery acceptance paths.
- [x] Operational SLO, alert, and incident-severity documentation.
- [x] DR evidence template and restore-run command.

### P2

- [x] Environment contract and fail-fast validation.
- [x] Separate migration job contract.
- [x] Dependency/action/image pinning, vulnerability/container scans, SBOM/provenance, signing workflow, and hardened non-root/read-only runtimes; protected-registry evidence is a separate environment gate.
- [x] Private-platform IaC extension points.
- [x] Vendor sandbox configuration validator.
- [ ] Cloud apply, vendor sandbox execution, and real attestation evidence (external credentials/environment).

### P3

- [x] Service and capability justification register.
- [x] Future frontend and operator-surface acceptance criteria.
- [x] Portfolio proof and durable evidence index.
- [ ] Product-specific rollout decisions and capacity-funded expansion (product decision required; see `docs/engineering/p3-decision-register.md`).

## Definition of done

For every item, design is linked, implementation is present, verification is
repeatable, operational ownership is named, and evidence is retained. The
only intentionally open boxes are actions that require external access or a
decision the repository cannot safely invent.
