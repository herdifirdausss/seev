# Plan 59 — C3 Multi-Channel Notifications

**Created:** 2026-07-28
**Status:** In progress — implementation present; acceptance evidence pending
**Roadmap track:** C3 — Multi-channel notifications
**Activation trigger:** Conscious user-facing delivery-pipeline learning decision
**Primary owner:** Gateway `internal/notify`
**Supporting owner:** AuthService for verified email resolution
**Operator surface:** Admin BFF
**Initial channels:** In-app, email through local SMTP, push through a local mock provider
**Delivery model:** At-least-once external delivery, exactly-once logical in-app creation
**No paid provider and no new application service are authorized by this plan.**

**Current implementation note:** The C3 code, migrations, local providers,
operational artifacts, and reference documents are implemented in the
dedicated worktree. The entry-gate, cutover, and final-acceptance records
intentionally remain pending because this pass changes code only; see
[C3 entry-gate evidence](../../evidence/c3-entry-gate.md) and
[C3 final acceptance](../../evidence/c3-final-acceptance.md).

---

## 1. Purpose

Expand Seev's existing Gateway-owned in-app notification module into a durable,
versioned, preference-aware, multi-channel delivery platform.

C3 must add:

- a stable notification-kind registry;
- versioned and reviewable templates;
- localized in-app, email, and push rendering;
- user notification settings and per-category preferences;
- verified email resolution through AuthService;
- encrypted push-device endpoint registration;
- durable per-channel delivery records;
- independent channel workers;
- bounded retries and dead delivery;
- daily email digest delivery;
- quiet hours and timezone handling;
- operator template publishing, channel controls, delivery inspection, and
  replay;
- privacy, retention, observability, and failure-recovery evidence;
- compatibility with the current in-app inbox and its public API.

The implementation must preserve these principles:

1. Gateway remains the owner of user-facing notifications.
2. No tenth application service is created.
3. Money owners continue to publish domain facts, not user-facing prose.
4. Notification failure never changes financial state.
5. RabbitMQ remains at-least-once.
6. One domain event may create notifications for multiple recipients.
7. Duplicate domain-event delivery must not create duplicate logical
   notifications.
8. External email and push delivery are at-least-once and may be duplicated
   across an unavoidable provider-acceptance crash window.
9. A template update never changes an already-rendered delivery.
10. User preferences never erase or rewrite financial history.
11. Mandatory in-app financial and security notices cannot be disabled.
12. Email addresses and push tokens are never logged or stored in plaintext
    beyond the minimum encrypted delivery/contact snapshot.
13. Notification rendering may not use arbitrary raw event payloads.
14. No provider call occurs inside a database transaction.
15. No money request waits for notification delivery.
16. Existing Gateway notification endpoints remain backward compatible.
17. Existing retention and privacy behavior from A8 remains in force.
18. Paid email, push, SMS, WhatsApp, and production deliverability work are
    outside the learning baseline.

---

## 2. Current-state baseline

At plan creation, Gateway already owns:

```text
internal/notify
migrations/gateway
seev_gateway.notif_notifications
GET  /api/v1/notifications
POST /api/v1/notifications/{id}/read
```

The current consumer:

- binds queue `ledger.events.notifications`;
- consumes `ledger.transaction.posted.v1`;
- recognizes:
  - `money_in`;
  - `transfer_p2p`;
  - `withdraw_settle`;
  - `withdraw_cancel`;
- generates hard-coded English title/body text;
- creates one row per recipient;
- uses `UNIQUE(event_id, user_id)` as the at-least-once deduplication guard;
- stores the RabbitMQ body in `payload`;
- exposes a keyset-paginated in-app inbox;
- allows mark-read only for the authenticated owner;
- does not yet have templates, preferences, email, push, digest, or
  per-channel delivery evidence.

C3 evolves this implementation. It must not present the current foundation as
missing or rebuild it from scratch.

---

## 3. Activation and entry gate

### 3.1 Activation decision

C3 is activated on 2026-07-28 as a conscious learning decision for:

- notification-domain modeling;
- template governance;
- asynchronous fan-out;
- SMTP integration;
- push-provider abstraction;
- per-channel retry;
- user preferences;
- quiet hours;
- digest scheduling;
- operational replay;
- privacy-safe recipient handling.

This trigger is sufficient under Plan 42.

### 3.2 Required entry-gate evidence

T0 must record the current result of all items below.

- [ ] `make contracts` passes from a clean tree.
- [ ] Existing event catalog and JSON Schema checks pass.
- [ ] Existing Gateway migrations and integration tests pass.
- [ ] Existing notification list and mark-read tests pass.
- [ ] Existing business E2E produces the expected in-app notifications.
- [ ] Existing notification-retention and privacy tests pass.
- [ ] Current RabbitMQ queue, exchange, routing keys, retry, and DLQ behavior
      are documented.
- [ ] Current event payload fields used by notifications are inventoried.
- [ ] Current notification API response shape is recorded.
- [ ] Current Gateway migration head is recorded.
- [ ] Current Auth user/email model and verification semantics are recorded.
- [ ] Existing encryption and secret-file utilities are inventoried.
- [ ] Existing Admin BFF Gateway proxy and audit behavior is recorded.
- [ ] Existing notification SLO, metric, dashboard, and alert definitions are
      recorded.
- [ ] The exact baseline commit is recorded.
- [ ] There is no unrelated event, notification, or identity migration in
      flight.

### 3.3 Gate policy

The following work may begin before the entry gate is fully green:

- architecture documentation;
- template and preference contract design;
- provider interfaces;
- Mailpit and mock-push Compose scaffolding;
- synthetic rendering fixtures;
- schema drafts;
- threat modeling.

The following may not merge before the gate is green:

- a replacement event consumer;
- a destructive change to `notif_notifications`;
- an external delivery worker;
- a verified-email lookup contract;
- a public preference/device endpoint;
- a template publishing admin endpoint;
- a new routing key bound to the production-style notification queue.

---

## 4. Locked architecture decisions

## 4.1 Keep notifications inside Gateway

C3 does not create `NotificationService`.

The current nine-service topology remains the baseline. Gateway already owns:

- the public user API;
- the in-app notification inbox;
- the RabbitMQ notification consumer;
- the `seev_gateway` database;
- JWT user context;
- public notification routes;
- notification-lag observability.

C3 expands `internal/notify` into a larger but still bounded Gateway module.

A future extraction requires separate evidence such as:

- Gateway deployment scaling being dominated by notification workers;
- a different ownership or security boundary;
- independently measured release or availability requirements;
- sustained provider throughput that cannot be isolated by worker/config
  controls inside Gateway.

### 4.2 Preserve the module facade

External Gateway code continues importing only:

```text
internal/notify
```

Suggested internal layout:

```text
internal/notify/
├── notify.go                 # public module facade and lifecycle wiring
├── http.go                   # user-facing HTTP surface
├── admin_http.go             # Gateway-owned admin API
├── ingestion/                # AMQP decode, validation, inbox, normalization
├── registry/                 # notification kinds and policy metadata
├── planner/                  # recipient fan-out, preference and template plan
├── template/                 # versions, approval, render, preview
├── preference/               # user settings, category/channel policy
├── contact/                  # Auth verified-contact resolver
├── delivery/                 # lease, retry, replay, status machine
├── channel/
│   ├── inapp/
│   ├── email/
│   └── push/
├── digest/                   # daily window scheduler and renderer
├── retention/                # cleanup and recipient-secret erasure
├── privacy/                  # export/pseudonymization/redaction integration
├── observability/
├── model/
└── repository/
```

Boundary tests must prevent another module from importing private
notification subpackages.

### 4.3 Domain events remain factual

Upstream services publish domain facts.

They may not publish:

```text
title
email subject
HTML body
localized user-facing prose
provider-specific payload
template version
marketing copy
```

Gateway maps domain events into a stable notification kind and typed rendering
context.

### 4.4 No generic arbitrary-send API

C3 does not expose an internal endpoint such as:

```text
POST /send-notification
```

that accepts arbitrary recipient, title, body, or HTML.

Notifications must originate from:

- an allowlisted versioned domain event;
- an explicitly defined system notification command with a reviewed contract;
- an operator test-delivery fixture restricted to a local development sink.

This prevents the notification platform from becoming an ungoverned messaging
or phishing surface.

### 4.5 Current Ledger event is the first source

The first C3 vertical slice continues using:

```text
ledger.transaction.posted.v1
```

Later sources are activated only after canonical contracts exist.

Candidate later sources:

```text
payin lifecycle event
payout lifecycle event
KYC lifecycle event
security/login event
merchant lifecycle event after C1
```

C3 must not guess event names or fields. T0/T1 must bind only to event catalog
entries that exist or are added through A9-compatible evolution.

### 4.6 Authoritative source per notification kind

One user-visible kind has one authoritative source.

Examples:

| Notification kind | Authoritative fact owner |
|---|---|
| `money.transfer.sent` | Ledger posted transaction |
| `money.transfer.received` | Ledger posted transaction |
| `money.topup.succeeded` | Ledger posted money-in transaction |
| `money.payout.succeeded` | Ledger posted withdrawal settlement |
| `money.payout.cancelled` | Ledger posted withdrawal cancellation/release |
| `money.payout.failed_before_posting` | Payout lifecycle event, if introduced |
| `account.kyc.approved` | Auth KYC lifecycle event, if introduced |
| `security.login.new_device` | Auth security event, if introduced |

C3 must not send the same semantic message from both an owner lifecycle event
and a Ledger event.

### 4.7 Two-stage asynchronous design

The delivery pipeline is split.

```text
Domain event
    |
    v
RabbitMQ consumer
    |
    v
Gateway DB transaction:
event inbox
-> normalized recipient notification
-> rendered in-app snapshot
-> durable external delivery plans
    |
    +--> ACK RabbitMQ
    |
    v
Independent email/push/digest workers
    |
    v
Local provider adapters
```

The RabbitMQ handler does not call:

- AuthService;
- SMTP;
- push provider;
- external HTTP;
- Metabase;
- any downstream service.

### 4.8 In-app is the durable primary channel

In-app remains the primary, queryable user notification record.

For mandatory financial/security kinds:

- in-app creation is always enabled;
- user preference cannot suppress it;
- external-channel failure does not hide the in-app notice;
- read status remains separate from external delivery status.

### 4.9 External delivery is at-least-once

Email and push delivery may duplicate if the process crashes after provider
acceptance but before the delivered state commits.

Mitigations:

- stable delivery ID;
- stable email `Message-ID`;
- provider idempotency key where supported;
- exact rendered payload reuse;
- attempt history;
- deduplicating local mock provider;
- explicit documentation.

C3 does not claim exactly-once email or push.

### 4.10 Local provider baseline

Email:

```text
SMTP adapter -> Mailpit
```

Push:

```text
provider-neutral HTTP adapter -> repository-local mock push provider
```

No paid account is required.

C3 does not integrate:

- Amazon SES;
- SendGrid;
- Mailgun;
- Postmark;
- Firebase Cloud Messaging production project;
- Apple Push Notification service production credentials.

### 4.11 No SMS or WhatsApp

C3 channels are:

```text
in_app
email
push
```

SMS, WhatsApp, voice, and social messaging require separate cost, privacy, and
provider evidence.

### 4.12 Polling remains sufficient for in-app

C3 does not add WebSocket, SSE, or realtime socket infrastructure.

The client may poll:

```text
GET /api/v1/notifications
GET /api/v1/notifications/unread-count
```

Realtime transport requires separate evidence.

---

## 5. Notification domain model

## 5.1 Stable notification kind

A notification kind is a versioned semantic identifier independent of source
event and channel.

Initial kinds:

```text
money.transfer.sent
money.transfer.received
money.topup.succeeded
money.payout.succeeded
money.payout.cancelled
```

