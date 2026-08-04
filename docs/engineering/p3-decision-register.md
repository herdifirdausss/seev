# P3 decision and implementation register

The P3 backlog in the world-class engineering plan contains product,
regulatory, commercial, and capacity decisions. This register turns every
bullet into an owned decision gate so that a plausible-looking schema or
integration is not mistaken for an approved financial capability.

`Decision required` means the repository must not invent the policy. Once the
decision is approved, the owner adds the design, migration, acceptance
evidence, and rollout/rollback plan in the same change.

| Plan bullet | Current repository position | Decision or evidence required before implementation | Acceptance evidence |
|---|---|---|---|
| Tenant-aware pricing | Fee rules and quotes are versioned, but merchant/tenant pricing semantics are not yet a settled contract. | Product + Finance decide precedence, effective time, currency, tax, overrides, and tenant isolation. | Tenant-isolated pricing tests, immutable rule history, quote-to-post reconciliation, and approval sign-off. |
| Merchant invoices and statements | Transaction and reconciliation data can be queried; there is no persisted document lifecycle. | Finance/Compliance decide document numbering, period close, corrections, retention, and delivery obligations. | Golden statement/invoice fixture, immutable document audit, reconciliation proof, and export/restore test. |
| Settlement cycles | Payout and reconciliation flows exist; a merchant settlement schedule is not defined. | Finance decides T+N, cutoffs, holidays, reserve interaction, failed settlement handling, and ownership. | Two-cycle simulation, idempotent settlement run, replay proof, and reconciliation report. |
| Rolling reserves | No reserve policy is applied implicitly. | Risk/Finance decide eligibility, calculation basis, release schedule, caps, and dispute treatment. | Balance invariant tests, reserve ledger entries, maker-checker policy approval, and release simulation. |
| Dormant-account handling | Account closure/offboarding controls exist; dormancy/escheatment policy is not inferred. | Legal/Compliance decide inactivity period, notices, unclaimed-property jurisdiction, and reactivation rules. | Time-based state-machine tests, notice evidence, restricted-access review, and rollback procedure. |
| Real FX-rate source | Exact currency arithmetic exists; production FX sourcing and quote validity are not selected. | Treasury/Risk select provider, source hierarchy, timestamp/TTL, spread, fallback, and correction policy. | Signed rate provenance, stale-rate rejection, exact conversion fixtures, and provider outage drill. |
| Consolidate duplicated routing logic | Payin and payout routing implementations are intentionally separate service-owned paths with known duplication. | Architecture owners decide shared-library versus service-owned abstraction and migration order. | No cross-service ownership violation, equivalent routing behavior, failure/replay tests, and measured maintenance reduction. |
| Capacity-driven optimization | SLO thresholds and measurement contracts exist; no production capacity claim is made. | Engineering defines workload shape, budget, dependency limits, and acceptable cost/headroom targets from a representative run. | Reproducible load/soak artifact, bottleneck diagnosis, scale threshold, and before/after comparison. |
| Customer-facing frontend | Frontend platform acceptance criteria are tracked separately; no product workflow is assumed by this plan. | Product chooses first user, journey, read/write scope, accessibility, and release boundary. | Browser E2E against stable contracts, authorization/tenant tests, money-display tests, and rollback. |
| Additional vendors | VendorService supports sandbox/mock boundaries; production vendors require commercial and compliance approval. | Risk/Compliance/Finance approve vendor, regions, limits, callback contract, SLA, and exit plan. | Vendor sandbox run, signed callback/replay tests, unknown-state recovery, reconciliation, and operational owner. |

## Closure rule

No P3 item is marked implemented by documentation alone. A decision record
must name an owner and approval date; the implementation PR must link the
decision and add its acceptance artifact to the production-readiness scorecard.
Until then, the item remains `decision_required` in
`docs/engineering/improvement-plan-tracker.md`.
