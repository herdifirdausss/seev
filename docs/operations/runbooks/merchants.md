# Merchant Runbook

> [Operations](../README.md) · [Runbooks](README.md)

Covers the Gateway merchant/B2B surface: API key lifecycle, tenant suspension,
idempotency, quota backend, webhook delivery, and webhook secret management.

---

## API key compromise

**Symptom:** Merchant reports a leaked API key, or anomalous traffic is observed
from one `merchant_api_keys` row (unexpected volume, scopes, or source environment).

**Authorization:** Follow this procedure only in an environment where you are
authorized to touch merchant credentials.

### Immediate containment

Identify the key's `public_prefix` from the merchant's report or from
`X-Request-Id`-correlated access logs (the plaintext key is never logged, only
the prefix). Revoke immediately via Admin BFF (`/api/v1/admin/merchant`, "API keys")
or directly:

```http
POST /api/v1/admin/gateway/tenants/{tenant_id}/keys/{key_id}/revoke
```

Requires the maker role. Revocation takes effect immediately — `RequireMerchantAuth`'s
`GetActiveByPrefix` only matches `status = 'active'`, so a revoked key fails the very
next request with `401`. Confirm via the audit log or directly:

```sql
SELECT id, public_id, status, revoked_at FROM merchant_api_keys WHERE id = '<key_id>';
```

### Issue a replacement

Create a new key with the same (or narrower) scopes:

```http
POST /api/v1/admin/gateway/tenants/{tenant_id}/keys
```

The response's `plaintext` field is shown **exactly once** — hand it to the merchant
through a secure out-of-band channel. It can never be re-fetched.

If `MERCHANT_API_KEY_PEPPER` itself may be compromised (every key across every
tenant uses the same pepper), stop and escalate — that requires rotating the pepper
and re-issuing every key on the platform, not a single-key revocation.

### Follow-up

