# Golden-route failure matrix

| Failure | Detection | Retry/age threshold | Escalation | Operator action | Replay/recovery |
|---|---|---|---|---|---|
| Vendor timeout after payout submission | vendor request timeout plus `unknown` state | no blind retry; reconcile within vendor SLA | SEV-1 when SLA is breached | pin original provider reference and stop duplicate submits | payout recovery runbook queries provider and closes exactly once |
| Duplicate callback | provider reference/idempotency digest conflict check | immediate idempotent read; alert on conflicting payload | SEV-2 for conflict | preserve first payload and inspect provider evidence | replay callback with same key only |
| Delayed callback | intent remains awaiting vendor | age > callback SLA | SEV-2 | inspect vendor queue and outbox | redeliver callback or run provider reconciliation |
| Outbox backlog | oldest pending event age/count metric | warn at 5m, critical at 15m | SEV-2/SEV-1 if money-flow event | inspect broker, relay, dead letters | replay pending/dead event after dependency recovery |
| Scheduled occurrence stuck processing | lease age/attempt state | age > 2× worker interval | SEV-2 | inspect worker lease and policy decision | lease recovery then retry same occurrence key |
| KYC expires before execution | execution gate reads effective expiry | immediate business rejection | SEV-2 if many subjects | do not force post; re-run after approved state | explicit user re-authorizes/requeues |
| Tenant/account disabled | execution gate status | immediate business rejection | SEV-2 for unexpected bulk | verify auth status propagation | restore state only through auth workflow |
| Conflicting idempotency payload | digest mismatch | no retry with same key | SEV-2 | return conflict and investigate client/provider | new key only after business review |
| Insufficient funds or changed limit | ledger/policy business error | no automatic retry unless state changes | SEV-3, SEV-2 for spike | inspect balance/limit and explain failure | user/operator submits a new authorized command |
| Reconciliation mismatch | settlement import and ledger comparison | age > settlement SLA | SEV-2; SEV-1 for material unexplained loss | freeze affected settlement and open correction request | maker-checker append-only adjustment |
| Dispute deadline missed | expiry worker and due-date metric | immediately at deadline | SEV-2 compliance | capture evidence and stop silent mutation | resolve as expired with audit trail |
| Migration/schema mismatch | readiness/schema-version gate | no application retry loop | SEV-1 release | keep pods unready; run migration job | rollback migration/app using documented compatibility window |
| Secret/provider dependency unavailable | startup/config validator and health checks | fail closed for money-moving dependency | SEV-1 | stop intake and rotate/fix dependency | replay durable command only after health is green |
