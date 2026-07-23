# Service Deep Dive

> [Documentation home](../README.md) · [Reference](README.md)

> **Status: Current. Audience: technical readers.** For an everyday-language
> introduction to these same services, start with the
> [beginner guide](../learn/beginner-guide.md), continue through the
> [product tour](../learn/product-tour.md), and use the
> [glossary](glossary.md) for unfamiliar terms.

[Architecture](architecture.md) covers the system as a whole; this
document goes one level deeper into **each of the eight services**: the
specific problem it exists to solve, what data it owns, everything it can
actually do (every HTTP/gRPC endpoint and background job), and how it
depends on — or is depended on by — the others. Read this when you need to
know "can service X already do Y" before writing new code, or when you're
about to work inside one service and want the full picture of its surface
area first.

Every code reference below was checked directly against the current
routers, gRPC servers, and migrations — not assumed from a plan document.

## How to read a service section

Each section answers the same questions: what real-world problem the service
solves, what data and decisions it owns, how other components reach it, what
it depends on, and what it deliberately does not do. The opening paragraphs
are the mental model; endpoint and package tables are implementation
reference.

One boundary is intentionally easy to misread: `internal/vendorgw` is a shared
library loaded inside Payin and Payout today. It is not a running
VendorService. The separate service is a [target design](../roadmap/active/54-vendor-service-boundary.md).

---

## Gateway

**In plain English**: Gateway is the customer-facing front desk. It checks the
request context and forwards work to the service that owns the decision. It
does not own balances, top-up intents, or withdrawal state.

**Problem it solves**: end-user client apps need exactly one place to
talk to, with one auth story, one rate limiter, and one set of security
headers — without knowing that "the backend" is actually seven other
services. Gateway is the composition layer that makes the rest of the
system look like a single API from the outside.

**What's inside**: `internal/handler` (routing + composition),
`internal/server` (HTTP server, graceful shutdown), `internal/notify`
(user-facing notifications, its own small module with its own table).
Gateway owns `seev_gateway`, containing exactly one table:
`notif_notifications`. It holds almost no business logic of its own —
everything it does is validate/compose/forward.

**What it can do**:

| Surface | Endpoint | Behind |
|---|---|---|
| HTTP (public) | `POST /api/v1/topup`, `GET /api/v1/topup/{id}` | JWT + KYC tier 1 required — calls Payin over gRPC |
| HTTP (public) | `POST /api/v1/payout`, `GET /api/v1/payout/{id}` | JWT + KYC tier 1 required — calls Payout over gRPC |
| HTTP (public) | `/api/v1/ledger/*` | JWT (+ KYC tier for postings) — reverse-proxies the ledger API surface to ledger-service |
| HTTP (public) | `GET /api/v1/notifications`, `POST /api/v1/notifications/{id}/read` | JWT — served directly by gateway's own `internal/notify` |
| HTTP (public, unauthenticated) | `POST /webhooks/{vendor}` | HMAC-verified by Payin itself, not by gateway — gateway only forwards raw bytes over gRPC (`HandleWebhook`) |
| HTTP (public probes) | `GET /health`, `GET /ready` | none |
| HTTP (internal ops) | `GET /metrics` on `:8081` | mTLS service identity |
| Background | RabbitMQ consumer in `internal/notify` | consumes `ledger.transaction.posted.v1` to create in-app notifications |

