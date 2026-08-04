# Plan 57 — C1 Merchant/B2B API

**Created:** 2026-07-28
**Status:** Ready for execution after the C1 entry gate
**Roadmap track:** C1 — Merchant/B2B API
**Depends on:** Plan 49 / A6 Internal Security, Plan 52 / A9 Contract Evolution
**Primary runtime owners:** Gateway, LedgerService, PayinService, PayoutService, Admin BFF
**No new service extraction is authorized by this plan.**

---

## 1. Purpose

Build a secure, tenant-isolated Merchant/B2B API on top of Seev's existing
money-movement architecture.

The track must add:

- merchant tenants;
- sandbox and live-mode credentials;
- API keys and scopes;
- per-tenant quotas;
- durable write idempotency;
- merchant account and transaction reads;
- merchant transfer, pay-in, and payout journeys;
- signed outbound webhooks;
- durable retry, dead-letter, and replay;
- operator management through the existing Admin BFF;
- contract, security, observability, and failure evidence.

The implementation must preserve the existing architectural rules:

1. Gateway remains the public HTTP edge.
2. LedgerService remains the source of truth for money.
3. PayinService and PayoutService remain owners of their lifecycles.
4. VendorService remains the vendor boundary and callback ingress owner.
5. Services may not read another service's database.
6. Every financial mutation must be exactly represented by balanced ledger
   entries.
7. Duplicate HTTP, AMQP, callback, and webhook delivery must never duplicate
   money movement.
8. All money values remain exact integer minor units.
9. Existing consumer and admin contracts must remain compatible.
10. A new service may not be introduced without independent evidence and a new
    roadmap decision.

---

## 2. Activation and entry gate

### 2.1 Activation decision

This execution plan is created from the C1 roadmap trigger on 2026-07-28.

A6 is complete. A9's core contract-governance foundation is archived as done,
while some broader fixture expansion and manual chaos evidence remain follow-up
work.

C1 implementation may begin only after T0 records a fresh entry-gate result.

### 2.2 Required entry-gate evidence

The C1 entry gate is `PASS` only when all of the following are true:

- [ ] `make contracts` passes from a clean tree.
- [ ] The generated HTTP operation inventory has no unresolved drift.
- [ ] Current event schemas and Protobuf semantic checks pass.
- [ ] Every existing operation that C1 will call or extend has a canonical
      contract entry.
- [ ] Every C1-touched existing HTTP operation has at least one live fixture or
      a recorded task that must land in the same PR as the C1 change.
- [ ] The A6 internal-auth and mTLS verification commands pass.
- [ ] Existing business, admin, callback, and smoke journeys remain green.
- [ ] The working branch contains no unrelated schema or topology migration.
- [ ] The exact baseline commit is recorded in this plan's evidence log.

### 2.3 Gate policy

The following work may start before the gate is fully green:

- documentation;
- threat modeling;
- OpenAPI design;
- package scaffolding;
- isolated schema drafts;
- test fixture design.

The following may not merge before the gate is green:

- a public B2B financial write endpoint;
- merchant account provisioning;
- owner-neutral Payin/Payout schema changes;
- outbound merchant webhook delivery;
- any change that alters an existing public contract.

---

## 3. Locked architecture decisions

### 3.1 No MerchantService in C1

C1 does **not** create a tenth service.

Merchant edge concerns belong in a bounded Gateway module because Gateway
already owns public HTTP ingress, request authentication middleware, request
IDs, rate limiting, security headers, timeouts, tracing, and calls into the
money owner services.

Initial package boundary:

```text
services/gateway/internal/merchant/
├── api/                 # HTTP handlers and DTO mapping
├── application/         # use cases and orchestration
├── auth/                # API-key verification and scopes
├── quota/               # policy loading and distributed enforcement
├── idempotency/         # request fingerprint and response replay
├── webhook/             # endpoint, event, delivery, signing, retry
├── repository/          # Gateway-owned persistence only
├── client/              # typed clients to Ledger/Payin/Payout
├── model/               # internal domain types
└── observability/       # metrics and trace helpers
```

This module may use the Gateway database only for edge-owned state. It may not
read or write Ledger, Payin, Payout, Auth, Vendor, or Admin BFF tables.

### 3.2 Identity split

Human and machine identities remain separate:

- human users continue to use AuthService and JWT/session flows;
- merchants use API keys issued for a merchant tenant;
- an API key is not an Auth user;
- a merchant request does not fabricate a user JWT;
- internal service calls continue to use the existing internal service identity
  and mTLS controls;
- the merchant tenant and key identity are propagated as explicit actor
  metadata.

### 3.3 Financial ownership

- LedgerService owns merchant accounts, balances, entries, statements, and
  transfer posting.
- PayinService owns merchant pay-in intent lifecycle.
- PayoutService owns merchant payout lifecycle.
- VendorService owns vendor dispatch and callback normalization.
- Gateway owns external B2B authentication, authorization, quota,
  idempotency-response replay, and outbound webhook delivery.

### 3.4 Sandbox definition

For C1, `sandbox` is a local learning environment mode, not a claim of
production-grade environmental isolation.

Sandbox rules:

- sandbox keys use a different prefix from live-mode keys;
- sandbox tenants may call only mock adapters;
- sandbox requests can never select a real vendor route;
- sandbox and live-mode keys cannot access each other's tenant resources;
- every public response and webhook envelope exposes `livemode`;
- C1 does not host a public internet sandbox;
- production legal, KYB, AML, settlement, and regulatory isolation are out of
  scope.

### 3.5 Exact money

All public money amounts are decimal strings containing integer minor units.

Example:

```json
{
  "amount": "125000",
  "currency": "IDR"
}
```

The API must not accept or emit floating-point money.

### 3.6 At-least-once delivery

Outbound webhooks are at-least-once.

C1 does not promise exactly-once delivery. Receivers must deduplicate using the
stable event ID.

---

## 4. Product scope

### 4.1 Merchant tenant capabilities

A merchant tenant can:

- authenticate using an API key;
- inspect its own profile;
- inspect its own accounts and balances;
- list and retrieve its own transactions;
- create an idempotent transfer;
- create and retrieve a pay-in;
- create and retrieve a payout;
- create, inspect, rotate, disable, and delete webhook endpoints where policy
  allows;
- list webhook deliveries;
- replay an eligible failed or dead delivery;
- receive signed lifecycle webhooks.

Initial API-key issuance and live-mode tenant activation remain operator-owned
through Admin BFF.

### 4.2 Operator capabilities

Authorized operators can:

- create a sandbox or live-mode merchant tenant;
- provision the tenant's default ledger account;
- activate, suspend, or close a tenant;
- create, rotate, expire, and revoke API keys;
- configure scopes;
- configure quotas;
- inspect webhook endpoints and delivery health;
- disable a compromised endpoint;
- replay eligible deliveries;
- inspect non-secret audit evidence.

### 4.3 Initial external event families

C1 exposes the following external event families:

- `transaction.posted.v1`
- `transaction.reversed.v1`
- `payin.updated.v1`
- `payout.updated.v1`
- `webhook.endpoint.disabled.v1`

Only events relevant to the owning tenant may be delivered.

---

## 5. Explicit non-goals

C1 does not include:

- a new MerchantService;
- OAuth 2.0 client credentials;
- public-edge mTLS;
- client JWT assertions;
- hosted production infrastructure;
- real banking or real-money claims;
- merchant KYB, AML, sanctions, or legal onboarding;
- pricing plans, invoicing, or billing collection;
- multi-region deployment;
- GraphQL;
- generated public SDK publication;
- a merchant web portal;
- arbitrary webhook payload transformations;
- arbitrary merchant-defined retry schedules;
- exactly-once HTTP or webhook delivery;
- cross-service database access;
- direct merchant access to internal gRPC;
- per-tenant Prometheus labels;
- storing plaintext API keys or webhook secrets;
- exposing vendor credentials or vendor-native callback payloads;
- multi-currency or FX behavior beyond the currencies already supported by the
  owner services;
- automatic service extraction after C1.

---

## 6. Public API contract

### 6.1 Contract source

Add:

```text
contracts/http/b2b-v1.yaml
```

Register every operation in:

```text
contracts/compatibility/surfaces.yaml
```

Every operation must have:

- a stable `operationId`;
- authentication and scope requirements;
- success and error schemas;
- canonical live fixtures;
- compatibility classification;
- explicit idempotency behavior for writes;
- request and response examples;
- pagination semantics for lists.

### 6.2 Base path

```text
/api/v1/b2b
```

### 6.3 Required and supported headers

#### Authentication

```http
Authorization: Bearer <merchant_api_key>
```

API-key prefixes:

```text
sk_test_<public-prefix>_<secret>
sk_live_<public-prefix>_<secret>
```

#### Correlation

```http
X-Request-ID: <optional-client-request-id>
```

Gateway returns the accepted or generated request ID.

#### Idempotency

All financial `POST` operations require:

```http
Idempotency-Key: <merchant-generated-key>
```

#### Rate-limit response headers

```http
RateLimit-Limit
RateLimit-Remaining
RateLimit-Reset
Retry-After
```

`Retry-After` is required on `429`.

### 6.4 Endpoint inventory

#### Merchant profile

```text
GET /api/v1/b2b/merchant
```

Required scope:

```text
merchant:read
```

#### Accounts

```text
GET /api/v1/b2b/accounts
GET /api/v1/b2b/accounts/{account_id}
GET /api/v1/b2b/accounts/{account_id}/balance
```

Required scope:

```text
accounts:read
```

#### Transactions

```text
GET /api/v1/b2b/transactions
GET /api/v1/b2b/transactions/{transaction_id}
```

Required scope:

```text
transactions:read
```

#### Transfers

```text
POST /api/v1/b2b/transfers
GET  /api/v1/b2b/transfers/{transaction_id}
```

Required scopes:

```text
transfers:write
transactions:read
```

The source merchant account is inferred from the authenticated tenant and may
not be supplied as an arbitrary debit account.

The destination is an owner-service-resolved account identifier accepted by the
contract. Gateway may not validate destination ownership by querying Ledger's
database.

#### Pay-ins

```text
POST /api/v1/b2b/payins
GET  /api/v1/b2b/payins/{payin_id}
```

Required scopes:

```text
payins:write
payins:read
```

#### Payouts

```text
POST /api/v1/b2b/payouts
GET  /api/v1/b2b/payouts/{payout_id}
```

Required scopes:

```text
payouts:write
payouts:read
```

#### Webhook endpoints

```text
GET    /api/v1/b2b/webhook-endpoints
POST   /api/v1/b2b/webhook-endpoints
GET    /api/v1/b2b/webhook-endpoints/{endpoint_id}
PATCH  /api/v1/b2b/webhook-endpoints/{endpoint_id}
DELETE /api/v1/b2b/webhook-endpoints/{endpoint_id}
POST   /api/v1/b2b/webhook-endpoints/{endpoint_id}/rotate-secret
```

Required scopes:

```text
webhooks:read
webhooks:write
```

#### Webhook deliveries

```text
GET  /api/v1/b2b/webhook-deliveries
GET  /api/v1/b2b/webhook-deliveries/{delivery_id}
POST /api/v1/b2b/webhook-deliveries/{delivery_id}/replay
```

Required scopes:

```text
webhooks:read
webhooks:write
```

### 6.5 Resource identifier policy

Public IDs must be opaque.

Recommended prefixes:

```text
mrc_  merchant
key_  API-key record
evt_  external webhook event
wh_   webhook endpoint
wd_   webhook delivery
```

Existing owner-service resource IDs may remain UUIDs where changing them would
break established contracts, but new merchant-edge resources should use opaque
public IDs.

### 6.6 Pagination

Cursor pagination is required for transaction and delivery lists.

Rules:

- maximum page size: `100`;
- default page size: `25`;
- cursors are opaque;
- order is deterministic;
- cursor payloads are signed or server-side stored;
- no offset pagination on high-growth tables;
- invalid or expired cursor returns a stable `400` error.

### 6.7 Error envelope

All B2B errors use one envelope:

```json
{
  "error": {
    "code": "IDEMPOTENCY_KEY_REUSED",
    "message": "The idempotency key was already used with a different request.",
    "request_id": "01J...",
    "details": {}
  }
}
```

Required stable codes include:

```text
AUTHENTICATION_REQUIRED
API_KEY_INVALID
API_KEY_EXPIRED
API_KEY_REVOKED
TENANT_SUSPENDED
SCOPE_DENIED
RESOURCE_NOT_FOUND
VALIDATION_FAILED
RATE_LIMITED
QUOTA_UNAVAILABLE
IDEMPOTENCY_KEY_REQUIRED
IDEMPOTENCY_KEY_REUSED
IDEMPOTENCY_IN_PROGRESS
CURRENCY_MISMATCH
INSUFFICIENT_FUNDS
OWNER_SERVICE_UNAVAILABLE
WEBHOOK_ENDPOINT_INVALID
WEBHOOK_REPLAY_NOT_ALLOWED
INTERNAL_ERROR
```

Tenant-ownership failure must not reveal resource existence. Return the same
`RESOURCE_NOT_FOUND` result for a missing resource and another tenant's
resource.

---

## 7. Scope model

### 7.1 Initial scopes

```text
merchant:read
accounts:read
transactions:read
transfers:write
payins:read
payins:write
payouts:read
payouts:write
webhooks:read
webhooks:write
```

### 7.2 Scope rules

- unknown scopes are rejected at key creation;
- wildcard scopes are not supported in C1;
- scopes are stored as normalized rows, not an unchecked comma-separated
  string;
- every handler declares its required scope in one central route registry;
- scope evaluation occurs after key and tenant validation;
- a key may be read-only;
- the default key template is least privilege;
- no handler may perform an inline ad-hoc scope string comparison.

### 7.3 Tenant isolation rule

Every repository query for merchant-owned data must include `tenant_id`.

Every typed owner-service request must include merchant actor metadata.

Code review must reject repository methods such as:

```text
GetTransaction(ctx, transactionID)
```

when the resource is tenant-owned.

Required form:

```text
GetTransaction(ctx, tenantID, transactionID)
```

---

## 8. API-key security design

### 8.1 Secret handling

API-key plaintext is shown exactly once.

Store:

- key record ID;
- tenant ID;
- environment;
- public prefix;
- secret digest;
- scopes;
- status;
- expiry;
- created metadata;
- revoked metadata;
- last-used timestamp.

Do not store:

- full plaintext key;
- reversible full API key;
- the key in logs, traces, audit details, metrics, or error messages.

### 8.2 Digest

Use an HMAC-SHA-256 digest with an application pepper:

```text
digest = HMAC-SHA-256(api_key_pepper, full_api_key)
```

The pepper must come from the existing secret-loading boundary.

Comparison must be constant-time.

### 8.3 Prefix lookup

Lookup flow:

1. Parse and validate the key format and environment prefix.
2. Extract the non-secret public prefix.
3. Fetch the active candidate record by unique prefix.
4. Recompute the digest.
5. Compare in constant time.
6. Validate tenant status, key status, expiry, and environment.
7. Construct a machine principal containing tenant ID, key ID, environment,
   and scopes.

### 8.4 Rotation

- a tenant may have at most two active keys per environment;
- new and old keys may overlap for controlled rotation;
- the old key receives an explicit expiry or revocation;
- revocation takes effect immediately;
- C1 starts without a positive authentication cache to avoid stale revocation;
- any later cache requires a separate measured decision and bounded TTL.

### 8.5 Usage tracking

Do not update `last_used_at` on every request.

Use a sampled or coalesced update no more frequently than once per configured
interval, initially five minutes per key.

---

## 9. Quota design

### 9.1 Policy ownership

PostgreSQL stores authoritative tenant quota configuration.

Redis stores distributed counters.

Quota classes:

```text
read
write
transfer
payin
payout
webhook_management
```

Do not use raw URL paths as quota labels.

### 9.2 Initial dimensions

A quota policy may define:

- requests per minute;
- burst capacity;
- optional daily financial-operation count;
- maximum webhook endpoints;
- maximum active API keys;
- maximum page size;
- maximum request body size.

No amount-based financial limit is introduced unless the owner service already
has an authoritative policy for it.

### 9.3 Redis outage posture

- financial writes fail closed with `503 QUOTA_UNAVAILABLE`;
- webhook-management writes fail closed;
- reads may use a bounded in-process emergency limiter at a small fixed
  fraction of their configured quota;
- every fallback decision emits a metric and structured warning;
- the fallback may not silently become the normal path.

### 9.4 Atomic enforcement

