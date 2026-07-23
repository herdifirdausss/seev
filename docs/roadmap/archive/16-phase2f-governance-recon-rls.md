# 16 — Phase 2f: Adjustment Governance, Reconciliation, and RLS (H5, H2, H8)

Prerequisite: [14](14-phase2d-ledger-semantics-events.md) and
[15](15-phase2e-snapshots-statements.md) are complete. Design decisions are
[13 K5, K8, and K9](13-p1-backlog-review.md). Execute T1 → T2 → T3: reconciliation
uses maker-checker, and RLS must cover all final tables.

## T1 — Maker-checker adjustments (07 H5, K8)

Add `000006_pending_adjustments`:

```sql
CREATE TABLE pending_adjustments (
    id             UUID PRIMARY KEY,
    requested_by   TEXT NOT NULL,
    approved_by    TEXT NULL,
    cmd_payload    JSONB NOT NULL,
    reason         TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending','approved','rejected','executed','failed')),
    executed_tx_id UUID NULL REFERENCES ledger_transactions(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at     TIMESTAMPTZ NULL,
    CHECK (approved_by IS NULL OR approved_by <> requested_by)
);
```

The database constraint is mandatory; self-approval must not rely only on Go
validation. Add a small adjustments service with Create, Approve, Reject, and
List. Approve uses a conditional `pending → approved` update, posts with the
deterministic key `adj:<pending_id>`, then marks the row executed with the
transaction ID. A posting failure becomes `failed`; it does not silently
return to pending.

Expose internal admin routes for create, approve, reject, and list. Remove
direct adjustment posting from the internal transaction endpoint; adjustments
must use the pending flow. Keep freeze/chargeback direct but require a reason.
Emit `ledger.adjustment.decided.v1` with both identities and the decision.

Tests cover self-approval, concurrent approvals (exactly one wins), idempotent
approval retries, database-level constraints, and the full create → approve →
balance flow. The completed implementation passed all build, unit, integration,
and Docker smoke gates.

## T2 — External reconciliation (07 H2, N2, K5)

1. Add `external_ref` and `gateway` to `ledger_transactions` with a partial
   index. Populate them from validated metadata; never silently truncate
   `external_ref`.
2. Add `recon_batches` and `recon_items`, including the four match statuses and
   an optional `resolved_by_adjustment_id`.
3. Add admin-gated internal CSV import with streaming parsing, integral minor
   units, and a 50,000-row cap.
4. Run a synchronous set-based matcher after import: match on gateway and
   external reference, classify amount mismatches, and add missing external
   rows from ledger data. Do not add a worker for this small-box path.
5. Add a paginated batch report.
6. Resolve an item by creating a pending suspense adjustment. Never move money
   automatically; approval by a second identity is required.
7. Add `docs/operations/runbooks/reconciliation.md` with status meanings, investigation
   ownership, false-positive settlement lag, and escalation guidance.

Tests cover all four statuses, malformed CSV, decimal amounts, the row cap, and
the resolve → approve suspense flow. Integration testing found and fixed a
PostgreSQL timezone cast bug in the matcher; smoke testing found and fixed the
JSON middleware's rejection of `multipart/form-data`. The completed flow and
all verification gates passed.

## T3 — RLS and database roles (07 H8, K9)

Port the legacy RLS design into `000009_rls_roles`, including every final table:
`outbox_events`, snapshots, reconciliation tables, and pending adjustments.

- `app_service` receives explicit SELECT/INSERT grants and UPDATE only on the
  tables updated by the application. It must never update or delete
  `ledger_entries` or snapshots.
- `app_readonly` can SELECT ordinary data but not outbox or pending command
  payloads.
- Enable and force RLS everywhere with `USING (true)` policies for now. This is
  defense in depth, not tenant isolation.
- Separate migration credentials (schema owner) from application credentials
  (`seev_app` with `app_service`). Use the same restricted role in development,
  CI, and production so grant mistakes are found before deployment.

Required tests run the full integration suite as `app_service`, prove that
`ledger_entries` cannot be updated, prove that `app_readonly` cannot write or
read restricted tables, and rerun the chaos suite with the restricted role.

### Result

The migration, role separation, grant matrix, negative permission tests, full
chaos run, and up/down cycle completed successfully. The application and CI now
use `seev_app` rather than the schema owner.

## Phase 2f verification

```bash
go build ./... && make test && go test -tags=integration -race ./...
./scripts/chaos-test.sh all
```

Finish with manual maker-checker and reconciliation curl flows against Docker.
After this phase, H1–H8 are complete; the S-track follows the decisions in
[13 K-S](13-p1-backlog-review.md).
