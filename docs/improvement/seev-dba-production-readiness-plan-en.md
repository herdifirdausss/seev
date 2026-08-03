# Seev Database Production-Readiness Plan

> Repository: `herdifirdausss/seev`  
> Current status: not yet used in production  
> Perspective: World-class Database Administrator / Database Reliability Engineer  
> Goal: evolve Seev from a strong database architecture into a system with verifiable production safety, operational maturity, scalability evidence, and recovery capability.

---

## 1. Executive Summary

Seev already has a strong database foundation for a non-production repository:

- service-owned databases;
- an append-only financial ledger;
- deterministic row locking;
- transactional outbox;
- idempotency controls;
- reconciliation workflows;
- backup and point-in-time recovery support;
- retention mechanisms;
- load, soak, and chaos testing;
- database-focused operational runbooks.

The largest remaining gap is no longer basic schema design or index creation. The main challenge is proving that the database remains:

- correct under concurrency;
- safe during schema evolution;
- recoverable after storage or primary database failure;
- stable as data volume grows;
- operable by engineers other than the original author;
- protected even when an application path contains a bug.

The highest-priority work is:

1. close privilege and financial-invariant gaps;
2. establish production-safe migration governance;
3. prove backup, restore, failover, and disaster recovery;
4. strengthen database observability;
5. complete capacity and data-growth evidence;
6. establish recurring operational governance.

---

## 2. Target State

After this plan is completed, Seev should have the following characteristics.

### Correctness

- An unbalanced ledger transaction cannot become `posted`.
- Balance projections can be verified and rebuilt from ledger entries.
- Every financial write has an explicit idempotency boundary.
- Duplicate callbacks, retries, replay, and process crashes cannot create duplicate financial effects.
- Direct updates or deletes against immutable financial records are rejected by the database.

### Security

- Runtime roles follow least privilege.
- Migration roles are never used by application processes.
- Sensitive `SECURITY DEFINER` functions are not executable by `PUBLIC`.
- Object ownership, role membership, grants, and default privileges are auditable.
- Database credentials and backup credentials are separated.

### Availability

- Loss of the primary database does not require prolonged manual recovery.
- Failover cannot produce dual-primary or split-brain behavior.
- Connection storms cannot exhaust PostgreSQL `max_connections`.
- Backup, standby, replication, and WAL health are continuously monitored.

### Recoverability

- Full restore and point-in-time recovery have been tested.
- Restore is performed from a backup stored outside the primary database host.
- RPO and RTO are measured rather than assumed.
- Restore verification covers every service database and all critical financial invariants.

### Scalability

- Table and index growth can be forecast.
- Partitioning is introduced only when evidence justifies it.
- Autovacuum, WAL, checkpoints, connections, and storage have explicit budgets.
- Load tests use realistic row counts and cardinality.

### Operability

- DBA dashboards and alerts are available.
- Runbooks are validated through game days.
- Risky migrations are automatically flagged or rejected by CI.
- Every database risk has an owner, acceptance criteria, and review date.

---

## 3. Scope

This plan covers:

- PostgreSQL schemas and constraints;
- transactions and concurrency;
- ledger correctness;
- migration safety;
- access control and database privileges;
- connection management;
- indexing;
- autovacuum and table maintenance;
- backup, restore, PITR, and disaster recovery;
- high availability and failover;
- database observability;
- data growth, archival, retention, and partitioning;
- database testing;
- operational governance.

This plan does not deeply cover:

- application security as a whole;
- the complete Kubernetes production architecture;
- vendor contracts and business compliance;
- frontend development;
- non-database code quality unless it directly affects database correctness.

---

## 4. Prioritization Model

| Priority | Meaning | Target |
|---|---|---|
| P0 | May cause unauthorized operations, corrupted financial state, unrecoverable data, or total outage | Complete before any production trial |
| P1 | May cause a major incident, severe degradation, or difficult operations | Complete before public production |
| P2 | Improves scalability, efficiency, and operational maturity | Complete after the production foundation is stable |
| P3 | Advanced optimization and long-term evolution | Execute only when supported by evidence |

---

# 5. Phase 0 — Establish the Database Baseline

## Objective

Create a single source of truth for all future DBA decisions.

## 5.1 Build a database inventory

Document the following for every service:

- database name;
- schema name;
- database owner;
- migration role;
- runtime role;
- monitoring or read-only role;
- enabled PostgreSQL extensions;
- primary tables;
- foreign keys;
- triggers;
- functions and procedures;
- `SECURITY DEFINER` objects;
- row-level security policies;
- expected data growth;
- retention class;
- backup criticality;
- target RPO;
- target RTO.

## 5.2 Capture a schema-size baseline

Capture:

- row counts;
- table sizes;
- index sizes;
- total relation sizes;
- largest tables;
- largest indexes;
- unused indexes;
- duplicate or overlapping indexes;
- dead tuples;
- transaction ID age;
- sequence utilization.

