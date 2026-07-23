# 22 — Phase 4a: Payin Module and Vendor Gateway (`mockvendor`)

Prerequisite: [plan 21](21-service-topology-review.md), including decisions K-T1, K-T2, K-T5, and K-T6, plus `boundary_test.go`. The verification rules from [plan 09](09-hardening-review.md) apply: every posting path requires PostgreSQL integration tests and curl smoke tests.

## Goal and scope

This phase implements the first real money-in path. A vendor webhook is verified, deduplicated, and posted as `money_in` to the ledger through `internal/payin`.

The scope is settled-webhook-only: a successful payment webhook becomes a ledger credit. VA/QRIS payment intents and pending-payment flows are intentionally deferred.

## T1 — Vendor-neutral gateway contract

### Design

Add `internal/vendorgw` with a normalized `PayinEvent` and a `PayinVerifier` interface:

```go
type PayinEvent struct {
    Vendor        string
    VendorEventID string
    ExternalRef   string
    UserID        uuid.UUID
    Amount        decimal.Decimal // integral minor units
    Currency      string
    OccurredAt    time.Time
}

type PayinVerifier interface {
    Vendor() string
    VerifyAndParse(headers http.Header, rawBody []byte) (*PayinEvent, error)
}
```

The signature must be calculated over the raw request bytes before JSON decoding. A valid signature with a non-settled event returns `(nil, nil)` so the receiver can acknowledge an event it does not process.

The registry is a plain container (`NewRegistry`, `AddPayin`, and `Payin`). The composition root constructs the configured adapter and registers it. This avoids a circular import between the root gateway package and adapter subpackages while preserving the “one adapter package plus one registry entry” property.

`internal/vendorgw/mockvendor` implements HMAC-SHA256 with the `X-Mock-Signature` header and exports `Sign` for tests and smoke scripts. It uses string amounts rather than JSON floating-point numbers.

### Result

The interface, registry, mock adapter, invalid-signature sentinel, and boundary tests were implemented. Twelve unit tests cover normalization, precision, invalid signatures, modified bodies, non-settled events, and unknown or disabled vendors. The raw-body signature test proves that re-marshalling semantically equivalent JSON does not affect verification of the original bytes.

The boundary rule was adjusted to exempt the composition root and test files where construction and integration tests legitimately need adapter subpackages. Production module boundaries remain enforced.

## T2 — Payin module and `money_in` mapping

### Data model

Migration `000019_payin` adds `payin_webhook_events` with:

- vendor and vendor-event identifiers;
- external reference, user, amount, and currency;
- raw JSON for forensics and replay;
- `received`, `posted`, and `failed` status;
- a unique `(vendor, vendor_event_id)` deduplication key.

The raw payload is never exposed by reporting or admin list responses. Grants and RLS follow the screening pattern: `app_service` may select/insert/update status, and `app_readonly` may select.

### Processing flow

`payin.Module.HandleWebhook` performs:

1. Registry lookup; unknown vendors return a stable 404 error.
2. Signature verification; an invalid signature returns an error without any database side effect.
3. Insert-or-load of the webhook event. A previously posted event is an idempotent success. A received or failed event may be retried.
4. `ledger.Post` with `money_in`, an idempotency key `payin:<vendor>:<vendor-event-id>`, scope `payin:<vendor>`, and `gateway` plus `external_ref` metadata.
5. Status update to `posted` on success. Infrastructure failures leave the event `received` so the vendor returns later. Genuine business failures are recorded as `failed` and acknowledged with HTTP 200; an operator can replay them after fixing the underlying issue.

The vendor-to-gateway mapping is injected from configuration. `mockvendor` maps to `bca` for the MVP.

### Result

The payin facade, repository, model, migration, registry wiring, and composition-root configuration were implemented. A design review during integration testing clarified that only errors explicitly wrapped as `*LedgerError` are classified as retryable business failures; other validation and infrastructure errors retain their existing transaction semantics. This behavior is documented in code so future validators use the correct error wrapper.

Unit tests cover the happy path, duplicate delivery, infrastructure failure, business failure, and replay. PostgreSQL integration tests prove that a real webhook increases the user balance, stores gateway and external-reference metadata for existing reconciliation, and leaves the ledger verifier clean. Ten concurrent deliveries create exactly one webhook event and one ledger transaction. Migration 000019 passed up/down/up verification.

## T3 — Public webhook receiver

### Design

Add `POST /webhooks/{vendor}` directly to the public root, outside JWT, CORS, and JSON-validation middleware, but inside request ID, logging, recovery, security headers, timeout, and per-vendor rate limiting.

The handler caps the body at 64 KiB, reads the raw bytes, calls `payin.HandleWebhook`, and returns only `{"received":true}`. Errors map to 404 for an unknown vendor, 401 for an invalid signature, 200 for an acknowledged business failure, and 503 for infrastructure failure. With no vendor configured, every webhook route returns 404, preserving backward compatibility.

### Result

The webhook chain and configuration were implemented. Enabling `mockvendor` without a secret is rejected at startup because an empty HMAC secret would accept signatures that should not be trusted.

Integration tests cover valid and invalid signatures, unknown vendors, oversized bodies, and redelivery after an infrastructure failure. A smoke test against the live server verified a signed webhook, the `posted` event row, and the user's increased balance.

During testing, a pre-existing logging bug was discovered: the request-body masking helper truncated bodies above 16 KiB before downstream handlers could enforce their own limits. That issue belongs to `pkg/logger`, not the payin implementation, and was tracked separately rather than hidden inside this phase.

## T4 — Admin operations

Add internal, admin-gated endpoints to list webhook events and replay failed events. Responses expose structured fields only and never include the raw payload. Replay uses the same idempotency key and processing path as the original webhook.

### Result

Admin list and replay handlers were added and tested for authorization, filtering, not-found behavior, and idempotent replay. The internal router is not publicly reachable.

## Final verification

```bash
go build ./...
go vet ./...
go vet -tags=integration ./...
make test
go test -tags=integration -race ./...
./scripts/business-e2e.sh
```

Migration 000019 was tested through up/down/up, and the signed webhook smoke test was run against the Docker stack.
