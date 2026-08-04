# Production-readiness scorecard

This is the go/no-go record for a commit and environment. A score is not a
claim that the environment exists; the evidence column must point to a real
artifact.

| Gate | Required result | Current repository evidence | Environment evidence | Decision |
|---|---|---|---|---|
| Correctness | all P0 invariants and duplicate/retry tests pass | command, fee, dispute, disbursement tests | `<attach>` | `GO / NO-GO` |
| Reliability | failure matrix exercised and recovery verified | golden route and chaos scenarios | `<attach>` | `GO / NO-GO` |
| Security | no open P0/P1 security risk; independent review complete | threat model, boundary tests, CI, [`P0/P1 risk gate`](../acceptance/p0-p1-risk-gate.md), and [`independent-review acceptance packet`](../acceptance/independent-security-review.md) | `<attach>` | `GO / NO-GO` |
| Operations | SLOs, alerts, runbooks, owners, migration/rollback | operations docs and workflows | `<attach>` | `GO / NO-GO` |
| Capacity | peak/dependency degradation test meets budget | load workflow and benchmark docs | `<attach>` | `GO / NO-GO` |
| External dependencies | cloud, vendor, registry, and identity evidence present | validators and IaC | `<attach>` | `GO / NO-GO` |

## Release blockers

- any open P0 correctness/security risk;
- any missing owner for a money-moving path;
- any missing backup/restore or migration rollback evidence;
- any unsigned/unprovenanced release artifact;
- any vendor unknown-state path without a reconciler;
- any external evidence row still marked `evidence_required`.

The release owner records the final decision, timestamp, commit, environment,
approvers, and links to the evidence bundle.
