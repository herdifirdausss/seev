# Seev `scripts/` Audit & Improvement Plan

> Repository: `herdifirdausss/seev`
> Scope: `scripts/` and directly related execution surfaces (`.github/workflows/ci.yml`, `Makefile`)
> Audited branch: `main`
> Audit date: 2026-08-09

## 1. Objective

This plan audits the repository's operational and test automation under `scripts/` with emphasis on:

- correctness and failure semantics;
- destructive-operation safety;
- backup / restore / disaster-recovery completeness;
- security of secret handling;
- maintainability and change blast radius;
- consistency of service inventory;
- test reliability and false-positive prevention;
- developer experience and portability;
- CI enforcement and supply-chain safety.

The goal is **not** to add more scripts. The repository already has broad automation coverage. The goal is to make the existing automation easier to reason about, harder to misuse, and less likely to silently produce incorrect evidence.

---

## 2. Executive Summary

The current `scripts/` area is already more mature than a typical application repository. It contains real automation for smoke/E2E, chaos, load testing, database bootstrap/migration, backup/restore, privacy workflows, DR drills, supply-chain checks, and platform verification.

The main risk has shifted from **missing automation** to **automation complexity and drift**.

### Highest-priority findings

1. **Migration failures can be silently ignored by the shared test harness.**
   - `scripts/lib.sh::apply_migrations()` executes migration SQL with `ON_ERROR_STOP=0` and `|| true`.
   - A genuine migration regression can therefore be hidden while smoke/E2E/chaos tests continue against an incomplete schema.

2. **Service inventory has drifted after the introduction of `vendor`.**
   - Fresh Postgres bootstrap creates/migrates nine service databases including `vendor`.
   - The backup role also includes `vendor`.
   - `backup-manifest.sh` and `restore-cluster.sh` still enumerate only eight services and omit `vendor` from migration evidence/inventory.
   - pgBackRest physical backup still captures the PostgreSQL cluster, so this is primarily an **evidence/verification completeness problem**, not proof that vendor data is absent from the physical backup.

3. **Critical orchestration logic is concentrated in very large Bash files.**
   - `scripts/lib.sh`: ~1,400 lines.
   - `scripts/chaos-test.sh`: ~2,000 lines.
   - `scripts/business-e2e.sh`: ~700 lines.
   - `scripts/dr-drill.sh`: ~550 lines.
   - This creates a large blast radius for otherwise small changes.

4. **Backup/DR scripts are inconsistent in how they protect secret values.**
   - `pgbackrest.sh` intentionally reads the repository passphrase inside the container.
   - Other backup/DR scripts pass the passphrase value through Docker `-e ...=<secret>` arguments.
   - Standardize the safer pattern.

5. **Shell quality gates exist, but are intentionally weak.**
   - CI pins ShellCheck, which is good.
   - It currently runs only with `--severity=error`.
   - There is no formatting gate such as `shfmt`.

### Recommended delivery order

- **Phase 0:** fix false-positive / incomplete verification risks.
- **Phase 1:** introduce a single service registry and consistent safety primitives.
- **Phase 2:** split shared Bash infrastructure and reorganize scripts without breaking callers.
- **Phase 3:** tighten CI, parsing, portability, documentation, and tests.

---

## 3. What Is Already Good

These patterns should be preserved during refactoring.

### 3.1 Strong fail-fast defaults

Most Bash entrypoints use:

```bash
set -euo pipefail
```

and validate arguments before executing sensitive operations.

Examples:

- `scripts/migrate.sh`
- `scripts/load-test.sh`
- `scripts/restore-cluster.sh`
- `scripts/post-restore-security.sh`
- `scripts/ci/*`

### 3.2 Good defensive design in the load-test harness

`scripts/load-test.sh` already has several useful safety properties:

- explicit `SEEV_LOAD_ACK=disposable-only` acknowledgement;
- loopback-only base URL;
- production environment refusal;
- mock-vendor enforcement;
- constrained artifact paths;
- unique Compose project naming;
- run locking;
- cleanup on failure.

This should become the model for other destructive or environment-sensitive scripts.

### 3.3 Good isolation in restore / DR flows

`restore-cluster.sh` explicitly isolates the restore into a dedicated Compose project and volume and avoids starting application traffic before verification.