Review `merchant_api_keys_scopes` for over-broad grants; tighten on the replacement.
If fraudulent transactions were posted before revocation, escalate to the
[Ledger integrity runbook](ledger-integrity-alert.md) — key revocation stops NEW
requests; it does not reverse anything already posted. Consider tenant suspension
([below](#tenant-suspension)) while the merchant rotates their integration.

---

## Tenant suspension

**When to use:** A single merchant tenant needs to stop transacting immediately
(suspected fraud, compliance hold, contractual dispute, or merchant request) without
affecting any other tenant.

**Authorization:** Follow this procedure only in an environment where you are
authorized to act on merchant accounts.

This is narrower than the global kill switch (`services/gateway/internal/merchant/auth.GlobalFlag`,
`PUT /api/v1/admin/gateway/global/b2b-api`, checker-gated) — suspension acts on ONE
tenant; the kill switch acts on the entire B2B surface for every tenant at once.

### Suspend

```http
POST /api/v1/admin/gateway/tenants/{tenant_id}/suspend
```

Maker role only — no checker approval required (reversible, immediately protective).
Takes effect immediately and structurally: `RequireMerchantAuth` checks `tenant.Status`
on every request before any other processing. A suspended tenant receives `403 tenant
suspended` from their very next request with no propagation delay.

Confirm:
```sql
SELECT status, suspended_by, suspended_at FROM merchant_tenants WHERE id = '<tenant_id>';
```

### What suspension does NOT do

- Existing accepted writes remain fully queryable (suspension gates NEW inbound
  requests only, not past history).
- In-flight webhook deliveries already queued continue to be attempted; disable
  specific endpoints separately if needed
  (`POST .../webhooks/{id}/disable`).
- API keys remain `active` in `merchant_api_keys` — they just can't authenticate
  while the tenant is suspended. Revoke separately if the incident calls for key
  invalidation ([API key compromise](#api-key-compromise)).

### Reactivate

Reactivation uses the same maker-checker lifecycle flow as a live activation
(`POST .../lifecycle/propose` with `action: "activate"`, then a DIFFERENT identity
approving via `POST /api/v1/admin/gateway/lifecycle/{id}/approve`). Confirm the
merchant can authenticate again before declaring the incident resolved.

---

## Idempotency stuck

**Alert:** `SeevMerchantIdempotencyStuckLeases` — `seev_merchant_idempotency_stuck_leases > 0`
for 15+ minutes.

**Symptom:** Merchant reports a request that appears never to have completed; retries
with the same `Idempotency-Key` keep returning `IDEMPOTENCY_IN_PROGRESS`.

### Mechanism

`services/gateway/internal/merchant/idempotency` (T4) claims a row in `'processing'`
state under a lease. Most stuck records self-heal the moment the merchant retries:
`IdempotencyRepository.TakeoverExpiredLease` atomically reassigns the lease and lets
the new request retry the operation. The alert fires when nobody has retried yet, not
because the system is broken.

### Diagnose

```sql
SELECT id, tenant_id, operation_id, idempotency_key, lease_owner, lease_expires_at, created_at
FROM merchant_idempotency_records
WHERE state = 'processing' AND lease_expires_at < now()
ORDER BY created_at;
```

Check `lease_owner` — if the same pod appears across many rows, that instance likely
crashed mid-request. Check whether the DOWNSTREAM operation actually completed despite
the stuck record (ledger transaction, payin intent, or payout) — if it did, the stuck
record is a bookkeeping gap only, and `TakeoverExpiredLease` will still let the retry
proceed correctly.

### Fix

**Do not manually flip `state` to `'completed'` or `'failed'`** — a fabricated
response could desync the merchant's records. The correct fix: let (or ask) the
merchant retry with the same `Idempotency-Key`. If the merchant's client cannot retry
and the downstream operation never actually executed, the merchant must submit a NEW
request with a NEW key — there is no way to "resume" an abandoned key.

If the alert fires broadly across many tenants, suspect a rolling deploy that killed
pods mid-request without graceful drain, or a database connectivity blip. Check deploy
timestamps and Postgres connection logs around when the alert started.

### Verify recovery

```sql
SELECT count(*) FROM merchant_idempotency_records
WHERE state = 'processing' AND lease_expires_at < now();
```

Trending toward zero as retries land. The gauge (`seev_merchant_idempotency_records{state="processing"}`,
refreshed every 30s) and the Merchant B2B dashboard are the fastest confirmation.

---

## Quota backend outage

**Symptom:** Merchant API requests start failing with `503` and a `Retry-After` header,
or read requests succeed but with a degraded-mode response header, while Redis is
unreachable.

### Fail-open/fail-closed behavior

`services/gateway/internal/merchant/quota.Enforcer.Check` (T4) is deliberately asymmetric:

- **Write requests fail CLOSED** — `503` with `Retry-After`. `MERCHANT_QUOTA_FAIL_OPEN`
  defaults to `false`; an operator must explicitly opt into fail-open behavior for writes.
- **Read requests degrade to ALLOW** — reads succeed with a `Degraded: true` internal
  flag; no side-effect risk from being temporarily unmetered.

`503` means the Redis backend is unreachable. `429` means the enforced limit was
legitimately exceeded. Confirm which one you're seeing.

### Diagnose

```bash
docker compose exec redis redis-cli ping
docker compose logs gateway-service | grep "cache.NewFailoverLimiter"
```

### During the outage

Writes keep failing `503` — this is expected and safe. Do not manually flip
`MERCHANT_QUOTA_FAIL_OPEN=true` unless the business impact of unbounded write volume
is explicitly determined to be less bad than the outage; that decision needs the same
authority as any other fail-safe override.

### Recovery

Once Redis is healthy, writes resume automatically — `cache.NewFailoverLimiter`'s own
recovery requires two consecutive healthy background probes. No manual re-enable step;
no cache to warm; no quota state to repair (Redis rate limit counters naturally reset
during the outage — an empty bucket on reconnect is equivalent to no requests having
been made during that window).

Confirm recovery by watching `503` responses stop in gateway's access logs, or via a
manual test write against a known sandbox tenant.

---

## Owner-service outage

**Symptom:** Merchant transfer, pay-in, or payout requests fail or time out because
Ledger, Payin, or Payout is unreachable.

### Understand the blast radius

Gateway's merchant module shares the SAME gRPC connections as end-user traffic to
Ledger/Payin/Payout — there is no separate merchant-only connection. A Ledger/Payin/Payout
outage therefore affects merchant AND end-user traffic simultaneously. If end-user traffic
through the same owner service is also failing, follow that owner service's own runbook
rather than this one.

### Diagnose

```bash
docker compose ps ledger-service payin-service payout-service
curl -k --cacert deploy/certs/ca.pem \
  --cert deploy/certs/dev-operator.pem \
  --key deploy/certs/dev-operator-key.pem \
  https://localhost:<internal-port>/ready
```

A transient network blip is usually retried by gRPC's own backoff. A long-lived TLS/identity
failure requires a Gateway restart (check cert expiry if the outage coincides with a cert
rotation window — see [cert-rotation.md](cert-rotation.md)).

Check `merchant_idempotency_records` for a spike in `'processing'` rows with `expires_at`
approaching — the visible symptom of requests hanging against an unreachable owner service.

### During the outage

**A merchant retry during the outage is safe.** Every merchant-facing write path is
idempotency-gated (T4) BEFORE the owner-service call. A request that times out
mid-flight leaves its idempotency record `'processing'`; a retry with the SAME
`Idempotency-Key` either replays the already-completed response or takes over the expired
lease and tries again. Merchants should always retry with the same idempotency key during
an outage, never a new one — a new key risks double-submission once the owner service
recovers.

Do not manually resubmit a merchant's request using a fabricated idempotency key.

### Recovery

Confirm via a synthetic sandbox transaction that the full merchant path
(auth → quota → idempotency → owner-service call → response) works end to end before
declaring the incident resolved.

---

## Webhook backlog

**Alerts:** `SeevMerchantWebhookBacklogStale`, `SeevMerchantWebhookDeliveriesDeadLettering`,
`SeevMerchantWebhookDeliveryFailuresHigh`.

**Symptom:** Merchant B2B dashboard shows a growing `pending`/`failed` count, rising
oldest-delivery age, or nonzero `dead` rate.

### Mechanism

T7's relay worker (`services/gateway/internal/merchant/webhook.RelayWorker`) polls every
10s, dispatches with a 5s connect / 10s response timeout, and retries failures with
exponential backoff: base 30s, factor 2, capped at 15 minutes, up to 50% jitter. A
delivery reaches `'dead'` only after 15 failed attempts OR an explicit `410 Gone`.

### Diagnose

```bash
# Check if the relay worker is running at all
docker compose logs gateway-service | grep "merchant/webhook: process once failed"
```

A steady `"claim due deliveries"` error means worker is ticking but can't reach
Postgres — treat as a database connectivity incident. If the worker is running, find the
failing endpoints:

```sql
SELECT e.id, e.url, e.status, e.environment, count(*) AS pending_or_failed
FROM merchant_webhook_deliveries d
JOIN merchant_webhook_endpoints e ON e.id = d.endpoint_id
WHERE d.status IN ('pending', 'failed')
GROUP BY e.id, e.url, e.status, e.environment
ORDER BY pending_or_failed DESC;
```

Check the most recent attempts:
```sql
SELECT a.attempt_number, a.started_at, a.http_status, a.error_code, a.response_excerpt
FROM merchant_webhook_attempts a
JOIN merchant_webhook_deliveries d ON d.id = a.delivery_id
WHERE d.endpoint_id = '<endpoint_id>'
ORDER BY a.started_at DESC LIMIT 20;
```

`error_code = 'dispatch_error'` with no `http_status` = receiver was unreachable (DNS,
connection refused, timeout, or SSRF guard refusing a private/metadata address). An
`http_XXX` code means the receiver responded but rejected.

### Fix

**Receiver-side outage:** self-resolves once they recover — backoff keeps retrying for
up to ~2 hours before dead-lettering. If down longer, disable the endpoint proactively
(`POST .../webhooks/{id}/disable`, maker + reason) then have them re-enable and request
a replay once recovered.

**Live-mode SSRF rejection:** confirm the URL genuinely resolves to a public address.
A private/loopback/link-local/cloud-metadata target is refused structurally in live mode
(`services/gateway/internal/merchant/webhook/ssrf.go`).

**410 auto-disabled endpoint:** `status = 'disabled'`, `disabled_at` set — receiver
told us it's gone. No "re-enable" for a 410-disabled endpoint; the merchant must register
a new endpoint.

### Replay a dead-letter

Once the underlying cause is fixed:
```http
POST /api/v1/admin/gateway/tenants/{tenant_id}/deliveries/{delivery_id}/replay
```

Requires maker role and a non-empty `reason`. Creates a NEW delivery row sharing the
same event id — the merchant's receiver sees an identical `id`/`created_at`/body/signature
to the original attempt, so their idempotency-on-`id` handling treats it correctly as the
same logical event.

Confirm:
```sql
SELECT id, status, replay_of_delivery_id, created_at FROM merchant_webhook_deliveries
WHERE replay_of_delivery_id = '<original_delivery_id>' ORDER BY created_at DESC;
```

---

## Webhook secret compromise

**Symptom:** Merchant reports their webhook signing secret has leaked, or you observe
webhook deliveries being accepted by a receiver that shouldn't be able to verify signatures.

### Immediate action: rotate

```http
POST /api/v1/admin/gateway/tenants/{tenant_id}/webhooks/{endpoint_id}/rotate-secret
```

Maker role; no reason required (rotation itself is low-risk — unlike disable/replay, it
doesn't stop or resend anything). The response's `plaintext_secret` is shown **exactly
once** — hand it to the merchant through a secure out-of-band channel, never a ticket or
chat log.

Confirm the old secret's ciphertext changed:
```sql
SELECT secret_version, updated_at FROM merchant_webhook_endpoints WHERE id = '<endpoint_id>';
```

### What rotation does NOT do automatically

**In-flight retries signed under the OLD secret keep using it.** T7's signature scheme
signs `t` once at delivery creation — `Sign(secret, delivery.CreatedAt, payload)` uses
whatever secret was current AT THAT DELIVERY'S CREATION TIME, read fresh from the endpoint
row on every attempt. Rotating the secret changes what NEW deliveries sign with; it does
NOT retroactively re-sign queued deliveries. During the transition window, tell the merchant
to accept both old and new secrets until the pre-rotation backlog clears.

Rotation does not disable the endpoint or interrupt delivery — traffic continues
uninterrupted using the new secret for every delivery created from this point forward.

### If compromise is actively being misused

Force-disable the endpoint immediately (buying time for the merchant to fix their receiver
before you rotate and re-enable):

```http
POST /api/v1/admin/gateway/tenants/{tenant_id}/webhooks/{endpoint_id}/disable
```

Maker role + reason required. This stops ALL delivery; weigh it against the simpler
rotate-only path. Disabling is for "stop everything now," not routine hygiene. There is
no re-enable for a disabled endpoint — the merchant must create a fresh one.

Advise the merchant to audit their receiver logs for signatures that verified successfully
against the OLD secret from unexpected IPs or at unexpected times.
