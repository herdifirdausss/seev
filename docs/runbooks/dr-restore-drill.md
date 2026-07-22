# Runbook: Disaster Recovery Restore Drill

> **Status: Current. Audience: database and service operators.** This is a
> controlled restore drill; it is not authorization to overwrite production.

Covers restoring the ledger database from a backup and proving it's usable again — restore, rebuild the balance projection, verify integrity, smoke test. Run this **periodically against staging** (not production) to keep the procedure and its RTO honest. Every drill's timing goes in the table at the bottom of this file — this is the whole point of drilling: an untimed, never-executed runbook is a guess, not a plan.

> **Scope note (docs/plan/50 Track A7):** for a real incident affecting the
> shared Postgres cluster, use
> [**Cluster-wide restore (scripts/restore-cluster.sh)**](#cluster-wide-restore-scriptsrestore-clustersh)
> below first — it restores all eight authoritative service databases
> together via pgBackRest/WAL PITR, not just the ledger schema. The
> procedure on this page (below the cluster-wide section) predates that
> tooling and stays useful for a narrower case: a **ledger projection-only
> drift** on an otherwise-healthy cluster — `account_balances` disagreeing
> with `ledger_entries` without any underlying data loss. Rebuilding the
> projection is a **repair tool for that specific, narrower problem, never
> a substitute for point-in-time recovery** when the underlying data
> itself is what's wrong or missing.

## Cluster-wide restore (scripts/restore-cluster.sh)

Restores all eight authoritative service databases (`seev_ledger`,
`seev_auth`, `seev_payin`, `seev_payout`, `seev_fraud`, `seev_gateway`,
`seev_adminbff`, `seev_assurance`) as one physical unit from the encrypted
pgBackRest repository (Track A7 K1/K2), to the latest available point or a
specific point in time / LSN.

```sh
scripts/restore-cluster.sh latest
scripts/restore-cluster.sh time "2026-07-22 15:00:00+07"
scripts/restore-cluster.sh lsn "0/7000060"
```

What it does (K7 — always fail-closed, always isolated):

- Restores into its own dedicated `seev-a7-drill` Compose project
  (`deploy/backup/restore-compose.yml`) — refuses to run against the
  default/unset project name or `seev`, and never touches the real
  `seev_postgres_data` volume.
- Refuses an already-populated target volume unless `FORCE_REUSE_VOLUME=1`
  is set explicitly.
- Mounts the backup repository read-only throughout — the restored
  instance never writes a WAL segment back into the repository its own
  data came from (`archive_mode` stays off).
- Waits for PostgreSQL to actually reach the target and promote
  (`pg_is_in_recovery()` → false) before doing anything else; a target
  that cannot be reached (before the earliest backup, or past the latest
  archived WAL) makes PostgreSQL itself refuse to promote, and the script
  exits non-zero without ever starting an application.
- Inventories every service's database/migration version **before** any
  migration command is allowed to run against the restored data (K8 — a
  dirty migration table is a fatal recovery failure, never silently
  migrated past).
- Writes a stage-timing JSON report (`STAGE_REPORT_PATH`, default
  `/tmp/seev-a7-drill-stages.json`) — the timestamps K12's RPO/RTO
  boundaries are computed from.

Tear down explicitly when done — this always names the project and prints
the exact volumes before removing anything, never an unscoped
`docker compose down -v`:

```sh
scripts/restore-cluster.sh cleanup
```

**What this does NOT yet do** (later Track A7 tasks, not built as of this
runbook's last update): offline cross-database integrity verification
(`cmd/drverify`, T4), Redis/RabbitMQ ephemeral-state reseed and the
post-restore session/token revocation fence (`cmd/drreseed`, T5), and a
scripted end-to-end game-day drill with RPO/RTO reporting
(`scripts/dr-drill.sh`, T6). Until those land, treat a
`restore-cluster.sh` run as producing an **inventoried but unverified**
restored cluster — inspect it manually (the drill project is left running
specifically for this) before drawing any conclusion about data
integrity, and never point real traffic at it.

## What must be restored, and from where

Not every kind of state is recovered the same way. Classify what you are
looking at before choosing a recovery action:

| State class | Examples | Recovery source | Notes |
|---|---|---|---|
| **PostgreSQL — authoritative** | `seev_ledger`, `seev_auth`, `seev_payin`, `seev_payout`, `seev_fraud`, `seev_gateway`, `seev_adminbff`, `seev_assurance` (all 8 service databases, one shared cluster) | Physical cluster backup + WAL (Track A7); `pg_dump`/projection rebuild for the ledger-only minimal case covered by this runbook | The only state that is ever wrong to silently regenerate — restore it, don't reconstruct it from inference. |
| **Redis — reconstructable/ephemeral** | Rate-limit buckets, policy counters, fraud velocity keys, scheduler locks, circuit-breaker state | Rebuilt from PostgreSQL after a restore (Track A7 `cmd/drreseed`); safe to start empty otherwise | Never a backup target itself. A cold Redis after restore is expected, not a failure — it must be reseeded from durable evidence before production traffic resumes, not left to warm up silently. |
| **RabbitMQ — delivery-only** | Outbox event deliveries, durable payout vendor commands in flight | Topology (exchanges/queues/bindings) is recreated from service startup code; in-flight-only deliveries are not backed up and must be treated as potentially lost | The event's *fact* lives in PostgreSQL (the outbox row); the broker is a delivery mechanism, not a source of truth. A lost in-flight delivery is recovered by replaying from the durable outbox row, never by trying to back up the broker. |
| **Vault / certificates — external** | Vault dev-mode secrets, mTLS leaf certificates and the local mini-CA | Re-seeded (`scripts/vault-seed.sh`) / re-issued (`make certs`) from current external configuration, never from a database backup | Runtime secrets and identity material must come from the environment at restore time, not resurrected from backup data — see `docs/security/threat-model.md` for why. |

## Ledger-only projection repair (narrower case — see the scope note above)

Use this section when the cluster's underlying data is intact but the
ledger's `account_balances` projection has drifted from `ledger_entries`
(e.g. after a restore mechanism that doesn't guarantee point-in-time
consistency across tables, or a suspected projection bug). If the
underlying data itself is what's wrong or missing, use
[cluster-wide restore](#cluster-wide-restore-scriptsrestore-clustersh)
above instead — this procedure cannot recover data that isn't there.

### When to run this

- Scheduled: quarterly, or whenever the backup/restore tooling changes.
- Ad-hoc: after a real incident, to re-validate the procedure while it's fresh.
- Whenever `scripts/rebuild-projection.sh` or its SQL (`scripts/sql/rebuild_projection.sql`) changes — a drill is the only thing that proves the rebuild logic actually works against a realistic, restored dataset instead of a synthetic test fixture.

### Prerequisites

- A recent backup (`pg_dump`/`pg_basebackup`, or a volume/disk snapshot — whichever this environment actually uses in production; this runbook is mechanism-agnostic on purpose).
- A target Postgres instance to restore into — **never production**. Staging, or a fresh throwaway instance.
- `migrate` CLI, `psql`, and this repo checked out at the commit the backup corresponds to (migrations must match).

### Procedure

1. **Stop the clock. Note the start time.** RTO is measured from "we decided to restore" to "the app is serving traffic again with verified-clean data" — not from when the backup finished.
2. **Provision the target Postgres instance** (or wipe the drill target if reusing one).
3. **Restore the backup** into the target instance. Record how long this step alone took — it's usually the single largest RTO component and the one most worth knowing in advance.
4. **Apply migrations**: `make migrate-up` (reads `POSTGRES_MIGRATE_*` env vars pointed at the target — idempotent, `golang-migrate` tracks the applied version, so this is safe even if the backup already includes some migrations).
5. **Rebuild the balance projection**: `POSTGRES_HOST=<target> POSTGRES_PORT=<target> POSTGRES_USER=seev_app POSTGRES_PASSWORD=<...> POSTGRES_DB=seev ./scripts/rebuild-projection.sh`. This is defensive, not optional — a backup taken mid-write or restored via a mechanism that doesn't guarantee point-in-time consistency across tables could leave `account_balances` slightly stale relative to `ledger_entries`; the rebuild makes the projection correct by construction regardless. The script refuses to run if it can reach a live `/health` endpoint at the target — confirm nothing is pointed at the target yet.
6. **Run the full verifier**: connect to the target and run `SELECT * FROM fn_verify_ledger_balance('-infinity', 'infinity');` and `SELECT * FROM v_account_balance_audit WHERE is_consistent = false;` — both must return zero rows. If either doesn't, STOP — do not proceed to serving traffic; treat this as a real integrity incident (see [ledger-integrity-alert.md](ledger-integrity-alert.md)), not a drill artifact to ignore.
7. **Point a server instance at the target** and start it.
8. **Smoke test**: at minimum, `GET /health`, provision a throwaway user, post a `money_in`, confirm the balance. This proves the restored+rebuilt database is actually servable, not just internally consistent.
9. **Stop the clock. Record the total elapsed time as this drill's RTO.**
10. **Tear down the drill target** (unless it's a permanent staging environment) — this is throwaway infrastructure, not something to leave running.

### RTO log

Fill in a new row every time this drill runs for real — not from planning documents, from an actual clock. Keep entries even when a drill reveals a problem; a slow or failed drill is exactly the information this table exists to preserve.

| Date | Environment | Backup restore | Migrate | Rebuild projection | Verifier | Smoke test | **Total RTO** | Notes |
|---|---|---|---|---|---|---|---|---|
| 2026-07-12 | Local Docker (dev stack, docker-compose, Postgres remapped to host port 5433 — native Postgres already held 5432 on the drill machine) | <1s (`pg_restore --no-owner`, 13 accounts / 1 transaction, 46KB dump) | 0s (`migrate up` reported "no change" — dump already included `schema_migrations` at version 9) | <1s per run; both `UPDATE` statements affected 0 rows (restore was already consistent) | 0 rows, both `fn_verify_ledger_balance()` and the per-account consistency check | `GET /health` ok, pre-incident balance (77,000) intact, new `money_in` posted successfully post-restore | **~171s wall-clock, dominated by debugging, not mechanism** | First real drill — tiny dataset, so per-step timings are a floor, not a production estimate. More important than the total: **this drill caught a real bug in `scripts/rebuild-projection.sh`** on its first run — the script passed `-f <path>` to `psql` invoked via `docker exec`, but that path is resolved inside the *container's* filesystem, not the host's, so the docker-exec code path could never find the SQL file. Fixed by piping the file over stdin (`docker exec -i ... psql ... < file`) instead. This is exactly why this runbook exists: an untested procedure looks correct until the day it's actually needed. Re-run after the fix succeeded cleanly. A staging drill against production-scale data is still needed for a meaningful RTO number. |

## Related

- [scripts/rebuild-projection.sh](../../scripts/rebuild-projection.sh) / [scripts/sql/rebuild_projection.sql](../../scripts/sql/rebuild_projection.sql) — the rebuild this runbook invokes; same SQL file the Go integration test (`TestSchemaContract_RebuildProjection`) proves against corrupted data.
- [ledger-integrity-alert.md](ledger-integrity-alert.md) — what to do if the verifier finds a real discrepancy, during a drill or otherwise.
- [docs/plan/17-phase3a-policy-recovery.md](../plan/17-phase3a-policy-recovery.md) Task T2 — design decisions (UPDATE not TRUNCATE, why `allow_negative` must survive).
