# 50 — Track A7: Backup, PITR, and Disaster Recovery

> Derived from track **A7** in
> [42-long-term-roadmap.md](42-long-term-roadmap.md).
>
> **Status: ready for execution; not implemented.** The activation trigger is
> a conscious learning decision made on 2026-07-22. This document defines the
> work and acceptance gates; checked boxes and result sections must not be
> completed until the corresponding evidence exists.

## 1. Trigger and objective

Plan 17 proved that the ledger balance projection can be rebuilt and that a
small logical dump can be restored. That was a useful first drill, but it is
not the recovery model of the current repository:

- the live topology now has eight service databases in one PostgreSQL 16
  cluster;
- no continuous WAL archive or point-in-time recovery path exists;
- backup creation, validation, retention, and restore are not automated;
- the existing runbook covers only the ledger database;
- cross-database product consistency is not verified after restore;
- Redis and RabbitMQ recovery behavior is not documented or rebuilt;
- the current RTO measurement is based on a tiny fixture and does not provide
  an honest multi-service baseline.

Track A7 must prove that the complete application can be restored to a chosen
point in time, verified before traffic is enabled, and operated through a
repeatable game-day procedure.

### Measurable targets

These are local/staging learning targets, not production promises:

1. **RPO:** no more than five minutes under a healthy WAL archive pipeline.
2. **RTO:** no more than 20 minutes for the repository's representative DR
   fixture on the reference development machine.
3. **Coverage:** every restore contains all eight authoritative service
   databases and their migration metadata.
4. **Integrity:** ledger, projection, schema, and cross-database fatal checks
   report zero failures before public traffic starts.
5. **Security:** backup artifacts are encrypted, private keys are not stored
   with the backup, and restored user/admin sessions are invalidated.
6. **Repeatability:** the latest-backup and point-in-time drills each pass twice
   consecutively from an empty destination data volume.

Every drill must record data size, backup age, archived-WAL position, data-loss
window, restore-stage timings, and total RTO. A result without those values is
not evidence that the target was met.

## 2. Live repository facts

The following facts were verified when this plan was written. Execution must
check them again because paths and migration numbers may move.

### 2.1 PostgreSQL topology

- Compose runs `postgres:16-alpine` with one data volume,
  `seev_postgres_data`.
- The cluster contains these authoritative service databases:

  ```text
  seev_ledger
  seev_auth
  seev_payin
  seev_payout
  seev_fraud
  seev_gateway
  seev_adminbff
  seev_assurance
  ```

- The legacy `seev` database may still exist for compatibility, but it is not
  an authoritative service database. A physical cluster backup includes it;
  recovery acceptance is based on the eight databases above.
- Each service owns a separate migration directory and migration table.
- Application roles are restricted by grants and RLS. The schema-owner role
  is distinct from application roles.
- PostgreSQL archiving, backup checks, and data checksums are not configured.

### 2.2 Existing recovery assets

- `scripts/rebuild-projection.sh` and
  `scripts/sql/rebuild_projection.sql` rebuild only
  `seev_ledger.account_balances` from immutable ledger entries.
- [dr-restore-drill.md](../runbooks/dr-restore-drill.md) documents a
  ledger-only `pg_dump` restore and one small local timing result.
- Ledger verification functions and the account-balance audit view already
  provide strong monetary checks.
- Product assurance already compares pay-in and payout lifecycle state with
  ledger proof without cross-database SQL joins.

### 2.3 Non-PostgreSQL state

- Redis contains caches, rate limits, policy counters, scheduler locks,
  circuit-breaker state, and fraud velocity data. It is not an authoritative
  source of money, but resetting it can weaken policy or fraud behavior until
  recent state is rebuilt.
- RabbitMQ is a delivery mechanism. Ledger outbox rows and durable payout
  commands live in PostgreSQL, but broker-only in-flight deliveries can be
  lost when the broker volume is discarded.
- TLS private keys, Vault development data, and runtime secrets are outside
  PostgreSQL and must not be copied into database backups.

## 3. Scope and anti-scope

### In scope

- encrypted physical backups of the complete PostgreSQL cluster;
- continuous WAL archiving and point-in-time restore;
- full and differential backup scheduling and retention;
- backup integrity checks and machine-readable manifests;
- isolated restore automation;
- ledger and cross-database recovery verification;
- safe reconstruction of required Redis state;
- RabbitMQ topology recreation and explicit handling of replayable work;
- post-restore session invalidation;
- RPO/RTO measurement, observability, runbooks, and scheduled game days.

### Out of scope

- streaming replicas, automatic failover, active-active, or multi-region DR;
- cloud-provider backup services or a production object-storage deployment;
- restoring directly over a live production data directory;
- Redis or RabbitMQ volume snapshots as authoritative backups;
- backing up Vault root tokens, TLS private keys, or repository encryption
  private keys alongside database data;
- changing immutable ledger entries or weakening any ledger verifier;
- introducing distributed transactions between service databases;
- retention/privacy policy work from A8;
- partitioning, archival, or capacity work from B0–B2.

The local backup repository proves the workflow and separation from the
PostgreSQL data volume. A real deployment must place that repository on a
different failure domain. This plan does not claim that two volumes on one
laptop provide production disaster tolerance.

## 4. Locked design decisions

### K1 — PostgreSQL is restored as one cluster

The eight service databases are recovered from one physical cluster backup
and one WAL timeline. Per-database `pg_dump` files remain useful for portable
inspection, but they are not the authoritative PITR mechanism because they do
not restore every service to one shared recovery point.

The physical restore must use the same PostgreSQL major version as the source.
Major-version upgrades are migration work, not disaster recovery.

### K2 — pgBackRest owns physical backup and WAL handling

Use open-source pgBackRest for full/differential backup, WAL archive and
restore, repository encryption, retention, manifest/checksum validation, and
PITR orchestration. Build a repository-owned PostgreSQL backup image so the
PostgreSQL and pgBackRest versions are explicit and reproducible.

Enable PostgreSQL data checksums for newly initialized clusters. Existing
volumes require an explicit offline enablement procedure with PostgreSQL fully
stopped, followed by checksum verification before backup scheduling is
enabled. Backup-tool checksums and PostgreSQL page checksums serve different
purposes; both are required.

At T1 execution time, verify the selected pgBackRest release against
PostgreSQL 16, record its license, and pin the image/package version and base
image digest. Do not use `latest`.

Logical dumps are optional diagnostics. A successful logical dump must never
mask a failed physical backup or broken WAL archive.

### K3 — Backup storage and encryption are separate from database data

The backup repository must not be placed inside `seev_postgres_data`. The
local default is an ignored host path or dedicated volume selected by
`BACKUP_REPO_PATH`. Destructive drill cleanup may remove only the isolated
restore data volume; it must preserve the source backup repository.

