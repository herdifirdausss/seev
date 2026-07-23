# 12 — Phase 2c: Resilience, Optional Redis, and Operations Tooling

> Prerequisite: read sections B and E of [09-hardening-review.md](09-hardening-review.md).
> These tasks make the system resilient to Redis, RabbitMQ, and PostgreSQL
> failures without losing money, and give operators tools for incident response.

## T1 — Optional Redis (K2)

**Problem:** `cmd/server/main.go` exits when Redis cannot connect, even though
Redis is used only for rate limiting and scheduler locks—both of which have
reasonable single-node fallbacks.

### Implementation

1. Add `RedisConfig.Enabled` and `REDIS_ENABLED`, defaulting to `true` for
   backward compatibility and multi-replica deployments. When false, Redis
   address validation is not required.
2. In `cmd/server/main.go`, construct the Redis client only when enabled. If an
   operator explicitly enables Redis and it cannot connect, fail fast with a
   configuration error. When disabled, log that rate limiting and scheduler
   locks are running in memory and are single-instance only.
3. Make the router select a Redis or memory limiter depending on whether the
   cache is nil. Add `MemoryRateLimiter` in `pkg/cache` with the same `Limiter`
   interface, a mutex-protected token bucket, and periodic cleanup of expired
   entries.
4. Keep the current rate-limiter fail-open behavior when Redis fails during
   runtime. Rate limiting protects availability and abuse boundaries; it is not
   a money-moving mechanism, so fail-open does not create money loss. Document
   that rationale in the middleware comment.
5. The scheduler already has `MemoryLock` and `RedisLock`. Ensure the module
   receives a nil Redis client safely when Redis is disabled; do not call a
   method on a nil cache without a guard.
6. Document `REDIS_ENABLED=true` in `.env.example`, including the single-node
   `false` option.

### Tests and definition of done

- [ ] Memory limiter rejects requests beyond the burst, accepts requests after
      the window resets, and is race-safe.
- [ ] A router with `deps.Cache == nil` does not panic and still rate-limits.
- [ ] The server starts with Redis completely absent when disabled, readiness
      reports Redis as `disabled`, the in-memory limiter works, and the verifier
      scheduler runs with a memory lock.
- [ ] Default `REDIS_ENABLED=true` behavior is unchanged.

## T2 — Outbox backoff without reaper retry inflation

**Problem:** `ReapStuck` increments `retry_count`, so a long broker outage can
make an event `dead` without the configured number of real publish attempts.
Retries also run on a fixed 30-second cadence with no per-event backoff.

1. Add `outbox_events.next_attempt_at TIMESTAMPTZ NULL` in
   `000003_outbox_backoff.up.sql` and its down migration. Replace
   `idx_outbox_retry` with an index on `next_attempt_at` for failed rows.
2. Make `MarkFailed` increment the new retry count and set an exponential
   backoff with jitter: base 30 seconds, factor 2, maximum 15 minutes, plus
   random jitter up to half the delay.
3. Make `ClaimFailedForRetry` require
   `next_attempt_at IS NULL OR next_attempt_at <= now()`.
4. Change `ReapStuck` so it only returns a stale processing row to `failed`,
   sets `next_attempt_at=now()`, and records a reaper message. It must not
   increment `retry_count`; only a real publish attempt may do that.

Tests must prove that `MarkFailed` schedules a future retry, `ReapStuck` leaves
the retry count unchanged, and repeated reaping during a broker outage does not
kill an event prematurely.

## T3 — Admin replay for dead outbox events

Add `ReplayDead(eventID)` and bounded `ReplayAllDead(olderThan)` repository
methods. They reset a dead row to `failed`, clear its retry count, set
`next_attempt_at=now()`, and record that an admin replayed it. Expose them only
on the internal router:

- `POST /admin/outbox/dead/{id}/replay`
- `POST /admin/outbox/dead/replay-all`

Both routes remain admin-gated even on the internal network. Test that replayed
events are claimed by the relay and published after the broker recovers.

## T4 — Verifier alert hook and runbook

