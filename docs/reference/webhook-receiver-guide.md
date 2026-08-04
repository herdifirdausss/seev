# Merchant webhook receiver guide

Audience: a merchant tenant building a receiver for Seev's outbound B2B
webhooks (Plan 57 T7, `services/gateway/internal/merchant/webhook`). This is the
implementation-facing companion to `docs/reference/c1-b2b-design.md §2`,
which is the locked contract; this document only explains how to verify
and consume it correctly.

## 1. Envelope shape

Every delivery POSTs this exact JSON body to your configured endpoint URL:

```json
{
  "id": "evt_018f1e2a-...",
  "type": "transaction.posted.v1",
  "livemode": true,
  "created_at": "2026-01-15T10:30:00Z",
  "data": { "...": "the event-specific payload" }
}
```

- `id` is stable across every redelivery and every replay of the same
  logical event — use it as your idempotency key (§3 below).
- `livemode` reflects your tenant's own environment (`sandbox` or `live`)
  — a tenant is always entirely one or the other, never mixed.
- `type` is currently always `transaction.posted.v1`. Treat unknown future
  values as ignorable, not an error, so new event types can roll out
  without breaking existing receivers.

## 2. Verifying the signature

Every delivery carries:

```http
Seev-Signature: t=1735808645,v1=<hex hmac-sha256>
```

`v1` is `HMAC-SHA256(your_endpoint_secret, "{t}.{raw_request_body}")`,
hex-encoded. `t` is a Unix timestamp. Verify in three steps:

1. Split the header on `,` and parse `t=` and `v1=`.
2. Recompute `HMAC-SHA256(secret, "{t}.{raw_body}")` using the **raw,
   unparsed** request body bytes — not a re-serialized copy of the JSON.
   Any JSON re-encoding (key reordering, whitespace changes) will not
   match the signature that was actually computed.
3. Compare in constant time. Reject the request if they don't match.

Also enforce a replay-window check: reject a signature whose `t` is more
than a few minutes old or in the future, the same way this codebase's own
`webhook.VerifyWithTolerance` does it.

**Important:** `t` is NOT refreshed on retry. Every attempt of the same
delivery — including replays — carries the exact same `t` and the exact
same raw body, so `v1` is reproducible across your own retry/attempt logs.
Do not treat a repeated `t` as suspicious; it is expected for redeliveries
of one logical event.

### Reference implementation (Go)

This is exactly what `services/gateway/internal/merchant/webhook.Verify` /
`VerifyWithTolerance` do — reproduced here as an example for receivers
written outside this codebase:

```go
func verifySeevSignature(secret []byte, header string, body []byte, now time.Time, tolerance time.Duration) bool {
	parts := strings.Split(header, ",")
	if len(parts) != 2 {
		return false
	}
	var t int64
	var v1 string
	for _, p := range parts {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			return false
		}
		switch kv[0] {
		case "t":
			parsed, err := strconv.ParseInt(kv[1], 10, 64)
			if err != nil {
				return false
			}
			t = parsed
		case "v1":
			v1 = kv[1]
		}
	}
	if t == 0 || v1 == "" {
		return false
	}
	signedAt := time.Unix(t, 0)
	if signedAt.Before(now.Add(-tolerance)) || signedAt.After(now.Add(tolerance)) {
		return false
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(strconv.FormatInt(t, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(v1), []byte(want))
}
```

## 3. Idempotency — you WILL see duplicates

This is an at-least-once delivery system, by design:

- RabbitMQ redelivery can cause the relay to enqueue an event more than
  once internally, though the relay's own dedup (on the internal ledger
  event's logical ID) collapses that to a single external event and a
  single automatic delivery per (endpoint, event) pair.
- A failed attempt is retried on a backoff schedule (§4) — every retry
  resends the **exact same** `id`, `created_at`, and body bytes.
- A tenant or operator can explicitly **replay** a past event — a replay
  creates a new delivery (so you'll see a new HTTP request) but carries
  the **same** `id` as the original.

Your receiver must be idempotent on `id`: if you've already processed an
`id`, acknowledge (2xx) and skip re-applying its side effects.

## 4. Retry schedule and dead state

A failed attempt (non-2xx response, timeout, connection error) is retried
with exponential backoff: base 30s, factor 2, capped at 15 minutes, plus
up to 50% random jitter — the same formula this codebase uses for its
other outbox-style retries. After 15 attempts, the delivery is marked
`dead` and stops retrying. You can request a replay after fixing whatever
caused the failures.

Returning HTTP `410 Gone` tells the relay your endpoint is permanently
retired: it auto-disables the endpoint (no further deliveries, to any
event) rather than continuing to retry. Only return `410` if the endpoint
URL itself is being decommissioned, not for a transient failure.

## 5. Response requirements

- Respond within 10 seconds. A slower response is treated as a failed
  attempt.
- Return any 2xx status to acknowledge. Anything else is a failure.
- The relay never follows redirects — respond directly, don't 3xx.
- The relay reads at most 64 KiB of your response body (for logging); it
  ignores anything beyond that. Keep your response body small.

## 6. Sandbox vs. live

`livemode` mirrors your tenant/endpoint environment. Sandbox endpoints may
point at a local/private address for testing; live endpoints must be a
publicly routable HTTPS URL — private, loopback, link-local, and cloud
metadata addresses are rejected at dispatch time for live endpoints, not
only at creation, so a URL that later resolves to a private address (e.g.
DNS rebinding) is still rejected on the next delivery attempt.

## 7. Secret rotation

Your endpoint's signing secret is shown to you exactly once, at creation
or rotation — Seev never stores or displays the plaintext again. If you
rotate, accept both the old and new secret for a short overlap window
until you've confirmed the new one is deployed, since an in-flight retry
may still be signed with the secret that was current at delivery-creation
time.
