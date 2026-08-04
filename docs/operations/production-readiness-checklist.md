# Production-readiness checklist

Use this checklist for a specific commit and environment. Replace every
`<evidence>` value with a retained artifact link or command output. “Code
exists” is not an acceptable substitute for runtime evidence.

## Correctness

- [ ] All money movement enters the shared command pipeline — `<evidence>`
- [ ] Idempotency duplicate and conflicting-payload tests pass — `<evidence>`
- [ ] Ledger balance invariant and append-only correction tests pass — `<evidence>`
- [ ] KYC expiry, account status, tenant status, limit-change, and privilege tests pass — `<evidence>`
- [ ] Fee rules are approved by a distinct checker and historical versions resolve deterministically — `<evidence>`
- [ ] Disbursement processing requires approval in the database — `<evidence>`
- [ ] Dispute amount/deadline/terminal-state invariants pass — `<evidence>`
- [ ] Currency and FX rounding tests pass for every enabled currency — `<evidence>`

## Reliability

- [ ] Crash/retry and duplicate callback tests pass — `<evidence>`
- [ ] Payout timeout/unknown/recovery path is verified against the configured vendor — `<evidence>`
- [ ] Outbox replay/dead-letter path is exercised — `<evidence>`
- [ ] Reconciliation mismatch path creates an attributable correction request — `<evidence>`
- [ ] Stuck-state scanners and alerts are enabled — `<evidence>`
- [ ] DR restore drill completed within the documented RTO/RPO — `<evidence>`

## Security

- [ ] Public/internal route separation and authorization are verified — `<evidence>`
- [ ] Tenant isolation tests pass — `<evidence>`
- [ ] Secrets use workload identity/secret manager and never appear in logs — `<evidence>`
- [x] Repository dependency/action/image pinning, Go/Trivy scans, SBOM/provenance requests, and non-root/read-only runtime controls pass — [`docs/acceptance/supply-chain.md`](../acceptance/supply-chain.md)
- [ ] Protected-registry signature, live attestation verification, and admission/runtime proof are attached — `<evidence>`
- [x] Independent security review scope, evidence fields, and exit criteria are prepared — [`docs/acceptance/independent-security-review.md`](../acceptance/independent-security-review.md)
- [ ] Independent assessor report, reproduction artifacts, remediation/risk decisions, and retest statement are attached — `<evidence>`

## Operations

- [ ] SLO dashboards, alert rules, and runbooks are linked — `<evidence>`
- [ ] On-call ownership, escalation, and incident severity are recorded — `<evidence>`
- [ ] Migration runs as a separate job and rollback is rehearsed — `<evidence>`
- [ ] Logs, metrics, and traces include correlation IDs and no sensitive payloads — `<evidence>`
- [ ] Vendor sandbox/production configuration is validated — `<evidence>`

## Capacity and release

- [ ] Load test covers peak rate, concurrency, queue backlog, and dependency degradation — `<evidence>`
- [ ] Capacity assumptions and autoscaling limits are documented — `<evidence>`
- [ ] No unresolved P0/P1 correctness or security risk remains; complete the [`P0/P1 risk gate`](../acceptance/p0-p1-risk-gate.md) with current evidence and approval — `<evidence>`
- [ ] Product owner, engineering owner, operations owner, and security owner approve — `<evidence>`

## Decision

| Area | Result | Evidence | Owner |
|---|---|---|---|
| Correctness | `GO / NO-GO` |  |  |
| Reliability | `GO / NO-GO` |  |  |
| Security | `GO / NO-GO` |  |  |
| Operations | `GO / NO-GO` |  |  |
| Capacity | `GO / NO-GO` |  |  |

Any `NO-GO`, missing owner, or missing evidence blocks production acceptance.