Later kinds may include:

```text
money.payin.failed
money.payout.failed
account.kyc.approved
account.kyc.rejected
security.login.new_device
security.credential.changed
system.maintenance.notice
```

A kind name is never reused for different semantics.

### 5.2 Category

Initial categories:

```text
money_movement
account
security
compliance
system
```

Marketing is not part of C3.

### 5.3 Priority

```text
critical
high
normal
low
```

Policy:

- `critical`: never digested; may bypass quiet hours when an external channel
  is enabled by mandatory policy;
- `high`: immediate in-app; external delivery is immediate unless disabled;
- `normal`: immediate or daily digest;
- `low`: digest-eligible when supported.

Initial Ledger transaction-success notifications are `high`, not `critical`.

### 5.4 Delivery requirement

```text
mandatory
transactional
optional
```

Rules:

- `mandatory`: in-app cannot be disabled;
- `transactional`: in-app remains enabled; email/push preference may disable;
- `optional`: all supported channels may be disabled;
- no template may override the registry's requirement.

### 5.5 Delivery modes

```text
immediate
daily_digest
disabled
```

Channel support:

| Channel | immediate | daily_digest | disabled |
|---|---:|---:|---:|
| In-app | yes | no | only for optional kinds |
| Email | yes | yes | yes |
| Push | yes | no in C3 | yes |

### 5.6 Notification-kind registry

Implement a compile-time registry.

Each kind declares:

```text
kind
category
priority
delivery requirement
authoritative source event
recipient resolver
typed context builder
template variable schema version
default channel modes
quiet-hours behavior
digest eligibility
deep-link builder
privacy classification
retention class
```

The registry is the authoritative policy boundary.

Database templates cannot change:

- category;
- mandatory status;
- recipient;
- source event;
- deep-link target;
- privacy classification.

---

## 6. Typed rendering context

## 6.1 No raw-event template access

Templates may render only an approved typed context.

Example conceptual context for a transfer sender:

```json
{
  "notification_id": "uuid",
  "amount": {
    "minor": "125000",
    "currency": "IDR",
    "display": "IDR 125,000"
  },
  "transaction": {
    "id": "uuid",
    "posted_at": "2026-07-28T08:00:00Z"
  },
  "action": {
    "deep_link": "/transactions/uuid"
  }
}
```

The runtime may store a minimized canonical JSON representation, but rendering
code uses a kind-specific typed structure.

### 6.2 Forbidden context fields

Do not provide templates with:

```text
password
credential
API key
access token
refresh token
session ID
full idempotency key
bank account number
payout destination
KYC document
raw vendor request
raw vendor response
raw callback
internal service token
arbitrary HTML
arbitrary URL
```

### 6.3 Money formatting

Money formatting must:

- use exact integer/decimal source values;
- never convert through binary floating point;
- use currency-aware formatting;
- preserve source currency;
- fail closed on invalid amount/currency;
- be covered by fixtures;
- remain compatible with future C4 currencies.

### 6.4 Deep links

Deep links are built by registered code.

Templates may reference the prebuilt deep link but may not create an arbitrary
URL.

Initial internal paths:

```text
/transactions/{id}
/topups/{id}
/payouts/{id}
/notifications/{id}
/security
/profile/kyc
```

The frontend may map these paths later. C3 does not require a new frontend
implementation.

---

## 7. Template system

## 7.1 Template identity

A template is identified by:

```text
notification kind + channel + locale
```

Example:

```text
money.transfer.sent / in_app / en-US
money.transfer.sent / email / en-US
money.transfer.sent / push / en-US
```

### 7.2 Versioning

Versions are immutable positive integers.

```text
v1
v2
v3
```

Publishing a new version never modifies an older row.

### 7.3 Lifecycle

```text
draft
pending_approval
active
retired
rejected
```

Allowed flow:

```text
draft -> pending_approval
pending_approval -> active
pending_approval -> rejected
active -> retired
```

An active version cannot return to draft.

### 7.4 Maker/checker publication

Publishing a financial, security, or compliance template requires:

- creator/maker;
- separate approver/checker;
- content hash;
- preview fixture;
- rendered snapshot;
- audit record;
- activation transaction.

The same operator may not create and approve the same version.

Gateway enforces this owner-side, even though Admin BFF also enforces role and
audit controls.

### 7.5 Locale fallback

Resolution order:

```text
exact locale
-> language fallback if explicitly configured
-> repository default locale
```

Initial repository default:

```text
en-US
```

Recommended second locale:

```text
id-ID
```

Missing active in-app fallback is a blocking planning error.

Missing optional external-channel template creates a blocked delivery and an
alert rather than losing the in-app record.

### 7.6 Renderer

Use:

```text
text/template
html/template
```

Rules:

- `missingkey=error`;
- variables are typed;
- HTML variables are escaped;
- no arbitrary function registration;
- no filesystem include from user input;
- no network call;
- no dynamic code;
- no template-to-template recursion;
- maximum rendered size is bounded;
- deterministic output for the same template version and context;
- line endings are normalized.

### 7.7 Allowed helper functions

Keep the allowlist small:

```text
formatMoney
formatDate
formatDateTime
upper
lower
title
```

Every helper is deterministic and locale-aware.

Do not expose:

```text
env
file
exec
HTTP
SQL
reflection
random
current time
```

### 7.8 Channel fields

#### In-app

```text
title
body_text
```

#### Email

```text
subject
body_text
body_html
```

#### Push

```text
title
body_text
```

Push content follows the privacy-safe preview policy.

### 7.9 Render snapshot

Every immediate delivery stores:

```text
template_version_id
locale
rendered_subject
rendered_title
rendered_text
rendered_html
rendered_payload_bytes or canonical payload JSON
content_hash
```

Retries reuse these exact values.

### 7.10 Template preview

Admin preview uses only:

- repository fixtures;
- manually supplied schema-validated synthetic values;
- no live user lookup;
- no arbitrary recipient;
- no send to a real provider.

### 7.11 Template compatibility

A new template version must pass:

- variable-schema validation;
- all required variables used correctly;
- no unknown variable;
- render fixtures;
- size limit;
- HTML safety;
- email header safety;
- deep-link policy;
- snapshot diff;
- locale completeness for mandatory kinds.

---

## 8. Public API contract

Existing routes remain.

### 8.1 List notifications

```text
GET /api/v1/notifications
```

Supported query parameters:

```text
limit
before
unread
category
kind
```

Rules:

- keyset pagination;
- own rows only;
- default 50;
- maximum 200;
- deterministic order by `(created_at, id)`;
- unknown category/kind is a stable validation error.

Existing response fields remain compatible.

Additive fields may include:

```text
kind
category
priority
deep_link
read_at
created_at
```

Do not expose raw source-event payload.

### 8.2 Notification detail

```text
GET /api/v1/notifications/{id}
```

Own row only.

Another user's ID returns the same not-found response as a missing ID.

### 8.3 Unread count

```text
GET /api/v1/notifications/unread-count
```

Response:

```json
{
  "count": 3
}
```

No unbounded scan.

### 8.4 Mark one read

Preserve:

```text
POST /api/v1/notifications/{id}/read
```

Idempotent.

### 8.5 Mark all read

Add:

```text
POST /api/v1/notifications/read-all
```

Optional body:

```json
{
  "before": "2026-07-28T08:00:00Z"
}
```

The update is bounded by user and cutoff.

### 8.6 Notification settings

```text
GET /api/v1/notification-settings
PUT /api/v1/notification-settings
```

Fields:

```text
locale
timezone
daily_digest_hour
quiet_hours_start
quiet_hours_end
quiet_hours_enabled
version
```

Use optimistic concurrency with a version or ETag.

### 8.7 Preferences

```text
GET /api/v1/notification-preferences
PUT /api/v1/notification-preferences
```

Preference entry:

```json
{
  "category": "money_movement",
  "channel": "email",
  "mode": "daily_digest"
}
```

The response must show effective policy, including mandatory overrides.

### 8.8 Push-device endpoints

```text
GET    /api/v1/notification-devices
POST   /api/v1/notification-devices
DELETE /api/v1/notification-devices/{id}
```

Registration input:

```json
{
  "platform": "android",
  "token": "opaque-provider-token",
  "device_name": "My phone"
}
```

Rules:

- authenticated owner only;
- token never returned after registration;
- response returns token fingerprint suffix only;
- duplicate token registration is idempotent;
- maximum active devices per user is bounded;
- delete/revoke is idempotent;
- platform is allowlisted;
- body and token sizes are bounded.

### 8.9 Stable error envelope

Use the current Gateway error contract or an A9-compatible additive extension.

Required codes:

```text
NOTIFICATION_NOT_FOUND
NOTIFICATION_KIND_INVALID
NOTIFICATION_CATEGORY_INVALID
NOTIFICATION_SETTINGS_CONFLICT
NOTIFICATION_PREFERENCE_INVALID
NOTIFICATION_PREFERENCE_MANDATORY
NOTIFICATION_DEVICE_INVALID
NOTIFICATION_DEVICE_LIMIT
NOTIFICATION_DEVICE_NOT_FOUND
NOTIFICATION_CHANNEL_UNAVAILABLE
INTERNAL_ERROR
```

---

## 9. Auth verified-contact contract

## 9.1 Ownership

AuthService remains the owner of:

- email identity;
- email verification status;
- user account status.

Gateway must not query the Auth database.

### 9.2 Purpose-built internal endpoint

Add an internal, mTLS/service-identity protected contract.

Conceptual route:

```text
GET /internal/v1/users/{user_id}/notification-contact
```

Response:

```json
{
  "user_id": "uuid",
  "email": "user@example.test",
  "email_verified": true,
  "user_status": "active",
  "updated_at": "2026-07-28T08:00:00Z"
}
```

The exact transport may be typed HTTP or an additive existing internal
transport. T0 must choose the least disruptive current convention.

### 9.3 Contract rules

- only approved internal service identity may call it;
- no password or credential fields;
- no KYC document fields;
- not exposed as a public user endpoint;
- user not found and inactive semantics are stable;
- email must be verified for email delivery;
- response is not logged;
- access is traced and counted without user-ID metric labels;
- rate and timeout are bounded.

### 9.4 Resolution timing

Email deliveries begin in:

```text
pending_recipient
```

A resolver worker:

1. claims a bounded batch;
2. calls Auth outside a DB transaction;
3. validates active user and verified email;
4. encrypts the email recipient snapshot;
5. persists only ciphertext plus a fingerprint;
6. schedules the delivery;
7. suppresses or retries according to failure classification.

### 9.5 Recipient snapshot

The plaintext recipient exists only:

- in process memory during resolution/delivery;
- in the SMTP request.

At rest, store:

```text
recipient_ciphertext
recipient_key_version
recipient_fingerprint
recipient_resolved_at
```

Retries reuse the same encrypted recipient snapshot.

A new notification resolves the current verified address.

### 9.6 Auth outage

Auth outage:

- does not block in-app creation;
- leaves email delivery pending;
- retries with bounded backoff;
- triggers contact-resolution lag metrics;
- never causes the domain event to be reprocessed merely to fetch email.

---

## 10. User settings and preference policy

## 10.1 Settings owner

Gateway owns notification-specific settings:

```text
locale
timezone
quiet hours
daily digest time
channel/category preferences
```

Auth continues owning identity contact.

### 10.2 Defaults

Initial defaults:

```text
locale: en-US
timezone: Asia/Jakarta
quiet hours: disabled
daily digest hour: 08:00 local
```

Defaults are configuration-backed and documented.

### 10.3 Effective-preference calculation

Order:

```text
kind mandatory policy
-> category/channel user preference
-> channel capability
-> endpoint/contact availability
-> global channel control
-> quiet hours
-> delivery mode
```