Example baseline query:

```sql
SELECT
    schemaname,
    relname,
    n_live_tup,
    n_dead_tup,
    pg_size_pretty(pg_total_relation_size(relid)) AS total_size
FROM pg_stat_user_tables
ORDER BY pg_total_relation_size(relid) DESC;
```

## 5.3 Capture a PostgreSQL configuration baseline

At minimum, record:

- PostgreSQL version;
- `max_connections`;
- `shared_buffers`;
- `effective_cache_size`;
- `work_mem`;
- `maintenance_work_mem`;
- `wal_level`;
- `max_wal_size`;
- `checkpoint_timeout`;
- `checkpoint_completion_target`;
- autovacuum settings;
- archive settings;
- replication settings;
- session timeout settings;
- application pool settings.

## 5.4 Define supported PostgreSQL versions

Document:

- minimum supported PostgreSQL version;
- recommended PostgreSQL version;
- minor-version patching policy;
- extension compatibility policy;
- end-of-life policy;
- major-version upgrade policy.

## Deliverables

- `docs/database/database-inventory.md`
- `docs/database/schema-size-baseline.md`
- `docs/database/postgresql-configuration-baseline.md`
- `docs/database/supported-versions.md`
- repeatable inventory scripts.

## Acceptance Criteria

- All databases and roles are inventoried.
- No function, trigger, policy, extension, or grant is undocumented.
- The baseline can be regenerated with a single command.
- Every baseline includes a commit hash and timestamp.

---

# 6. Phase 1 — P0 Privilege and Security Hardening

## Objective

Make the database an explicit, auditable security boundary.

## 6.1 Audit all `SECURITY DEFINER` functions

Run an inventory query:

```sql
SELECT
    n.nspname AS schema_name,
    p.proname AS function_name,
    p.prosecdef AS security_definer,
    pg_get_userbyid(p.proowner) AS owner,
    p.proacl
FROM pg_proc p
JOIN pg_namespace n ON n.oid = p.pronamespace
WHERE p.prosecdef = true;
```

For every function:

- revoke execution from `PUBLIC`;
- grant execution only to explicit allow-listed roles;
- use a trusted owner;
- set a fixed `search_path`;
- schema-qualify referenced objects;
- avoid dynamic SQL where possible;
- validate all arguments;
- document all side effects.

Recommended pattern:

```sql
CREATE OR REPLACE FUNCTION ...
RETURNS ...
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
    ...
END;
$$;

REVOKE ALL ON FUNCTION function_name(...) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION function_name(...) TO app_service;
```

## 6.2 Harden default privileges

For each owner role:

```sql
ALTER DEFAULT PRIVILEGES
FOR ROLE ledger_owner
IN SCHEMA public
REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC;
```

Add explicit default grants only for approved runtime roles.

## 6.3 Audit runtime roles

A runtime role should:

- not be a superuser;
- not create databases;
- not create roles;
- not bypass RLS;
- not create schemas;
- not own application objects;
- not have migration privileges;
- receive only required `CONNECT`, `USAGE`, `SELECT`, `INSERT`, `UPDATE`, or `EXECUTE` privileges;
- avoid direct writes to immutable tables when writes can be controlled through a trusted function.

## 6.4 Audit object ownership

Ensure:

- each database is owned by a dedicated owner role;
- schemas are owned by a migration or owner role;
- runtime roles are not owners;
- backup roles are separate;
- monitoring roles are read-only;
- no object is accidentally owned by a personal user.

## 6.5 Add privilege regression tests

Tests must fail when:

- `PUBLIC` can execute a sensitive function;
- a runtime role can `DROP`, `ALTER`, or `TRUNCATE`;
- a runtime role can update immutable ledger entries;
- a monitoring role can write;
- service A can directly access service B's database.

## Deliverables

- `docs/database/security/role-model.md`
- `docs/database/security/privilege-matrix.md`
- `scripts/database/audit-privileges.sql`
- `scripts/database/audit-security-definer.sql`
- CI privilege regression tests.

## Acceptance Criteria

- No sensitive function is executable by `PUBLIC`.
- Runtime roles pass least-privilege tests.
- Every object has the expected owner.
- Privilege drift is automatically detectable.
- Cross-service database access is rejected.

---

# 7. Phase 2 — P0 Financial Invariant Enforcement

## Objective

Move critical financial rules from conventions into enforced guarantees.

## 7.1 Define formal invariants

At minimum:

1. Total debit equals total credit for every posted transaction.
2. Currency is consistent across all entries in a transaction unless an explicit FX model is used.
3. Posted transactions are immutable.
4. Ledger entries are immutable.
5. Non-overdraft user accounts cannot become negative.
6. An idempotency key cannot be reused for a semantically different request.
7. The balance projection must match the ledger-derived balance.
8. The outbox event must be created in the same database transaction as the financial state.
9. Corrections must use compensating transactions.
10. Orphan entries and orphan projections must not exist.

