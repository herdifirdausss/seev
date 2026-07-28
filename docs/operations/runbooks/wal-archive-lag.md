# Runbook: WAL Archive Lag

> [Documentation home](../../README.md) · [Operations](../README.md) · [Runbooks](README.md)

> **Status: Current. Audience: database and platform operators.** This is
> an RPO-budget alert, not a data-loss alert by itself — every committed
> transaction is still safe in `pg_wal` until it archives. Investigate
> promptly; don't panic.

Triggered by: `deploy/observability/prometheus/rules/backup.yml` —
`SeevBackupWALArchiveStale` (`seev_backup_wal_archive_age_seconds > 300`
for 2 minutes; `severity: critical`). Reads only
`backup-agent`'s own fixed metric (docs/roadmap/archive/50 K13) — never a
filename, LSN, or secret path label.

`archive_timeout = 60s` (docs/roadmap/archive/50 K4) means a healthy cluster
archives at least once a minute even when completely idle — this alert
firing means archiving is genuinely stuck, not merely quiet. Every minute
this stays unresolved directly widens the real recovery point (RPO
budget: five minutes, K4/K12) — this is the alert that most directly
threatens the RPO target, unlike a stale full/differential backup, which
only affects RTO (how far back a restore has to replay from).

## Step 1 — Confirm what's actually stuck

```sh
make backup-status
docker compose exec postgres pgbackrest --stanza=seev info
```

Distinguish two different failure shapes before doing anything else:

- **Nothing has archived recently at all** (the common case) — go to
  Step 2.
- **Archiving is succeeding but the metric itself looks stale** —
  confirm `backup-agent`'s own `/ready` endpoint is reachable and its
  process hasn't crashed; a dead metrics exporter can look identical to a
  dead archiver from the outside. If `backup-agent` itself is down,
  restart it — this is an observability failure, not a WAL failure.

## Step 2 — Read the archive_command's own error

The Postgres server logs every failed `archive_command` invocation
directly:

```sh
docker compose logs postgres --since 1h | grep -i 'archive command failed\|archive_command'
```

Common root causes, in likely order:

1. **Lock-path or file permission problem.** T1's own documented bug:
   `archive_command` always runs as the `postgres` OS user; if
   pgBackRest's lock path (`/tmp/pgbackrest` by default) or the
   repository path is owned by the wrong user (e.g. created by a `root`
   `docker compose exec` before the server process ever touched it),
   every archive attempt fails silently with a permission error. Check
   ownership directly inside the container.
2. **Repository unreachable or out of space.** Same underlying cause as
   [backup failure](backup-failure.md) Step 2.2, but here it blocks
   *every* WAL segment, not just the next scheduled full/differential —
   more urgent.
3. **Wrong or rotated passphrase.** `archive_command` needs
   `PGBACKREST_REPO1_CIPHER_PASS`, exported by `deploy/backup/entrypoint.sh`
   — if the secret file changed without restarting the postgres
   container (the entrypoint exports it once, at container start), every
   subsequent archive attempt fails until the container restarts with the
   current secret.
4. **`pg_wal` filling up.** If archiving has been stuck long enough,
   `pg_wal` itself may be approaching disk capacity — check this
   directly (`docker compose exec postgres du -sh /var/lib/postgresql/data/pg_wal`)
   before assuming Step 2.1–2.3 alone explain the alert; a full disk can
   itself be the reason `archive_command` keeps failing.

## Step 3 — Fix and force a manual segment to confirm

After addressing the root cause, don't wait out `archive_timeout` to
confirm the fix — force a segment switch immediately, the same technique
`scripts/dr-drill.sh` uses to keep its own RPO measurement meaningful:

```sh
docker compose exec postgres psql -U seev -d postgres -c "SELECT pg_switch_wal();"
```

Then re-check `make backup-status` or
`seev_backup_wal_archive_age_seconds` directly — it should drop back
under the five-minute budget within a few seconds of a successful forced
switch.

## Step 4 — Confirm no window was silently lost

If the outage lasted long enough that `pg_wal` itself rotated past
un-archived segments before the fix (rare, but possible on a long stall
combined with high write volume), the archive is genuinely missing a
segment — this changes the situation from "delayed RPO" to "a real gap in
the restorable timeline." Confirm via `pgbackrest info`'s reported WAL
range against the server's actual WAL history; if a gap is confirmed,
escalate and treat any restore target inside that gap as unavailable
(the restore tooling will itself refuse a target it cannot reach — T3).

## Related

- [Backup failure](backup-failure.md) — the scheduled full/differential
  job itself, a separate concern from continuous archiving.
- [Repository corruption](repository-corruption.md) — an existing backup
  or the repository itself fails an integrity check.
- [DR restore drill](dr-restore-drill.md) — PITR target selection assumes
  a continuous, gap-free WAL archive; a confirmed gap (Step 4) narrows
  which targets are actually reachable.
- docs/roadmap/archive/50-a7-backup-pitr-disaster-recovery.md — K4 (`archive_timeout`
  policy), K12 (RPO measurement boundaries).
