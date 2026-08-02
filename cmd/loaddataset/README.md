# loaddataset

`loaddataset` is a read-only observer (same posture as `cmd/loadprobe`) that
summarizes the REAL dataset a load-test run's self-seeding
(`tests/load/lib/seed.js`, via real service APIs) produced in the disposable
ledger database — closing the gap
`docs/performance/reports/2026-xx-baseline.md` §24.1 names: "a
machine-readable seed manifest with counts, random seed, schema version, and
content hash."

```bash
go run ./cmd/loaddataset \
  -dsn "$SEEV_LOAD_OBSERVER_DSN" \
  -run-id "$SEEV_LOAD_RUN_ID" \
  -tier D1 \
  -out artifacts/load/<run>/dataset-manifest.json
```

`scripts/load-test.sh`'s `run`/`smoke` commands call this automatically
after every run, writing `dataset-manifest.json` alongside the run's other
evidence. `-tier` is optional (`SEEV_LOAD_DATASET_TIER` env var when invoked
through the script) — omit it to get a report with no conformance check;
pass `D0`, `D1`, or `D2` to check the resulting `user_account_count`/
`ledger_entry_count` against that tier's declared bounds (§4.2) and exit
non-zero on a mismatch.

## What this is NOT

- **Not `cmd/loadseed`** — that tool emits deterministic *synthetic* JSONL
  rows (never written to a database) with its own seed/manifest. This tool
  reports on the REAL rows `tests/load/lib/seed.js` created via real service
  APIs, an entirely separate path.
- **Not `scripts/load-snapshot.sh`** — that hashes the raw `pg_dump` bytes
  for snapshot/restore integrity. This tool computes a *semantic* content
  hash over account/entry counts and balances, meaningful even when the raw
  dump bytes differ (e.g. different `created_at` timestamps) but the
  dataset's logical shape is identical.
- **Not a pre-load snapshot.** k6's `setup()` seeds as the first step of the
  same `compose run` invocation that then runs the load itself — there is no
  separate hook this tool (or the harness script) can observe between
  "seeded" and "load started." The manifest reflects POST-RUN state: account
  counts are stable across most scenarios (seeding doesn't create new
  accounts mid-run), but `ledger_entry_count` includes every transaction the
  run itself posted, not just the seed.

## `content_hash`

A sha256 over `schema_version`, account counts, transaction/entry counts,
and balances-by-currency — deliberately excludes `generated_at`/`run_id` so
two runs that produced logically identical datasets hash identically
regardless of when or under which run id they were captured.
