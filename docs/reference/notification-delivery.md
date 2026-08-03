# Notification Delivery Reliability

In-app delivery is inserted as `delivered` in the same Gateway transaction as
the logical notification. Email and push are durable rows processed by
independent workers after the transaction commits.

## Delivery states

| State | Meaning |
|---|---|
| `pending_recipient` | email needs a verified Auth contact resolved and encrypted |
| `scheduled` | eligible for a provider attempt |
| `processing` | leased by one worker |
| `retry_wait` | transient failure with a bounded next-attempt time |
| `delivered` | provider accepted the message |
| `suppressed` | preference, quiet-hour, inactive-device, or unverified-contact guard |
| `blocked` | operator action is needed, usually a missing/invalid template |
| `dead` | permanent failure or exhausted retry schedule |
| `cancelled` | reserved terminal state for future cancellation flows |

Claims use `FOR UPDATE SKIP LOCKED`, an owner, and an expiry lease. Worker
restart recovers expired leases. Email retries use bounded delays through 24
hours; push retries use shorter bounded delays. Provider calls happen outside
database transactions. Every provider attempt stores a sanitized result class,
stable error code, duration, and bounded response excerpt.

Email uses a deterministic Message-ID and push uses the delivery ID as the
provider idempotency key, but neither channel promises exactly-once external
delivery. A provider acceptance followed by a process/database failure can
produce a duplicate on retry; this is an explicit operational risk.

## Digest

Digest windows are unique by user, email channel, local window date, and
timezone. A window covers the interval between two local digest times, so late
notifications are not appended to an already-sent window. Empty windows are
suppressed. A bounded number of items is rendered with a `more_count`; the
digest then uses the same email recipient resolution, leasing, retry, and
retention path as immediate email.

## Operator recovery

Admin BFF exposes sanitized delivery inspection, maker/checker template
changes, channel `running`/`paused`/`drain_only` controls, and reason-required
replay for dead/blocked rows. Replaying a blocked row first rebuilds its
rendered snapshot from the newly active template; it never sends an empty
snapshot. See the [notification runbooks](../operations/runbooks/notification-provider-outage.md)
and [duplicate-delivery runbook](../operations/runbooks/notification-duplicate-external-delivery.md).
