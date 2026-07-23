# Shared Infrastructure Deep Dive (`pkg/`)

> [Documentation home](../README.md) · [Reference](README.md)

> **Status: Current. Audience: contributors working below the business
> services.** Start with [Services](services.md) if you do not yet know which
> business component needs the shared infrastructure described here.

[Services](services.md) covers business logic; [Operations](../operations/README.md)
covers the tooling that builds/runs/verifies it. This document covers the
17 packages under `pkg/` — the infrastructure every service is built out
of — plus the handful of cross-cutting `internal/` packages that exist for
the same reason but can't live in `pkg/` for boundary reasons explained
below. Every type/function listed was checked directly against the
current source, not assumed.

## General: what belongs in `pkg/`, and why

**The rule** (already stated in
[Project guide](../development/project-guide.md#service-boundaries), repeated here
because it's the single fact that explains this entire directory):
**`pkg/` must never import `internal/`.** Dependency only flows one way —
`cmd → internal → pkg`. A package belongs in `pkg/` exactly when it has
**zero knowledge of Seev's business domain** — no idea what a ledger
transaction, a KYC tier, or a payout is. `pkg/database` doesn't know
"account_balances" exists; `pkg/messaging` doesn't know
"ledger.transaction.posted.v1" exists. That's what makes every package
here safely reusable by all eight services without creating a hidden
coupling between otherwise-independent domains.

**The problem this solves**: without this rule, "shared code" tends to
accrete business assumptions over time (a generic HTTP client slowly grows
a `GetLedgerBalance` method, a generic cache slowly grows a
`CacheKYCStatus` method) until nothing is actually shared anymore — every
service ends up depending on every other service's concerns through a
supposedly-neutral layer. `pkg/` staying domain-neutral is what lets
`internal/ledger`, `internal/auth`, `internal/payin`, etc. be extracted,
recomposed, or even deleted independently, which is the entire premise
[Architecture](architecture.md#3-how-it-was-built-monolith-first-services-only-with-evidence)
describes for how this repo grew from one binary into eight.

**Taxonomy** — the 17 packages fall into five concerns:

| Concern | Packages |
|---|---|
| Security & transport identity | `tlsx`, `grpcx`, `middleware` (auth/cors/security parts) |
| Data access & state | `database`, `cache`, `currency` |
| Messaging & scheduled work | `messaging`, `scheduler`, `alerting` |
| HTTP/gRPC request plumbing | `middleware` (the rest), `response`, `ledgerclient`, `ledgererr`, `fraudcheck` |
| Observability | `logger`, `tracing` |
| General-purpose utilities | `generalerror`, `generalutil` |

None of these packages talk to each other's internals across concern
boundaries either — `pkg/tlsx` doesn't import `pkg/grpcx` (it's the other
way around), `pkg/cache` doesn't know `pkg/scheduler` exists. Each is
independently testable and independently understandable.

---

## Security & transport identity

### `pkg/tlsx`

**Problem it solves**: every internal hop (gRPC and HTTP, across all
eight services) needs to cryptographically prove *which service* is
calling, not just that *some* certificate was presented — and that proof
has to survive a certificate rotation without a process restart.

**What's inside**: `CertSource` (`NewCertSource`, `LoadFromDir`) — loads
and hot-reloads a leaf cert + key + CA pool from disk, extracting the
SPIFFE-style URI SAN identity (`spiffe://seev/<service>`) as the only
thing ever trusted about a peer, never a Common Name. `ServerConfig`
builds a `*tls.Config` that verifies an inbound connection's identity
against an explicit allowlist. `ClientConfig`/`HTTPClient` build the
outbound equivalent, pinned to one expected server identity.

**The bug this session found and fixed here**: `ServerConfig` originally
snapshotted its CA pool at construction time — after `certgen rotate`,
every server rejected freshly-issued clients forever, since Go's
`RequireAndVerifyClientCert` verified against the frozen pool. Fixed by
switching to `RequireAnyClientCert` + a manual `VerifyConnection` that
reads `src.CAPool()` fresh on every handshake. Full writeup in
[docs/security/threat-model.md](../security/threat-model.md) (TM-13).

**Used by**: `pkg/grpcx` (server + client TLS config), every service's
internal HTTP server/client, `cmd/certgen` (the identity/cert issuer
itself).

### `pkg/grpcx`

**Problem it solves**: every one of the 13 internal gRPC hops needs the
same server/client plumbing (mTLS wiring, a shared-token check as
defense-in-depth, request-ID propagation, panic recovery, structured
logging) — re-deriving this per service risks one service quietly
skipping a control the others have.

**What's inside**: `NewServer(logger, token, tlsConfig, opts...)` — fails
fast (refuses to start) if the token is empty or the TLS config is nil,
rather than booting into a silently-unauthenticated state. `Dial`/
`DialLazy` — the client-side equivalent, `DialLazy` deferring the actual
connection attempt for callers that shouldn't block startup on a
dependency being up yet. Built-in interceptors: request-ID propagation,
panic-to-Internal-error conversion (the server survives a handler panic),
structured request logging.

**Used by**: every service that exposes or calls an internal gRPC API
(Ledger, Auth, Payin, Payout, Fraud, Assurance, Gateway).

### `pkg/middleware` (security-relevant half)

**Problem it solves**: JWT verification, CORS policy, and security
headers need to be identical in behavior across every public and admin
HTTP surface — a one-off variant on a single service is how a real gap
like TM-06 (wildcard CORS default) or TM-07 (skippable JWT issuer check)
happens.

**What's inside** (this half): `WithAuth` (JWT verification + claims
injection), `WithRole` (role-gate a handler), `WithCORS` +
`DefaultCORSConfig` (empty-by-default allowlist, not `*` — the TM-06 fix),
`WithSecurityHeaders`, `RequireJSON`. See the next section for the
non-security half.

**Used by**: every service's public and admin HTTP router.

---

## Data access & state

### `pkg/database`

**Problem it solves**: every service needs the same Postgres connection
handling (pooling, config, a transaction helper) without each one
re-implementing `sql.DB` wiring slightly differently.

**What's inside**: the `DatabaseSQL` interface (the abstraction every
repository in the repo codes against, per
[Onboarding](../development/onboarding.md#naming-conventions)'s repository
convention), `DBSQL` (the real `*sql.DB`-backed implementation),
`NewFromSQL`, `Config`, and a generated `MockDatabaseSQL` for unit tests
that never touch a real database.

**Used by**: literally every repository package in the repo — this is the
single most-imported package under `pkg/`.

### `pkg/cache`

**Problem it solves**: Redis is used for three different things (rate
limiting, distributed counters, generic caching) across services that
must each degrade predictably — not identically, but *by documented
contract* — when Redis goes down, per
[docs/security/threat-model.md](../security/threat-model.md)'s
fail-open/fail-closed distinction.

**What's inside**: `Limiter` (`RedisRateLimiter`, `MemoryRateLimiter`,
`FailoverLimiter` — the last one auto-degrades to the in-memory
implementation and recovers automatically), `Counter`
(`RedisCounter`/`MemoryCounter`/`FailoverCounter`, same pattern),
`RedisHealthSwitcher` (the shared "is Redis actually healthy right now"
primitive both failover types are built on), a generic `Cacher`/`Cache`
wrapper, and `MockCache`/`MockLimiter` for tests.