## 7.2 Choose a posting boundary

Evaluate the following options.

### Option A — Controlled database posting function

The application calls one trusted database function that:

- accepts the transaction header and entries;
- validates idempotency;
- locks accounts in deterministic order;
- validates balances;
- inserts the transaction;
- inserts ledger entries;
- verifies debit and credit totals;
- updates the balance projection;
- inserts the outbox event;
- marks the transaction as posted.

Advantages:

- PostgreSQL becomes the final enforcement boundary;
- every application path uses the same posting mechanism;
- direct mutations can be revoked.

Trade-offs:

- more domain logic lives in PostgreSQL;
- function testing and deployment become more complex.

### Option B — Deferred constraint trigger

Keep the application-driven transaction, but validate the invariant before commit.

Advantages:

- most domain logic remains in Go;
- easier to integrate into the current architecture.

Trade-offs:

- triggers can be harder to understand and debug;
- cross-row validation is more complex;
- error handling must remain clear.

## 7.3 Add scheduled invariant verification

A recurring verification job should detect:

- unbalanced transactions;
- projection mismatches;
- orphan entries;
- semantic duplicates;
- missing outbox events;
- stale pending transactions;
- unexpected sequence or timestamp anomalies.

## 7.4 Define a safe repair policy

Repairs must never rely on manual direct updates.

For every mismatch:

1. classify the issue;
2. freeze the affected account when necessary;
3. identify the source event;
4. create a compensating transaction;
5. rebuild or recalculate the projection;
6. preserve evidence;
7. close the incident with an audit trail.

## Deliverables

- `docs/database/ledger-invariants.md`
- ADR for the selected posting boundary.
- migrations that enforce the chosen approach.
- invariant verification SQL.
- automated invariant tests.
- ledger repair runbook.

## Acceptance Criteria

- PostgreSQL rejects an unbalanced posted transaction.
- Direct update or delete against immutable ledger data fails.
- Projection mismatches are detected automatically.
- Retries and concurrent postings do not create duplicate financial effects.
- Every repair leaves an audit trail.

---

# 8. Phase 3 — P0 Production-Safe Migration Framework

## Objective

Prevent schema changes from causing blocking, table rewrites, corruption, or prolonged outage.

## 8.1 Create a migration policy

Every migration must be classified.

| Category | Example | Requirement |
|---|---|---|
| Safe metadata change | Add nullable column without default | Standard review |
| Online index | Add index to a large table | `CONCURRENTLY` |
| Constraint validation | Add FK or CHECK to a large table | `NOT VALID`, then `VALIDATE` |
| Data backfill | Populate a new column | Batched and resumable |
| Rewrite risk | Alter type or risky default | Mandatory rehearsal |
| Destructive | Drop column or table | Zero-read proof and delayed cleanup |

## 8.2 Add migration session guards

Migration sessions should use bounded timeouts:

```sql
SET lock_timeout = '3s';
SET statement_timeout = '15min';
SET idle_in_transaction_session_timeout = '60s';
```

Final values should be environment-specific.

## 8.3 Enforce expand–migrate–contract

Recommended column-change sequence:

1. Add a new nullable column.
2. Deploy dual-write.
3. Backfill in batches.
4. Verify completeness.
5. Deploy reads from the new column.
6. Stop writes to the old column.
7. Add constraints.
8. Drop the old column in a later release.

## 8.4 Build a backfill framework

Every backfill must:

- run in bounded batches;
- persist checkpoints;
- resume after interruption;
- support throttling;
- avoid long transactions;
- report progress;
- support safe cancellation;
- monitor WAL, replication lag, CPU, I/O, locks, and dead tuples.

## 8.5 Add a migration linter

CI should reject or require an explicit waiver for:

- non-concurrent `CREATE INDEX` on large tables;
- `ALTER COLUMN TYPE`;
- unbounded `UPDATE`;
- unbounded `DELETE`;
- immediate `SET NOT NULL`;
- `DROP COLUMN`;
- `TRUNCATE`;
- `SECURITY DEFINER` without privilege hardening;
- foreign keys without supporting indexes;
- migrations without rollback or roll-forward strategy.

## 8.6 Rehearse migrations at production-like scale

Before a large migration:

- restore a production-like snapshot;
- execute the migration;
- measure lock duration;
- measure WAL generation;
- detect table rewrites;
- measure replica lag;
- test rollback or roll-forward;
- record expected duration and resource usage.

## Deliverables

- `docs/database/migrations/migration-policy.md`
- `docs/database/migrations/expand-contract-guide.md`
- `docs/database/migrations/backfill-guide.md`
- migration linter.
- rehearsal report template.
- migration-readiness checklist.

## Acceptance Criteria

- CI detects risky DDL.
- Every high-risk migration has rehearsal evidence.
- No unbounded backfill is permitted.
- Lock timeouts prevent indefinite blocking.
- Roll-forward is the primary recovery strategy for destructive migrations.

