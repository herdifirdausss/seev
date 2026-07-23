# 18 — Phase 3b: Multi-Currency Foundation and FX (S2, S3)

Prerequisite: plan 17 is complete. This document covers currency-aware amounts, currency-specific system accounts, and the optional internal FX flow.

## Contract for monetary amounts

All API and database amounts remain integer minor units for every currency. The currency exponent determines how those units are displayed and validated:

- IDR uses exponent 0;
- USD uses exponent 2.

Clients must not send major-unit decimals where a minor-unit integer is expected. `minor_unit` is used for display, validation, and future FX work; it does not change the ledger's integer accounting model.

## T1 — Currency registry and validation

### Implementation

1. Add migration `000011_currencies.up.sql` and its down migration. The `currencies` table contains `code`, `minor_unit`, and `enabled`, and is seeded with IDR and USD.

2. Add `pkg/currency` with an atomic registry and the following responsibilities:

   - load enabled currencies at startup;
   - return a currency's minor-unit exponent;
   - validate currency codes;
   - convert display values to minor units without silently rounding.

3. Remove hard-coded IDR assumptions from validation, DTOs, and service wiring. Existing IDR behavior remains unchanged.

### Definition of done

- [x] Currency metadata is stored in the database and loaded at startup.
- [x] Invalid or disabled currencies are rejected consistently.
- [x] Integer minor-unit behavior is covered by unit and integration tests.
- [x] Migration up/down verification passes.

### Result

The registry and database migration were implemented. Tests cover IDR and USD exponents, startup loading, invalid codes, disabled currencies, and conversion boundaries. Existing ledger and API tests remain green.

## T2 — Currency-specific system accounts

### Objective

Ensure that settlement, fee, and other system accounts are resolved by both account purpose and currency. A USD transaction must never be posted against an IDR system account.

### Implementation

1. Add migration `000012_currency_system_accounts.up.sql` and its down migration. Seed the required USD system accounts alongside the existing IDR accounts.

2. Resolve the user's account and currency before selecting the corresponding system account. Reorder processors where necessary so currency is known before fees, settlement, or system-account lookups are performed.

3. Include currency in fee-policy keys and idempotency keys where the operation can exist in more than one currency.

4. Reject cross-currency transfers unless they go through the explicit FX flow in T3. Do not silently convert or mix balances.

### Required tests

- Each supported currency resolves the correct system accounts.
- Missing system accounts fail clearly and do not create partial ledger state.
- Same-currency transfers succeed.
- Cross-currency transfers are rejected with a stable application error.
- Fee policies and idempotency remain isolated by currency.

### Result

Currency-specific system-account lookup and USD seed data were added. Processor ordering was adjusted so the user's currency is resolved before fee and settlement processing. Cross-currency transfers now fail explicitly, and the unit, integration, and migration tests pass.

## T3 — Optional internal FX conversion

### Objective

Support an explicit, auditable conversion between currencies without changing the public transfer contract or hiding an exchange inside a normal transfer.

### Locked design

- Add migration `000013_fx_conversion.up.sql` and its down migration.
- Represent FX as an explicit internal transaction type with dedicated FX-out and FX-in processors.
- Store `quote_id`, rate, and currency pair in transaction metadata.
- Use deterministic idempotency keys and dedicated currency-specific FX accounts.
- Keep the flow internal until the product and compliance contract for public FX is defined.
- Document open FX positions and reconciliation in an operations runbook.

The existing service handler contract does not change. FX is an additional internal operation, not an implicit branch inside `transfer_p2p`.

### Required tests

- A valid FX conversion debits the source currency and credits the target currency with the expected minor-unit amounts.
- Replaying the same idempotency key does not duplicate either side of the conversion.
- Missing or invalid quotes are rejected before entries are posted.
- FX accounts and currency pairs are validated independently.
- The ledger balance verifier remains clean after successful and failed conversions.

### Result

The FX transaction type, metadata, processors, and dedicated accounts were implemented. Conversion is deterministic and auditable, with no change to `service.handle`. Tests cover successful conversions, idempotent replay, invalid quotes, account isolation, and reconciliation. The open-position runbook was added, and the complete build, unit, integration, and race suites passed.

## Final verification

```bash
go build ./...
go vet ./...
go vet -tags=integration ./...
make test
go test -tags=integration -race ./...
```

Also verify migration 000011–000013 up/down/up cycles and run the currency-aware transfer and FX smoke tests against the Docker stack.