**Used by**: `pkg/middleware`'s rate limiter, Fraud's velocity store
(`internal/fraud/velocity_store.go` — the fail-*closed* exception to this
package's generally fail-open failover pattern, and the source of TM-14),
Auth's login rate limiting.

### `pkg/currency`

**Problem it solves**: currency codes and their minor-unit exponent
(IDR has 0 decimal places, most currencies have 2) need one authoritative
runtime source, not a hardcoded assumption scattered across every place
that formats or validates an amount.

**What's inside**: a runtime registry bootstrapped with IDR only (this
platform's original single-currency assumption), loaded from the
`currencies` table via `Load` — `internal/ledger.NewModule` calls it once
at startup with the DB-backed currency list.

**Used by**: `internal/ledger` (amount validation, FX primitives),
anywhere a minor-unit exponent needs to be looked up rather than assumed.

---

## Messaging & scheduled work

### `pkg/messaging`

**Problem it solves**: the transactional-outbox pattern (write the event
in the same DB transaction as the posting, relay it to RabbitMQ
separately) needs one correct, well-tested broker client — a
re-implementation per service risks the exact kind of subtle
publish/consume bug that silently loses or duplicates an event.

**What's inside**: `RabbitMQ` (the concrete broker, `New`/`NewWithRegistry`),
the `Publisher`/`Consumer`/`TopologyManager`/`Broker` interfaces every
consuming service codes against (never the concrete type — this is what
lets `internal/fraud`'s and `internal/notify`'s consumers be tested
without a real broker), `HandlerFunc`, `PublishOptions`/`ConsumeOptions`,
a `RetriableError` wrapper so a handler can distinguish "retry this
delivery" from "this message is permanently bad," and
`WithCorrelationID`/`CorrelationIDFromContext` for tracing an event across
its publish → consume hop.

**Used by**: Ledger (outbox relay, the only publisher), Fraud's velocity
consumer, Gateway's `internal/notify` consumer — both independent
subscribers to the same `ledger.transaction.posted.v1` event, per
[Architecture](architecture.md#52-one-transaction-start-to-finish)'s
transaction walkthrough.

### `pkg/scheduler`

**Problem it solves**: five different background jobs across three
services (Ledger's snapshot/accrual/verifier/schedule-runner, Auth's
KYC-retry/rescreen, Admin BFF's session cleanup) all need the same
distributed-locking guarantee — only one replica should run a given cron
job at a time — without each one hand-rolling Redis lock logic.

**What's inside**: documented in its own
[pkg/scheduler/README.md](../../pkg/scheduler/README.md) — extended cron syntax
(`W`/`L`/`#`), Redis or in-memory distributed locking, graceful shutdown
that waits for an in-flight job, per-job panic recovery, and a pluggable
metrics interface.

**Used by**: every background job listed in
[Services](services.md)'s per-service "Background" rows.

### `pkg/alerting`

**Problem it solves**: multiple independent verification/monitoring
concerns (ledger integrity, Assurance findings) need to fire an external
webhook alert without each one owning its own HTTP-posting logic and
retry/timeout policy.

**What's inside**: deliberately minimal and domain-neutral — an
`AlertFunc` type any caller can implement, plus a webhook-posting
implementation. It has no idea what a "trial balance discrepancy" or an
"assurance finding" is; the caller decides what's alert-worthy.

**Used by**: `internal/ledger/worker/verifier.go` (integrity alerts),
`internal/assurance/alert.go` (finding-severity alerts).

---

## HTTP/gRPC request plumbing

### `pkg/middleware` (the rest)

**What's inside** beyond the security half: `Chain` (composes middleware
in order — every router in the repo uses this same composition pattern,
per [Onboarding](../development/onboarding.md#how-a-services-folder-is-organized)),
`WithRequestID`, `WithRoutePattern` (used by the rate limiter to key on
route, not raw path, so `/users/{id}` doesn't create unbounded rate-limit
buckets), `WithHTTPMetrics`, `WithLogger` (the source of TM-12's body
truncation bug — see the threat model for the fix), `WithRecovery`,
`WithTimeout`, `WithTracing`.

### `pkg/response`

**Problem it solves**: every HTTP handler in the repo needs to return a
consistently-shaped JSON response (success envelope, error envelope,
pagination metadata) — inconsistent response shapes across services is
exactly the kind of thing that makes a shared client library painful to
write.

**What's inside**: `OK`/`Created`/`Accepted`/`NoContent`/`OKWithMeta` for
success responses, `BadRequest`/`Unauthorized`/`Forbidden`/`NotFound`/
`Conflict`/`UnprocessableEntity`/`TooManyRequests`/`ServiceUnavailable`/
`InternalServerError` for errors (each mapping to the right HTTP status
with a consistent body shape), `Decode` (JSON request decoding with
sane defaults: rejects unknown fields, bounds body size).

**Used by**: every HTTP handler in every service.

### `pkg/ledgerclient`

**Problem it solves**: every service that posts a transaction (Payin,
Payout, and previously in-process callers) needs a typed, boundary-clean
way to call Ledger's `Post` RPC without reaching into
`internal/ledger`'s implementation packages — which
[Project guide](../development/project-guide.md#service-boundaries) forbids outright.

**What's inside**: `Command` (the typed posting request), `Transaction`
(the typed response), `Client.New(conn)` — a thin wrapper over the raw
gRPC stub that hides the wire format and translates gRPC errors via
`pkg/ledgererr`.

**Used by**: Payin, Payout — anywhere that needs to post a transaction
without importing `internal/ledger`.

### `pkg/ledgererr`

**Problem it solves**: a raw gRPC status code (`FAILED_PRECONDITION`,
`ALREADY_EXISTS`) is meaningless to a caller without knowing Ledger's
specific error semantics — every caller needs the same translation, not
each one guessing at what a given code means for this specific service.

**What's inside**: `FromStatus(err)` — translates a gRPC status back into
a stable, typed `LedgerError` (with retryability metadata preserved),
`ErrAlreadyClosed` as a named sentinel for the one case callers commonly
need to branch on by identity rather than message.

**Used by**: `pkg/ledgerclient`, anywhere a raw Ledger gRPC error needs to
become an actionable Go error.

### `pkg/fraudcheck`

**Problem it solves**: three independent callers (Ledger's P2P transfer
path, Payin's topup path, Payout's payout-create path) all call Fraud's
`Screen` RPC and all need the *exact same* timeout and fail-open contract
— a per-caller reimplementation is how one path quietly ends up with a
different failure posture than the other two, which is itself a security
gap.

**What's inside**: one shared client implementing the contract: `Check`
returns a non-nil error *only* for an infrastructure failure (unreachable
service, timeout, malformed response) — the caller decides fail-open or
fail-closed on that error (every current caller fails open, logged as
`ERROR`). A **nil** error with `Verdict.Block == true` is a definite
business decision the caller **must** honor — the transaction is rejected
before any posting happens. That distinction (infra-error vs.
business-verdict) is the entire point of the package existing as shared
code rather than three separate gRPC calls.

**Used by**: `internal/ledger` (P2P transfer `PrePostHook`), `internal/payin`
(topup), `internal/payout` (payout create), `internal/auth` (KYC sanctions
screening, via a locally-typed equivalent interface — see
[Services](services.md)'s Auth section).

---

## Observability

### `pkg/logger`

**Problem it solves**: every service needs structured, correlation-aware
logging that never leaks credentials, full idempotency keys, or other
replayable values into a log line an operator or a log-aggregation system
might read.

**What's inside**: `New(cfg)` (a configured `*slog.Logger`),
`WithContext`/`FromContext`/`FromContextOrDefault`/`With` (request-scoped
logger propagation through `context.Context`), and the masking layer:
`ReadAndMaskRequestBody`, `MaskResponseBody`, `SanitizeHeaders` — redacts
known-sensitive JSON keys and header values before they're ever logged.

**The bug this session found and fixed here**: `ReadAndMaskRequestBody`
read the body through a size-limited reader for logging purposes, then
reconstructed `r.Body` from that *already-truncated* slice instead of the
original — silently truncating every request over 16KiB before it reached
the real handler, not just before it reached the log line. Full writeup
in the threat model (TM-12).

**Used by**: `pkg/middleware.WithLogger`, and directly by any code that
wants a request-scoped logger.

### `pkg/tracing`

**Problem it solves**: distributed tracing across service boundaries only
works if every service installs the *same* OpenTelemetry configuration —
a per-service `setupTracing` (which is literally how this package started,
duplicated in `cmd/gateway` and `cmd/ledger-service`) drifts the moment
one service's config changes and the others don't follow.

**What's inside**: one shared `TracerProvider` installer, used by all
eight services (opt-in — this repo runs correctly with tracing
disabled), feeding Tempo via the observability profile described in
[Operations](../operations/README.md#5-observability-deployobservability--optional-operator-facing).

**Used by**: every service's startup wiring, `pkg/middleware.WithTracing`.

---

## General-purpose utilities

### `pkg/generalerror`

**Problem it solves**: "is this a duplicate-key violation" and "is this
error safe to retry" are questions asked constantly across every
repository package, against the *same* underlying Postgres driver error
shapes — worth answering once, correctly, rather than per call site.

**What's inside**: `IsDuplicateKey(err)`, `IsRetryable(err)` — thin,
driver-aware helpers.

**Used by**: repository packages across every service (e.g.
`internal/auth/repository/user_repository.go`'s `CreateUser` translating
a duplicate-key error into `ErrDuplicateEmail`).

### `pkg/generalutil`

**Problem it solves**: a handful of small, genuinely domain-neutral
operations (extracting a typed value out of a `map[string]any` metadata
blob, building a SQL placeholder list for an `IN (...)` clause,
generating a sortable UUID, nullable-value helpers) are needed by enough
unrelated packages that duplicating them is worse than one shared,
well-tested home.

**What's inside**: `MetaString`/`MetaUUID`/`MetaDecimal` (typed metadata
extraction), `BuildArgs(ids)` (the `$1,$2,...` placeholder-list builder —
used in `internal/ledger/repository/account_balance_repository.go`, but
a few other ledger repository files still hand-roll the same
`strings.Builder`-based pattern instead of calling it, a known, minor
consistency follow-up rather than a correctness issue),
`NewV7()` (sortable UUIDv7 generation), `Deduplicate`,
`SortedDecimalKeys`, and small nullable-value helpers
(`NullString`/`NullUUID`/`StringPtr`/`UUIDPtr`).

**Used by**: scattered across repository and service packages wherever
one of these specific small operations is needed.

---

## Cross-cutting `internal/` packages (same reason, different boundary)

Two more packages are genuinely shared the same way `pkg/*` is, but can't
live under `pkg/` because they *do* know about the repo's specific
services/config shape — which is exactly what `pkg/` is defined to
exclude:

### `internal/config`

**Problem it solves**: every service reads roughly the same shape of
environment configuration (JWT secrets, database DSN, per-service URLs,
feature toggles) — one shared loader/validator means a missing or
malformed required variable fails boot loudly and identically everywhere,
rather than each service's `main.go` re-deriving its own validation (and
inevitably validating a little differently).

**What's inside**: env loading and `validate()` — the function that,
per the TM-07 fix in the threat model, now refuses to boot any service
when `JWT_ISSUER` is empty, in every environment, not just production. An
optional `vaultGetenv` seam overlays HashiCorp Vault dev-mode secrets on
top of plain env vars when `VAULT_ADDR`/`VAULT_TOKEN` are set (see
[docs/operations/runbooks/vault-seed.md](../operations/runbooks/vault-seed.md)).

**Used by**: every service's `main.go` — the first thing each one does.

### `internal/testutil`

**Problem it solves**: integration tests across every service need the
same testcontainers-based Postgres/Redis bootstrap — duplicating this
setup per service risks subtle drift between what each service's
integration suite is actually testing against.

**What's inside**: shared test-database provisioning and teardown helpers
for `-tags=integration` test files.

**Used by**: every `*_integration_test.go` file in the repo.

---

## Where this fits with everything else

| You want... | Read |
|---|---|
| Why the system is built this way at all | [Architecture](architecture.md) |
| What each service does with this infrastructure | [Services](services.md) |
| How this infrastructure is built, verified, and observed | [Operations](../operations/README.md) |
| The security findings referenced above, in full | [docs/security/threat-model.md](../security/threat-model.md) |
| The rules for changing shared code | [Project guide](../development/project-guide.md#service-boundaries) |
