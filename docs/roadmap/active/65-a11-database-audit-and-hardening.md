# Plan 65 · A11 — Database Audit and Hardening

**Created:** 2026-08-01
**Status:** In progress — Round 1 and Round 2 findings fixed and verified; Round 3 findings documented, not yet fixed
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

**Explicitly not fixed that round** (recorded here so it isn't silently
dropped): unused DELETE grants on `merchant_api_key_scopes`,
`merchant_webhook_attempts`, `merchant_webhook_events`,
`payin_vendor_gateways`/`payin_routing_rules`,
`payout_vendor_gateways`/`payout_routing_rules` — no Go code issues a
`DELETE` against any of them. **Confirmed still open in Round 3 (§4.2).**

## 4. Round 3 — findings, not yet fixed

Four parallel investigations, aware of everything in §2–3, found the
following new gaps. None have been fixed yet — this section is the
"document first" record the user asked for; §5 tracks what's explicitly
deferred vs. genuinely open for a future session.

### 4.1 High priority — business and DB-level consistency

- **Fee-rule changes are single-admin.** `POST/PUT /admin/ledger/fee-rules`
  (`services/ledger/internal/transport/http.go:283,316`) is gated only by
  `isAdmin(r)` — no maker-checker, even though Round 2 established that
  exact pattern (a second identity required) for adjustments, reversals,
  chargebacks, and bulk disbursement. A bad or malicious fee-rule change has
  ongoing, compounding revenue impact, arguably larger than any single
  adjustment.
- **`fee_rules.flat_minor_units` has no non-negativity CHECK**
  (`services/ledger/migrations/000019_fee_rules.up.sql:9`) — its sibling column,
  `percent_basis_pts`, has one two lines below. The HTTP layer validates
  this, but there is no DB-level backstop; a negative flat fee would make
  `FeeCalculator` credit users instead of charging them.
- **`disbursement_batches`' maker-checker gate is app-code-only.** Round 2's
  own doc comment in `services/ledger/internal/ledger/disbursement/disbursement.go:124`
  claims it is "enforced by a DB CHECK constraint as the backstop," but
  `services/ledger/migrations/000038` only added a `chk_disbursement_batches_approver_not_creator`
  check — nothing enforces that `status IN ('processing', ...)` implies
  `approved_by IS NOT NULL`. A direct SQL fix or a future non-`ApproveBatch`
  code path could flip a batch to processing with no approval on record.
- **Chargeback dispute case management has zero API exposure.** The full
  service (`services/ledger/internal/ledger/dispute/dispute.go`) is never wired into
  any router — `grep` for `dispute` across `services/ledger/internal/transport/http.go`
  and `services/adminbff/internal/` returns nothing. Nobody can open a case, submit
  evidence, or see the open-dispute queue except via raw SQL. There is also
  no worker analogous to the KYC-expiry job (§2) that auto-transitions a
  case past its `evidence_due_at` deadline to a loss — a real card-network
  SLA-compliance gap sitting on top of otherwise-complete Round 1 work.
- **Scheduled/recurring transactions bypass the policy engine entirely.**
  `services/ledger/internal/ledger/schedule/schedule.go`'s `Poster` interface calls
  `svc.Post` directly, never through the `PolicyChecker` the public HTTP
  router applies before posting. A recurring transfer created while a user
  was KYC level 2 keeps firing at level-2 limits indefinitely after
  `kyc_verified_until` lapses and the expiry worker (§2) downgrades them —
  the KYC-expiry feature doesn't reach this path at all.

### 4.2 Medium priority

- **Unused DELETE grants still not revoked** — confirmed still present,
  carried over unfixed from Round 2 (§3).
- **`auth_users.kyc_level`/`kyc_verified_until` consistency has no DB
  CHECK** — entirely app-maintained (approval sets it, downgrade clears it);
  no constraint like `CHECK (kyc_level > 0 OR kyc_verified_until IS NULL)`
  backs the invariant the migration's own comment states.
- **`chargeback_disputes.amount` isn't bounded against the original
  transaction's amount** — only `CHECK (amount > 0)` exists; nothing catches
  a dispute opened for more than the underlying charge.
- **`vendor_callback_inbox`/`vendor_outbound_attempts` still have no
  `app_readonly` policy**, unlike every other table in the schema including
  this same migration directory's own `vendor_retention_holds`/
  `vendor_retention_audit`. The Round 2 migration's comment rationalizes
  this as intentional (vendor never gets a cross-service grant), but it
  means compliance/ops tooling that can read every other table has a blind
  spot on the two tables holding raw vendor payloads.
- **`freeze_initiate`/`freeze_release` remain single-admin**, inconsistent
  with `freeze_confiscate`'s Round 2 elevation to maker-checker — the
  original design comment even explains why confiscation was elevated
  without saying why lock/unlock wasn't.
- **Eight tables use four different column names for "who approved this"**
  (`approved_by`/`resolved_by`/`decided_by`, plus `disbursement_batches`
  reusing generic `created_by`), and three names for "why"
  (`reason`/`decision_reason`/`resolution_reason`) — a real, if cosmetic,
  obstacle to any cross-cutting "who decided X" admin query. Bulk
  disbursement's maker-checker migration also didn't reuse the
  `requested_by`/`decided_at` shape two sibling migrations from the same era
  (`merchant_tenant_lifecycle_requests`, `auth_operator_offboarding_requests`)
  explicitly cite as their template.

### 4.3 Low priority

- `policy_tier_limits`' limit columns lack the `CHECK (> 0)` its sibling
  `policy_limits` table has — low risk today since the table has no live
  write path, only the seeding migration's own INSERT.
- Chargeback vs. KYC audit-trail tables use different actor-column names
  (see §4.2) and `chargeback_dispute_status_changes`' pagination index omits
  the `id DESC` tiebreaker `kyc_level_changes`' equivalent index has.
- `merchant_idempotency_records.idempotency_key` is stored in plaintext, not
  through the digest pattern `services/ledger/migrations/000028_idempotency_digest.up.sql`
  established — lower risk since the whole row hard-deletes 24h after
  expiry, but a pattern inconsistency worth a follow-up if that changes.
- KYC tier approval (`Module.ApproveKYC`/`RejectKYC`,
  `services/auth/internal/auth/kyc.go:204-220`) is still single-approver — confirmed still
  open, unaddressed by Round 2's maker-checker work.

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
commit). `go build ./...`, `go vet ./...`, and
`go test -tags=integration ./...` all pass clean as of the last Round 3
documentation pass.

## 7. Definition of done

This plan is not done. It closes only when either:

- Round 3's high-priority items (§4.1) are triaged and fixed or explicitly
  deferred with a stated reason (matching §5's pattern), and
- a Round 4 pass finds no new high-priority gaps, or the user decides
  further rounds aren't warranted.