`dr-drill.sh` also separates gameday and restored projects and explicitly scopes cleanup.

These are good operational invariants and should not be weakened during restructuring.

### 3.4 Good post-restore security fence

`post-restore-security.sh` has several strong patterns:

- dry-run by default;
- explicit `--confirm` for mutation;
- refuses to proceed while the app health endpoint is reachable;
- avoids logging token/session values;
- verifies the post-condition after revocation.

This is a good template for destructive operational CLIs.

### 3.5 CI already pins ShellCheck

The repository does not need a new basic ShellCheck setup. CI already installs a pinned version and runs it across `scripts/**/*.sh`.

The improvement should be **tightening and standardizing** the existing gate rather than replacing it.

---

# 4. Actionable Improvement Plan

## P0 — Verification Integrity

### P0.1 Make migration application fail closed

**Problem**

`scripts/lib.sh::apply_migrations()` currently suppresses every migration error:

```bash
psql ... -v ON_ERROR_STOP=0 ... || true
```

This was intended to tolerate migrations that were already applied on reusable development volumes, but it also hides real errors such as:

- syntax errors;
- missing dependency objects;
- incompatible DDL;
- permission failures;
- partial migration application;
- migration ordering regressions.

A smoke/chaos/E2E test that starts after such an error is not reliable evidence.

**Action**

Replace raw best-effort SQL replay with the same migration semantics used by the normal migration path.

Preferred approach:

1. Add a reusable function such as:

   ```text
   migrate_service <service>
   ```

2. Call `golang-migrate` for each service database.
3. Use the service's actual `schema_migrations_<service>` table.
4. Fail immediately when a migration is dirty or cannot be applied.
5. Preserve support for reused local volumes by allowing the migration tool to detect the current version instead of replaying every `.up.sql` file.

If `golang-migrate` cannot reasonably be required inside this harness, implement an explicit migration-state check and execute **only unapplied migrations**, with `ON_ERROR_STOP=1`.

**Do not** solve this by adding more `IF NOT EXISTS` to arbitrary historical migration files.

**Files**

- `scripts/lib.sh`
- `scripts/migrate.sh`
- optionally `scripts/postgres-init/03-service-migrations.sh`

**Acceptance criteria**

- [ ] A syntactically invalid migration makes smoke/E2E/chaos setup fail.
- [ ] A permission error makes setup fail.
- [ ] Re-running the harness against an already migrated local volume succeeds without suppressing arbitrary SQL errors.
- [ ] Dirty migration state is reported explicitly.
- [ ] CI contains a regression test proving migration errors cannot be swallowed.

---

### P0.2 Add a service-inventory consistency gate

**Problem**

The repository currently contains conflicting service lists.

Fresh DB bootstrap includes:

```text
ledger auth payin payout fraud gateway vendor adminbff assurance
```

But backup/restore evidence still uses:

```text
ledger auth payin payout fraud gateway adminbff assurance
```

This means `vendor` can be omitted from backup-manifest migration evidence and restore inventory while the output still appears structurally valid.

**Action**

Create one canonical machine-readable service registry.

Recommended format:

```text
config/service-inventory.tsv
```

Example columns:

```text
service | migration_dir | database | app_role | backup_required | load_enabled | deployable
```

Example:

```text
ledger    ledger          seev_ledger      ledger_app      true  true  true
vendor    vendor-service  seev_vendor      vendor_app      true  true  true
adminbff  adminbff        seev_adminbff    adminbff_app    true  false true
```

A simple TSV is preferable here because both `/bin/sh` bootstrap scripts and Bash scripts can consume it without introducing a YAML parser dependency.

Then migrate these hard-coded lists to the registry:

- `scripts/lib.sh`
- `scripts/migrate.sh`
- `scripts/postgres-init/02-service-dbs.sh`
- `scripts/postgres-init/03-service-migrations.sh`
- `scripts/postgres-init/04-backup-role.sh`
- `scripts/load-postgres-init/*`
- `scripts/backup-manifest.sh`
- `scripts/restore-cluster.sh`
- `scripts/dr-drill.sh`
- any CI capability/service inventory checks that duplicate the same concept.

Add a CI check:

```text
scripts/ci/check-service-inventory.sh
```

The check should fail when:

- a `services/*/migrations` directory exists but is not represented;
- a deployable service has no configured database/migration mapping;
- a `backup_required=true` service is absent from backup/restore verification;
- a service references a migration directory that does not exist.

**Acceptance criteria**

- [ ] `vendor` appears in backup manifest migration evidence.
- [ ] `vendor` appears in restore inventory.
- [ ] adding a new service without updating the canonical registry causes CI to fail clearly.
- [ ] service lists no longer need to be manually synchronized across multiple operational scripts.

---

## P1 — Security and Operational Safety

### P1.1 Standardize backup/DR secret handling

**Problem**

`pgbackrest.sh` correctly avoids placing the repository passphrase value directly in the host Docker command arguments by reading it inside the container.

Other scripts still use patterns similar to:

```bash
docker compose exec -T \
  -e PGBACKREST_REPO1_CIPHER_PASS="$(cat .../pgbackrest_repo_passphrase)" \
  postgres ...
```

or:

```bash
docker run ... -e PGBACKREST_REPO1_CIPHER_PASS="$REPO_PASSPHRASE" ...
```

This creates inconsistent secret exposure characteristics between operational paths.

**Action**

Create a single helper for pgBackRest invocation.

Suggested shape:

```text
scripts/lib/backup.sh
```

Functions:

```text
pgbackrest_compose_exec
pgbackrest_oneshot
```

For one-shot containers:

1. mount the passphrase file read-only as a secret file;
2. start a tiny shell inside the container;
3. read/export the value inside the container process;
4. `exec pgbackrest ...`.

Do not interpolate the secret value into the host command line.

**Files**

- `scripts/backup-manifest.sh`
- `scripts/dr-drill.sh`
- `scripts/restore-cluster.sh`
- `scripts/pgbackrest.sh`

**Acceptance criteria**

- [ ] no pgBackRest repository passphrase value is interpolated into Docker CLI arguments;
- [ ] scripts continue to avoid printing secret values;
- [ ] missing secret files fail with a clear message;
- [ ] local backup, latest restore, and PITR drill still pass.

---

### P1.2 Add explicit safety acknowledgement for migration rollback

**Problem**

`scripts/migrate.sh down` immediately performs:

```text
migrate ... down 1
```

The script supports remote hosts through environment variables and does not require an explicit rollback acknowledgement.

Input validation protects against shell injection, but not against an operator accidentally targeting the wrong environment.

**Action**

For `down` only:

- require `SERVICE` to be explicitly set rather than silently defaulting to `ledger`;
- require an acknowledgement, for example:

  ```text
  SEEV_MIGRATE_DOWN_ACK=<environment>:<service>
  ```

- fail closed when `APP_ENV=production` unless a stronger production-specific override is present;
- print target host, database, service, and current migration version before rollback;
- never print credentials.

Example operator contract:

```bash
APP_ENV=development \
SERVICE=ledger \
SEEV_MIGRATE_DOWN_ACK=development:ledger \
./scripts/migrate.sh down
```

**Acceptance criteria**

- [ ] `down` without an acknowledgement exits with code 2.
- [ ] `down` without an explicit `SERVICE` exits with code 2.
- [ ] target DB/service are printed before execution.
- [ ] automated rollback tests use a disposable database only.

---

### P1.3 Harden `backup-checksums-enable.sh`

**Problem**

This script stops the Compose `postgres` service and mutates the PostgreSQL data directory by enabling checksums. It currently relies on the caller being in the correct Compose context and has no acknowledgement mechanism.

**Action**

Adopt the same safety model already used by the load/DR scripts:

- determine and print the Compose project name;
- reject empty or explicitly protected project names;
- require `SEEV_CHECKSUM_ACK=<project>`;
- verify the exact Postgres volume/container before mutation;
- preserve the existing restart-on-exit behavior;
- add `--check` to inspect checksum state without mutation.

**Acceptance criteria**

- [ ] mutation cannot run accidentally without acknowledgement.
- [ ] `--check` is non-mutating.
- [ ] interruption still brings Postgres back up when the script had stopped it.

---

### P1.4 Introduce a test-harness environment fence

**Problem**

`scripts/lib.sh` contains intentionally weak/local test credentials and fixed cryptographic keys. This is reasonable for an isolated disposable harness, but the safety invariant is currently distributed across callers rather than expressed as one enforceable contract.

**Action**

Add:

```text
assert_test_harness_environment
```

before any service startup.

It should verify at minimum:

- `APP_ENV` is `development`, `test`, `ci`, or an explicit drill/load environment;
- DB/Redis/RabbitMQ addresses are local or the explicitly isolated Compose project;
- mock vendor adapters are used where expected;
- known fixture JWT/crypto secrets are rejected in staging/production mode;
- work directory is isolated and safe to delete.

Keep the existing load-specific stronger fence.

**Acceptance criteria**

- [ ] attempting to start the shared harness with `APP_ENV=production` fails before any service starts.
- [ ] fixture secrets cannot silently become production configuration.
- [ ] smoke/E2E/chaos behavior remains unchanged for local/CI runs.

---

## P1 — Maintainability and Blast-Radius Reduction

### P1.5 Split `scripts/lib.sh` by responsibility

**Problem**

`scripts/lib.sh` has become a large shared runtime for many unrelated workflows. It contains:

- configuration defaults;
- Docker bootstrap;
- migration orchestration;
- binary builds;
- certificate generation;
- service startup/shutdown;
- process management;
- data seeding;
- HTTP helpers;
- JSON parsing;
- KYC helpers;
- ledger assertions;
- notification helpers.

Because many scripts source the whole file, a change in one domain can unintentionally affect all smoke/E2E/chaos/DR callers.

**Action**

Split it incrementally into focused libraries:

```text
scripts/lib/
  core.sh          # logging, die, dependency checks, temp/work-dir handling
  compose.sh       # compose project/container resolution + health waits
  postgres.sh      # psql helpers + DB readiness
  migrations.sh    # service migration orchestration
  services.sh      # build/start/stop service processes
  certs.sh         # local mTLS fixtures
  http.sh          # curl helpers and status assertions
  fixtures.sh      # user/funding/KYC fixture helpers
  assertions.sh    # ledger/projection consistency checks
  backup.sh        # pgBackRest shared invocation
```

Keep `scripts/lib.sh` temporarily as a compatibility aggregator:

```bash
source scripts/lib/core.sh
source scripts/lib/compose.sh
...
```

Then migrate callers gradually.

**Important**

Do **not** perform a big-bang rewrite of `lib.sh` and every E2E script in one PR.

**Acceptance criteria**

- [ ] compatibility `scripts/lib.sh` remains available during migration.
- [ ] each new library can be sourced independently without starting infrastructure.
- [ ] pure helper libraries have unit-style shell tests.
- [ ] smoke, business E2E, chaos, and DR still run after each extraction step.

---

### P1.6 Split the giant scenario scripts into scenario modules

**Problem**

`chaos-test.sh`, `business-e2e.sh`, and `dr-drill.sh` have grown into large procedural scripts. Navigation and isolated testing are increasingly difficult.

**Action**

Keep the user-facing entrypoint stable but move scenarios into modules.

Example:

```text
scripts/tests/chaos/
  runner.sh
  postgres-restart.sh
  redis-failure.sh
  rabbitmq-failure.sh
  payout-breaker.sh
  vendor-boundary.sh
  projection-recovery.sh
```

Entry point remains:

```text
scripts/chaos-test.sh
```

as a thin wrapper during migration.

For business E2E:

```text
scripts/tests/business/
  onboarding.sh
  topup.sh
  transfer.sh
  payout.sh
  pricing.sh
  reconciliation.sh
```

The runner owns setup/cleanup once; scenario files only define scenario functions.

**Acceptance criteria**

- [ ] individual scenarios can be selected without executing unrelated scenario code.
- [ ] setup/cleanup remains centralized.
- [ ] no scenario owns global infrastructure cleanup directly.
- [ ] existing CLI invocations remain compatible.

---

## P2 — Correctness and Parsing Robustness

### P2.1 Replace regex-based JSON parsing

**Problem**

The shared `json_field()` helper uses `sed` to extract JSON string fields. `smoke-test.sh` also contains a second implementation with slightly different whitespace behavior.

Regex parsing is fragile for:

- escaped quotes;
- nested objects;
- duplicate keys;
- non-string values;
- formatting changes.

The duplicate implementation is already evidence of drift.

**Action**

Choose one real JSON parser and standardize it.

Recommended options, in order:

1. `jq` with an explicit dependency check;
2. a tiny repository Go helper such as `tools/jsonget` if avoiding a new system dependency is important.

Suggested API:

```bash
json_get '.id'
json_get_required '.access_token'
```

Delete every local `json_field()` implementation after migration.

**Acceptance criteria**

- [ ] formatted JSON with whitespace works.
- [ ] escaped string values work.
- [ ] missing required field fails clearly.
- [ ] smoke/E2E scripts use one implementation only.

---

### P2.2 Validate operator CLI path/query inputs

**Problem**

Operator helpers such as `product-assurance.sh` manually construct URL path segments and JSON bodies. Some arguments described by the usage contract are not validated before interpolation.

**Action**

Add reusable validators:

```text
require_uuid
require_uint
require_enum
urlencode_query
```

At minimum validate:

- assurance `flow`: only `payin|payout`;
- `revision`: non-negative integer;
- finding / command IDs: expected identifier format;
- arbitrary list query: encode safely rather than concatenating raw input.

Generate JSON bodies with the standardized JSON helper instead of manual escaping where practical.

**Acceptance criteria**

- [ ] malformed revisions fail before curl.
- [ ] unsupported flows fail before curl.
- [ ] query values cannot accidentally alter the request structure.

---

## P2 — CI Quality Gates

### P2.3 Raise ShellCheck strictness gradually

**Current state**

CI already runs:

```text
shellcheck --severity=error
```

This is useful but intentionally ignores warning-level findings.

**Action**

Use a staged rollout:

1. run `--severity=warning` locally and capture the baseline;
2. fix high-confidence warnings;
3. document intentional suppressions next to the relevant code;
4. change CI to `--severity=warning`;
5. optionally evaluate `style` later, but do not block the repository on noisy style rules immediately.

**Acceptance criteria**

- [ ] CI blocks ShellCheck errors and warnings.
- [ ] suppressions are narrow and documented.
- [ ] generated/vendor scripts, if any, are explicitly excluded rather than globally lowering severity.

---

### P2.4 Add `shfmt` as a formatting gate

**Problem**

Shell formatting currently varies between files and directories. This becomes more noticeable as the number of scripts grows.

**Action**

Pin `shfmt` in CI similarly to ShellCheck.

Add:

```text
make shell-format
make shell-format-check
```

Suggested scope:

```text
scripts/**/*.sh
```

Choose formatting options once and document them in `.editorconfig` or the Makefile.

**Acceptance criteria**

- [ ] one command formats all shell scripts.
- [ ] CI fails when shell formatting differs.
- [ ] `/bin/sh` and Bash scripts are both parsed correctly by the chosen configuration.

---

### P2.5 Verify downloaded CI tool archives by checksum

**Problem**

CI pins actionlint and ShellCheck versions, which is good, but downloads release archives and extracts them without independently checking a known SHA-256 checksum.

**Action**

For every directly downloaded CI binary/archive:

- pin version;
- pin expected SHA-256;
- verify with `sha256sum -c` before extraction;
- fail if checksum does not match.

Keep version and checksum together in one location.

Example:

```text
ACTIONLINT_VERSION
ACTIONLINT_SHA256
SHELLCHECK_VERSION
SHELLCHECK_SHA256
```

**Acceptance criteria**

- [ ] corrupt/tampered archive fails before extraction.
- [ ] dependency bump requires version + checksum change in the same PR.

---

### P2.6 Reduce brittle text-only CI policy checks

**Problem**

Some CI policy scripts verify configuration by looking for exact strings with `grep`/`rg`.

This is simple and fast, but exact-text checks can:

- pass because required text exists only in a comment;
- fail after harmless formatting/refactoring;
- miss semantically equivalent but unsafe configuration.

**Action**

Keep text checks only where textual presence is genuinely the contract.

For semantic configuration:

- use `docker compose config` for Compose;
- use `helm template` plus structured verification for rendered Kubernetes manifests;
- parse YAML/JSON with a real parser;
- use existing dedicated tools for Terraform validation;
- use a small Go checker for repository-specific policies where no standard parser fits.

Start with `scripts/ci/supply-chain-check.sh`, because it protects high-value security invariants.

**Acceptance criteria**

- [ ] commented-out configuration cannot satisfy a runtime-hardening check.
- [ ] harmless YAML formatting changes do not fail CI.
- [ ] policy failures identify the semantic field that is wrong.

