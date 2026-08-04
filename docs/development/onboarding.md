# Onboarding

> [Documentation home](../README.md) · [Development](README.md)

> **Status: Current. Audience: contributors.** If you first need an
> explanation of the product rather than its code, read the
> [beginner guide](../learn/beginner-guide.md) and
> [product tour](../learn/product-tour.md). Unfamiliar terms are defined in the
> [glossary](../reference/glossary.md).

This is the "start here" guide for a new contributor.
[Architecture](../reference/architecture.md) covers *why* the system is built this
way (the problem, the business capability map, the design decisions);
[README.md](../../README.md) covers how to run the stack; [Project guide](project-guide.md)
covers the rules you must follow when changing code. This guide is about
getting your bearings fast in the code itself: where things live, why
they're named that way, and what to read first.

Run `make onboarding-check` after structural changes. It verifies the critical
ownership paths used in the first-contribution walkthrough, the service-local
test commands, and the full verification command. It is a structural guardrail;
the under-two-minute target still needs a real unfamiliar contributor review.

## 60-second mental model

Seev started as a single ledger-first monolith and was deliberately split
into 9 independently deployable services over time (the full history is in
[docs/roadmap/](../roadmap/README.md), but you don't need to read that to be
productive). Each service owns one Postgres database; nothing reads another
service's tables directly — every cross-service call goes through HTTP,
gRPC, or a RabbitMQ event.

```
   Gateway (public edge)        Auth (public edge, independent)
   /    |      \                       |
 Payin Payout (money workflows)        |
   \    |      /                       |
      Ledger (source of truth: double-entry postings) ←───────┘
               |
        Fraud, Assurance, Admin BFF (internal-only)
        Vendor → VendorService (restricted callback edge) → Payin/Payout
```

Only **Gateway** (`:8080`) and **Auth** (`:8082`) expose end-user APIs.
Compose also publishes the internal/admin listeners to `127.0.0.1` for
local operations and test scripts, but they are not public application
surfaces. This surprises people expecting one public entrypoint:
auth-service is public on its own, not proxied through gateway.

Vendor callbacks and outbound vendor calls cross the separate VendorService
boundary (`:8098` / gRPC `:9098`); Payin and Payout retain ownership of their
own workflow and correlation decisions.

- **Gateway** composes calls to payin/payout/ledger and consumes ledger
  events for notifications — it holds almost no business logic itself.
- **Auth**, **Payin**, **Payout**, **Fraud** own one domain each.
- **Ledger** is the money source of truth: every balance-affecting action
  ends up as a balanced double-entry posting here.
- **Assurance** is read-only — it continuously cross-checks payin/payout
  against ledger and never moves money.
- **Admin BFF** is the operator console — session-based, proxies typed
  requests to the other services, writes an audit log.

## Service map (name → code → data)

Each service now has one obvious source root. Start at its README, then follow
`cmd/` into the composition root and `internal/` into feature, transport,
repository, and worker packages:

| Service | Source root | Entrypoint | Database |
|---|---|---|---|
| gateway | `services/gateway/` | `services/gateway/cmd/gateway` | seev_gateway |
| auth | `services/auth/` | `services/auth/cmd/auth` | seev_auth |
| ledger | `services/ledger/` | `services/ledger/cmd/ledger` | seev_ledger |
| payin | `services/payin/` | `services/payin/cmd/payin` | seev_payin |
| payout | `services/payout/` | `services/payout/cmd/payout` | seev_payout |
| fraud | `services/fraud/` | `services/fraud/cmd/fraud` | seev_fraud |
| admin console | `services/adminbff/` | `services/adminbff/cmd/adminbff` | seev_adminbff |
| assurance | `services/assurance/` | `services/assurance/cmd/assurance` | seev_assurance |
| vendor | `services/vendor-service/` | `services/vendor-service/cmd/vendor` | seev_vendor |

Everything cross-cutting lives outside any one service: `internal/platform/`
(domain-neutral runtime infrastructure), `contracts/` (wire and compatibility
contracts), and `internal/testkit` (test harness shared by integration tests).
Platform code must not import `services/` or encode service-owned business
decisions. Auth-only KYC adapters remain under
`services/auth/internal/adapter/kycvendor`; they are not shared infrastructure.

