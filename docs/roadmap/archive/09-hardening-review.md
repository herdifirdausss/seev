# 09 — Hardening Review: Findings and Design Decisions

> Comprehensive review of the end-to-end MVP ledger (2026-07-11), covering
> **limited resources, efficiency, no-money-loss behavior under failure/chaos,
> dependency failures, security, locking/bottlenecks, and cost**. This is a
> **reference document**; implementation tasks live in [10](10-phase2a-security-gating.md),
> [11](11-phase2b-efficiency-locking.md), and [12](12-phase2c-resilience-ops.md).
> Every `file:line` reference was valid when this document was written; if the
> code has moved, search for the named function or construct.

## Locked design decisions

| # | Decision | Rationale |
|---|---|---|
| K1 | **Move system transaction types to a separate internal router** (`/internal/v1/ledger`, a second listener bound to `127.0.0.1` or the internal network), rather than relying on role gating in the public router. | Network-level isolation ensures that endpoints capable of minting or moving money from system accounts are never publicly exposed, even when an authentication-layer bug exists. |
| K2 | **Target one small node that is ready for multiple replicas.** Redis is optional; rate limiting and scheduler locks fall back to memory when Redis is not configured, and use Redis automatically when it is available. | Minimize current cost without locking the architecture. All mechanisms remain correct when Redis is enabled for multiple replicas. |
| K3 | **Calculate fees server-side** from fee policy and reject client-supplied `fee_amount` in the public router. | A client-controlled fee is a business-logic vulnerability because it permits a zero fee. |
| K4 | **Amounts must be integral minor units** according to the currency exponent (IDR=0, USD=2). Reject fractions in both transport and validators. | `IntPart()` silently truncates, which can create or destroy money. |
| K5 | **Use the JWT user ID as the idempotency scope**, set server-side by transport. | A global key shared across users enables denial-of-service and cross-tenant probing. |
| K6 | **Do not lock system accounts with `FOR UPDATE`.** Apply their balances as an atomic delta (`UPDATE ... SET balance = balance + $d ... RETURNING balance`) near the end of the transaction; keep user accounts under `FOR UPDATE`. | Removes hot-row serialization without weakening the user overdraft check. See C13. |
| K7 | **Keep the core architecture unchanged:** synchronous double-entry posting and the transactional outbox remain. Only the locking pipeline (K6) and API-surface separation (K1) are redesigned. | The review found the core design sound; see section E. |

---

## A. Critical — money-loss and fraud risks

### A1. Ordinary users can call system transaction types

`internal/ledger/transport/http.go:19-29` (`adminOnlyTypes`) gates only seven
types (`adjustment_*`, `freeze_*`, `reversal`, and `chargeback`). Ordinary JWT
users can call the remaining types, including:

- `money_in` — a user can credit their own balance from
  `settlement[gateway]` without making a real deposit (`money_in.go:47`).
- `refund` — `merchant.settle` → `user.cash`.
- `withdraw_settle`, `withdraw_pending_settle`, `withdraw_cancel`,
  `withdraw_pending`, and `withdraw_pending_cancel` — lifecycle operations that
  should be triggered by a payment gateway or operations.
- `escrow_release`, `escrow_refund`, and `fee_collect`.

**Fix (K1):** create a separate internal router. Public types remain
`transfer_p2p`, `transfer_pocket`, `withdraw_initiate`, and `escrow_hold`.
See [10 T1](10-phase2a-security-gating.md).

### A2. The client controls `gateway`, `fee_amount`, and `fee_gateway`

`metadata` is passed unchanged from the request body to the processor
(`transport/http.go:106`) and read by `processors.go:182` (`fee_amount`),
`:196` (`fee_gateway`), and `:210` (`requireGateway`). A user can set their own
fee, including zero, and choose which settlement or fee account is affected.

**Fix (K3):** allowlist client metadata (descriptive keys such as `note` and
`external_ref` only), validate `gateway` against configuration, and calculate
fees server-side through fee policy. See [10 T3](10-phase2a-security-gating.md).

### A3. Fractional amounts are accepted and silently truncated

- Transport: `decimalFromString` uses `decimal.NewFromString`
  (`transport/dto.go:108-111`), so `"100.75"` passes.
- Validator: `PositiveAmountValidator` (`processors/validators.go:44-52`) only
  checks `> 0`.
- Repository: `newBalances[id].IntPart()` silently truncates the fraction,
  causing the stored debit/credit totals to differ from the validated totals.

**Fix (K4):** reject non-integral amounts, including amounts that are not
integral for the currency exponent, in both transport and validators. See
[10 T4](10-phase2a-security-gating.md).

### A4. No amount cap exists

`MaxAmountValidator` exists (`validators.go:66-74`) but is not wired to any
processor. Comments in `processors.go:162,297` and `transfer_p2p.go:22` defer it
to an API/policy layer that does not yet exist. See [10 T5](10-phase2a-security-gating.md).

### A5. Idempotency keys are global across users

