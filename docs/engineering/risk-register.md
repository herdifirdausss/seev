# Engineering risk register

| ID | Risk | Severity | Owner | Mitigation/control | Trigger | Evidence/response |
|---|---|---|---|---|---|---|
| R-001 | A money movement bypasses the shared command boundary | P0 | Ledger | Execution boundary, adapter, static architecture test | New direct low-level posting call | Block merge; run `go test ./services/ledger/internal/...` |
| R-002 | KYC expires after scheduling but before execution | P0 | Auth/Ledger | Execution-time subject snapshot and fail-closed gate | `kyc_verified_until <= effective_time` | Reject with audited policy decision; retry only after state changes |
| R-003 | Tenant/account is disabled while a command is queued | P0 | Auth/Ledger | Subject/tenant status gate at execution | status is not active | Business failure, no ledger post, operator audit |
| R-004 | A fee rule is approved by its maker or overlaps an active rule | P0 | Ledger/Ops | DB checks, overlap trigger, maker-checker API | invalid approval or conflicting range | Reject transaction; inspect fee-rule audit trail |
| R-005 | Disbursement enters processing without approval | P0 | Ledger | Database check and explicit transitions | raw SQL or race attempts transition | Transaction rollback; schema contract test |
| R-006 | Dispute exceeds the original transaction amount | P0 | Compliance/Ledger | Service validation plus database trigger | insert/update amount > original | Reject and alert; preserve original history |
| R-007 | Duplicate or conflicting idempotency request | P0 | Ledger | Digest comparison and immutable scope | same key with different payload | Return conflict; no second monetary effect |
| R-008 | Provider timeout leaves payout outcome unknown | P0 | Payout/Ledger | Pin provider, recovery state machine, reconciler | timeout after provider acceptance window | Do not retry blindly; use recovery runbook |
| R-009 | Outbox backlog hides external delivery failure | P1 | Platform/Ledger | Age/count metrics, alert thresholds, replay/dead-letter controls | oldest event breaches SLO | Follow outbox runbook and retain evidence |
| R-010 | Reconciliation mismatch is “fixed” by rewriting history | P0 | Ledger/Ops | Append-only adjustment maker-checker path | mismatch persists past SLA | Freeze settlement; create attributable correction |
| R-011 | Migration runs concurrently with application startup | P1 | Platform | Separate migration job and schema-version readiness gate | app starts on old schema | Keep pods unready; run migration job/rollback plan |
| R-012 | Secrets are exposed through config or logs | P0 | Security/Platform | Secret manager, workload identity, redaction checks | secret-like value in CI/log scan | Revoke/rotate; incident severity SEV-1 |
| R-013 | Dependency or image provenance is unverified | P1 | Security/Platform | Pinned actions, vulnerability scan, SBOM, signing/provenance | unsigned release artifact | Stop promotion; rebuild from trusted workflow |
| R-014 | Cloud/vendor/independent-review evidence is assumed | P1 | Release owner | Explicit evidence-required state, independent-review scope/packet, and go/no-go gate | missing external artifact | Attach the dated external report/retest or approved risk acceptance; do not mark production-ready |
| R-015 | Capacity limits are inferred from local load tests | P1 | Platform | SLO/capacity plan and retained environment run | p95/error budget breach | Scale/limit traffic; update capacity decision |

Each risk must be reviewed at release go/no-go. P0 risks are blocking until
the mitigation and an environment-appropriate verification artifact are both
attached. The release evidence fields and decision semantics are defined in
[`docs/acceptance/p0-p1-risk-gate.md`](../acceptance/p0-p1-risk-gate.md) and
checked by `make risk-gate-check`.
