# Runbook: Backup Failure

> [Documentation home](../../README.md) · [Operations](../README.md) · [Runbooks](README.md)

> **Status: Current. Audience: database and platform operators.** A stale
> backup is not an emergency by itself — treat it as an urgent maintenance
> item, not a reason to touch the live database.

Triggered by: `deploy/observability/prometheus/rules/backup.yml` —
`SeevBackupFullStale` (no successful full backup in over eight days;
`severity: warning`) or `SeevBackupDiffStale` (no successful backup, full
or differential, in over thirty hours; `severity: warning`). Both read
only `backup-agent`'s own `seev_backup_last_success_timestamp_seconds{type}`
metric (docs/roadmap/archive/50 K13) — no filename, LSN, backup ID, or
secret path is ever a label.

Distinct from [WAL archive lag](wal-archive-lag.md) (RPO risk on an
otherwise-healthy backup chain) and
[repository corruption](repository-corruption.md) (an existing backup
that fails its own checksum) — this runbook is for the scheduled
full/differential **job itself** not running or not succeeding.

## Step 1 — Confirm the scope

```sh
make backup-status
```

Compares current WAL position against the archive and reports the
oldest/latest restorable point (T2). Also check the backup-agent's own
health directly:

```sh
curl -k --cert <operator-cert> --key <operator-key> \
  https://localhost:18097/ready
```

`503` with `has_valid_full_backup: false` means there has never been a
successful full backup at all — more urgent than a merely stale one.
`200` with an old `seev_backup_last_success_timestamp_seconds` confirms
the scheduled job is the thing that's broken, not backup-agent's own
liveness.

## Step 2 — Read the agent's own logs

`backup-agent` emits structured JSON logs for every run, including a
failed one (docs/roadmap/archive/50 T2, Work item 1 — "bounded execution
time... JSON logs"). Find the most recent failed attempt and read its
error verbatim before guessing:

```sh
docker compose logs backup-agent --since 48h | grep -i '"result":"error"\|"level":"error"'
```

Common root causes, in likely order:

1. **Wrong or rotated passphrase.** pgBackRest fails closed with a clear
   decrypt error (`unable to load info file ... FormatError`) rather than
   silently producing a bad backup — T1's own documented fail-closed
   behavior. Confirm `deploy/backup/secrets/pgbackrest_repo_passphrase`
   matches the passphrase the existing repository was created with; a
   locally regenerated passphrase cannot decrypt an existing repository.
2. **Repository path unwritable or out of disk space.** Check the host
   path backing `BACKUP_REPO_PATH` (or the volume it's mounted from) for
   free space and correct ownership.
3. **`seev_backup` role or grants changed.** The role needs `LOGIN
   REPLICATION`, per-database `CONNECT`, and `SELECT` on each
   `schema_migrations_<service>` table plus `pg_read_all_settings` and
   `EXECUTE` on the backup-control functions (T1 K5). If these grants
   were altered outside `scripts/postgres-init/04-backup-role.sh`, re-run
   `make backup-role-bootstrap` (idempotent — safe to re-run).
4. **A previous run is still holding the overlap lock.** `internal/platform/scheduling`
   rejects a concurrent run rather than corrupting a chain — if a prior
   invocation genuinely hung, confirm no `pgbackrest` process is actually
   running before assuming this is the cause.
5. **The job timed out.** `scheduler.WithJobTimeout` bounds full (1h) and
   differential (20m) runs (T2) — a database that has grown meaningfully
   larger than the reference fixture may need this timeout raised; do not
   simply retry indefinitely without addressing the underlying duration.

## Step 3 — Retry manually, in the foreground

Use the same code path the scheduler uses (T2's explicit design goal —
"manual and scheduled paths use the same implementation"), so a manual
retry is a faithful test of the real failure, not a different mechanism:

```sh
make backup-full    # or: make backup-diff
make backup-check   # must be clean before trusting the result
```

Watch the command's own stdout/stderr directly rather than only the
agent's async logs — a foreground run surfaces the exact pgBackRest error
immediately.

## Step 4 — Confirm the existing chain is still safe

A failed *new* backup must never have touched the *previous* valid
chain (T2, proven live: a failed differential attempt left both prior
backups fully listed and restorable). Confirm this directly rather than
assuming it:

```sh
docker compose exec postgres pgbackrest --stanza=seev info --output=json | \
  python3 -c 'import json,sys; d=json.load(sys.stdin)[0]; print([{"label": b["label"], "type": b["type"]} for b in d["backup"]])'
```

If the previous chain is intact, this is urgent maintenance, not an
active data-loss incident — proceed to Step 5. If the previous chain is
*also* missing or unreadable, treat this as
[repository corruption](repository-corruption.md) instead and escalate
immediately.

## Step 5 — Fix and verify the schedule resumes

After the manual backup in Step 3 succeeds, confirm the metric clears
(`seev_backup_last_success_timestamp_seconds` advances) and that the
*next* scheduled run also succeeds — a one-off manual fix that doesn't
address the root cause (e.g. a disk that's still nearly full) will only
page again on the next scheduled window.

## Related

- [WAL archive lag](wal-archive-lag.md) — the backup chain itself is
  fine, but continuous archiving has stalled.
- [Repository corruption](repository-corruption.md) — an existing backup
  fails its own integrity check.
- [DR restore drill](dr-restore-drill.md) — the full recovery procedure
  this backup chain exists to support.
- docs/roadmap/archive/50-a7-backup-pitr-disaster-recovery.md — K4 (backup/WAL
  policy), K5 (least-privilege backup identity), K13 (observable backup
  status).