**Notably does NOT do**: registration, login, or KYC — those hit
Auth-service directly on its own public port (`:8082`), not through
gateway. See [Onboarding](../development/onboarding.md#service-map-name--code--data)
for why that's easy to assume wrong.

**Depends on**: Auth (JWT verification via shared secret, not a live
call), Payin, Payout, Ledger (gRPC/proxy), RabbitMQ (notifications).
**Depended on by**: nothing internal — it's a leaf in the call graph from
every other service's perspective.

---

## Auth

**In plain English**: Auth proves who is using the wallet and what identity
checks they have completed. It does not decide whether accounting entries
balance.

**Problem it solves**: every other service needs to know "who is this"
and "how verified are they" without re-implementing credential storage,
session/token lifecycle, or KYC compliance workflow themselves. Auth is
the single owner of user identity and the only service allowed to decide
a user's KYC tier.

**What's inside**: a flat `Module` facade split by concern —
`auth.go` (register/login/refresh/me), `bootstrap.go` (seeding the first
admin account), `kyc.go` (submission/approval/rejection flow),
`kyc_retry.go` (durable retry worker wiring), `documents.go` (encrypted
KYC document storage). `internal/auth/repository` is split into
`UserRepository`, `RefreshTokenRepository`, and `KYCRepository` (one
struct per interface — see [Onboarding](../development/onboarding.md#naming-conventions)
for why). `internal/auth/worker` holds the two background jobs.
`internal/kycvendor` (sibling package, not under `internal/auth`) is the
pluggable third-party KYC verification client. Owns `seev_auth`:
`auth_users`, `auth_credentials`, `auth_refresh_tokens`,
`kyc_submissions`, `kyc_documents`, `kyc_level_changes`,
`kyc_apply_retries`.

**What it can do**:

| Surface | Endpoint | Behind |
|---|---|---|
| HTTP (public) | `POST /api/v1/auth/register`, `POST /api/v1/auth/login`, `POST /api/v1/auth/refresh` | rate-limited, unauthenticated by design |
| HTTP (public) | `GET/PUT /api/v1/users/me` | JWT |
| HTTP (public) | `POST /api/v1/users/me/kyc`, `GET /api/v1/users/me/kyc`, `POST /api/v1/users/me/kyc/documents` | JWT |
| HTTP (internal admin) | `GET /api/v1/admin/kyc/submissions`, `POST .../approve`, `POST .../reject`, `POST /api/v1/admin/kyc/users/{id}/downgrade`, `GET /api/v1/admin/kyc/documents/{id}` | admin/maker/checker role |
| Background | KYC apply-retry job | claims durable intents when a KYC approval's ledger-side tier apply failed transiently; re-runs the full limits-first flow (`internal/auth/worker/retry.go`) |
| Background | Sanctions re-screen job | periodically re-submits already-approved KYC subjects to the sanctions checker (`internal/auth/worker/rescreen.go`) |

**A KYC approval is deliberately limits-first, not claim-first**: the
ledger's policy-tier limits are applied *before* the user's `kyc_level` is
allowed to advance, inside the same transaction where possible — and
durably retried via the queue above when the ledger call fails, rather
than ever letting a user's claimed tier get ahead of what they're actually
allowed to do.

**Depends on**: Ledger (`ProvisionUser` on register, `ApplyKycTier` on
approval/downgrade), Fraud (sanctions screening on KYC submission,
optional), `internal/kycvendor` (pluggable identity verification
provider).
**Depended on by**: Gateway and Admin BFF (JWT verification is stateless
— they just need the shared secret, not a live call to Auth), every
service indirectly (JWT claims carry the identity everything else trusts).

---

## Ledger

**In plain English**: Ledger is the permanent accounting book. Other services
can request a movement, but only Ledger can make it part of the wallet's
financial history.

**Problem it solves**: this is the one place in the entire system where
"is this true" and "did this actually happen" have the same answer,
provably, forever. Every other service can be rebuilt from scratch and
the business survives; if the ledger is wrong, nothing else matters.

**What's inside**: the largest and most structurally decomposed module in
the repo (the reference implementation for
[Project guide](../development/project-guide.md#package-layout-conventions)'s Tier 2
convention) — `service/{handle,accrual,adjustments,disbursement,provision,
recon,schedule}` (one subpackage per independent sub-process),
`repository/` (one file per aggregate), `processors/` (per-transaction-type
posting logic — `money_in`, `money_out`, `transfer_p2p`, holds, reversals,
FX pairs, and more), `feepolicy/`, `worker/` (four background jobs),
`transport/` (the HTTP surface), `grpcserver/` (the internal RPC surface).
Owns `seev_ledger`: `accounts`, `account_balances`,
`account_balance_snapshots`, `ledger_transactions`, `ledger_entries`,
`currencies`, `policy_limits`, `policy_tier_limits`, `fee_rules`,
`fee_quotes`, `outbox_events`, `pending_adjustments`, `recon_batches`,
`recon_items`, `scheduled_transactions`, `disbursement_batches`,
`disbursement_items`, `savings_config`.

**What it can do**:

| Surface | Endpoint | Behind |
|---|---|---|
| HTTP (public-proxied via gateway) | `POST /transactions`, `GET /transactions/{id}` | JWT + KYC tier (for postings) |
| HTTP (public-proxied) | `GET /accounts`, `GET /accounts/{id}/balance`, `GET /accounts/{id}/entries`, `GET /accounts/{id}/statement` | JWT |
| HTTP (public-proxied) | `POST /accounts/pockets` (named sub-wallets) | JWT |
| HTTP (public-proxied) | `POST /fees/quote` (exact, enforceable fee quotes) | JWT |
| HTTP (public-proxied) | `GET/POST /schedules`, `POST /schedules/{id}/{cancel,pause,resume}` | JWT — recurring/scheduled transactions |
| HTTP (admin) | `GET /admin/adjustments[/{id}]`, `POST /admin/adjustments`, `POST .../{id}/{approve,reject}` | maker-checker enforced *inside this service*, not just gated by role |
| HTTP (admin) | `POST /admin/disbursements`, `GET /admin/disbursements/{id}`, `POST .../{id}/run` | admin — batch payouts (e.g. interest, refund runs) |
| HTTP (admin) | `GET /admin/recon/batches[/{id}]`, `POST /admin/recon/batches`, `POST /admin/recon/items/{id}/resolve` | admin — external settlement reconciliation |
| HTTP (admin) | `POST /admin/schedules/run` | admin — trigger the due-schedule worker immediately |
| HTTP (admin) | `GET/POST /admin/ledger/fee-rules`, `PUT /admin/ledger/fee-rules/{id}` | admin — database-driven fee rule CRUD |
| HTTP (admin) | `GET /admin/outbox/dead`, `POST .../replay-all`, `POST .../{id}/replay` | admin — dead-letter recovery for the event relay |
| HTTP (admin) | `GET /admin/savings`, `PUT /admin/savings/{account_id}` | admin — interest-bearing savings configuration |
| HTTP (admin) | `GET/PUT /admin/policy/limits` | admin — per-user transaction limits |
| HTTP (admin) | `GET /admin/reports/{kind}` | admin — read-only regulatory/compliance reporting |
| gRPC (internal) | `Post`, `GetTransactionByIdempotencyKey` | the only way any other service moves money |
| gRPC (internal) | `ProvisionUser`, `ApplyKycTier` | called by Auth |
| gRPC (internal) | `GetUserCurrency`, `ResolveFee`, `ConsumeFeeQuote` | called by Payin/Payout |
| gRPC (internal) | `BatchGetAssuranceTransactions` | the ONLY thing Assurance is allowed to call — read-only, batched |
| Background | Outbox relay | publishes posted transactions to RabbitMQ with backoff + dead-letter |
| Background | Verifier (`worker/verifier.go`) | hourly trial-balance check + daily projection audit — the thing that would catch a correctness bug before a human does |
| Background | Snapshot (`worker/snapshot.go`) | daily balance snapshots, enabling fast as-of queries without scanning full history |
| Background | Accrual (`worker/accrual.go`) | interest accrual on savings-configured accounts |
| Background | Schedule runner (`worker/schedule_runner.go`) | executes due scheduled/recurring transactions |

**Depends on**: nothing internal for its core posting engine — this is
deliberate. The public HTTP transport screens with Fraud before opening a
ledger database transaction; internal posting calls do not make that network
round trip. A known fraud-velocity dependency failure blocks the public
request, while other screening transport failures follow the documented
fail-open path.
**Depended on by**: every other service, directly or via events. This is
the center of the dependency graph.

---

## Payin

**In plain English**: Payin owns the plan for money entering a wallet. A
pending top-up is only an expectation; Payin must validate confirmation before
asking Ledger to record money.

**Known current limitation**: the implementation retains a backward-
compatibility fallback that can use `user_id` from a verified vendor payload
when no top-up intent matches. That behavior is current code, not the intended
final trust model. [Plan 54](../roadmap/active/54-vendor-service-boundary.md) removes
it, requires strict intent/vendor correlation, and moves direct vendor traffic
to VendorService.

**Problem it solves**: getting money *into* the system from a payment
gateway vendor — an untrusted, asynchronous, sometimes-duplicate,
sometimes-out-of-order webhook source — and turning that into exactly one
correct ledger posting, no matter how many times the vendor retries.

**What's inside**: `topup.go` (intent lifecycle), `routing.go` +
`routing_http.go` (database-driven vendor selection — which payment
gateway handles which top-up, and failover between them), `intake.go`
(the emergency pause/resume circuit breaker Assurance can trigger),
`assurance.go` (the read-only projection Assurance is allowed to read),
`repository/` (topup + routing, split per
[Onboarding](../development/onboarding.md#naming-conventions)'s repository rule),
`grpcserver/` (internal RPC), shares `internal/vendorgw` with Payout for
the actual vendor adapter interface + circuit breaker + the local
`mockvendor` test double. Owns `seev_payin`: `payin_topup_intents`,
`payin_webhook_events`, `payin_routing_rules`, `payin_vendor_gateways`,
`payin_intake_control`, `payin_intake_commands`.

**What it can do**:

| Surface | Endpoint | Behind |
|---|---|---|
| gRPC (internal) | `CreateTopupIntent`, `GetTopupIntent` | called by Gateway |
| gRPC (internal) | `HandleWebhook` | called by Gateway, forwarding a vendor's raw signed payload |
| gRPC (internal) | `ListAssuranceRecords` | called ONLY by Assurance, read-only |
| gRPC (internal) | `GetIntakeControl`, `ApplyIntakeControl` | called by Assurance (auto) or an admin directly (manual fallback) — pause/resume new intent creation |
| HTTP (admin) | `GET /admin/payin/events`, `POST .../{id}/replay` | admin — webhook event history + manual replay |
| HTTP (admin) | `GET/POST/PUT /admin/payin/routing-rules[/{id}]` | admin — which vendor handles which top-up, in what priority order |
| HTTP (admin) | `GET/PUT /admin/payin/vendor-gateways/{vendor}` | admin — per-vendor credentials/config |
| HTTP (admin) | `GET /admin/payin/vendors/health` | admin — circuit breaker state per vendor |
| HTTP (admin) | `POST /admin/payin/intake/pause` | admin — the direct fallback if Assurance itself is down |

**Vendor resilience**: routing is priority-ordered and database-driven
(no redeploy to add/reorder a vendor), with a circuit breaker per vendor
(`internal/vendorgw`) so a failing gateway degrades gracefully to the next
one instead of blocking every top-up.

**Depends on**: Ledger (post the confirmed top-up) and Fraud (synchronous
screening before posting; explicit velocity-backend failure blocks the path).
**Depended on by**: Gateway (creates intents, forwards webhooks),
Assurance (reads intents + intake state, can pause intake).

---

## Payout

**In plain English**: Payout owns the withdrawal journey from reservation to
settlement or cancellation. It preserves uncertain vendor results instead of
guessing and risking a second payment.

**Problem it solves**: the mirror image of Payin, and structurally
harder — money leaving the system has to survive a crash *mid-flight*
(the vendor call was sent, but did it succeed?) without ever double-paying
or silently losing the request. Payout is built specifically to survive
that failure mode, proven with dedicated chaos tests.

**What's inside**: `orchestrate.go` (the state machine — hold → dispatch
→ settle/cancel), `http.go`, `routing.go` (database-driven vendor
selection, mirroring Payin), `intake.go`/`assurance.go` (same pattern as
Payin), `worker/resume.go` (the crash-recovery job — the single most
important background job in this service), `repository/`, `grpcserver/`,
shares `internal/vendorgw` with Payin. Owns `seev_payout`:
`payout_requests`, `payout_vendor_calls`, `payout_vendor_commands`,
`payout_routing_rules`, `payout_vendor_gateways`, `payout_intake_control`,
`payout_intake_commands`.

**What it can do**:

| Surface | Endpoint | Behind |
|---|---|---|
| gRPC (internal) | `CreatePayout`, `GetPayout` | called by Gateway |
| gRPC (internal) | `ListAssuranceRecords` | called ONLY by Assurance, read-only |
| gRPC (internal) | `GetIntakeControl`, `ApplyIntakeControl` | same pause/resume pattern as Payin |
| HTTP (admin) | `GET /admin/payout/requests` | admin — full request history/state |
| HTTP (admin) | `POST /admin/payout/requests/{id}/{cancel,retry}` | admin — operator intervention on a stuck request |
| HTTP (admin) | `GET/POST/PUT /admin/payout/routing-rules[/{id}]`, `GET/PUT /admin/payout/vendor-gateways/{vendor}` | admin — same routing model as Payin |
| HTTP (admin) | `GET /admin/payout/vendor-commands/dead`, `POST .../replay-all`, `POST .../{id}/replay` | admin — dead-letter recovery for vendor commands that never got a confirmed outcome |
| HTTP (admin) | `GET /admin/payout/vendors/health`, `POST /admin/payout/vendors/{vendor}/force-fail` | admin — circuit breaker state + operator-forced failover for a drill or a known-bad vendor |
| HTTP (admin) | `POST /admin/payout/intake/pause` | admin — direct fallback pause |
| Background | Resume job (`worker/resume.go`) | on startup and periodically, finds any payout left in an in-flight state after a crash and resumes it from exactly where it left off — this is what the "crash-mid-flight chaos test" in `scripts/chaos-test.sh` proves works |

**Depends on**: Ledger (post hold/settle/cancel), Fraud (synchronous screening
before request and hold creation), and the vendor's actual payout API (via
`internal/vendorgw`).
**Depended on by**: Gateway, Assurance.

---

## Fraud

**In plain English**: Fraud is a safety checker. It can recommend or enforce a
decision at documented boundaries, but it never edits balances itself.

**Problem it solves**: risk and compliance decisions (is this transaction
suspicious, is this person sanctioned) need to sit *outside* the code path
that actually moves money, so a fraud-service bug or outage can never
itself become a way to move money — and so the posture (block vs. allow
through) for each kind of failure is a conscious, tested decision instead
of an accident of implementation order.

**What's inside**: `fraud.go` (the `Screen` entrypoint + persistence),
`consumer.go` (the RabbitMQ consumer for asynchronous velocity checks —
extracted as its own file specifically because it's a distinct concern
from the synchronous path), `velocity_store.go` (the fail-closed Redis
velocity counter — hardened after finding TM-14 in the A6 review; see
`docs/security/threat-model.md`), `rules/` (screening
rule implementations), `sanctions/` (sanctions-list matching, loaded
offline by `cmd/sanctions-loader`), `repository/` (three interfaces:
screening, rule-mode, sanctions — one struct each, not a shared one — see
[Onboarding](../development/onboarding.md#naming-conventions)). Owns `seev_fraud`:
`screening_events`, `screening_rule_modes`, `sanctions_entries`.

**What it can do**:

| Surface | Endpoint | Behind |
|---|---|---|
| gRPC (internal) | `Screen` | called synchronously by Ledger's public transport, Payin before posting, Payout before request/hold creation, and Auth for KYC screening |
| HTTP (admin) | `GET /api/v1/admin/fraud/events` | admin — screening history |
| HTTP (admin) | `GET/PUT /api/v1/admin/fraud/rules/{rule}/mode` | admin — toggle a rule between enforce/monitor/off without a redeploy |
| Background | Velocity consumer (`consumer.go`) | consumes `ledger.transaction.posted.v1` asynchronously to update per-user velocity counters — deliberately NOT on the synchronous posting path, so it can never block or fail a transaction |

**Two different failure postures, on purpose**: ordinary screening transport
errors can fail open where the caller contract allows it, but an explicit
Redis velocity dependency failure fails closed before money movement. The
distinction is deliberate: an unavailable authoritative velocity counter
must not silently weaken a limit. TM-14 documents the bug that once blurred
those two cases and the fix that restored the fail-closed result.

**Depends on**: Redis (velocity counters — fail-closed on outage),
RabbitMQ (async velocity events), an offline-loaded sanctions dataset (no
outbound network calls of its own).
**Depended on by**: Ledger, Auth, Payin, and Payout at explicit pre-movement or
KYC screening boundaries; Admin BFF manages rule modes.

---

## Admin BFF

**In plain English**: Admin BFF is a controlled workspace for operators. It
does not bypass the owning service's authorization or financial rules.

**Problem it solves**: operators need one console with one login, but the
actual admin capabilities live scattered across six other services'
internal APIs — and sensitive actions (ledger adjustments, KYC decisions,
intake pauses) need segregation-of-duties and an audit trail that no
individual downstream service is positioned to enforce on its own for
actions that span services.

**What's inside**: `module.go` (wiring + router), `login.go` (session
lifecycle, CSRF), `proxy.go` (typed reverse-proxy to every downstream
admin API), `audit.go` (the audit log — write path and admin-facing read
API), `session.go`/`authclient.go` (pre-existing, session persistence +
the auth-service client used only for login), `client/` (typed HTTP
clients per downstream service), `web/` (server-rendered HTML console —
`dashboard`, `maker`, `payout`, `recon`, `catalog` pages, using
htmx + Pico CSS, no separate frontend build). Owns `seev_adminbff`:
`sessions`, `audit_log`. It intentionally has no domain tables — the
"catalog" page and every data-bearing page are live proxied reads, not a
local copy.

**What it can do**:

| Surface | Endpoint | Behind |
|---|---|---|
| HTTP (public-ish, but session not JWT) | `POST /login`, `GET /login`, `POST /logout` | HttpOnly session cookie, CSRF token on every mutating request |
| HTTP (console) | `GET /api/v1/admin/{maker,payout,recon,catalog}` | server-rendered HTML pages |
| HTTP (console) | `GET /api/v1/admin/audit` | the audit log itself, filterable |
| HTTP (proxy) | `/api/v1/admin/{ledger,policy}/...`, `/api/v1/admin/adjustments...` | reverse-proxied to Ledger, with a dedicated approve/reject path enforcing maker-checker at the UI + session layer (Ledger enforces the same≠same-person rule again, independently, server-side) |
| HTTP (proxy) | `/api/v1/admin/payin/...`, `/api/v1/admin/payout/...` | reverse-proxied to Payin/Payout |
| HTTP (proxy) | `/api/v1/admin/fraud/...` | reverse-proxied to Fraud |
| HTTP (proxy) | `/api/v1/admin/kyc/...` | reverse-proxied to Auth's admin API |
| HTTP (proxy) | `/api/v1/admin/gateway/...` | reverse-proxied to Gateway |
| Background | Session cleanup cron | every 5 minutes, purges expired sessions |

**Every mutating request through this service is audited**, regardless of
which downstream service it actually hits — the audit row records who,
what route pattern, which downstream, and the resulting outcome, even if
the downstream call itself fails.

**Depends on**: Auth (login only — mints its own downstream-scoped token
after that, never stores Auth's own access/refresh tokens), Ledger,
Payin, Payout, Fraud, Gateway (all as typed proxy targets).
**Depended on by**: nothing internal — it's a leaf, just like Gateway, but
for operators instead of end users.

---

## Assurance

**In plain English**: Assurance is an independent inspector. It compares what
Payin and Payout believe with what Ledger recorded, reports disagreements, and
never repairs money automatically.

**Problem it solves**: every other service *believes* it's keeping the
ledger and its own state in sync — but belief isn't proof. Assurance is
the independent auditor that trusts nothing and re-derives the truth by
comparing Payin/Payout's own records against the Ledger's, continuously,
and it is architecturally incapable of "fixing" what it finds by writing
data — the only power it has is to raise an alarm and, in an emergency,
slow new money-in/money-out to a stop.

**What's inside**: `module.go` (wiring, the periodic run loop),
`correlation.go` (the actual payin↔ledger and payout↔ledger comparison
logic — deliberately one file, since both directions share real helper
logic rather than being artificially split), `cursor.go` (durable
scan-position tracking so a restart resumes instead of re-scanning
everything), `finding.go` (finding lifecycle: open → acknowledge →
resolve, or reopen if the mismatch recurs), `alert.go` (delivery to an
external alert webhook), `metrics.go`. Owns `seev_assurance`:
`assurance_runs`, `assurance_cursors`, `assurance_findings`,
`assurance_alert_deliveries`, `intake_control_commands`.

**What it can do**:

| Surface | Endpoint | Behind |
|---|---|---|
| HTTP (admin) | `GET /admin/assurance/summary` | admin — current health at a glance |
| HTTP (admin) | `GET /admin/assurance/runs`, `POST /admin/assurance/runs` | admin — run history + trigger a manual run |
| HTTP (admin) | `GET /admin/assurance/findings`, `POST .../{id}/acknowledge`, `POST .../{id}/resolve` | admin — finding lifecycle |
| HTTP (admin) | `GET /admin/assurance/intake` | admin — current pause state per flow |
| HTTP (admin) | `POST /admin/assurance/intake/{flow}/pause` | admin — emergency stop on new payin or payout intake |
| HTTP (admin) | `POST /admin/assurance/intake/{flow}/resume-requests`, `POST .../{id}/approve` | admin — resume requires a SECOND, different principal than whoever requested it |
| Background | Correlation run | on a fixed interval, batches through Payin/Payout/Ledger via gRPC, cross-checks money-in/money-out records against ledger postings, and raises/updates findings |

**Architecturally read-only, not just policy-read-only**: it holds no DB
credentials for Payin/Payout/Ledger at all — every fact it knows comes
through their own authenticated, purpose-built gRPC contracts
(`ListAssuranceRecords`, `BatchGetAssuranceTransactions`), and its own
database contains only its own findings/run history, never a copy of
domain data it could accidentally treat as authoritative.

**Depends on**: Payin, Payout, Ledger (all read-only gRPC).
**Depended on by**: nothing internal — but operators depend on it heavily
as the trust layer over everything else.

---

## Supporting utilities (not services — no listener, run once)

| Binary | Problem it solves |
|---|---|
| `cmd/certgen` | Issues and rotates the internal mini-CA and every service's short-lived mTLS leaf certificate — the identity foundation the whole internal-security model (§5.4 of [Architecture](architecture.md)) depends on. |
| `cmd/doccheck` | Validates local Markdown links and heading anchors with no external runtime dependency; `make docs-check` and CI use it. |
| `cmd/gentoken` | Mints a JWT for scripts/chaos/smoke tests without going through a real login — a test-harness concern, never used in a request path. |
| `cmd/sanctions-loader` | Imports an offline sanctions-list export into Fraud's database. No network access by design, so CI stays deterministic and reproducible. |

## Cross-cutting: what's genuinely shared

- `internal/config` — env loading, imported by every service.
- `pkg/*` — infrastructure with zero business logic (database, cache,
  messaging, middleware, logger, scheduler, tlsx, grpcx). `pkg/` must
  never import `internal/`; dependencies only flow one way.
- `internal/vendorgw` — shared by Payin and Payout only (the vendor
  adapter interface, circuit breaker, and `mockvendor` test double);
  nothing else touches it.
- `internal/testutil` — shared integration-test bootstrap.

See [Project guide](../development/project-guide.md#service-boundaries) for the
enforced rule behind all of this: a service must never write another
service's tables, and cross-service communication only ever happens
through a published HTTP, gRPC, or event contract — never a direct query.

For how these eight services are actually built, verified, run locally,
and observed — Docker Compose, the Makefile, every script under
`scripts/`, CI, and the observability stack — see
[Operations](../operations/README.md). For what every `pkg/*` package listed
above actually does and who calls it, see [Shared packages](shared-packages.md).