---

# 9. Phase 4 — P1 High Availability and Failover

## Objective

Remove the primary PostgreSQL instance as a single point of failure.

## 9.1 Define deployment tiers

Recommended initial grouping:

### Tier 0 — Financial core

- Ledger
- Payin
- Payout

### Tier 1 — Transaction support

- Vendor
- Fraud
- Auth
- Gateway

### Tier 2 — Operational workloads

- Admin
- Assurance
- reporting support

Nine separate clusters are not required immediately. Separation should be based on:

- criticality;
- workload profile;
- blast radius;
- compliance boundary;
- RPO and RTO.

## 9.2 Build an HA topology

Minimum requirements:

- one primary;
- at least one standby;
- different availability zones;
- automated health checks;
- controlled failover;
- fencing;
- a stable application endpoint;
- continuous WAL archiving;
- clear synchronous versus asynchronous replication policy.

## 9.3 Establish connection management

Add PgBouncer or a managed connection proxy.

Define a connection budget:

```text
PostgreSQL max_connections
- superuser reserve
- migration reserve
- monitoring
- backup
- administrative access
- application pools
- failover headroom
```

Each service must define:

- maximum open connections;
- maximum idle connections;
- connection lifetime;
- acquisition timeout;
- retry with jitter;
- overload protection.

## 9.4 Perform failover tests

Test at least:

1. primary termination while idle;
2. primary termination during a financial posting;
3. primary termination after commit but before response;
4. network partition between primary and standby;
5. high replica lag;
6. application reconnect storm;
7. failback to a replacement primary;
8. backup and WAL continuity after failover.

## Deliverables

- `docs/database/ha/topology.md`
- `docs/database/ha/connection-budget.md`
- `docs/database/ha/failover-runbook.md`
- failover game-day report.
- measured recovery time.

## Acceptance Criteria

- Failover does not create duplicate financial effects.
- Dual-primary behavior cannot occur.
- Application reconnects do not exhaust PostgreSQL connections.
- Recovery time meets the target.
- Backup and WAL archiving remain healthy after failover.

---

# 10. Phase 5 — P1 Backup, PITR, and Disaster Recovery Evidence

## Objective

Prove that the system can recover from real failure conditions.

## 10.1 Separate the backup failure domain

Production backups should be:

- stored outside the database host;
- stored in object storage;
- ideally stored in a separate account or project;
- encrypted;
- protected with immutable retention or object lock;
- governed by lifecycle policy;
- inaccessible to runtime application roles;
- replicated cross-region when required by RPO/RTO.

## 10.2 Define the backup policy

Recommended baseline:

- weekly full backup;
- daily differential backup;
- continuous WAL archiving;
- retention based on data classification;
- monthly long-term backup;
- automatic backup metadata verification;
- immediate alerting for archive failure or delay.

## 10.3 Test restore scenarios

Test:

- latest restore;
- point-in-time recovery before an erroneous transaction;
- recovery from corrupted primary storage;
- recovery when the primary backup repository is unavailable;
- recovery into another region;
- recovery of a cluster containing multiple service databases.

## 10.4 Verify every restore

After restore:

- PostgreSQL starts successfully;
- all schemas and migration versions are correct;
- all required extensions are available;
- row counts are plausible;
- ledger invariants pass;
- projections match;
- outbox state is consistent;
- application smoke tests pass;
- production credentials are disabled in isolated environments;
- the recovery timeline is correct.

## 10.5 Measure RPO and RTO

Record:

- incident declaration time;
- restore start time;
- database-ready time;
- application smoke-test completion time;
- latest recovered transaction;
- estimated data loss;
- operator actions;
- bottlenecks.

## Deliverables

- `docs/database/backup/backup-policy.md`
- `docs/database/backup/pitr-runbook.md`
- `docs/database/backup/offsite-backup-design.md`
- quarterly restore reports.
- measured RPO/RTO evidence.

## Acceptance Criteria

- Restore succeeds from an off-host backup.
- PITR can stop at a selected timestamp.
- All financial invariants pass after restore.
- RPO and RTO are backed by actual measurements.
- An engineer other than the original author can complete the restore.

---

# 11. Phase 6 — P1 Database Observability

## Objective

Detect degradation before it becomes an outage or correctness incident.

## 11.1 Enable database telemetry

Consider enabling:

- `pg_stat_statements`;
- `track_io_timing`;
- lock-wait logging;
- slow-query logging;
- autovacuum logging;
- checkpoint logging;
- connection metrics;
- replication metrics;
- backup metrics.

## 11.2 Minimum dashboard coverage

### Connections

- active connections;
- idle connections;
- waiting connections;
- connection utilization;
- pool utilization;
- pool wait count;
- pool wait duration;
- connection errors.

### Queries

- top queries by total time;
- top queries by mean time;
- top queries by calls;
- top queries by shared blocks read;
- temporary file usage;
- query error rate.

### Transactions

