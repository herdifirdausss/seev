# Notification event DLQ

Impact: affected events do not create new notification plans; financial state is unchanged and already-created in-app rows remain available.

Diagnosis: inspect the bounded event type/error and RabbitMQ DLQ depth. Never copy the raw message into an incident ticket.

Safe action: keep the source outbox event durable, correct the contract/configuration, and replay only after the event schema and recipient mapping are reviewed.

Recovery: replay the original event through the existing notification queue and verify the event-inbox and logical-notification uniqueness guards.

Replay warning: external delivery is at-least-once; a replay can produce a duplicate email/push across a provider-acceptance crash window.

Verify: check in-app creation, delivery status, DLQ depth, and bounded metrics. Record event type, error code, timestamps, and operator/audit reference—never payload, email, or token.
