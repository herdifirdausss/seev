# C4 Entry-Gate Evidence — Plan 60 T0

This document records the baseline inventory and boundary decisions before
the C4 acceptance execution. The implementation was present in the current
Ledger, Gateway, Admin BFF, and shared currency paths as of the activation
date; this record captures what was found at T0 and what was deferred.

## Activation decision

C4 was activated on 2026-07-28 as a conscious learning decision for
multi-currency money modeling (plan §3.1). The merge commit carrying the
primary implementation is `feat: implement end-to-end multi-currency (#26)`.

## Baseline commit and migration heads

| Item | Value |
|---|---|
| Baseline commit at activation | `2cbd6d5` (Reorganize repository around service ownership) |
| Ledger migration head at T0 | `000013_fx_position_accounts.up.sql` |
| Branch for acceptance work | `claude/multi-currency-e2e-68c019` |
| Merge-from-main at acceptance | `ce06a8d` (C3 multi-channel notifications) |
| No unrelated large Ledger migration in flight | confirmed |

## Existing foundation found at T0

All items listed in plan §2 were confirmed present:

| Item | Location |
|---|---|
| `accounts.currency` column | `services/ledger/migrations/000001_*.up.sql` |
| `ledger_transactions.currency` | migration 000001 |
| `currencies(code, minor_unit, enabled)` table | migration 000008 |
| IDR exponent 0, USD exponent 2 | seed data in migration 000008 |
| `internal/platform/money/currency` registry | `internal/platform/money/currency/` |
| Currency-specific system accounts (USD settlement, fee, FX position, etc.) | migrations 000010–000013 |
| `fx_out` / `fx_in` processors | `services/ledger/internal/ledger/fx/service.go` |
| Explicit cross-currency rejection in ordinary transfer | `services/ledger/internal/ledger/ledger.go` |
| FX quotes and conversions tables | migrations 000011–000012 |
| Transactional outbox and versioned event governance | `services/ledger/internal/ledger/outbox/` |
| Per-service databases and typed gRPC boundaries | confirmed across all services |

## Hard-coded IDR inventory (relevant to C4 scope)

| Location | Nature | Disposition |
|---|---|---|
| `internal/platform/money/currency` boot | boots IDR-only; USD loaded at startup via `module.LoadCurrencies` | working as designed — runtime loads DB |
| Gateway balance endpoint | historically returned IDR-only single balance | new `GET /api/v1/balances` returns per-currency list |
| Fee rules | keyed by currency | backward-compatible; IDR rules unchanged |
| `ProvisionUser` in `ledger.go` | was silently resetting `kyc_level` to 0 on every call | **critical bug found and fixed** (see §Bugs fixed) |

## Currency registry and system accounts

| Item | Status |
|---|---|
| IDR row in `currencies` | present, `minor_unit=0`, `enabled=true` |
| USD row in `currencies` | present, `minor_unit=2`, `enabled=true` |
| USD settlement account | created by migration 000010 |
| USD fee account | created by migration 000010 |
| USD FX position account (IDR leg) | created by migration 000013 |
| USD FX position account (USD leg) | created by migration 000013 |
| IDR system accounts | unchanged from prior plans |

## Public and internal request fields carrying currency

| Endpoint | Currency field | Status |
|---|---|---|
| `GET /api/v1/currencies` | response `code` | active |
| `GET /api/v1/balances` | response per-currency list | active |
| `GET /api/v1/balances/{currency}` | path param | active |
| `POST /api/v1/currencies/{currency}/enable` | path param | active |
| `POST /api/v1/transfers` | request `currency` | active |
| `POST /api/v1/fx/quotes` | request `source_currency`, `target_currency` | active |
| `GET /api/v1/fx/quotes/{id}` | response | active |
| `POST /api/v1/fx/conversions` | request `quote_id`, amounts | active |
| `GET /api/v1/fx/pairs` | response `base_currency`, `quote_currency` | active |

## FX implementation atomicity review

The implementation in `services/ledger/internal/ledger/fx/service.go`
(`ExecuteConversion`) executes within one PostgreSQL SERIALIZABLE transaction:

1. lock conversion idempotency record
2. lock quote row
3. validate owner, status, expiry, expected amounts
4. resolve and lock all four account-balance rows (deterministic UUID order)
5. validate source balance and projected position limits
6. post source-currency ledger transaction (`fx_out`)
7. post target-currency ledger transaction (`fx_in`)
8. mark quote consumed
9. mark conversion posted
10. insert outbox events
11. commit

Crash before commit → neither leg posted. Retry → existing conversion returned.
Both legs share `conversion_id` and `quote_id` for linkage (plan §16.4).

## No unrelated migration in flight

Confirmed: no migration was staged in the current branch beyond the baseline
that touches existing IDR account or transaction semantics.

## Gate policy compliance

The following were deferred to remain within the C4 local-stack scope:

- Real bank USD corridor
- Real market rate feed
- USD top-up via real payin route (funded via governed admin adjustment instead)
- USD payout via real payout route
- Chaos/load gates (outside local-stack baseline)
- Non-zero spread activation (plan §5.14: zero bps for initial slice)

These deferrals do not block C4 archival per plan §3.4.

## Evidence status

| Gate item | Status | Artifact |
|---|---|---|
| `make contracts` / protobuf / OpenAPI gates | pass | confirmed at baseline commit |
| All service unit tests | pass | `go test ./...` clean |
| Ledger migrations 000011–000013 present | confirmed | migration files verified |
| IDR and USD registry rows confirmed | confirmed | DB seed in migration 000008 |
| Required system accounts inventoried | confirmed | see table above |
| FX atomicity reviewed | confirmed | single-transaction implementation |
| No unrelated large migration in flight | confirmed | branch diff reviewed |
| Runtime journey checks | completed | see [C4 final acceptance](c4-final-acceptance.md) |
