# 20 — Phase 3d: Screening and Regulatory Reporting (S7, S8)

Prerequisite: the policy, hardening, recovery, and snapshot work in plans 10–16 is complete. This phase adds pre-posting screening and read-only reporting foundations.

## T1 — Screening hook and AML-style rules (S8)

### Objective

Allow compliance rules to observe a proposed posting before ledger entries are created. A rule may monitor or block a transaction, while infrastructure failures remain fail-open so a screening outage does not silently become a money-movement outage.

### Locked design

- Run the hook after business validation and before entries are built.
- `SCREENING_MODE` supports `off`, `monitor`, and `block`; `off` is the default and preserves the previous posting path.
- A blocked transaction returns `ErrScreeningBlocked` and HTTP 422.
- A hook infrastructure error is logged and metered, then posting continues.
- Screening events are written outside the posting transaction as best-effort audit records. The failed ledger transaction remains the authoritative result when a transaction is blocked.
- The initial rules are an amount threshold and hourly velocity. Screening counters use separate keys from policy counters.

### Implementation

1. Add the `PrePostHook` interface and wire an optional hook list into the ledger service. The hook runs only after all business validation succeeds.

2. Add `internal/ledger/screening` with:

   - `AmountThresholdRule`;
   - `VelocityAnomalyRule`;
   - mode parsing and rule-level metrics.

3. Add migration `000017_screening.up.sql` and its down migration for `screening_events`. Grant `app_service` only the required select/insert permissions and add separate RLS policies for those operations.

4. Add the admin-gated endpoint `GET /admin/screening/events?user_id=&verdict=&limit=&offset=`.

5. Read `SCREENING_MODE`, `SCREENING_AMOUNT_THRESHOLD`, and `SCREENING_VELOCITY_MAX_PER_HOUR` at startup. When mode is `off`, do not construct screening rules or counters at all.

### Required tests

- Unit tests for threshold and velocity rules in both monitor and block modes.
- Tests proving that event persistence is best-effort and that counter errors reach the fail-open pipeline path.
- Integration tests for blocked and monitored posts, including transaction status, screening event, balance, and ledger-verifier assertions.
- An ordering test proving that screening is not called when ordinary business validation has already rejected the request.
- A chaos run with the screening hook enabled.

### Definition of done

- [x] `SCREENING_MODE=off` is the default and keeps the previous behavior.
- [x] Blocking creates both a failed transaction record and a screening-event record.
- [x] Migration up/down verification passes, including grants and RLS.

### Result

The hook interface, pipeline wiring, rules, persistence, admin endpoint, configuration, and metrics were implemented. The hook runs at the intended point in the pipeline; a block commits a failed transaction with an auditable reason, while hook errors log and allow the posting to continue.

Unit tests cover thresholds, velocity, modes, persistence failures, and counter failures. PostgreSQL integration tests cover blocked and monitored transactions and confirm that failed business validation does not invoke the hook. Migration 000017 was tested through up/down/up cycles.

The chaos scenario was rerun with screening enabled in monitor mode. The pipeline remained balanced, no transaction was left pending, and the full build, vet, unit, integration, and race suites passed.

## T2 — Regulatory reporting (S7)

### Objective

Provide read-only reporting for compliance and finance without adding writes to the transaction path or exposing raw sensitive payloads. Final BI/OJK formats can be added later once the legal entity and reporting contract are fixed.

### Locked design

Reporting is built on snapshots and reconciliation data and is accessible through the `app_readonly` role. The feature is read-only; it must not add a new write path.

### Implementation

1. Add migration `000018_reporting_views.up.sql` and its down migration with three reviewed query contracts:

   - `v_report_daily_position`: daily position by date, currency, account type, and owner type;
   - `v_report_daily_mutation`: posted transaction counts and amounts by WIB date, transaction type, and currency;
   - `v_report_recon_summary`: reconciliation status counts and resolved-item totals per batch.

   Use `(created_at AT TIME ZONE 'Asia/Jakarta')::date` explicitly for transaction dates. Grant `SELECT` on the views to `app_readonly` and `app_service`. The views must not expose outbox or pending-adjustment payloads.

2. Add admin-gated endpoints:

   ```text
   GET /admin/reports/position?from=&to=&format=csv|json
   GET /admin/reports/mutation?from=&to=&format=csv|json
   GET /admin/reports/recon?from=&to=&format=csv|json
   ```

   CSV output streams directly to the response, and date ranges are capped at 366 days.

3. Add [the regulatory-reporting runbook](../../operations/runbooks/regulatory-reporting.md), documenting both the application endpoint and direct `app_readonly` access, available grants, protected tables, and the recommended schedule after the 00:15 snapshot.

4. Do not add a scheduler. Reports are on-demand; any future scheduled delivery should consume these views or endpoints.

### Required tests

- Integration tests comparing all three views with manual aggregates from real posting, snapshot, and reconciliation data.
- Role tests proving that `app_readonly` can select the views but cannot read protected raw payload tables.
- A timezone regression test for a transaction at 00:30 WIB.
- Smoke tests for JSON and CSV output, headers, permissions, and invalid report types.

### Definition of done

- [x] The task adds no `INSERT`, `UPDATE`, or `DELETE` statement to the reporting path.
- [x] The views do not expose `cmd_payload` or outbox payload data.
- [x] Migration up/down verification and the runbook are complete.

### Result

The three views, reporting repository, DTOs, admin endpoints, streaming CSV helpers, and runbook were implemented. Integration tests compare each view with a manual aggregate, verify the `app_readonly` role, and protect the Asia/Jakarta date conversion.

The implementation also documented an existing grant on `scheduled_transactions` instead of incorrectly treating that table as protected by this task. No scheduler or write operation was added.

JSON and CSV smoke tests passed for all report types, including permission and invalid-route checks. Migration 000018 passed up/down/up verification. Full build, vet, unit, integration, race, and chaos verification passed; an intermittent policy cache test was confirmed to be load-sensitive and passed when run in isolation.

## Final verification

```bash
go build ./...
go vet ./...
go vet -tags=integration ./...
make test
go test -tags=integration -race ./...
./scripts/chaos-test.sh all
```

Also run the screening and reporting smoke tests and verify migration 000017–000018 up/down/up cycles.