---

## P2 — Directory Structure and Discoverability

### P2.7 Reorganize `scripts/` by purpose

**Problem**

The root `scripts/` directory currently mixes:

- E2E tests;
- chaos tests;
- DB migration/bootstrap;
- backup/restore;
- privacy operations;
- load testing;
- operator CLIs;
- shared libraries.

The root has become difficult to scan safely.

**Target structure**

```text
scripts/
  README.md

  lib/
    core.sh
    compose.sh
    postgres.sh
    migrations.sh
    services.sh
    http.sh
    fixtures.sh
    assertions.sh
    backup.sh

  db/
    migrate.sh
    grant-app-role.sh
    rebuild-projection.sh
    postgres-init/

  backup/
    pgbackrest.sh
    manifest.sh
    enable-checksums.sh
    restore-cluster.sh
    post-restore-security.sh
    dr-drill.sh

  test/
    smoke.sh
    container-smoke.sh
    e2e/
      business.sh
      merchant.sh
      admin.sh
      privacy.sh
    chaos/
      runner.sh
      vendor-boundary.sh

  load/
    run.sh
    capacity.sh
    seed.sh
    snapshot.sh
    report-check.sh
    postgres-init/

  ops/
    privacy-export.sh
    product-assurance.sh
    rotation-drill.sh
    vault-seed.sh

  ci/
    ...
```

**Migration strategy**

Do not immediately delete the existing paths.

For one deprecation window, keep thin wrappers such as:

```bash
#!/usr/bin/env bash
exec "$(dirname "$0")/backup/restore-cluster.sh" "$@"
```

Update Makefile, CI, and docs to use new paths first. Remove wrappers only after all internal references are migrated.

**Acceptance criteria**

- [ ] every script has one obvious category.
- [ ] existing Make targets continue to work throughout migration.
- [ ] CI prevents new operational scripts from being added to the root accidentally.

---

### P2.8 Add `scripts/README.md` as an operational index

**Action**

Create a table with these columns:

| Script | Purpose | Mutates data? | Starts/stops infra? | Safe target | Required tools | Typical entrypoint |
|---|---|---:|---:|---|---|---|

Also document safety classes:

```text
S0 - read-only / validation
S1 - disposable local mutation
S2 - persistent local mutation
S3 - environment-sensitive operational mutation
```

For every S2/S3 script, document:

- expected environment;
- acknowledgement flag/env;
- rollback/recovery path;
- artifact/evidence output;
- whether it is safe while application traffic is live.

**Acceptance criteria**

- [ ] a new engineer can choose the correct script without reading its implementation first.
- [ ] destructive scripts are visibly classified.
- [ ] README links to Make targets when a Make target is the preferred interface.

---

## P2 — CLI Contract Standardization

### P2.9 Define a common shell CLI contract

Standardize new/refactored scripts around:

```text
--help
--dry-run       # when meaningful
--confirm       # mutation requiring explicit confirmation
--verbose       # optional diagnostics
```

Exit codes:

```text
0 = success
1 = operation/check failed
2 = invalid usage/precondition
```

Common logging API:

```text
log_info
log_warn
log_error
log_success
```

Rules:

- errors go to stderr;
- machine-readable output is kept separate from human logs;
- secrets are never printed;
- mutation scripts print the resolved target before execution.

**Acceptance criteria**

- [ ] every operator-facing script supports `--help`.
- [ ] invalid input consistently exits 2.
- [ ] automation can rely on stable stdout for machine-readable output.

---

## P2 — Script Testing

### P2.10 Add tests for pure shell logic and safety guards

**Problem**

The repository heavily tests application behavior using shell scripts, but the shell orchestration itself has relatively little isolated regression coverage.

**Action**

Add focused tests for functions that do not require the full stack.

Suggested coverage:

- path safety checks;
- environment guards;
- service registry parsing;
- run/project-name validation;
- acknowledgement logic;
- JSON helper behavior;
- migration command construction;
- backup/restore target validation;
- cleanup scoping.

Possible structure:

```text
tests/scripts/
  service-inventory-test.sh
  safety-guards-test.sh
  json-helper-test.sh
  migration-runner-test.sh
  cleanup-scope-test.sh
```