- transactions per second;
- rollback rate;
- long-running transactions;
- idle-in-transaction sessions;
- transaction age.

### Locks

- blocked sessions;
- blocker query;
- lock-wait duration;
- deadlock count;
- relation-level lock contention.

### Vacuum

- dead-tuple ratio;
- last autovacuum;
- last analyze;
- autovacuum duration;
- transaction ID age;
- multixact age.

### WAL and replication

- WAL bytes generated;
- archive failures;
- archive lag;
- replication lag;
- replication-slot retained WAL;
- timeline changes.

### Storage

- disk utilization;
- growth per database;
- growth per table;
- growth per index;
- bloat estimate;
- temporary disk usage.

### Financial integrity

- unbalanced transactions;
- projection mismatches;
- stale pending transactions;
- missing outbox events;
- reconciliation mismatches;
- unprocessed callback age.

## 11.3 Define an alert strategy

Use severity levels:

- **Critical:** correctness risk or imminent outage;
- **High:** severe degradation;
- **Warning:** capacity or maintenance risk;
- **Info:** planned operational action.

Every alert must define:

- owner;
- threshold;
- evaluation duration;
- runbook;
- deduplication strategy;
- escalation path.

## Deliverables

- `docs/database/observability/metrics-catalog.md`
- `docs/database/observability/alert-catalog.md`
- Grafana dashboards or equivalent.
- alert-to-runbook mappings.

## Acceptance Criteria

- Every P0 database failure mode has an alert.
- Alerts include actionable context.
- No alert exists without an owner and runbook.
- Long transactions, lock waits, archive failures, disk pressure, and integrity mismatches are automatically detected.

---

# 12. Phase 7 — P1 Capacity and Growth Model

## Objective

Turn benchmark results into a capacity contract that supports production planning.

## 12.1 Create one canonical capacity model

The authoritative document must include:

- commit hash;
- environment;
- CPU;
- memory;
- PostgreSQL version;
- PostgreSQL configuration;
- dataset size;
- number of accounts;
- number of initial transactions;
- scenario;
- test duration;
- throughput;
- latency p50/p95/p99;
- error rate;
- dropped iterations;
- pool saturation;
- lock waits;
- WAL generation;
- storage growth;
- outbox lag;
- autovacuum behavior.

## 12.2 Define workload scenarios

At minimum:

1. normal P2P transfer;
2. hot-account contention;
3. webhook burst;
4. mixed pay-in, transfer, and payout;
5. retry storm;
6. reconciliation import;
7. long-running backfill under production traffic;
8. backup under production traffic.

## 12.3 Define Maximum Sustainable Service Load

MSSL should require:

- no dropped iterations;
- latency within SLO;
- errors within threshold;
- no sustained pool saturation;
- controlled lock waits;
- stable WAL and storage behavior;
- memory reaching a plateau;
- autovacuum keeping up;
- outbox lag returning to normal after a burst.

Until production evidence exists, planning load should remain below approximately 40–50% of confirmed MSSL.

## 12.4 Build a growth budget

For every permanent table, calculate:

```text
rows per business event
bytes per row
index amplification
events per day
monthly growth
annual growth
backup impact
restore impact
vacuum impact
```

Prioritize:

- ledger entries;
- outbox events;
- callback inbox;
- webhook events;
- reconciliation data;
- audit logs;
- idempotency records.

## 12.5 Close the hot-account soak-test gap

Evaluate:

- bounded retention for non-financial operational payloads;
- archival of raw payloads to object storage;
- separation of searchable metadata from raw request bodies;
- compression;
- time partitioning;
- reduction of duplicate audit writes;
- rerunning the test before introducing account sharding.

## Deliverables

- `docs/performance/capacity-model.md`
- raw evidence for every scenario.
- `docs/database/data-growth-model.md`
- MSSL and planning limit.
- partitioning activation thresholds.

## Acceptance Criteria

- Capacity has one source of truth.
- Every result can be reproduced.
- Hot-account soak no longer causes unbounded memory or storage growth.
- Planning limits are evidence-based.
- Test datasets reflect target cardinality.

---

# 13. Phase 8 — P2 Index and Query Governance

## Objective

Keep critical queries efficient without creating excessive index overhead.

## 13.1 Build a query inventory

Document:

- critical query;
- execution frequency;
- expected cardinality;
- expected latency;
- index dependency;
- locking behavior;
- pagination strategy.

## 13.2 Perform an index review

Audit:

- unused indexes;
- overlapping indexes;
- duplicate indexes;
- foreign keys without supporting indexes;
- low-selectivity indexes;
- high write-amplification indexes;
- invalid indexes;
- index bloat.

Do not remove an index solely because `idx_scan = 0`. Consider:

- PostgreSQL restart;
- statistics reset;
- monthly or quarterly workloads;
- incident-only workflows;
- maintenance or integrity operations.

## 13.3 Add query-plan regression testing

For every critical query:

