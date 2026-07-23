# 37 — Phase 7b: Move Fraud Screening Out of the Ledger Transaction

Read [plan 36](36-phase7a-request-tracing.md) first. The goal is to remove the fraud network call from the ledger database transaction, where it currently holds user balance rows for up to 500 ms.

## Target placement

- Public P2P requests: ledger transport, before opening the database transaction.
- Top-ups: payin-service, before `ledger.Post`.
- Payouts: payout-service, before the hold.
- Settlement and cancellation: never screen; funds are already held.

The ledger core becomes write-and-validate only. Remove the old `PrePostHook` seam and `internal/ledger/screening` implementation entirely. The asynchronous velocity consumer remains unchanged and continues to count posted events in Redis DB 1.

## T1 — Add request context to the fraud contract

Add `request_id` and `flow` to `FraudService.ScreenRequest` as additive protobuf fields. Supported flow values are `p2p_transfer`, `topup`, and `payout`. Persist both values in `screening_events`; rules do not change.

**Result:** generated code, lint, round-trip tests, legacy callers without the new fields, and real database persistence all passed. The change is additive; the repository's current branch has no committed proto baseline for a meaningful breaking comparison.

## T2 — Shared `pkg/fraudcheck` client

Create a public shared client used by ledger, payin, and payout. It must not import internal modules. The client:

- enforces a 500 ms timeout;
- injects request ID and flow;
- returns a business block verdict without an error;
- returns infrastructure failures as errors so each caller can apply fail-open behavior;
- records `screening_client_errors_total{caller}`.

**Result:** five unit tests cover blocking, allowed verdicts, timeout, error propagation, and metadata injection. Boundary and both vet modes passed.

## T3 — Ledger transport screening

Remove the in-transaction hook, hook fields, old screening package, and hook metric. Keep `ErrScreeningBlocked` so the HTTP contract remains 422. Inject `fraudcheck.Client` into the public router only; internal adjustment, disbursement, and system routes are not screened.

Run the check after policy validation and before `svc.Post`. A block creates no ledger transaction. An infrastructure error logs and continues.

**Result:** public block, fail-open, internal-router, and no-client tests passed. The ledger transaction handler no longer makes a network call while holding database locks.

## T4 — Payin screening before posting

Screen a top-up in `postAndFinalize` before `ledger.Post`. Add `blocked` to the webhook event status constraint. A block records the reason, acknowledges the vendor with HTTP 200 because the vendor payment has already arrived, and does not post money. Admin replay uses the same path and therefore screens again. Fraud infrastructure errors fail open so a real deposit is not stranded.

**Result:** migration 000005 passed up/down/up verification. Unit and integration tests cover block, no-post, fail-open, and replay re-screening. Payin remains backward compatible when no fraud client is configured.

## T5 — Payout screening before the hold

Screen in `payout.Create` before route resolution, row insertion, or `withdraw_initiate`. A block creates no payout request and maps to 422 `SCREENING_BLOCKED`. Infrastructure errors fail open. Settlement and cancellation are intentionally untouched.

**Result:** gRPC, gateway, unit, and integration tests cover block, no row/no hold, fail-open, and settlement without screening. The full payout suite passed.

## T6 — Chaos and index

Extend scenario 7 to cover all three flows in both modes:

- fraud down: P2P, top-up, and payout all proceed with fail-open logs;
- fraud in block mode: all three are rejected before any money-moving write.

Keep ledger-balance and projection assertions at the end of the scenario.

**Result:** the stale log assertion from the removed in-transaction hook was corrected, and a false baseline comparison in the first block-mode draft was fixed. From a fresh PostgreSQL volume, scenario 7 and the full seven-scenario chaos suite passed, as did smoke, business end-to-end, lint, vet, and tests. The plan index marks this phase done.

## Final verification

Run the master gate from plan 36 and continue to [plan 38](38-phase7c-fee-quotes.md).
