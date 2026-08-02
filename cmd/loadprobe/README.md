# loadprobe

`loadprobe` reads only PostgreSQL monitoring views in a disposable load
database and emits redacted JSONL samples. It records query IDs, not query
text or parameters. Example:

```bash
go run ./cmd/loadprobe \
  -dsn 'host=127.0.0.1 port=15433 user=seev dbname=seev_load_ledger sslmode=disable' \
  -interval 100ms -duration 60s -out artifacts/load/<run>/postgres.jsonl
```

## Choosing `-interval`

`scripts/load-test.sh`'s `start_postgres_probe` defaults to `1s`
(`SEEV_LOAD_PROBE_INTERVAL` overrides it) — fine for a multi-minute steady
window or a 60-minute soak, where four lightweight catalog/stat queries per
sample is negligible overhead. A short, high-intensity run (e.g. the
disbursement-burst scenario, which completes in a few seconds) needs a much
finer interval or a 1s cadence produces only a handful of samples for the
whole run — set `SEEV_LOAD_PROBE_INTERVAL=100ms` (or lower) explicitly for
those. `TestCollect_FastEnoughForSubSecondPolling` (`main_test.go`) verifies
`collect()` itself comfortably fits inside a 100ms budget (observed:
low-single-digit milliseconds mean, under 10ms worst case), so the collector
is not what would limit how fine an interval you can actually use.

## Reading `lock_wait_relations`

A `relation: "unknown"` entry is not a collector failure — it means the
blocked session is waiting on a `transactionid` lock, not a relation lock.
This is exactly what PostgreSQL reports for a row-level UPDATE/UPDATE
conflict (session B tries to update a row session A already modified but
hasn't committed): B blocks on A's *transaction* finishing, not on the
table itself, so `pg_locks.relation` is NULL for that wait. For a
hot-account balance UPDATE (the contention shape B1's own experiments
target, `docs/performance/reports/2026-07-31-baseline.md` §16.3/§16.4),
`unknown`/`ShareLock` rows are the expected, correct signal — treat them
the same as a named-relation row when checking for lock concentration, not
as noise to filter out.