Mandatory policy wins over user attempts to disable mandatory in-app delivery.

### 10.4 Unknown kind safety

If a new kind reaches the planner before preference metadata exists:

- mandatory safe fallback: in-app only;
- external email/push disabled;
- alert emitted;
- no arbitrary default external send.

### 10.5 Preference evaluation moments

Evaluate twice.

#### Planning time

Determines whether a delivery plan or digest item should be created.

#### Dispatch time

For non-mandatory external delivery, re-check current preference before
provider call.

This ensures a recent opt-out suppresses a pending external message.

### 10.6 Quiet hours

Quiet hours apply to email and push.

Rules:

- stored as local wall-clock range plus IANA timezone;
- cross-midnight ranges supported;
- in-app is not delayed;
- critical mandatory external notification may bypass if registry policy says
  so;
- high/normal delivery is moved to the next allowed time;
- retry never schedules earlier than the quiet-hours boundary;
- timezone validation uses the Go timezone database;
- invalid timezone is rejected.

### 10.7 Preference changes

- versioned optimistic concurrency;
- audited in structured security log without raw contact values;
- no retroactive deletion of in-app history;
- pending optional external deliveries may be suppressed;
- already accepted provider deliveries cannot be recalled.

---

## 11. Push-device endpoint design

## 11.1 Ownership

Gateway owns push-delivery endpoints because they are channel-specific and
revocable independently from user identity.

### 11.2 Platforms

Initial allowlist:

```text
android
ios
web
test
```

The local mock provider supports all values.

No production FCM/APNs implementation is required.

### 11.3 Token storage

Store:

```text
encrypted token
encryption key version
HMAC fingerprint
last four display characters where safe
platform
device name
status
last successful delivery
last failure
created_at
updated_at
revoked_at
```

Do not store plaintext token.

### 11.4 Unique identity

Use a keyed HMAC fingerprint.

Uniqueness:

```text
user_id + token_fingerprint
```

A token may not silently move between users.

A detected cross-user token conflict is rejected and security-logged.

### 11.5 Token lifecycle

```text
active
invalid
revoked
```

Permanent provider invalid-token response:

- marks endpoint invalid;
- suppresses future deliveries;
- does not delete attempt evidence;
- appears in user device list without token.

### 11.6 Device limit

Initial limit:

```text
10 active devices per user
```

Configurable but bounded.

### 11.7 Push privacy

Default push preview must not expose sensitive financial detail on a lock
screen.

Default content example:

```text
Title: Transaction update
Body: Open Seev to view the details.
```

Provider data payload may contain:

```text
notification_id
kind
deep_link
```

It may not contain:

```text
amount
balance
account number
recipient name
raw event
credential
```

Detailed push preview is out of scope.

---

## 12. Event ingestion and normalization

## 12.1 Event inbox

Add a durable event inbox before planning.

The inbox records:

```text
source event ID
source event type
source service
schema version
payload hash
received_at
processed_at
planning status
error code
```

Unique:

```text
source_service + event_id
```

The raw event body must not be retained indefinitely.

Store either:

- a minimized approved canonical payload; or
- a short-lived encrypted raw payload only when required for recovery.

Preferred design: validate and normalize within the same transaction, then
retain only payload hash and approved context.

### 12.2 Consumer transaction

For one valid event:

1. insert inbox row;
2. if duplicate, return success;
3. validate event contract;
4. map to zero or more recipient notification specs;
5. validate recipient IDs;
6. build typed contexts;
7. resolve current template bindings;
8. evaluate preferences without network calls;
9. render in-app and immediate external snapshots;
10. create one logical notification per recipient/kind;
11. create channel delivery/digest plans;
12. mark inbox processed;
13. commit;
14. ACK RabbitMQ.

### 12.3 Dedup identity

Logical notification uniqueness:

```text
source_event_id + user_id + notification_kind
```

This replaces the narrower legacy assumption while preserving current
behavior.

### 12.4 Recipient fan-out

For `transfer_p2p`:

```text
source user -> money.transfer.sent
target user -> money.transfer.received
```

Each recipient receives:

- independent notification ID;
- independent read state;
- independent preferences;
- independent channel deliveries;
- independent digest membership.

### 12.5 Malformed event

Malformed or schema-invalid event:

- no notification rows;
- no provider call;
- message follows current retry/DLQ policy;
- structured error omits raw sensitive payload;
- metric and alert are emitted;
- admin diagnostic exposes event ID/type/error, not secrets.

### 12.6 Unknown transaction type

Unknown/not-notifiable Ledger transaction type:

- ACK;
- no user notification;
- increment a bounded `filtered` metric;
- not treated as error.

Unknown event contract bound to the queue:

- safe failure and DLQ;
- alert for routing/configuration mistake.

### 12.7 Queue evolution

Phase 1 keeps the current queue and Ledger routing key.

When the first non-Ledger event source is activated:

1. create `user.notifications.events.v1`;
2. bind explicit allowlisted routing keys;
3. use event-inbox deduplication;
4. perform controlled dual-binding;
5. compare planned counts;
6. stop the legacy consumer;
7. drain or archive the legacy queue;
8. update runbooks and dashboards.

Do not rename/delete the current queue casually.

---

## 13. Delivery data model and state machine

## 13.1 Delivery statuses

```text
pending_recipient
scheduled
processing
retry_wait
delivered
suppressed
blocked
dead
cancelled
```

### 13.2 Valid transitions

```text
pending_recipient -> scheduled
pending_recipient -> retry_wait
pending_recipient -> suppressed
pending_recipient -> dead

scheduled -> processing
processing -> delivered
processing -> retry_wait
processing -> dead
processing -> suppressed

retry_wait -> processing
retry_wait -> suppressed
retry_wait -> dead

blocked -> scheduled
blocked -> suppressed

dead -> scheduled         # authorized replay
```

Terminal without replay:

```text
delivered
suppressed
cancelled
```

### 13.3 Channel delivery uniqueness

```text
notification_id + channel + endpoint_identity
```

For email, endpoint identity is the resolved-contact slot.

For push, one logical notification may have one delivery per active device.

### 13.4 Worker leasing

Use PostgreSQL:

```sql
SELECT ...
FOR UPDATE SKIP LOCKED
LIMIT ...
```

Store:

```text
lease_owner
lease_expires_at
attempt_count
next_attempt_at
```

Requirements:

- bounded batch;
- bounded workers;
- lease longer than provider timeout;
- expired lease recovery;
- graceful shutdown;
- no network call while row lock/transaction remains open;
- no two workers reuse one attempt number.

### 13.5 Attempt evidence

Each provider attempt stores:

```text
delivery_id
attempt_number
started_at
finished_at
result
provider
provider_message_id where safe
status code/class
error code
duration
response excerpt
```

Response excerpt:

- sanitized;
- bounded;
- no recipient;
- no token;
- no provider secret.

### 13.6 Crash after provider acceptance

If a worker crashes after provider acceptance but before committing delivered
state:

- lease eventually expires;
- another worker retries;
- a duplicate external message is possible;
- stable delivery ID and provider idempotency key are reused;
- attempt history records the abandoned lease where detectable;
- runbook explains the uncertainty.

No automated financial action depends on delivery count.

---

## 14. In-app channel

## 14.1 Backward-compatible record

The current `notif_notifications` remains the user inbox table.

C3 extends it additively.

The current fields remain:

```text
id
user_id
event_id
type
title
body
payload
read_at
created_at
```

### 14.2 New fields

Expected additive fields:

```text
event_type
source_service
kind
category
priority
requirement
locale
template_version_id
deep_link
context
content_hash
expires_at
updated_at
```

T0 must confirm names and migration head.

### 14.3 Legacy fields

`type` remains for compatibility during C3.

New code uses `kind`.

`payload` becomes legacy and is not populated with raw domain-event bodies for
new rows.

A later plan may remove legacy fields only through an A9-compatible
expand/contract process.

### 14.4 Read semantics

- mark-read is idempotent;
- mark-all-read is user-scoped;
- no unread state is shared across recipients;
- external delivery does not mark in-app read;
- retention does not depend solely on read status for mandatory financial
  records.

### 14.5 In-app delivery evidence

Create an `in_app` delivery row in terminal `delivered` state in the same
transaction as notification creation.

This gives uniform channel reporting without adding a provider worker.

### 14.6 API privacy

Do not return:

```text
raw event payload
recipient ciphertext
device token
provider response
template internals
operator audit details
```

---

## 15. Email channel

## 15.1 Local provider

Use Mailpit in an optional Compose profile.

Suggested profile:

```text
notifications
```

Mailpit provides:

- local SMTP sink;
- browser inspection;
- deterministic learning environment;
- no paid provider;
- no internet delivery.

### 15.2 SMTP adapter

Implement a narrow interface:

```go
type EmailSender interface {
    Send(ctx context.Context, message EmailMessage) (ProviderResult, error)
}
```

`EmailMessage` contains already-rendered safe content.

### 15.3 Email headers

Set from configuration:

```text
From
Reply-To where approved
Message-ID
Date
MIME-Version
Content-Type
X-Seev-Delivery-ID
X-Seev-Notification-ID
```

Rules:

- user/template variables cannot create header names;
- subject rejects CR/LF;
- from/reply-to are not template-controlled;
- stable `Message-ID` derives from delivery ID;
- no BCC;
- recipient is one resolved verified address;
- no recipient list in logs.

### 15.4 Email content

Multipart alternative:

```text
text/plain
text/html
```

The HTML renderer escapes variables.

No remote tracking pixel.

No click tracking.

No open tracking.

No hidden analytics identifier.

### 15.5 SMTP classification

Transient:

```text
connection timeout
connection reset
temporary DNS failure
SMTP 4xx
provider unavailable
```

Permanent:

```text
invalid recipient format
unverified contact
SMTP permanent recipient rejection
rendered message invalid
```

Local Mailpit acceptance counts as provider accepted, not real mailbox
delivery.

### 15.6 Email retry schedule

Initial default:

```text
attempt 1: immediate
attempt 2: +1 minute
attempt 3: +5 minutes
attempt 4: +30 minutes
attempt 5: +2 hours
attempt 6: +8 hours
attempt 7: +24 hours
```

After the final retry:

```text
dead
```

### 15.7 Email suppression

Suppress when:

```text
user inactive
email missing
email unverified
preference disabled
global channel paused
recipient permanently rejected
retention deadline exceeded
```

A future production provider bounce/complaint callback is out of scope.

### 15.8 Email security

- TLS required outside local Mailpit;
- no `InsecureSkipVerify` configuration;
- credentials loaded from secret files;
- SMTP password never logged;
- maximum message size bounded;
- body generation before provider call;
- provider timeout bounded;
- no attachment support in C3.

---

## 16. Push channel

## 16.1 Adapter

Implement:

```go
type PushSender interface {
    Send(ctx context.Context, message PushMessage) (ProviderResult, error)
}
```

### 16.2 Local mock provider

Add a repository-local mock push provider as a development utility/profile,
not a new business service.

Conceptual endpoint:

```text
POST /v1/messages
```

Input:

```json
{
  "delivery_id": "uuid",
  "token": "opaque-token",
  "platform": "android",
  "notification": {
    "title": "Transaction update",
    "body": "Open Seev to view the details."
  },
  "data": {
    "notification_id": "uuid",
    "kind": "money.transfer.sent",
    "deep_link": "/transactions/uuid"
  }
}
```

### 16.3 Mock behavior

Deterministic token prefixes may simulate:

```text
success
transient failure
timeout
invalid token
rate limit
provider 5xx
duplicate idempotency key
```

The mock provider:

- deduplicates by delivery ID;
- records accepted messages for E2E inspection;
- exposes no internet;
- binds to localhost/internal Compose network;
- contains no user database.

### 16.4 Push retry schedule

