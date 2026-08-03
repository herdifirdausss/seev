# Messaging matrix

RabbitMQ uses a durable topic exchange named ledger.events. Publishers use
confirms; consumers are at-least-once and must deduplicate. Every queue gets a
durable dead-letter exchange and queue derived as
ledger.events.dlx / <queue>.dlq.

| Queue | Owner | Binding | Consumer | Prefetch / attempts | First deployment |
|---|---|---|---|---|---|
| ledger.events.audit | ledger topology | # | no consumer yet; declaration creates exchange | — | enabled |
| ledger.events.notifications | gateway/notify | ledger.transaction.posted.v1 | notify-consumer | 10 / 5 | enabled |
| ledger.events.fraud | fraud | ledger.transaction.posted.v1 | fraud-velocity-consumer | 10 / 5 | enabled |
| ledger.events.merchant_webhooks | gateway/merchant | ledger.transaction.posted.v1 | merchant-webhook-consumer | 10 / 5 | enabled |

The ledger audit queue is intentionally a topology anchor and currently has no
consumer; future audit consumers must add a separate queue rather than broaden
an application permission. DLQ replay is an explicit operations action and no
message payloads are stored in K0 evidence.

Source: [messaging.yaml](../../deploy/inventory/messaging.yaml),
pkg/messaging/topology.go, pkg/messaging/publisher.go,
pkg/messaging/consumer.go, and the queue-owning modules.
