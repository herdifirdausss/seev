# C5 Final Acceptance Evidence — Plan 61

## Current disposition

Implementation is present from commit `bb5ab9e`. Core correctness tests
(unit, integration, E2E journey scripts) were added in this acceptance pass
on branch `claude/advanced-financial-products-period-close-113a33` (2026-08-05).
Chaos/load/public-BFF-exposure gates are explicitly deferred and recorded
below — they do not block this evidence record.

## Evidence checklist

### Part A — Monthly interest capitalization

| Area | Result | Evidence |
|---|---|---|
| Daily interest formula (CalculateDaily) | unit-tested | `services/ledger/internal/ledger/interest/math_test.go` — normal day, zero/negative balance (no recognition), carry accumulation across days, carry boundary, large-balance overflow safety, malformed carry string |
| Interest service PreviewPeriodClose | unit-tested | `services/ledger/internal/ledger/interest/service_test.go` — ready vs. not-ready paths (missing rate, retryable accrual pending, blocked accrual) |
| Interest service ClosePeriod | unit-tested | `service_test.go` — happy path, `ErrClosedPeriodImmutable` on second call, `ErrPeriodNotReady` when preview not clean |
| CreateAdjustment / ApproveAdjustment | unit-tested | `service_test.go` — direction validation, exactly-one-of-source, source period must be closed, checker-required check |
| RunDaily + ClosePeriod (real Postgres, testcontainers) | integration-tested | `services/ledger/internal/ledger/interest_integration_test.go` — seeds product + rate + enrollment, runs RunDaily, verifies accrual posting to system accounts, ClosePeriod posts capitalization, second ClosePeriod rejected |
| `fn_prevent_c5_closed_period_mutation` DB trigger | integration-tested + E2E | integration test attempts raw UPDATE on closed period → error; E2E script confirms same via psql |
| End-to-end period-close journey | E2E script | `scripts/interest-period-e2e.sh` — product create → rate approve (maker/checker) → enroll → balance snapshot → RunDaily → poll accrual `completed_posted` → get period ID → PreviewPeriodClose (0 missing items) → ClosePeriod (checker) → verify `status=closed` → second close rejected → DB trigger blocked → `assert_ledger_balanced` |
| Period immutability (service layer) | confirmed | `ErrClosedPeriodImmutable` returned on second `ClosePeriod` call |
| Period immutability (DB layer) | confirmed | trigger `fn_prevent_c5_closed_period_mutation` rejects raw `UPDATE interest_periods SET status=…` |
| System account IDs from migration 000040 | confirmed | expense `00000000-0000-0000-0000-000000000029`, payable `00000000-0000-0000-0000-000000000031` |

### Part B — Durable scheduled-transaction failure policy

| Area | Result | Evidence |
|---|---|---|
| NormalizePolicy bounds validation | unit-tested | `services/ledger/internal/ledger/schedule/policy_test.go` — `CatchUpLimit` 0–7, `MaxInfrastructureAttempts` 1–20, `RetryWindowSeconds`, `ConsecutiveFailureThreshold` 1–20, unsupported `FeeMode` |
| PlanMissed all three missed-run policies | unit-tested | `policy_test.go` — `skip`, `run_once_latest`, `catch_up_bounded` at and above `CatchUpLimit=7` cap, across `once`/`daily`/`monthly` kinds |
| OccurrenceIdempotencyKey format | unit-tested | `policy_test.go` — `sched:<scheduleID>:<YYYY-MM-DD>` pattern |
| CommandDigest determinism | unit-tested | `policy_test.go` — same input → identical digest across calls |
| ExecuteOccurrence happy path | unit-tested | `services/ledger/internal/ledger/schedule/durable_test.go` — transfer posts, occurrence → `succeeded`, schedule `last_run_date` updated |
| Infra-retryable → `retry_wait` → `blocked` after exhaustion | unit-tested | `durable_test.go` — `INFRA_RETRY_EXHAUSTED` after `MaxInfrastructureAttempts` |
| Business failure → `failed_business`, schedule pauses | unit-tested | `durable_test.go` — pause after `ConsecutiveFailureThreshold` |
| Fee-cap exceeded → `SCHEDULE_FEE_CAP_EXCEEDED`, schedule paused | unit-tested | `durable_test.go` — `fee_cap_exceeded` pause reason |
| ErrAlreadyPosted treated as success | unit-tested + integration-tested | `durable_test.go`; `schedule_durable_integration_test.go:TestDurableSchedule_IdempotentReplay_ErrAlreadyPosted` |
| ExecuteOccurrence on inactive schedule → cancelled | unit-tested | `durable_test.go` — not counted as failure |
| PlanSchedule + ExecuteOccurrence (real Postgres) | integration-tested | `services/ledger/internal/ledger/schedule_durable_integration_test.go` — once-schedule happy path, execution-attempt row, `last_run_date` written |
| Idempotent replay ErrAlreadyPosted (real Postgres) | integration-tested | `TestDurableSchedule_IdempotentReplay_ErrAlreadyPosted` — pre-seeds ledger transaction, confirms exactly 1 transaction and occurrence `succeeded` |
| Daily skip policy — 3 `skipped_missed` + 1 executed (real Postgres) | integration-tested | `TestDurableSchedule_PlanSchedule_DailySkipPolicy` — schedule starting 3 days ago, run today, 4 total occurrence rows, 3 `skipped_missed`, 1 claimable |
| End-to-end once-schedule journey | E2E script | `scripts/schedule-policy-e2e.sh` Journey A — create once-schedule, `RunSchedulesNow`, poll occurrence `succeeded`, balance deltas verified |
| End-to-end daily-skip journey | E2E script | `scripts/schedule-policy-e2e.sh` Journey B — daily starting 3 days ago, run today, 3 `skipped_missed` rows confirmed via psql, today's occurrence `succeeded`, balance delta = 1 run only |
| `assert_ledger_balanced` after schedule run | confirmed | end of `schedule-policy-e2e.sh` |