A dedicated shell test framework can be adopted later; plain executable tests are sufficient initially if they are deterministic and CI-friendly.

**Acceptance criteria**

- [ ] destructive commands are stubbed/mocked in unit-style tests.
- [ ] tests prove that protected targets are rejected.
- [ ] tests run in the normal lint/test CI job without Docker where possible.

---

## P3 — Portability and Developer Experience

### P3.1 Define the supported host-platform contract

**Problem**

Several scripts rely on GNU/Linux-specific utilities or behavior, for example:

- `sha256sum`;
- GNU `date` nanosecond/millisecond formatting;
- Bash process substitution;
- Linux-flavored tool availability assumptions.

This is not necessarily wrong, but the repository should decide whether scripts are:

1. Linux-only; or
2. expected to work from macOS + Docker Desktop as well.

**Recommended approach**

Support macOS for developer-facing local commands where the cost is low, while allowing Linux-only internals where necessary.

Add helper functions such as:

```text
sha256_file
utc_timestamp_ms
require_command
```

with portable fallbacks.

For workflows that are intentionally Linux-only, fail early with a clear message or provide a containerized Make target.

**Acceptance criteria**

- [ ] support policy is documented in `scripts/README.md`.
- [ ] common smoke/migration/load validation commands either work on macOS or fail with an explicit supported-path message.

---

### P3.2 Reduce repeated `go run` startup in orchestration paths

Several wrappers execute Go tools through `go run` repeatedly.

For developer workflows this is often acceptable, but repeated tool startup in larger workflows can add noise and latency.

**Action**

Where the same Go helper is executed multiple times in one run:

- build it once into `$WORK_DIR/tools/` or `bin/tools/`;
- reuse the binary for the rest of the workflow;
- keep `go run` for one-off simple commands where build caching already makes the cost negligible.

Do not optimize this before P0-P2 correctness work.

---

# 5. Recommended Implementation Sequence

## Phase 0 — Correctness hotfixes

Target: **small PRs, no folder moves**.

### PR 1 — Migration fail-closed

- [ ] replace error-suppressed SQL replay in `lib.sh`;
- [ ] add migration failure regression test;
- [ ] run smoke + business E2E + selected chaos scenario.

### PR 2 — Service inventory drift

- [ ] add `vendor` to backup manifest evidence;
- [ ] add `vendor` to restore inventory;
- [ ] update stale "eight database" comments/documentation;
- [ ] add temporary CI consistency check.

### PR 3 — Backup secret propagation

- [ ] centralize pgBackRest command wrapper;
- [ ] stop passing repository passphrase value as a host Docker argument;
- [ ] verify backup + latest restore + PITR drill.

---

## Phase 1 — Canonical contracts

### PR 4 — Canonical service registry

- [ ] add `config/service-inventory.tsv`;
- [ ] add parser/helper;
- [ ] migrate DB bootstrap and backup/restore consumers;
- [ ] add `check-service-inventory.sh`.

### PR 5 — Destructive-operation contract

- [ ] rollback acknowledgement in `migrate.sh`;
- [ ] checksum mutation acknowledgement;
- [ ] common target-printing and exit-code conventions.

### PR 6 — Test harness fence

- [ ] explicit allowed environments;
- [ ] fixture-secret production guard;
- [ ] local dependency target validation.

---

## Phase 2 — Maintainability refactor

### PR 7 — Extract pure shared libraries

Start with lowest-risk code:

- [ ] `core.sh`;
- [ ] `http.sh`;
- [ ] JSON helper;
- [ ] safety validators.

### PR 8 — Extract DB/Compose libraries

- [ ] `compose.sh`;
- [ ] `postgres.sh`;
- [ ] `migrations.sh`.

### PR 9 — Extract service lifecycle

- [ ] build helpers;
- [ ] start/stop helpers;
- [ ] certificate helpers.

Keep `scripts/lib.sh` as compatibility aggregator.

### PR 10+ — Modularize large scenarios

One family per PR:

- [ ] chaos;
- [ ] business E2E;
- [ ] DR drill.

---

## Phase 3 — Structure and CI hardening

### PR 11 — Directory reorganization

- [ ] introduce categorized directories;
- [ ] keep compatibility wrappers;
- [ ] update Makefile/CI/docs.

### PR 12 — Shell quality

