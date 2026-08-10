---
status: active
plan_id: "65"
owner: database
last_reviewed: 2026-08-10
generated: false
---

# Plan 65 · A11 — Database Audit and Hardening

> **Status:** In progress — Round 3 critical findings and governance decisions are closed; full integration acceptance remains open
> **Owner:** database
> **Last reviewed:** 2026-08-10
> **Depends on:** Plan 53 · B0 (load baseline), Plan 49 · A6 (internal security)
> **Current milestone:** Round 3 — complete integration acceptance and resolve remaining read-access governance
> **Next action:** Complete the bounded full integration suite
> **Blocking issue:** none
> **Runtime acceptance:** partial — the complete race-enabled Ledger
> integration suite, focused `000049` posting-boundary contracts, and focused
> `000022` KYC maker/checker contracts pass; the repository-wide integration
> suite remains pending

## Open Work (Round 3)

- [x] Fee-rule maker/checker and DB non-negative constraint (`000044`)
- [x] Disbursement processing DB approval gate (`000045`)
- [x] Dispute API, amount/deadline guards, terminal immutability, and expiry worker (`000046`–`000047`)
- [x] Scheduled execution routed through the shared command/KYC subject boundary
- [x] Revoke unused merchant and routing-table `DELETE` grants
- [x] Add the KYC level/deadline consistency constraint
- [x] Remove direct app-role writes to `ledger_entries` and add the bounded, single-batch bulk-posting function (`000049`)
- [x] Retain container-backed migration contracts for direct-SQL rejection, balanced posting, duplicate-batch rejection, and atomic rollback
- [x] Keep encrypted vendor boundary payloads inaccessible to broad `app_readonly`; prove denial with a role-level integration contract
- [x] Keep emergency freeze initiation single-admin, but require maker/checker before releasing frozen funds
- [x] Require distinct maker/checker identities for manual KYC approval/rejection and retain retry-safe authorization evidence (`auth/000022`)
- [x] Preserve domain-specific approval columns and record their canonical semantic mapping for future cross-domain consumers

## Exit Criteria

- Every non-deferred Round 3 finding is fixed and verified by a corresponding schema contract test.
- `make integration` passes clean against Round 3 migrations.
- Every remaining finding is either closed or explicitly deferred with owner and rationale.

---

**Created:** 2026-08-01
**Track:** A11 — End-to-end database integrity, security, and business-completeness audit
**Primary objective:** Find and close real gaps in schema safety, security posture, and business-process completeness across the whole database, through repeated, evidence-backed audit passes
**Repository:** `herdifirdausss/seev`
**Trigger:** User request for a full end-to-end database audit ("safe, secure, efficient, apakah ada duplication, bagaimana secara bisnis") after the B0 load-capacity work (Plan 53) surfaced a real data-model gap (unbounded row growth from a service never wired into the shared retention framework)

---

## 1. Method

Each round runs four parallel, read-only investigations against the current
migration set and Go repository code, each briefed on what prior rounds
already found and fixed so effort concentrates on genuinely new ground:

1. **Schema safety and efficiency** — missing CHECK/NOT NULL/UNIQUE
   constraints, missing or redundant indexes, data-type correctness,
   orphan-prone foreign keys, dead tables/columns.
2. **Security** — Row-Level Security coverage, GRANT hygiene, SQL injection
   surface, encryption coverage, audit-trail completeness for sensitive
   state transitions, idempotency-key storage consistency.
3. **Business completeness** — does the data model support what the
   business actually needs (dispute handling, KYC lifecycle, fee
   configuration, reversal/correction paths, statement/reporting access)?
4. **Duplication and redundancy** — repeated concepts modeled independently
   across services, inconsistent naming/typing for the same concept,
   copy-paste-drift in the RLS/GRANT pattern every table is expected to
   follow.

Findings are triaged by the user before any fix lands — this plan does not
assume every finding is worth fixing; some are explicitly deferred with a
stated reason (see §5).

## 2. Round 1 — findings and fixes (done)

**Findings:** no DB-level guarantee that `ledger_entries` stays balanced
(app-code and a daily detective job only); merchant refunds had no DB-level
link back to the original charge, so double-refunding the same charge relied
entirely on application logic; KYC never expired once approved, no matter how
stale the underlying identity verification; no chargeback/dispute
case-management data model existed at all; three amount/quantity columns
(`fraud.screening_events.amount`, `ledger.recon_items.amount`,
`assurance_findings.amount_minor`) had no positivity CHECK.

**Fixed:**

- `services/ledger/migrations/000034_entries_balance_trigger.up.sql` — a `DEFERRABLE
  INITIALLY DEFERRED` constraint trigger on `ledger_entries` that rejects any
  unbalanced transaction at commit time, not just via the periodic
  `fn_verify_ledger_balance` detective check.
- `services/ledger/internal/processors/refund.go` + `services/ledger/internal/ledger/handle/service.go`'s
  `lifecycleCloseReason` map — refund now requires `ReferenceID` (the
  original charge) and closes it via the same atomic `CloseOriginal`
  mechanism reversal/escrow-release/withdraw-settle already used, making
  double-refund impossible even under a race
  (`services/ledger/internal/ledger/schema_contract_test.go`'s
  `TestSchemaContract_Refund_LinksToOriginalCharge`).