- store a representative query;
- run `EXPLAIN (ANALYZE, BUFFERS)`;
- test with production-like cardinality;
- define maximum latency;
- detect unexpected sequential scans;
- detect sort spills;
- detect nested-loop explosions.

## 13.4 Use scalable pagination

For large tables:

- use keyset pagination;
- avoid large `OFFSET`;
- use deterministic ordering;
- align indexes with filters and sort order.

## Deliverables

- `docs/database/query-catalog.md`
- `docs/database/index-review.md`
- critical query regression tests.
- index cleanup migrations.

## Acceptance Criteria

- Every critical query has an expected plan.
- No hot-path foreign key lacks a supporting index.
- Redundant indexes have been verified before removal.
- Query plans are tested at target cardinality.

---

# 14. Phase 9 — P2 Autovacuum and Table Maintenance

## Objective

Prevent bloat, transaction ID exhaustion, and query degradation.

## 14.1 Define per-table autovacuum policies

Large or high-churn tables may require:

- lower `autovacuum_vacuum_scale_factor`;
- lower `autovacuum_analyze_scale_factor`;
- custom thresholds;
- custom vacuum cost limits;
- additional workers;
- higher maintenance memory.

## 14.2 Monitor freeze age

Alert on:

- database transaction age;
- table transaction age;
- multixact age;
- tables that are not vacuuming;
- canceled autovacuum workers;
- long-running transactions preventing cleanup.

## 14.3 Manage bloat safely

Preferred order:

1. fix the root cause;
2. run normal vacuum;
3. use `REINDEX CONCURRENTLY`;
4. use an online rebuild tool when necessary;
5. avoid routine `VACUUM FULL` on critical production tables.

## 14.4 Keep statistics current

After bulk load or backfill:

- analyze affected tables;
- verify statistics;
- re-check execution plans;
- ensure the planner is not using stale cardinality estimates.

## Deliverables

- `docs/database/maintenance/autovacuum-policy.md`
- bloat dashboard.
- maintenance runbook.
- per-table storage parameters.

## Acceptance Criteria

- Dead-tuple growth reaches a stable plateau.
- No table approaches transaction ID wraparound.
- Large backfills do not leave stale statistics for long periods.
- Bloat remediation is evidence-based.

---

# 15. Phase 10 — P2 Retention, Archival, and Partitioning

## Objective

Manage long-term data growth without sacrificing auditability.

## 15.1 Classify data

| Class | Example | Treatment |
|---|---|---|
| Financial source of truth | ledger entries | Immutable, long retention |
| Financial evidence | callback payload, settlement report | Immutable archive |
| Projection | account balance snapshot | Rebuildable |
| Operational | published outbox row | Bounded retention |
| Security audit | authentication event | Policy-based retention |
| Debug telemetry | request log | Short retention |

## 15.2 Separate hot and cold data

For raw callback or webhook data:

- keep searchable metadata in PostgreSQL;
- move raw payloads to immutable object storage where appropriate;
- store content hashes;
- store object keys;
- store receive timestamps;
- store signature-verification results.

## 15.3 Define partitioning triggers

Do not partition a table only because it may become large.

Introduce partitioning when evidence shows:

- retention deletes are too expensive;
- autovacuum cannot keep up;
- indexes exceed the memory budget;
- backup or restore misses its target;
- time-range queries dominate;
- table growth exceeds an agreed threshold;
- maintenance isolation is required.

## 15.4 Apply a partition design checklist

Every partitioned table should define:

- partition key;
- query alignment;
- retention alignment;
- primary and unique constraint compatibility;
- default partition;
- future partition creation;
- missing-partition alert;
- detach and archive process;
- per-partition indexes;
- per-partition autovacuum settings;
- migration path from the non-partitioned table.

## Deliverables

- `docs/database/data-classification.md`
- `docs/database/retention-matrix.md`
- `docs/database/archival-design.md`
- partitioning activation ADR.
- archive retrieval test.

## Acceptance Criteria

- Every table has a retention owner.
- Raw evidence remains verifiable after archival.
- Retention jobs are bounded and resumable.
- Partitioning is activated only after an evidence-based threshold is reached.

---

# 16. Phase 11 — P2 Reconciliation and Data Repair

## Objective

Ensure differences between the internal ledger and external settlement can be detected and repaired safely.

## 16.1 Make report import idempotent

Add unique identity for each settlement report using:

- gateway or vendor;
- report date;
- external report ID;
- content digest.

Filename alone must not be treated as the report identity.

## 16.2 Define reconciliation states

Recommended states:

- matched;
- internal-only;
- external-only;
- amount mismatch;
- status mismatch;
- duplicate external record;
- duplicate internal record;
- unresolved;
- compensated.

## 16.3 Preserve reconciliation evidence

Store:

- source report digest;
- source object location;
- import timestamp;
- parser version;
- reconciliation-rule version;
- operator action;
- compensation reference.

## 16.4 Define the repair workflow

