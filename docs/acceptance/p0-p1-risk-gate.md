# P0/P1 correctness and security release gate

Use this packet for the release approval of one immutable commit and
environment. It is a gate template, not evidence that the risks are closed.
The source register is
[`docs/engineering/risk-register.md`](../engineering/risk-register.md), and
the final decision belongs in the
[production-readiness scorecard](../engineering/production-readiness-scorecard.md).

## Gate rule

For the reviewed commit and environment:

- every P0 correctness or security risk must have its mitigation exercised and
  a retained environment-appropriate artifact;
- every P1 correctness or security risk must be closed, or have a named owner,
  explicit compensating control, expiry date, and approved residual-risk
  decision;
- evidence must identify the commit/image digest, environment, execution time,
  result, and artifact retention location;
- the release owner and security owner must record `GO` only after the current
  evidence is reviewed; and
- any missing evidence, open P0, expired risk acceptance, or unresolved P1
  without approval is `NO-GO`.

This packet does not turn repository tests into production evidence. It also
does not grant a risk acceptance; the designated owner must make that decision
for the specific release.

## Blocking risk inventory

Complete the decision and evidence columns during release approval. The risk
IDs and control descriptions must remain aligned with the
[risk register](../engineering/risk-register.md).

| ID | Severity | Blocking area | Required current result/evidence | Decision (`CLOSED / ACCEPTED / NO-GO`) | Evidence/approval |
|---|---|---|---|---|---|
| `R-001` | P0 | Money-movement boundary | No direct posting bypass; architecture and negative tests pass for the reviewed commit. |  |  |
| `R-002` | P0 | KYC/subject state | Execution-time KYC/subject gate rejects expired or missing state with audit evidence. |  |  |
| `R-003` | P0 | Tenant/account state | Disabled tenant/account cannot produce a queued execution; boundary evidence is retained. |  |  |
| `R-004` | P0 | Fee policy | Maker-checker and non-overlap constraints pass against the release schema. |  |  |
| `R-005` | P0 | Disbursement approval | Database invariant rejects processing without approval, including race/raw-SQL attempts. |  |  |
| `R-006` | P0 | Dispute amount | Amount, currency, deadline, and terminal-state database controls pass. |  |  |
| `R-007` | P0 | Idempotency | Duplicate requests have one effect and conflicting digests return a conflict. |  |  |
| `R-008` | P0 | Payout unknown state | Timeout/recovery/reconciliation evidence proves no blind duplicate payout. |  |  |
| `R-009` | P1 | Outbox delivery | Age/count SLO, alert, replay, and dead-letter evidence is current. |  |  |
| `R-010` | P0 | Reconciliation integrity | Mismatch handling creates an attributable correction and does not rewrite history. |  |  |
| `R-011` | P1 | Migration safety | Separate migration job, readiness guard, and rollback evidence cover the release. |  |  |
| `R-012` | P0 | Secret handling | Workload identity/secret-manager and log-redaction checks pass for the environment. |  |  |
| `R-013` | P1 | Supply-chain provenance | Immutable artifact scan, SBOM, provenance, signature, and verification evidence is retained. |  |  |
| `R-014` | P1 | Independent review | Independent dated report, findings, remediation/risk decisions, and retest are attached. |  |  |
| `R-015` | P1 | Capacity | Peak/dependency-degradation evidence meets the documented SLO and capacity limits. |  |  |

## Release approval record

| Field | Recorded value |
|---|---|
| Release version/tag |  |
| Reviewed commit |  |
| Image digests |  |
| Environment and region |  |
| Evidence bundle URI and retention |  |
| Approval date/time (UTC) |  |
| Release owner |  |
| Engineering owner |  |
| Operations owner |  |
| Security owner |  |
| Final decision (`GO / NO-GO`) |  |
| Exception/incident reference |  |

## Required evidence bundle

The bundle linked from this packet must include, as applicable:

- current automated test and invariant results for correctness risks;
- deployment, database, queue, callback, reconciliation, and runtime results
  from the exact environment;
- scan/SBOM/provenance/signature records for the exact image digests;
- independent-review and vendor/cloud evidence for external rows;
- every risk acceptance with approver, compensating control, expiry, and
  residual-risk statement; and
- the signed scorecard decision with timestamps and approvers.

Artifacts must be immutable or access-controlled, retain enough metadata to
reproduce the result, and contain no secrets or unnecessary personal data.

## Repository preflight

These checks validate the repository controls and packet shape before release
approval. They do not close the environment evidence gate:

```sh
make risk-gate-check
make docs-check
make improvement-check
```

Until the release approval record is completed with current evidence, the
improvement-plan tracker must remain `evidence_required`.