### Part C — Top-up fees and fee-quote atomicity

| Area | Result | Evidence |
|---|---|---|
| money_in fee rule maker/checker flow | E2E script | `scripts/topup-fee-e2e.sh` — POST draft → submit → approve, flat 500 IDR, `bca` gateway |
| Fee quote on public router only | confirmed | `POST /fees/quote` registered only when `h.allowedTypes != nil` (`transport/http.go:185`); script calls `LEDGER_APP_PORT` (18090) |
| Fee quote returns correct fee amount | E2E script | `topup-fee-e2e.sh` — `fee_amount=500` returned, `quote_id` present |
| money_in atomically consumes fee quote | E2E script | `topup-fee-e2e.sh` — user wallet +100000 IDR; platform fee account +500 IDR; verified against psql snapshot |
| User credited full requested amount | confirmed | wallet balance = 100000 IDR (fee is a separate system-account entry, not deducted from wallet) |
| Idempotent replay does not double-charge | E2E script | `topup-fee-e2e.sh` — same idempotency key replayed; fee balance unchanged after replay |
| Consumed quote rejected on fresh key (QUOTE_EXPIRED) | E2E script | `topup-fee-e2e.sh` — distinct key + consumed `quote_id` → 422 with `QUOTE_EXPIRED` error code |
| `assert_ledger_balanced` after top-up with fee | confirmed | end of `topup-fee-e2e.sh` |

## Bugs fixed during acceptance

| Bug | File | Fix |
|---|---|---|
| Off-by-one in `RefreshExpectedItemCount` LEAST clause added +1 to `period_end_at::date` | `services/ledger/internal/repository/interest_repository.go` | Removed the erroneous `+ 1`; period boundary is exclusive (the next day), so subtracting `effective_from` already counts the correct number of days |

## Known residuals (tracked; do not block C5 evidence archival)

| Item | Tracking |
|---|---|
| Chaos matrix (13 scenarios from plan §3.5) — concurrent period-close, mid-run crash, duplicate RunDaily, etc. | plan §3.5; deferred, not done |
| Load baselines for `RunInterestDaily` fan-out and schedule dispatcher throughput | plan §4.10; deferred |
| Public Gateway / Admin-BFF exposure of savings product, rate, enrollment, and schedule endpoints | plan §13; deferred |
| Full per-transaction evidence log and timing data (plan §52.5) | deferred |
| `C5_FINANCIAL_PRODUCTS_ENABLED` activation and production rollout | plan §44; separate rollout decision |
| Multi-enrollment concurrent `ClosePeriod` atomicity under contention | deferred |
| `ConfirmFeeCap` / `RetryOccurrence` operator flows (schedule recovery) | plan §19; unit-tested but not exercised in E2E |

## Makefile targets

| Target | Script | Description |
|---|---|---|
| `make interest-period-e2e` | `scripts/interest-period-e2e.sh` | C5 Part A — period-close journey (`C5_FINANCIAL_PRODUCTS_ENABLED=true`; opt-in) |
| `make schedule-policy-e2e` | `scripts/schedule-policy-e2e.sh` | C5 Part B — once/daily-skip occurrence journey (`C5_FINANCIAL_PRODUCTS_ENABLED=true`; opt-in) |
| `make topup-fee-e2e` | `scripts/topup-fee-e2e.sh` | C5 Part C — fee quote atomicity, idempotent replay, `QUOTE_EXPIRED` journey |

Targets are **not** wired into `verify-full` — they require the feature flag
and are opt-in evidence-gathering runs.

## Foundational rules compliance (plan §1)

| Rule | Status |
|---|---|
| LedgerService is the source of truth for money | ✓ |
| Interest accrual and capitalization post through the standard transaction core | ✓ proven by integration test |
| Accrual and capitalization use named system accounts (expense / payable) | ✓ seeded by migration `000040` |
| Period close is a one-way state transition; no reopen | ✓ `ErrClosedPeriodImmutable` + DB trigger |
| Fee quote is single-use; consumption is atomic with the posting | ✓ `QUOTE_EXPIRED` on reuse |
| Daily schedule with skip policy does not catch up on missed runs | ✓ `skipped_missed` rows, balance delta = 1 run only |
| Durable schedule execution is idempotent under crash (ErrAlreadyPosted → success) | ✓ integration-tested |
| `C5_FINANCIAL_PRODUCTS_ENABLED` defaults false; E2E scripts set it only for their own startup | ✓ confirmed in config.go:740 |
| No chaos / load / public BFF exposure claimed as done | ✓ explicitly deferred above |