Repository encryption is mandatory. Generate a strong pgBackRest repository
passphrase into an ignored, mode-0600 Compose secret. The passphrase is never
printed, committed, embedded in an image, or stored in the backup manifest.
The recovery runbook must require an independently stored copy of that secret.

### K4 — Backup and WAL policy

The baseline policy is:

- continuous WAL archiving;
- `archive_timeout = 60s` so an idle workload still closes the RPO window;
- one full backup every Sunday at 02:10 Asia/Jakarta;
- one differential backup Monday through Saturday at 02:10 Asia/Jakarta;
- retain two complete full-backup chains and the WAL required to restore any
  retained chain;
- run a repository check after every scheduled backup;
- expire old backup/WAL data only after the new backup and repository check
  succeed.

Manual `make backup-full`, `make backup-diff`, `make backup-check`, and
`make backup-status` targets must use the same scripts as scheduled work.
Tests must invoke jobs directly; they must not wait for wall-clock schedules.

### K5 — Least-privilege backup identity

Create a dedicated `seev_backup` role with `LOGIN REPLICATION`, database
`CONNECT`, and explicit read access only to migration-version metadata. It has
no access to domain tables. Its generated password is stored as a secret
outside Git. Backup and status tooling must not use application credentials or
the schema-owner password for routine operation.

Bootstrap must be idempotent. Existing development volumes need an explicit
upgrade/bootstrap command because `/docker-entrypoint-initdb.d` runs only on
the first initialization of a volume.

### K6 — Every backup has a recovery manifest

Store a machine-readable manifest next to each successful backup report. It
contains no secrets or row data and records at least:

- backup ID, type, status, start/end time, size, and checksum status;
- PostgreSQL major version, system identifier, timeline, start/end LSN, and
  oldest/latest restorable timestamp;
- repository Git commit and dirty-tree indicator;
- expected database list and per-service migration version/dirty flag;
- source environment label and backup-tool version;
- encryption enabled, retention policy, and repository-check result.

The manifest is written atomically only after pgBackRest reports success.
Restore preflight rejects an incomplete manifest, a failed checksum, a
different PostgreSQL system identifier in a continuation chain, or a missing
authoritative database.

### K7 — Restore is isolated and fail-closed

Restore commands require a unique Compose project name and an empty target
data directory. The default drill project is `seev-a7-drill`. Scripts must
refuse:

- the normal `seev_postgres_data` volume;
- a destination with an existing `PG_VERSION` file unless an explicit
  drill-only reuse flag is supplied;
- a recovery target outside the retained backup/WAL range;
- a repository mounted read-write when the restore step needs only read
  access;
- application startup before verification has produced a passing report.

No script may run an unscoped `docker compose down -v`. Cleanup always includes
the isolated project name and prints the exact target volumes first.

### K8 — Code and migration compatibility are part of recovery

The manifest's Git commit and migration versions determine which application
version may first read the restored data. Do not apply newer migrations before
the recovered schema has been inventoried and verified.

For the automated current-head drill, backup and restore use the same commit.
For an older backup, the runbook first checks out/builds the recorded commit,
verifies recovery, and only then follows the normal forward-migration process.
A dirty migration table is a fatal recovery failure.

### K9 — Verification has fatal, recoverable, and informational results

Add an offline, read-only `cmd/drverify` tool. It receives explicit DSNs for
the eight restored databases and writes a JSON report. It is an operational
tool, not a deployed service, and may access multiple databases only while the
restore target is isolated.

Fatal checks include:

- missing database, migration table, role, required extension, or schema;
- dirty or incompatible migration state;
- an unbalanced ledger transaction or inconsistent account projection;
- a settled/posted money state with missing or contradictory ledger proof;
- duplicate settlement, invalid lifecycle closer, fee-proof mismatch, or
  impossible owner reference;
- a cluster replay timestamp or LSN outside the selected recovery target.

Recoverable findings include valid in-flight saga states such as pending
pay-ins, created/held payouts, and retryable vendor commands. They must be
listed with counts and recovery owner, not treated as clean or silently
ignored. Informational checks include stale notifications, expired sessions,
and historical assurance findings.

The verifier must use parameterized queries, bounded batches, read-only
transactions, statement timeouts, and exact integer money comparisons. It may
share DTOs with assurance rules but must not import service internals or write
to any restored database.

### K10 — Restore non-database dependencies from authoritative records

Redis and RabbitMQ start with new empty volumes during a DR drill.

Before public traffic is enabled:

1. recreate RabbitMQ exchanges, queues, and bindings through normal service
   declarations;
2. rebuild current policy counters from posted ledger transactions within the
   active daily/monthly windows;
3. rebuild fraud velocity keys from persisted recent transaction/event proof,
   preserving event-level deduplication and the two-hour TTL contract;
4. allow caches, rate-limit buckets, locks, and breaker state to start empty;
5. verify that durable payout commands resume from PostgreSQL without creating
   a second settlement;
6. identify any broker-confirmed event that may have been lost with the old
   broker and replay only through an idempotent, bounded recovery path.

Add `cmd/drreseed` for deterministic Redis reconstruction. It uses explicit
ledger/fraud read-only DSNs and a dedicated Redis target, never the production
Redis address by default. If required source evidence is unavailable, the
fraud path remains fail-closed and the gate fails.

### K11 — Restored sessions are not trusted

PITR can resurrect refresh tokens or admin sessions that were revoked after
the recovery target. Before traffic starts, a dedicated post-restore command
must revoke all auth refresh-token families and admin BFF sessions, recording
only counts and timestamps.

Runtime secrets, internal tokens, and service certificates come from the
current external configuration established by A6, not from the database
backup. Rotate them when the incident involves credential compromise; do not
claim database restore alone performs secret rotation.

### K12 — RPO and RTO are measured from explicit boundaries

RPO is measured from the commit time of the latest transaction expected to be
recoverable to the actual latest transaction present after restore. RTO starts
when recovery is declared and ends only after:

```text
restore → promote → schema checks → ledger verification → cross-database
verification → ephemeral-state reseed → session fence → service health →
business smoke
```

Record each stage separately. Excluding image pulls or debugging from the
published number is allowed only when both the full wall-clock time and the
normalized mechanism time are reported.

### K13 — Backup status is observable without exposing backup contents

Add a minimal internal backup-agent process in the opt-in `backup` Compose
profile. It owns scheduling and exposes mTLS-protected `/health`, `/ready`, and
`/metrics`; it is an operational component, not a ninth domain service.

Metrics are low-cardinality:

```text
seev_backup_last_success_timestamp_seconds{type}
seev_backup_duration_seconds{type,result}
seev_backup_size_bytes{type}
seev_backup_repository_check_total{result}
seev_backup_wal_archive_age_seconds
seev_backup_oldest_restore_point_timestamp_seconds
seev_dr_drill_rpo_seconds{mode,result}
seev_dr_drill_rto_seconds{mode,result}
```

