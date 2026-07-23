# 39 — Phase 7d: Tiered KYC (L0/L1/L2) and Per-Tier Limits

Read [plan 36](36-phase7a-request-tracing.md) first. Prerequisite: [plan 38](38-phase7c-fee-quotes.md).

## Context and policy

The current auth model lets every newly registered user transact fully. This phase adds:

- L0: registered but unable to move money;
- L1: basic KYC with low limits;
- L2: full KYC with higher limits and optional business/KYB data.

KYC state lives in auth. The mock provider auto-approves L1 and always refers L2 for manual admin review. JWT `kyc_level` is a UX gate; the authoritative limits are synchronized into ledger `policy_limits` through `ApplyKycTier`.

## T1 — Auth schema

Migration `auth/000002_kyc` adds `auth_users.kyc_level`, `kyc_submissions`, and a partial unique index allowing only one pending submission per user. Constraints permit only levels 0–2, requested levels 1–2, and pending/approved/rejected statuses.

**Result:** grants, RLS, and a real PostgreSQL up/down/up cycle passed.

## T2 — Mock KYC provider

Add `internal/kycvendor` and `mockkyc`. `mock_mode` supports approve, reject, refer, and timeout. Level 2 always returns refer regardless of mode; level 1 defaults to approve.

**Result:** table-driven tests cover every mode, the level-2 rule, defaults, and invalid mode values. Auth-service owns the provider module in the boundary map.

## T3 — Submit, status, and admin review

Users may submit only the next level and may not create a second pending submission. Approval must atomically:

1. lock the submission;
2. apply the ledger tier limits;
3. update the user level;
4. mark the submission approved.

If `ApplyKycTier` fails, everything rolls back and the submission remains pending. Rejection records a reason without changing the level. Add public submit/status routes and internal admin list/approve/reject routes.

**Result:** auto-approval, referral, manual approval, rejection, duplicate pending submission, and rollback tests passed. A runtime type-assertion bug was replaced with a compile-time `Provisioner.ApplyKycTier` method; real auth-to-ledger integration tests now cover the approval path.

## T4 — JWT claim and gateway gates

Add `kyc_level` to access and refresh tokens. Tokens without the claim are treated as L0. Bootstrap admins receive L2.

Gate user-facing top-up, payout, and ledger posting routes at L1. Fee quotes and read-only GET routes remain available to L0 users. Return 403 `KYC_REQUIRED` with the minimum required level. Add the same posting check in the public ledger transport as defense in depth.

**Result:** tests cover old tokens, L0/L1 behavior, read-only exceptions, and direct ledger posting. A real routing bug was fixed: one middleware had been reused for both a multi-path ledger proxy and exact `/topup`/`/payout` routes, so its internal path check bypassed KYC on the exact routes. Separate middleware now handles proxy subpaths and exact route mounts.

## T5 — Tier templates and ledger application

Migration `ledger/000022_policy_tier_limits` adds read-only templates for L1 and L2. L1 has deliberately small limits; L2 is 100 times larger in the MVP seed. Add `LedgerService.ApplyKycTier`, which upserts the user's effective `policy_limits` from the selected template and is idempotent. Unknown levels return `InvalidArgument`.

**Result:** the implementation added the repository transaction, facade, gRPC method, client method, auth interface, and test harness. Four real PostgreSQL/bufconn integration tests prove L1-to-L2 in-place upgrade, policy enforcement, unknown-level errors, and idempotency. The work found and fixed a critical gap where the proto existed but no Go implementation was wired.

## T6 — Fixtures, business journey, and index

Update shared fixture helpers so existing scripts can still mint usable test tokens. The business journey covers:

```text
L0 transfer → 403 KYC_REQUIRED
L1 approval → small transfer succeeds, large transfer hits policy limit
L2 referral → admin approval → refreshed token → large transfer succeeds
```

**Result:** `business-e2e.sh` passed all eight sections from a clean volume; smoke and all chaos scenarios remained green without modifying their core flows. A documented policy-cache staleness issue was addressed by making the cache TTL configurable for the test process (`2s`) while keeping the production default at 60 seconds.

## Final verification

Run the plan 36 gate from a fresh PostgreSQL, Redis, and RabbitMQ volume. Continue to [plan 40](40-phase7e-vendor-resilience.md).
