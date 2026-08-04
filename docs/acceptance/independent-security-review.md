# Independent security review acceptance packet

This is the repository-side packet for the external security review. It is a
template and evidence index, not a security report. The canonical scope and
exit contract are in
[`docs/security/independent-review-scope.md`](../security/independent-review-scope.md).

## Repository preparation

- [x] Scope areas and required review methods are defined.
- [x] Finding, remediation, risk-acceptance, and retest fields are defined.
- [x] Exit criteria and the production go/no-go boundary are defined.
- [x] Repository-side security controls are indexed in
  [`docs/acceptance/security.md`](security.md).
- [ ] An independent assessor has supplied a dated report for the reviewed
  commit and environment.

## Review record

Complete these fields from the assessor's signed report. Do not put secrets or
personal data in this repository.

| Field | Recorded value |
|---|---|
| Assessor and organization |  |
| Independence/conflict declaration |  |
| Report date and review period |  |
| Reviewed commit and image digests |  |
| Authorized environment |  |
| Approved exclusions and rationale |  |
| Report link and retention location |  |
| Security-owner decision | `GO / NO-GO` |

## Scope coverage

The assessor must complete every row or record an approved `N/A` rationale.
Use the IDs from the [canonical scope](../security/independent-review-scope.md)
when naming findings and artifacts.

| Scope ID | Result (`PASS / FAIL / N/A`) | Evidence links | Finding IDs | Retest reference |
|---|---|---|---|---|
| `EDGE` |  |  |  |  |
| `TENANT` |  |  |  |  |
| `MONEY` |  |  |  |  |
| `DATA` |  |  |  |  |
| `RECOVERY` |  |  |  |  |
| `SUPPLY` |  |  |  |  |

## Finding register

Every finding in the report must have the following information. Link
reproduction artifacts rather than embedding secrets, credentials, or
personal data.

| ID | Scope | Severity | Exploitability and impact | Reproduction/artifact | Remediation and owner | Due date | Risk acceptance/expiry | Retest |
|---|---|---|---|---|---|---|---|---|
|  |  |  |  |  |  |  |  |  |

## Required retained evidence

The evidence bundle must contain or link to:

- the assessor's dated report and independence statement;
- the scope coverage results, including approved exclusions;
- severity-ranked findings with exact reproduction evidence;
- remediation records or explicit risk-acceptance decisions with expiry and
  compensating controls; and
- the dated retest statement for each remediated or accepted finding.

The bundle must identify the reviewed commit, image digests, environment, and
retention/access controls. The production readiness scorecard and risk
register must link to the same immutable evidence location.

## Repository preflight inputs

These commands provide repository-side inputs for the assessor; passing them
does not satisfy the independent-review gate:

```sh
make ci-lint
make docs-check
make improvement-check
make supply-chain-check
go test ./services/auth ./services/ledger/internal/ledger/command
go test ./services/ledger -run TestProductionPostingDoesNotBypassCommandBoundary
```

## Exit checklist

- [ ] Independent assessor and conflict declaration are recorded.
- [ ] All six scope areas have an assessor result and evidence reference.
- [ ] Every finding has severity, impact, reproduction, owner, due date, and
  remediation or risk-acceptance decision.
- [ ] No P0/P1 security risk remains without a named, time-bounded approval and
  compensating controls.
- [ ] Remediated findings have passed retest; accepted findings have a current
  expiry and residual-risk statement.
- [ ] The dated report, evidence bundle, and retest statement are retained and
  linked from the production scorecard.
- [ ] Security owner records the final go/no-go decision.

Until the unchecked external items are complete, the improvement-plan tracker
must continue to show `evidence_required`.
