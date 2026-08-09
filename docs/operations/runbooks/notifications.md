# Notification Runbook

> [Operations](../README.md) · [Runbooks](README.md)

Gateway owns the notification platform. Financial processing is never blocked by
notification state — in-app creation is exactly-once via database uniqueness; external
delivery (email/push) is at-least-once and independently pausable.

**Global safety rules:**
- Do not requeue the source domain event to recover from a notification failure.
- Do not delete attempt evidence or rewrite rendered snapshots.
- Pause the affected channel before replay in active incidents; replay carries a duplicate
  delivery risk across the provider-acceptance crash window.
- Never expose the token plaintext, recipient email, or raw event payload in incident tickets
  or logs.

---

## Auth contact outage

**Impact:** Email remains `pending_recipient`; in-app creation and financial processing
continue independently.

Auth contact resolution is used only by the email worker. An Auth outage leaves event
ingestion and in-app delivery available; email stays pending and retryable.

**Do not:** requeue the source domain event. Keep the delivery in `pending_recipient`/
`retry_wait` and restore the purpose-built Auth resolver contract (mTLS identity,
internal-token configuration, contact-resolution timeout).

**Verify:** no email appears in logs/metrics; verified-only delivery resumes; in-app
counts are unchanged.

---

## Channel pause

**Impact:** Pause stops new external claims for the selected channel; in-app planning
and financial processing continue.

Use `paused` for an incident (no new work claimed); use `drain_only` when existing
in-flight work may safely finish. In-app has no ordinary global pause.

**Recover:** Resume only after the provider/template/contact condition is understood.
Resuming existing in-flight work is not a replay. Explicit replay of dead/blocked rows
carries duplicate risk and requires an operator reason.

**Verify:** channel state/expiry, claim rate, oldest due age, and audit/CSRF evidence.

---

## Dead delivery

**Impact:** One external channel attempt is exhausted or permanently rejected; the
in-app notification is retained.

Inspect delivery status, stable error code, attempt history, template version, and
endpoint/contact lifecycle without exposing secrets or ciphertext.

**Recover:** Fix the underlying policy/provider/contact issue before replay. Replay
requires an operator reason and must not alter the rendered snapshot. A replay can
duplicate mail/push when the provider accepted an earlier attempt but the commit was
lost.

**Verify:** attempt number, provider result class, delivery state transition, audit
record, and in-app availability.

---

## Device token compromise

**Impact:** The affected push endpoint may receive unauthorized previews; in-app and
financial state are unaffected.

**Treat as a security incident.** Revoke the endpoint; pause push if the scope is
broad; erase ciphertext after the grace period or via approved security handling; rotate
provider credentials if applicable. Do not replay deliveries to a compromised endpoint.

Identify endpoint IDs/platform/status and bounded invalid-device metrics. Never expose
the token or decrypt it for an incident report.

**Verify:** endpoint is revoked/invalid; token ciphertext is inaccessible/erased; no
token plaintext appears in logs/exports; audit evidence is complete.

---

## Digest lag

**Impact:** Daily email may arrive late or be suppressed when empty; in-app notices are
immediate and money movement is unaffected.

Inspect due windows, local timezone/window boundaries, digest channel control, scheduler
lease expiry, Auth contact resolution, and email backlog.

**Do not:** create a second window manually. Recover the leased window or resume the
digest control after checking the configured local time. A provider-acceptance crash can
duplicate the digest email.

**Verify:** one window per user/date/timezone, one digest delivery, item cap/more-count,
schedule lag, and sanitized metrics.

---

## Duplicate external delivery

**Impact:** A user may receive duplicate email/push; in-app logical creation remains
deduplicated and money movement is unaffected.

External delivery is at-least-once by design — the platform guarantees in-app
exactly-once via uniqueness; email uses a stable `Message-ID` and push uses the delivery
ID as the provider idempotency key, but an acceptance followed by a process failure can
produce a duplicate on retry.

**Do not:** infer a financial duplicate from a duplicate notification. Do not delete
attempt evidence or rewrite rendered snapshots.

**Recover:** Fix lease/timeout/provider behavior; resume with bounded workers; document
the unavoidable acceptance/commit crash window. Pause the affected channel if the
duplicate rate is rising.

**Verify:** one logical notification per event/user/kind, provider deduplication
behavior, attempt sequence, and sanitized incident evidence.

---

## Email backlog

**Impact:** In-app notices continue; email is delayed or `pending_recipient`. Financial
requests never wait for email.

