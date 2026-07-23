# 23 — Phase 4b: Payout Orchestration

Prerequisite: [plan 22](22-phase4a-payin-vendorgw.md), which provides the vendor registry and webhook infrastructure. The payout state machine is a multi-step money flow, so the full verification rules from [plan 09](09-hardening-review.md) apply and chaos testing is mandatory.

## T1 — Payout state machine and persistence

Add `internal/payout` and migration `000020_payout` with a `payout_requests` table. A request records the user, amount, currency, destination, vendor, state, idempotency key, ledger transaction references, vendor reference, and error information. State transitions are guarded atomically in SQL; callers do not provide an arbitrary predecessor state, preventing TOCTOU races.

The intended states are:

```text
created → held → submitted → vendor_pending → settled
                                      ├──────→ failed
                                      └──────→ cancelled
```

The hold is a `withdraw_initiate` transaction. Settlement and cancellation use the ledger facade and reference the original hold transaction so the same hold cannot be closed twice or settled after cancellation.

### Result

The migration, model, repository, state-transition guards, and module facade were implemented. Unit tests cover every legal and illegal transition, concurrent settlement/cancellation, and the guard against closing the same hold twice. Migration 000020 passed up/down/up verification with its grants, indexes, and RLS policies restored.

## T2 — Payout provider and mock modes

Extend `vendorgw` with `PayoutProvider` and a normalized `PayoutResult`. Providers receive the payout request ID as their idempotency key, use explicit timeouts and bounded retries, and return one of the normalized outcomes:

- settled immediately;
- pending and requiring polling;
- failed.

`mockvendor` supports instant settlement, asynchronous pending behavior, timeouts, failures, and duplicate-safe retries. The adapter never imports ledger or payout internals.

### Result

The provider contract and mock modes were implemented and covered by unit tests. The design intentionally uses polling rather than a payout callback webhook; the mock provider does not push a callback, so recovery owns the pending-state query.

## T3 — Hold, submit, and recovery

`Create` validates the request, writes the payout request, performs the ledger hold, transitions to `held`, submits to the provider, and transitions to `submitted`, `vendor_pending`, or `settled` according to the provider result.

A resume worker periodically finds stale `created`, `held`, `submitted`, and `vendor_pending` requests. Each operation is idempotent, and an infrastructure error leaves the request in a state that the next run can retry.

### Result

The orchestration flow, resume worker, metrics, and timeout handling were implemented. Integration tests cover successful settlement, provider failures, pending results, retries, and recovery after a process restart.

## T4 — Settlement and cancellation

Settlement calls `withdraw_settle`; cancellation calls `withdraw_cancel`. Both use the original hold reference and the ledger's atomic close guard. A second callback or a retry with the same idempotency key is harmless. A cancellation after settlement and a settlement after cancellation are rejected as invalid transitions.

### Result

The facade and processors were wired with the existing ledger guard. Race tests prove that concurrent settlement/cancellation produces one terminal outcome and leaves the ledger balanced.

## T5 — Public and admin APIs

Public endpoints:

```text
POST /api/v1/payout
GET  /api/v1/payout/{id}
```

Internal admin endpoints:

```text
GET  /admin/payout/requests?status=&vendor=
POST /admin/payout/requests/{id}/cancel
POST /admin/payout/requests/{id}/retry
```

Users may access only their own requests; another user's request is reported as 404 rather than confirming that it exists. Admin cancellation is available for stuck `vendor_pending` requests, and retry resubmits a stuck `submitted` request.

### Result

Handlers, router wiring, admin operations, ownership checks, and worker lifecycle were added. Twenty handler tests cover authentication, authorization, validation, ownership, not-found behavior, invalid transitions, create, cancel, and retry.

## T6 — Crash recovery chaos tests

The payout chaos scenario exercises recovery at `created`, `held`, `submitted`, and `vendor_pending`, then verifies terminal-state correctness, ledger balance, projection consistency, and the absence of permanently stuck requests.

Some states are seeded directly because the sub-millisecond window between a committed database transition and the next ledger call cannot be reached deterministically from an external shell. The recovery path is the same one used after a real crash. Provider timeout and pending modes exercise real infrastructure and provider behavior.

The test work found an important gap: the first `ResumeStuck` implementation handled only `submitted` and `vendor_pending`. A request stuck in `created` or `held` could therefore keep a user's funds held forever. Recovery was extended to retry `hold` for `created` and `submit` for `held`, with idempotent operations and regression tests.

### Result

`scenario_5` was added to `scripts/chaos-test.sh`. All four recovery points passed, including `fn_verify_ledger_balance` and `v_account_balance_audit`, and the existing scenarios remained green from a fresh Docker volume.

## Final verification

```bash
go build ./...
go vet ./...
go vet -tags=integration ./...
make test
go test -tags=integration -race ./...
./scripts/chaos-test.sh all
```

Live smoke tests covered instant settlement, asynchronous payout, admin listing, cancellation, ownership, and final ledger verification. The implementation deliberately does not claim a payout callback path that was not built; pending recovery is validated through provider polling and admin cancellation.
