# Runbook: Merchant Webhook Backlog / Dead-Letter

> [Documentation home](../../README.md) · [Operations](../README.md) · [Runbooks](README.md)

> **Status: Current. Audience: operators.**

**Alerts:** `SeevMerchantWebhookBacklogStale`,
`SeevMerchantWebhookDeliveriesDeadLettering`,
`SeevMerchantWebhookDeliveryFailuresHigh`
(`deploy/observability/prometheus/rules/merchant.yml`).

**Symptom:** the [Merchant B2B dashboard](../../../deploy/observability/grafana/dashboards/merchant-b2b.json)
shows a growing `pending`/`failed` count in "Webhook deliveries by
status," a rising "oldest delivery age," or a nonzero `dead` rate.

## Understand the mechanism

T7's relay worker (`services/gateway/internal/merchant/webhook.RelayWorker`) polls for due
deliveries every 10s (`DefaultWebhookRelayInterval`), dispatches with a
5s connect / 10s response timeout, and retries a failure with exponential
backoff: base 30s, factor 2, capped at 15 minutes, plus up to 50% jitter.
A delivery only reaches `'dead'` after 15 failed attempts OR an explicit
`410 Gone` from the receiver (which also auto-disables the endpoint).

## Diagnose

1. Is the relay worker running at all, or is the backlog growing because
   NOTHING is being attempted?
   ```bash
   docker compose logs gateway-service | grep "merchant/webhook: process once failed"
   ```
   A steady stream of `"claim due deliveries"` errors means the worker is
   ticking but can't reach Postgres — treat as a database connectivity
   incident, not a webhook-specific one.
2. If the worker is running and attempts ARE happening, find which
   endpoint(s) are failing:
   ```sql
   SELECT e.id, e.url, e.status, e.environment, count(*) AS pending_or_failed
   FROM merchant_webhook_deliveries d
   JOIN merchant_webhook_endpoints e ON e.id = d.endpoint_id
   WHERE d.status IN ('pending', 'failed')
   GROUP BY e.id, e.url, e.status, e.environment
   ORDER BY pending_or_failed DESC;
   ```
3. Check the most recent attempts against the worst-offending endpoint:
   ```sql
   SELECT a.attempt_number, a.started_at, a.http_status, a.error_code, a.response_excerpt
   FROM merchant_webhook_attempts a
   JOIN merchant_webhook_deliveries d ON d.id = a.delivery_id
   WHERE d.endpoint_id = '<endpoint_id>'
   ORDER BY a.started_at DESC LIMIT 20;
   ```
   `error_code = 'dispatch_error'` with no `http_status` means the
   receiver was unreachable (DNS, connection refused, timeout, or — in
   live mode — the SSRF guard refusing to dial a private/metadata
   address); an `http_XXX` code means the receiver responded but
   rejected/errored.

## Fix

4. **Receiver-side outage** (the merchant's own endpoint is down): this
   resolves itself once they recover — the backoff schedule keeps
   retrying for up to ~2 hours (15 attempts at up to 15-minute intervals)
   before dead-lettering. If the merchant confirms they'll be down longer
   than that, consider disabling the endpoint proactively
   (`POST .../webhooks/{id}/disable`, maker + reason) to stop generating
   futile attempts, then have them re-enable and request a
   [replay](#replay-a-dead-letter) once recovered.
5. **Live-mode SSRF rejection** (`error_code = 'dispatch_error'`, no
   `http_status`, and the endpoint's `environment = 'live'`): confirm the
   URL genuinely resolves to a public address from your network — a
   private/loopback/link-local/cloud-metadata target is refused
   structurally in live mode (`services/gateway/internal/merchant/webhook/ssrf.go`), not a
   bug. If the merchant insists their receiver is genuinely public, check
   for DNS misconfiguration or a CDN/load-balancer resolving to an
   internal address from Gateway's own network position.
6. **410 auto-disabled endpoint**: `merchant_webhook_endpoints.status =
   'disabled'`, `disabled_at` set — the receiver itself told us it's
   gone. This requires the merchant to either fix the endpoint and
   register a NEW one, or confirm the old URL should come back (there is
   no "re-enable" for a 410-disabled endpoint distinct from creating a
   fresh one — a 410 is a deliberate, permanent signal from HTTP
   semantics).

## Replay a dead-letter

7. Once the underlying cause is fixed, replay the specific delivery:
   ```http
   POST /api/v1/admin/gateway/tenants/{tenant_id}/deliveries/{delivery_id}/replay
   ```
   Requires the maker role and a non-empty `reason`. This creates a NEW
   delivery row sharing the SAME event id — the merchant's receiver sees
   an identical `id`/`created_at`/body/signature to the original attempt
   (T7's own "retries reuse exact event bytes" guarantee extends to
   replays), so their own idempotency-on-`id` handling treats it
   correctly as the same logical event.
8. Confirm the replay lands:
   ```sql
   SELECT id, status, replay_of_delivery_id, created_at FROM merchant_webhook_deliveries
   WHERE replay_of_delivery_id = '<original_delivery_id>' ORDER BY created_at DESC;
   ```

## Verify recovery

9. Backlog count and oldest-age gauges on the dashboard trending back
   down; `SeevMerchantWebhookDeliveryFailuresHigh`/
   `SeevMerchantWebhookBacklogStale` clearing.
