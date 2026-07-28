# Runbook: Backup Repository Corruption

> [Documentation home](../../README.md) · [Operations](../README.md) · [Runbooks](README.md)

> **Status: Current. Audience: database and platform operators.** Treat
> every finding here as potentially data-loss-relevant until proven
> otherwise — a corrupted backup is only discovered to be a problem at
> the exact moment it's needed, unless it's checked proactively.

Triggered by: `deploy/observability/prometheus/rules/backup.yml` —
`SeevBackupRepositoryCheckFailing`
(`increase(seev_backup_repository_check_total{result="error"}[1h]) > 0`;
`severity: critical`). The `check` step runs automatically after every
successful backup (docs/roadmap/archive/50 K4 — "expire old backup/WAL data
only after the new backup and repository check succeed"), so this alert
means pgBackRest's own checksum/consistency validation found a real
problem, not a transient network blip.

This is the most severe of the three backup-related alerts: it means the
repository itself — not just the schedule (see
[backup failure](backup-failure.md)) or the archive stream (see
[WAL archive lag](wal-archive-lag.md)) — may not be trustworthy for a
future restore.

## Step 1 — Do not expire anything

K4's own ordering exists specifically to prevent this scenario: expiry
only ever removes old data after a *new* backup and check succeed. Do
**not** manually run `make backup-expire` while investigating a check
failure — a corrupted repository with two retained chains is recoverable
if one chain is still good; a corrupted repository with one chain
deliberately expired down to is not.

## Step 2 — Read the actual check error

```sh
docker compose exec postgres pgbackrest --stanza=seev check
```

Run it directly rather than only reading the metric — the exact
pgBackRest error code and message determine what's actually wrong:

- **`[028]` / checksum mismatch on a specific file** — a real integrity
  problem with that backup's stored data (disk corruption, an
  interrupted write during backup, or interference with the repository
  path from outside pgBackRest's own tooling).
- **`[029]` / info file format error** — usually a passphrase problem
  (see [backup failure](backup-failure.md) Step 2.1), not necessarily
  data corruption — rule this out first, since it's the more common and
  much less severe cause.
- **`[055]` / missing file** — something outside pgBackRest deleted or
  moved repository contents. Check for any process with write access to
  `BACKUP_REPO_PATH` other than pgBackRest itself.

## Step 3 — Identify which specific backup is affected

```sh
docker compose exec postgres pgbackrest --stanza=seev info --output=json
```

A corrupted **differential** backup is less severe than a corrupted
**full** backup — every differential in that chain depends on its parent
full backup, but a later differential in the same chain (if uncorrupted)
is still restorable back to its own point, and the *other* retained full
chain (K4 retains two) may be entirely unaffected.

A corrupted **full** backup is more severe: every differential built on
top of it becomes unrestorable too. Check whether the second retained
full chain (if the corruption is isolated to one chain) is itself clean:

```sh
docker compose exec postgres pgbackrest --stanza=seev check --set=<other-backup-label>
```

## Step 4 — If a chain is confirmed corrupted, prove the other one first

Before doing anything destructive, confirm K4's second retained chain
can actually restore — this is the whole reason two chains are kept:

```sh
BACKUP_REPO_PATH=<repo path> ./scripts/restore-cluster.sh latest
```

Run this against the **known-good** point if the corrupted backup is the
more recent one (see [dr-restore-drill.md](dr-restore-drill.md#pitr-target-selection)
for target selection) — this both confirms real recoverability and
produces a genuinely fresh, verified full backup once the drill target is
promoted and a new `backup-full` is taken from it.

## Step 5 — Rebuild the repository if genuinely necessary

Only after confirming a clean recovery path exists (Step 4): take a fresh
full backup immediately (`make backup-full` then `make backup-check`) to
re-establish a verified chain as soon as possible, and treat the
corrupted backup's retention window as effectively already expired —
investigate the root cause (Step 2) before trusting the repository path
again, since an unaddressed cause (failing disk, external write access)
will simply corrupt the next backup too.

## Step 6 — Escalate

Repository corruption on the *only* clean chain, or a root cause that
points at underlying disk/hardware failure rather than a one-off
interrupted write, is not a solo fix — escalate immediately per the same
15-minute rule as
[ledger-integrity-alert.md](ledger-integrity-alert.md), since the
severity here (backups may not be restorable) is comparable.

## Related

- [Backup failure](backup-failure.md) — the scheduled job itself not
  running or succeeding.
- [WAL archive lag](wal-archive-lag.md) — continuous archiving stalled on
  an otherwise-healthy repository.
- [DR restore drill](dr-restore-drill.md) — the full recovery procedure;
  Step 4 above is a direct application of it.
- docs/roadmap/archive/50-a7-backup-pitr-disaster-recovery.md — K4 (expire-after-check
  ordering, two-chain retention), K6 (manifest checksum status).
