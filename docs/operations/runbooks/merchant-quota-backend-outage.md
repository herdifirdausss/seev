# Runbook: Merchant Quota Backend (Redis) Outage

> [Documentation home](../../README.md) · [Operations](../README.md) · [Runbooks](README.md)

> **Status: Current. Audience: operators.**

**Symptom:** merchant API requests start failing with `503` and a
`Retry-After` header, or (for read-only requests) succeed but with a
degraded-mode response header, while Redis is unreachable or the shared
Redis instance is under load.

## Understand the behavior before you act

`services/gateway/internal/merchant/quota.Enforcer.Check` (T4) has an explicit,
deliberately asymmetric fail-open/fail-closed policy — this is not a bug,
it is the documented design:

- **Write requests fail CLOSED** when Redis is unreachable: `503` with
  `Retry-After`. `MERCHANT_QUOTA_FAIL_OPEN` defaults to `false` — an
  operator must explicitly opt into fail-open behavior for writes; the
  default protects against unbounded write volume with no rate limit
  backing it.
- **Read requests degrade to ALLOW** when Redis is unreachable: the
  response still succeeds, but with `Degraded: true` internally (surfaced
  as a response header) — reads have no side effect risk from being
  temporarily unmetered, so failing them closed would only add
  unnecessary downtime.

Confirm you're looking at the actual outage (not a single tenant hitting
its own configured burst limit — that also returns `429`, not `503`;
`503` specifically means the Redis backend itself is unreachable, `429`
means the enforced limit was legitimately exceeded).

## Diagnose

1. Check Redis health directly:
   ```bash
   docker compose exec redis redis-cli ping
   ```
2. Check gateway-service logs for the specific error
   `cache.NewFailoverLimiter` logs on Redis connection failure.
3. Check whether this is affecting every merchant request or only a
   subset — a partial Redis cluster failure (if Redis is clustered in
   your deployment) behaves differently from a total outage.

## During the outage

4. Writes will keep failing `503` for the duration — this is expected and
   safe. Do not manually flip `MERCHANT_QUOTA_FAIL_OPEN=true` mid-incident
   unless you have explicitly decided the business impact of unbounded
   write volume is less bad than the outage itself; that decision needs
   the same authority as any other fail-safe override in this system.
5. Merchants will see `503`/`Retry-After` — this is a normal, documented
   response their integration should already retry against (the same
   contract every other rate-limited API in this codebase uses).

## Recovery

6. Once Redis is confirmed healthy again
   (`cache.NewFailoverLimiter`'s own automatic recovery: two consecutive
   healthy background probes), writes resume automatically — there is no
   manual "re-enable" step, no cache to warm, no state to reconcile. The
   failover limiter's recovery is entirely automatic by design.
7. Confirm recovery by watching for `503` responses to stop in gateway's
   own access logs, or via a manual test write request against a known
   sandbox tenant.
8. No quota state is lost during the outage — Redis-backed rate limit
   counters are naturally reset by the outage itself (an empty bucket on
   reconnect behaves the same as if the tenant had simply not made any
   requests during that window); there is nothing to backfill or repair.