Initial default:

```text
attempt 1: immediate
attempt 2: +30 seconds
attempt 3: +2 minutes
attempt 4: +10 minutes
attempt 5: +1 hour
```

Permanent invalid-token response:

- marks endpoint `invalid`;
- marks current delivery `dead` or `suppressed` with stable code;
- prevents future plans for that endpoint.

### 16.5 Provider payload

Provider payload is bounded.

Do not include detailed money or identity values.

### 16.6 Push global pause

Push delivery may be paused independently.

Pausing:

- does not remove devices;
- does not affect in-app;
- leaves scheduled deliveries pending;
- shifts due time or blocks claims;
- is audited;
- has a maximum backlog guard.

---

## 17. Daily email digest

## 17.1 Scope

C3 implements daily email digest only.

Not included:

```text
weekly digest
push digest
SMS digest
marketing newsletter
arbitrary campaign
```

### 17.2 Eligible notifications

A kind is digest-eligible only when its registry metadata allows it.

Critical notifications are never digested.

Initial financial transaction success kinds may be configured either:

- immediate email;
- daily email digest;
- disabled email.

In-app remains immediate.

### 17.3 Digest window identity

Unique:

```text
user_id + channel + local_window_date + timezone
```

### 17.4 Digest item

A digest item references:

```text
notification_id
kind
category
created_at
read state at send cutoff
template/context snapshot
```

Do not duplicate an item in one digest window.

### 17.5 Scheduler

A bounded scheduler runs periodically.

Flow:

1. find due user windows;
2. lock with `SKIP LOCKED`;
3. establish local-time cutoff;
4. load eligible unread items;
5. re-check preference and verified contact;
6. skip empty digest;
7. cap displayed items;
8. render a versioned digest template;
9. create one email delivery;
10. mark window planned;
11. external email worker sends normally.

### 17.6 Timezone behavior

- IANA timezone required;
- local daily time stored in user settings;
- scheduler works across cross-midnight quiet hours;
- UTC timestamps remain canonical;
- local window boundary is recorded;
- timezone change affects future windows only.

### 17.7 Digest size

Initial limits:

```text
maximum 20 rendered items
maximum 100 source items counted
remainder summarized as “and N more”
```

No attachment.

### 17.8 Read-before-send behavior

At digest planning time:

- already-read optional notifications may be excluded;
- mandatory/security policy may override exclusion if explicitly registered;
- the decision is deterministic and tested.

### 17.9 Digest idempotency

A scheduler retry returns the same digest window/delivery.

Unique constraints prevent duplicate digest creation.

---

## 18. Global channel controls

## 18.1 Controls

Gateway owns:

```text
email enabled/paused
push enabled/paused
digest enabled/paused
```

In-app cannot be globally disabled through ordinary operator controls.

### 18.2 Control states

```text
enabled
paused
drain_only
```

Semantics:

- `enabled`: normal planning and delivery;
- `paused`: no provider claim; new plans remain pending/blocked;
- `drain_only`: no new external plans, existing backlog may deliver.

### 18.3 Operator requirements

Mutations require:

- authorized operator;
- CSRF through Admin BFF;
- reason;
- audit;
- optional checker approval for long-duration live-style pause;
- expiry or reminder for temporary pause.

### 18.4 Product independence

Channel pause does not:

- block RabbitMQ event processing;
- block in-app creation;
- block money movement;
- change source event status.

---

## 19. Gateway-owned schema

Use additive migrations after the current Gateway migration head.

T0 must validate exact types, names, and existing retention functions.

## 19.1 Alter `notif_notifications`

Add conceptually:

```text
event_type TEXT
source_service TEXT
kind TEXT
category TEXT
priority TEXT
requirement TEXT
locale TEXT
template_version_id UUID
deep_link TEXT
context JSONB
content_hash BYTEA
expires_at TIMESTAMPTZ
updated_at TIMESTAMPTZ
```

Backfill current rows:

```text
kind from legacy transaction type + recipient role where determinable
category = money_movement
priority = high
requirement = transactional
locale = en-US
source_service = ledger
event_type = ledger.transaction.posted.v1
context from approved payload fields where safely derivable
```

Rows that cannot be mapped remain explicit legacy rows.

### 19.2 Dedup migration

Create new unique index:

```text
(event_id, user_id, kind)
```

Migration sequence:

1. add/backfill `kind`;
2. detect duplicate candidates;
3. create new unique index concurrently where repository policy permits;
4. update insertion conflict target;
5. verify;
6. remove the old narrower unique constraint only after code rollout evidence.

No gap may allow duplicate logical notifications.

## 19.3 `notif_event_inbox`

```text
id UUID PRIMARY KEY
source_service TEXT NOT NULL
event_id UUID NOT NULL
event_type TEXT NOT NULL
schema_version INTEGER NOT NULL
payload_hash BYTEA NOT NULL
status TEXT NOT NULL
error_code TEXT NULL
received_at TIMESTAMPTZ NOT NULL
processed_at TIMESTAMPTZ NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
UNIQUE (source_service, event_id)
```

## 19.4 `notif_templates`

```text
id UUID PRIMARY KEY
kind TEXT NOT NULL
description TEXT NOT NULL
variable_schema JSONB NOT NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
UNIQUE (kind)
```

The registry must recognize every row.

## 19.5 `notif_template_versions`

```text
id UUID PRIMARY KEY
template_id UUID NOT NULL
channel TEXT NOT NULL
locale TEXT NOT NULL
version INTEGER NOT NULL
status TEXT NOT NULL
subject_template TEXT NULL
title_template TEXT NULL
body_text_template TEXT NOT NULL
body_html_template TEXT NULL
content_hash BYTEA NOT NULL
created_by TEXT NOT NULL
submitted_by TEXT NULL
approved_by TEXT NULL
rejected_by TEXT NULL
created_at TIMESTAMPTZ NOT NULL
submitted_at TIMESTAMPTZ NULL
published_at TIMESTAMPTZ NULL
retired_at TIMESTAMPTZ NULL
rejection_reason TEXT NULL
UNIQUE (template_id, channel, locale, version)
```

Add a partial unique index for one active version per:

```text
template_id + channel + locale
```

## 19.6 `notif_user_settings`

```text
user_id UUID PRIMARY KEY
locale TEXT NOT NULL
timezone TEXT NOT NULL
quiet_hours_enabled BOOLEAN NOT NULL
quiet_hours_start TIME NULL
quiet_hours_end TIME NULL
daily_digest_hour TIME NOT NULL
version BIGINT NOT NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
```

No cross-database foreign key to Auth.

## 19.7 `notif_preferences`

```text
id UUID PRIMARY KEY
user_id UUID NOT NULL
category TEXT NOT NULL
channel TEXT NOT NULL
mode TEXT NOT NULL
version BIGINT NOT NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
UNIQUE (user_id, category, channel)
```

## 19.8 `notif_device_endpoints`

```text
id UUID PRIMARY KEY
user_id UUID NOT NULL
platform TEXT NOT NULL
device_name TEXT NULL
token_ciphertext BYTEA NOT NULL
token_key_version INTEGER NOT NULL
token_fingerprint BYTEA NOT NULL
token_suffix TEXT NULL
status TEXT NOT NULL
last_success_at TIMESTAMPTZ NULL
last_failure_at TIMESTAMPTZ NULL
last_failure_code TEXT NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
revoked_at TIMESTAMPTZ NULL
UNIQUE (user_id, token_fingerprint)
```

## 19.9 `notif_deliveries`

```text
id UUID PRIMARY KEY
notification_id UUID NULL
digest_window_id UUID NULL
user_id UUID NOT NULL
channel TEXT NOT NULL
endpoint_id UUID NULL
status TEXT NOT NULL
template_version_id UUID NOT NULL
locale TEXT NOT NULL
recipient_ciphertext BYTEA NULL
recipient_key_version INTEGER NULL
recipient_fingerprint BYTEA NULL
rendered_subject TEXT NULL
rendered_title TEXT NULL
rendered_text TEXT NOT NULL
rendered_html TEXT NULL
provider_payload JSONB NULL
content_hash BYTEA NOT NULL
attempt_count INTEGER NOT NULL
next_attempt_at TIMESTAMPTZ NULL
lease_owner TEXT NULL
lease_expires_at TIMESTAMPTZ NULL
provider_message_id TEXT NULL
last_error_code TEXT NULL
delivered_at TIMESTAMPTZ NULL
suppressed_at TIMESTAMPTZ NULL
dead_at TIMESTAMPTZ NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
```

Exactly one of:

```text
notification_id
digest_window_id
```

must be set.

## 19.10 `notif_delivery_attempts`

```text
id UUID PRIMARY KEY
delivery_id UUID NOT NULL
attempt_number INTEGER NOT NULL
lease_owner TEXT NOT NULL
provider TEXT NOT NULL
started_at TIMESTAMPTZ NOT NULL
finished_at TIMESTAMPTZ NULL
result TEXT NOT NULL
status_class TEXT NULL
provider_message_id TEXT NULL
error_code TEXT NULL
duration_ms INTEGER NULL
response_excerpt TEXT NULL
created_at TIMESTAMPTZ NOT NULL
UNIQUE (delivery_id, attempt_number)
```

## 19.11 `notif_digest_windows`

```text
id UUID PRIMARY KEY
user_id UUID NOT NULL
channel TEXT NOT NULL
timezone TEXT NOT NULL
local_window_date DATE NOT NULL
window_start_at TIMESTAMPTZ NOT NULL
window_end_at TIMESTAMPTZ NOT NULL
scheduled_at TIMESTAMPTZ NOT NULL
status TEXT NOT NULL
item_count INTEGER NOT NULL
delivery_id UUID NULL
lease_owner TEXT NULL
lease_expires_at TIMESTAMPTZ NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
UNIQUE (user_id, channel, local_window_date, timezone)
```

## 19.12 `notif_digest_items`

```text
digest_window_id UUID NOT NULL
notification_id UUID NOT NULL
created_at TIMESTAMPTZ NOT NULL
PRIMARY KEY (digest_window_id, notification_id)
```

## 19.13 `notif_channel_controls`

```text
channel TEXT PRIMARY KEY
state TEXT NOT NULL
reason TEXT NULL
changed_by TEXT NOT NULL
changed_at TIMESTAMPTZ NOT NULL
expires_at TIMESTAMPTZ NULL
version BIGINT NOT NULL
```

## 19.14 Indexes

Add indexes for:

```text
notification user keyset pagination
notification unread count
notification kind/category filters
event inbox unprocessed/error
active template lookup
pending contact resolution
due delivery claim
expired delivery lease
dead delivery admin listing
device endpoint active lookup
preference user/category/channel
digest due window
digest item lookup
retention expiry
```

Every index needs an owned query and `EXPLAIN` evidence.

---

## 20. Migration and compatibility strategy

## 20.1 Expand first

Initial migrations only add:

- nullable columns;
- new tables;
- new indexes;
- new constraints validated after backfill where supported.

### 20.2 Legacy consumer compatibility

During the first migration release:

- existing code can continue inserting old columns;
- new columns allow null/default;
- public API remains unchanged;
- no external provider worker runs.

### 20.3 Backfill

A bounded backfill:

- processes by primary key/time cursor;
- maps current notifiable types;
- derives recipient role where deterministic;
- does not guess missing context;
- records unmapped count;
- is resumable;
- has statement timeout;
- emits progress metrics;
- does not hold one large transaction.

### 20.4 Dual-read/dual-write phase

New code:

- writes legacy title/body fields;
- writes new kind/category/template/context fields;
- returns legacy response shape plus additive fields;
- compares old hard-coded rendering with active v1 template fixtures in shadow
  mode.

### 20.5 Cutover

After evidence:

- template renderer becomes authoritative;
- raw event body is no longer written into legacy `payload`;
- delivery planner creates uniform delivery rows;
- external channels remain feature-gated.

### 20.6 Contract cleanup

Dropping or renaming legacy fields is not part of C3.

A future A9-compatible plan may remove them after deprecation evidence.

---

## 21. Retry and failure classification

## 21.1 Retryable

Examples:

```text
Auth unavailable
SMTP timeout
SMTP 4xx
push timeout
push 429
push 5xx
temporary DNS failure
temporary database deadlock before claim completion
```

### 21.2 Permanent

Examples:

```text
invalid template
missing mandatory variable
unverified email
invalid email format
invalid push token
unsupported platform
recipient inactive
payload exceeds provider limit
prohibited content
```

### 21.3 Blocked

Use `blocked` when operator action can repair without changing source event:

```text
missing active optional template
channel paused
provider configuration absent
encryption key version unavailable
```

### 21.4 Dead

Use `dead` after:

- retry budget exhausted;
- unrecoverable permanent provider failure;
- replay age exceeded;
- retention deadline makes delivery invalid.

### 21.5 Replay

Admin replay:

- requires reason;
- is audited;
- reuses rendered content and template version;
- preserves notification ID;
- resets delivery state and creates a new attempt number;
- does not create a second in-app notification;
- cannot bypass current user opt-out for optional delivery;
- cannot use revoked device endpoint;
- cannot use erased recipient ciphertext without re-resolution;
- is idempotent for repeated replay request ID.

---

## 22. Template and delivery Admin BFF surfaces

## 22.1 Boundary

Admin BFF remains the operator browser surface.

It calls typed Gateway admin APIs.

It does not read Gateway tables directly.

### 22.2 Template pages

Add:

```text
template catalog
template detail
version history
create draft
edit draft
preview fixture
submit for approval
approve
reject
retire
compare versions
```

### 22.3 Delivery pages

Add:

```text
delivery search
delivery detail
attempt history
dead delivery list
blocked delivery list
replay
endpoint/provider error category
```

Do not display plaintext recipient or token.

Display:

```text
recipient fingerprint
channel
kind
status
template version
timestamps
stable error code
sanitized provider result
```

### 22.4 Channel control pages

Add:

```text
email status
push status
digest status
pause
resume
drain-only
backlog
oldest due age
```

### 22.5 Operator test delivery

Allowed only to:

- Mailpit fixed development mailbox; or
- local mock-push test token.

It uses synthetic fixture data.

It may not accept arbitrary production-style user ID or recipient.

### 22.6 Authorization

Suggested policy:

| Action | Role |
|---|---|
| View template/delivery | operator/read |
| Create/edit draft | maker |
| Submit draft | maker |
| Approve/reject | checker, different actor |
| Retire active template | checker |
| Replay delivery | maker with reason |
| Pause channel | maker with reason |
| Long-duration pause | checker |
| Resume channel | maker |
| View provider configuration state | operator, secret-free |

Gateway re-validates security-sensitive rules.

### 22.7 Audit

Audit:

```text
notification.template.created
notification.template.updated
notification.template.submitted
notification.template.approved
notification.template.rejected
notification.template.retired
notification.delivery.replayed
notification.channel.paused
notification.channel.resumed
notification.channel.drain_only
notification.test_delivery.requested
```

No audit row contains:

```text
email plaintext
push token
SMTP password
provider secret
raw rendered HTML with user data
```

---

## 23. Retention and privacy

## 23.1 Retention classes

T0 must align exact values with A8.

Initial proposed configurable classes:

```text
mandatory financial in-app notification:  aligned with financial-history policy
optional in-app notification:             365 days
delivery attempts:                        90 days
dead/blocked delivery evidence:           180 days
recipient ciphertext after terminal:      erase after 30 days
event inbox successful row:               30 days
event inbox failed row:                   90 days
device endpoint after revoke/invalid:      erase token after 30 days
preferences/settings:                     active lifetime + deletion policy
template versions/audit:                  long-lived operational evidence
digest windows/items:                     90 days
```

Exact values require source-policy review.

### 23.2 Recipient erasure

After terminal retention window:

- clear recipient ciphertext;
- keep non-reversible fingerprint where policy permits;
- keep delivery status and operational evidence;
- preserve no provider secret.

### 23.3 Push-token erasure

For invalid/revoked endpoints:

- clear ciphertext after grace period;
- retain endpoint ID, fingerprint, status, and timestamps where policy permits.

### 23.4 User export

A8 user export should add:

```text
notification settings
preferences
in-app notification history within retention
registered device metadata without token
external delivery status without recipient ciphertext
```

### 23.5 User deletion/pseudonymization

Follow A8.

Do not mutate immutable Ledger events.

Gateway notification user references may be:

- deleted where policy allows; or
- pseudonymized while retaining mandatory operational evidence.

The exact behavior is recorded in the privacy matrix.

### 23.6 Payload minimization

Stop storing full RabbitMQ body in new `notif_notifications.payload`.

Store:

- normalized minimal context;
- event ID/type;
- payload hash;
- required identifiers;
- rendered snapshot.

---

## 24. Security and threat model

Update the repository threat model.

## 24.1 Template threats

- template injection;
- unsafe HTML;
- malicious deep link;
- email header injection;
- operator self-approval;
- misleading financial wording;
- missing locale fallback;
- template change affecting retries;
- secret exposure through preview.

### 24.2 Recipient threats

- cross-user device registration;
- email sent to unverified contact;
- stale recipient after identity change;
- token/address logged;
- unauthorized device listing;
- contact-resolution endpoint abuse.

### 24.3 Delivery threats

- provider credential leakage;
- duplicate delivery;
- retry storm;
- unbounded backlog;
- provider response-body exhaustion;
- slow provider goroutine exhaustion;
- invalid token loop;
- channel pause bypass;
- replay abuse.

### 24.4 Preference threats

- mandatory notification disabled;
- cross-user preference edit;
- race between opt-out and provider call;
- timezone/quiet-hour abuse;
- digest suppression hiding critical event.

### 24.5 Event threats

- forged RabbitMQ event;
- malformed payload;
- duplicate event;
- event ID collision;
- wrong authoritative source;
- raw sensitive data copied into notification context;
- one event notifying wrong recipient.

### 24.6 Admin threats

- CSRF;
- role escalation;
- maker/checker bypass;
- arbitrary user send;
- plaintext recipient display;
- mass replay;
- permanent channel pause without visibility.

### 24.7 Required control format

For every threat:

```text
prevention
detection
test
runbook
residual risk
owner
```

---

## 25. Observability

## 25.1 Ingestion metrics

```text
seev_notification_events_total{source,event_type,result}
seev_notification_event_processing_duration_seconds{source,event_type,result}
seev_notification_events_filtered_total{source,reason}
seev_notification_logical_created_total{kind,category}
seev_notification_duplicates_total{source,event_type}
seev_notification_planning_failures_total{kind,reason}
```

### 25.2 Delivery metrics

```text
seev_notification_deliveries_total{channel,kind,result}
seev_notification_delivery_attempts_total{channel,provider,result}
seev_notification_delivery_duration_seconds{channel,provider,result}
seev_notification_delivery_due_total{channel,status}
seev_notification_oldest_due_age_seconds{channel}
seev_notification_dead_total{channel,reason}
seev_notification_blocked_total{channel,reason}
seev_notification_contact_resolution_total{result}
seev_notification_contact_resolution_duration_seconds{result}
```

### 25.3 Preference/digest metrics

```text
seev_notification_preference_updates_total{channel,mode,result}
seev_notification_digest_windows_total{result}
seev_notification_digest_items_total{category}
seev_notification_digest_schedule_lag_seconds
seev_notification_devices_total{platform,status}
```

### 25.4 Template metrics

```text
seev_notification_template_render_total{channel,kind,locale,result}
seev_notification_template_missing_total{channel,kind,locale}
seev_notification_template_publish_total{channel,kind,result}
```

### 25.5 Forbidden metric labels

Never label with:

```text
user ID
notification ID
delivery ID
event ID
email
email domain
device token
device ID
transaction ID
request ID
template content hash
raw provider error
```

### 25.6 Structured logs

Permitted bounded context:

```text
channel
kind
category
status
provider
attempt number
stable error code
source event type
trace ID
request ID where applicable
```

Public IDs may appear only where operationally required and redacted policy
allows.

Never log:

```text
recipient
token
rendered body
raw provider payload
SMTP command
authorization
secret
```

### 25.7 Tracing

Trace links should connect:

```text
Ledger outbox publish
-> RabbitMQ notification consumer
-> planning transaction
-> external delivery worker
-> provider adapter
```

Asynchronous work uses links/event IDs, not one false synchronous parent span.

### 25.8 SLOs

Local learning SLO candidates:

```text
event to in-app p95:                <= 30 seconds
immediate external scheduled p95:   <= 60 seconds
normal provider-accepted p95:       <= 2 minutes
daily digest schedule p95:          <= 15 minutes from configured time
mandatory in-app planning success:  >= 99.9% in controlled tests
```

These are not production commitments.

### 25.9 Alerts

Required:

```text
notification event DLQ growth
mandatory template missing
in-app planning failure
email oldest due age
push oldest due age
contact resolution backlog
dead delivery growth
blocked delivery growth
digest schedule lag
channel unexpectedly paused
invalid-device spike
provider failure-rate spike
retention/recipient-erasure failure
```

Every alert links to a runbook.

---

## 26. Runbooks

Create:

```text
docs/runbooks/notification-event-dlq.md
docs/runbooks/notification-template-missing.md
docs/runbooks/notification-template-incident.md
docs/runbooks/notification-email-backlog.md
docs/runbooks/notification-push-backlog.md
docs/runbooks/notification-auth-contact-outage.md
docs/runbooks/notification-provider-outage.md
docs/runbooks/notification-dead-delivery.md
docs/runbooks/notification-channel-pause.md
docs/runbooks/notification-digest-lag.md
docs/runbooks/notification-recipient-data-exposure.md
docs/runbooks/notification-duplicate-external-delivery.md
docs/runbooks/notification-device-token-compromise.md
```

Each runbook includes:

- impact;
- diagnosis;
- safe immediate action;
- how in-app is affected;
- how money movement is unaffected;
- backlog estimate;
- recovery;
- replay policy;
- duplicate-risk warning;
- verification;
- evidence to record.

---

## 27. Configuration

Suggested configuration:

```text
NOTIFY_ENABLED=true
NOTIFY_INAPP_ENABLED=true
NOTIFY_EMAIL_ENABLED=false
NOTIFY_PUSH_ENABLED=false
NOTIFY_DIGEST_ENABLED=false

NOTIFY_DEFAULT_LOCALE=en-US
NOTIFY_DEFAULT_TIMEZONE=Asia/Jakarta
NOTIFY_DEFAULT_DIGEST_HOUR=08:00

NOTIFY_EVENT_PREFETCH=10
NOTIFY_EVENT_MAX_DELIVERY_ATTEMPTS=5

NOTIFY_DELIVERY_BATCH_SIZE=50
NOTIFY_EMAIL_WORKERS=2
NOTIFY_PUSH_WORKERS=2
NOTIFY_CONTACT_WORKERS=2
NOTIFY_DIGEST_WORKERS=1

NOTIFY_DELIVERY_LEASE_DURATION=2m
NOTIFY_PROVIDER_TIMEOUT=10s
NOTIFY_MAX_RENDERED_TEXT_BYTES=
NOTIFY_MAX_RENDERED_HTML_BYTES=
NOTIFY_MAX_PUSH_PAYLOAD_BYTES=

NOTIFY_ENCRYPTION_KEY_FILE=
NOTIFY_TOKEN_FINGERPRINT_KEY_FILE=

NOTIFY_SMTP_HOST=
NOTIFY_SMTP_PORT=
NOTIFY_SMTP_USERNAME_FILE=
NOTIFY_SMTP_PASSWORD_FILE=
NOTIFY_SMTP_FROM=
NOTIFY_SMTP_REPLY_TO=
NOTIFY_SMTP_TLS_MODE=

NOTIFY_PUSH_PROVIDER_URL=
NOTIFY_PUSH_PROVIDER_TOKEN_FILE=

NOTIFY_RECIPIENT_CIPHERTEXT_RETENTION=
NOTIFY_ATTEMPT_RETENTION=
NOTIFY_EVENT_INBOX_RETENTION=
NOTIFY_DIGEST_RETENTION=
```