1. Add optional `ALERT_WEBHOOK_URL`. Empty means backward-compatible
   log-and-metric behavior.
2. Give the verifier an injectable
   `alertFn(ctx, severity, message) error`. Call it for trial-balance and
   projection discrepancies, alongside the error log.
3. Add reusable `pkg/alerting/webhook.go` that sends one short-timeout JSON
   request (`severity`, `message`, `service`, and `timestamp`). It must never
   block or retry the verifier; log a delivery failure and continue.
4. Wire the hook in `cmd/server/main.go`.
5. Add `docs/operations/runbooks/ledger-integrity-alert.md`: inspect the verifier query and
   affected entries, never update/delete append-only `ledger_entries`, correct
   through an admin reversal, and escalate when the cause is not clear within
   15 minutes.

Test alert invocation count/arguments and webhook timeout behavior.

## T5 — Optional OTel exporter

Add optional `OTEL_EXPORTER_OTLP_ENDPOINT`. When empty, do not install a tracer
provider; existing spans remain no-op with zero deployment overhead. When set,
install an OTLP exporter, a batch `sdktrace.TracerProvider`, and the TraceContext
propagator, then shut the provider down during cleanup. Document an example
Tempo/Jaeger endpoint in `.env.example`.

Test the empty-endpoint startup path and manually verify a span reaches the
backend when the exporter is enabled.

## T6 — Small defensive fixes

1. Replace `uuid.MustParse` in `ledger_transaction_repository.go:GetByID` with
   `uuid.Parse` and return a wrapped scan error instead of panicking.
2. Remove obsolete commented-out rate-limiter fallback code.
3. Fix `RateLimitByUser` to use the shared user-ID context key, check the type
   assertion, and fall back to IP limiting when no user ID exists.
4. Make the readiness handler report Redis as `disabled` instead of unhealthy
   when `REDIS_ENABLED=false`.

## T7 — Chaos test script

The goal is empirical proof of no money loss under real failures. Add
`scripts/chaos-test.sh` or document an equivalent manual procedure with these
minimum scenarios:

1. **Kill during posting:** run parallel `money_in`/`transfer_p2p` requests,
   kill the server, restart it, retry with the same keys, and compare the final
   balances with successful requests. Verify the ledger function is empty.
2. **Broker outage:** stop RabbitMQ for two minutes while posting continues;
   postings must succeed, and events must publish after RabbitMQ returns.
3. **PostgreSQL restart:** restart PostgreSQL during traffic; affected requests
   fail clearly and later requests succeed. No stale pending transaction or
   partial write may remain.
4. **Redis outage:** stop Redis, verify posting continues with rate-limit
   fail-open, restart the process while Redis remains down, and verify the
   memory-lock fallback. Document that a running Redis lock does not switch to a
   memory lock until process restart.

Every scenario needs automated assertions through PostgreSQL queries, not only
manual observation.

### Recorded results (2026-07-11)

The four scenarios were run against real PostgreSQL, RabbitMQ, and Redis with
Docker Compose:

- 40 concurrent transfers survived a server kill and idempotent retry; all
  ledger checks were clean and no transaction remained pending.
- Ten postings during a RabbitMQ outage remained successful; every outbox event
  was published after recovery and none became dead.
- During a PostgreSQL restart, requests already accepted succeeded, requests
  that hit the outage failed quickly, and later requests succeeded; no partial
  writes or stale pending transactions remained.
- During a Redis outage, posting remained successful before and after a process
  restart using the in-memory fallback.

All four runs ended with an empty `fn_verify_ledger_balance()` result and a
consistent `v_account_balance_audit` view.

## Execution order

Run T1 first because it affects deployment and cost. Run T2 before T3 because
replay depends on `next_attempt_at`. T4–T6 are independent. Run T7 last, after
T1 and T2 establish the final failure behavior.

## Phase 2c final verification

```bash
go build ./...
make lint
make test
go test -tags=integration -race ./...
```

Run the T7 chaos scenarios manually with Docker enabled as the final phase
evidence.