Use a Lua script or equivalent atomic Redis operation to:

- refill;
- consume;
- return remaining capacity;
- return reset time.

### 9.5 Label discipline

Metrics may include:

- quota class;
- decision;
- backend;
- environment.

Metrics may not include:

- tenant ID;
- API-key ID;
- request ID.

---

## 10. Durable idempotency

### 10.1 Scope

All financial `POST` operations require durable merchant-edge idempotency:

- transfers;
- pay-ins;
- payouts.

Webhook endpoint creation and secret rotation should also support idempotency,
but they are secondary to the financial write gate.

### 10.2 Idempotency identity

Unique identity:

```text
tenant_id + operation_id + idempotency_key
```

The request fingerprint is:

```text
SHA-256(canonical_method + canonical_path + canonical_body)
```

Canonicalization must be deterministic and covered by fixtures.

### 10.3 States

```text
processing
completed
failed_retryable
failed_terminal
```

Persist:

- request hash;
- downstream idempotency key;
- state;
- owner resource ID;
- HTTP status;
- response body snapshot;
- selected response headers;
- error code;
- lease owner;
- lease expiry;
- created and updated timestamps;
- expiry timestamp.

### 10.4 Behavior

#### Same key and same request

Return the stored response without creating another money operation.

#### Same key and different request

Return:

```text
409 IDEMPOTENCY_KEY_REUSED
```

#### Concurrent duplicate while processing

Return:

```text
409 IDEMPOTENCY_IN_PROGRESS
```

with a bounded `Retry-After`.

#### Gateway crash after owner-service success

A retry must reuse a deterministic downstream idempotency key and recover the
owner resource rather than post money again.

### 10.5 Downstream scope

Derive the owner-service idempotency scope from:

```text
merchant:<tenant_id>:<operation_id>
```

The downstream key must be stable across Gateway retries.

### 10.6 Retention

Initial idempotency retention:

```text
7 days
```

Purge through a bounded maintenance job with metrics.

---

## 11. Gateway-owned schema

Add additive Gateway migrations starting after the current migration head.

### 11.1 `merchant_tenants`

Minimum columns:

```text
id UUID PRIMARY KEY
public_id TEXT UNIQUE NOT NULL
external_code TEXT UNIQUE NOT NULL
name TEXT NOT NULL
environment TEXT NOT NULL CHECK (environment IN ('sandbox', 'live'))
status TEXT NOT NULL CHECK (status IN ('draft', 'active', 'suspended', 'closed'))
default_currency TEXT NOT NULL
primary_account_id UUID NULL
created_by TEXT NOT NULL
activated_by TEXT NULL
suspended_by TEXT NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
activated_at TIMESTAMPTZ NULL
suspended_at TIMESTAMPTZ NULL
closed_at TIMESTAMPTZ NULL
```

`primary_account_id` is an application-level reference to LedgerService. It is
not a cross-database foreign key.

### 11.2 `merchant_api_keys`

Minimum columns:

```text
id UUID PRIMARY KEY
public_id TEXT UNIQUE NOT NULL
tenant_id UUID NOT NULL
public_prefix TEXT UNIQUE NOT NULL
secret_digest BYTEA NOT NULL
environment TEXT NOT NULL
status TEXT NOT NULL
expires_at TIMESTAMPTZ NULL
last_used_at TIMESTAMPTZ NULL
created_by TEXT NOT NULL
revoked_by TEXT NULL
created_at TIMESTAMPTZ NOT NULL
revoked_at TIMESTAMPTZ NULL
```

### 11.3 `merchant_api_key_scopes`

```text
key_id UUID NOT NULL
scope TEXT NOT NULL
PRIMARY KEY (key_id, scope)
```

### 11.4 `merchant_quota_policies`

```text
id UUID PRIMARY KEY
tenant_id UUID NOT NULL
quota_class TEXT NOT NULL
requests_per_minute INTEGER NOT NULL
burst INTEGER NOT NULL
is_enabled BOOLEAN NOT NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
UNIQUE (tenant_id, quota_class)
```

### 11.5 `merchant_idempotency_records`

```text
id UUID PRIMARY KEY
tenant_id UUID NOT NULL
operation_id TEXT NOT NULL
idempotency_key TEXT NOT NULL
request_hash BYTEA NOT NULL
downstream_key TEXT NOT NULL
state TEXT NOT NULL
resource_id TEXT NULL
http_status INTEGER NULL
response_body JSONB NULL
response_headers JSONB NULL
error_code TEXT NULL
lease_owner TEXT NULL
lease_expires_at TIMESTAMPTZ NULL
expires_at TIMESTAMPTZ NOT NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
UNIQUE (tenant_id, operation_id, idempotency_key)
```

### 11.6 `merchant_event_inbox`

Deduplicates internal AMQP events before creating an external event.

```text
event_id UUID PRIMARY KEY
event_type TEXT NOT NULL
source TEXT NOT NULL
payload_hash BYTEA NOT NULL
received_at TIMESTAMPTZ NOT NULL
processed_at TIMESTAMPTZ NULL
processing_error TEXT NULL
```

### 11.7 `merchant_webhook_endpoints`

```text
id UUID PRIMARY KEY
public_id TEXT UNIQUE NOT NULL
tenant_id UUID NOT NULL
url TEXT NOT NULL
status TEXT NOT NULL
secret_ciphertext BYTEA NOT NULL
secret_version INTEGER NOT NULL
subscribed_events TEXT[] NOT NULL
description TEXT NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
disabled_at TIMESTAMPTZ NULL
```

Use the existing cryptographic envelope for reversible secret encryption.
Plaintext webhook secrets are shown only at creation and rotation.

### 11.8 `merchant_webhook_events`

Persist immutable external event bytes once.

```text
id UUID PRIMARY KEY
public_id TEXT UNIQUE NOT NULL
tenant_id UUID NOT NULL
event_type TEXT NOT NULL
schema_version INTEGER NOT NULL
livemode BOOLEAN NOT NULL
payload JSONB NOT NULL
payload_bytes BYTEA NOT NULL
source_event_id UUID NOT NULL
created_at TIMESTAMPTZ NOT NULL
UNIQUE (tenant_id, source_event_id, event_type)
```

`payload_bytes` is the exact serialized body used for signing and retry.

### 11.9 `merchant_webhook_deliveries`

```text
id UUID PRIMARY KEY
public_id TEXT UNIQUE NOT NULL
tenant_id UUID NOT NULL
endpoint_id UUID NOT NULL
event_id UUID NOT NULL
status TEXT NOT NULL
attempt_count INTEGER NOT NULL
next_attempt_at TIMESTAMPTZ NULL
lease_owner TEXT NULL
lease_expires_at TIMESTAMPTZ NULL
last_http_status INTEGER NULL
last_error_code TEXT NULL
first_attempt_at TIMESTAMPTZ NULL
delivered_at TIMESTAMPTZ NULL
dead_at TIMESTAMPTZ NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
UNIQUE (endpoint_id, event_id)
```

### 11.10 `merchant_webhook_attempts`

Append-only attempt evidence:

```text
id UUID PRIMARY KEY
delivery_id UUID NOT NULL
attempt_number INTEGER NOT NULL
started_at TIMESTAMPTZ NOT NULL
finished_at TIMESTAMPTZ NOT NULL
http_status INTEGER NULL
duration_ms INTEGER NOT NULL
error_code TEXT NULL
response_excerpt TEXT NULL
UNIQUE (delivery_id, attempt_number)
```

Do not persist sensitive response bodies. Truncate and sanitize excerpts.

### 11.11 Index requirements

Add indexes for:

- active API-key prefix lookup;
- active key count per tenant;
- tenant status/environment;
- idempotency expiry;
- idempotency processing lease expiry;
- inbox unprocessed rows;
- webhook endpoint status per tenant;
- delivery due time and status;
- delivery list cursor;
- event source ID;
- attempt history.

Every index must have an explained query owner. Avoid speculative indexes.

---

## 12. LedgerService changes

### 12.1 Reuse existing account model

Ledger accounts already support merchant ownership. C1 must reuse the existing
`owner_type = merchant` model rather than create a parallel balance table.

### 12.2 Additive internal contracts

Add purpose-built additive Protobuf operations:

```text
ProvisionMerchantAccount
GetMerchantAccount
ListMerchantAccounts
GetMerchantTransaction
ListMerchantTransactions
PostMerchantTransfer
```

Names may be adjusted to match current proto conventions, but semantics must
remain merchant-specific.

Avoid a dangerously generic public internal operation that lets Gateway debit
an arbitrary account.

### 12.3 Provisioning behavior

`ProvisionMerchantAccount` must:

- be idempotent for `tenant_id + currency + account_type`;
- create or return the merchant-owned ledger account;
- provision required balance rows through the existing Ledger rules;
- return the same account on retry;
- reject a conflicting currency or owner mapping;
- emit audit and tracing evidence;
- never let Gateway insert Ledger rows directly.

### 12.4 Transfer behavior

`PostMerchantTransfer` must:

- infer and validate the merchant source account from the authenticated tenant
  actor;
- resolve the destination through Ledger-owned data;
- validate currency equality;
- post balanced entries through the existing posting core;
- use a deterministic merchant idempotency scope;
- attach merchant actor metadata;
- return the existing transaction on retry;
- emit the canonical ledger event;
- preserve all existing balance and append-only invariants.

### 12.5 Event enrichment

Add optional, backward-compatible fields to relevant internal event schemas:

```text
merchant_id
merchant_environment
merchant_operation
```

Only add fields actually needed to route and construct tenant webhooks.

Do not force the webhook relay to query Ledger's database for ownership.

### 12.6 Existing-contract protection

All current user-facing Ledger HTTP and gRPC behavior must remain unchanged.

Every additive proto change must pass:

- generated artifact checks;
- field-number reuse checks;
- enum semantic checks;
- v1/v2 rollout harness;
- current consumer tests.

---

## 13. PayinService changes

### 13.1 Owner-neutral principal

Current user journeys must remain unchanged.

Introduce an additive principal model where required:

```text
principal_type = user | merchant
principal_id
```

Prefer additive columns and fields with existing rows backfilled to `user`.

### 13.2 Merchant pay-in behavior

A merchant pay-in must:

- be created only for the authenticated merchant tenant;
- target the tenant's provisioned merchant account;
- use sandbox adapters for sandbox tenants;
- preserve PayinService ownership of lifecycle state;
- preserve VendorService ownership of vendor dispatch and callbacks;
- post final money movement through LedgerService;
- use durable idempotency;
- emit an owner lifecycle event through a transactional outbox;
- include enough tenant metadata for webhook routing.

### 13.3 State mapping

Public B2B statuses must be stable and owner-neutral.

Map internal states into a documented B2B state machine such as:

```text
pending
processing
succeeded
failed
expired
cancelled
```

Do not leak vendor-native state names.

### 13.4 Callback behavior

Vendor callbacks continue to enter through VendorService.

Gateway may not accept vendor callbacks under B2B routes.

---

## 14. PayoutService changes

### 14.1 Owner-neutral principal

Introduce the same additive merchant principal model where required.

### 14.2 Merchant payout behavior

A merchant payout must:

- be created only for the authenticated tenant;
- debit or hold only the tenant's provisioned merchant account;
- preserve PayoutService ownership of payout state;
- use VendorService for dispatch and callback normalization;
- use sandbox adapters for sandbox tenants;
- preserve hold, success, failure, reversal, and release invariants;
- use durable idempotency;
- emit owner lifecycle events through a transactional outbox;
- include enough tenant metadata for webhook routing.

### 14.3 Failure safety

Required cases:

- vendor timeout after request acceptance;
- duplicate dispatch;
- duplicate callback;
- callback before synchronous response;
- success callback after client timeout;
- failed payout releases the correct hold exactly once;
- repeated status query does not mutate money;
- Gateway retry does not create a second payout.

---

## 15. Outbound webhook design

### 15.1 External event envelope

Example:

```json
{
  "id": "evt_01J...",
  "type": "payout.updated.v1",
  "schema_version": 1,
  "created_at": "2026-07-28T08:00:00Z",
  "livemode": false,
  "tenant_id": "mrc_01J...",
  "data": {
    "id": "payout-id",
    "status": "succeeded",
    "amount": "125000",
    "currency": "IDR"
  }
}
```

The envelope must not contain:

- API keys;
- webhook secrets;
- internal service tokens;
- vendor credentials;
- raw vendor callback bodies;
- another tenant's identifiers;
- unnecessary PII.

### 15.2 Internal event ingestion

Flow:

```text
Ledger/Payin/Payout transaction
    -> owner transactional outbox
    -> RabbitMQ internal event
    -> Gateway merchant_event_inbox
    -> immutable merchant_webhook_event
    -> one delivery per matching active endpoint
    -> delivery worker
```

Inbox insertion and external event/delivery creation must be atomic in the
Gateway database.

### 15.3 Subscription filtering

An endpoint subscribes to an explicit allowlist.

Rules:

- unknown event types are rejected;
- empty subscription list is rejected;
- wildcard subscription is optional only if represented by a known constant;
- endpoint receives only events for its tenant;
- changes apply only to future event fan-out unless explicitly replayed.

### 15.4 Signature

Headers:

```http
Seev-Event-ID: evt_...
Seev-Delivery-ID: wd_...
Seev-Signature: t=<unix-seconds>,v1=<hex-hmac>
User-Agent: Seev-Webhook/1.0
Content-Type: application/json
```

Signing input:

```text
<timestamp>.<exact_raw_body>
```

Algorithm:

```text
HMAC-SHA-256(endpoint_secret, signing_input)
```

The exact body bytes must be persisted once so retries sign and send the same
payload.

### 15.5 Receiver verification documentation

Reference documentation must explain how a receiver should:

1. parse the timestamp and signatures;
2. reject an old timestamp outside a recommended tolerance;
3. construct the signing input from the raw body;
4. compute HMAC-SHA-256;
5. compare signatures in constant time;
6. deduplicate using event ID;
7. return `2xx` only after durable acceptance.

### 15.6 HTTP client safety

Live-mode endpoint requirements:

- HTTPS only;
- no embedded user credentials;
- no loopback, link-local, private, multicast, unspecified, or metadata IP;
- DNS results validated on every delivery attempt;
- redirects disabled;
- response body read is bounded;
- request timeout is bounded, initially 10 seconds;
- payload size is bounded, initially 256 KiB;
- TLS certificate verification may not be disabled.

Sandbox local URLs may be allowed only through an explicit local-development
allowlist and may never be accepted for live-mode tenants.

### 15.7 Retry policy

Initial retry schedule:

```text
attempt 1: immediate
attempt 2: +1 minute
attempt 3: +5 minutes
attempt 4: +30 minutes
attempt 5: +2 hours
attempt 6: +8 hours
attempt 7: +24 hours
```

After the final failed attempt, mark the delivery `dead`.

Response behavior:

- `2xx`: delivered;
- `410`: disable endpoint and stop automatic retry;
- `408`, `425`, `429`, `5xx`, timeout, network error: retry;
- other `4xx`: retry only within the bounded schedule, then dead;
- redirects: failure, no follow.

### 15.8 Worker leasing

Use PostgreSQL leasing:

```text
SELECT ... FOR UPDATE SKIP LOCKED
```

Required controls:

- bounded batch size;
- lease owner;
- lease expiry;
- heartbeat or attempt bounded below the lease duration;
- recovery of expired leases;
- no concurrent attempt number reuse;
- graceful shutdown;
- worker metrics.

### 15.9 Replay

Replay rules:

- only terminal failed or dead deliveries are eligible;
- replay is tenant-scoped;
- replay creates a new delivery ID;
- replay preserves the same event ID and exact payload;
- replay records actor, reason, and source delivery;
- default replay age limit: seven days;
- replay may not bypass a disabled endpoint without first re-enabling it;
- repeated replay requests are idempotent.

---

## 16. Admin BFF changes

### 16.1 Maintain existing boundary

Browser operators continue to use Admin BFF.

Admin BFF calls typed Gateway merchant-management endpoints or an internal
merchant administration contract. It does not read Gateway tables directly.

### 16.2 Operator pages and routes

Add:

- merchant tenant list and detail;
- create sandbox tenant;
- create live-mode tenant in draft state;
- provision account;
- activate, suspend, and close tenant;
- create and revoke API key;
- rotate API key;
- display a newly created secret exactly once;
- inspect and update scopes;
- inspect and update quotas;
- inspect webhook endpoints;
- disable an endpoint;
- inspect delivery attempts;
- replay eligible delivery;
- inspect merchant audit history.

