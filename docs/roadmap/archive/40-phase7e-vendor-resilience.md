# 40 — Phase 7e: Multi-Vendor Resilience

Read [plan 36](36-phase7a-request-tracing.md) first. Prerequisite: [plan 39](39-phase7d-kyc-tiers.md).

## Context and safety rule

Routing previously selected one vendor and payout recovery retried that vendor forever. This phase adds per-vendor circuit breakers and failover only before vendor confirmation.

Failover is allowed only when the payout is `created` or `held` and its `payout_vendor_calls` contain no `accepted` or `uncertain` result. A timeout or unknown result after submit is `uncertain` and permanently pins the payout to that vendor. Only a synchronous, definitive rejection may move to another candidate. The breaker improves availability; `payout_vendor_calls` is the anti-double-payout authority.

## T1 — Per-vendor circuit breaker

Add an in-memory `HealthTracker` with closed, open, and half-open states. Defaults are five consecutive infrastructure failures and a 30-second cooldown, configurable through `BREAKER_FAILURE_THRESHOLD` and `BREAKER_COOLDOWN`. Exactly one half-open probe is allowed. Business rejections must be recorded as success from the breaker's perspective.

Expose `Allow`, `RecordSuccess`, `RecordFailure`, and deterministic health snapshots. Document that the tracker is per process; multiple replicas converge more slowly but do not rely on the breaker for monetary safety.

**Result:** race tests prove threshold, single-probe behavior, recovery, re-open, snapshot ordering, and business-error handling. Payin and payout receive optional trackers; nil preserves previous behavior.

## T2 — Candidate routing

Change routing repositories from one matching rule to all matching candidates, ordered by user specificity and priority. Skip excluded vendors, unregistered vendors, and vendors whose breakers are open. No matching rule remains `NO_ROUTE`/422; matching rules that are all unavailable return `VENDOR_UNAVAILABLE`/503.

**Result:** payin and payout unit tests cover priority, user overrides, open breakers, exclusion lists, and all-vendors-unavailable behavior. gRPC and gateway status mappings distinguish configuration failure from transient vendor unavailability.

## T3 — Safe payout failover

Record each vendor call as `accepted`, `rejected`, or `uncertain` in migration `payout/000005_vendor_call_outcome`. On submit:

- successful or pending vendor response → accepted;
- synchronous definitive business rejection → rejected and eligible for the next candidate;
- timeout or transport failure → uncertain, breaker failure, and pin to the same vendor for resume.

Implement `mayFailover` from persisted call outcomes and cap attempts by the candidate count. Resume continues to use the stored vendor and never consults the breaker to move an uncertain payout.

**Result:** unit and PostgreSQL race tests prove that a rejection can fail over, an uncertain call cannot, and concurrent submit/resume activity produces one settle. Mockvendor idempotency caches are isolated by vendor name.

## T4 — Second mock vendor

Parameterize mockvendor constructors by name and secret. Register `mockvendor2` behind `MOCKVENDOR2_ENABLED` and `MOCKVENDOR2_SECRET`. Add a vendor-level force-fail admin endpoint for the payout mock and seed fallback routing through the admin API, not a migration.

**Result:** a named-vendor signature regression was fixed so events use the adapter's own name. Two-vendor signature isolation and force-fail authorization tests passed.

## T5 — Vendor health admin surface

Add admin-gated health endpoints under the existing module namespaces:

```text
GET /admin/payin/vendors/health
GET /admin/payout/vendors/health
```

Return the tracker snapshot. When the breaker is disabled, return an empty vendor list.

**Result:** authorization, nil-tracker, and closed/open/half-open snapshot tests passed. The namespaced paths follow the existing router structure.

## T6 — Chaos and index

Add scenario 8:

1. force-fail mockvendor and configure mockvendor2 as a lower-priority fallback;
2. create payout 1, which becomes uncertain and remains pinned to mockvendor;
3. create payout 2, which skips the open breaker and settles through mockvendor2;
4. restore mockvendor and let the resume job settle payout 1 through the original vendor;
5. verify two payouts, one settle per idempotency key, a balanced ledger, and clean projections.

The fallback priority is intentionally 1001 because the seeded mockvendor rule is priority 1000 and lower numbers win.

**Result:** scenario 8 and the full eight-scenario suite passed from a clean volume. Smoke, business, build, vet, lint, and test gates passed. The plan index marks this phase done.

## Final verification

Run the full plan 36 gate from a clean PostgreSQL volume and continue to [plan 41](41-phase7f-mvp-acceptance.md).