Rules:

- in-app remains enabled by default;
- email/push/digest are disabled by default;
- missing encryption key fails startup only when an external channel requiring
  it is enabled;
- invalid size/duration config fails startup;
- no insecure TLS bypass option;
- provider secrets use file-based loading;
- configuration values are redacted from diagnostics.

---

## 28. Task breakdown

# T0 — Entry gate and current-state inventory

### Work

- Record baseline commit.
- Run all current gates.
- Inventory current `internal/notify` files and package boundaries.
- Inventory current migrations and grants.
- Inventory current notification API fixtures.
- Inventory queue/exchange/DLQ behavior.
- Inventory event fields and notifiable transaction types.
- Inventory current raw payload exposure.
- Inventory A8 retention/privacy integration.
- Inventory Auth email/verification fields and transport options.
- Inventory Admin BFF Gateway proxy and roles.
- Inventory encryption helper, secret loading, metrics, tracing, and breakers.
- Record current notification SLO and dashboard.
- Produce a blast-radius map.

### Deliverables

```text
docs/evidence/c3-entry-gate.md
docs/reference/c3-current-notification-inventory.md
docs/reference/c3-event-source-inventory.md
docs/reference/c3-privacy-inventory.md
```

### Acceptance

- [ ] Existing behavior is reproducible.
- [ ] API compatibility baseline is captured.
- [ ] Raw payload fields are classified.
- [ ] Auth contact ownership is explicit.
- [ ] Current event/DLQ semantics are explicit.
- [ ] Migration head and grants are recorded.
- [ ] No plan statement assumes unseen code.
- [ ] All blockers have owners.

---

# T1 — Lock notification contracts, policies, and threat model

### Work

- Add notification-kind registry specification.
- Lock categories, priorities, requirements, and delivery modes.
- Lock authoritative source per kind.
- Lock typed contexts.
- Lock public API additive changes.
- Lock internal Auth contact contract.
- Lock template lifecycle and maker/checker.
- Lock preferences and quiet hours.
- Lock email and push provider contracts.
- Lock digest semantics.
- Lock retry classification and schedules.
- Update threat model.
- Add sequence, state, and failure diagrams.

### Required diagrams

```text
Ledger event to in-app
event to email
event to push devices
Auth contact resolution
template publishing
preference evaluation
quiet-hours scheduling
daily digest
provider retry/dead
admin replay
Gateway crash after provider acceptance
legacy queue cutover
```

### Acceptance

- [ ] Every initial kind has one source and typed context.
- [ ] No raw event template access exists.
- [ ] No arbitrary-send API exists.
- [ ] Mandatory in-app policy is explicit.
- [ ] External at-least-once limitation is explicit.
- [ ] Template approval is owner-enforced.
- [ ] Privacy classification is complete.
- [ ] Threat controls have tests.

---

# T2 — Additive schema and compatibility foundation

### Work

- Add new Gateway migrations.
- Add event inbox.
- Add template/version tables.
- Add settings/preferences.
- Add device endpoints.
- Add deliveries and attempts.
- Add digest tables.
- Add channel controls.
- Add new notification columns.
- Add indexes and constraints.
- Add bounded legacy backfill.
- Preserve grants/RLS conventions.
- Update retention functions.
- Add migration integration tests.

### Acceptance

- [ ] Old Gateway binary can run against expanded schema during rollout where
      repository policy requires.
- [ ] Existing notification insert/list/read remains green.
- [ ] Backfill is resumable.
- [ ] No raw secret column exists.
- [ ] Recipient/token columns are ciphertext.
- [ ] New dedup index is proven.
- [ ] Existing rows remain queryable.
- [ ] Retention tests remain green.
- [ ] `EXPLAIN` evidence exists for new hot queries.

---

# T3 — Kind registry, templates, and renderer

### Work

- Implement registry.
- Add initial five notification kinds.
- Add typed contexts.
- Add template repository.
- Add immutable version lifecycle.
- Add active binding lookup.
- Add renderer and helpers.
- Add `en-US` v1 templates matching current behavior.
- Add optional `id-ID` templates.
- Add fixture preview.
- Add maker/checker application logic.
- Add template contract tests.
- Add render snapshots.
- Add missing-template policies.

### Acceptance

- [ ] Current four transaction-type behaviors map correctly.
- [ ] Transfer sender/receiver render distinct kinds.
- [ ] v1 in-app output matches or intentionally documents current copy change.
- [ ] Missing variable fails.
- [ ] Unknown variable fails.
- [ ] HTML escaping passes.
- [ ] Header injection fixtures fail safely.
- [ ] Active version is unique.
- [ ] Maker cannot approve own version.
- [ ] Retry content is immutable after template v2 publishes.

---

# T4 — Durable event inbox and notification planner

### Work

- Insert event inbox.
- Normalize current Ledger event.
- Build recipient specs.
- Evaluate registry policy.
- Load user settings/preferences from Gateway only.
- Render in-app and immediate external snapshots.
- Insert logical notification.
- Insert in-app delivered row.
- Insert external pending/digest plans.
- Mark inbox processed.
- ACK after commit.
- Replace raw payload storage with minimized context for new rows.
- Add duplicate and crash tests.
- Preserve current queue initially.

### Acceptance

- [ ] Duplicate RabbitMQ event creates one logical notification per recipient/kind.
- [ ] Crash after DB commit before ACK remains deduplicated.
- [ ] No provider/Auth network call occurs in handler.
- [ ] Transfer creates two independent records.
- [ ] Unknown notifiable mapping fails visibly.
- [ ] Non-notifiable Ledger type is filtered safely.
- [ ] Mandatory missing in-app template does not silently ACK.
- [ ] Raw body is absent from new notification rows.
- [ ] Existing public API remains green.
- [ ] Notification lag metrics remain valid.

---

# T5 — Public settings, preferences, and device endpoints

### Work

- Add settings endpoints.
- Add preference endpoints.
- Add effective-policy response.
- Add optimistic concurrency.
- Add quiet-hour calculation.
- Add push-device registration/list/revoke.
- Add encryption and fingerprinting.
- Add device limit.
- Add cross-user protection.
- Add dispatch-time opt-out check.
- Add privacy export integration.
- Add contract fixtures.

### Acceptance

- [ ] User sees only own settings/devices.
- [ ] Mandatory in-app cannot be disabled.
- [ ] Invalid mode/channel is rejected.
- [ ] Quiet hours handle cross-midnight.
- [ ] Timezone validation passes.
- [ ] Token plaintext is never returned.
- [ ] Duplicate registration is idempotent.
- [ ] Cross-user token conflict fails.
- [ ] Revoked device receives no new delivery.
- [ ] Log scan finds no token.

---

# T6 — Auth verified-contact resolution

### Work

- Add purpose-built internal Auth contract.
- Add service-identity authorization.
- Add typed Gateway client.
- Add contact resolver worker.
- Add encrypted recipient snapshot.
- Add active/verified checks.
- Add retry and suppression classification.
- Add timeout and breaker/resilience pattern consistent with A3.
- Add metrics and tracing.
- Add contract/evolution fixtures.
- Add Auth outage tests.

### Acceptance

- [ ] Gateway never reads Auth DB.
- [ ] Unauthorized caller cannot use contact endpoint.
- [ ] Unverified email is suppressed.
- [ ] Auth outage leaves in-app intact.
- [ ] Resolver retries without reprocessing domain event.
- [ ] Email plaintext is absent from logs and DB.
- [ ] Address change affects future notification resolution.
- [ ] Existing Auth public endpoints remain green.
- [ ] Contract gate passes.

---

# T7 — Email adapter and durable worker

### Work

- Add Mailpit profile.
- Add SMTP adapter.
- Add message builder.
- Add stable Message-ID.
- Add channel worker and lease recovery.
- Add retry schedule.
- Add failure classification.
- Add preference/quiet-hour dispatch guard.
- Add channel pause.
- Add attempt evidence.
- Add recipient ciphertext erasure.
- Add email E2E fixtures.

### Acceptance

- [ ] Mailpit receives expected text/HTML message.
- [ ] Subject/header injection is blocked.
- [ ] Provider call occurs outside DB transaction.
- [ ] SMTP 4xx retries.
- [ ] Permanent invalid recipient suppresses/deads correctly.
- [ ] Worker restart recovers lease.
- [ ] Same rendered bytes are reused.
- [ ] Stable delivery ID appears in headers.
- [ ] User opt-out before dispatch suppresses optional email.
- [ ] Email outage does not affect in-app or money journeys.

---

# T8 — Push adapter, mock provider, and device fan-out

### Work

- Add local mock provider.
- Add push adapter.
- Decrypt token only for provider request.
- Create one delivery per active device.
- Add privacy-safe payload.
- Add retry classification.
- Add invalid-token endpoint transition.
- Add rate/concurrency bounds.
- Add channel pause.
- Add provider idempotency key.
- Add E2E inspection endpoint for tests only.
- Add chaos fixtures.

### Acceptance

- [ ] Active devices receive one logical planned delivery each.
- [ ] Invalid token marks endpoint invalid.
- [ ] Revoked endpoint is skipped.
- [ ] Push payload contains no amount or sensitive identity.
- [ ] Transient failure retries.
- [ ] Mock provider deduplicates delivery ID.
- [ ] Worker restart recovers.
- [ ] Token plaintext is absent from logs/DB/API.
- [ ] Push outage does not affect email/in-app.
- [ ] Device limit and cross-user isolation pass.

---

# T9 — Daily email digest

### Work

- Add digest eligibility to registry.
- Add daily digest mode.
- Add window scheduler.
- Add item assignment.
- Add unread-at-cutoff logic.
- Add contact resolution.
- Add versioned digest template.
- Add item cap and “N more”.
- Add idempotency.
- Add quiet-hour interaction.
- Add late scheduler recovery.
- Add digest retention.
- Add E2E fixtures across timezone boundaries.

### Acceptance

- [ ] One user/window creates at most one digest.
- [ ] One notification appears at most once in one digest.
- [ ] Critical kind is never digested.
- [ ] Empty digest is not sent.
- [ ] Read optional item is excluded per policy.
- [ ] Timezone and cross-midnight tests pass.
- [ ] Scheduler restart does not duplicate digest.
- [ ] Digest uses exact active template snapshot.
- [ ] Email retry uses normal delivery worker.
- [ ] In-app remains immediate.

---

# T10 — Admin BFF operations and audit

### Work

- Add typed Gateway admin client.
- Add template catalog/version pages.
- Add preview and snapshot diff.
- Add submit/approve/reject/retire.
- Add delivery/dead/blocked pages.
- Add replay.
- Add channel control pages.
- Add synthetic local test delivery.
- Add CSRF.
- Add route policy.
- Add owner-side maker/checker validation.
- Add audit events and redaction.

### Acceptance

- [ ] Read-only operator cannot mutate.
- [ ] Maker cannot approve own template.
- [ ] Secret/recipient/token never appears.
- [ ] Replay requires reason.
- [ ] Arbitrary real-user send is impossible.
- [ ] Channel pause/resume is audited.
- [ ] Browser mutation requires CSRF.
- [ ] Existing Admin BFF routes remain green.
- [ ] Admin E2E covers template publish and delivery replay.
- [ ] Gateway independently enforces critical rules.

