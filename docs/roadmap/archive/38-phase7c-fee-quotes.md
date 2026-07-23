# 38 — Phase 7c: Fee Quotes Users Can Trust

Read [plan 36](36-phase7a-request-tracing.md) first. Prerequisite: [plan 37](37-phase7b-fraud-seam.md).

## Context

Previously, fees were resolved only while posting or settling. A user could see one amount in a UI and be charged another after an administrator changed the fee rule. This phase adds a quote flow:

```text
request quote → receive fee and expiry → submit quote_id →
pay exactly the quoted fee, or receive a clear 422
```

Internal flows without a quote retain the existing resolve-at-posting behavior.

## T1 — `fee_quotes` table

Add ledger migration `000021_fee_quotes` with user, transaction type, gateway, currency, exact amount, fee amount, fee gateway, expiry, consumption timestamp, and consumption reference. Enforce `amount > 0` and `0 <= fee_amount < amount`, with indexes for user history and unconsumed expiry. Apply the standard grants and RLS.

**Result:** migration up/down/up passed against real PostgreSQL and the schema matches the locked contract. The number follows the request-ID migration 000020 from plan 36.

## T2 — Create and consume quotes

Add `CreateQuote` and `ConsumeQuote` to `internal/ledger/feepolicy`.

- `CreateQuote` uses the existing specificity and clamp logic and defaults to a ten-minute TTL.
- Quotes are valid for one exact user, transaction type, currency, and amount.
- Consumption is one atomic update and accepts an executor so P2P consumption can run inside the posting transaction.
- A mismatch does not consume the quote and returns `QUOTE_MISMATCH`.
- Missing, expired, or already-consumed quotes return `QUOTE_EXPIRED` without revealing unnecessary state.

The implementation uses a read-only classification query after a zero-row update to distinguish mismatch from expired state without burning a valid quote. Ten concurrent consumers yield exactly one winner.

**Result:** unit, PostgreSQL integration, and race tests passed for creation, zero-fee quotes, expiry, mismatch preservation, replay, and concurrency.

## T3 — Public quote endpoint

Add `POST /api/v1/ledger/fees/quote` through the existing ledger proxy. The authenticated user comes from JWT, never the request body. The request includes transaction type, amount, optional currency, and optional gateway. The response includes quote ID, amount, fee, fee gateway, total debit, currency, and expiry.

`money_in` may be quoted even though it cannot be posted directly through the public transaction route; top-up fees remain deferred and therefore quote as zero.

**Result:** transport tests cover JWT ownership, validation, TTL, no-token behavior, and money-in quote creation. No gateway change was required.

## T4 — P2P posting honors the quote

Add typed `quote_id` to the public transfer request and command. With a quote:

1. perform the idempotency lookup first;
2. peek before account resolution so the fee account is included in the entry set;
3. consume atomically after opening the transaction and before locking balances;
4. use the consumed fee and gateway as the only fee source.

Expired quotes roll back the entire posting and create no transaction or entries. Mismatches also roll back and leave the quote usable. Replaying an already successful idempotency key returns the original transaction without consuming again. Requests without a quote are unchanged.

Map both errors to HTTP 422 and preserve the generic gRPC typed-error mechanism for future internal callers.

**Result:** integration tests prove the exact quoted fee is charged even after fee rules change, expired/mismatch rollback, mismatch reuse, idempotent replay, single-use concurrency, and unchanged no-quote behavior. The initial implementation exposed a timing issue in fee-account resolution; the read-only peek fixed it without changing the atomic consume source of truth.

## T5 — Payout uses stored fee data

Add `ConsumeFeeQuote` to the ledger gRPC contract and an optional `quote_id` to payout creation. Store `fee_quote_id`, `fee_amount`, and `fee_gateway` on `payout_requests` in migration `000004_quoted_fee`. The anti-burn order is:

```text
insert payout row → consume quote → store quoted fee → place hold
```

An expired or mismatched quote marks the row `rejected`, creates no hold, and returns 422. Settlement uses stored fee data even if fee rules change hours later. Payouts without quotes continue using `ResolveFee`.

**Result:** protobuf generation and lint passed. Integration tests prove quote stability at immediate settlement, resume-job settlement, and expired-quote rejection. A boundary violation in shared test utilities was fixed by translating quote errors at the ledger facade rather than importing a private ledger subpackage.

## T6 — Business journey and index

Extend `business-e2e.sh` to quote a transfer, change the fee rule, prove the old quote remains authoritative, test mismatch and single-use rejection, and repeat the guarantee for payout settlement.

**Result:** the complete seven-section business journey passed from a clean volume, including 13 quote assertions. A duplicate seed issue on a reused development volume was resolved by following the documented clean-volume gate. The plan index marks this phase done.

## Final verification

Run the plan 36 gate from a fresh database volume, including smoke, business, chaos, build, vet, lint, unit, and integration tests. Continue to [plan 39](39-phase7d-kyc-tiers.md).
