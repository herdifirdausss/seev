# 21 — Service Topology Review: Long-Term Monolith-to-Services Plan

Date: 2026-07-12, after plans 03–20 were completed and verified.

This is a long-term architecture reference. It locks the decisions needed by [plan 22](22-phase4a-payin-vendorgw.md), [plan 23](23-phase4b-payout-orchestration.md), and the [extraction playbook](24-extraction-playbook.md), so those documents do not reopen the same architecture debate.

## Current state (2026-07-12 audit)

The following facts were checked against the codebase:

| Area | Current state |
|---|---|
| Runtime and database | One `cmd/server` binary, one PostgreSQL schema, one migration timeline (000001–000018), publish-only RabbitMQ outbox, and optional Redis. |
| Modules | Mature `internal/ledger`; `internal/policy`; and `internal/handler` as the composition root. Auth, user, and admin routes are still placeholders. |
| Listeners | Public `:8080` with an allowlisted transaction surface, rate limiting, CORS, and policy checks; internal `127.0.0.1:8081` with admin tooling, metrics, and no public rate limit. |
| Vendor integration | No real vendor calls or webhook receiver yet. `money_in` and `money_out` can currently be posted only by an internal trusted caller. Recon is CSV-based, and disbursement posts to the ledger without contacting a bank. |
| Boundaries | Module boundaries are clean. Consumers may import `internal/ledger/events`; policy and ledger meet only through a structural interface. |

Payin, payout, vendor adapters, and fraud are not separate services yet. That is intentional: they can be introduced as service-grade modules before a deployment split is justified.

## Target topology: seven future services mapped to monolith modules

| Future service | Exposure | Monolith module today | Data ownership | Split boundary |
|---|---|---|---|---|
| Ledger | Internal only | `internal/ledger` | Existing unprefixed ledger tables | The internal `:8081` router already provides the future service API. |
| Payin | Internal core; thin public webhook edge | `internal/payin` | `payin_*` | Replace in-process `ledger.Post` with the ledger HTTP client and move the webhook edge with payin. |
| Payout | Internal only | `internal/payout` | `payout_*` | Move the state machine and vendor orchestration together. |
| Vendor adapters | Library, not a service by default | `internal/vendorgw` | Stateless | Keep with payin/payout unless operational isolation requires a sidecar. |
| Fraud | Internal only | `internal/ledger/screening` | `screening_events` until extraction | Replace the synchronous hook with a fail-open HTTP client and move fraud data when the service is extracted. |
| Internal admin | Internal only | No new module | None | Build a BFF that calls each service's frozen internal API. |
| User-facing | Public | Public router plus future `internal/auth` | `auth_*` later | Move the public surface and auth module together. |

The intended flow after plans 22 and 23 are complete is:

```text
Internet
  ├── public :8080 ── user-facing routes ──┐
  └── /webhooks/{vendor} ── payin edge ────┤
                                           ▼
                                  internal/ledger
                                  (the only money owner)
                                           ▲
                 withdraw hold/settle/cancel│
                                           │
                    internal/payout ── vendor adapter ── vendor API

127.0.0.1:8081: internal admin and operational API
```

The guiding principle is: “service or module” is a deployment decision, not a code decision. A cleanly bounded module can run in one binary today and in an independent service later without rewriting its business logic.

## Locked design decisions

### K-T1 — Webhooks use the public listener

Mount `POST /webhooks/{vendor}` on the public listener, outside JWT, CORS, and JSON middleware. Authenticate with the vendor-specific signature over the raw request body, cap the body at approximately 64 KiB, and rate-limit by vendor. The payin core APIs remain internal; only the unavoidable webhook edge is internet-reachable. A third dedicated webhook listener is not justified on a small single-box deployment.

### K-T2 — Payin processes inline

The flow is signature verification → durable dedup record → `ledger.Post(money_in)` → HTTP 200. Vendor retry-with-backoff is the queue. Infrastructure errors return 5xx so the vendor redelivers; duplicate delivery is safe through both the payin unique key and ledger idempotency. Permanent failures are handled through an admin replay endpoint. No additional worker or queue is needed for the initial volume.

### K-T3 — Ledger disbursement is not payout

Ledger disbursement is a bulk-posting primitive. Payout is vendor orchestration with a state machine such as `created → held → submitted → vendor_pending → settled|failed|cancelled`. Keep them separate so payout does not import ledger internals and can later offer its own batch behavior.

### K-T4 — Keep fraud screening inside ledger for now

The synchronous seam is `processors.PrePostHook`, already fail-open. The asynchronous seam is the versioned `ledger.transaction.posted.v1` event. Do not move screening now; move the implementation and `screening_events` only when a real extraction trigger exists.

### K-T5 — Enforce boundaries in CI

`boundary_test.go` must enforce that:

- packages outside a module cannot import its subpackages, except the published `events` package;
- `pkg/*` does not import `internal/*`;
- payin and payout do not import each other;
- new tables use module prefixes (`payin_*`, `payout_*`, `auth_*`), while legacy ledger tables remain unprefixed.

The composition root (`cmd/`) and test files are explicitly exempt where construction or integration tests require it. Existing `pkg/*` imports of `internal/config` are grandfathered as a single tracked exception and must be removed before the first service extraction.

Per-service database roles are deferred until extraction; separate roles inside one process would add grants without meaningful isolation.

### K-T6 — Generic vendor contracts, mock first

`PayinVerifier` normalizes raw vendor webhooks into a `PayinEvent`. `PayoutProvider` uses the payout request ID as its idempotency key, explicit timeouts, bounded retries, and a normalized `PayoutResult`. A config-selected registry lets a real vendor be added as one adapter package plus one composition-root entry. `mockvendor` comes first because no real vendor account exists yet.

### K-T7 — Freeze the internal admin contract

The internal `:8081` listener is the API that a future admin BFF will call. Do not create an `internal/admin` module now. Inventory and freeze the routes in [plan 24](24-extraction-playbook.md); route changes become API changes rather than casual refactors.

## Extraction triggers

Extract a module only when at least one of these is supported by evidence:

1. It needs a materially different deployment cadence.
2. Its incidents have a demonstrated blast radius against ledger posting.
3. Its load scales independently, such as webhook I/O versus database-bound ledger work.
4. Multiple teams need independent ownership and release cycles.
5. Compliance requires organizational or data isolation.

Until then, a split adds network hops, deployment and on-call surfaces, and distributed tracing requirements without a demonstrated benefit.

## Anti-goals for the current stage

- No gRPC/protobuf requirement between modules; in-process facades and events are sufficient until extraction.
- No separate database or schema yet; table prefixes prepare the later carve-out.
- No split binaries, Kubernetes, service mesh, or API gateway for the small single-box deployment.
- No real vendor integration before credentials and contracts exist; use `mockvendor` behind the final interfaces.
- No new webhook queue or `internal/admin` module.
- No screening rename or move.
- No per-service database roles before extraction.
- No payment intents in the first payin iteration; settled webhooks come first.

## Execution order

1. [Plan 22](22-phase4a-payin-vendorgw.md): vendor interfaces, registry, mock vendor, payin module, webhook receiver, and admin endpoints.
2. [Plan 23](23-phase4b-payout-orchestration.md): payout state machine, vendor calls, settlement, cancellation, and recovery.
3. [Plan 24](24-extraction-playbook.md): reference only; execute it when an extraction trigger is met.

The boundary check is introduced before payin and payout so both modules are born under enforcement. All posting paths require integration tests, curl smoke tests, and chaos tests whenever the posting pipeline changes.