---

# T11 — Observability, retention, and operational controls

### Work

- Add metrics.
- Add traces.
- Update notification dashboard.
- Add channel and digest panels.
- Add alerts.
- Add runbooks.
- Add recipient/token erasure jobs.
- Add event inbox cleanup.
- Add attempt/digest cleanup.
- Add backlog guard.
- Add global controls.
- Validate cardinality.
- Add incident data-export restrictions.

### Acceptance

- [ ] Event, planner, contact, channel, digest lag is visible.
- [ ] Dead/blocked delivery is visible.
- [ ] Channel pause state is visible.
- [ ] Every alert has a runbook.
- [ ] Retention jobs are bounded.
- [ ] Recipient ciphertext erasure is proven.
- [ ] Device-token erasure is proven.
- [ ] No high-cardinality labels exist.
- [ ] Existing notification SLO remains valid or is deliberately evolved.
- [ ] Product health and notification health are distinguishable.

---

# T12 — E2E, chaos, performance, and final evidence

### Work

- Add notification E2E script.
- Add notification chaos script.
- Test duplicate event.
- Test Gateway crash after planning commit.
- Test Auth outage.
- Test Mailpit outage.
- Test push provider outage.
- Test worker crash.
- Test DB restart.
- Test RabbitMQ reconnect.
- Test missing template.
- Test channel pause/backlog/resume.
- Test opt-out race.
- Test digest scheduler interruption.
- Test retention.
- Add load scenario for notification burst.
- Run clean-tree full gate.
- Record residual risks.
- Update roadmap and current-service docs.
- Archive only after evidence.

### Acceptance

- [ ] Every mandatory event creates one in-app notification.
- [ ] Duplicate source event does not duplicate in-app.
- [ ] External delivery follows documented at-least-once behavior.
- [ ] Money and existing business journeys remain green through all channel
      outages.
- [ ] Missing mandatory template is detected.
- [ ] Auth outage recovers.
- [ ] Email/push outage recovers or reaches dead state.
- [ ] Backlog drains after resume.
- [ ] Preference race suppresses optional provider call where still pending.
- [ ] Digest is not duplicated.
- [ ] Secret scan is clean.
- [ ] Performance target is recorded.
- [ ] Final clean-tree gate passes.
- [ ] Residual risks are explicit.

---

## 29. Recommended pull-request sequence

```text
PR 1  — C3 entry evidence, architecture, registry/policy contracts, threat model
PR 2  — Additive Gateway schema and compatibility backfill
PR 3  — Kind registry, v1 templates, renderer, and admin-free fixtures
PR 4  — Event inbox and durable planner, still in-app only
PR 5  — Settings, preferences, quiet hours, and public API
PR 6  — Device endpoint registration and encryption
PR 7  — Auth verified-contact contract and resolver
PR 8  — Mailpit profile, SMTP adapter, email worker, and retry
PR 9  — Mock push provider, push worker, and device invalidation
PR 10 — Daily email digest
PR 11 — Admin BFF template/delivery/channel operations
PR 12 — Observability, retention, runbooks, load, chaos, final evidence
```

Split source-event additions into separate PRs.

Do not combine:

- Auth contract;
- schema migration;
- email provider;
- push provider;
- template admin;
- digest;

into one unreviewable change.

---

## 30. Dependency graph

```text
T0 Entry gate
  |
  v
T1 Contracts + threat model
  |
  v
T2 Additive schema
  |
  v
T3 Registry + templates
  |
  v
T4 Event inbox + planner
  |
  |----------------------|
  v                      v
T5 Preferences/devices  T6 Auth contact resolver
  |                      |
  |----------|-----------|
             v
       T7 Email worker
             |
             |----------------|
             v                v
       T8 Push worker     T9 Daily digest
             |                |
             |--------|-------|
                      v
            T10 Admin BFF
                      |
                      v
        T11 Observability/retention
                      |
                      v
             T12 Final evidence
```

T8 device schema/API may begin after T5.

T9 requires T5, T6, and T7.

---

## 31. First implementation cut

The first C3 vertical slice must modernize current in-app behavior without
external delivery.

```text
ledger.transaction.posted.v1
        |
        v
event inbox
        |
        v
kind registry
        |
        v
typed context
        |
        v
versioned en-US in-app template
        |
        v
logical notification
        |
        v
existing GET/read API
```

It proves:

- event dedup;
- transfer fan-out;
- typed context;
- template versioning;
- additive schema;
- no raw payload for new rows;
- rendering compatibility;
- current API compatibility;
- mandatory in-app durability;
- crash after commit before ACK.

Do not start email before this slice passes.

---

## 32. Second implementation cut

Add email only.

```text
notification plan
        |
        v
preference + quiet hours
        |
        v
pending recipient
        |
        v
Auth verified-contact resolution
        |
        v
encrypted recipient snapshot
        |
        v
SMTP worker
        |
        v
Mailpit
```

It proves:

- service-owned identity boundary;
- no Auth DB access;
- recipient encryption;
- independent retry;
- external channel outage isolation;
- dispatch-time opt-out;
- stable rendered content.

---

## 33. Third implementation cut

Add push.

```text
user registers encrypted device token
        |
        v
notification planner fans out per active device
        |
        v
privacy-safe push payload
        |
        v
push worker
        |
        v
local mock provider
```

It proves:

- device lifecycle;
- token security;
- per-endpoint fan-out;
- invalid-token handling;
- per-channel independent failure.

---

## 34. Fourth implementation cut

Add daily email digest.

```text
digest-eligible notification
        |
        v
user daily preference
        |
        v
timezone window
        |
        v
digest item
        |
        v
versioned digest render
        |
        v
ordinary email delivery
```

---

## 35. Test strategy

## 35.1 Unit tests

Cover:

```text
kind registry
authoritative source mapping
recipient resolver
typed context builder
money/date formatting
locale fallback
template lifecycle
maker/checker
template render
HTML escaping
header injection
deep-link builder
effective preference
mandatory override
quiet-hours calculation
timezone handling
digest eligibility
retry schedule
failure classification
recipient redaction
token fingerprint
delivery state transitions
lease expiry
payload size limits
```

### 35.2 Fuzz tests

Fuzz:

```text
RabbitMQ event decoder
template parser
template variable validation
locale parser
timezone input
email subject
email address parser
push token input
provider response parser
preference payload
cursor parser
digest window calculation
```

### 35.3 PostgreSQL integration tests

Prove:

```text
event inbox uniqueness
notification uniqueness
active template uniqueness
maker/checker constraints
preference uniqueness/version
device token uniqueness
due-delivery SKIP LOCKED concurrency
lease recovery
attempt numbering
digest-window uniqueness
digest-item uniqueness
retention batching
recipient ciphertext erasure
legacy backfill
migration up/down where supported
```

### 35.4 RabbitMQ integration tests

Prove:

```text
valid event
duplicate event
malformed event
redelivery
DLQ
consumer reconnect
commit-before-ACK crash simulation
queue binding
legacy queue transition
```

### 35.5 Auth contract tests

Prove:

```text
authorized service identity
unauthorized caller
active verified user
unverified email
inactive user
not found
timeout
compatible additive response field
no sensitive extra field
```

### 35.6 Email integration tests

With Mailpit:

```text
correct recipient
correct Message-ID
subject
text body
HTML body
no header injection
no hidden tracking
retry after SMTP outage
same rendered content after template change
```

### 35.7 Push integration tests

With mock provider:

```text
success
duplicate delivery ID
timeout
429
5xx
invalid token
device revocation
payload privacy
multiple devices
```

### 35.8 Public API tests

For every endpoint:

```text
authentication required
own-resource access
cross-user not found
validation
optimistic concurrency
pagination
mandatory preference override
token redaction
idempotent read/revoke
contract fixture
```

### 35.9 Admin E2E

```text
maker creates template
maker previews
maker submits
same maker cannot approve
checker approves
new event uses active version
operator inspects dead delivery
operator replays with reason
operator pauses/resumes channel
audit rows exist and are redacted
```

---

## 36. Chaos matrix

## 36.1 Gateway crash after notification DB commit before RabbitMQ ACK

Expected:

- RabbitMQ redelivers;
- event inbox detects duplicate;
- no duplicate in-app/delivery plan;
- message ACKs after duplicate lookup.

### 36.2 Gateway crash while external delivery is leased

Expected:

- lease expires;
- worker reclaims;
- same rendered snapshot and delivery ID used;
- possible external duplicate is documented;
- one logical delivery row remains.

### 36.3 RabbitMQ outage

Expected:

- source outbox retains events;
- no money failure;
- notification lag grows;
- alert fires;
- backlog catches up after recovery.

### 36.4 Gateway database outage

Expected:

- notification consumer cannot commit and does not ACK successfully;
- no provider call;
- money services continue;
- recovery processes backlog once;
- dedup remains correct.

### 36.5 Auth outage

Expected:

- in-app created;
- email remains pending/retry;
- push may proceed independently;
- contact-resolution alert fires;
- recovery schedules email.

### 36.6 Mailpit/SMTP outage

Expected:

- email retry;
- in-app/push unaffected;
- bounded workers;
- dead state after budget if outage persists;
- backlog drains after recovery/replay.

### 36.7 Push provider outage

Expected:

- push retry;
- in-app/email unaffected;
- no token invalidation for transient failure;
- backlog drains.

### 36.8 Invalid push token

Expected:

- one permanent classification;
- endpoint marked invalid;
- future delivery not planned;
- no retry storm.

### 36.9 Missing mandatory template

Expected:

- event cannot silently create incomplete mandatory notification;
- DLQ/blocked path visible;
- alert fires;
- operator publishes corrected version;
- controlled replay succeeds.

### 36.10 Missing optional email/push template

Expected:

- in-app succeeds;
- external delivery blocked;
- alert and admin visibility;
- replay after template activation.

### 36.11 Template changed during retry

Expected:

- existing delivery uses old version/rendered snapshot;
- new notifications use new version.

### 36.12 User opts out during pending delivery

Expected:

- dispatch-time guard suppresses optional delivery;
- no provider call after guard;
- in-app history remains.

### 36.13 User revokes device during leased push attempt

Expected:

- best-effort dispatch guard;
- an already-started provider call may complete;
- future attempts/plans suppressed;
- behavior documented.

### 36.14 Channel pause with backlog

Expected:

- no new provider claims;
- in-app continues;
- backlog visible;
- resume drains with rate/concurrency bounds;
- no thundering herd.

### 36.15 Digest scheduler interruption

Expected:

- window lease expires;
- one window/delivery only;
- no duplicate digest;
- schedule lag visible.

### 36.16 Encryption key unavailable

Expected:

- affected external channel fails closed/blocked;
- in-app continues;
- plaintext is never stored;
- alert/runbook used.

---

## 37. Performance and resource boundaries

C3 does not make production capacity claims before measurement.

Local engineering boundaries:

```text
event planning DB transaction is bounded
no network in planning transaction
notification list uses keyset pagination
unread count uses owned index
worker batch is bounded
worker concurrency is bounded per channel
provider timeout is bounded
rendered content is bounded
response excerpt is bounded
digest item count is bounded
device count is bounded
retention delete batch is bounded
contact resolution concurrency is bounded
no goroutine per retained notification
no metric label per user/delivery
```

Initial local targets to measure:

```text
event-to-in-app p95 under normal test load:    <= 30s
planner transaction p95:                       <= 100ms
email provider acceptance p95 with Mailpit:    <= 2m end-to-end
push provider acceptance p95 with mock:        <= 2m end-to-end
digest schedule lag p95:                       <= 15m
Gateway money/API p95 regression:              <= 5%
```