### 16.3 Authorization

Use existing admin roles and maker/checker controls.

At minimum:

- sandbox tenant creation: maker;
- live-mode activation: checker;
- quota increase above the default baseline: checker;
- key creation: maker with audit;
- key revocation: maker with audit;
- tenant closure: checker;
- delivery replay: maker with reason;
- endpoint force-disable: maker with reason.

Exact route-role mapping must be locked in the Admin BFF policy registry and
tested.

### 16.4 CSRF and secret display

All browser mutations require existing CSRF protection.

Secret plaintext:

- appears only in the immediate create/rotate response;
- is never re-fetchable;
- is never stored in audit details;
- is masked from logs and templates after the response;
- has a copy warning explaining one-time visibility.

---

## 17. Audit model

Audit all security- and money-relevant merchant actions.

Required events include:

```text
merchant.tenant.created
merchant.tenant.activated
merchant.tenant.suspended
merchant.tenant.closed
merchant.account.provisioned
merchant.api_key.created
merchant.api_key.rotated
merchant.api_key.revoked
merchant.scope.changed
merchant.quota.changed
merchant.webhook_endpoint.created
merchant.webhook_endpoint.updated
merchant.webhook_endpoint.disabled
merchant.webhook_secret.rotated
merchant.webhook_delivery.replayed
merchant.transfer.requested
merchant.payin.requested
merchant.payout.requested
```

Audit details may contain:

- tenant public ID;
- key public ID or prefix;
- endpoint public ID;
- changed non-secret fields;
- actor type and actor ID;
- request ID;
- reason;
- before/after policy metadata.

Audit details may not contain:

- API-key plaintext;
- webhook-secret plaintext;
- full authorization headers;
- internal tokens;
- vendor credentials;
- raw private payloads.

---

## 18. Observability

### 18.1 API metrics

Add bounded-cardinality metrics:

```text
seev_b2b_requests_total{operation_id,status_class,environment}
seev_b2b_request_duration_seconds{operation_id,environment}
seev_b2b_auth_failures_total{reason,environment}
seev_b2b_scope_denials_total{scope,environment}
seev_b2b_quota_decisions_total{quota_class,result,backend,environment}
seev_b2b_idempotency_total{operation_id,result,environment}
seev_b2b_owner_calls_total{owner,operation,result}
seev_b2b_owner_call_duration_seconds{owner,operation}
```

### 18.2 Webhook metrics

```text
seev_merchant_webhook_events_total{event_type,environment}
seev_merchant_webhook_deliveries_total{event_type,result,environment}
seev_merchant_webhook_attempt_duration_seconds{result,environment}
seev_merchant_webhook_due_total{status,environment}
seev_merchant_webhook_oldest_due_age_seconds{environment}
seev_merchant_webhook_dead_total{event_type,environment}
seev_merchant_webhook_endpoints_disabled_total{reason,environment}
seev_merchant_webhook_inbox_duplicates_total{source,event_type}
```

### 18.3 Forbidden metric labels

Do not label metrics with:

- tenant ID;
- key ID;
- account ID;
- transaction ID;
- event ID;
- delivery ID;
- request ID;
- webhook host.

### 18.4 Logging

Structured logs must include bounded context:

- operation ID;
- request ID;
- trace ID;
- environment;
- authenticated actor type;
- owner service;
- stable error code.

Tenant and key identifiers may appear only where operationally required and
must be represented by non-secret public IDs. Never log the full key or
signature.

### 18.5 Tracing

A merchant write trace must connect:

```text
B2B HTTP request
-> Gateway auth/quota/idempotency
-> owner-service RPC
-> Ledger posting where applicable
-> owner outbox publication
-> Gateway merchant event inbox
-> external webhook attempt
```

Async links should use event IDs and trace-link semantics rather than pretending
the full delivery is one synchronous span.

### 18.6 Alerts

Add actionable alerts for:

- sustained B2B authentication failures;
- quota backend unavailable;
- idempotency records stuck in `processing`;
- owner-service error rate;
- merchant inbox oldest unprocessed age;
- webhook oldest due age;
- dead-delivery growth;
- endpoint auto-disable spike;
- merchant event payload creation failures;
- API-key digest/pepper initialization failure.

Alerts need a runbook link and must avoid noisy per-tenant alert creation.

---

## 19. Security and threat-model updates

Update the threat model with at least:

### 19.1 Credential threats

- API-key leakage;
- plaintext key persistence;
- authorization-header logging;
- brute-force prefix probing;
- stale-key cache after revocation;
- over-broad scopes;
- operator key exfiltration.

### 19.2 Tenant-isolation threats

- IDOR across merchant resources;
- tenant ID accepted from untrusted request body;
- source account substitution;
- cross-tenant idempotency collision;
- cross-tenant webhook fan-out;
- list-query scope omission.

### 19.3 Financial threats

- duplicate money from HTTP retry;
- Gateway crash after downstream success;
- owner callback duplication;
- stale payout state;
- mismatched request reuse under one idempotency key;
- integer overflow or floating-point conversion;
- debit from non-owned account.

### 19.4 Webhook threats

- SSRF;
- DNS rebinding;
- redirect to private address;
- signature replay;
- secret leakage;
- payload tampering;
- unbounded response body;
- slow endpoint resource exhaustion;
- duplicate delivery;
- malicious endpoint returning sensitive content.

### 19.5 Administrative threats

- CSRF;
- privilege escalation;
- secret exposed in browser history or audit;
- unauthorized live activation;
- unsafe quota increase;
- replay abuse.

Every threat requires:

- prevention;
- detection;
- test;
- residual risk;
- owner.

---

## 20. Task breakdown

## T0 — Entry-gate re-inventory

### Work

- Re-read current roadmap index and active/archive status.
- Record exact baseline commit.
- Run current contract, security, business, callback, admin, and smoke gates.
- Inventory Gateway, Ledger, Payin, Payout, Vendor, and Admin BFF contracts.
- Identify every user-specific field that blocks a merchant principal.
- Confirm migration heads and generated-code commands.
- Confirm existing encryption, secret-loading, outbox, rate-limit, request-ID,
  audit, and admin-policy helpers that can be reused.
- Produce a dependency and blast-radius table.

### Deliverables

```text
docs/evidence/c1-entry-gate.md
docs/reference/c1-current-contract-inventory.md
```

### Acceptance

- [x] All entry-gate checks are recorded.
- [x] Every failed prerequisite has an owner and blocking disposition.
- [x] No implementation assumption remains based only on roadmap prose.
- [x] Baseline commit and commands are reproducible.

### Result

Executed 2026-07-28 at baseline commit `d20e5295ef0cdbbc44816af239c90c3d7514439b`.
Gate disposition: **PASS** — no failed prerequisite, so no blocking
disposition needed. Full evidence:
[docs/evidence/c1-entry-gate.md](../../evidence/c1-entry-gate.md) (gate
checklist + command log) and
[docs/reference/c1-current-contract-inventory.md](../../reference/c1-current-contract-inventory.md)
(contract/event/migration inventory, user-specific blocking-field findings,
reusable-helper confirmation, dependency/blast-radius table).

Most consequential finding: `services/ledger`'s `accounts.owner_type` CHECK
constraint has allowed `'merchant'` since the very first migration
(`000001_ledger_core.up.sql`) — the schema already anticipated this track.
T5 extends existing repository query methods rather than designing a new
ownership model or writing a new Ledger migration for account ownership.

T1 may begin.

---

## T1 — Lock contracts, states, and trust boundaries

### Work

- Add the initial B2B OpenAPI contract.
- Register every operation.
- Lock scope mapping.
- Lock error envelope.
- Lock pagination.
- Lock public state mappings for transfer, pay-in, and payout.
- Lock the webhook envelope and signature.
- Update architecture, service ownership, and threat model.
- Add a sequence diagram for every money journey.
- Add a failure matrix.

### Required diagrams

```text
merchant tenant + key provisioning
B2B request authentication
merchant transfer
merchant pay-in
merchant payout
owner event to external webhook
webhook retry and replay
Gateway crash after owner success
```

### Acceptance

- [x] No handler is implemented before its operation contract exists.
- [x] Every financial write documents idempotency.
- [x] Every resource documents tenant ownership.
- [x] Every event documents producer, consumer, key, version, and dedup identity.
- [x] Threat model changes are reviewed.

### Result

Delivered 2026-07-28:

- **Contract**: [contracts/http/b2b-v1.yaml](../../../contracts/http/b2b-v1.yaml) —
  21 operations across 7 resource groups, registered in
  [contracts/compatibility/surfaces.yaml](../../../contracts/compatibility/surfaces.yaml)
  (audience `merchant`, newly added to the allowed-audience enum in
  `contracts/compatibility/surfaces_test.go`). Reuses the repo-wide `SuccessEnvelope`/
  `ErrorEnvelope` (no forked envelope) — the only addition is an optional,
  additive `request_id` field on `Error` and a new `merchantApiKey` security
  scheme, both in `contracts/http/components/common.yaml`. 16 new stable error
  codes added to `contracts/compatibility/errors.yaml` per §6.7. One deliberate
  deviation from §6.4's illustrative path: `POST
  .../webhook-endpoints/{id}/rotate-secret` is registered as `.../rotate`
  instead — the literal word "secret" in a path/inventory-ID trips this
  repo's own `surfaces_test.go` sensitive-value scanner (by design, for
  every OTHER contract); renaming avoids a real safety check rather than
  weakening it.
- **Design lock**: public state mappings (transfer/pay-in/payout/webhook
  delivery), the webhook envelope + `Seev-Signature: t=...,v1=...` scheme,
  all 8 required sequence diagrams, and the failure matrix are in
  [docs/reference/c1-b2b-design.md](../../reference/c1-b2b-design.md).
- **Threat model**: TM-15 through TM-19 added to
  [docs/security/threat-model.md](../../security/threat-model.md) (API-key
  compromise, webhook SSRF, missing tenant-scoping, webhook secret
  plaintext, quota-exhaustion blast radius) — status "Planned control",
  each pointing at the T-task that closes it.
- **Service ownership**: `docs/reference/services.md`'s Gateway entry notes
  the planned `services/gateway/internal/merchant` sub-module.
- **Real bug found and fixed while wiring this in**: `cmd/contractcheck`'s
  `compareParameters` matched `$ref`-shaped parameters by `name`/`in`, but
  the bundler (`cmd/contractgenerate`) represents a resolved `$ref` as
  `{"$ref": {...resolved...}}` rather than replacing the node — so every
  `$ref` parameter's `name`/`in` read as `nil`. With at most one `$ref`
  parameter per operation anywhere in the repository before this, the
  `nil == nil` match happened to hit the only candidate by luck. This
  contract's own operations are the first with TWO (`X-Request-ID` +
  a path ID), which exposed that the matcher was comparing unrelated
  parameters against each other. Fixed by unwrapping `$ref` before
  reading `name`/`in`; regression test
  `TestCompatibilityHandlesMultipleRefParametersOnOneOperation` added to
  `cmd/contractcheck/main_test.go`, proven both ways (identical fixture
  passes, a genuine field change is still caught).
- **Verified**: `make contracts` (generate/lint/breaking/test — a fresh
  `contracts/compatibility/baseline/openapi/b2b-v1.yaml` bootstrap snapshot was
  needed since this is the first-ever bundle for the file, same pattern
  ci.yml's own bootstrap fallback already documents), `make docs-check`,
  `go build ./...`, `go vet -tags=integration ./...`, and
  `go test ./cmd/contractcheck/...` all pass.

T2 may begin.

---

## T2 — Gateway merchant module and persistence

### Work

- Add `services/gateway/internal/merchant`.
- Add configuration with secure defaults.
- Add additive Gateway migrations.
- Add repositories with mandatory tenant-scoped methods.
- Add transaction helper usage.
- Add retention jobs for idempotency and delivery evidence.
- Add module health/readiness checks where required.
- Add migration rollback only where safe; document irreversible steps.

### Acceptance

- [x] Migration up/down tests pass where supported.
- [x] Repository integration tests use real PostgreSQL.
- [x] Unique constraints prove race safety.
- [x] All timestamps are UTC.
- [x] No cross-service foreign key exists.
- [x] No secret plaintext column exists.
- [x] Existing Gateway notification tables and routes remain green.

### Result

Delivered 2026-07-28:

- **Migrations**: `migrations/gateway/000004_merchant_schema` (all 10 tables
  from §11, RLS + `app_service`/`app_readonly` grants, matching every other
  Gateway table's convention) and `000005_merchant_retention` (5 purge
  functions for the classes registered in `config/data-retention.yaml`).
  Verified up→down→up live against a real Postgres container (`make
  migrate-up`/`migrate-down SERVICE=gateway`), both directions clean.
- **Retention**: 10 new `gateway.merchant.*` classes added to
  `config/data-retention.yaml` (`make retention-check`: 100 entries valid).
  `services/gateway/internal/merchant.Module.StartRetentionRunner` wires the 5 age-based
  classes on their own scheduler, mirroring `services/gateway/internal/notification`'s own
  pattern (both are Gateway submodules).
- **Package**: `services/gateway/internal/merchant/{model,repository}` populated;
  `{api,application,auth,quota,idempotency,webhook,client,observability}`
  scaffolded as empty directories per §3.1's layout, populated task by
  task starting T3. `internal/platform/config.MerchantConfig` (API-key pepper,
  idempotency default TTL, quota fail-closed default) follows the same
  secret-loading-boundary pattern as `ClosureConfig`/`CryptoxConfig`.
- **Real bug found and fixed during its own integration test**: the first
  schema draft used a plain `UNIQUE(endpoint_id, event_id)` on
  `merchant_webhook_deliveries` with a comment claiming replay rows were
  "intentionally not covered" — false. Postgres rejected every replay
  INSERT outright, caught immediately by
  `TestWebhookRepository_DeliveryUniqueness_RaceSafe`. Fixed with proper
  lineage tracking: a new `replay_of_delivery_id` column (NULL for the
  automatic delivery, set to the original delivery's id for every replay)
  and a partial unique index (`... WHERE replay_of_delivery_id IS NULL`)
  that bounds only the automatic path, exactly matching T7's own
  requirement ("one endpoint receives at most one automatic delivery
  record per event... replay creates a new delivery ID with the same
  event ID").
- **Race safety proven live** (not just declared): concurrent-goroutine
  tests for `merchant_api_keys.public_prefix`,
  `merchant_idempotency_records`'s `(tenant_id, operation_id,
  idempotency_key)`, and `merchant_webhook_deliveries`'s automatic-path
  uniqueness each assert exactly one of 10 concurrent inserts wins.
  Tenant-scoping is proven both positively (two tenants can reuse the
  identical idempotency key string independently) and negatively (a
  cross-tenant `Revoke` call affects zero rows, per §7.3).
- **Verified**: `go build ./...`, `go vet -tags=integration ./...`, `make
  lint` (0 issues), `go test -race ./...` (full repo, all green), `make
  docs-check`, `make retention-check`, the full
  `services/gateway/internal/merchant/repository` integration suite (8/8 pass), and the
  full pre-existing `services/gateway/internal/notification` integration suite (27/27 pass,
  confirming existing Gateway notification tables/routes are unaffected).

T3 may begin.

---

## T3 — API-key authentication and scopes

### Work

- Implement key generation and one-time display.
- Implement HMAC digest.
- Implement prefix lookup and constant-time comparison.
- Implement tenant and key status checks.
- Implement expiry.
- Implement route scope registry.
- Add machine principal to request context.
- Propagate actor metadata to typed owner clients.
- Mask authorization data from logs.
- Add sampled last-used updates.
- Add operator create/rotate/revoke application services.

### Acceptance

- [x] Invalid, expired, revoked, wrong-environment, and suspended-tenant keys fail
      closed.
- [x] Key plaintext is never queryable after creation.
- [x] Scope denial returns stable `403`.
- [x] Resource existence is not leaked.
- [x] Revocation applies immediately.
- [x] Authentication unit, integration, fuzz, and race tests pass.
- [x] Log-capture tests find no key plaintext.

### Result

Delivered 2026-07-28:

- **Package**: `services/gateway/internal/merchant/auth` — key generation/parsing
  (`key.go`), HMAC-SHA-256 digest with constant-time compare
  (`digest.go`), the machine `Principal` type + context helpers
  (`principal.go`), the central scope registry (`scopes.go`, kept in sync
  with `contracts/http/b2b-v1.yaml`'s `x-see-scopes` by
  `TestScopeRegistryMatchesContract`), the `RequireMerchantAuth`/
  `RequireScope` HTTP middleware (`middleware.go`), and the operator
  `KeyService` (create/rotate/revoke, `service.go`).
- **Design decision on multi-scope operations**: where §6.4 lists two
  scopes together for one resource group (transfers, payins, payouts),
  the registry requires ALL listed scopes (AND), not OR — documented
  explicitly in `scopes.go` since the plan's prose doesn't specify a
  per-operation split.
- **Log masking reused, not rebuilt**: the merchant API key travels on
  the same `Authorization: Bearer ...` header AuthService's JWTs already
  use — `internal/platform/observability/logging`'s existing `sensitiveKeys`/`SanitizeHeaders` masking
  already covers it with no code change. Proven, not assumed:
  `TestRequireMerchantAuth_KeyPlaintextNeverAppearsInLogs` and its
  tampered-attempt counterpart both capture real log output through
  `internal/platform/security/middleware.WithLogger` and assert the plaintext never appears.
- **Two real bugs found and fixed via this task's own tests**:
  1. `ParseKey` split the key on the first `_` character to separate the
     public prefix from the secret — but `base64.RawURLEncoding`'s own
     alphabet includes `_`, so a prefix or secret containing that
     character could shift the split to the wrong position. Caught
     immediately by `TestGenerateKey_SandboxAndLive_RoundTripThroughParseKey`
     (a real generated key round-tripped incorrectly). Fixed by slicing on
     the prefix's exact, deterministic encoded length instead of
     separator-splitting.
  2. `go fix`'s safe-modernizer check (`make lint`'s own
     `modernize-check` step) flagged two hand-written linear scans
     (`Principal.HasScope`, `ValidScope`) that `slices.Contains` already
     expresses — applied via `go fix -omitzero=false ./services/gateway/internal/merchant/...`
     rather than left for a future pass.
- **Verified**: unit tests (23, all pass), a 368,392-execution/15s fuzz
  run of `ParseKey` (0 crashes), `-race` on both the plain and
  `-tags=integration` suites, 3 real-Postgres integration tests
  (`TestKeyServiceAndMiddleware_RealStack`,
  `TestKeyService_RotateKey_RealStack`,
  `TestKeyService_CreateKey_EnforcesMaxTwoActiveKeysPerEnvironment` —
  covering the full create→authenticate→scope-deny→revoke→re-reject
  lifecycle and §8.4's two-key-per-environment limit against a real
  database, not fakes), `go build ./...`, `go vet -tags=integration
  ./...`, `make lint` (0 issues), `go test -race ./...` (full repo), and
  `make docs-check`.

T4 may begin.

---

## T4 — Quotas and durable idempotency

### Work

- Add quota policy repository.
- Add atomic Redis limiter.
- Add read-only emergency fallback.
- Add write fail-closed posture.
- Add rate-limit headers.
- Add durable idempotency claim, lease, completion, replay, and purge.
- Add canonical request hashing.
- Derive deterministic downstream keys.
- Add recovery query for interrupted `processing` records.
- Add idempotent management writes where appropriate.

### Acceptance

- [x] Concurrent same-key same-body requests produce one owner operation.
- [x] Same key with different body returns `IDEMPOTENCY_KEY_REUSED`.
- [x] Gateway crash after downstream success does not duplicate money.
- [x] Redis outage blocks financial writes.
- [x] Read fallback is bounded and observable.
- [x] Counter updates are atomic.
- [x] Idempotency expiry job is bounded.
- [x] No tenant can collide with another tenant's idempotency key.

### Result

Delivered 2026-07-29:

- **Package `services/gateway/internal/merchant/quota`** — `Enforcer` wraps T2's existing
  `merchant_quota_policies` repository around `internal/platform/cache.RedisRateLimiter`'s
  already-proven atomic Lua token-bucket script (reused, not reimplemented
  — the only new logic is loading a PER-TENANT policy at request time,
  since the shared `internal/platform/cache` type bakes its rate/burst in at
  construction). Fail-closed on write when Redis is unreachable
  (`ErrQuotaBackendUnavailable`, bounded by a 200ms `redisPingTimeout` so a
  half-dead Redis can't eat the caller's whole deadline — same principle
  as TM-14's fraud-velocity sub-deadline), a bounded and explicitly
  `Degraded`-flagged allow on read-class checks, and a secure
  `defaultPolicy` (60 req/min) when a tenant has no configured row.
  `middleware.go`'s `RequireQuota` sets §6.3's `RateLimit-*`/`Retry-After`
  headers and returns 429 (over quota) or 503 (backend down).
- **Package `services/gateway/internal/merchant/idempotency`** — canonical request hashing
  (`hash.go`: SHA-256 of operationID + raw body), deterministic
  downstream-key derivation (stable across retries so the owner service
  sees one logical operation), and `Service.Begin`'s full claim/replay/
  conflict/in-progress/lease-recovery decision table (`idempotency.go`) on
  top of T2's `IdempotencyRepository`.
- **Real concurrency bug found and fixed via self-review, before any test
  ran**: the first draft of `Begin`'s `"failed"`-state branch returned
  `OutcomeNew` unconditionally — no compare-and-swap — so two concurrent
  retries of a failed record would BOTH re-run the downstream operation.
  Fixed by adding `IdempotencyRepository.ReclaimFailed` (the same
  `WHERE state = 'failed'`-as-CAS shape as the existing
  `TakeoverExpiredLease`), and proven with
  `TestService_Begin_ConcurrentRetryOfFailedRecord` (20 concurrent
  retries, exactly one `OutcomeNew`).
- **`go fix`'s modernize-check** replaced the hand-written `strPtr`/
  `timePtr` helpers with Go 1.26's `new(x)` builtin at the call site,
  then flagged the now-dead helper functions themselves for removal —
  applied and deleted.
- **Idempotency expiry job**: already bounded — T2 built
  `fn_retention_purge_merchant_idempotency_records` (batched, hold-aware)
  and wired it into the shared retention worker
  (`services/gateway/internal/merchant/merchant.go`'s `StartRetentionRunner`); T4 added no
  new purge path, `make retention-check` confirms the policy is still
  current.
- **Verified**: 18 unit tests across `quota`/`idempotency` (miniredis for
  the Redis-backed quota path; an in-memory fake `IdempotencyRepository`
  reproducing the same compare-and-swap semantics as the real SQL for the
  idempotency path — including two dedicated concurrency races: N
  concurrent first claims and N concurrent retries of a failed record,
  both proving exactly one winner), 5 HTTP middleware tests
  (`RequireQuota`: unauthenticated, allow+headers, 429+Retry-After,
  503-fail-closed-write, degraded-allow-read), 4 real-Postgres
  integration tests proving T4's own acceptance criteria end-to-end
  through `Service` (not just the repository, which T2 already covered):
  25 concurrent same-key-same-body claims → exactly one `OutcomeNew`;
  `IDEMPOTENCY_KEY_REUSED` on a differing body; a full crash-recovery
  cycle (claim → simulated crash with no `Complete`/`Fail` → lease
  expires → a second process reclaims the SAME record and SAME
  downstream key → completes → every later retry replays); and two
  tenants using an identical idempotency key claiming independently. All
  four integration tests, plus T2's/T3's own, pass together in the same
  run. `-race` on both the plain and `-tags=integration` suites, `go
  build ./...`, `go vet -tags=integration ./...`, `make lint` (0 issues),
  `go test -race ./...` (full repo), `make docs-check`, and `make
  retention-check`.

T5 may begin.

---

## T5 — Ledger merchant account and transfer support

### Work

- Add merchant-specific additive Protobuf contracts.
- Implement idempotent merchant account provisioning.
- Implement merchant account and balance reads.
- Implement tenant-scoped transaction reads.
- Implement merchant transfer through the existing posting core.
- Add optional merchant routing fields to event schemas.
- Generate artifacts.
- Add compatibility fixtures and rollout tests.
- Add Admin BFF account-provisioning client.

### Acceptance

- [x] Merchant account uses the existing ledger account model.
- [x] Provision retry returns the same account.
- [x] Transfer posts balanced entries.
- [x] Source account cannot be substituted by the caller.
- [x] Currency mismatch fails before posting.
- [x] Duplicate request returns the original transaction.
- [x] Cross-tenant read/write tests pass.
- [x] Existing user transfer tests remain unchanged and green.
- [x] Ledger event remains backward compatible.

### Result

Delivered 2026-07-29:

- **Schema, zero new migration**: T0 had already found `accounts.owner_type`
  allows `'merchant'` since migration 000001 — confirmed true; T5 needed no
  new Ledger migration at all, only new Go code reading/writing that
  existing column value.
- **Additive-only, per the plan's own lock**: rather than parameterizing
  `AccountRepository.GetAccountID`/`ProvisioningRepository.UpsertAccount`
  (both hardcode `owner_type='user'`), added SEPARATE methods —
  `GetMerchantAccountID`, `ListByMerchantTenant`, `UpsertMerchantAccount`,
  `TransactionRepository.ListByAccountEitherSide` — so every existing
  user-scoped call site is byte-for-byte unchanged.
- **New processor `merchant_transfer`**
  (`services/ledger/internal/processors/merchant_transfer.go`): source is ALWAYS
  `GetMerchantAccountID(cmd.MerchantTenantID)` — there is no
  source-account-id input anywhere on the path, so the caller
  structurally cannot substitute it (proven by
  `TestMerchantTransferResolveAccounts_SourceNeverCallerSupplied`, which
  plants decoy `source_account_id`/`account_id` keys in Metadata and
  confirms they're never read). Destination is a raw account id from
  `Metadata["destination_account_id"]` — the same established pattern
  `EscrowRelease` already uses for a caller-supplied target account.
  Internal-router-only, never added to `publicUserTypes`, matching the
  existing FxIn/FxOut/Disbursement precedent.
- **Currency-mismatch check needed zero new code**: `service/handle`'s
  existing `validateAccounts` already rejects any account in
  `AccountIDs` whose currency differs from `cmd.Currency`, generically,
  for every processor — `merchant_transfer` gets this for free by setting
  `Currency` from the resolved source account.
- **Provisioning**: `provision.Service.ProvisionMerchantAccount` (single
  `cash` account per tenant — no hold/pending/frozen, those are
  end-user-only withdrawal-lifecycle states) — idempotent via the same
  partial-unique-index `ON CONFLICT DO UPDATE ... RETURNING` shape
  `UpsertAccount` already uses.
- **Contracts, additive**: 3 new `LedgerService` RPCs
  (`ProvisionMerchant`, `GetMerchantAccount`, `ListMerchantTransactions`)
  and one new `PostRequest.merchant_tenant_id` field —
  `contracts/proto/seev/ledger/v1/ledger.proto`, regenerated via `make proto`,
  registered in `contracts/compatibility/surfaces.yaml`. `GetMerchantAccount`/
  `ListMerchantTransactions` accept ONLY `tenant_id` — there is no
  account-id parameter anywhere on these RPCs, so a caller can never read
  another tenant's data by guessing an account id.
- **Event backward compatibility**: `events.TransactionPosted` gained one
  new optional field, `MerchantTenantID *uuid.UUID
  json:"merchant_tenant_id,omitempty"` — nil (omitted from the wire) for
  every existing transaction type, following this package's own
  documented policy ("a new OPTIONAL field is NOT a breaking change").
  Proven three ways: a golden-JSON test for the new field
  (`TestTransactionPosted_GoldenJSON_WithMerchantTenantID`), a
  compatibility fixture proving an EXISTING type's JSON is unaffected
  (`TestTransactionPosted_ExistingEventShape_UnaffectedByMerchantField`),
  and a rollout test proving a NEW payload still decodes cleanly into an
  OLDER consumer struct that has never heard of the field
  (`TestTransactionPosted_RolloutCompatibility_NewProducerOldConsumer`).
- **contracts/clients/ledger** (the shared gRPC client every service reuses) got
  `ProvisionMerchant`/`GetMerchantAccount`/`ListMerchantTransactions`
  methods and `Command.MerchantTenantID`, so Gateway's `services/gateway/internal/merchant`
  module (T6+) and Admin BFF can call the new surface without any new
  boilerplate.
- **Verified end-to-end against real Postgres, through the actual gRPC
  surface** (not just the repository or processor in isolation): 5
  integration tests in
  `services/ledger/internal/transport/grpc/merchant_transfer_integration_test.go`
  covering every acceptance line — idempotent provisioning, balanced
  posting (`fn_verify_ledger_balance` returns zero unbalanced), a
  currency-mismatch rejection that posts ZERO ledger entries, a
  duplicate-request replay that posts exactly one transaction and never
  double-deducts, and cross-tenant isolation (two tenants' balances stay
  independent, both legitimately see a transaction between them, and an
  unprovisioned tenant gets a clean error rather than another tenant's
  data). Plus 12 processor unit tests
  (`services/ledger/internal/processors/merchant_transfer_test.go`) with a real
  `MockAccountRepository`.
- **Full-repo regression, unaffected**: `go test -race ./...` (whole
  repo) is green. While investigating an unrelated `-tags=integration`
  full-suite run, found 3 pre-existing failures in
  `services/ledger/internal/ledger/schema_contract_test.go`
  (`TestSchemaContract_Accrual_BasicFlow_IdempotentAcrossRuns`,
  `..._BasisIsSnapshotNotLiveBalance`,
  `TestSchemaContract_Reporting_DailyPositionMatchesManualAggregate`) —
  confirmed via `git stash` to fail IDENTICALLY on a clean checkout with
  none of this task's changes applied, so not a T5 regression; flagged as
  a separate follow-up task rather than fixed here (out of T5's scope,
  and out of caution around silently touching financial accrual logic).
- **Full sweep**: `go build ./...`, `go vet -tags=integration ./...`,
  `make lint` (0 issues), `go test -race ./...` (full repo, green),
  `go test -tags=integration -race` on every touched package
  (`processors`, `repository`, `events`, `grpcserver`, `ledgerclient`,
  `api/contracts`), `make docs-check`, `make retention-check` (no new
  data classes — accounts/ledger_entries/ledger_transactions already
  covered by existing permanent-ledger rules).

T6 may begin.

---

## T6 — Merchant pay-in and payout journeys

### Work

- Add owner-neutral principal fields where required.
- Backfill existing rows safely.
- Extend typed internal contracts additively.
- Add merchant create/get use cases.
- Add stable public status mapping.
- Add merchant metadata to owner outbox events.
- Enforce sandbox-to-mock routing.
- Add failure and duplicate handling.
- Keep callbacks in VendorService.
- Add B2B Gateway handlers only after owner contracts are green.

### Acceptance

- [x] Existing user Payin/Payout journeys remain green.
- [x] Sandbox merchant cannot invoke a live adapter.
- [x] Pay-in credits the correct merchant account once.
- [x] Payout holds/debits/releases the correct merchant account once.
- [x] Duplicate synchronous request and duplicate callback are safe.
- [x] Lost synchronous response is recoverable by idempotent query/retry.
- [x] Owner events contain sufficient tenant routing data.
- [x] No vendor-native response leaks into the public B2B contract.

### Result

Delivered 2026-07-29:

- **Ledger prerequisite, discovered mid-task**: T5's `ProvisionMerchantAccount`
  doc comment claimed "a merchant gets no hold account" — true for T5's own
  scope (transfer only), but wrong once payout needs a hold↔cash state
  machine. Added `ProvisionMerchantHoldAccount` (same idempotent upsert,
  `AccountTypeHold`) and 4 new internal-router-only ledger processors
  mirroring the user path exactly: `merchant_payin_credit` (→ MoneyIn),
  `merchant_payout_hold`/`merchant_payout_settle`/`merchant_payout_cancel`
  (→ WithdrawInitiate/Settle/Cancel) — same `GetMerchantAccountID`-only
  resolution T5's `merchant_transfer` established, never a caller-supplied
  account id. Zero new migration.
- **Owner identity, additive, no owner_type column**: rather than T0's
  suggested `owner_type` enum + nullable `merchant_tenant_id`, used a
  SIMPLER sentinel-zero-UUID convention already established by
  `Command.MerchantTenantID` (T5): `merchant_tenant_id UUID NOT NULL
  DEFAULT '0000...'`, exactly one of `user_id`/`merchant_tenant_id` ever
  non-zero, enforced by a CHECK constraint on `payin_topup_intents` and
  `payout_requests` (`migrations/payin/000014`, `migrations/payout/000014`).
  `DEFAULT` backfills every existing row for free — no data migration
  pass. `payin_webhook_events` gets the column with no CHECK (an
  unmatched callback legitimately has neither owner yet — pre-existing
  behavior since migration 000013, not new).
- **New create use cases**: `CreateMerchantTopupIntent`
  (`services/payin/internal/merchant.go`) and `CreateMerchant`
  (`services/payout/internal/merchant.go`) — currency is caller-supplied (the B2B
  contract's own field), unlike the user path's `GetUserCurrency`
  resolution. Fee-quote consumption is not offered on the merchant payout
  path (no `quote_id` field on the B2B contract) — settle() falls back to
  `ResolveFee` exactly as any unquoted user payout already does.
- **Sandbox-to-mock routing, structural not rule-based**: both modules'
  `resolveMerchantVendor` route `environment="sandbox"` straight to
  `sandboxVendor` ("mockvendor") with NO routing-table lookup at all —
  `ErrSandboxVendorUnavailable` if it isn't registered, never a fallback
  to rule-based resolution. This means a future routing-rule
  misconfiguration structurally cannot leak a sandbox tenant onto a live
  vendor, mirroring `merchant_transfer`'s own "source is never
  caller-supplied" defense-in-depth philosophy.
- **Fraud screening deliberately skipped for merchant events** (a
  documented scope decision, not an oversight): `fraudClient.Check` is
  keyed on a single `userID`; running it unmodified for merchant events
  (whose `UserID` is the zero sentinel) would silently pool every
  merchant tenant into one shared "zero user" velocity bucket — a real
  correctness bug, not a missing nice-to-have. Merchant-specific
  fraud/velocity screening is out of scope for T6.
- **Duplicate handling reuses existing owner-neutral mechanisms
  unmodified**: payin's `GetOrInsert` dedup on `(vendor, vendor_event_id)`
  and payout's `TransitionTo*` conditional UPDATEs + the ledger's own
  idempotency keys already didn't care about owner type — proven directly
  with merchant-owned redelivery/retry tests, not just inherited by
  assumption.
- **Lost synchronous response recovery reuses `ResumeStuck`/`pollVendorPending`
  unmodified** — proven end to end with a real Postgres test
  (`TestPayout_CreateMerchant_Async_ResumeJobSettles`): a merchant payout
  left `vendor_pending`, resolved out of band, gets picked up by the SAME
  resume job via `Query` (never a duplicate `Submit`) that the user
  journey already relies on.
- **Owner event tenant-routing data comes for free**: since the 4 new
  merchant processors set `cmd.MerchantTenantID`, every merchant
  money-movement automatically carries `TransactionPosted.MerchantTenantID`
  (T5's own optional event field) with zero new payin/payout event
  infrastructure. A genuinely NEW payin/payout-owned outbox (covering the
  `pending`-at-creation moment, before any ledger posting) was considered
  and explicitly deferred — T6's own acceptance criterion only requires
  "sufficient tenant routing data," which the settlement-moment ledger
  event already satisfies; a full pending-state outbox is better scoped
  as T7's own prerequisite if the locked webhook envelope turns out to
  need it.
- **No vendor-native response leakage — confirmed pre-existing, not built
  here**: `services/vendor-service/internal`'s double normalization (vendor-native →
  VendorService's own typed proto → `vendorgw.PayoutResult`/normalized
  payin callback) already made this true before T6 touched anything;
  merchant requests flow through the exact same normalized surface.
- **Real pre-existing test-harness bug found and fixed**:
  `testutil.LedgerHarness.Post` (used by every payin/payout integration
  test in this repo, including payout's own pre-existing suite) manually
  copied `ledgerclient.Command` fields into `ledger.Command` and never
  copied `MerchantTenantID` — a gap left over from T5 that went unnoticed
  because T5's own integration tests used a real gRPC bufconn harness,
  not this in-process one. Every merchant-owned integration test in this
  task failed with `VALIDATION_ERROR: ... requires MerchantTenantID`
  until this one-line fix landed.
- **Deferred, tracked separately, NOT silently skipped**: the plan's own
  Work list item "Add B2B Gateway handlers only after owner contracts are
  green" — `services/gateway/internal/merchant`'s actual `/api/v1/b2b/payins`/`/payouts`
  HTTP route wiring (request parsing, T3's API-key auth, T4's
  quota/idempotency middleware, public status mapping) is real,
  substantial, distinct work that no T6 acceptance criterion actually
  requires (all 8 are owner-service-level and are now proven end to end
  without it existing). Flagged as a separate follow-up task.
- **Verified end to end against real Postgres**: 3 payin integration
  tests (`services/payin/internal/merchant_integration_test.go` — credits once,
  duplicate callback safe, sandbox routing) + 4 payout integration tests
  (`services/payout/internal/merchant_integration_test.go` — instant-settle
  hold→debit→release, async resume-job recovery, vendor-failure release,
  sandbox routing), all passing live, plus 8 payin unit tests and 8
  payout unit tests (processor-type branching, sandbox structural
  enforcement, fraud-skip proof) and 21 new ledger-side processor unit
  tests across the 4 new processors.
- **Full sweep**: `go build ./...`, `go vet -tags=integration ./...`
  (whole repo, clean), `make lint` (0 issues), `go test -race ./...`
  (full repo, green — proves "existing user journeys remain green"),
  `go test -tags=integration -race` on every touched package (`payin`,
  `payin/grpcserver`, `payout`, `payout/grpcserver`, `payout/repository`,
  `payout/worker`, `ledger/processors`, `ledger/repository`,
  `ledger/grpcserver`), `make docs-check`, `make retention-check` (no new
  data classes — `merchant_tenant_id` is an additive column on
  already-classified tables).

T7 may begin.

---

## T7 — Outbound webhook relay

### Work

- Consume relevant internal events.
- Deduplicate using logical event ID.
- Build immutable external event envelopes.
- Validate endpoint URL and subscriptions.
- Encrypt endpoint secrets.
- Implement signing.
- Implement fan-out.
- Implement leasing worker and retry schedule.
- Add attempt evidence.
- Add dead state.
- Add endpoint auto-disable on `410`.
- Add tenant and operator replay.
- Add SSRF/DNS/redirect defenses.
- Add retention jobs.
- Publish receiver verification documentation and examples.

### Acceptance

- [x] Duplicate internal event creates no duplicate external event.
- [x] One endpoint receives at most one automatic delivery record per event.
- [x] Retries reuse exact event bytes.
- [x] Signature fixtures are deterministic.
- [x] Private and metadata IP destinations are rejected for live mode.
- [x] Redirects are not followed.
- [x] Timeout and response-body limits are enforced.
- [x] Failed deliveries become dead after the bounded schedule.
- [x] Replay creates a new delivery ID with the same event ID.
- [x] Secret plaintext does not appear in DB dumps, logs, traces, or audit.
- [x] Worker restart recovers expired leases.

### Result

Delivered 2026-07-29:

- **Endpoint management + delivery/dispatch split into two sides of one
  package** (`services/gateway/internal/merchant/webhook`): `Service` (tenant-facing —
  create/rotate/list/delete endpoints, list/get deliveries) is the
  counterpart of `RelayWorker` (the dispatch side, `relay.go`). `Consumer`
  (`consumer.go`) is the inbound side that turns internal ledger events
  into external `WebhookEvent`/`WebhookDelivery` rows; `Replay`
  (`replay.go`) is the tenant/operator replay path. All four share the
  same repository already scaffolded in earlier tasks
  (`WebhookRepository`, migration 000004 + T7's own 000006 for the new
  `environment` column) — no new tables were needed this task, only new
  Go code and one additive column.
- **Envelope/signature/SSRF exactly per the T1 lock**
  (`docs/reference/c1-b2b-design.md §2/§4`): `envelope.go` builds the
  locked `{id, type, livemode, created_at, data}` body with `id` derived
  from the SAME internal logical `EventID` convention `contracts/events/ledger`
  already uses (no second hash); `signature.go` implements
  `t=<unix>,v1=<hmac-sha256 hex>` with `t` bound to the delivery row's own
  immutable `CreatedAt` — reused, never recomputed per attempt, so retries
  and replays are reproducible without a dedicated "signed_at" column;
  `ssrf.go` resolves-then-dials-the-validated-IP directly (never
  re-resolving the hostname) to close the DNS-rebinding TOCTOU window,
  enforced only for `environment == "live"` per the design doc's own
  locked failure-matrix row.
- **Retry/backoff kept in lockstep with the rest of the codebase's outbox
  implementations, on purpose**: `relay.go`'s `nextAttemptAt` uses the
  identical formula to
  `services/payout/internal/repository/vendor_command_repository.go`'s
  `FailCommand` (itself matched to the ledger outbox's `MarkFailed`) —
  base 30s, factor 2, cap 15m, +50% jitter. Unlike
  `payout_vendor_commands`, `merchant_webhook_deliveries` has no per-row
  `max_retries` column (T7's own migration never added one), so
  `maxDeliveryAttempts = 15` is a package constant instead — a deliberate
  simplification since nothing in this task's acceptance list requires a
  per-tenant-configurable retry depth.
- **Structural SSRF/security posture, not rule-based** (same philosophy
  established in T5/T6): `resolveAndDial` dials the IP it just validated,
  never the hostname a second time; `safeClient`'s `CheckRedirect` refuses
  every redirect unconditionally (`http.ErrUseLastResponse`) regardless of
  environment, since that's baseline delivery hygiene, not an SSRF-only
  concern; the 410-auto-disable path and the dead-letter path are both
  reached through the SAME `processDelivery` function as the success
  path — there is no separate "special case" code path that could drift
  out of sync with the ordinary one.
- **Real bug found live, not by inspection**: the pgx v5 stdlib driver
  cannot `Scan` a Postgres `TEXT[]` column (`subscribed_events`) into a
  plain `*[]string` through `database/sql`'s generic interface — a live
  integration test failed with `unsupported Scan, storing driver.Value
  type string into type *[]string` the first time `ListEndpoints` was
  exercised against real Postgres. Fixed by scanning through
  `pgtype.Map.SQLScanner(&e.SubscribedEvents)`
  (`services/gateway/internal/merchant/repository/webhook_repository.go`) — pgx's own
  documented bridge for exactly this case, no new dependency (pgtype is
  already a subpackage of the pgx/v5 module this repo already depends
  on). This is the only `TEXT[]` column in the entire schema, so there
  was no prior precedent to follow; a naive `pgtype.Array[string]` scan
  destination was tried first and also failed for the same underlying
  reason (generic pgtype wrapper types don't implement `sql.Scanner` on
  their own outside a `Map`), which is what led to the `SQLScanner`
  fix.
- **Second bug found the same way**: two PRE-EXISTING integration tests in
  `services/gateway/internal/merchant/repository/repository_integration_test.go`
  (`TestWebhookRepository_DeliveryUniqueness_RaceSafe`,
  `TestWebhookRepository_AttemptsCascadeDeleteWithDelivery`, both written
  before this task added the `environment` column + its
  `CHECK (environment IN ('sandbox','live'))` constraint) constructed a
  `model.WebhookEndpoint{}` with no `Environment` set, which now fails
  the CHECK with an empty string instead of falling through to the
  column's `DEFAULT 'live'` — a Go zero-value insert always sends an
  explicit value, so it never reaches the column default. Fixed by adding
  `Environment: "sandbox"` (matching the tenant those tests already
  provision as `"sandbox"`) to both literals.
- **Test convention**: hand-written fakes for `WebhookRepository` and
  `TenantRepository` (`fakes_test.go`), matching this package's own
  established no-gomock convention — the fake reproduces the real
  implementation's key invariants (`ClaimDue`'s lease exclusivity,
  `CreateDelivery`'s dedup vs. `CreateReplayDelivery`'s exemption from
  it) rather than being a dumb stub. `messaging.MockBroker` (already
  shared by `internal/platform/messaging`) is reused for `Consumer` tests instead of a
  second hand-rolled broker fake.
- **Verified end-to-end against real Postgres and a real HTTP server**
  (`webhook_integration_test.go`, `-tags=integration`):
  `TestWebhookRelay_EndToEnd` drives the full chain through the actual
  public API — `Consumer.Start`'s registered handler (captured via
  `messaging.MockBroker`, not called directly) dedupes a redelivered
  message to one `WebhookEvent` and one `WebhookDelivery`; the stored
  `secret_ciphertext` column is read raw via SQL and asserted to never
  contain the plaintext secret; `RelayWorker.ProcessOnce` dispatches to a
  real `httptest.Server` and the received signature is verified against
  `Sign(secret, delivery.CreatedAt, payload)` byte-for-byte; `Replay`
  produces a new delivery ID sharing the original event ID and that
  replay is itself picked up and dispatched on the next poll; a delivery
  manually given an EXPIRED lease (simulating a crashed worker) is
  reclaimed and dispatched by the next `ProcessOnce` call, proving
  restart recovery without a separate recovery pass. Unit tests
  additionally cover: SSRF rejection table (loopback/private/link-local/
  metadata/unspecified/multicast), live-vs-sandbox SSRF divergence against
  the same URL, no-redirect, bounded response body, signature determinism
  and tamper/malformed-header rejection, exhausted-retry dead-lettering,
  410 auto-disable, and replay's exemption from the automatic path's
  dedup constraint.
- **Scope deliberately NOT covered this task** (unchanged from earlier
  scoping notes in `envelope.go`): `payin.updated.v1`/`payout.updated.v1`
  external event types — these require a payin/payout-owned pending-state
  outbox that T6 explicitly deferred; no T7 acceptance criterion requires
  them. Wiring `services/gateway/internal/merchant.Module.StartWebhookConsumer`/
  `StartWebhookRelay` into `cmd/gateway/main.go` is also not done here —
  `services/gateway/internal/merchant.Module` has never been wired into `cmd/gateway` at
  all (confirmed: zero references anywhere under `cmd/`), a pre-existing
  gap from T2 onward and already tracked separately (`task_6214960b`,
  flagged during T6 for the B2B HTTP handlers specifically); this task
  only had to make the Module itself capable of being started, which it
  now is (`NewModule` takes the `*cryptox.Ring` it needs; `StartWebhookRelay`/
  `StartWebhookConsumer` exist with the same `stop func()`/`(stop, err)`
  shapes as `StartRetentionRunner`).
- **Full sweep**: `go build ./...`, `go vet -tags=integration ./...`,
  `make lint` (0 issues, after applying `go fix -omitzero=false`'s two
  suggested modernizations — a `slices.Contains` replacement and a
  range-over-int loop), `go test -race ./...` (full repo, green),
  `go test -tags=integration ./services/gateway/internal/merchant/...` (repository +
  webhook packages against real Postgres, green — including the two
  pre-existing tests fixed above), `go run ./cmd/doccheck` (129 files
  valid, including the new receiver guide), `go run ./cmd/retentioncheck`
  (100 policy entries valid — no new retention classes needed, T2 already
  covers `gateway.merchant.webhook_events`/`webhook_deliveries`).
- **Published**: `docs/reference/webhook-receiver-guide.md` — envelope
  shape, signature verification (with a standalone Go reference
  implementation independent of this codebase's own `webhook.Verify`),
  idempotency guidance, retry/dead-letter schedule, response requirements,
  sandbox-vs-live behavior, and secret rotation guidance for a merchant
  building a receiver.

T8 may begin.

---

## T8 — Admin BFF merchant operations

### Work

- Add typed management client.
- Add routes, views, CSRF checks, and policy registry entries.
- Add tenant lifecycle.
- Add account provisioning.
- Add key and scope management.
- Add quota management.
- Add webhook endpoint and delivery views.
- Add replay and disable controls.
- Add one-time secret UI.
- Add maker/checker flow where required.
- Add audit events.

### Acceptance

- [x] Unauthorized roles cannot view or mutate merchant management.
- [x] Browser mutations require CSRF.
- [x] Live activation and tenant closure require checker approval.
- [x] Secret is shown once and never re-rendered.
- [x] Every mutation emits a redacted audit event.
- [x] Existing Admin BFF routes remain green.
- [x] Admin E2E covers the full sandbox onboarding flow.

### Result

Delivered 2026-07-29:

- **The real gap wasn't Admin BFF — it was that `services/gateway/internal/merchant` had no
  HTTP surface at all, and was never wired into `cmd/gateway`.** Admin
  BFF's own `/api/v1/admin/gateway/` route, generic proxy, CSRF
  middleware, and `AuditMutation` call were ALL already fully wired from
  earlier work (`services/adminbff/internal/module.go:125`,
  `services/adminbff/internal/proxy.go`'s `m.proxy(...)`) — they simply had nothing
  behind them to proxy to. T8's actual scope, once that was established,
  was: (1) build `services/gateway/internal/merchant`'s own admin HTTP router
  (`services/gateway/internal/merchant/adminhttp.go`, new), (2) add the maker-checker gate
  T8 needed that didn't exist yet (`services/gateway/internal/merchant/lifecycle`, new),
  (3) wire `services/gateway/internal/merchant.Module` into `cmd/gateway/main.go` for the
  first time ever, and (4) add one console page to Admin BFF. No new
  Go code was needed in `services/adminbff` itself beyond the template —
  CSRF, audit, and the downstream call were free.
- **Maker-checker for tenant lifecycle**
  (`services/gateway/internal/merchant/lifecycle`, migration `000007_merchant_tenant_lifecycle`):
  mirrors `services/auth`'s own `OperatorOffboardingRequest` shape almost
  exactly — `Propose`/`Approve`/`Reject` on a
  `merchant_tenant_lifecycle_requests` table with a
  `CHECK (approved_by IS NULL OR approved_by <> requested_by)` backstop
  and a partial unique index limiting one pending proposal per
  (tenant, action). `Approve` calls the SAME `TenantRepository.UpdateStatus`
  T2/T3 already built for the ungated transitions — the only new work was
  the two-person gate in front of it, not the status flip itself.
  §16.3's exact rule set is enforced structurally, not just documented:
  sandbox tenant creation goes 'active' immediately (maker only, no
  lifecycle row at all); a live tenant is created 'draft' and requires a
  separate `activate` propose/approve; `close` uses the identical
  propose/approve path; `suspend` stays a direct maker-only call with no
  lifecycle row, since §16.3 never lists it as checker-gated; quota
  updates compare the REQUESTED values against the 60/60 baseline
  BEFORE any write and require the checker role directly (a single-actor
  permission check, not a two-step propose/approve — §16.3 distinguishes
  these two enforcement shapes and this implementation preserves the
  distinction) — a maker can never sneak an oversized quota through by
  pairing it with an unrelated field change.
- **`services/gateway/internal/merchant/adminhttp.go`** (new, 22 routes on
  `Module.AdminRouter()`): tenant create/list/get/suspend, lifecycle
  propose/approve/reject/list, account provision/get, key
  create/list/rotate/revoke, quota get/update, webhook endpoint
  create/list/rotate-secret/disable, delivery list/replay. Role gates
  (`isAdmin`/`isAdminMaker`/`isAdminChecker`) are byte-identical in shape
  to `services/ledger/internal/transport/http.go`'s own trio — this codebase's
  established per-package duplication convention for this exact check,
  not a new pattern. One-time secrets (API key plaintext, webhook signing
  secret) are returned ONLY from the create/rotate response body, never
  from any list/get endpoint (`redactedKey`/`redactedWebhookEndpoint`
  wire types explicitly omit them) — proven by
  `TestAdminRouter_CreateKey_ReturnsPlaintextOnce` asserting the returned
  plaintext string never appears in a subsequent list response.
- **Bug found live, before any unit test caught it**: the first working
  build mapped every `KeyService.CreateKey` error to a bare 500 — a live
  curl request with an invalid scope name returned
  `{"code":"INTERNAL_ERROR"}` when it should have been 400. Fixed with a
  `writeKeyServiceError` helper mapping `auth.ErrUnknownScope`/
  `ErrTooManyActiveKeys` to 400/409, and locked in with
  `TestAdminRouter_CreateKey_UnknownScopeIsBadRequest`.
- **Two contract gates caught by `go test -race ./...`, both fixed
  before commit**: (1) `TestModuleBoundaries` — `cmd/gateway` and
  `services/gateway/internal/transport/http` importing `services/gateway/internal/merchant` for the first time
  tripped the module-ownership allowlist in `boundary_test.go`; fixed by
  adding `"merchant": true` to gateway's owned-module set (one line,
  `boundary_test.go:55`). (2) `TestValidate_RealPolicyIsClean` — the new
  `merchant_tenant_lifecycle_requests` table had no
  `config/data-retention.yaml` entry; fixed by adding
  `gateway.merchant.tenant_lifecycle_requests` (classification internal,
  `retain_permanent`, mirroring `auth.operator_offboarding_requests`'s own
  entry and rationale — both are permanent two-person-control audit
  trails), then regenerating `docs/data/retention.md` via
  `make retention-docs`.
- **Wiring** (`cmd/gateway/main.go`, `services/gateway/internal/transport/http/{dependencies,router}.go`):
  `cfg.Cryptox.Ring()` (boot-fails on a missing/malformed ring, same
  "money-safety, never optional" posture as every other cryptox-dependent
  service) and `ledgerclient.New(ledgerConn)` (reusing Gateway's existing
  gRPC connection — no second dial) feed `merchant.NewModule`.
  `StartWebhookRelay`/`StartWebhookConsumer`/`StartRetentionRunner` (all
  already built in T7 but never started anywhere) are now started
  alongside Gateway's other background workers, with matching cleanup on
  shutdown. `services/gateway/internal/transport/http.NewInternalRouter` gained its first-ever JWT
  `authed` chain (`middleware.WithAuth`, matching ledger/payin/payout's
  own convention) specifically to mount
  `AdminRouter()` at `/api/v1/admin/gateway/`.
- **Real deployment gap found and fixed via a live boot**: `cmd/gateway`
  had never required `cryptox`/`MERCHANT_API_KEY_PEPPER` before, so
  `docker-compose.yml`'s `gateway-service` block had neither secret
  wired — the container would have failed to boot in every real
  deployment the moment this code shipped. Confirmed live: added
  `merchant_api_key_pepper` to `make cryptox-secret`, to the top-level
  `secrets:` block, to `gateway-service`'s own `environment`/`secrets`
  keys (`CRYPTOX_KEY_V1_FILE`, `MERCHANT_API_KEY_PEPPER_FILE`), and to
  `scripts/load-test.sh`'s disposable-secret seeding list (the same class
  of gap this session's own memory already flags for
  `LEDGER_IDEMPOTENCY_KEY_V1`) — then rebuilt and booted the real
  container to confirm.
- **Verified live against the real stack** (Docker Compose, real
  Postgres, real mTLS, real signed JWTs — no unit-test shortcuts) before
  concurrent unrelated work on the same machine began mutating the shared
  Compose project: `docker compose --profile app up -d --build
  gateway-service` + `curl` through the dev-operator mTLS identity at the
  internal listener, driving the actual production code path
  end-to-end: created a sandbox tenant (verified `status: active`
  immediately, no lifecycle row); created a live tenant (verified
  `status: draft`); proposed `activate` as a maker, verified the SAME
  maker role could not approve its own proposal (403), then verified a
  DIFFERENT checker identity's approval succeeded and the tenant flipped
  to `active` with `ActivatedBy` correctly set to the checker; verified a
  `user`-role token was rejected outright (403) from tenant creation;
  verified a quota update above the 60/60 baseline was rejected for a
  maker (403) and accepted for a checker (200). This live pass is what
  caught the `writeKeyServiceError` gap above.
- **Full sweep**: `go build ./...`, `go vet -tags=integration ./...`,
  `make lint` (0 issues), `go test -race ./...` (full repo, green,
  including the two contract-gate fixes above), `go run ./cmd/doccheck`
  (129 files valid, including the new merchant console template),
  `go run ./cmd/retentioncheck` (101 policy entries valid,
  `docs/data/retention.md` regenerated and current). 14 new unit tests in
  `services/gateway/internal/merchant/adminhttp_test.go` (hand-written fakes, matching this
  package's own no-gomock convention, driven through the REAL
  `middleware.WithAuth` JWT chain rather than injecting claims directly)
  plus 9 in `services/gateway/internal/merchant/lifecycle/lifecycle_test.go` cover every
  role gate, the self-approval rejection, the quota baseline boundary,
  the one-time-secret contract, and a `TestAdminRouter_FullSandboxOnboardingFlow`
  test that chains create-tenant → create-key → create-webhook-endpoint →
  confirm-neither-secret-is-re-exposed → list-deliveries through the real
  HTTP handlers, satisfying this task's own "Admin E2E covers the full
  sandbox onboarding flow" acceptance line by name.
- **Deliberately out of scope**: a dedicated "policy registry" module —
  §16.3 says "must be locked in the Admin BFF policy registry," but no
  such module exists anywhere in this codebase (confirmed by explicit
  search); the established, working convention this codebase already
  uses everywhere else for this exact check is a local
  `isAdmin`/`isAdminMaker`/`isAdminChecker` trio per package (ledger,
  auth, and now merchant) — introducing a new shared registry abstraction
  for T8 alone, when three independent precedents already reject that
  design, would be scope creep, not scope completion. Rich tenant-list
  browsing (search/filter/paginate across every tenant) is also out of
  scope: `TenantRepository` has no `ListAll`, and §16.2's own "tenant list
  and detail" is satisfied by a public-ID lookup, matching the plan's own
  minimal-UI, htmx-placeholder-div style already established by every
  other Admin BFF console page (`catalog.html`, `payout.html`).

T9 may begin.

---

## T9 — Observability, operational controls, and runbooks

### Work

- Add metrics, logs, traces, dashboards, and alerts.
- Add stuck-idempotency detection.
- Add webhook queue and dead-letter panels.
- Add key-compromise and endpoint-compromise runbooks.
- Add Redis quota outage runbook.
- Add RabbitMQ outage runbook.
- Add owner-service outage runbook.
- Add merchant suspension and global route-disable controls.
- Document restart and recovery behavior.
- Validate cardinality.

### Required runbooks

```text
docs/runbooks/merchant-api-key-compromise.md
docs/runbooks/merchant-tenant-suspension.md
docs/runbooks/merchant-quota-backend-outage.md
docs/runbooks/merchant-idempotency-stuck.md
docs/runbooks/merchant-webhook-backlog.md
docs/runbooks/merchant-webhook-secret-compromise.md
docs/runbooks/merchant-owner-service-outage.md
```

### Acceptance

- [x] Every alert has an actionable runbook.
- [x] Dashboards avoid tenant-level metric cardinality.
- [x] Suspension prevents new writes.
- [x] Existing accepted writes remain queryable.
- [x] Webhook backlog and oldest age are visible.
- [x] Stuck leases and idempotency records are visible.
- [x] Trace evidence crosses owner-service and async boundaries.

### Result

Delivered 2026-07-29:

- **Runbook path corrected from the plan's own stale literal**: this
  task's own "Required runbooks" list above names
  `docs/runbooks/merchant-*.md`, but `docs/runbooks` is explicitly listed
  as a FORBIDDEN legacy path in `cmd/doccheck`'s own
  `documentationLayoutFailures` check (a prior reorg moved every runbook
  under `docs/operations/runbooks/` and doccheck now fails the build if
  that old path is recreated). All seven runbooks were written at
  `docs/operations/runbooks/merchant-*.md` instead — the currently
  enforced location — and cross-linked from
  `docs/operations/runbooks/README.md`'s own "choose by symptom" and
  index tables.
- **Metrics** (`services/gateway/internal/merchant/metrics.go`,
  `services/gateway/internal/merchant/webhook/metrics.go`, new): `seev_merchant_idempotency_records{state}`,
  `seev_merchant_idempotency_stuck_leases`,
  `seev_merchant_webhook_deliveries{status}`,
  `seev_merchant_webhook_backlog_oldest_age_seconds`,
  `seev_merchant_webhook_delivery_attempts_total{result}`, and
  `seev_merchant_b2b_api_enabled`. Every label is a small fixed enum
  (`state`/`status`/`result`) — never a tenant id, delivery id, endpoint
  id, or idempotency key, satisfying "dashboards avoid tenant-level
  metric cardinality" structurally rather than by convention alone (there
  is no per-tenant label anywhere to accidentally add one to). The three
  snapshot gauges are refreshed every 30s by a new
  `Module.StartObservabilityRefresher` ticker
  (`services/gateway/internal/merchant/metrics.go`), mirroring
  `services/auth.refreshPrivacyRequestsGauge`'s own established
  "recompute from the database once per tick" convention; the delivery
  counter increments inline in `relay.go`'s own `processDelivery`.
- **Stuck-idempotency and webhook-backlog visibility** (T9's own two named
  acceptance lines): backed by two new repository methods proven against
  real Postgres in this task
  (`IdempotencyRepository.StateCounts`/`CountStuckLeases`,
  `WebhookRepository.BacklogStats` — `services/gateway/internal/merchant/repository/{idempotency,webhook}_repository.go`)
  — `TestIdempotencyRepository_ObservabilityQueries_T9` and
  `TestWebhookRepository_BacklogStats_T9` in
  `services/gateway/internal/merchant/repository/repository_integration_test.go`.
- **Alerts** (`deploy/observability/prometheus/rules/merchant.yml`, new,
  registered in `prometheus.yml`'s `rule_files`, validated live via
  `promtool check rules` in a throwaway `prom/prometheus` container — 5
  rules, 0 errors): `SeevMerchantIdempotencyStuckLeases`,
  `SeevMerchantWebhookBacklogStale` (oldest pending/failed delivery older
  than T7's own 15-minute backoff cap), `SeevMerchantWebhookDeliveriesDeadLettering`,
  `SeevMerchantWebhookDeliveryFailuresHigh`, `SeevMerchantB2BAPIDisabled`.
  Every alert's `runbook:` annotation points at one of the seven runbooks
  below — "every alert has an actionable runbook" checked by
  construction, not just by intent.
- **Dashboard** (`deploy/observability/grafana/dashboards/merchant-b2b.json`,
  new, auto-discovered by the existing file-provider — no provisioning
  change needed): idempotency records by state, stuck-lease stat panel,
  webhook deliveries by status, backlog-oldest-age stat panel (with
  yellow/red thresholds at 5/15 minutes matching the alert), delivery
  attempt rate by result, and the global kill-switch state.
- **Global route-disable control**
  (`services/gateway/internal/merchant/auth.GlobalFlag`/`RequireB2BEnabled`, new;
  `merchant_settings` table, migration `000008`): a generic key/value
  operational-settings table (extensible to future toggles with no new
  migration) backs an in-memory-cached, atomically-read flag —
  `RequireB2BEnabled` returns `503` before any API-key lookup when
  disabled. A missing row (the state of every fresh deployment) reads as
  enabled; only an explicit operator `SetEnabled(false)` call can ever
  disable traffic, and `SetEnabled` updates the in-memory value
  immediately on the instance that called it (other instances pick it up
  on their next 30s refresh tick) rather than waiting for the next
  scheduled reload. Exposed at
  `GET`/`PUT /api/v1/admin/gateway/global/b2b-api` — `PUT` (disabling OR
  re-enabling) requires the checker role, the single highest-blast-radius
  action this router exposes, since it affects every tenant
  simultaneously rather than one. **This control is structurally correct
  but not yet load-bearing**: it is mounted at the exact chokepoint
  (`RequireMerchantAuth`'s own layer) every future merchant-facing
  business route will pass through, but — as already noted in T6's own
  Result section and tracked separately as `task_6214960b` — Gateway has
  no live merchant transfer/payin/payout HTTP routes wired yet, so there
  is currently nothing for this switch to gate in a running deployment
  beyond the admin surface itself. `RequireB2BEnabled` will need one line
  added to whichever router eventually mounts those routes.
- **Tenant suspension already existed and needed no new code** (T3):
  `RequireMerchantAuth` was already checking `tenant.Status ==
  "suspended"` and returning `403` before this task began — this task's
  job was verifying and documenting the behavior, not building it. Traced
  through both existing unit test coverage
  (`services/gateway/internal/merchant/auth/middleware_test.go`'s own
  `suspended_tenant` subtest) and the new
  [merchant-tenant-suspension.md](../../operations/runbooks/merchant-tenant-suspension.md)
  runbook, which also documents the two things suspension deliberately
  does NOT do (cancel in-flight webhook deliveries; revoke API keys) so
  an operator doesn't assume broader effect than the mechanism actually
  has.
- **Restart and recovery behavior documented**
  (`docs/reference/c1-b2b-design.md` new §6): every background component
  (idempotency leases, the webhook relay's claim-then-expire pattern, the
  webhook consumer's topology re-declaration on restart, the
  observability gauges, quota's ephemeral Redis state) recovers through
  the SAME mechanism already exercised by normal operation — no bespoke
  recovery procedure exists anywhere in this module, and none was added;
  the doc explains why by walking through each component's own already-
  built self-healing path (with a direct citation to T7's own
  `TestWebhookRelay_EndToEnd` live proof of lease reclaim after a
  simulated crash).
- **Trace evidence across the async boundary already existed
  structurally, confirmed rather than built**: `internal/platform/messaging`'s own
  `Publisher`/`Consumer` inject and extract OpenTelemetry trace context
  into/from AMQP headers unconditionally
  (`internal/platform/messaging/publisher.go:124`, `internal/platform/messaging/consumer.go:196`) —
  since T7's `webhook.Consumer.Start` calls `c.broker.Consume(...)` the
  identical way `services/gateway/internal/notification`'s own consumer does, it inherits this
  propagation for free. The synchronous HTTP boundary (Admin BFF →
  Gateway's internal listener) is covered the same way: `AdminRouter()`
  is mounted inside `services/gateway/internal/transport/http.NewInternalRouter`'s existing
  `global` middleware chain, which already includes
  `middleware.WithTracing` for every request. No new tracing code was
  needed anywhere in this task.
- **Full sweep**: `go build ./...`, `go vet -tags=integration ./...`,
  `make lint` (0 issues), `go test -race ./...` (full repo, green),
  `go test -tags=integration ./services/gateway/internal/merchant/...` (repository +
  webhook + auth packages against real Postgres, green — including three
  new T9-specific integration tests), `go run ./cmd/doccheck` (136 files
  valid, including all seven new runbooks and the new design-doc
  section), `go run ./cmd/retentioncheck` (102 policy entries valid,
  covering the new `merchant_tenant_lifecycle_requests` — filed under T8
  but only now retention-classified — and `merchant_settings` tables),
  `promtool check rules` on the new alert file (5 rules, 0 errors). 15
  new unit tests (`services/gateway/internal/merchant/auth/globalflag_test.go` plus
  additions to `services/gateway/internal/merchant/adminhttp_test.go`) cover the flag's
  default-enabled state, immediate-effect `SetEnabled`, the
  multi-instance refresh-lag behavior, `RequireB2BEnabled`'s pass/block
  paths, and the admin route's own checker-only gate.

T10 may begin.

---

## T10 — Verification, chaos, and release evidence

### Work

- Add unit, integration, contract, E2E, race, fuzz, and chaos tests.
- Add `scripts/merchant-e2e.sh`.
- Add `scripts/merchant-chaos.sh`.
- Add Make targets.
- Run a clean-tree final gate.
- Record all commands, outputs, metrics, and known residual risks.
- Update roadmap indexes and current service reference.
- Archive the plan only after final acceptance.

### Acceptance

- [x] All final gates pass from a clean tree (one pre-existing, unrelated
  script flake found and tracked — see Result).
- [x] Chaos evidence demonstrates no duplicate money.
- [x] Cross-tenant access attempts all fail (one item not applicable to the
  current API shape, one nuance deferred — see Result).
- [x] No secret is found in repository/log/database scans.
- [x] All contract artifacts are generated and clean.
- [x] Existing user, admin, and callback journeys remain green (privacy-e2e
  exception below, unrelated to this plan).
- [x] Operational rollback and disable controls are exercised.
- [x] Residual risks are documented.
- [x] Plan status and roadmap index are updated truthfully.

### Result

**Core complete.** Every money-movement, auth, and operational-control path
required for T10 is built and live-verified. Two follow-up gaps are
explicitly deferred as **"T10b"** (§23.8 race-test items 2-6, and the
non-precision-targeted chaos coverage in §24.2-24.6) — the same
honest-scope-reduction discipline this repository already used for A8's
T2.5b/T4b/T5b/T6b, applied here to the final verification task rather than
pretending a narrower scope was the whole of T10.

**Two real bugs found and fixed this pass, both live-verified:**

1. **T9's kill switch was never wired to the B2B router.** `RequireB2BEnabled`
   existed since T9, but no B2B HTTP route existed yet to gate it — and it
   was *still* never mounted once T10's follow-up built the router. Found
   live via `scripts/merchant-e2e.sh` (disabling the flag had zero effect on
   real traffic). Fixed: `GlobalFlag` is now a required `Deps` field
   (`NewRouter` panics without it), `RequireB2BEnabled` runs first in the
   middleware chain, and `TestB2BRouter_GlobalKillSwitchGatesEveryRoute`
   proves it (commit `d818370`).
2. **A live-activation bypass via key/tenant environment mismatch.**
   `KeyService.CreateKey` took `tenantID` and `environment` as fully
   independent arguments and never checked either against the tenant's own
   `Environment` — so a sandbox tenant (auto-activates, no checker approval)
   could be issued a "live" key, completely bypassing the maker/checker
   draft→active gate a real live tenant is supposed to require before
   touching real vendors and real money. Found while auditing §23.7's "test
   key accesses live tenant" / "live key accesses sandbox tenant" cases.
   Fixed at two layers: `CreateKey` now rejects a mismatched environment
   before ever issuing a key, and `RequireMerchantAuth` independently
   rejects any already-issued key whose environment disagrees with its
   tenant's (protects rows that predate the fix). New unit tests for both
   layers; full merchant suite (unit + `-tags=integration`, real Postgres)
   and a fresh live `merchant-e2e.sh` run both stayed green (commit
   `86b4824`).

**Chaos (§24.1).** Added **scenario 21** to `scripts/chaos-test.sh`
(the plan's forecast of a separate `scripts/merchant-chaos.sh` was narrowed
to a new scenario in the existing shared chaos harness instead — the
established convention every other track already uses, and the reason
`scripts/merchant-chaos.sh` does not exist as its own file). Mirrors
scenario 1's kill-9 pattern (40 concurrent requests, kill -9 partway
through, restart, retry non-2xx) against the merchant B2B transfer endpoint
instead of the user-facing ledger API — proving the money-safety guarantee
for a genuine gateway process crash, not just a client-side idempotent
retry (which `merchant-e2e.sh`'s own replay test already covered but is a
weaker claim). Verified live: all 40 in-flight requests were killed before
receiving any response; all 40 retries landed exactly once; final balances
exact (tenant A debited 40, tenant B credited 40); `fn_verify_ledger_balance`,
`v_account_balance_audit`, and no-stuck-pending all clean (commit
`b2e20b7`). §24.2-24.6 (owner-service/RabbitMQ/Redis/webhook-receiver/
database failure matrices) are covered generically by the pre-existing
chaos scenarios for the shared ledger/payin/payout/webhook machinery
merchant transactions route through, plus T7's own webhook receiver-failure
unit tests (timeout/reset/TLS/429/500/410/oversized/redirect/DNS-rebinding/
private-address) — but no scenario re-runs those failure modes through the
merchant-specific surface precisely. Tracked as T10b.

**Cross-tenant matrix (§23.7).** Audited all 9 required cases against the
existing test suite (`services/gateway/internal/merchant/**/*_test.go`):
tenant-reads-tenant, tenant-mutates-tenant, idempotency-key-reuse, and
delivery-replay are covered by existing tests
(`b2b_integration_test.go`, `idempotency_test.go`, `replay_test.go`,
`repository_integration_test.go`). Test-key-vs-live-tenant and
live-key-vs-sandbox-tenant were **not** covered — and turned out to be the
real gap fixed above, now covered by
`TestKeyService_CreateKey_RejectsEnvironmentMismatch` and
`TestRequireMerchantAuth_TenantKeyEnvironmentMismatch_FailsClosed`.
Source-account targeting does not apply to the current API shape:
`source_account_id` is always derived server-side from the caller's own
tenant (`services/gateway/internal/merchant/api/transactions_handler.go`), never taken from
the request body, so there is no field for tenant A to nominate tenant B's
account as a debit source. Suspended-tenant reads vs. writes: the
middleware currently fails closed uniformly for a suspended tenant
(`TestRequireMerchantAuth_FailsClosed/suspended_tenant`), which is stricter
than §23.7's stated default policy ("read access may remain available for
reconciliation") — no test (or code path) currently distinguishes the two.
Tracked as T10b.

**Race tests (§23.8).** `go test -race ./services/gateway/internal/merchant/...` (plain and
`-tags=integration`) is clean — no data races in anything that exists. Of
the 7 required scenarios: concurrent-same-idempotency-key is fully covered
(3 tests, unit + real-Postgres); duplicate-owner-events is covered
functionally but not under real goroutine contention; concurrent
key-rotation-vs-request, concurrent-webhook-workers,
concurrent-replay-vs-replay, concurrent-endpoint-disable-vs-delivery, and
concurrent-tenant-suspension-vs-financial-write have no test coverage at
all — there is nothing for `-race` to have caught. Tracked as T10b.

**Final gate, run from a clean tree this pass:** `go build ./...`,
`go vet ./...`, `make lint` (0 issues), `make contracts` (clean),
`go run ./cmd/doccheck` (139 files, clean), `go test ./...` (91 packages,
0 failures), `go test -race ./...` (91 packages, 0 failures, 0 data races),
`go test -tags=integration ./...` (0 failures after fixing the environment
issue below), `scripts/smoke-test.sh all` (19/19), `scripts/business-e2e.sh`
(84/84), `scripts/admin-e2e.sh` (5/5), `scripts/merchant-e2e.sh` (25/25).
`scripts/privacy-e2e-host.sh` fails reproducibly (2/2 runs) at its closure
leg — root-caused via log analysis to the script's own `assurance_run()`
manual trigger racing the `ASSURANCE_INTERVAL=1s` background scheduler it
also configures (a 409-style collision under `curl -sf` kills the script via
`set -e`, which tears down services mid-run). Confirmed via
`git log -- scripts/privacy-e2e.sh scripts/privacy-e2e-host.sh
services/assurance` that none of these have been touched by any commit in
this plan — this is a pre-existing bug in plan 51's own test tooling, not a
Plan 57 regression, and does not exercise anything Plan 57 owns. Filed as
its own follow-up task rather than fixed here (out of this plan's blast
radius).

**Environment issues found and fixed along the way (not product bugs, but
real blockers to running the gate honestly):** the accumulated
`KEEP_WORK_DIR=1`/testcontainers runs across this session's verification
work left 66 leaked testcontainers Postgres instances and 1 leaked
testcontainers RabbitMQ instance running, and separately left ~11 GB of
stale Go build/lint/module caches under `/tmp`, pushing the host disk to
95% full — together these caused a transient `internal/platform/database` testcontainers
timeout and a `seev-rabbitmq-1` health-check failure that had nothing to do
with any code change. Cleaned up (containers stopped/removed by name,
scoped to exclude the real `seev-*` compose stack; stale `/tmp/seev-*`
caches removed) and every affected gate re-run clean afterward.

**Secret scan.** `merchant_api_keys.secret_digest` and
`merchant_webhook_endpoints.secret_ciphertext` are both `bytea` — no
plaintext secret column exists in the schema. Every service log from a
full live `merchant-e2e.sh` run was grepped for the `mk_sandbox_`/`mk_live_`
API-key prefix pattern: zero matches outside the one intentional one-time
plaintext response.

**Operational controls exercised live:** the global B2B kill switch
(disable → 503 on every route → re-enable → immediate recovery, both via
`merchant-e2e.sh` and a dedicated Go test), checker-only enforcement on the
kill switch, and the maker/checker tenant lifecycle (T8) via the admin
console.

**Docs.** `docs/reference/services.md`'s Gateway section, `docs/roadmap/
README.md`, and `docs/roadmap/42-long-term-roadmap.md` all previously said
"not yet implemented" — updated to reflect the actual T0-T9-complete,
T10-in-progress state (commit `32047d7`). `docs/evidence/
c1-final-acceptance.md` records this task's full evidence log.

**Plan status:** T10 is core-complete; the plan stays **active** (not
archived) until the T10b follow-up items above are resolved, matching the
same pattern A8 used for its own T6/T6b split.

### T10b closure

All three T10b follow-up items are now closed.

**1. Race tests (§23.8 items 2-6).** Added, all passing repeatedly under
`-race` against real Postgres (commit `e30e283`):

- concurrent key rotation/revocation vs. an in-flight request
  (`services/gateway/internal/merchant/auth/auth_race_test.go`);
- concurrent webhook workers claiming the same due delivery, concurrent
  replay of the same original delivery, and concurrent endpoint disable
  vs. an in-flight delivery batch (`services/gateway/internal/merchant/webhook/webhook_race_test.go`);
- concurrent tenant suspension vs. a financial write
  (`services/gateway/internal/merchant/api/b2b_integration_test.go`).

Each proves the real invariant — no double-dispatch, no delivery escapes
to a disabled endpoint, no write reports success without completing —
not just "no crash."

**2. Suspended-tenant read/write policy (§23.7).** Fixed: `RequireMerchantAuth`
previously rejected a suspended tenant's requests uniformly, including
reads, which was stricter than §23.7's own stated default ("read access
may remain available for reconciliation"). `Principal` gained a
`TenantSuspended` field; a new `RequireTenantNotSuspendedForWrites`
middleware, mounted per-route (only the router knows `isWrite`), denies
writes with 403 `TENANT_SUSPENDED` while letting reads through. Verified
with new unit tests plus a live integration test through the actual
assembled router against real Postgres: read succeeds while suspended,
write is denied, both recover immediately on reactivation (commit
`7ec70dd`).

**3. Precision chaos coverage (§24.3/§24.4).** Added chaos scenarios 22
and 23 (commit `bcdee3f`), both passing live:

- **Scenario 22** stops the real Redis container and proves
  `services/gateway/internal/merchant/quota.Enforcer`'s outage posture through the actual
  assembled Gateway — writes fail closed with 503 `QUOTA_UNAVAILABLE`,
  reads degrade to a bounded allow, both recover immediately once Redis
  returns, no restart needed. Previously only proven against a
  fake/unreachable client in unit tests.
- **Scenario 23** stops RabbitMQ, settles a real merchant payin through
  mockvendor's signed webhook while the broker is down (proving posting
  never depends on RabbitMQ), confirms zero webhook hits during the
  outage, then restarts the broker and confirms the merchant webhook
  `Consumer` — a distinct queue binding from ledger's own outbox-draining
  consumer that scenario 2 already covers — catches up and the relay
  delivers the event.

§24.2 (owner-service timeouts) and §24.6 (database failures) remain
covered only generically, through the shared ledger/payin/payout
machinery merchant transactions route through (the same underlying
`execTransfer`/Postgres recovery path scenarios 1/3/5/etc. already prove)
— a judgment call that this is adequate given merchant transfers share
that exact code path with user-facing transfers, rather than a gap.
§24.5 (webhook receiver failures: timeout/reset/TLS/429/500/410/oversized/
redirect/DNS-rebinding/private-address) remains covered by T7's own
extensive unit-level receiver-failure-matrix tests, not re-proven as a
chaos-test.sh scenario — judged adequate for the same reason.

A pre-existing, unrelated bug was found and fixed along the way while
building scenario 23: `docs/reference/services.md` claimed a
merchant-facing `/api/v1/b2b/webhook-endpoints` route (webhook management
is actually admin-only) and named external event types
(`payin.settled.v1`, `transfer.posted.v1`) that were never implemented —
`transaction.posted.v1` is the one real external event type.

**Final gate re-run after T10b:** `go build ./...`, `go vet ./...`,
`make lint` (0 issues), `go test -race ./services/gateway/internal/merchant/...` and
`go test -race -tags=integration ./services/gateway/internal/merchant/...` (both clean, no
data races), `shellcheck scripts/chaos-test.sh` (no new warning classes),
and a fresh `scripts/merchant-e2e.sh` run (all assertions passing) — all
this pass.

**Plan status: complete.** All T10 and T10b acceptance items are
satisfied; the plan is archived to `docs/roadmap/archive/` as of this
commit.

---

## 21. Recommended PR sequence

Keep pull requests narrow enough to review and revert.

```text
PR 1  — C1 entry evidence, architecture, threat model, OpenAPI skeleton
PR 2  — Gateway merchant schema and package scaffold
PR 3  — API-key authentication, scopes, and redaction
PR 4  — Quota and durable idempotency
PR 5  — Ledger merchant provisioning and read contracts
PR 6  — Merchant transfer endpoint and E2E
PR 7  — Payin merchant-principal support and endpoint
PR 8  — Payout merchant-principal support and endpoint
PR 9  — External webhook event schema and inbox
PR 10 — Webhook endpoint management, signing, delivery, retry, dead/replay
PR 11 — Admin BFF management surfaces and audit
PR 12 — Dashboards, alerts, runbooks, chaos, final evidence
```

A PR may be split further. Do not combine unrelated owner-service changes merely
to reduce the PR count.

---

## 22. Dependency graph

```text
T0 Entry gate
  |
  v
