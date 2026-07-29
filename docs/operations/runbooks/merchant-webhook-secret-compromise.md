# Runbook: Merchant Webhook Endpoint Secret Compromise

> [Documentation home](../../README.md) · [Operations](../README.md) · [Runbooks](README.md)

> **Status: Current. Audience: operators.**

**Symptom:** a merchant reports their webhook signing secret has leaked
(committed to a public repo, exposed in a log, shared insecurely), or you
observe webhook deliveries being accepted by a receiver that shouldn't be
able to verify signatures it never should have received (suggesting
someone else has the secret and is forging events, or the merchant's own
verification is broken and they mistakenly suspect compromise).

## Immediate action: rotate

1. Rotate via the Admin BFF console or directly:
   ```http
   POST /api/v1/admin/gateway/tenants/{tenant_id}/webhooks/{endpoint_id}/rotate-secret
   ```
   Maker role, no reason required (rotation itself is low-risk — unlike
   disable/replay, it doesn't stop or resend anything). The response's
   `plaintext_secret` field is shown exactly once, matching the same
   one-time-display contract as API key creation
   ([merchant-api-key-compromise.md](merchant-api-key-compromise.md)) —
   hand it to the merchant through your own secure channel, never a
   ticket or chat log that gets archived in plaintext.
2. Confirm the OLD secret's ciphertext changed:
   ```sql
   SELECT secret_version, updated_at FROM merchant_webhook_endpoints WHERE id = '<endpoint_id>';
   ```
   `secret_version` reflects the `pkg/cryptox.Ring` key version the NEW
   secret was sealed under — it does not itself prove the SECRET changed
   (the ring's current key version may be unchanged across a rotation),
   but `updated_at` moving confirms the row was actually rewritten.

## What rotation does NOT do automatically

3. **In-flight retries signed under the OLD secret keep using it.** T7's
   signature scheme signs `t` once at delivery creation and never
   recomputes it — `Sign(secret, delivery.CreatedAt, payload)` uses
   whatever secret was current AT THAT DELIVERY'S CREATION TIME, read
   fresh from the endpoint row on every attempt. Rotating the secret
   changes what NEW deliveries (created after rotation) sign with; it
   does NOT retroactively re-sign deliveries already queued before the
   rotation. If those pre-rotation deliveries are still retrying, the
   receiver must be prepared to verify against EITHER secret during the
   transition window — this is exactly the "accept both old and new
   during an overlap window" guidance in
   [docs/reference/webhook-receiver-guide.md](../../reference/webhook-receiver-guide.md)'s
   own "Secret rotation" section. Tell the merchant to keep the old
   secret valid on their side until the in-flight backlog for this
   endpoint clears (check via the webhook-backlog runbook's own
   diagnostic query, scoped to this `endpoint_id`).
4. Rotation does not disable the endpoint or interrupt delivery — traffic
   continues uninterrupted using the new secret for every delivery
   created from this point forward.

## If the compromise is severe (secret was actively being misused)

5. Consider force-disabling the endpoint immediately instead of (or in
   addition to) rotating, if you believe an attacker is actively forging
   signed events toward the merchant's receiver right now:
   ```http
   POST /api/v1/admin/gateway/tenants/{tenant_id}/webhooks/{endpoint_id}/disable
   ```
   Maker role + a reason are required. This stops ALL delivery to this
   endpoint immediately, buying time for the merchant to fix their
   receiver-side verification before you rotate and re-enable — you
   cannot re-enable a disabled endpoint distinct from creating a fresh
   one (see the disable/410 note in
   [merchant-webhook-backlog.md](merchant-webhook-backlog.md)), so weigh
   this against the simpler rotate-only path above; disabling is for
   "stop everything now," not routine hygiene.
6. Advise the merchant to audit their own receiver's logs for signatures
   that verified successfully against the OLD secret from IP addresses or
   at times they don't recognize — that is direct evidence of actual
   misuse, not just a theoretical leak.

## Verify recovery

7. New deliveries for this endpoint use the new secret (confirm by
   decrypting is not possible/needed at the operator level — instead,
   confirm the merchant's receiver reports successful verification on the
   next delivery after rotation).
8. `updated_at` on the endpoint row reflects the rotation timestamp, and
   no further deliveries reference the old secret once the pre-rotation
   backlog (if any) has fully drained or dead-lettered.