Full port/DB table is in [README.md](../../README.md#runtime-architecture) —
not duplicated here since it changes with the Compose file.

## How `internal/` is organized

The root `internal/` directory is intentionally small: `platform/` contains
domain-neutral runtime infrastructure and `testkit/` contains test-only
helpers. Business code belongs under the owning `services/<service>/internal/`
tree. The full root map is in [`internal/README.md`](../../internal/README.md).

Inside a bounded context, use focused subpackages only when they have a
meaningful API and independent tests. The established names are `model/` for
data types, `repository/` for owned persistence, `transport/` for HTTP/gRPC
adapters, `worker/` for background jobs, and `service/` for independent
lifecycles such as Ledger's accrual or reconciliation flows. Keep small
concerns as files at the module root rather than creating empty layers.

Repositories remain one interface, implementation, constructor, and generated
mock per concern; see the [project guide](project-guide.md#package-layout-conventions).

Run `make ide-config` when using VS Code. It generates one Delve launch entry
per canonical service from the same registry enforced by the architecture
tests, so debugger paths stay current after a service move.

## Naming conventions

These hold across the whole repo — if you see an exception, it's a bug to
flag, not a new pattern to copy:

- **Errors**: package-level `var ErrXxx = errors.New("<service>: message")`,
  declared in that package's `errors.go`, wrapped with `%w` when crossing a
  layer (`services/auth/internal/auth/errors.go` is a clean example). Never a bare string
  comparison — always `errors.Is`.
- **Tests**: `<file>_test.go` beside the code for unit tests (mocked deps,
  no Docker needed); `<file>_integration_test.go` behind the `integration`
  build tag for tests that need a real Postgres/Redis via testcontainers.
  Run unit tests with `go test ./...`, integration with
  `go test -tags=integration ./...`.
- **Mocks**: always generated, never hand-written — `//go:generate mockgen
  -source=<file>.go -destination=<file>_mock.go -package=repository` sits
  directly above the interface it mocks. Regenerate with `go generate
  ./services/<service>/internal/repository/...`; there is no `make mocks` target.
- **Migrations**: `services/<service>/migrations/NNNNNN_description.{up,down}.sql`,
  strictly numbered, one pair per change, both directions always written
  together. Never edit a migration that's already merged — write a new one.
- **Scripts**: `scripts/<purpose>.sh` (`smoke-test.sh`, `business-e2e.sh`,
  `admin-e2e.sh`, `privacy-e2e-host.sh`, `chaos-test.sh`), all sourcing shared bootstrap logic from
  `scripts/lib.sh` — never copy-paste startup logic between them.
- **Docs**: `docs/roadmap/NN-description.md` is chronological *implementation*
  history (what was built and when) — the number is a sequence, not a
  feature ID, and does **not** necessarily match any "track" number
  mentioned inside the doc. Don't use plan numbers as an index into
  features; use [docs/roadmap/README.md](../roadmap/README.md)'s table instead.

## Recommended first read: trace one request end-to-end

Reading isolated files won't build a mental model as fast as following one
real request across service boundaries. The register → top-up → ledger
posting path touches every architectural idea in this repo in ~15 minutes
of reading:

1. **`POST /api/v1/auth/register`** — note this hits auth-service
   **directly** on its own public port (`127.0.0.1:8082`), not through the
   gateway. Auth is one of two services with its own public edge (gateway
   is the other); everything else is internal-only, reached only through
   gateway or another service. Routed in
   [services/auth/internal/transport/http/http.go](../../services/auth/internal/transport/http/http.go) (`RegisterHandler`).
2. `Module.Register` in [services/auth/internal/auth/auth.go](../../services/auth/internal/auth/auth.go) —
   creates the user row, then calls `Provisioner.ProvisionUser` (a small
   interface, not a concrete ledger import — see how auth depends on
   ledger through an interface it owns, not the other way around).
3. **`POST /topup`** — this one DOES go through gateway:
   [services/gateway/internal/transport/http/topup.go](../../services/gateway/internal/transport/http/topup.go) calls
   payin-service over gRPC (`payinv1.PayinServiceClient`), which lands in
   `Module.CreateTopupIntent` in
   [services/payin/internal/payin/topup.go](../../services/payin/internal/payin/topup.go) — creates a pending
   intent, no money has moved yet.
4. A vendor webhook confirms the top-up → payin resolves the intent → it
   calls ledger-service's gRPC contract to post the actual double-entry
   transaction.

   **Current boundary:** the vendor calls VendorService's `/webhooks/{vendor}`
   endpoint. VendorService authenticates and durably stores the callback, then
   delivers a normalized event to Payin/Payout over mTLS; the owner service
   correlates it with its own state.
5. **The posting itself**: `Service.Handle` →
   `Service.execTransfer` in
   [services/ledger/internal/ledger/handle/service.go](../../services/ledger/internal/ledger/handle/service.go) —
   this is the single most load-bearing function in the repo. Read the
   comment above it before ever touching it; the
   [project guide](project-guide.md) calls out by name that reordering it
   requires understanding the idempotency gate,
   lock order, validation order, balance projection, posting, and outbox
   guarantees.
6. The posting writes an outbox row; a relay worker publishes it to
   RabbitMQ; gateway's `services/gateway/internal/notification` consumes it for user-facing
   notifications, and `services/fraud`'s consumer independently consumes
   it for velocity checks — same event, two unrelated subscribers, neither
   blocks the posting transaction.

Once that path makes sense, `services/assurance` (reads payin/payout/ledger
to cross-check without ever writing) and `services/adminbff` (operator
console proxying typed requests to everything else) are easy to place
mentally — they sit outside the money-movement path entirely.

## Where to read next, by question

| You want to know... | Read |
|---|---|
| How do I run this locally? | [README.md](../../README.md) — Local quick start |
| What am I not allowed to do? | [Project guide](project-guide.md) — service boundaries, financial invariants, security rules |
| What does service X actually do, its full endpoint/job list, and what depends on it? | [Services](../reference/services.md) |
| What does `docker-compose.yml`/the Makefile/a script/CI/observability actually solve, and how? | [Operations](../operations/README.md) |
| What does a `internal/platform/` package actually do and who uses it? | [Shared packages](../reference/shared-packages.md) |
| Why does the code look like *this*? | [docs/roadmap/README.md](../roadmap/README.md) — find the phase, read that one doc, not the whole folder |
| What does event X mean on the wire? | [docs/reference/events.md](../reference/events.md) |
| Something's broken in prod-like conditions | [docs/operations/runbooks/](../operations/runbooks/) — one doc per incident class (recon, DR restore, cert rotation, ledger integrity, etc.) |
| What's the threat model / what's already been reviewed? | [docs/security/threat-model.md](../security/threat-model.md) |
| How do I add a scheduled job? | [internal/platform/scheduling/README.md](../../internal/platform/scheduling/README.md) |

## Gotchas that cost people real time

- **`gateway`'s code isn't in `services/gateway`** — see the service map
  above. Searching for the wrong package name is the #1 time-waster.
- **`docs/roadmap` numbers aren't feature IDs.** Doc 48 is track A10, doc 49 is
  track A6 — always check the doc's own title, never assume `NN` means
  anything outside "this was written Nth."
- **Every internal hop is mutually authenticated (mTLS + SPIFFE-style URI
  SAN).** Adding a new service-to-service call means adding the caller to
  the callee's identity allowlist explicitly — it is never inferred from
  "signed by our CA."
- **`ledger_entries` is genuinely append-only.** There is no UPDATE/DELETE
  code path for it anywhere in the codebase on purpose; corrections are
  new compensating transactions. If you think you need to mutate one,
  you've misunderstood the requirement.
- **Money is never a float.** `decimal.Decimal` or integer minor units,
  everywhere, no exceptions — `go vet`/review will (should) catch a
  `float64` near anything monetary.
- **`make verify-full` is expensive on purpose** — it resets Docker volumes
  and runs the complete repeatable non-chaos gate, including integration,
  container, business, admin, and disposable load-smoke checks. Use
  `make verify-chaos` separately for the dependency-kill recovery drill. Use
  the normal gate (`go build`, `go vet`, `make lint`, `make test`) for most
  changes; reach for `verify-full` only for money-movement, persistence,
  messaging, or service-startup changes (the [project guide](project-guide.md)
  spells out the line).
- **`scripts/lib.sh` recomputes `WORK_DIR`/`GENTOKEN_BIN` per shell
  invocation.** Source it once per debug session, not once per command, or
  helpers like `gen_token` silently break.

## Contributor terms

The [shared glossary](../reference/glossary.md) owns the plain-English definitions.
The notes below explain their repository-specific consequences:

- **[Idempotency key](../reference/glossary.md#idempotency-key)** — caller-supplied key required on every posting
  command; replays with the same key are safe no-ops, not duplicate
  postings.
- **[Outbox](../reference/glossary.md#outbox)** — a table written in the same DB
  transaction as a ledger posting, later relayed to RabbitMQ by a worker;
  guarantees "posted" and "event will eventually be published" never
  diverge.
- **[KYC tier / KYC level](../reference/glossary.md#kyc)** — 0/1/2 identity-verification level on a user;
  gates policy limits enforced in `services/ledger/policy`, applied via
  `Provisioner.ApplyKycTier`.
- **[Fraud screening boundary](../reference/glossary.md#fraud-screening)** — synchronous screening happens before a
  public ledger posting opens its database transaction, before Payin posts a
  confirmed top-up, and before Payout creates a request or hold. Explicit
  velocity-backend failures fail closed; other transport failures follow the
  caller's documented fail-open policy. The old in-transaction `PrePostHook`
  was removed by plan 37 and appears only in historical plans.
- **[Maker-checker](../reference/glossary.md#maker-checker)** — a two-operator approval pattern for sensitive admin
  actions (e.g. ledger adjustments); one operator proposes, a different
  one approves.
- **[SPIFFE-style URI SAN](../reference/glossary.md#spiffe-style-uri-san)** — the internal mTLS identity scheme
  (`internal/platform/security/tls`); a certificate's identity is its URI SAN, never its Common
  Name.

## First day checklist

1. `cp .env.example .env`, `make docker-up`, `make migrate-up-all`,
   `docker compose --profile app up --build -d` (see README for details).
2. `POST` to `http://127.0.0.1:8082/api/v1/auth/register` and confirm you
   get a token pair back, then hit `http://127.0.0.1:8080` with it — proves
   both public edges and the ledger provisioning path work.
3. Read the [project guide](project-guide.md) top to bottom once — it's short,
   and every rule in it exists because someone got burned skipping it.
4. Walk the "trace one request" section above with the actual files open.
5. Run `go test ./...` and `go test -tags=integration ./...` once, cold,
   so you know what green looks like before you change anything.
6. Pick a small, contained first task (a `_test.go` gap, a lint warning, a
   docs/operations/runbooks correction) before touching `execTransfer` or anything in
   `services/ledger/internal/ledger/handle`.
