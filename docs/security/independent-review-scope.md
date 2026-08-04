# Independent security review scope

This document is the acceptance contract for the independent security review
required by section 12.6 of the
[world-class engineering improvement plan](../improvement/seev-world-class-engineering-improvement-plan-en.md).
The execution packet is maintained in
[`docs/acceptance/independent-security-review.md`](../acceptance/independent-security-review.md).

## Independence and evidence boundary

The assessor must be an external person or organization that did not implement
the controls under review and has no unresolved conflict of interest. The
review record must identify the assessor, organization, report date, reviewed
commit and environment, independence statement, and approved exclusions.

Repository tests, static checks, automated scanners, and an internal
self-review are useful inputs but cannot satisfy the independent-review gate.
The final evidence must be a dated report from the independent assessor,
retained reproduction artifacts, remediation or risk-acceptance decisions, and
a retest statement.

## Required scope

Every scope area below must be assessed. A `not applicable` result requires a
written rationale and owner approval in the review packet.

| ID | Required coverage | Minimum evidence to retain |
|---|---|---|
| `EDGE` | Public versus internal listener exposure; authentication and authorization middleware; webhook verification; mTLS identity; secret lifecycle; SSRF, injection, mass assignment, replay, rate limiting, and denial-of-service boundaries. | Route/authentication matrix, authenticated and unauthenticated requests, negative test results, and redacted logs or traces. |
| `TENANT` | Tenant isolation; KYC and subject-state propagation; maker-checker paths; administrator operations; privilege escalation; and service-to-service authorization. | Cross-tenant attempts, state-transition checks, role matrix, and database or API results showing the enforced boundary. |
| `MONEY` | Idempotency digest and conflict behavior; every money-moving entry point; ledger balance and append-only rules; currency/FX rounding; fee-rule approval and overlap; disbursement approval; dispute amount/deadline/terminal-state rules; and financial-abuse cases. | A complete entry-point inventory, replay/conflict cases, invariant results, approval evidence, and attributable financial test outputs. |
| `DATA` | SQL permissions; RLS; migration roles; raw SQL bypass attempts; credential and secret handling; audit-log integrity; and audit-column tampering. | Effective grants/RLS results, migration-role review, bypass attempts, secret-redaction evidence, and append-only/tamper-test results. |
| `RECOVERY` | Payout timeout and unknown-state recovery; retries; callbacks; outbox replay and dead-letter handling; reconciliation; notification delivery/consumer integration; and auditability of recovery actions. | Failure-injection and replay evidence, callback cases, queue/consumer results, reconciliation records, notification delivery evidence, and audit references. |
| `SUPPLY` | Dependency, image, and action provenance; vulnerability and container scans; SBOM and provenance attestations; signing and verification; runtime identity; non-root/read-only controls; admission policy; and audit-log integrity across release/runtime boundaries. | Reviewed immutable digests, scan/SBOM/provenance records, signature and attestation verification, admission decisions, runtime identity/configuration, and retained logs. |

## Required review method

The assessor must combine the following methods and record what was actually
executed:

- architecture, threat-model, API-contract, migration, and source review;
- static analysis and review of the repository security-contract tests;
- authenticated and unauthenticated dynamic tests against an authorized
  deployed environment;
- database privilege, RLS, migration-role, and raw-SQL bypass tests;
- retry, replay, callback, timeout, outbox, notification, and reconciliation
  failure tests;
- release-artifact, registry, admission, runtime-identity, and audit-log
  verification where those controls are in scope.

Testing must be authorized, rate-limited, and scrubbed of secrets and
personal data. The report must identify the exact commit/image digests and
environment used; results from a different build or an undocumented local
environment are not interchangeable.

## Required finding record

Each finding must include:

- a stable ID and severity (`Critical`, `High`, `Medium`, `Low`, or
  `Informational`);
- exploitability, affected asset/component, preconditions, and business
  impact;
- exact reproduction steps, inputs, expected result, observed result, and
  retained artifact references;
- remediation, owner, due date, and verification evidence;
- if not remediated, an explicit risk-acceptance decision, approver,
  compensating controls, and expiry date; and
- a retest result linking the fixed commit/image or explaining the accepted
  residual risk.

## Exit criteria

The review is complete only when all of the following are true:

- an independent assessor has signed and dated the report and declared any
  conflicts of interest;
- every `EDGE`, `TENANT`, `MONEY`, `DATA`, `RECOVERY`, and `SUPPLY` area has a
  result, evidence reference, and assessor conclusion;
- every finding is severity-ranked, reproducible, assigned, and either
  remediated with retest evidence or covered by a named, time-bounded,
  owner-approved risk acceptance with compensating controls;
- no unresolved P0/P1 security risk is released without an explicit risk
  acceptance recorded in the risk register and production scorecard;
- the report, reproduction artifacts, remediation/risk decisions, and retest
  statement are retained with access controls and linked from the acceptance
  packet; and
- the security owner records the final go/no-go decision for the reviewed
  commit and environment.

The repository-side preparation is complete when the scope and packet are
available. The tracker and production checklist must remain
`evidence_required` until the external report and retest evidence satisfy this
contract.
