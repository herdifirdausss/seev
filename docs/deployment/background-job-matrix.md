# Background-job matrix

Workers currently run in the request-service process. This is a runtime
inventory, not a replica recommendation. Durable relays and consumers can
usually compete safely; scheduled jobs that use memory locks begin at one
active replica until K3 provides a distributed lock or lease.

| Owner | Worker families | Trigger | Replica rule | First deployment |
|---|---|---|---|---|
| ledger | outbox, verifier, snapshot, schedule, accrual, retention | poll, cron, retention scheduler | claims/lock; one active for schedules | enabled |
| auth | KYC retry, expiry, rescreen, retention, object outbox | ticker, lock, retention scheduler | database/distributed claims | enabled |
| payin | retention | retention scheduler | distributed lock | enabled |
| payout | resume, vendor relay, retention | minute cron, poll, retention scheduler | lock/claim | enabled |
| fraud | velocity consumer, spill flusher, retention | RabbitMQ, ticker, retention scheduler | competing consumers; spill is per-process | enabled |
| gateway | notify, merchant webhook relay/consumer, retention, observability | RabbitMQ, poll, scheduler, ticker | claims/consumer; gauges are observational | enabled |
| admin-bff | session cleanup, retention | shared scheduler | memory lock; one replica until externalized | enabled |
| assurance | correlation, retention | module scheduler, retention | one active policy | enabled |
| vendor | retention | retention scheduler | distributed lock | enabled |

Privacy export, closure saga, and backup scheduling are explicitly deferred
because their keys, external stores, or operations contracts are not part of
the first deployment. No enabled worker has an UNKNOWN_BLOCKER; source-level
intervals, schedules, failure behavior, and ownership are in
[jobs.yaml](../../deploy/inventory/jobs.yaml).
