# C1 Current Contract Inventory — Plan 57 T0

> [Documentation home](../README.md) · [Reference](README.md) ·
> [Plan 57](../roadmap/archive/57-c1-merchant-b2b-api.md)

Snapshot at baseline commit `d20e5295ef0cdbbc44816af239c90c3d7514439b`
(2026-07-28). See [c1-entry-gate.md](../evidence/c1-entry-gate.md) for the
gate result this inventory supports.

## 1. Existing contract surfaces

| Contract file | Operations | Audience |
|---|---|---|
| `api/openapi/public-v1.yaml` | 32 | end-user (JWT) |
| `api/openapi/internal-v1.yaml` | 26 | service-to-service (mTLS + internal token) |
| `api/openapi/admin-v1.yaml` | 14 | operator (Admin BFF session) |
| `api/openapi/webhooks-v1.yaml` | 1 | inbound vendor callback (HMAC) |

A fifth file, `api/openapi/b2b-v1.yaml`, is the new C1 surface (Plan 57 §6.1,
delivered in T1). Every operation across all five files registers in the
single `api/contracts/surfaces.yaml` inventory and is subject to the same
`make contracts` gate (`contract-generate`/`contract-lint`/`contract-breaking`/
`contract-test`).

Existing operations C1 depends on are already registered and canonical —
no gap found that blocks T1:

- Ledger: account read/balance, transaction read, transfer post (internal
  surface, gRPC — `api/proto/ledger/v1/*.proto`, 6 `.proto` files, 547 lines
  total).
- Payin/Payout: intent create/get (internal gRPC surfaces).
- Admin BFF: session/CSRF/audit patterns (`admin-v1.yaml`).

## 2. Event contracts (RabbitMQ, `internal/ledger/events`)

| Event type | Constant |
|---|---|
| `ledger.transaction.posted.v1` | `events.TypeTransactionPosted` |
| `ledger.transaction.reversed.v1` | `events.TypeTransactionReversed` |
| `ledger.adjustment.decided.v1` | `events.TypeAdjustmentDecided` |

Every event already carries a **logical, deterministic `EventID`**
(`uuid.NewSHA1(eventType+identity)`, see `internal/ledger/events/events.go`
lines 10-11, 241-244) — this is the exact identity C1's T7 webhook relay
must dedupe on (Plan 57 §15, "deduplicate using logical event ID"), not the
outbox row's own primary key. (This distinction was the root cause of a real
regression fixed earlier this session in
`internal/notify/notify_integration_test.go` — any new C1 webhook-relay code
or test must use the same `EventID` semantics, not `outbox_events.id`.)

Plan 57 §4.3's five external event families (`transaction.posted.v1`,
`transaction.reversed.v1`, `payin.updated.v1`, `payout.updated.v1`,
`webhook.endpoint.disabled.v1`) map onto the first two existing internal
events directly; `payin.updated.v1`/`payout.updated.v1` require new
owner-neutral event fields (T6), and `webhook.endpoint.disabled.v1` is a
Gateway-internal event with no existing analog (new in T7).

## 3. Migration heads (no service needs a heads-alignment migration)

| Service | Head |
|---|---|
| ledger | `000032_retention_scheduled_transactions` |
| auth | `000017_closure_finalize_grant` |
| payin | `000013_normalized_callbacks` |
| payout | `000013_retention_commands` |
| fraud | `000007_retention_screening_events` |
| gateway | `000003_retention_purge_functions` |
| adminbff | `000007_retention_purge_audit_log` |
| assurance | `000007_retention_remaining` |
| vendor | `000001_vendor_boundary` |

Per Plan 57 §3.1 ("No new service extraction... Gateway-owned persistence
only"), C1's new tables land in `migrations/gateway/000004_...` onward. No
other service's migration head needs a schema change for T2; Ledger/Payin/
Payout changes in T5/T6 are additive Go-level contract/field changes against
their *existing* tables, not new migrations (with one exception — see §4).

## 4. User-specific fields that block a merchant principal

These are the concrete places where existing code assumes a human JWT
principal and must be extended (not replaced) for a merchant/API-key
principal, found by direct inspection (not assumption):

1. **`pkg/middleware/auth.go`'s `Claims` struct** (`UserID`, `Email`, `Role`,
   `KYCLevel`) is the ONLY current HTTP principal type, produced solely by
   `WithAuth` (JWT-only). There is no generic "actor" abstraction — every
   handler that needs the caller's identity type-asserts through `Claims`
   directly via context. **C1 needs a parallel, distinct middleware**
   (`WithMerchantAuth` or similar, per Plan 57 §3.2) rather than extending
   `Claims`, exactly as the plan already locks: "an API key is not an Auth
   user."
