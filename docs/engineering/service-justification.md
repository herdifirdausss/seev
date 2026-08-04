# Service and capability justification

Every service must have a distinct ownership boundary, data boundary, failure
policy, and reason it cannot safely be a package in another service.

| Service/capability | Owns | Separate because | Failure policy | Evidence/owner |
|---|---|---|---|---|
| Gateway | external HTTP routing, auth middleware composition, rate limits | internet boundary and API contracts | reject unauthenticated requests; no ledger writes | `services/gateway/cmd/gateway`; Gateway owner |
| Auth | identity, sessions, KYC workflow, account status | sensitive identity and KYC data boundary | fail closed for authorization state | `services/auth`; Auth owner |
| Ledger | double-entry truth, balances, idempotency, outbox | financial source of truth and transaction boundary | never rewrite history; append corrections | `services/ledger`; Ledger owner |
| Payin | payment intent/vendor intake | vendor callback and settlement lifecycle | unknown provider state is reconciled | `services/payin`; Payin owner |
| Payout | withdrawal intent/vendor submission/recovery | external side effect and unknown-state lifecycle | no blind duplicate submission | `services/payout`; Payout owner |
| Reconciliation | compare external settlement evidence with ledger | independent control-plane view | mismatch freezes/corrects through maker-checker | Ledger/Ops owner |
| Fraud/screening | risk decision and velocity signals | dependency and policy lifecycle differs from ledger | configured flows fail closed on unavailable decision dependency | `contracts/clients/fraud`; Security owner |
| Admin BFF | operator UI/API aggregation | operator access and audit surface | no direct database access to Ledger | `services/adminbff`; Platform owner |
| Messaging/outbox | durable event delivery | delivery is asynchronous and retryable | dead-letter and replay | `internal/platform/messaging`; Platform owner |

## Simplification rules

- Do not add a service solely to move an existing repository interface.
- Do not move ledger truth into a vendor or a cache.
- Prefer a package when a capability shares the same data owner, transaction,
  deploy lifecycle, and security boundary.
- Prefer a service when an external side effect, sensitive data boundary, or
  independent failure/release policy requires isolation.

The table is reviewed whenever a new service, queue, database, or operator
surface is proposed.
