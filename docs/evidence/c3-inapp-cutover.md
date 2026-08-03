# C3 In-App Cutover Evidence

This is the staged-cutover record for the mandatory in-app path. It documents
the code and the evidence still required; it does not claim runtime execution.

## Implemented cutover controls

1. The existing queue name and `ledger.transaction.posted.v1` source are kept.
2. New rows use the additive C3 columns and a bounded typed context; legacy
   rows continue through the old list/read compatibility path.
3. Event-inbox and `(event_id, user_id, kind)` uniqueness protect redelivery and
   transfer fan-out.
4. In-app planning is committed before RabbitMQ acknowledgement.
5. In-app is forced immediate in the preference API and is not controlled by
   the ordinary external-channel pause state.
6. External channels are independently feature-gated, so the initial cutover
   can be in-app-only.

## Required acceptance evidence

| Scenario | Required proof |
|---|---|
| duplicate source delivery | one logical row per event/user/kind |
| sender/receiver transfer | two correctly scoped copies |
| crash after DB commit before ACK | redelivery is a no-op |
| legacy row read | old response fields remain available |
| Auth/SMTP/push outage | in-app creation and financial journey remain available |
| raw payload minimization | new notification payload is empty and context is bounded |

The cutover is therefore code-ready but acceptance-pending until those checks
are executed and attached to this file.