Transport never fills `IdempotencyScope` (`http.go:101` sets only the key), even
though the unique index is
`uq_ltx_idempotency (idempotency_key, COALESCE(idempotency_scope,''))`.
User A can guess or occupy user B's key, causing B's transaction to fail with
`ErrStillProcessing`/`ErrPreviousFailed` or allowing status probing.

**Fix (K5):** transport sets `IdempotencyScope` from the JWT user ID. The
internal router uses the calling service's scope name. See [10 T2](10-phase2a-security-gating.md).

## B. High priority — resilience and chaos

### B1. No PostgreSQL-level timeouts

`DSN()` (`internal/config/config.go:305-310`) has no
`statement_timeout`, `lock_timeout`, or `idle_in_transaction_session_timeout`,
and worker queries have no context deadline. One hanging transaction can hold a
row lock, fill the 25-connection pool, and take down the entire API rather than
just the affected path. See [11 T5](11-phase2b-efficiency-locking.md).

### B2. Rate limiting fails open without a Redis fallback

`WithRateLimit` (`pkg/middleware/rate_limit.go:12-39`) forwards the request when
the limiter returns an error. The in-memory fallback exists only in commented
code (`:54-109`). When Redis is down, there is no rate limit. See [12 T1](12-phase2c-resilience-ops.md).

### B3. The verifier only logs and emits metrics

`worker/verifier.go` logs an unbalanced transaction or inconsistent projection
with `logger.Error` and a Prometheus counter, then stops. There is no alert
path (webhook or pager) and no runbook. A ledger discrepancy is a P1 incident
and must wake an operator. See [12 T4](12-phase2c-resilience-ops.md).

### B4. No replay tooling for dead outbox events

The only way to revive a `dead` event is manual SQL. Add a replay endpoint to
the internal router. See [12 T3](12-phase2c-resilience-ops.md).

### B5. The reaper increments `retry_count`

`ReapStuck` (`repository/outbox_event_repository.go:195-211`) sets
`status='failed'` and increments `retry_count`. A one-hour broker outage can
claim an event, fail to publish (+1), reap it (+1), and reach
`max_retries=5`—dead without five actual publish attempts. There is also no
backoff column; retries follow only the global 30-second tick. See [12 T2](12-phase2c-resilience-ops.md).

### B6. OTel is instrumented but no provider is installed

`service/handle` and `pkg/messaging` create spans through `otel.Tracer(...)`,
but there is no `SetTracerProvider` or exporter anywhere. All spans are no-ops:
small overhead with no benefit, and misleading to readers. See [12 T5](12-phase2c-resilience-ops.md).

### B7. `uuid.MustParse` is used on a scan path

`ledger_transaction_repository.go:284-319` (`GetByID`) panics on invalid data.
Use `uuid.Parse` and return an error instead. See [12 T6](12-phase2c-resilience-ops.md).

## C. Efficiency and bottlenecks (limited resources)

### C13. Hot row: system accounts are included in `SELECT ... FOR UPDATE`

`LockBalances` (`repository/account_balance_repository.go:75`) locks every
account involved, including `settlement[gateway]` and `fee[gateway]`. Every
`money_in` through the same gateway is serialized across validation, entry
construction, insertion, and update. On a small host this caps throughput—the
classic hot-account problem described in the TigerBeetle references below.

**Fix (K6):** separate the account classes:

- **User accounts** (`allow_negative=false`) remain under `FOR UPDATE`; a
  pre-read is needed for a consistent overdraft check.
- **System accounts** (`allow_negative=true`) are not locked or pre-read. Apply
  their balance in the update step with
  `UPDATE account_balances SET balance = balance + $delta WHERE account_id = $1 RETURNING balance`.
  No floor check is required; read `balance_after` for entries from `RETURNING`.
  The row is locked only for that statement, not for the whole validation
  transaction.

See [11 T1](11-phase2b-efficiency-locking.md). **Verify this with a real Docker
integration test, not sqlmock**; see the verified lessons below.

### C14. `InsertEntries` performs one round trip per entry

`repository/ledger_entry_repository.go:50-70` calls `ExecContext` for each
entry while holding locks. The outbox repository already has a multi-row batch
pattern in `InsertEvents` (`outbox_event_repository.go:89-119`); follow it.
See [11 T2](11-phase2b-efficiency-locking.md).

### C15. `ResolveAccounts` uses three or four pool queries per posting

`money_in.go` and `money_out.go` call `GetAccountID`, `GetSystemAccountID`, and
`GetAccountCurrency` before the transaction. System-account IDs are immutable
after seeding and can be cached for the process lifetime. Cache
`(userID,type) → (accountID,currency)` with a TTL and invalidate it during
provisioning. See [11 T3](11-phase2b-efficiency-locking.md).

### C16. UUIDv4 on insert-heavy tables

Random primary keys spread across the B-tree, causing page splits, cache misses,
and larger WAL. `google/uuid` ≥1.6 provides time-ordered `uuid.NewV7()` (RFC
9562). Use it for IDs in `ledger_transactions`, `ledger_entries`, and
`outbox_events`. See [11 T4](11-phase2b-efficiency-locking.md).