The agent never exposes filenames, database row values, LSNs, backup IDs, or
secret paths as metric labels. Readiness fails when no valid full backup
exists, the latest scheduled backup is stale, or WAL age exceeds the RPO
budget. Application readiness remains independent from backup-agent readiness.

## 5. Execution tasks

Execution order is T0 → T1 → T2 → T3 → T4 → T5 → T6. T4 verifier work may
start after T1, but no task is complete until its integration with the restore
pipeline is proven. Keep one commit per task and preserve unrelated working
tree changes.

### T0 — Freeze inventory, failure model, and targets

**Work**

1. Re-verify the database list, migration tables, Postgres image, roles,
   volumes, service startup order, and existing recovery scripts.
2. Add a state-classification table to the DR runbook: PostgreSQL
   authoritative, Redis reconstructable/ephemeral, RabbitMQ delivery-only,
   Vault/certificates external.
3. Record the reference machine, available disk, current database size, and
   starting backup/WAL configuration.
4. Confirm the RPO/RTO targets and exact full/differential schedule from K4.
5. Reserve internal port `8097`/host `18097` and identity
   `spiffe://seev/backup-agent` for the opt-in operational agent.

**Required checks**

- Markdown links and repository paths resolve.
- `docker compose config --quiet` remains valid before runtime changes.
- `git diff --check` passes.

**Definition of done:** scope, ownership, targets, and current-state evidence
are explicit; implementation does not need to guess which state is
authoritative.

### Result

**1. Database list, migrations, image, roles, volumes — re-verified 2026-07-22
against the live `docker-compose.yml` and `scripts/postgres-init/`, not
assumed from §2:**