2. **`internal/ledger` account ownership is already schema-ready but
   code-ready only for `owner_type='user'`.** The `accounts.owner_type`
   CHECK constraint in the very first migration
   (`migrations/ledger/000001_ledger_core.up.sql:14-15`) already allows
   `'merchant'` alongside `'user'`/`'system'`/`'partner'`/`'escrow'` — this
   was anticipated at schema design time, genuinely surprising to find,
   and means **T5 needs no new Ledger migration for account ownership**.
   However, `internal/ledger/repository/account_repository.go`'s query
   methods (`GetByOwner` and friends, lines 86/109/166) and
   `internal/ledger/service/closure/closure.go`'s multiple `owner_type =
   'user'` predicates all hardcode the literal `'user'` string. T5 must
   extend these call sites to accept an owner-type parameter (or add
   parallel merchant-scoped methods) rather than assume the schema needs
   migrating.
3. **`RateLimitByUser` (`pkg/middleware/rate_limit.go:82`)** keys off the
   JWT `Claims.UserID` — T4's quota enforcement needs an equivalent
   `RateLimitByMerchant`/tenant-keyed function, following the same pattern
   as the existing `RateLimitByIP`/`RateLimitByVendor` siblings.
4. **Payin/Payout intent ownership** is keyed by the human `user_id` at
   creation time in their respective repositories — T6 explicitly plans
   "owner-neutral principal fields" and "backfill existing rows safely,"
   consistent with what's found here (no owner-type abstraction currently
   exists in either service, unlike Ledger's schema which already
   anticipated it).

## 5. Reusable helpers confirmed (avoid rebuilding any of these in C1)

| Concern | Existing helper | Reuse plan |
|---|---|---|
| Request ID | `pkg/middleware.WithRequestID` | Reuse as-is; B2B requests get the same `X-Request-ID` propagation |
| Internal token auth | `pkg/middleware.WithInternalToken` (`internal_token.go`) | Reference pattern (constant-time compare, fail-closed on empty token) for API-key digest comparison in T3 — not directly reusable (different key shape) but same security posture |
| Rate limiting / quota | `pkg/cache.Limiter` interface, `RedisRateLimiter`, `MemoryRateLimiter`, `FailoverLimiter` (`pkg/cache/rate_limiter.go`, `failover.go`) | Directly reusable for T4's quota enforcement; **note Plan 57 §T4 requires write fail-closed on Redis outage** — the existing `FailoverLimiter`'s hot-swap-to-memory behavior (built for A3) is the *wrong* default for merchant financial writes and must NOT be used for write-path quota (mirrors A8 T3's `FailClosedVelocityStore` precedent for fraud velocity, same K4 rule: no memory fallback for financial controls) |
| Envelope encryption | `pkg/cryptox.Ring`/`DigestRing`/`LookupKey` (versioned AEAD envelope, AAD, KEK ring, HMAC lookup digest) | Directly reusable for T3 (API key secret-at-rest, if any secret component needs storage) and T7 (webhook endpoint secret encryption) — same pattern as A8 T2's auth email/KYC and payin/payout field encryption |
| Durable outbox / async relay | `pkg/objectoutbox.Worker` (claim/lease/process/retry, `ProcessOnce`/`Start`), plus `internal/ledger/worker/outbox_relay.go`'s sibling pattern | Directly reusable shape for T7's webhook relay (claim → attempt → dead-letter), and for T2/T4's retention jobs (`pkg/retentionworker.Runner`, already used by 8 of 9 services) |
| Admin audit trail | `internal/adminbff/audit.go` (`AuditEntry`, `WriteAudit`, `ListAudit`, `Module.AuditMutation`) | Directly reusable for T8's "every mutation emits a redacted audit event" requirement — same call pattern as every other Admin BFF mutation this session (closure, offboarding, retention) |
| Retention scheduling | `pkg/retentionworker.Runner` + `pkg/scheduler` | Directly reusable for T2's idempotency/delivery-evidence retention jobs and T9's stuck-lease detection |

## 6. Dependency and blast-radius table

| C1 task | Touches | Existing invariant at risk | Mitigation already proven in this codebase |
|---|---|---|---|
| T2 (Gateway module) | Gateway DB only (new tables) | None — additive, Gateway-owned | Gateway already has 3 migrations of precedent for additive-only changes |
| T3 (API-key auth) | New Gateway tables + middleware | Must not weaken `WithAuth`/JWT path | Parallel middleware, not modification (§3.2 lock) |
| T4 (quota/idempotency) | Gateway DB + Redis | Fail-closed vs fail-open policy divergence (see §5 note above) | Reuse `Limiter` interface, do NOT reuse `FailoverLimiter`'s memory fallback for writes |
| T5 (Ledger) | `account_repository.go`, `closure.go` (owner_type literals) | Existing user transfer/closure paths must remain byte-identical | Schema already supports `owner_type='merchant'`; extend queries, do not alter existing `'user'`-scoped call sites — additive new methods preferred over parameterizing existing ones, to keep blast radius on existing user paths at zero |
| T6 (Payin/Payout) | Owner-neutral fields, existing tables | Existing webhook/callback dedup and vendor-adapter contracts | VendorService boundary (Plan 54) already isolates vendor-native payloads from any Gateway/B2B surface — no new coupling introduced |
| T7 (webhook relay) | New Gateway tables, RabbitMQ consumer | Must use logical `EventID`, not `outbox_events.id` (see §2) | Directly informed by this session's own notify-test regression finding |
| T8 (Admin BFF) | New routes/policy entries | Existing Admin BFF routes/CSRF/maker-checker | Additive routes + existing `AuditMutation`/CSRF middleware, same pattern as A8 T5b's operator-offboarding addition |
| T9/T10 | Observability + scripts | Cardinality explosion if tenant ID becomes a Prometheus label | Plan 57 §31 already locks "avoid tenant-level metric cardinality" — must audit every new metric before merge |

## 7. Conclusion

No prerequisite gap blocks starting T1. All entry-gate checks pass (see
[c1-entry-gate.md](../evidence/c1-entry-gate.md)). The most consequential
finding is that Ledger's schema already anticipated merchant accounts
(`owner_type='merchant'` since migration 000001) while its repository code
did not — T5 should treat this as "extend existing queries," not "design a
new ownership model."