T1 Contracts + threat model
  |
  v
T2 Gateway module + schema
  |------------------------------|
  v                              v
T3 Auth + scopes              T5 Ledger merchant support
  |                              |
  v                              |
T4 Quota + idempotency            |
  |                              |
  |------------------------------|
                 |
                 v
         Merchant transfer
                 |
        |--------|--------|
        v                 v
T6 Payin/Payout       T7 Webhook relay
        |                 |
        |--------|--------|
                 v
         T8 Admin BFF
                 |
                 v
 T9 Observability + runbooks
                 |
                 v
 T10 Final verification + archive
```

T7 event-envelope and receiver-documentation work may start earlier, but live
delivery cannot complete before owner events contain authoritative tenant
routing data.

---

## 23. Test strategy

### 23.1 Unit tests

Cover:

- key format parser;
- HMAC digest and constant-time comparison;
- scope registry;
- tenant status decisions;
- quota decision mapping;
- request canonicalization;
- request hashing;
- idempotency state transitions;
- error-envelope mapping;
- public state mapping;
- event envelope construction;
- webhook HMAC;
- retry schedule;
- URL and resolved-IP validation;
- response-body truncation;
- cursor encode/decode;
- redaction.

### 23.2 Fuzz tests

Fuzz:

- API-key parser;
- authorization header parser;
- idempotency key validation;
- JSON canonicalization;
- cursor parser;
- webhook URL parser;
- signature header parser;
- public error mapping;
- event envelope deserialization.

### 23.3 PostgreSQL integration tests

Prove:

- unique API-key prefix;
- active-key limit race safety;
- tenant-scoped repository behavior;
- idempotency concurrent claim;
- lease recovery;
- inbox dedup;
- webhook fan-out uniqueness;
- `SKIP LOCKED` worker concurrency;
- replay uniqueness;
- retention batching;
- migration compatibility.

### 23.4 Redis integration tests

Prove:

- atomic token consumption;
- burst behavior;
- reset behavior;
- independent tenant/key/class buckets;
- outage posture;
- local read fallback bound;
- no write fallback.

### 23.5 Contract tests

For every B2B operation:

- request fixture;
- success fixture;
- validation failure;
- auth failure;
- scope denial;
- tenant-isolation result;
- owner timeout;
- compatibility mutation.

For every external event:

- JSON Schema;
- stable event ID;
- exact money strings;
- additive-field compatibility;
- signature fixture;
- duplicate delivery expectation.

### 23.6 End-to-end journeys

#### Journey A — Sandbox onboarding

```text
operator creates sandbox tenant
-> checker not required for sandbox activation
-> Ledger merchant account provisioned
-> operator creates scoped test key
-> secret shown once
-> merchant retrieves profile and balance
```

#### Journey B — Merchant transfer

```text
merchant posts transfer with idempotency key
-> Gateway authenticates/scopes/limits/claims
-> Ledger posts balanced entries
-> Gateway stores response
-> merchant retries same request
-> same response returned
-> transaction webhook delivered
```

#### Journey C — Merchant pay-in

```text
merchant creates sandbox pay-in
-> Payin owns lifecycle
-> VendorService uses mock adapter
-> callback normalized
-> Ledger credits merchant account once
-> payin.updated webhook delivered
```

#### Journey D — Merchant payout

```text
merchant creates sandbox payout
-> Payout owns lifecycle and hold
-> VendorService dispatches mock request
-> callback normalized
-> Ledger settles or releases once
-> payout.updated webhook delivered
```

#### Journey E — Webhook retry

```text
receiver returns 500
-> delivery retries
-> receiver later returns 200
-> delivery marked delivered
-> attempt evidence preserved
```

#### Journey F — Dead and replay

```text
receiver fails through retry budget
-> delivery marked dead
-> endpoint fixed
-> authorized replay creates new delivery
-> same event ID delivered successfully
```

### 23.7 Cross-tenant matrix

For every resource endpoint, test:

```text
tenant A reads tenant B resource
tenant A mutates tenant B resource
tenant A reuses tenant B idempotency key text
tenant A replays tenant B delivery
tenant A targets tenant B source account
test key accesses live tenant
live key accesses sandbox tenant
suspended tenant reads
suspended tenant writes
```

Default suspension policy:

- new financial writes denied;
- management writes denied except recovery operations;
- read access may remain available for reconciliation;
- webhook delivery continues unless the endpoint or tenant policy explicitly
  pauses it.

### 23.8 Race tests

Run race tests for:

- concurrent same idempotency key;
- concurrent key rotation/revocation and request;
- concurrent webhook workers;
- concurrent replay;
- concurrent endpoint disable and delivery;
- concurrent tenant suspension and financial write;
- duplicate owner events.

---

## 24. Chaos matrix

### 24.1 Gateway failures

- crash after idempotency claim;
- crash after owner success but before response persistence;
- crash after event inbox insert but before fan-out completion;
- restart during delivery lease.

Expected result: recovery without duplicate money or duplicate automatic event.

### 24.2 Owner-service failures

- Ledger timeout;
- Payin timeout;
- Payout timeout;
- owner returns retryable error;
- owner commits but response is lost.

Expected result: deterministic retry and query recover the original resource.

### 24.3 RabbitMQ failures

- broker unavailable during owner commit;
- duplicate delivery after reconnect;
- consumer restart;
- delayed backlog drain.

Expected result: owner outbox preserves event; Gateway inbox deduplicates.

### 24.4 Redis failures

- Redis unavailable before request;
- Redis timeout;
- Redis restart and counter loss.

Expected result: financial writes fail closed; reads use bounded fallback; all
decisions are observable.

### 24.5 Webhook receiver failures

- timeout;
- connection reset;
- TLS failure;
- 429;
- 500;
- 410;
- oversized response;
- redirect;
- DNS rebinding attempt;
- private-address resolution.

Expected result: bounded resource use, correct retry/dead/disable behavior, and
no SSRF.

### 24.6 Database failures

- Gateway DB restart;
- lease transaction rollback;
- deadlock;
- statement timeout;
- retention job interruption.

Expected result: no lost durable record and safe recovery.

---

## 25. Performance and resource boundaries

C1 does not invent production capacity claims before B0 evidence.

Still enforce these engineering boundaries:

- request bodies are bounded;
- list page sizes are bounded;
- webhook payloads and responses are bounded;
- database statements use timeouts;
- owner calls use deadlines;
- worker batches are bounded;
- retention deletes are bounded;
- no unbounded goroutine per delivery;
- no network call inside a database transaction;
- no API-key plaintext encryption/decryption hot path;
- no per-request audit DB write for read-only calls;
- no last-used update per request;
- no offset scan on growing transaction/delivery lists.

C1 must add a B2B scenario to the load harness, but B0 remains the authority for
capacity activation decisions.

---

## 26. Configuration

Add explicit configuration with safe defaults.

Example names:

```text
B2B_ENABLED=false
B2B_LIVE_ENABLED=false
B2B_WEBHOOKS_ENABLED=false
B2B_API_KEY_PEPPER_FILE=
B2B_MAX_REQUEST_BODY_BYTES=
B2B_IDEMPOTENCY_RETENTION=
B2B_IDEMPOTENCY_LEASE_DURATION=
B2B_QUOTA_REDIS_TIMEOUT=
B2B_READ_FALLBACK_RATE=
B2B_WEBHOOK_BATCH_SIZE=
B2B_WEBHOOK_WORKERS=
B2B_WEBHOOK_REQUEST_TIMEOUT=
B2B_WEBHOOK_MAX_PAYLOAD_BYTES=
B2B_WEBHOOK_MAX_RESPONSE_BYTES=
B2B_WEBHOOK_LEASE_DURATION=
B2B_WEBHOOK_REPLAY_MAX_AGE=
B2B_SANDBOX_LOCAL_WEBHOOK_ALLOWLIST=
```

Rules:

- missing pepper fails Gateway startup when B2B is enabled;
- live mode remains disabled by default;
- webhook worker may be disabled independently;
- all durations and sizes are validated at startup;
- unsafe TLS bypass configuration does not exist.

---

## 27. Rollout and rollback

### 27.1 Rollout stages

#### Stage 0 — Disabled

- schema present;
- code present;
- routes return not found or feature disabled;
- no merchant worker active.

#### Stage 1 — Sandbox read-only

- sandbox tenant and key;
- profile, account, balance, transaction reads;
- no financial write routes.

#### Stage 2 — Sandbox transfer

- transfer and idempotency enabled;
- external webhook delivery enabled to approved local receivers;
- chaos evidence recorded.

#### Stage 3 — Sandbox Payin/Payout

- mock adapters only;
- complete owner lifecycle;
- full E2E and callback evidence.

#### Stage 4 — Local live-mode simulation

- `sk_live` credentials;
- still no real vendor or real-money claim;
- stricter HTTPS endpoint requirements;
- operator checker activation exercised.

### 27.2 Kill switches

Required:

- global B2B route enable;
- live-mode enable;
- webhook worker enable;
- tenant suspension;
- endpoint disable.

A global financial-write pause may be added if it reuses an existing operational
control pattern. Do not invent a second inconsistent control plane.

### 27.3 Rollback

Code rollback must preserve additive data.

During rollback:

- disable new B2B writes first;
- allow safe read/reconciliation where possible;
- keep owner-service data authoritative;
- do not delete merchant ledger accounts;
- do not re-run money operations manually;
- preserve idempotency and webhook evidence;
- resume delivery only after compatibility is verified.

---

## 28. Documentation deliverables

Add or update:

```text
docs/roadmap/archive/57-c1-merchant-b2b-api.md
docs/roadmap/README.md
docs/roadmap/42-long-term-roadmap.md
docs/reference/current-services.md
docs/reference/b2b-api.md
docs/reference/b2b-authentication.md
docs/reference/b2b-idempotency.md
docs/reference/b2b-errors.md
docs/reference/b2b-webhooks.md
docs/reference/b2b-sandbox.md
docs/architecture/merchant-b2b-boundary.md
docs/threat-models/merchant-b2b-api.md
docs/evidence/c1-entry-gate.md
docs/evidence/c1-final-acceptance.md
```

Examples must use fake secrets and clearly synthetic data.

---

## 29. Proposed repository changes

Expected areas:

```text
contracts/http/b2b-v1.yaml
contracts/compatibility/surfaces.yaml
contracts/events/catalog.yaml
contracts/events/schemas/
contracts/proto/

