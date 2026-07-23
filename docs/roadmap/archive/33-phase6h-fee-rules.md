# 33 — Phase 6h: Database-Driven Fees per User and Route

Read [plan 26](26-phase6a-foundations.md) first. Prerequisite: [plan 32](32-phase6g-gateway-service.md).

The current fee policy is configured through static `FEE_*` environment variables and is keyed only by transaction type, gateway, and currency. This phase replaces it with `fee_rules` in `seev_ledger`, while keeping downstream fee metadata, validation, deduct-from-amount semantics, and withdrawal fee-on-settle behavior unchanged.

Resolution precedence is:

```text
exact user + route > user default > route default > global default
```

## T1 — `fee_rules` migration

Add `migrations/ledger/000019_fee_rules` with transaction type, gateway, currency, optional user, flat minor units, percentage basis points, fee gateway, enabled state, timestamps, and a uniqueness constraint that treats `NULL` user IDs as equal. Add an index for enabled lookups plus the same grants and RLS pattern as `policy_limits`.

**Tests:** clean PostgreSQL up/down/up cycle and duplicate-rule protection.

Status: not started.

## T2 — Database-backed fee policy

Replace the in-memory resolver with:

```go
Resolve(ctx, userID, txType, gateway, currency, amount)
    (fee, feeGateway, ok)
```

Use one query that considers the exact user and default rows, specific and empty gateways, and the enabled flag, ordered by specificity. Calculate flat plus basis-point fees with the existing truncation and defensive clamp: a fee must be positive and smaller than the amount.

Pass the user ID through transport metadata and the ledger facade. Extend the protobuf, gRPC server, ledger client, and payout poster signatures. Remove fee environment configuration and the old router-rebuild mechanism after the database resolver is live.

**Tests:** the four specificity levels, disabled and no-match rules, clamping, per-user transfers, and per-user withdrawal settlement with fee legs in `ledger_entries`.

**DoD:** no `FEE_*` environment variables remain and all fees come from the database.

Status: not started.

## T3 — Admin CRUD

Add admin-gated ledger routes:

```text
GET  /api/v1/admin/ledger/fee-rules
POST /api/v1/admin/ledger/fee-rules
PUT  /api/v1/admin/ledger/fee-rules/{id}
```

Disable rules with `enabled=false`; do not delete them. Validate transaction type, gateway, registered currency, and basis points below 10,000.

**Tests:** validation, admin authorization, list/create/update, and disable behavior.

Status: not started.

## T4 — Per-user end-to-end fees

Update `business-e2e.sh` to seed a global P2P fee and a user-specific override through the admin API. Prove different users pay different rates by checking their balances and the platform fee account. Also test a withdrawal fee and a full no-fee cancellation.

**DoD:** the route from admin API configuration to ledger entries is proven in the six-service business journey.

Status: not started.

## Final verification

Run the master gate and the six-service business journey, then continue to [plan 34](34-phase6i-verification.md).