- [ ] ShellCheck warning baseline cleanup;
- [ ] raise CI to `--severity=warning`;
- [ ] add pinned `shfmt` + formatting check.

### PR 13 — CI supply-chain improvements

- [ ] checksum downloaded tools;
- [ ] replace high-value text-only checks with semantic checks.

### PR 14 — Script tests + documentation

- [ ] add `scripts/README.md`;
- [ ] add safety classification;
- [ ] add pure helper/safety tests;
- [ ] document platform support.

---

# 6. Definition of Done

The `scripts/` improvement work should be considered complete when all of the following are true.

## Correctness

- [ ] migration failures cannot be silently ignored;
- [ ] every authoritative service is included in backup/restore verification;
- [ ] one canonical service inventory exists;
- [ ] JSON parsing uses a real parser.

## Safety

- [ ] destructive commands have explicit target scoping;
- [ ] high-risk mutations require acknowledgement or confirmation;
- [ ] test-only secrets cannot be used in production mode;
- [ ] backup repository secrets are not passed as plaintext host CLI arguments.

## Maintainability

- [ ] `lib.sh` is a compatibility layer, not the primary implementation location;
- [ ] large scenario files are decomposed into focused modules;
- [ ] root `scripts/` contains only intentional stable entrypoints/wrappers;
- [ ] service additions do not require updating many unrelated hard-coded arrays.

## CI

- [ ] ShellCheck runs at warning severity or stricter;
- [ ] shfmt formatting is enforced;
- [ ] directly downloaded CI binaries are checksum verified;
- [ ] service-inventory drift fails CI;
- [ ] safety helpers have isolated tests.

## Developer Experience

- [ ] `scripts/README.md` describes every operator-facing entrypoint;
- [ ] scripts have consistent help/exit semantics;
- [ ] supported host platforms are explicit;
- [ ] compatibility wrappers prevent abrupt breakage during folder reorganization.

---

# 7. Priority Matrix

| Priority | Item | Impact | Effort | Why now |
|---|---|---:|---:|---|
| P0 | Fail-closed migrations in test harness | Very High | Medium | Current behavior can produce false-positive acceptance evidence |
| P0 | Service inventory / `vendor` consistency | Very High | Low-Medium | DR/backup verification completeness has already drifted |
| P1 | Standardize backup secret handling | High | Medium | Removes inconsistent secret exposure across operational paths |
| P1 | Canonical service registry | High | Medium | Prevents the same class of drift from recurring |
| P1 | Rollback/checksum safety acknowledgements | High | Low | Reduces operator-error blast radius |
| P1 | Test-harness environment fence | High | Low-Medium | Makes test-only credentials and assumptions explicitly safe |
| P1 | Split `lib.sh` | High | Medium-High | Reduces shared blast radius and enables isolated testing |
| P1 | Modularize chaos/business/DR scenarios | High | High | Large scripts are becoming difficult to change safely |
| P2 | Real JSON parsing | Medium-High | Low-Medium | Removes brittle parsing and duplicate helper drift |
| P2 | ShellCheck warnings + shfmt | Medium | Low-Medium | Cheap quality improvement on a large Bash surface |
| P2 | Checksum CI tool downloads | Medium-High | Low | Completes existing pinning strategy |
| P2 | Semantic CI policy checks | Medium | Medium | Makes security gates harder to bypass accidentally |
| P2 | Directory reorganization + wrappers | Medium | Medium | Improves onboarding after internal contracts are stabilized |
| P2 | Script README + common CLI contract | Medium | Low | High developer-experience return |
| P2 | Shell safety/helper tests | Medium | Medium | Protects refactoring and destructive guards |
| P3 | Cross-platform helpers | Low-Medium | Medium | Useful, but lower priority than correctness/safety |
| P3 | Reuse built Go helper binaries | Low | Low-Medium | Optimization only; should follow correctness work |

---

# 8. Recommended First Change

If only one improvement is implemented first, choose:

> **Replace the fail-open `apply_migrations()` behavior in `scripts/lib.sh`.**

Reason: almost every high-value acceptance flow depends on the shared harness. A test suite that can silently continue after a broken migration weakens the trustworthiness of smoke, E2E, chaos, and DR evidence at the same time.

Immediately after that, fix the `vendor` service-inventory drift and introduce a CI guard so the mismatch cannot recur.