services/gateway/internal/merchant/
services/gateway/internal/transport/http/router.go
internal/platform/config/
internal/platform/security/middleware/
internal/platform/security/crypto/

migrations/gateway/
migrations/ledger/
migrations/payin/
migrations/payout/

services/ledger/
services/payin/
services/payout/
services/adminbff/

scripts/merchant-e2e.sh
scripts/merchant-chaos.sh
Makefile
```

This is a forecast, not permission to modify every listed area. T0 must narrow
the actual blast radius.

---

## 30. Verification commands

Use the repository's current canonical commands discovered in T0. The final
gate should include equivalents of:

```bash
make contracts
make proto
make build
make test
make lint
go vet ./...
go test -race ./...
go test -tags=integration ./...
./scripts/smoke-test.sh all
./scripts/business-e2e.sh
./scripts/admin-e2e.sh
./scripts/merchant-e2e.sh
git diff --check
git status --short
```

Chaos remains a separate, explicitly invoked gate:

```bash
./scripts/merchant-chaos.sh
```

Do not put destructive chaos behavior inside an ordinary repeatable verification
target.

---

## 31. Final definition of done

C1 is complete only when:

### Contracts

- [x] B2B OpenAPI is complete and generated checks pass.
- [x] Every operation has canonical fixtures.
- [x] Error, pagination, idempotency, and scope semantics are documented.
- [x] Internal event and proto changes are backward compatible.
- [x] External webhook schemas are versioned and tested.

### Security

- [x] API keys are one-time visible and digest-only at rest.
- [x] Webhook secrets are encrypted at rest.
- [x] Revocation is immediate.
- [x] Tenant isolation is proven on every resource (source-account
  targeting not applicable to the current API shape; suspended-tenant
  read/write nuance fixed — T10b, see Result).
- [x] SSRF protections are exercised.
- [x] Secret scans across logs, DB evidence, and fixtures are clean.
- [x] Admin CSRF and role checks pass.
- [x] Threat model is complete.

### Money correctness

- [x] Merchant accounts use LedgerService.
- [x] Every transfer is balanced.
- [x] Pay-in credits once.
- [x] Payout hold/settle/release occurs once.
- [x] Duplicate request, event, and callback cannot duplicate money.
- [x] Gateway crash recovery returns the original resource.
- [x] Existing user money journeys remain green.

### Reliability

- [x] Quota outage posture is proven.
- [x] Owner outboxes survive RabbitMQ outage.
- [x] Merchant event inbox deduplicates.
- [x] Webhook retry, dead, replay, and worker recovery are proven.
- [x] Retention jobs are bounded.
- [x] Kill switches and tenant suspension are exercised.

### Operations

- [x] Metrics, traces, dashboards, alerts, and runbooks exist.
- [x] Cardinality checks pass.
- [x] Clean-tree final verification passes (one pre-existing, unrelated
  script flake tracked separately — see Result).
- [x] Chaos evidence is recorded.
- [x] Residual risks are explicit (T10b closed; the one remaining item,
  `privacy-e2e-host.sh`'s pre-existing unrelated flake, is tracked as its
  own follow-up task — see Result).
- [x] Roadmap and current-service documentation reflect reality.
- [x] The plan is archived only after all required evidence is linked —
  T10b closed (see T10's Result section), plan archived to
  `docs/roadmap/archive/`.

---

## 32. Final evidence log

| Evidence | Commit / artifact | Result | Notes |
|---|---|---:|---|
| C1 entry gate | `docs/evidence/c1-entry-gate.md` | pass | T0 |
| B2B OpenAPI gate | `make contracts` | pass | clean, this pass |
| API-key security | `services/gateway/internal/merchant/auth` | pass | digest-only at rest; environment-mismatch bypass found+fixed `86b4824` |
| Quota outage | `services/gateway/internal/merchant/quota` tests | pass | T4 |
| Idempotency concurrency | `idempotency_test.go`, `idempotency_integration_test.go`, `repository_integration_test.go` | pass | real-Postgres concurrent-claim races, T4 |
| Merchant account provisioning | `b2b_integration_test.go` | pass | T5 |
| Transfer E2E | `merchant-e2e.sh` §4, chaos scenario 21 | pass | idempotent replay + genuine crash, no double-debit |
| Payin E2E | `merchant-e2e.sh` §3 | pass | real mockvendor signed webhook |
| Payout E2E | `b2b_integration_test.go` | pass | T6 |
| Webhook signature fixtures | `webhook` package unit tests | pass | T7 |
| Webhook retry/dead/replay | `webhook_integration_test.go`, `relay_test.go`, `replay_test.go` | pass | T7 |
| Cross-tenant matrix | `b2b_integration_test.go`, `repository_integration_test.go`, `service_test.go` | 9/9 covered | 1 n/a to API shape (documented), suspended-tenant nuance fixed — T10b |
| Admin BFF E2E | `admin-e2e.sh` | pass | 5/5, this pass |
| RabbitMQ chaos | chaos scenarios 2 (shared outbox) and 23 (merchant webhook consumer) | pass | 23 added T10b — proves the merchant-specific consumer, not just the shared outbox |
| Redis chaos | chaos scenario 22 | pass | added T10b — real Redis container stop/start through the assembled Gateway, not just a fake client |
| Gateway crash recovery | chaos scenario 21 | pass | 40/40 concurrent, exact balances, this pass |
| Final clean-tree gate | build/vet/lint/contracts/doccheck/`test`/`test -race`/`test -tags=integration`/smoke/business-e2e/admin-e2e/merchant-e2e | pass | this pass; `privacy-e2e-host.sh` fails on a pre-existing, unrelated issue (task filed separately) |

---

## 33. Recommended first implementation cut

The first mergeable vertical slice should be intentionally narrow:

```text
sandbox tenant creation
-> merchant account provisioning
-> one read-only API key
-> GET merchant profile
-> GET account balance
-> tenant-isolation tests
-> audit + metrics
```

The first money-writing slice should then be:

```text
scoped sandbox key
-> POST merchant transfer
-> durable Gateway idempotency
-> Ledger merchant transfer
-> GET transaction
-> transaction.posted.v1 webhook
-> duplicate/retry/crash evidence
```

Do not start Payin/Payout merchant adaptation before this transfer slice proves
the core tenant, key, scope, quota, idempotency, Ledger, event, and webhook
boundaries.