Targets are adjusted from recorded B0/local hardware evidence.

---

## 38. Load-test additions

Add notification scenarios without mixing them into unsafe financial scale
claims.

Scenarios:

```text
ledger notification burst
transfer fan-out burst
read/unread inbox traffic
preference update traffic
device registration churn
email backlog drain
push backlog drain
digest window fan-out
```

Measure:

```text
consumer lag
planner DB latency
notification insert rate
delivery due age
worker throughput
Auth contact calls
SMTP/push latency
DB pool saturation
RabbitMQ queue depth
Gateway public API latency
```

Safety:

- disposable data only;
- explicit acknowledgement variable;
- bounded local profile;
- external channels use local providers;
- no internet recipient;
- no real email/token.

---

## 39. Rollout stages

### Stage 0 — Schema and code disabled

- additive tables/columns;
- old in-app path active;
- no new worker;
- no provider.

### Stage 1 — Template shadow mode

- registry and v1 templates;
- old hard-coded rendering remains authoritative;
- compare rendered output in tests/metrics;
- no user-visible change unless intentionally approved.

### Stage 2 — New planner, in-app only

- event inbox;
- template rendering authoritative;
- uniform in-app delivery evidence;
- external channel flags off.

### Stage 3 — User settings/preferences/devices

- public APIs active;
- no external provider delivery yet;
- encryption and privacy evidence.

### Stage 4 — Email local

- Auth contact resolution;
- Mailpit;
- a sandbox/test cohort or config gate;
- retry/dead evidence.

### Stage 5 — Push local

- mock provider;
- test devices only;
- invalid-token and outage evidence.

### Stage 6 — Daily digest

- selected eligible categories;
- timezone and scheduler evidence.

### Stage 7 — Operator controls

- template lifecycle;
- dead delivery/replay;
- channel controls;
- full audit.

### Stage 8 — Additional source events

Only after:

- event contract exists;
- authoritative-source decision exists;
- duplicate semantic notification risk is tested;
- templates and preferences are ready.

---

## 40. Rollback

### 40.1 Immediate safety rollback

1. pause email/push/digest;
2. leave in-app enabled;
3. stop external workers;
4. continue event ingestion/in-app if safe;
5. inspect backlog;
6. preserve delivery evidence;
7. roll back code without dropping additive schema.

### 40.2 Template rollback

Activate a prior approved template as a new explicit publication decision.

Do not mutate an old version.

Already-rendered deliveries retain old content.

### 40.3 Consumer rollback

If new planner is defective:

- stop new consumer;
- restore legacy in-app path if still compatible;
- use event inbox/dedup evidence;
- avoid running two non-deduplicated consumers;
- replay only after duplicate analysis.

### 40.4 External duplicate caution

A rolled-back/restarted worker may resend an externally accepted delivery whose
commit was lost.

Runbook must communicate the at-least-once risk.

### 40.5 Data rollback

Do not drop:

```text
notifications
delivery evidence
template versions
audit
event inbox needed for dedup
```

Schema contraction is deferred.

---

## 41. Documentation deliverables

Add or update:

```text
docs/roadmap/active/59-c3-multi-channel-notifications.md
docs/roadmap/README.md
docs/roadmap/42-long-term-roadmap.md

docs/reference/current-services.md
docs/reference/notifications.md
docs/reference/notification-kinds.md
docs/reference/notification-templates.md
docs/reference/notification-preferences.md
docs/reference/notification-delivery.md
docs/reference/notification-providers.md
docs/reference/events.md

docs/architecture/notification-platform.md
docs/security/threat-model.md
docs/evidence/c3-entry-gate.md
docs/evidence/c3-inapp-cutover.md
docs/evidence/c3-final-acceptance.md

docs/runbooks/notification-*.md
```

---

## 42. Proposed repository changes

Expected areas:

```text
internal/notify/
internal/auth/
internal/adminbff/
internal/handler/

migrations/gateway/
api/openapi/
api/contracts/
api/events/
api/proto/ if the chosen Auth transport requires it
gen/ if Protobuf changes

cmd/mock-push-provider/
deploy/notifications/
docker-compose.yml
Makefile
scripts/notification-e2e.sh
scripts/notification-chaos.sh
tests/load/

deploy/observability/
docs/
```

This is forecast, not authorization to modify all paths.

T0 narrows the actual blast radius.

---

## 43. Make targets

Recommended:

```text
make notification-config-check
make notification-templates-check
make notification-fixtures
make notification-providers-up
make notification-providers-down
make notification-e2e
make notification-chaos
make notification-retention-test
make notification-load-lint
make notification-load-smoke
make notification-verify
```

Repository policy:

- lightweight template/contract/config checks join `make verify-full`;
- local provider E2E may join the full repeatable gate if resource cost is
  acceptable;
- destructive chaos remains separate;
- paid/external provider is never required.

---

## 44. Final verification commands

T0 replaces examples with canonical current commands.

Expected:

```bash
make contracts
make proto
make build-all
make test
make vet
make lint
make ci-lint
make docs-check

go test -tags=integration -race ./...

make notification-config-check
make notification-templates-check
make notification-providers-up
make notification-e2e
make notification-retention-test

./scripts/smoke-test.sh
./scripts/business-e2e.sh
./scripts/admin-e2e.sh

make verify-full
git diff --check
git status --short
```

Separate manual/destructive gate:

```bash
make notification-chaos
make verify-chaos
```

---

## 45. Final definition of done

C3 is complete only when all required items below pass.

### Architecture

- [ ] Gateway remains notification owner.
- [ ] No new business service is introduced.
- [ ] No money path depends on notification delivery.
- [ ] Domain events contain facts, not user-facing prose.
- [ ] Provider calls occur outside database transactions.
- [ ] RabbitMQ and provider at-least-once semantics are documented.

### Compatibility

- [ ] Existing notification list/read endpoints remain compatible.
- [ ] Existing in-app notifications are still queryable.
- [ ] Legacy backfill is complete or explicitly classified.
- [ ] Current business journey remains green.
- [ ] New event and Auth contracts pass A9 gates.

### Templates

- [ ] Initial kinds have active in-app templates.
- [ ] Email/push templates exist for enabled kinds.
- [ ] Template versions are immutable.
- [ ] Maker/checker is enforced.
- [ ] Locale fallback is tested.
- [ ] HTML/header/deep-link safety passes.
- [ ] Retry reuses original rendered snapshot.

### Ingestion and in-app

- [ ] Event inbox deduplicates.
- [ ] Transfer fan-out is correct.
- [ ] Duplicate RabbitMQ delivery creates no duplicate logical notification.
- [ ] Commit-before-ACK crash is safe.
- [ ] New rows do not store raw event bodies.
- [ ] Mandatory in-app cannot be disabled.

### Preferences and identity

- [ ] Settings and preferences are user-scoped.
- [ ] Quiet hours/timezone work.
- [ ] Dispatch-time opt-out guard works.
- [ ] Gateway does not read Auth DB.
- [ ] Only verified email is used.
- [ ] Auth outage does not affect in-app.
- [ ] Recipient is encrypted at rest.

### Push devices

- [ ] Tokens are encrypted at rest.
- [ ] API never returns token plaintext.
- [ ] Cross-user token conflict is rejected.
- [ ] Invalid token disables endpoint.
- [ ] Push preview is privacy-safe.
- [ ] Revoked device receives no future plan.

### Delivery reliability

- [ ] Email and push workers lease safely.
- [ ] Per-channel retry works.
- [ ] Dead and blocked states work.
- [ ] Replay is audited.
- [ ] Channel pause/resume works.
- [ ] Worker restart recovers.
- [ ] Provider outage is isolated.
- [ ] External duplicate risk is documented.

### Digest

- [ ] Daily email digest is implemented.
- [ ] Critical messages are never digested.
- [ ] One window creates one digest.
- [ ] Empty digest is skipped.
- [ ] Timezone and scheduler recovery pass.
- [ ] Digest delivery uses normal email reliability path.

### Security and privacy

- [ ] Threat model is updated.
- [ ] No arbitrary-send API exists.
- [ ] No paid/provider production secret is required.
- [ ] Logs/metrics/audit contain no recipient or token plaintext.
- [ ] Recipient/token erasure is proven.
- [ ] User export/privacy behavior is updated.
- [ ] Admin CSRF and maker/checker tests pass.
- [ ] Secret scan passes.

### Operations

- [ ] Ingestion, contact, delivery, digest, dead, and backlog metrics exist.
- [ ] Dashboard is updated.
- [ ] Alerts have runbooks.
- [ ] Metric cardinality is bounded.
- [ ] Channel health is distinct from financial health.
- [ ] Backlog drain and retention jobs are bounded.

### Evidence

- [ ] Email E2E through Mailpit passes.
- [ ] Push E2E through mock provider passes.
- [ ] Chaos matrix is exercised.
- [ ] Load baseline is recorded.
- [ ] Final clean-tree gate passes.
- [ ] Residual risks are documented.
- [ ] Roadmap/index/current-service docs reflect reality.
- [ ] Plan is archived only after evidence links are complete.

---

## 46. Final evidence log

Fill during execution.

| Evidence | Commit / artifact | Result | Notes |
|---|---|---:|---|
| C3 entry gate |  |  |  |
| Current API compatibility |  |  |  |
| Legacy notification backfill |  |  |  |
| Template fixture/render gate |  |  |  |
| Maker/checker template approval |  |  |  |
| Event inbox duplicate handling |  |  |  |
| Commit-before-ACK recovery |  |  |  |
| Transfer recipient fan-out |  |  |  |
| Raw-payload minimization |  |  |  |
| Settings/preferences E2E |  |  |  |
| Quiet-hours/timezone tests |  |  |  |
| Device token encryption |  |  |  |
| Cross-user isolation |  |  |  |
| Auth contact contract |  |  |  |
| Auth outage recovery |  |  |  |
| Mailpit email E2E |  |  |  |
| SMTP outage/retry |  |  |  |
| Mock push E2E |  |  |  |
| Invalid push token |  |  |  |
| Push outage/retry |  |  |  |
| Daily digest |  |  |  |
| Digest scheduler recovery |  |  |  |
| Channel pause/backlog drain |  |  |  |
| Dead delivery/replay |  |  |  |
| Recipient/token erasure |  |  |  |
| Admin BFF E2E |  |  |  |
| Notification load baseline |  |  |  |
| Final clean-tree gate |  |  |  |

---

## 47. Residual risks

A completed local C3 still does not prove:

- production email inbox placement;
- SPF, DKIM, DMARC, bounce, or complaint handling;
- production FCM/APNs credential rotation;
- mobile OS delivery guarantees;
- provider multi-region resilience;
- exactly-once external delivery;
- legal notification-consent compliance;
- marketing unsubscribe regulation;
- mass-campaign safety;
- production rate/volume pricing;
- real browser/mobile push registration UX;
- WebSocket/SSE realtime delivery;
- cross-region digest scheduling;
- large-scale template localization operations;
- customer-support tooling;
- recall of already delivered messages.

These limitations must remain explicit in README and portfolio claims.

---

## 48. Recommended immediate next action

Start with T0 and T1, then deliver only the in-app modernization slice.

Execution order:

```text
inventory current notification behavior
        ->
lock kind/source/preference/template contracts
        ->
additive schema
        ->
v1 templates matching current copy
        ->
event inbox and planner
        ->
cut current Ledger event to template-backed in-app
        ->
prove dedup, fan-out, API compatibility, and no raw payload
```

Only after this is green:

```text
preferences
-> Auth verified email
-> Mailpit email
-> encrypted push devices
-> mock push
-> daily digest
-> Admin BFF governance
```

This sequence prevents C3 from hiding core event/dedup/template mistakes behind
provider complexity.