- `services/auth/migrations/000018_kyc_expiry.up.sql` (`auth_users.kyc_verified_until`)
  + `services/auth/internal/worker/expiry.go` — a periodic job downgrades a user to
  KYC level 0 once their verification window lapses, forcing
  re-verification; mirrors the existing sanctions-rescreen job's shape.
- `services/ledger/migrations/000035_chargeback_disputes.up.sql` — a full
  open/evidence_submitted/won/lost/expired case lifecycle, linked to both
  the original charge and the chargeback money-movement transaction
  (`services/ledger/internal/ledger/dispute/`).
- `services/fraud/migrations/000008_*`, `services/ledger/migrations/000033_*`,
  `services/assurance/migrations/000008_*` — the three missing positivity CHECKs,
  each verified safe against existing insert paths before adding.

## 3. Round 2 — findings and fixes (done)

**Findings:** `vendor_callback_inbox`/`vendor_outbound_attempts` were the
only two tables in the entire ~90-table schema with zero Row-Level Security,
and the raw vendor webhook payload (`raw_body`) had no field-level encryption
at all, unlike every other raw-payload column in the codebase; chargeback
dispute resolution recorded no actor (no `resolved_by`, no transition
history); four high-risk admin action types (`freeze_confiscate`,
`reversal`, `chargeback`, bulk disbursement) required only a single admin's
authorization, unlike `services/ledger/internal/ledger/adjustments`'s existing
maker-checker precedent; several smaller gaps (missing FK indexes, a
redundant index on `sanctions_entries`, two more missing amount CHECKs, a
missing `NOT EXISTS` guard in a retention purge function, unused DELETE
grants on merchant/routing tables — this last one flagged but deliberately
not fixed that session).

**Fixed:**