Repair must:

- never modify historical ledger entries;
- create compensating transactions;
- require authorization based on financial materiality;
- preserve a complete audit trail;
- support repeated reconciliation.

## Deliverables

- reconciliation schema improvements.
- report deduplication constraints.
- repair workflow documentation.
- reconciliation test datasets.

## Acceptance Criteria

- The same report cannot be imported twice as separate batches.
- Every mismatch has a deterministic classification.
- Repair never removes evidence.
- Reconciliation can be rerun idempotently.

---

# 17. Phase 12 — P2 Database Testing Strategy

## Objective

Make correctness, migration safety, and recoverability continuously testable.

## Test Layers

### Schema tests

- constraints;
- defaults;
- nullability;
- ownership;
- privileges;
- triggers;
- RLS;
- extensions.

### Transaction tests

- concurrent debit;
- deterministic lock ordering;
- retry after serialization failure;
- crash after commit before response;
- duplicate idempotency;
- callback replay;
- insufficient balance.

### Migration tests

- upgrade from older versions;
- clean installation;
- rollback or roll-forward;
- production-size migration;
- concurrent traffic;
- partial backfill resume.

### Recovery tests

- backup restore;
- PITR;
- corrupted primary;
- failover;
- WAL archive outage.

### Capacity tests

- spike;
- soak;
- hot account;
- data growth;
- vacuum pressure;
- backup during load.

### Chaos tests

- terminate the application after database commit;
- terminate a database connection;
- slow disk;
- nearly full storage;
- network partition;
- replica lag;
- Redis or RabbitMQ unavailability while PostgreSQL remains healthy.

## Deliverables

- database test matrix.
- automated integration suite.
- game-day scenarios.
- failure-injection scripts.

## Acceptance Criteria

- Every critical invariant has an automated test.
- Retry and crash boundaries are tested.
- Migration from at least two older versions is tested.
- Restore tests can be run repeatedly.

---

# 18. Phase 13 — P3 Operational Governance

## Objective

Turn database reliability into a recurring operating process rather than a one-time project.

## Daily

- backup status;
- WAL archive status;
- disk capacity;
- replication lag;
- integrity checks;
- failed reconciliation;
- long-running transactions;
- stale outbox events.

## Weekly

- table and index growth;
- slow-query review;
- unused and invalid index review;
- autovacuum health;
- connection utilization;
- backup-chain verification.

## Monthly

- capacity trends;
- storage forecast;
- privilege drift;
- restore sample;
- migration review;
- PostgreSQL patch review;
- high-severity incident review.

## Quarterly

- full PITR drill;
- failover game day;
- production-size migration rehearsal;
- disaster recovery drill;
- access certification;
- RPO/RTO review;
- partitioning-threshold review.

## Annually

- major-version roadmap;
- threat-model update;
- cryptographic key-rotation review;
- backup-retention review;
- compliance-retention review;
- architecture and blast-radius review.

## Deliverables

- DBA operating calendar.
- database SLOs.
- quarterly readiness reports.
- incident-review template.
- database risk register.

## Acceptance Criteria

- Every recurring activity has an owner.
- Evidence is retained in the repository or evidence storage.
- Findings create tracked issues with due dates.
- SLOs and thresholds are periodically reviewed.

---

# 19. Recommended Repository Structure

```text
docs/
└── database/
    ├── README.md
    ├── production-readiness.md
    ├── database-inventory.md
    ├── schema-size-baseline.md
    ├── postgresql-configuration-baseline.md
    ├── supported-versions.md
    ├── ledger-invariants.md
    ├── data-growth-model.md
    ├── data-classification.md
    ├── retention-matrix.md
    ├── archival-design.md
    ├── query-catalog.md
    ├── index-review.md
    ├── security/
    │   ├── role-model.md
    │   └── privilege-matrix.md
    ├── migrations/
    │   ├── migration-policy.md
    │   ├── expand-contract-guide.md
    │   ├── backfill-guide.md
    │   └── readiness-checklist.md
    ├── ha/
    │   ├── topology.md
    │   ├── connection-budget.md
    │   └── failover-runbook.md
    ├── backup/
    │   ├── backup-policy.md
    │   ├── pitr-runbook.md
    │   └── offsite-backup-design.md
    ├── observability/
    │   ├── metrics-catalog.md
    │   └── alert-catalog.md
    └── maintenance/
        ├── autovacuum-policy.md
        └── maintenance-runbook.md

scripts/
└── database/
    ├── inventory.sql
    ├── audit-privileges.sql
    ├── audit-security-definer.sql
    ├── audit-indexes.sql
    ├── audit-constraints.sql
    ├── verify-ledger.sql
    ├── verify-projections.sql
    ├── restore-smoke-test.sh
    └── capacity-snapshot.sh
```

---

# 20. Suggested Implementation Order

## Milestone 1 — Database Safety Foundation

