# 41 — Phase 7f: MVP Acceptance, Chaos, and Documentation

Read [plan 36](36-phase7a-request-tracing.md) first. Prerequisite: [plan 40](40-phase7e-vendor-resilience.md). This closes the MVP series 36–41.

## T1 — Final business journey

Run one fresh-volume `business-e2e.sh` journey covering:

1. L0 transfer rejection, L1 approval, low-limit success, and high-limit rejection;
2. L2 referral, admin approval, token refresh, and high-limit success;
3. fee quote, rule change, exact quoted fee, mismatch rejection, and re-quote;
4. quoted payout, vendor failover, stored-fee settlement, and ledger balance;
5. request ID in gateway, ledger, fraud, and domain records;
6. KYC, fraud, vendor-health, fee-rule, reconciliation, and outbox admin surfaces;
7. final balance, projection, and stuck-pending invariants.

### Result

The final journey was added and passed from a clean volume. It includes a deterministic failover drill: a probe payout pins an uncertain request to mockvendor, opens its breaker, and routes the next quoted payout to mockvendor2. The quoted fee remains authoritative. A missing success log in the fraud velocity consumer was added so the asynchronous request-ID path is observable and testable.

## T2 — Consolidated chaos suite

Run scenarios 1–6, fraud fail-open/block scenario 7, and vendor-failover scenario 8. Shared fixtures mint KYC-approved tokens, and every scenario ends with `assert_ledger_balanced`, projection consistency, and no-stuck checks.

### Result

The complete eight-scenario suite passed from a clean volume. It covers service outages, vendor outages, infrastructure restarts, redelivery, fail-open screening, pre-write blocking, and payout recovery without balance loss or duplication.

## T3 — Final documentation

Update the plan index, root README, `.env.example`, and `PROJECT_GUIDE.md` with the final routes, environment variables, runtime architecture, fraud placement, fee quotes, KYC gates, breaker behavior, and deferred work.

### Result

Documentation was cross-checked against the code and Compose configuration. Deferred work includes top-up fees, a real KYC provider and document storage, distributed breakers, expired-quote cleanup, full non-IDR activation, broader tracing, and an asynchronous tier-application retry queue.

## Final verification

The MVP gate passed from clean Docker volumes: build, both vet modes, lint, race-enabled tests, smoke, business journey, and the full chaos suite. A timing-only payout chaos flake occurred under a heavily loaded verification run and passed on two immediate clean reruns; no MVP code regression was found.

The learning-oriented fintech MVP is complete. The next reference is [plan 42](../42-long-term-roadmap.md); execute a future track only when its documented trigger is met.
