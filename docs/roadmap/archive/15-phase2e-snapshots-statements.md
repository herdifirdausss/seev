# 15 — Phase 2e: Balance Snapshots and Statements (H3, H4)

Prerequisite: [14](14-phase2d-ledger-semantics-events.md) is complete. Design
decisions are [13 K6 and K7](13-p1-backlog-review.md). Execute T1 → T2.

## T1 — Daily balance snapshots (07 H3, K6)

Goal: answer historical-balance and opening-balance queries without replaying
all entries, reduce verifier scans, and provide the foundation for partitioning
and reporting.

1. Add `000005_balance_snapshots`:

   ```sql
   CREATE TABLE account_balance_snapshots (
       account_id      UUID        NOT NULL REFERENCES accounts(id),
       as_of_date      DATE        NOT NULL,
       closing_balance BIGINT      NOT NULL,
       entry_count     INT         NOT NULL,
       created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
       PRIMARY KEY (account_id, as_of_date)
   );
   ```

   Snapshots are append-only during normal operation, but may be rebuilt for a
   date during correction. They therefore do not need the immutable-entry
   trigger.
2. Add `snapshot_repository.go` with:
   - `GetLatestBefore(accountID, date)`;
   - set-based `InsertForDate(date)` that writes only accounts active that day,
     takes `closing_balance` from the last `balance_after`, and uses
     `ON CONFLICT DO NOTHING` for idempotent retries;
   - `VerifyDate(date)` to compare snapshots with current projections.
3. Schedule a 00:15 Asia/Jakarta job using the existing scheduler and lock. It
   snapshots the previous day, verifies it, emits an alert and metric on a
   mismatch, and never overwrites a correct value with a bad one. Wire it into
   `StartWorkers`.
4. On startup, catch up missing dates for at most 31 days and log a warning for
   a larger gap, directing operators to the runbook.
5. Extend `GET /accounts/{id}/balance?as_of=YYYY-MM-DD` with the latest snapshot
   plus entries after it through the requested date. This is two light queries,
   not a full replay.
6. Limit projection-audit scans to accounts active since their latest snapshot.

Tests must cover multi-day data, inactive accounts, idempotent reruns, the WIB
midnight boundary, and an intentionally corrupted snapshot that triggers an
alert. The empty-snapshot-table path must use `sql.NullTime`; PostgreSQL returns
one NULL row for `MAX(as_of_date)`, not `sql.ErrNoRows`.

### Result (2026-07-11)

Migration up/down, repository, catch-up job, API `as_of` response, four
testcontainer integration cases, and Docker smoke testing all passed. The
empty-table NULL regression was found by the real PostgreSQL smoke test and
fixed with `sql.NullTime`.

## T2 — Statements and export (07 H4, K7)

Add the public endpoint
`GET /accounts/{id}/statement?from=YYYY-MM-DD&to=YYYY-MM-DD&format=json|csv`
with `CanAccessAccount` ownership checks.

- Limit the range to 92 days and the response to 5,000 entries. Return 400 with
  `range too large, narrow the period` instead of silently truncating.
- Calculate the opening balance from T1's snapshot plus the delta to the start
  date. The closing balance is the final period entry's `balance_after`, or the
  opening balance when there are no entries.
- JSON fields: account ID, currency, date range, opening/closing balances, and
  entries with entry ID, transaction ID/type, direction, string amount,
  balance, note, and timestamp.
- Stream CSV through `encoding/csv` directly to `http.ResponseWriter`; do not
  buffer 5,000 rows in memory. Use `text/csv` and an attachment disposition.
- Expose a `Module.Statement` facade; transport must not access repositories
  directly.

Tests cover date/format validation, ownership, snapshot-based opening balance,
JSON/CSV equivalence, and the 5,000-entry limit. The completed implementation
passed unit, PostgreSQL integration, Docker smoke, build, vet, and race gates.

## Phase 2e verification

```bash
go build ./... && make test && go test -tags=integration -race ./...
```
