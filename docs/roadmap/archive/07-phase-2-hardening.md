# 07 — Phase 2: Hardening (P1 Features)

> **⚠️ Superseded as an execution document (2026-07-11).** The detailed
> designs requested below were moved to [13-p1-backlog-review.md](13-p1-backlog-review.md),
> with execution plans in [14](14-phase2d-ledger-semantics-events.md) (H6,
> H7, H1), [15](15-phase2e-snapshots-statements.md) (H3, H4), and
> [16](16-phase2f-governance-recon-rls.md) (H5, H2, H8). H9 was superseded by
> [12 T7](12-phase2c-resilience-ops.md). **Work from 14–16, not this file.**
> The backlog review found issues that change the shape of several tasks,
> including a double-reversal race, missing `external_ref` persistence, and
> lifecycle transitions without guards. This document remains as historical
> context for the original task list.

Prerequisite: MVP (03–06) is complete and verified. Tasks may be executed
independently unless stated otherwise. The details are intentionally shorter
than Phase 1; before starting a task, write its detailed design as
`docs/roadmap/07x-<task>.md` and request review.

## Task H1 — Versioned event contract

- Define payload schemas for `ledger.transaction.posted.v1` and
  `ledger.transaction.failed.v1`: transaction ID, type, amount as a string,
  currency, compact entries (`account_id`, direction, amount), `occurred_at`,
  and `schema_version`.
- Create one `internal/ledger/events` package for payload types and event-type
  constants. Consumer modules may import this package as the only ledger
  subpackage exception, or the types may be promoted to the root `ledger`
  package.
- Make every processor's `OutboxEvents()` use these types instead of building
  payloads independently. Audit the processors one by one.

## Task H2 — External reconciliation

- Add `recon_batches` and `recon_items`: import a gateway settlement CSV,
  match it to `ledger_transactions` using `reference_id`/metadata, and classify
  each item as `matched`, `missing_internal`, `missing_external`, or
  `amount_mismatch`.
- Add one `suspense` system account per gateway for differences. Resolve them
  through `adjustment_*`, which receives maker-checker approval in H5.
- Add an admin CLI or endpoint for report upload and match results.

## Task H3 — Daily balance snapshots

- Add `account_balance_snapshots (account_id, as_of_date, closing_balance, entry_count)`.
  Populate it daily at 00:15 WIB from that day's entries and the previous
  snapshot.
- Cross-check snapshots against `fn_verify_account_balance`; any difference is
  an error.
- Add `GET /accounts/{id}/balance?as_of=2026-07-01`, using the snapshot plus
  entries for the current day.
- This lets the projection verifier in 06 Task 1c.2 avoid full scans for old
  accounts.

## Task H4 — Statements and export

- Add `GET /accounts/{id}/statement?from=&to=&format=json|csv`, using the H3
  snapshot for the opening balance and entries for the selected period.

## Task H5 — Maker-checker for adjustments

- Add `pending_adjustments (id, requested_by, approved_by, cmd_payload, status)`.
- The API must not post `adjustment_credit/debit` directly. Create a pending
  request, require approval by a **different** admin, then execute `Handle()`
  with both identities recorded.
- Freeze and confiscation remain direct because compliance actions may be
  urgent, but they require a `reason` in metadata and an audit log.

## Task H6 — Correct source/destination semantics

- `ledger_transactions.source_account_id` and `destination_account_id` are
  currently filled from sorted `AccountIDs[0..1]`; they are not semantic (see
  the schema note in 04).
- Change `TxProcessor.ResolveAccounts` to return
  `{Source, Destination, Fee uuid.UUID}` instead of a plain slice, or add a new
  method. The service must write these columns from the explicit result.
  Existing data does not need a migration because the columns are informative
  and entries remain the source of truth; record only a cutoff date in the
  changelog.

## Task H7 — Lifecycle and reversal guards

- Add tests and guards: reject a reversal of an already reversed transaction,
  reject a reversal of a reversal, and reject `withdraw_settle` for a hold that
  has already been canceled. Use a unique nullable
  `reversed_by_tx_id UUID` column on `ledger_transactions` as the audit link.
- Document valid escrow/withdrawal state transitions and reject illegal
  transitions in processor `Validate`, querying the original transaction's
  status inside the same database transaction.

## Task H8 — RLS and database roles (deferred from D11)

- Port the RLS section from
  `docs/design/legacy-schemas/ledgernew.sql`: `app_service` and
  `app_readonly` roles, minimal grants, RLS with `FORCE`, and policies. Adapt
  table names to the canonical schema.
- Move the application connection to `app_service` and keep the full test suite
  green.

## Task H9 — Load and chaos testing

> **Superseded by Task T7 in [12-phase2c-resilience-ops.md](12-phase2c-resilience-ops.md).**
> The 2026-07-11 hardening review added more detailed chaos scenarios (kill -9
> during posting and broker/PostgreSQL/Redis outages) with concrete
> `fn_verify_ledger_balance` assertions. Work from document 12. The optional
> 500-rps k6/vegeta load test remains useful, but is not a Phase 2 blocker.

- ~~k6/vegeta: run mixed 500-rps transfers against a 1,000-account pool for ten
  minutes, record p99 latency, and verify clean balances afterward.~~
- ~~Interrupt PostgreSQL/RabbitMQ during load, confirm recovery, and run the
  verifier to prove there are no inconsistencies.~~

## Phase 2 definition of done

- [ ] Every H1–H9 task has tests; the verifier remains clean after the full
      suite and load test.
- [ ] Add `docs/runbook.md` covering verifier discrepancies, a growing outbox
      `dead` queue, and reconciliation mismatches.