- `services/vendor-service/migrations/000004_boundary_rls_and_encryption.up.sql` +
  `services/vendor-service/internal/callback.go` — RLS added (scoped to the
  `vendor_app` role, matching that table's existing narrow-grant design);
  `raw_body`/`selected_headers` now sealed via `internal/platform/security/crypto` before INSERT.
  Verified live: `tools/loaddataset`'s dataset-manifest tooling initially hit
  "permission denied" reading business tables through the `load_observer`
  role, which surfaced that `load_observer` needed `app_readonly`
  membership plus an explicit grant on `schema_migrations_ledger` — fixed
  in `scripts/load-postgres-init/05-load-observer-readonly.sh`.
- `services/ledger/migrations/000037_chargeback_dispute_audit_trail.up.sql` —
  `resolved_by` plus a `chargeback_dispute_status_changes` transition-history
  table, mirroring `kyc_level_changes`'s existing shape.
- Maker-checker extended to `freeze_confiscate`/`reversal`/`chargeback` by
  reusing the existing `pending_adjustments` table (no new schema needed —
  correctly recognized as the same shape); bulk disbursement got its own
  approval columns on `disbursement_batches`
  (`services/ledger/migrations/000038_disbursement_maker_checker.up.sql`).
- `services/gateway/migrations/000009_*` (missing FK index),
  `services/fraud/migrations/000009_sanctions_entries_drop_redundant_index.up.sql`,
  `services/payin/migrations/000016_*`, `services/payout/migrations/000016_*` (two more amount
  CHECKs), `services/assurance/migrations/000009_runs_purge_cursor_guard_and_index.up.sql`
  (the `NOT EXISTS` guard).

**Explicitly not fixed in Round 2, now closed in Round 3:** unused DELETE grants on `merchant_api_key_scopes`,
`merchant_webhook_attempts`, `merchant_webhook_events`,
`payin_vendor_gateways`/`payin_routing_rules`,
`payout_vendor_gateways`/`payout_routing_rules` — no Go code issues a
`DELETE` against any of them. Migrations `gateway/000013`, `payin/000018`, and
`payout/000017` revoke those table privileges.

## 4. Round 3 — current disposition

The original Round 3 snapshot is superseded by the implementation now in the
tree. Fee configuration uses versioned maker/checker approval (`000044`),
disbursement processing has a database approval gate (`000045`), disputes are
wired to admin HTTP APIs and an expiry worker with amount/deadline/terminal
guards (`000046`–`000047`), and durable schedules execute through the shared
command subject/KYC boundary. The app role now posts entries only through
`fn_post_ledger_entries_bulk` (`000049`); direct table writes are revoked, each
transaction is limited to one race-safe posting batch, and the immutable and
deferred-balance triggers remain active.

Manual KYC approval and rejection now use durable `kyc_decision_requests`
(`auth/000022`). A maker proposes the customer outcome, a different checker
authorizes or rejects that proposal, and `app_service` cannot move the KYC
submission terminal without the matching authorization. Automated provider
and sanctions decisions use distinct named machine actors, and an authorized
approval remains active across ledger timeout/retry without allowing a
conflicting second decision.

The decision evidence follows the same closure-gated, hold-aware 365-day
lifecycle as its KYC submission. The submission purge cascades to
`kyc_decision_requests`; retaining the mandatory child permanently would also
retain the sensitive parent indefinitely and violate the account-closure
contract. An integration contract proves both records survive before the
horizon and are removed together after it.

Freeze governance is now risk-based rather than treating both reversible
actions identically. `freeze_initiate` remains directly available to one admin
so suspected fraud or AML exposure can be contained without checker delay.
`freeze_release` removes that protective control and is therefore blocked from
direct posting; a maker must create a pending adjustment and a distinct
checker must approve it before funds return to the spendable balance.

The encrypted vendor boundary tables deliberately remain inaccessible to
`app_readonly`. Encryption reduces disclosure impact but does not turn raw
bank/card/customer callback material into reporting data; ordinary operations
use sanitized columns and retention audit tables instead. A PostgreSQL role
contract proves the cross-service reporting role cannot select either
`vendor_callback_inbox` or `vendor_outbound_attempts`.

Approval column names also remain domain-specific. Renaming persisted columns
would create migration and compatibility risk without changing authorization;
the names encode real differences in each workflow. Any future cross-domain
audit projection must map them to the canonical `maker`, `checker`,
`request_reason`, and `decision_reason` fields below rather than querying for
one physical naming convention.

| Domain record | Maker | Checker | Request reason | Decision reason |
|---|---|---|---|---|
| Pending adjustment | `requested_by` | `approved_by` | `reason` | Not stored separately |
| Fee-rule proposal | `created_by` | `approved_by` or `rejected_by` | Rule payload | `decision_reason` |
| Disbursement batch | `created_by` | `approved_by` | Batch payload | `decision_reason` |
| KYC decision request | `requested_by` | `decided_by` | `request_reason` | `review_reason` |

The remaining findings are low-risk normalization work rather than missing
critical money-safety paths:

- low-risk normalization work remains for tier-limit checks, dispute-history
  pagination, and merchant idempotency-key digests.

## 5. Still explicitly deferred (not new findings, carried forward for context)

Recorded across Rounds 1–3, deliberately not fixed, with the reason each was
deferred:

- **Merchant-tenant fee/pricing table** — `fee_rules`/`fee_quotes` have no
  merchant/tenant column at all, and `MerchantPayinCredit` is invoked with
  no fee metadata, meaning merchant payin fees are likely always zero. Large
  enough to need its own design pass, not a quick-win fix.
- **No merchant invoice/statement tables** — only ad-hoc query results, no
  persisted document-numbered statement entity.
- **FX has no real rate source** — `fx_in`/`fx_out` store `rate` as a free
  text note only, never used arithmetically, and nothing in the app calls
  these processors today.
- **No defined merchant settlement cycle** (T+1/T+2/rolling reserve) —
  purely on-demand payout.
- **No dormant-account/escheatment workflow.**
- **payin/payout routing-table and repository duplication** — byte-for-byte
  duplicated schema and Go code between `services/payin/internal/repository/routing_repository.go`
  and `services/payout/internal/repository/routing_repository.go`, already drifted
  (payin has `ListVendorGateways`, payout doesn't).
- **Inconsistent currency typing** — `CHAR(3)` in most tables, plain `TEXT`
  in `fee_rules`/`fee_quotes`/routing tables/`vendor_callback_inbox`.

## 6. Evidence

No dedicated load-test-style evidence directory — this plan's evidence is
the migration files themselves (each self-documenting its own audit-finding
provenance in its header comment) plus the integration tests added alongside
each Round 1/2 fix (`services/ledger/internal/ledger/schema_contract_test.go`,
`services/vendor-service/internal/callback_integration_test.go`,
`services/auth/internal/auth/kyc_integration_test.go`, and others named in each fix's own
commit). On 2026-08-10, the focused `000049` app-role contracts were rerun
against disposable PostgreSQL 16.14 containers and proved direct INSERT
rejection, balanced posting, duplicate-batch rejection, and atomic rollback.
An unbounded full tagged-suite run exposed Docker saturation and hit Go's
default 10-minute package timeout; the repository now bounds package
parallelism through `make integration`. A clean run of that canonical gate is
still required for the exit criterion. The bounded service gate
`make integration-service SERVICE=ledger` passed with the race detector on
2026-08-10; the schema-contract package completed in 534 seconds instead of
being killed by the former default timeout.
Focused Auth integration against disposable PostgreSQL 16.14 also passed on
2026-08-10 for automatic decisions, distinct manual maker/checker approval,
self-approval rejection, rejected-proposal recovery, idempotent replay, and
the raw-SQL app-role authorization backstop introduced by `000022`.

## 7. Definition of done

This plan is not done. It closes only when either:

- Round 3's high-priority items (§4.1) are triaged and fixed or explicitly
  deferred with a stated reason (matching §5's pattern), and
- a Round 4 pass finds no new high-priority gaps, or the user decides
  further rounds aren't warranted.
