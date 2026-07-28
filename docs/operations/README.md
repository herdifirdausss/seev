# Operational Tooling Deep Dive

> [Documentation home](../README.md) · **Operations**

> **Status: Current. Audience: developers and operators.** You can understand
> the product without this document. Read it when you want to start, verify,
> observe, or recover the software. Terms are defined in the
> [glossary](../reference/glossary.md).

[Services](../reference/services.md) covers what the nine services *do*; this
document covers everything that builds, verifies, runs, and observes
them: `docker-compose.yml`, `Makefile`, `scripts/`, `.github/` (CI), and
`deploy/observability/`. Every claim below was checked directly against
the actual compose file, workflow YAML, script headers, and Makefile
targets — not assumed from a plan document.

## The general problem this tooling layer solves

A distributed, money-moving system can pass every unit test and still be
wrong in ways that only show up under real conditions: a process crashing
mid-transaction, a dependency going down while a request is in flight, a
container image that builds but doesn't actually boot correctly, a
supply-chain dependency silently changing underneath a CI pipeline. Unit
tests prove the logic is right in isolation; they prove nothing about
what happens when Postgres restarts mid-posting, or a payout crashes
between "vendor call sent" and "outcome recorded." This repo treats that
gap as a first-class engineering problem, not an operational afterthought
— which is why the tooling here is as deliberately engineered as the
services it exercises.

---

## 1. Docker Compose — the local infrastructure

**Problem it solves**: nine services, three default infrastructure
dependencies (Postgres, Redis, RabbitMQ), optional Vault/object-store setup,
and a six-component observability stack all need to run reproducibly on a laptop,
without the services accidentally trusting each other more than they
would in a real multi-host deployment.

**How it's built**: `docker-compose.yml` is organized into three
profiles, so you only start what you actually need:

| Profile | What it starts | When you use it |
|---|---|---|
| *(default, no profile)* | Postgres, Redis, RabbitMQ | Always — the minimum for running services as host binaries |
| `secrets` | Vault (dev-mode) | Only when exercising the optional `vaultGetenv` secrets path (`scripts/vault-seed.sh`) |
| `app` | Object-store initialization plus all 9 services, built from source | Full-stack container testing (`scripts/smoke-container.sh`, CI's `smoke-container` job) |
| `observability` | Prometheus, Grafana, Loki, Tempo, Alloy, a restricted Docker socket proxy | Local dashboards/tracing/log exploration — never required to run or test the system |

**How this solves the problem**:

- **Bounded logging** (`x-app-logging` anchor, 10MB × 3 files per
  container) — without it, Docker's default `json-file` driver keeps
  container output forever, which fills a laptop's disk regardless of
  Loki's own 48h retention. A local, throwaway concern, but one that
  breaks a demo the moment nobody's watching disk usage.
- **A shared mTLS cert volume** (`x-cert-volume` anchor) — every
  internal-facing container mounts the same host-generated CA + per-service
  leaf certs read-only, generated once by `make certs` before Compose
  ever reads the file. This is what makes the *local* stack exercise the
  *same* mutual-TLS trust model described in
  [docs/security/threat-model.md](../security/threat-model.md), instead
  of a weaker "it's just Docker, ports are enough" default.
- **Vault runs in dev mode, on purpose, with the residual documented** —
  in-memory storage, a fixed non-secret dev token, everything lost on
  restart. That's not an oversight; it's explicitly out of the CI/nightly
  path (K7) and re-seeded via `scripts/vault-seed.sh` every time, so the
  tooling never pretends a throwaway local instance is a production
  secrets store.
- **Named volumes per infra component** (`seev_postgres_data`,
  `seev_redis_data`, etc.) — state survives a `docker compose down`
  (without `-v`) across work sessions, but every test script that needs a
  clean slate explicitly resets with `-v` first (see §3's `lib.sh`).

---

## 2. Makefile — the single entry point

**Problem it solves**: "how do I build/test/verify/run this" needs one
answer that doesn't drift between what a contributor's shell history says
and what CI actually runs.

**What it provides** (grouped by purpose, not alphabetically):

| Group | Targets |
|---|---|
| Build | `build`, `build-all`, `run`, `dev` |
| Workspace cleanup | `clean` (repository-local `bin/`, coverage, and smoke diagnostics only) |
| Database | `docker-up`, `docker-down`, `migrate-up`, `migrate-up-all`, `migrate-down`, `grant-app-role` |
| Static/security checks | `vet`, `lint`, `tools-lint`, `tools-security`, `modernize-check`, `security-vuln`, `ci-lint`, `tidy`, `docs-check`, `git diff --check` |
| Verification bundles | `verify-static`, `verify-full`, `verify-chaos` |
| Tests and journeys | `test`, `smoke-container`, `smoke-test`, `business-e2e`, `admin-e2e`, `privacy-e2e`, `chaos-debug` |
| Contracts | `contracts`, `contract-generate`, `contract-lint`, `contract-breaking`, `contract-test` |
| Protobuf | `proto`, `proto-lint`, `proto-breaking`, `tools` |
| Load safety | `load-lint`, `load-test`, `load-seed`, `load-smoke`, `load-run`, `load-capacity`, `load-report-check`, `load-clean` |
| Security and recovery | `certs`, `cryptox-secret`, `backup-secret`, `backup-role-bootstrap`, `backup-checksums-enable`, `backup-stanza-init`, `backup-full`, `backup-diff`, `backup-check`, `backup-status`, `backup-expire` |
| Observability | `observability-up`, `observability-down`, `observability-secret` |
| Meta | `help` |

**How this solves the problem**: `verify-full` is the concrete answer to
"is this repo actually correct" — it chains a Docker volume reset, build,
static checks, unit and integration tests, contract/protobuf/load checks, the
container smoke, the host smoke journey, the business journey, the
admin-console journey, the privacy lifecycle journey, and disposable load
smoke, in the order that
actually catches the volume-reset-skipped false-regression class of bug
documented in [Onboarding](../development/onboarding.md#gotchas-that-cost-people-real-time).
Chaos is exposed separately as `verify-chaos` because it kills dependencies and
is an operator-controlled recovery drill. The complete gate is deliberately
heavier than the everyday loop
(`go build && go vet && make lint && make test`) — see
[Project guide](../development/project-guide.md#build-and-verification) for exactly
which class of change earns the expensive gate.

Canonical load-capacity measurements remain separate because they have a
different safety boundary: `verify-full` runs only the non-claiming load
harness checks and one disposable `load-smoke`; use the explicitly acknowledged
`load-*` targets for measured capacity work. `verify-chaos` remains the manual
recovery gate.

---

## 3. `scripts/` — verification, operations, and bootstrap

**Problem it solves**: the same repeatable setup (build binaries, wait
for Postgres/Redis/RabbitMQ, run migrations, wire consistent test
credentials) was originally re-derived by hand for every new
verification need — expensive, and a source of drift between "what the
docs say to test" and "what actually got tested." Every script here
either eliminates that re-derivation or proves something specific that a
unit test structurally cannot.

### `lib.sh` — the shared foundation

Not executable — sourced by every other test script. Owns bootstrap
(build binaries, start/stop the compose infra, wait-for-ready), and
assertion helpers, so `chaos-test.sh` and `smoke-test.sh` share one
source of truth instead of drifting copies. **Gotcha** (already in
[Onboarding](../development/onboarding.md#gotchas-that-cost-people-real-time), worth
repeating here): source it once per debug session, not once per shell
command — it recomputes `WORK_DIR`/`GENTOKEN_BIN` on each source, and
re-sourcing mid-session silently breaks helpers like `gen_token`.

### Verification journeys — each proves a different kind of "it works"

| Script | Proves | How |
|---|---|---|
| `smoke-test.sh` | The system boots and the core paths respond, on **host binaries** against Compose infra | The manual curl walkthrough every doc's "final verification" section used to ask for by hand, checked in once |
| `smoke-container.sh` | The same, but against **real Docker images** (`docker compose --profile app up`) | Register → login → topup intent → signed mockvendor callback through VendorService → poll until settled → assert balance via `docker exec psql` — the container counterpart to `smoke-test.sh`, because "it works as a host binary" doesn't prove "the container image is correct" |
| `business-e2e.sh` | The MVP can be run **end-to-end as a business**, not just as a technical system | Two real users register and log in with real JWTs (not `gentoken`); one tops up via a signed VendorService callback, transfers to the other for a fee, withdraws for a fee, both get notified, and an operator confirms the books balance AND the platform earned the expected revenue |
| `admin-e2e.sh` | The operator console works end-to-end | Starts the BFF separately from the user-money gateway path, exercises the real admin session/CSRF/maker-checker/audit journey |
| `privacy-e2e-host.sh` + `privacy-e2e.sh` | Export, retention holds, and closure work against a clean host-binary stack | The wrapper owns dependencies, migrations, binaries, certificates, and cleanup; the underlying journey verifies export metadata, hold blocking/release, and closure completion |
| `chaos-test.sh {1..20\|all}` | The system survives real failure, dependency outage, privacy-lifecycle failure, and recovery — not just handles errors in tests | 20 scenarios (below); scenarios 1–14 are multi-service drills, while 15–20 invoke focused lifecycle tests and retain the same pass/fail evidence |

The local Compose default for `VENDOR_CALLBACK_CIDRS` includes the private
Docker bridge range because a host-originated callback reaches the container
with a bridge source address. Production deployments must override it with
the vendor's exact egress CIDRs; mTLS and signature verification remain
mandatory in both environments.

### Chaos scenarios — what each one actually proves

| # | Scenario | What it proves |
|---|---|---|
| 1 | `kill -9` mid-posting | A killed process mid-transaction never leaves a half-posted, unbalanced ledger |
| 2 | RabbitMQ down | The outbox pattern survives a broker outage — postings still happen, events queue and catch up |
| 3 | Postgres restart mid-traffic | In-flight requests fail cleanly and safely; nothing corrupts |
| 4 | Redis down | Rate limiting and velocity checks degrade per their documented fail-open/fail-closed contract, not by accident |
| 5 | Payout crash mid-flight | A payout interrupted after the vendor call was sent, before the outcome was recorded, resumes correctly; abandoned vendor-command leases are reaped within the recovery window — never double-pays, never loses the request |
| 6 | Payin-service unavailable during callback | A vendor callback retained by VendorService while Payin is down doesn't silently drop money |
| 7 | Fraud-service fail-open + block-mode E2E | The documented fail-open contract holds under a real outage, and a real block-mode screening decision actually blocks |
| 8 | Vendor failover | Payin/payout routing actually fails over to the next vendor when one is down, database-driven, no redeploy |
| 9 | Redis outage — selective hot-swap + fraud fail-closed | The velocity store's fail-*closed* contract holds specifically — this is the scenario that caught TM-14 (see [docs/security/threat-model.md](../security/threat-model.md)) |
| 10 | Distributed breaker across two payout replicas | The circuit breaker state is genuinely shared across replicas, not per-process |
| 11 | Payout crash after command enqueue / after network call | Two distinct crash points in the payout vendor-command lifecycle both resume correctly |
| 12 | Assurance restart/backlog recovery | Assurance resumes its scan cursor correctly after a restart instead of re-scanning or skipping |
| 13 | Ledger outage preserves assurance cursor/finding | Assurance's own state survives its one real dependency going down |
| 14 | Durable pause, owner recovery, maker/checker resume | The full emergency-intake-pause lifecycle, including the two-different-principals resume rule, works under real conditions |
| 15 | Retention database failure | A failure in one retention class is recorded and does not authorize an unsafe deletion in another class |
| 16 | Object-store outage | Export metadata remains truthful while the durable object-outbox retry path converges |
| 17 | Privacy key-version mismatch | Wrong or unavailable encryption key versions fail closed rather than guessing a key |
| 18 | Privacy worker restart | Closure saga checkpoints survive repeated worker invocations and resume forward |
| 19 | Owner timeout during closure | Owner failure leaves closure disabled and resumable from its durable checkpoint |
| 20 | Retention/closure race | Retention cannot purge data before the closure horizon while closure is still resumable |

Current acceptance note (2026-07-28): the latest `./scripts/chaos-test.sh 5`
run settled the created, held, and ledger-crash requests; the deliberate
`vendor_pending` request remained resumable, while the `submitted` kill point
remained submitted. The payout relay now performs an immediate failed-command
recovery pass on startup, and Scenario 5 must be rerun before this live gate is
marked green.

### Standalone drills and operator tools — not part of `verify-full`

| Script | Purpose |
|---|---|
| `rotation-drill.sh` | On-demand proof that `certgen rotate` is zero-downtime — no restart, no dropped baseline connections, and old certs are actually rejected post-rotation. Standalone by design: certificate rotation is an occasional operational event, not a per-commit regression to guard against. |
| `rebuild-projection.sh` (+ `sql/rebuild_projection.sql`) | Point-in-time proof that `account_balances` can be rebuilt 100% from the append-only `ledger_entries` — the empirical backing for "the projection is derived state, the ledger is truth." Refuses to run while the app is live, since rebuilding under concurrent posting traffic would race the posting engine's own balance writes. |
| `vault-seed.sh` | Idempotently seeds local dev-mode Vault with the subset of secrets safe to source that way — re-running never rotates an already-seeded value. |
| `product-assurance.sh` | The operator CLI for Assurance's admin surfaces — summary, findings lifecycle, intake pause/resume — documented in [docs/operations/runbooks/product-assurance.md](runbooks/product-assurance.md). |
| `postgres-init/{01,02,03}-*.sh` | Compose's Postgres bootstrap: create the app role, create each service's own database, run each service's own migration set — this is what turns one Postgres container into nine properly-isolated per-service databases on first boot. |
| `ci/check-action-pins.sh` | See §4 — the repo-local supply-chain gate for GitHub Actions. |

---

## 4. CI/CD (`.github/`)

**Problem it solves**: the verification scripts above are only valuable
if they actually run on every change, consistently, without silently
skipping, without a slow docs-only PR waiting on a 9-image container
build it doesn't need, and without a compromised or drifted third-party
Action becoming a supply-chain hole.

### `workflows/ci.yml` — the per-push/PR gate

| Job | What it does | When it runs |
|---|---|---|
| `changes` | Classifies the diff as docs-only vs. runtime (anything outside `docs/**`/`*.md` counts as runtime) | Always |
| `docs-check` | Uses the repository's standard-library Go checker to validate the required learning-path markers plus every local Markdown file link and heading anchor | Always |
| `lint-and-test` | Go build/vet/module verification, `actionlint` + `ShellCheck`, SHA-pin policy, `golangci-lint`, Go 1.26 modernizer check, `govulncheck`, race/coverage tests, load-tag safety tests, HTTP/protobuf contract gates, and the disposable B0 load-harness safety gate | Only if `runtime == true` |
| `integration` | `go test -tags=integration -race ./...` against testcontainers-provisioned Postgres | Only if `runtime == true` |
| `smoke-container` | Builds all 9 service images via a single Bake invocation, verifies each image's revision label actually matches the commit (never a stale cache hit), runs `smoke-container.sh` against them | Only if `runtime == true` |
| `ci-gate` | The one required check — requires `docs-check`, then asserts the three heavy jobs are `skipped` for a docs-only change or `success` for a runtime change; never silently green from an absent job | Always |

**How this solves the problem**: a documentation PR gets a fast, relevant
link check; a code PR gets that check plus the full, expensive proof — and `ci-gate` being the
single required branch-protection check means a job silently not running
can never be mistaken for a job that passed.

### `workflows/nightly.yml` — the scheduled full-stack run

Despite the filename (kept for grep-ability), the automatic schedule is
**weekly**, not nightly — a daily 90-minute gate doesn't fit this repo's
cost budget. Runs `business-e2e.sh`, `privacy-e2e`, then `chaos-test.sh all`,
generates fresh per-run credentials that are masked before they ever land in a
log, and uploads the work directories as a diagnostics artifact only on
failure. `workflow_dispatch` lets an operator run any subset (`all`,
`business`, `privacy`, or `chaos`) on demand between scheduled runs.

### `dependabot.yml` — supply-chain freshness

Weekly PRs bumping GitHub Actions versions, grouped into one PR, capped
at 3 open at a time, **never auto-merged** — every bump still runs the
full CI gate and gets read by a human. This is the freshness half of
supply-chain safety; `check-action-pins.sh` (next) is the integrity half.

### `scripts/ci/check-action-pins.sh` — the repo-local integrity gate

GitHub does not enforce SHA-pinning for this repo/org
(`sha_pinning_required` is `false`, confirmed at doc-44 T0), so this
script enforces it locally: every external `uses:` across
`.github/workflows/*.yml` must be a full 40-hex commit SHA with a
`# vX.Y.Z` comment recording the human-readable version — a floating tag
like `@v4` or `@main` is not an immutable reference, and a short SHA
isn't either. This is what makes Dependabot's weekly bump PRs *reviewable
diffs* (old SHA → new SHA, with the version comment as a sanity check)
instead of trusting a moving tag to still point where it pointed
yesterday.

---

## 5. Observability (`deploy/observability/`) — optional, operator-facing

**Problem it solves**: the verification tooling above proves correctness
at test time; observability is what lets an operator understand *live*
behavior — request rates, error budgets, security posture, log
correlation — without needing to reproduce the exact scenario locally
first. Explicitly optional: nothing in the system requires this profile
to run or to pass any test.

| Component | Role |
|---|---|
| **Prometheus** (`prometheus/prometheus.yml`, `rules/slo.yml`) | Scrapes `/metrics` on each service's *internal* admin listener only — never the public one — and now over mTLS with its own `prometheus` client identity, matching what every other internal caller needs (docs/roadmap/archive/49 K6). `rules/slo.yml` defines the SLO alerting rules; `slo_test.yml` proves them against synthetic data. |
| **Go runtime signals** | Each service that opens its owned PostgreSQL pool exports scheduler goroutine/thread gauges and a goroutine-created counter from `runtime/metrics`, alongside the standard Go collector. These labels contain no request, user, account, or run identifiers, so they remain safe for shared dashboards. |
| **Grafana** (`grafana/dashboards/*.json`, `provisioning/`) | Six purpose-built dashboards: `money-flow`, `service-red` (rate/errors/duration), `mtls-security`, `slo-alerts`, `compliance-a4`, `product-assurance` — each mapped to a specific operational question rather than being a generic catch-all. Datasources and alert rules are provisioned as code (`provisioning/`), not clicked together by hand. |
| **Loki** (`loki/loki.yaml`) | Log aggregation with a 48h retention window — bounded on purpose, working together with Compose's own bounded container logging (§1) rather than assuming either one alone is sufficient. |
| **Tempo** (`tempo/tempo.yaml`) | Distributed trace storage — the backend for the request-ID/tracing work described in `docs/roadmap/archive/36`. |
| **Alloy** (`alloy/config.alloy`) | The collector: discovers every container via a restricted Docker socket proxy (read-only, `NETWORKS=1` required only because Alloy's discovery component happens to call `GET /networks` even though it never creates/modifies one), tails container logs with JSON parsing, level extraction, and redaction, and persists its own read offsets so a restart resumes tailing instead of re-shipping history. |
| **`docker-socket-proxy`** | The reason Alloy never gets the raw Docker socket — a minimal proxy exposing only the read endpoints Alloy's discovery actually needs. |

**How this solves the problem**: every dashboard maps to a question an
operator actually asks ("is money moving correctly," "is mTLS healthy,"
"are we inside SLO"), the trace/log/metric backends are all bounded so
they can't silently fill a disk, and the whole profile can be started or
stopped independently of everything else (`make observability-up` /
`make observability-down`) since it's diagnostic tooling, never a
dependency of the system itself.

---

## 6. API contracts (`api/proto/`, `gen/`, `buf.yaml`, `buf.gen.yaml`)

**Problem it solves**: internal gRPC contracts between services need to
be typed, versioned, and safe to evolve — a hand-maintained client/server
pair drifts silently; an accidental breaking change to a wire message
shipped as a routine PR breaks every caller at once, discovered at
runtime instead of at review time.

**How it's built**: five `.proto` files under `api/proto/seev/{fraud,
ledger,payin,payout,ping}/v1/`, compiled via `buf` (`buf.yaml` /
`buf.gen.yaml`) into committed Go bindings under `gen/` — generated code
is checked into version control rather than generated at build time, so
a clone of the repo builds without the protobuf toolchain installed.

**How this solves the problem**:

- `make proto` regenerates bindings from the `.proto` source — the
  single command that keeps `gen/` and `api/proto/` from drifting apart.
- `make tools` installs the pinned Buf and Go protobuf compiler versions under
  `bin/tools/`; `make proto`, `make proto-lint`, and `make proto-breaking`
  depend on that local toolchain automatically, so a fresh checkout does not
  require `buf` in `PATH`.
- `make proto-lint` (`buf.yaml`'s `STANDARD` lint rules, with two
  explicit, documented exceptions for the locked ledger contract's
  existing wire names) catches inconsistent contract style before it
  ships.
- `make proto-breaking` (`buf.yaml`'s breaking-change detector, `FILE`
  mode) compares the current contract against the explicit
  `PROTO_MERGE_BASE_REF` ref (normally `main` locally, or the computed merge
  base SHA in CI)
  and fails the build if a change would break an existing caller —
  turning "did I just break every service that calls ledger's `Post`
  RPC" from a runtime surprise into a CI failure with a name attached to
  the offending field.
- `buf.gen.yaml`'s managed mode pins each proto package's Go import path
  explicitly (`github.com/herdifirdausss/seev/gen/<service>/v1`) so
  generated imports stay stable and predictable across regenerations.

---

## Where this fits with everything else

| You want... | Read |
|---|---|
| Why the system is built this way at all | [Architecture](../reference/architecture.md) |
| What each service actually does | [Services](../reference/services.md) |
| What each `pkg/` package does and who uses it | [Shared packages](../reference/shared-packages.md) |
| The rules for changing any of this | [Project guide](../development/project-guide.md) |
| Step-by-step local setup | [README.md](../../README.md) |
| What to do when something breaks in the field | [docs/operations/runbooks/](runbooks/) |
| How to actually submit a change | [CONTRIBUTING.md](../../CONTRIBUTING.md) |