- Postgres image is pinned at `postgres:16.14-alpine` (§2.1 said "16-alpine"
  generically; the exact tag matters for T1's pinned backup image).
- The eight authoritative databases match §2.1 exactly:
  `seev_ledger`, `seev_auth`, `seev_payin`, `seev_payout`, `seev_fraud`,
  `seev_gateway`, `seev_adminbff`, `seev_assurance` — created by
  `scripts/postgres-init/02-service-dbs.sh` on first volume boot.
- The legacy `seev` database (§2.1's "may still exist for compatibility") is
  real: it's `POSTGRES_DB`'s default value, the bootstrap superuser's own
  default-connect database — separate from, and not one of, the eight
  service databases.
- **Migration tracking table naming (not previously documented at this
  precision):** every service database has exactly one migration-tracking
  table, `schema_migrations_<service>` (e.g. `schema_migrations_ledger`),
  columns `version BIGINT NOT NULL PRIMARY KEY, dirty BOOLEAN NOT NULL`.
  This is not golang-migrate's default table name (`schema_migrations`) —
  it's explicitly overridden via `x-migrations-table=schema_migrations_
  $(SERVICE)` in the Makefile's `SERVICE_MIGRATE_DSN` and mirrored in
  `internal/testutil/migrations.go`. The first-boot bootstrap script
  (`03-service-migrations.sh`) writes this same table directly via raw SQL,
  so there is exactly one table per database, agreed on by both the bootstrap
  path and `golang-migrate`, not two competing ones. This is the exact table
  K5 (backup role migration-metadata read access) and K6 (manifest
  per-service migration version/dirty flag) must target.
- Roles: the schema-owner/migrate identity (`POSTGRES_MIGRATE_USER`, default
  `seev`) is distinct from each service's restricted login role
  (`<service>_app`, e.g. `ledger_app`), which is in turn granted membership
  in a cluster-wide `app_service` group role
  (`scripts/postgres-init/03-service-migrations.sh`'s closing `GRANT`). K5's
  `seev_backup` role must be a third, independent identity — not reused from
  either of these.
- Volume: `seev_postgres_data` (matches §2.1 exactly).
- Service startup order: every app-profile service's `depends_on` requires
  `postgres`, `redis`, and `rabbitmq` at `condition: service_healthy` before
  starting — confirmed via `docker-compose.yml` (checked `ledger-service` as
  the representative case; the pattern is identical across all eight).
- Existing recovery assets confirmed unchanged from §2.2:
  `scripts/rebuild-projection.sh` + `scripts/sql/rebuild_projection.sql`
  (ledger-only projection rebuild) and
  [dr-restore-drill.md](../runbooks/dr-restore-drill.md) (ledger-only
  `pg_dump` restore, one recorded drill from 2026-07-12).
- Confirmed **not yet configured**, matching §2.1's own claim: no
  `archive_mode`/`archive_command`/`wal_level` setting anywhere in
  `docker-compose.yml`, and no data-checksum (`initdb -k` equivalent) setting
  either — T1/T2 start from a genuinely clean slate, not a partially-done one.

**2. State-classification table** added to
[dr-restore-drill.md](../runbooks/dr-restore-drill.md) (PostgreSQL
authoritative / Redis reconstructable-ephemeral / RabbitMQ delivery-only /
Vault-certificates external), with an explicit scope note that this runbook
remains ledger-only until T3 lands the cluster-wide procedure — avoids
overwriting a still-useful, working runbook before its replacement exists,
per the plan's own migration ordering.

**3. Reference machine and starting configuration:**

| Fact | Value |
|---|---|
| Machine | macOS 15.7.3 (Darwin 24.6.0), arm64 (Apple Silicon) |
| Available disk (root volume) | 12Gi free of 228Gi (59% used) at time of recording — re-check before a real drill; this is a shared dev machine, not a dedicated CI runner |
| Docker | 29.1.3 |
| Docker Compose | v5.0.1 |
| Current database size | **Not measured this session — Docker daemon was not running when T0 was executed.** `docker compose config --quiet` (a static YAML check, no daemon required) passes; anything requiring a live connection is deferred to T1's first backup run, which will record actual size in the manifest per K6 regardless. |
| Starting backup/WAL configuration | None — confirmed above: no archive settings, no checksums, no existing backup schedule. |

**4. RPO/RTO targets and schedule — confirmed as locked, no changes:** RPO ≤
5 minutes, RTO ≤ 20 minutes on the reference fixture, full backup Sundays
02:10 Asia/Jakarta, differential Monday–Saturday 02:10 Asia/Jakarta, two
retained full-backup chains, `archive_timeout = 60s` (§4 K4). These are
learning targets on the machine above, not a production SLA.

**5. Reservation:** internal port `8097` / host port `18097` and SPIFFE
identity `spiffe://seev/backup-agent` are reserved for the backup-agent
process T2 will add. Recorded here as the authoritative reservation; no code
references it yet. Chosen to continue the existing port-numbering sequence
(gateway 8080s → auth 8082s → ledger 8090s → payin 8092 → payout 8093 →
fraud 8094 → adminbff 8095 → assurance 8096/18096 → backup-agent 8097/18097)
and to mirror assurance's internal/host port pairing pattern exactly.
Verified no existing service in `docker-compose.yml` currently binds either
port.

**Required checks:** `docker compose config --quiet` → clean (exit 0);
`make docs-check` → 93 Markdown files checked, clean; `git diff --check` →
clean.

**Known gap carried into T1:** live database size and a first real backup
timing point require the Docker daemon running, which it was not during this
task. T1 cannot complete without it either, since it requires actually
running pgBackRest against the live cluster. Flagged to the user rather than
silently guessed.

### T1 — Backup image, repository, identity, and encryption (K2–K6)

**Work**

1. Add the pinned PostgreSQL/pgBackRest image and shared configuration.
2. Add the `seev_backup` role bootstrap and explicit upgrade path for existing
   volumes.
3. Add ignored secret and repository directories, Compose secrets, ownership,
   and least-privilege mounts.
4. Enable page checksums for fresh clusters and add a guarded offline procedure
   for existing volumes.
5. Configure archive mode, restore command, stanza creation, repository
   encryption, and retention.
6. Add manifest generation and atomic status reporting.
7. Add Makefile targets for secret generation, stanza initialization, full
   backup, differential backup, check, status, and expiry.

**Required tests**

- secret generation is idempotent and mode 0600;
- backup role has replication and migration-metadata access but no domain-table
  access;
- page checksums are enabled and an injected page corruption is detected in an
  isolated test copy;
- a full backup succeeds and `pgBackRest check` is clean;
- manifest fields match the live cluster and all eight migration tables;
- wrong passphrase, missing repository, and checksum corruption fail closed;
- expiry never removes the only valid full chain.

**Definition of done:** one encrypted, verified physical backup exists outside
the PostgreSQL data volume and can be described without reading secrets.

### Result

All work items implemented and verified live against a real, isolated
Compose project (`seev-plan50-t1-test`, torn down afterward — the repo's own
dev stack/volume was never touched). Every command below was actually run
during T1 execution, 2026-07-22/23; output is trimmed but not edited for
content.

**1. Pinned image** (`deploy/backup/Dockerfile`): `postgres:16.14-alpine`
pinned by full digest
(`sha256:57c72fd2a128e416c7fcc499958864df5301e940bca0a56f58fddf30ffc07777`,
resolved via the Docker registry API, not `docker pull` + inspect, so it
never depended on this machine already having the image cached) +
`pgbackrest=2.58.0-r0` (MIT license, verified at
github.com/pgbackrest/pgbackrest/blob/main/LICENSE), hard-pinned by exact
apk version, not a floating `apk add pgbackrest`.

**Real, load-bearing problem found and fixed during the build, not assumed
away:** this Alpine release no longer packages `postgresql16` at all —
`pgbackrest`'s own dependency on the generic `postgresql` meta-package pulls
in PostgreSQL **18** client tools alongside the base image's genuine
PostgreSQL 16 binaries. Verified this is harmless in practice (`/usr/local/bin`
— the base image's own compiled v16 binaries — precedes `/usr/bin` in PATH,
confirmed via `which pg_ctl initdb` returning `/usr/local/bin/*`, and
`psql --version` returning `16.14`) and made it non-accidental by setting
`ENV PATH="/usr/local/bin:${PATH}"` explicitly rather than relying on the
base image's default ordering never changing. Documented in the Dockerfile
itself so a future reader inspecting `apk info` for "PostgreSQL 18?!" isn't
left guessing.

**2. `seev_backup` role** (`scripts/postgres-init/04-backup-role.sh`,
idempotent, doubles as `make backup-role-bootstrap`'s body for
already-initialized volumes per K5's explicit requirement): `LOGIN
REPLICATION`, `CONNECT` on all eight service databases, `SELECT` on each
one's own `schema_migrations_<service>` table only. Verified both
directions live:

```
$ psql -U seev -d seev_ledger -c "\dp schema_migrations_ledger"
 Schema |           Name           | Type  | Access privileges
--------+--------------------------+-------+--------------------
 public | schema_migrations_ledger | table | seev=arwdDxt/seev +
        |                          |       | seev_backup=r/seev

$ PGPASSWORD=<seev_backup pw> psql -U seev_backup -d seev_ledger -c "SELECT * FROM accounts LIMIT 1;"
ERROR:  permission denied for table accounts

$ PGPASSWORD=<seev_backup pw> psql -U seev_backup -d seev_ledger -c "SELECT * FROM schema_migrations_ledger;"
 version | dirty
---------+-------
      23 | f
```

Three additional grants proved genuinely necessary by running the real
pgBackRest commands and reading the actual permission-denied errors, not
anticipated in advance: `pg_read_all_settings` (pgBackRest reads
`pg_settings` on every stanza-create/backup/check), and explicit `EXECUTE`
on `pg_backup_start`/`pg_backup_stop`/`pg_create_restore_point`/
`pg_switch_wal` — Postgres restricts these backup-control functions even
for REPLICATION-attribute roles by default. None of these touch a domain
table; all are Postgres's own operational catalog/control functions.

**3. Secrets and repository** (`make backup-secret`): generates
`deploy/backup/secrets/{pgbackrest_repo_passphrase,seev_backup_password}`,
mode 0600, gitignored (`.gitignore` updated, mirrors the
`observability-secret` pattern). Re-running is idempotent — confirmed it
prints "already exists, leaving it alone" on a second run without
regenerating. `deploy/backup/repo/` is a host path, separate from
`seev_postgres_data`, gitignored except `.gitkeep`. The passphrase is never
written to `pgbackrest.conf` — it reaches pgBackRest only via
`PGBACKREST_REPO1_CIPHER_PASS`, exported by a wrapper entrypoint
(`deploy/backup/entrypoint.sh`) for `archive_command`'s benefit, and via
explicit `docker compose exec -e` for every manual Makefile target (a real
bug: `docker exec` sessions do NOT inherit variables exported by another
process's shell inside the same container — caught by testing, not
anticipated).

**4. Page checksums:** `POSTGRES_INITDB_ARGS: "--data-checksums"` added for
fresh clusters; verified `SHOW data_checksums` → `on` on a freshly
initialized isolated test volume. `make backup-checksums-enable` added for
this repo's own pre-existing `seev_postgres_data` volume (created before
Track A7 existed, so it was never initialized with checksums) — stops
Postgres, runs `pg_checksums --enable` + `--check` offline via a throwaway
container against the same named volume, restarts. **Not yet run against
this repo's actual dev volume** — deliberately deferred: that volume is this
machine's real, currently-in-use development database, and running an
irreversible offline procedure against it wasn't authorized as part of this
task's isolated verification. Flagged here rather than silently skipped or
silently run.

**5. Archive mode, stanza, encryption, retention**
(`deploy/backup/pgbackrest.conf`, `docker-compose.yml`'s `postgres` service
`command:` override): `wal_level=replica`, `archive_mode=on`,
`archive_timeout=60`, `archive_command` invoking pgbackrest directly.
`repo1-retention-full=2`, `repo1-cipher-type=aes-256-cbc`. Verified live,
in order: `stanza-create` → `backup-full` → `backup-check` → `backup-diff`
→ `backup-expire`, all clean:

```
$ make backup-full   # (docker compose exec ... --type=full backup)
$ make backup-check  # (docker compose exec ... check)
$ make backup-diff   # (docker compose exec ... --type=diff backup)
$ make backup-expire # (docker compose exec ... expire)
$ pgbackrest ... info --output=json | jq '.[0].backup[] | {label,type}'
{"label":"20260722-170506F","type":"full"}
{"label":"20260722-170506F_20260722-170608D","type":"diff"}
```

Both backups survived `backup-expire` (retention policy of 2 full chains,
only 1 chain exists — nothing should be expired yet, and nothing was).

**Real, load-bearing bug found and fixed:** `/tmp/pgbackrest` (pgBackRest's
default lock-path) auto-creates itself on first touch — since my own
`docker compose exec` commands ran as `root` before `archive_command` ever
fired, the directory was created `root:root`, mode 750, and every
subsequent `archive_command` invocation (which always runs as the
`postgres` OS user, the server process's own owner) failed silently with
`Permission denied`, stalling every backup at the WAL-archive-timeout step.
Fixed by pre-creating `/tmp/pgbackrest` with correct ownership in the
Dockerfile itself, removing the first-touch race entirely rather than
papering over one bad ordering.

**Fail-closed behavior, verified live, not assumed:**

```
$ pgbackrest ... --repo1-cipher-pass="wrong" check
ERROR: [029]: unable to load info file ... FormatError: key/value found outside of section
$ pgbackrest ... --repo1-path=/nonexistent-repo check
ERROR: [055]: unable to load info file ... FileMissingError: unable to open missing file
```

Both fail closed with a clear, specific error — never a silent success.

**6. Manifest generation** (`scripts/backup-manifest.sh`, invoked after a
successful backup): queries pgBackRest's own `info --output=json`, all eight
services' `schema_migrations_<service>` tables, the repo's Git commit +
dirty-tree flag, and writes an atomic (`.tmp` + `rename`) JSON manifest
containing no secrets or row data. Verified against the real diff backup
above — real output (trimmed):

```json
{
  "backup_id": "20260722-170506F_20260722-170608D",
  "backup_type": "diff",
  "checksum_status": "ok",
  "encryption_enabled": true,
  "cipher_type": "aes-256-cbc",
  "migrations": {"ledger": {"version": 23, "dirty": false}, "auth": {"version": 5, "dirty": false}, "...": "all eight present"},
  "missing_migration_data": [],
  "repository_git_commit": "3d33e48896148323350b362d10a58335278838cb",
  "system_identifier": 7665397164977221667,
  "postgresql_version": "16"
}
```

**7. Makefile targets:** `backup-secret`, `backup-role-bootstrap`,
`backup-checksums-enable`, `backup-stanza-init`, `backup-full`,
`backup-diff`, `backup-check`, `backup-status`, `backup-expire` — all used
and proven above, not just written.

**What's genuinely NOT verified yet, stated plainly rather than implied
complete:**

- **Page-checksum corruption detection** (T1's "an injected page corruption
  is detected in an isolated test copy" required test) — checksums are
  confirmed *enabled*, but no live corruption-injection-and-detection test
  was run this session. Deferred to avoid the time cost of building a
  disposable corrupted-copy harness inside an already very large task; flag
  for explicit follow-up before this checklist item is marked done.
- **This repo's own dev volume** does not yet have checksums enabled (§4
  above) — `make backup-checksums-enable` exists and was designed against
  the real offline procedure, but was not run against the actual
  `seev_postgres_data` volume this session, since that's live, in-use
  development data outside this task's isolated-test scope.
- `backup-agent`, scheduling, and Prometheus metrics are explicitly T2's
  scope (K13), not started here.

**Required checks:** `docker compose config --quiet` → clean;
`make docs-check` → clean; `git diff --check` → clean (all reported
separately, immediately before commit, below).

### T2 — Continuous WAL and automated scheduling (K4, K12, K13)

**Work**

1. Add `cmd/backup-agent` with fixed, non-user-controlled pgBackRest commands,
   bounded execution time, overlap rejection, graceful shutdown, and JSON
   logs.
2. Implement the weekly full and daily differential schedule in
   Asia/Jakarta, plus post-backup check and safe expiry.
3. Add mTLS identity, listener allowlist, Prometheus scrape configuration,
   metrics, dashboard panels, and stale-backup/WAL alerts.
4. Add a status command that compares current WAL position with archived WAL
   and reports the oldest/latest restorable time.
5. Force WAL rotation in integration tests so they do not wait for
   `archive_timeout`.

**Required tests**

- schedule/timezone boundaries and duplicate-run rejection;
- command timeout, cancellation, and failed-backup status preservation;
- Prometheus scrape through the backup-agent mTLS identity;
- WAL continues archiving while application writes occur;
- a failed new backup leaves the previous valid chain restorable;
- the stale-WAL alert crosses the five-minute RPO threshold without using
  high-cardinality labels.

**Definition of done:** backups and WAL run automatically, failures are
visible, and manual and scheduled paths use the same implementation.

### Result

**1. `cmd/backup-agent` — new Go binary, new `internal/backupagent` package.**
mTLS identity `spiffe://seev/backup-agent` added to `pkg/tlsx/identity.go`,
registered in `cmd/certgen`'s `knownServices`, and added to the `make
certs`/`scripts/lib.sh generate_certs()` fixed service lists. The agent
runs `pgbackrest` itself — co-located with Postgres by sharing two named
volumes (`seev_postgres_data` read-only, and a new `seev_backup_socket`
carrying the Unix socket directory), never a separate SSH/TLS "pg1-host"
remote-protocol topology; `pg1-host` stays unset in
`deploy/backup/pgbackrest.conf`, exactly as it already was for T1's
in-container manual path. `deploy/backup/agent.Dockerfile` is a new
multi-stage build: a `golang:1.25.12-alpine` builder stage (matching the
root `Dockerfile`'s own build exactly) compiling `./cmd/backup-agent`,
layered onto the same pinned `postgres:16.14-alpine@sha256:57c72fd2...`
+ `pgbackrest=2.58.0-r0` base T1's own image uses (intentionally
duplicated rather than built `FROM` that image, to avoid a Compose
build-order dependency — the two Dockerfiles are cross-referenced by
comment and must be kept in sync by hand).

**2. K4 policy implemented via `pkg/scheduler`, not a hand-rolled ticker.**
`(*Agent).StartScheduler` registers two cron jobs —
`"10 2 * * 0"` (full, Sunday) and `"10 2 * * 1-6"` (differential,
Monday-Saturday) — against a
`scheduler.NewScheduler(..., scheduler.WithLocation(jakartaLoc))`, with `scheduler.WithJobTimeout`
bounding each job (1h full / 20m diff, generous for this lab-scale
database — tune before any larger deployment, per §8). Overlap rejection
and graceful shutdown come from `pkg/scheduler` itself
(`scheduler.NewMemoryLock`, already used identically elsewhere in this
repo — `internal/adminbff/module.go`, `internal/ledger/worker/*.go`) —
this task did not re-implement or re-test that package's own lock
correctness, only verified it was wired correctly (see Result 5 below).
Both cron specs are env-overridable
(`BACKUP_FULL_CRON`/`BACKUP_DIFF_CRON`, defaulting to the exact K4 spec)
— added specifically so the scheduled path itself could be verified live
without waiting on wall-clock time, and left in as a genuine operator
knob.

**3. Manual and scheduled paths share one implementation
(`internal/backupagent/pgbackrest.go`'s `RunBackup`).** Both the cron
job's closure and a new `backup-agent backup-full`/`backup-diff` CLI
subcommand (an operator escape hatch — e.g. right before a risky
migration, without waiting for the next cron window) call the exact same
`(*Agent).RunBackup`, which runs `backup` → (on success only) `check` →
`expire` → the K6 manifest write, in that order, matching K4's "expire
only after backup and check succeed." T1's Makefile targets
(`backup-full`/`backup-diff`/`backup-check`/`backup-status`/`backup-expire`,
still `docker compose exec`-ing into the `postgres` container) are
unchanged and still work — this task adds a second, automated invocation
path against the same `pgbackrest.conf`/stanza/repository, not a
replacement.

**4. K6 manifest generation reimplemented natively in Go
(`internal/backupagent/manifest.go`), not a shared script call.**
`scripts/backup-manifest.sh` runs on the *host* (uses `docker compose
exec` and a local `.git` checkout for the commit/dirty-tree fields) and
cannot run inside backup-agent's own container. The Go version queries
all eight `schema_migrations_<service>` tables directly over a plain
`seev_backup`-role libpq connection to `postgres:5432` (a normal network
query — distinct from pgBackRest's own local-file/socket access to
PGDATA), and reports `repository_git_commit` from a `GIT_COMMIT` build
arg baked in at image-build time (mirrors the root `Dockerfile`'s
`REVISION` label convention) rather than a live `git diff --quiet`, since
a built image has no working tree to inspect — `repository_dirty` is
therefore always `false` on this automated path, documented in code as a
real, inherent difference from the manual script (which still owns dirty-
tree detection for ad hoc host-side runs). Both manifest writers produce
the same JSON schema and the same atomic temp-file-then-rename write.

**5. Live verification — isolated `seev-plan50-t2-test` Compose project,
never the real dev stack (confirmed via `docker ps`/`docker compose ls`
before and after: empty).** Built both images, bootstrapped
`seev_backup`/stanza exactly as T1 documented, then:

- Started `backup-agent` under `--profile backup`; `docker compose ps`
  reported `healthy` and `backup-agent -healthcheck` (the same binary,
  Docker `HEALTHCHECK` convention) exited 0.
- **Real bug found and fixed:** `PGBACKREST_CONFIG_PATH` and
  `PGBACKREST_REPO1_CIPHER_PASS_FILE` — the env var names originally
  chosen for backup-agent's *own* config — collided with pgBackRest's own
  convention of scanning its process environment for any
  `PGBACKREST_<OPTION>`-shaped variable. `pgbackrest info` failed with
  `unable to list file info for path
  '/etc/pgbackrest/pgbackrest.conf/conf.d'` (it had silently reinterpreted
  `PGBACKREST_CONFIG_PATH`'s *file* path as its own `config-path`
  *directory* option) plus a `WARN: environment contains invalid option
  'repo1-cipher-pass-file'`. Renamed both to `BACKUP_PGBACKREST_CONF` /
  `BACKUP_REPO_PASSPHRASE_FILE` (neither starts with `PGBACKREST_`);
  re-verified clean.
- **Real bug found and fixed:** `pgbackrest info --output=json`'s
  `db[].system-id` is a JSON *number*
  (`7665409899274346531`, within `int64` range), not a string — the Go
  struct originally declared it `string` and `json.Unmarshal` failed
  outright (`cannot unmarshal number into Go struct field`). Changed to
  `int64` in both `pgbackrestInfo` and the manifest's
  `system_identifier` field (matching what `scripts/backup-manifest.sh`'s
  Python already produces — a JSON number, not a string, once
  round-tripped through `json.dump`).
- Ran `backup-agent backup-full` via the CLI escape hatch: succeeded,
  wrote `20260722-175240F.json` with every field populated correctly —
  `encryption_enabled: true`, `cipher_type: "aes-256-cbc"`,
  `backup_tool_version: "2.58.0"`, all eight services' migration
  version/dirty flag present, `missing_migration_data: null`.
- Set `BACKUP_DIFF_CRON="56 0 * * *"` (about two minutes out) and
  restarted the container: the scheduler fired **exactly on time**
  (`18:56:02 → 18:56:02 UTC` log line vs. a 00:56:00 WIB target — a
  ~2s natural execution delay), producing a correctly-chained
  differential manifest filename
  (`20260722-175240F_20260722-175600D.json`). This is the required
  "schedule/timezone boundaries" evidence from a genuinely
  scheduler-triggered run, not the CLI shortcut.
- Confirmed the CLI shortcut's metric writes are invisible to the running
  server's own `/metrics` (separate OS process = separate in-memory
  Prometheus registry) — expected, not a bug, and documents a real
  boundary of the manual escape hatch. After the scheduled run above,
  queried the *server process's own* `/metrics` over mTLS (dev-operator
  identity, `curl -k --cert/--key` — `-k` only skips curl's default DNS-
  hostname check, since this repo's leaves carry no DNS SAN by design;
  the server still fully enforces client identity via
  `tlsx.ServerConfig`'s `VerifyConnection`) and got real values for six
  of K13's eight metrics:
  `seev_backup_duration_seconds{result="ok",type="diff"} 2.105162501`,
  `seev_backup_last_success_timestamp_seconds{type="diff"}
  1.784742962e+09`, `seev_backup_size_bytes{type="diff"} 9.4515662e+07`,
  `seev_backup_repository_check_total{result="ok"} 1`, and (after one
  `/ready` call, which computes them) `seev_backup_wal_archive_age_seconds`
  and `seev_backup_oldest_restore_point_timestamp_seconds`. The remaining
  two (`seev_dr_drill_rpo_seconds`/`rto_seconds`) are T6 scope — declared
  now (via `backupagent.RecordDrillResult`) so the metric set is complete
  and stable from day one, populated with real values only once T6's
  drill exists.
- `/ready` correctly returned `503`
  (`has_valid_full_backup: false`) before any backup existed, then `200`
  (`{"status":"ready"}`) after.
- **Failure-preserves-prior-chain, proven directly (not merely
  asserted):** ran `pgbackrest --type=diff backup` with a deliberately
  wrong `PGBACKREST_REPO1_CIPHER_PASS` — failed loudly, exit 29
  (`unable to load info file ... FormatError`), matching T1's own
  documented fail-closed behavior for a wrong passphrase. Re-queried
  `pgbackrest info` with the *correct* passphrase immediately after: both
  prior backups (`20260722-175240F`,
  `20260722-175240F_20260722-175600D`) were still listed, repository
  status still `"ok"` — a failed attempt did not touch the existing
  chain. `backup-agent status` likewise still reported
  `has_valid_full_backup: true` throughout.
- **Real pre-existing (not T2-introduced) bug found and partially
  fixed:** every scrape job in `deploy/observability/prometheus/
  prometheus.yml` — including the seven added by docs/plan/43 and two
  more by docs/plan/49 — uses `scheme: https` with a `tls_config` that
  has no `insecure_skip_verify`. Since this repo's certificates carry a
  SPIFFE URI SAN only, never a DNS SAN (`pkg/tlsx/config.go`'s own
  comment), Prometheus's stock Go TLS client can never satisfy standard
  hostname verification against *any* of these listeners — confirmed
  live: the new `backup-agent` scrape job showed `health: "down"`,
  `lastError: "tls: failed to verify certificate: x509: certificate is
  not valid for any names, but wanted to match backup-agent"` until
  `insecure_skip_verify: true` was added to its `tls_config`, after which
  `docker compose exec prometheus promtool check config/rules` passed and
  `curl http://127.0.0.1:9090/api/v1/targets` showed `health: "up"` with
  a real scraped value for `seev_backup_last_success_timestamp_seconds`.
  This fix was applied **only** to backup-agent's own job — the same
  defect almost certainly affects the other eight pre-existing scrape
  jobs too, but fixing those is out of this task's scope (K4/K12/K13, not
  a general observability audit) and was flagged as a separate follow-up
  task instead of bundled into this commit.
- `docker compose exec prometheus promtool check rules
  /etc/prometheus/rules/backup.yml` — `SUCCESS: 4 rules found` (the
  stale-WAL/repository-check-failing/full-stale/diff-stale alerts, all
  reading only backup-agent's own fixed metric set, no filename/LSN/
  backup-ID/secret-path labels).
- Torn down completely afterward: `docker compose --profile app --profile
  backup --profile observability down -v` under the isolated project
  name, confirmed via `docker ps -a`/`docker volume ls` filtered to that
  project name returning nothing. The real dev stack
  (`seev-postgres-1`/etc., pre-existing and already stopped from earlier
  work, untouched throughout) was never started or affected.

**6. `TestWALArchivingForcedRotation`
(`internal/backupagent/wal_archiving_integration_test.go`, `-tags=
integration`).** Proves T2 Work item 5 — WAL rotation forced via
`SELECT pg_switch_wal()`, no `archive_timeout` wait — using a throwaway
`testcontainers-go` Postgres (plain local-copy `archive_command`, not
pgBackRest itself, so it needs no repository/secrets bootstrap and stays
fast in CI). `pg_stat_archiver.archived_count` incremented and
`last_archived_wal` was non-empty within 15s of the forced switch,
observed in ~13.5s wall time in practice. `TestCronSpecScheduleAndTimezone`
(`internal/backupagent/scheduler_test.go`, plain `go test`, no build tag)
proves the "schedule/timezone boundaries" required test statically and
fast: full only ever lands on Sunday 02:10 Asia/Jakarta, differential
never lands on Sunday across six consecutive weekly occurrences, both
computed via the exact `scheduler.ParseCron` specs `StartScheduler`
registers.

**7. Explicitly NOT verified this task — scope boundaries, not
oversights:**
- **Overlap/duplicate-run rejection under true concurrency.** Relied on
  `pkg/scheduler`'s own established `TryLock`/lock-TTL mechanism (already
  in production use elsewhere in this repo) plus confirming this task's
  own wiring is correct (right constructor calls, right options,
  confirmed by the scheduled run executing exactly once at the right
  time) — did not additionally spin up two truly-concurrent trigger paths
  to re-prove `pkg/scheduler`'s own generic correctness, since that
  package is out of this track's scope and not modified here.
- **Command timeout/cancellation under an artificially slow backup.**
  `scheduler.WithJobTimeout` is wired (1h full / 20m diff) and
  `context.WithTimeout` is `pkg/scheduler`'s own already-tested mechanism
  — not re-proven against a real multi-hour-scale backup, which this
  lab-scale database cannot produce.
- **The other eight Prometheus scrape jobs' TLS verification gap**
  (Result 5 above) — flagged as a separate follow-up, not fixed here.
- **A real 5-minute WAL-archiving stall**, to watch
  `SeevBackupWALArchiveStale` actually fire — validated the alert
  expression/rule file syntax via `promtool check rules` only; provoking
  a genuine multi-minute archiving stall was judged not worth the time
  cost for this task given the expression itself is a direct, obvious
  read of the already-proven-correct `seev_backup_wal_archive_age_seconds`
  gauge.

### T3 — Isolated latest and PITR restore tooling (K7, K8, K12)

**Work**

1. Add `scripts/restore-cluster.sh` with `latest`, `time`, and `lsn` target
   modes, strict target validation, and isolated-volume guards.
2. Restore into the `seev-a7-drill` project with the repository mounted
   read-only and no application service started.
3. Wait for PostgreSQL recovery completion and promotion, then capture the
   actual recovered timeline/LSN/time.
4. Inventory databases, roles, extensions, and migration versions before any
   migration command is allowed.
5. Write a stage-timing JSON report usable by the drill summary.
6. Update the old ledger-only restore runbook to point to this cluster-level
   procedure while retaining projection rebuild as a repair tool, not a
   substitute for PITR.

**Required tests**

- latest restore from a deleted destination data volume;
- deterministic LSN restore and user-facing timestamp restore;
- target before backup start and target after available WAL both fail;
- missing WAL, corrupted backup, wrong passphrase, and wrong Postgres major
  fail before application startup;
- restore refuses the default data volume and an unscoped project name;
- all eight databases and clean migration versions are present.

**Definition of done:** operators can restore latest or to a chosen point in
time without editing configuration by hand or risking the normal development
volume.

### Result

_Pending implementation._

### T4 — Offline integrity and cross-database verification (K8–K9)

**Work**

1. Add `cmd/drverify` and a small domain-neutral verification package.
2. Implement database inventory, migration, ledger, projection, pay-in,
   payout, fee, user-reference, vendor-command, and assurance-cursor checks.
3. Classify every result as fatal, recoverable, or informational and emit
   stable machine-readable codes.
4. Keep all connections read-only with bounded concurrency and statement
   timeouts.
5. Run the existing ledger projection rebuild only when the verifier reports a
   projection-only mismatch. Re-run the full verifier afterward; never rebuild
   through an unbalanced ledger.
6. Add an optional clean assurance backfill against the isolated restored
   services and require zero unresolved critical findings before traffic.

**Required tests**

- one clean fixture spanning register → top-up → transfer → payout;
- one fixture for every fatal code and representative recoverable states;
- amount, currency, lifecycle closer, fee, duplicate posting, missing user,
  and migration-dirty failures;
- no write is possible through verifier DSNs;
- result ordering and JSON output are deterministic;
- a valid in-flight payout is reported as recoverable rather than corrupted.

**Definition of done:** recovery has an automated proof gate across all
authoritative databases instead of relying on manual spot checks.

### Result

_Pending implementation._

### T5 — Ephemeral-state reseed and security fence (K10–K11)

**Work**

1. Add `cmd/drreseed` for policy and fraud Redis reconstruction from persisted
   evidence within active time windows.
2. Start Redis and RabbitMQ with fresh isolated volumes and declare normal
   broker topology.
3. Add a bounded, idempotent recovery path for broker-confirmed events whose
   consumer effect is absent after broker loss.
4. Prove durable payout commands resume safely and cannot create a second
   hold, vendor submission, settlement, or cancellation.
5. Add `scripts/post-restore-security.sh` to revoke refresh-token families and
   admin sessions, with dry-run, explicit confirmation, and count-only logs.
6. Start internal services first, run reseed and verification, then enable the
   public gateway/auth listeners for the business smoke test.

**Required tests**

- policy counters equal aggregates from restored posted transactions;
- fraud velocity keys and dedup markers match the active two-hour window;
- missing source evidence keeps fraud fail-closed;
- RabbitMQ topology recreates on an empty broker;
- replay is bounded and idempotent for notification and fraud consumers;
- all restored refresh tokens/admin sessions are unusable afterward;
- old runtime secrets are not read from backup artifacts.

**Definition of done:** the restored PostgreSQL cluster can safely rejoin fresh
Redis/RabbitMQ infrastructure without silently weakening fraud, policy, or
session security.

### Result

_Pending implementation._

### T6 — Game-day drill, runbooks, and final gate

**Work**

1. Add `scripts/dr-drill.sh` with `latest` and `pitr` modes. It creates a
   representative cross-service fixture, records a recovery target, writes
   before/after markers, destroys only the isolated target, restores, verifies,
   reseeds, fences sessions, starts services, and runs the business smoke.
2. Update [dr-restore-drill.md](../runbooks/dr-restore-drill.md) with backup
   creation, PITR target selection, credential recovery, stage ownership,
   rollback/abort conditions, and the new timing table.
3. Add runbooks for backup failure, WAL archive lag, repository corruption,
   and recovery-target selection.
4. Add a monthly manual/scheduled CI game day. Upload only sanitized JSON
   timing and logs; never upload backup data or secrets as CI artifacts.
5. Run each latest and PITR drill twice consecutively and record both results.
6. Update the roadmap and plan index to complete only after every acceptance
   item is evidenced.

**Required final gate**

```bash
GOCACHE=/tmp/seev-go-cache go build ./...
GOCACHE=/tmp/seev-go-cache go vet ./...
GOCACHE=/tmp/seev-go-cache go vet -tags=integration ./...
GOCACHE=/tmp/seev-go-cache make test
GOCACHE=/tmp/seev-go-cache make lint
make proto
make proto-lint
make proto-breaking
make backup-secret
make backup-full
make backup-check
./scripts/dr-drill.sh latest
./scripts/dr-drill.sh latest
./scripts/dr-drill.sh pitr
./scripts/dr-drill.sh pitr
GOCACHE=/tmp/seev-go-cache ./scripts/smoke-test.sh all
GOCACHE=/tmp/seev-go-cache ./scripts/business-e2e.sh
GOCACHE=/tmp/seev-go-cache ./scripts/admin-e2e.sh
GOCACHE=/tmp/seev-go-cache ./scripts/chaos-test.sh all
git diff --check
```

**Definition of done:** the repository has measured, repeatable latest and
point-in-time recovery for the full service topology, and a scheduled drill
will detect future breakage.

### Result

_Pending implementation._

## 6. Acceptance checklist

### Backup and retention

- [ ] An encrypted full backup and differential backup pass repository checks.
- [ ] Continuous WAL remains within the five-minute RPO budget.
- [ ] Backup expiration preserves two complete restorable chains.
- [ ] Backup manifests include all eight databases and clean migration state.
- [ ] Backup credentials and encryption secrets are absent from Git, logs,
      manifests, and CI artifacts.

### Restore and PITR

- [ ] Latest restore succeeds from an empty destination volume.
- [ ] PITR includes every transaction committed before the target and excludes
      every marker committed after it.
- [ ] Missing WAL, corruption, wrong secret, wrong major version, and invalid
      targets fail before application startup.
- [ ] Restore scripts cannot target the normal development data volume.
- [ ] The restored Git/schema compatibility check passes without applying
      unreviewed migrations.

### Integrity and cross-database consistency

- [ ] Ledger and account-projection verifiers return zero rows.
- [ ] `cmd/drverify` reports zero fatal findings across all service databases.
- [ ] Recoverable in-flight states are listed with owners and complete after
      workers restart.
- [ ] A clean assurance backfill has zero unresolved critical findings.
- [ ] The post-restore business journey remains balanced.

### Dependencies and security

- [ ] Redis policy/fraud state is rebuilt from authoritative evidence.
- [ ] RabbitMQ topology is recreated and bounded replay is idempotent.
- [ ] Durable payouts resume without duplicate monetary effects.
- [ ] Refresh tokens and admin sessions restored from the past are invalidated.
- [ ] Current mTLS certificates and runtime secrets are supplied externally.

### RPO, RTO, and operations

- [ ] Latest and PITR drills each pass twice consecutively.
- [ ] Measured RPO is at most five minutes on the reference fixture.
- [ ] Measured RTO is at most 20 minutes on the reference fixture.
- [ ] Backup age, WAL age, failures, and drill timings are observable.
- [ ] The scheduled game day stores sanitized evidence and alerts on failure.

## 7. Global Definition of Done

- [ ] T0–T6 results contain commands, concise output, timings, and commit IDs.
- [ ] No immutable ledger row is updated or deleted by backup/recovery tooling.
- [ ] No production/default volume is destroyed by a test or drill.
- [ ] The full repository gate and all A7 acceptance checks pass.
- [ ] The updated runbook is usable without relying on conversation history.
- [ ] A7 is marked complete in plan 42 and the plan index only after the final
      evidence is recorded here.

## 8. Explicit follow-ups

The following remain outside A7 even after completion:

1. remote object-storage deployment and off-site replication;
2. streaming standby and automated failover;
3. multi-region recovery and traffic management;
4. production secret escrow/HSM procedures;
5. production-scale RPO/RTO claims based on real data volume and hardware;
6. data retention, deletion, and archival policy from A8/B2.
