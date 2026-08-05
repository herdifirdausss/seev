# C5 Entry-Gate Evidence — Plan 61 T0

This document records the baseline inventory and boundary decisions before
the C5 acceptance execution. The implementation was present in the current
Ledger codebase as of the activation date; this record captures what was
found at T0, what was tested this pass, and what was deferred.

## Activation decision

C5 was activated on 2026-08-05 to close the runtime/acceptance evidence gap
for three Ledger-owned product tracks: monthly interest capitalization (Part A),
durable scheduled-transaction failure policy (Part B), and top-up fees with
fee-quote atomicity (Part C).

## Baseline commit and migration heads

| Item | Value |
|---|---|
| Primary C5 implementation commit | `bb5ab9e` (~7,747 insertions: migration `000040`, interest, schedule, payin topup, savings HTTP handlers) |
| Ledger migration head at T0 | `000040_c5_advanced_financial_products.up.sql` |
| Branch for acceptance work | `claude/advanced-financial-products-period-close-113a33` |
| Merge-from-main at acceptance | `2cbd6d5` (Reorganize repository around service ownership) |

## Existing foundation found at T0

All items from plan §2 confirmed present:

| Item | Location |
|---|---|
| `savings_products`, `savings_rate_versions`, `savings_enrollments` tables | migration `000040` |
| `interest_periods`, `interest_accruals` tables | migration `000040` |
| `account_balance_snapshots` table (snapshot reader) | migration `000040` |
| `scheduled_transactions`, `scheduled_occurrences`, `scheduled_execution_attempts` tables | migration `000040` |
| `fee_quotes` table with `consumed_at` single-use enforcement | migration `000040` |
| `interest/service.go`, `interest/math.go` (Part A) | `services/ledger/internal/ledger/interest/` |
| `schedule/durable.go`, `schedule/policy.go` (Part B) | `services/ledger/internal/ledger/schedule/` |
| Payin `topup.go` fee-quote consumption (Part C) | `services/ledger/internal/ledger/` |
| Savings + interest HTTP handlers | `services/ledger/internal/transport/savings_http.go` |
| `fn_prevent_c5_closed_period_mutation` DB trigger | migration `000040` |
| Interest expense system account (IDR) | `00000000-0000-0000-0000-000000000029` (seeded by migration `000040`) |
| Interest payable system account (IDR) | `00000000-0000-0000-0000-000000000031` (seeded by migration `000040`) |

## Feature flag status

`C5_FINANCIAL_PRODUCTS_ENABLED` is read from the environment by
`internal/platform/config/config.go` (line 740) and defaults to `false`.
The monthly-capitalization workers and durable-schedule dispatcher are dormant
until the flag is set. The fee-quote path (Part C) is independent of the flag.

The flag remains `false` by default after this acceptance pass. Activation is
a separate rollout decision (plan §44), not a side effect of adding tests.

## Zero test coverage at T0

| Package | Files | Unit tests at T0 |
|---|---|---|
| `interest/` | `math.go`, `service.go` | zero |
| `schedule/` (C5 paths) | `durable.go`, `policy.go` | zero in those files |
| Integration tests (C5 flows) | — | zero |
| E2E journey scripts (C5) | — | zero |

## Gate policy compliance

The following were deferred to remain within the C5 local-stack scope:

- Chaos matrix (13 scenarios from plan §3.5)
- Load baselines for interest run and schedule dispatch
- Public Gateway / Admin-BFF exposure of savings and schedule endpoints
- Full per-transaction evidence log (plan §52.5)
- Multi-enrollment period-close atomicity under concurrency
- Feature-flag activation decision and production rollout

These deferrals do not block the C5 correctness-evidence gate.

## Evidence status

| Gate item | Status | Artifact |
|---|---|---|
| Package and table inventory at T0 | recorded | see tables above |
| Interest calculation unit tests | completed | `services/ledger/internal/ledger/interest/math_test.go`, `interest/service_test.go` |
| Durable schedule unit tests | completed | `services/ledger/internal/ledger/schedule/policy_test.go`, `schedule/durable_test.go` |
| Interest integration tests (real Postgres) | completed | `services/ledger/internal/ledger/interest_integration_test.go` |
| Durable schedule integration tests (real Postgres) | completed | `services/ledger/internal/ledger/schedule_durable_integration_test.go` |
| E2E journey scripts | completed | `scripts/interest-period-e2e.sh`, `scripts/schedule-policy-e2e.sh`, `scripts/topup-fee-e2e.sh` |
| Makefile targets | completed | `interest-period-e2e`, `schedule-policy-e2e`, `topup-fee-e2e` |
| Chaos / load / BFF exposure gates | deferred | plan §4.10, §3.5; see [C5 final acceptance](c5-final-acceptance.md) |
| Runtime journey checks | completed | see [C5 final acceptance](c5-final-acceptance.md) |