- [ ] Complete the database inventory.
- [ ] Audit privileges.
- [ ] Revoke `PUBLIC EXECUTE`.
- [ ] Harden default privileges.
- [ ] Add runtime-role regression tests.
- [ ] Define ledger invariants.
- [ ] Enforce balanced posting at the database layer.
- [ ] Add migration timeouts and a migration linter.

**Exit criteria:** no known P0 security or correctness gap remains.

## Milestone 2 — Recovery Foundation

- [ ] Create off-host backups.
- [ ] Complete a full restore.
- [ ] Complete PITR.
- [ ] Automate restore verification.
- [ ] Measure RPO and RTO.
- [ ] Establish a quarterly restore procedure.

**Exit criteria:** another engineer can restore the cluster from documented backup procedures.

## Milestone 3 — Availability Foundation

- [ ] Build primary–standby topology.
- [ ] Define connection budgets.
- [ ] Add PgBouncer or a managed proxy.
- [ ] Implement controlled failover.
- [ ] Verify fencing.
- [ ] Run a failover game day.

**Exit criteria:** primary failure does not cause corruption or uncontrolled downtime.

## Milestone 4 — Observability and Capacity

- [ ] Build database dashboards.
- [ ] Define the alert catalog.
- [ ] Publish the canonical capacity report.
- [ ] Publish the data-growth model.
- [ ] Remediate the hot-account soak issue.
- [ ] Define planning limits.

**Exit criteria:** saturation and growth risks can be predicted before failure.

## Milestone 5 — Long-Term Operations

- [ ] Define retention and archival.
- [ ] Define partitioning activation criteria.
- [ ] Add reconciliation deduplication.
- [ ] Tune autovacuum.
- [ ] Add query-plan regression tests.
- [ ] Establish the DBA operating calendar.

**Exit criteria:** the database can be operated repeatedly without depending on one individual.

---

# 21. Definition of Done

Seev may be considered **database production-ready for a controlled launch** only when the following conditions are met.

## Correctness

- [ ] An unbalanced posted ledger transaction is rejected by PostgreSQL.
- [ ] Immutable records cannot be modified by runtime roles.
- [ ] Projection verification runs automatically.
- [ ] Idempotency conflicts validate semantic request equivalence.
- [ ] Retry, crash, and concurrency tests pass.

## Security

- [ ] No sensitive `SECURITY DEFINER` function is executable by `PUBLIC`.
- [ ] Runtime roles follow least privilege.
- [ ] Migration identities are separate.
- [ ] Privilege-drift tests run in CI.
- [ ] Cross-service database access is denied.

## Migration

- [ ] Migration linting is active.
- [ ] Large indexes use an online strategy.
- [ ] Backfills are batched and resumable.
- [ ] Risky migrations have rehearsal evidence.
- [ ] Roll-forward procedures exist.

## Availability

- [ ] A standby is available.
- [ ] Failover has been tested.
- [ ] Fencing has been verified.
- [ ] Connection budgets are documented.
- [ ] Reconnect-storm tests pass.

## Recovery

- [ ] Backups are stored outside the primary host.
- [ ] Latest restore succeeds.
- [ ] PITR succeeds.
- [ ] Financial invariants pass after restore.
- [ ] RPO and RTO are measured.

## Observability

- [ ] Connection, query, lock, vacuum, WAL, storage, and integrity dashboards are available.
- [ ] Every critical alert has a runbook.
- [ ] Archive failure is detected.
- [ ] Long-running transactions are detected.
- [ ] Disk pressure is detected.

## Capacity

- [ ] A canonical capacity report exists.
- [ ] Soak tests pass.
- [ ] A data-growth model exists.
- [ ] Planning limits are defined.
- [ ] Partitioning thresholds are defined.

## Operations

- [ ] Quarterly restore drills are scheduled.
- [ ] Failover game days are scheduled.
- [ ] DBA ownership is clear.
- [ ] Incident workflows exist.
- [ ] The database risk register is active.

---

# 22. Recommended First 10 Pull Requests

1. **Audit and harden `SECURITY DEFINER` privileges**
2. **Add database role and privilege regression tests**
3. **Document ledger invariants and enforce balanced posting**
4. **Add migration policy and risky-DDL linting**
5. **Add database inventory and baseline scripts**
6. **Create the canonical database observability metrics catalog**
7. **Design an off-host pgBackRest repository**
8. **Automate restore verification and ledger-integrity checks**
9. **Create the canonical capacity and data-growth model**
10. **Fix permanent callback and audit-table growth identified by soak testing**

---

# 23. Final Recommendation

Do not start with sharding, partitioning, or adding many indexes.

The highest-ROI order is:

1. correctness;
2. privileges;
3. migration safety;
4. restore evidence;
5. failover;
6. observability;
7. capacity;
8. data lifecycle;
9. optimization.

Core principle:

> Database production readiness is not proven by the number of database features. It is proven by the system's ability to preserve correctness, availability, and recoverability when real failures occur.