Inspect oldest due age, channel control, `pending_recipient` count, Auth contact
latency, SMTP/Mailpit health, and dead/retry counts.

**Recover:** Restore Auth/SMTP; let bounded workers drain; replay only dead/blocked rows
with an operator reason. Pause email or switch to `drain_only` during provider
instability. Do not delete pending deliveries or request contacts from the public API.

Stable `Message-ID` reduces duplicates but cannot provide exactly-once delivery.

**Verify:** accepted attempts, retry schedule, oldest due age, recipient ciphertext
presence only as a boolean, and no plaintext in logs.

---

## Event DLQ

**Impact:** Affected events do not create new notification plans; financial state is
unchanged and already-created in-app rows remain available.

Inspect the bounded event type/error and RabbitMQ DLQ depth. Never copy the raw
message into an incident ticket.

**Recover:** Keep the source outbox event durable; correct the contract/configuration;
replay only after the event schema and recipient mapping are reviewed (replaying an event
through the existing notification queue will verify the event-inbox uniqueness guard).
External replay is at-least-once — it can produce a duplicate email/push across a
provider-acceptance crash window.

**Verify:** in-app creation, delivery status, DLQ depth, and bounded metrics. Record
event type, error code, timestamps, and operator/audit reference — never payload, email,
or token.

---

## Provider outage

**Impact:** The affected external channel accumulates retry/dead work; in-app remains
the source of truth and money movement is unaffected.

Classify adapter results as transient, permanent, invalid endpoint, or accepted. Inspect
channel control state and oldest due age.

**Recover:** Pause the channel and set an expiry/reason. Preserve rendered snapshots and
attempt evidence. Verify the local adapter/provider; resume or `drain_only`; replay only
after a checker-approved decision. An acceptance crash window can produce duplicate
external delivery even with idempotency keys.

**Verify:** bounded backlog drain, provider result rate, dead delivery growth, and
absence of credentials/recipient data in evidence.

---

## Push backlog

**Impact:** In-app notices continue; push is delayed. Money movement and registered
devices are unaffected.

Inspect push channel control, oldest due age, provider result classes, invalid-device
rate, and mock-provider accepted/deduplicated records.

**Recover:** Pause or `drain_only` push; preserve endpoint rows and attempt evidence.
Restore the adapter; allow bounded retries; invalidate permanent endpoints; replay only
authorized dead/blocked deliveries. Provider acceptance followed by a worker crash can
duplicate a push.

Never print or manually copy a push token.

**Verify:** provider idempotency keys, invalid endpoint transitions, retry counts, and
sanitized response excerpts.

---

## Recipient data exposure

**Impact:** Email address or device-token confidentiality may be at risk; financial
state remains unchanged.

**Treat as a security incident.** Restrict access immediately; inspect logs/audit/
provider traces for plaintext; identify the bounded delivery IDs without exporting
ciphertext.

**Recover:** Pause the affected channel; revoke exposed devices; rotate
encryption/fingerprint material according to the secret procedure; erase recipient
ciphertext through retention/closure controls; require security approval before resuming.
Replay is prohibited until the exposure scope is closed.

**Verify:** no plaintext in logs/exports; endpoint state; ciphertext erasure result;
audit trail; and notification health separated from product health.

---

## Template incident

**Impact:** Incorrect copy affects only notification presentation; ledger, payout,
payin, and account state are unchanged.

Compare immutable delivery snapshots, template version, locale, content hash, and the
approved fixture. Do not edit an active version in place.

**Recover:** Retire the faulty version and pause the affected channel when exposure is
ongoing. Create a new draft, validate fixtures and headers/HTML, obtain checker approval,
then replay only authorized blocked/dead rows. Provider acceptance cannot be recalled and
replay may duplicate external delivery.

**Verify:** render snapshots, active-version lookup, channel backlog, and user-facing
in-app history. Record version/hash/audit IDs only.

---

## Template missing

**Impact:** Mandatory in-app planning fails closed; optional email/push work is blocked.
Money movement is unaffected.

Inspect the registry kind, channel, locale, active version, and template hash. Do not
use a raw-event or arbitrary-send workaround.

**Recover:** Pause the affected external channel if needed; publish a reviewed
fixture-valid version with maker/checker separation; then resume. Replay only blocked
deliveries after the active version is verified — the stored in-app snapshot is never
rewritten. Replaying a provider-accepted delivery can duplicate external mail/push.

**Verify:** render the fixture; confirm one active version per locale/channel; inspect
blocked count; record the version ID and audit reference without rendered user data.