### C17. Redis is required for minimal usage

Redis is used only for rate limiting and scheduler locks; both can use memory
on a single node (`MemoryLock` already exists in `pkg/scheduler`). Make Redis
optional under K2. See [12 T1](12-phase2c-resilience-ops.md).

### C18. The 25/25 pool default is too large for a small host

See `config.go:145-148`. PostgreSQL on the same machine competes for memory and
CPU. Lower the default `MaxOpenConns` to 10 while retaining the environment
override, and document connection sizing. See [11 T5](11-phase2b-efficiency-locking.md).

### C19. The gauge loop runs two separate queries every 15 seconds

`worker/outbox_relay.go:184-208` calls `CountByStatus` twice (pending and dead).
Combine them into one `GROUP BY status` query. This is minor. See [11 T6](11-phase2b-efficiency-locking.md).

## D. Other security findings

| # | Finding | Location | Fix |
|---|---|---|---|
| D1 | JWT lacks `nbf`/`iss`/`aud`; `JWTConfig.Issuer` is unused. HMAC verification is otherwise correct and resistant to algorithm confusion: the header algorithm is ignored, HS256 is enforced server-side, and `hmac.Equal` is constant-time. | `pkg/middleware/auth.go:55-90` | [10 T6](10-phase2a-security-gating.md) |
| D2 | `/metrics` has no authentication and is outside the middleware chain. | `internal/handler/router.go:31-37` | [10 T6](10-phase2a-security-gating.md), bind it to the internal listener (K1) |
| D3 | HSTS is enabled only when `r.TLS != nil`, so it is never sent behind a reverse proxy. | `pkg/middleware/security.go:35` | [10 T6](10-phase2a-security-gating.md), add trusted-proxy configuration |
| D4 | The request body has a total 1 MiB limit, but metadata has no independent key/size limit. | `pkg/response/response.go:110` | [10 T3](10-phase2a-security-gating.md), allowlist and size limit |
| D5 | RLS is still deferred from D11. | `migrations/000001:244-245` | Keep it in [07 Task H8](07-phase-2-hardening.md); do not lose the item |

## E. Correct behavior to preserve

This is not new work; it exists to keep implementers from breaking these
contracts:

1. **The idempotency gate handles ambiguous commits.** A retry after an unclear
   commit hits a duplicate key, `handleDuplicate`, and `ErrAlreadyPosted`, then
   succeeds idempotently (`service/handle/service.go`). This is the same pattern
   published by Stripe.
2. **The outbox pattern is complete:** write the event in the posting
   transaction, claim with `FOR UPDATE SKIP LOCKED`, wait for broker confirms
   (`pkg/messaging/publisher.go:163-195`), and reap stuck rows. Delivery is
   **at least once**, so consumers MUST deduplicate by event ID
   (`outbox_events.id` = AMQP `message_id`). Add this contract to event
   documentation ([07 Task H1](07-phase-2-hardening.md)).
3. **Database guards:** `trg_entries_immutable` (append-only),
   `chk_balance_floor` plus `allow_negative`, unique idempotency, and the
   dead-letter trigger.
4. **Deterministic lock ordering** uses `ORDER BY account_id` in SQL
   `LockBalances`, not a Go slice sort. `AccountIDs` are positional; do not
   sort them. This bug has occurred and was fixed.
5. **Graceful shutdown order** is correct: drain HTTP → stop workers → close
   MQ → Redis → PostgreSQL (`cmd/server/main.go:78-95`).

## Verified lessons (2026-07-11)

Three real bugs passed all sqlmock unit tests and were found only by an
integration test against PostgreSQL:

1. `FOR UPDATE OF <table name>` requires an alias when the `FROM` clause uses
   an alias.
2. Placeholders inside a multi-branch `CASE` need an explicit `::bigint` cast
   for every placeholder.
3. Sorting `AccountIDs` changes debit/credit direction depending on UUID byte
   order.

**Consequence:** every task in 10–12 that touches raw SQL, ordering, or locking
MUST be verified with `go test -tags=integration -race ./...` in Docker and a
manual HTTP smoke test before it is considered complete. Green unit tests alone
are not enough.

## References

- PostgreSQL docs — [Explicit Locking](https://www.postgresql.org/docs/current/explicit-locking.html),
  [client connection defaults](https://www.postgresql.org/docs/current/runtime-config-client.html)
- Brandur Leach (Stripe) — [Implementing Stripe-like Idempotency Keys in Postgres](https://brandur.org/idempotency-keys)
- TigerBeetle — [design documentation](https://docs.tigerbeetle.com/) on hot accounts and contention
- AWS Builders' Library — [Timeouts, retries, and backoff with jitter](https://aws.amazon.com/builders-library/timeouts-retries-and-backoff-with-jitter/)
- RFC 9562 — UUIDv7 and time-ordered index locality
- Chris Richardson — [Transactional Outbox pattern](https://microservices.io/patterns/data/transactional-outbox.html)
