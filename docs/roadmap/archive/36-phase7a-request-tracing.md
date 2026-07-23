# 36 — Phase 7a: MVP Product — Master Reference and End-to-End Request Tracing

This document is both the master reference for plans 36–41 and the implementation plan for request tracing. Prerequisite: [plan 34](34-phase6i-verification.md), which verifies the six-service split.

## Context

Plans 26–34 complete the six-service topology, per-service databases, internal gRPC, database-driven routing and fees, and the chaos suite. Plans 36–41 turn that platform into a reliable fintech MVP while keeping development breaking changes acceptable.

The product capabilities are:

1. end-to-end request tracing;
2. fraud screening outside the ledger database transaction;
3. user-visible fee quotes that are honored exactly;
4. tiered KYC and transaction limits;
5. multi-vendor circuit breaking and safe pre-confirmation payout failover;
6. a final acceptance journey and documentation.

Currency and FX primitives already exist. New tables and quotes must carry currency, while full non-IDR activation remains future work.

## Locked decisions for plans 36–41

| Area | Decision |
|---|---|
| HTTP tracing | Sanitize and propagate `X-Request-Id`; generate one when absent. |
| gRPC tracing | Propagate `x-request-id` through client and server interceptors. |
| AMQP tracing | Use `CorrelationId`; outbox events persist the request ID because relay publishing is outside the original request context. |
| Persistence | Store nullable request IDs in payin webhook events and intents, payout requests, screening events, and ledger transactions. Rename payout vendor-call `request_id` to `payout_request_id` because it already means the payout UUID. |
| KYC | L0/L1/L2. State lives in auth; the mock provider automatically approves L1 and refers L2 for admin review. |
| KYC enforcement | JWT claims provide UX hints. Synchronous `ApplyKycTier` updates ledger policy limits, which remain authoritative. |
| Fraud placement | Ledger transport checks P2P before the database transaction; payin checks before posting; payout checks before hold. Settlement and cancellation are not screened. |
| Fee quotes | Single-use `fee_quotes` with exact amount binding and a ten-minute default TTL. Consume atomically. Expired, consumed, or missing quotes return 422; never silently reprice. |
| Payout fees | Quote at create, store fee data on `payout_requests`, and settle using the stored values. Insert the request before consuming the quote; failed consumption rejects the request rather than burning a quote without a record. |
| Breaker | Per-process in-memory breaker. Only transport, timeout, and 5xx errors trip it; business rejections do not. Breaker is not a monetary safety control. |
| Payout failover | Allowed only before vendor confirmation, while state is `created` or `held` and no vendor call is accepted or uncertain. An unknown result after submit permanently pins the request to that vendor. |
| Routing | Resolve all matching candidates in priority order, skip unregistered or open-breaker vendors, and return 503 `VENDOR_UNAVAILABLE` if none remain. |
| Second mock vendor | Register `mockvendor2` behind environment flags and seed its routing rule through the admin API, not a migration. |
| Migrations | Ledger request ID 000020, fee quotes 000021, policy tier limits 000022; auth 000002, payin 000004, payout 000003/000004, and fraud 000002. Never fill the old ledger 000017 gap. |
| Protobuf | Additive changes only; run Buf generation, lint, and breaking checks and commit generated code. |
| Order | Tracing → fraud seam → fee quotes → KYC → vendor resilience → final acceptance. |

## Required gate for every phase

Every phase must pass `make lint`, `make test`, both normal and integration `go vet`, `scripts/smoke-test.sh`, `scripts/business-e2e.sh`, and `scripts/chaos-test.sh all` before it is considered complete.

## Execution notes

- Add the request-ID interceptor before gRPC logging so every log receives the ID.
- Sanitize public IDs to 64 safe characters; invalid values are replaced rather than logged.
- Strip client metadata before injecting server-owned request IDs and quoted-fee values.
- Consume a P2P quote in the same database transaction as posting, after idempotency lookup and before balance locking.
- Do not screen payout settlement or cancellation; the money is already held.
- Update fixture users and admin approval steps whenever a new KYC gate is added.
- A missing `kyc_level` claim in an old token means L0, not an authentication error.
- Payin webhook verification accepts events from a vendor even if that vendor's outbound breaker is open.
- Do not trip breakers for business validation errors.
- Keep the development memory constraint: do not combine full Compose, Jaeger, and testcontainers.

Deferred work includes top-up fees, a real KYC provider and document storage, distributed breakers, expired-quote cleanup, full non-IDR activation, broader OpenTelemetry spans, and an asynchronous KYC tier retry queue.

## Phase 7A — Request tracing

Propagate one request ID from the gateway through HTTP proxying, gRPC, AMQP, logs, and domain storage.

### T1 — Request ID source and sanitization

Update `pkg/middleware.WithRequestID` to write the generated or validated value to both request and response headers. Reject oversized or unsafe client values. Add a logger helper that includes the ID in every HTTP access log.

**Result:** generated IDs are now available to reverse proxies; valid IDs are preserved; unsafe IDs are replaced. Seven middleware tests passed.

### T2 — Gateway proxy forwarding

Wrap the ledger reverse-proxy director so it explicitly copies the context request ID after the default path and host rewriting.

**Result:** tests prove both client-supplied and gateway-generated IDs reach the ledger backend.

### T3 — gRPC propagation

Add a server interceptor before logging to extract `x-request-id` or generate one, and a client interceptor to inject the context value. Include the ID in gRPC logs.

**Result:** bufconn tests prove identical IDs across client and server contexts and generated IDs for background callers.

### T4 — AMQP correlation

Use an explicit correlation ID when supplied; otherwise fall back to the request ID. Consumers put the delivery correlation ID into their handler context. Persist request IDs in `TransactionPosted` envelopes so the outbox relay can restore them when publishing outside the original request context.

**Result:** publisher/consumer round-trip tests and relay tests passed. Business end-to-end checks prove notification and fraud consumers receive the originating request ID.

### T5 — Domain persistence

Add request-ID columns to payin, payout, fraud, and ledger migrations. Preserve the payout-call rename. Inject the server-owned ID into ledger metadata after client metadata is stripped, then extract it into `ledger_transactions` beside existing `external_ref` and `gateway` fields.

**Result:** all four migrations passed up/down/up verification. Integration tests prove round trips and client spoofing protection across payin, payout, fraud, ledger, and notification.

### T6 — End-to-end assertion and index

Extend `business-e2e.sh` with a transfer carrying a known request ID and assert response echo, gateway and ledger logs, and `ledger_transactions.request_id`. Update the plan index.

**Result:** the clean-volume full gate passed: lint, both vet modes, unit/integration tests, smoke, business journey, and all seven chaos scenarios.

## Final verification

Phase 7A is complete when the master gate is green from a fresh PostgreSQL volume. Continue to [plan 37](37-phase7b-fraud-seam.md).
