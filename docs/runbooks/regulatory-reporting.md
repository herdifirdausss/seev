# Runbook: Regulatory/Compliance Reporting

Covers the reporting foundation built in docs/plan/20 Task T2 (decision K-S S7): fund position, transaction mutation, and reconciliation summary — read-only, pulled on demand by finance/compliance. This is a **foundation**, not a final BI/OJK-specific report format; region-specific formats are future views/endpoints layered on top of the same three views, once the legal entity and its specific reporting obligations are known.

## Two ways to read the same data

Both paths read the exact same three views (`v_report_daily_position`, `v_report_daily_mutation`, `v_report_recon_summary`, `migrations/000018_reporting_views.up.sql`) — there is only one query contract, reviewed once, not one per consumer.

### 1. The app's own endpoint (ad-hoc, human-triggered)

```
GET /admin/reports/position?from=2026-07-01&to=2026-07-31&format=json
GET /admin/reports/mutation?from=2026-07-01&to=2026-07-31&format=csv
GET /admin/reports/recon?from=2026-07-01&to=2026-07-31&format=csv
```

Internal-only listener, admin-gated (`isAdmin`, same as every other `/admin/*` endpoint). `format=csv` streams row-by-row (never fully buffered — same pattern as the statement export, docs/plan/15 Task T2). Date range capped at 366 days per request (`maxReportDays`, `internal/ledger/transport/http.go`) — a box-size guard, not a business rule; pull month-by-month for a multi-year export.

Queries here run through the normal `app_service` application connection pool — this path is for people using the app's own admin surface, not for a BI tool.

### 2. Direct `app_readonly` connection (external BI tool)

For a BI/reporting tool that wants to connect straight to Postgres rather than going through the HTTP API:

```
Host:     <same host as the app's Postgres>
Database: seev (or your deployment's POSTGRES_DB)
Role:     a LOGIN role granted `app_readonly` (see migrations/000009_rls_roles.up.sql —
          app_readonly itself has no LOGIN/password; provision a dedicated login role and
          GRANT app_readonly TO it, same pattern as seev_app for app_service)
SSL:      required in any non-local environment
```

**What this role CAN read**: the three report views above, plus every table they're built from (`accounts`, `account_balances`, `ledger_transactions`, `ledger_entries`, `account_balance_snapshots`, `recon_batches`, `recon_items`) — see the full grant list in `migrations/000009_rls_roles.up.sql`.

**What this role CANNOT read, and why**:
- `outbox_events` — carries the raw AMQP event `payload` column (internal wire format, not meant for external consumption; also would let a reporting tool see events before/regardless of whether they've actually been published).
- `pending_adjustments` — carries `cmd_payload`, an internal command shape (`processors.Command` JSON), not a reporting-shaped column.

Both RLS `FORCE` and the absence of a table-level `GRANT` block these — a query against them returns a permission-denied error, not empty rows (confirmed in `TestSchemaContract_Reporting_AppReadonlyBlockedFromPayloadTables`, docs/plan/20 Task T2 integration test).

**Note**: `scheduled_transactions` also carries a `cmd_payload` column of the same shape, but — unlike the two tables above — `app_readonly` already has direct `SELECT` on it (`migrations/000014_scheduled_transactions.up.sql`, docs/plan/19 Task T1, predating this task). This task did not change that grant either way; flagged here so it isn't mistaken for an oversight of the reporting views themselves.

None of the three report views themselves expose `payload`/`cmd_payload` from any table — reviewed column-by-column when the views were written (`migrations/000018_reporting_views.up.sql`'s own header comment records this review).

## Recommended schedule

Pull reports **after the daily 00:15 WIB snapshot job** (docs/plan/15 Task T1) has run — `v_report_daily_position` is built directly from `account_balance_snapshots`, so a report pulled before that day's snapshot job reflects the PREVIOUS day's position for accounts with no snapshot yet for the current date. `v_report_daily_mutation` and `v_report_recon_summary` don't depend on the snapshot job and are always current as of the query time.

## Timezone note

`v_report_daily_mutation.report_date` is computed as `(ledger_transactions.created_at AT TIME ZONE 'Asia/Jakarta')::date` — a transaction posted at 00:30 WIB (17:30 UTC the previous day) lands on the correct WIB calendar date, not the UTC one. This is the same `::date` vs `::timestamptz::date` lesson from docs/plan/16 Task T2 — do not write a new ad-hoc query against `ledger_transactions` that casts `created_at::date` directly; it truncates in the querying session's timezone, which is not guaranteed to be Asia/Jakarta.

## No scheduled delivery

There is no job or scheduler that pushes these reports anywhere — they are pulled on demand, either via the HTTP endpoint or a direct `app_readonly` connection. If a future requirement needs reports delivered on a schedule (e.g. emailed nightly to a compliance mailbox), that is a consumer of one of the two paths above (a cron job hitting the HTTP endpoint, or a BI tool's own scheduler), not a new feature inside the ledger module itself.

## Related

- [docs/plan/20-phase3d-aml-reporting.md](../plan/20-phase3d-aml-reporting.md) Task T2 — full design and decisions (K-S S7).
- [docs/plan/16-phase2f-governance-recon-rls.md](../plan/16-phase2f-governance-recon-rls.md) Task T3 — the `app_service`/`app_readonly` role split this runbook builds on.
