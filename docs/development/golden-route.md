# Golden route

The golden route proves a complete money lifecycle and its recovery paths.
It is intentionally written as invariants plus state transitions so a test,
operator, and reviewer use the same language.

```mermaid
flowchart LR
  R[Register] --> A[Authenticate]
  A --> K[KYC approved]
  K --> I[Pay-in intent]
  I --> V[Vendor accepted]
  V --> C[Callback: duplicate/delayed safe]
  C --> B[Ledger funds available]
  B --> P[P2P transfer]
  P --> O[Payout intent]
  O --> U[Unknown/timeout]
  U --> X[Recovery/reconcile]
  X --> S[Statement]
  S --> D[Dispute or reversal]
```

## Preconditions

- services use one commit under test and a migrated database;
- the test user is provisioned with one enabled currency and a valid KYC
  window;
- provider callbacks can be delivered, duplicated, delayed, and withheld;
- the ledger outbox and reconciliation workers are observable;
- the test records correlation IDs, database snapshots, event payload hashes,
  and final balances in the evidence bundle described by
  [runtime acceptance summary](../operations/runtime-acceptance.md). A local
  run may use `KEEP_WORK_DIR=1`; CI stores a run-specific retained artifact.

## State machines and invariants

| Aggregate | States | Terminal invariant |
|---|---|---|
| KYC subject | `pending → approved → expired/downgraded`; `disabled` from any non-terminal state | A money command requires active subject and a valid KYC window at execution time. |
| Pay-in | `created → awaiting_vendor → accepted/failed/unknown → settled/reconciled` | A provider reference maps to one idempotent ledger effect. |
| P2P transfer | `requested → posted` or `rejected` | One idempotency scope/key has one command digest and at most one posting. |
| Payout | `created → reserved → submitted → succeeded/failed/unknown → recovered` | An unknown vendor outcome is not retried as a new payout until reconciled. |
| Outbox event | `pending → published` or `dead` | Publishing is retryable and never changes ledger truth. |
| Reconciliation item | `unmatched/mismatch → resolved` | Resolution is an append-only correction with maker-checker attribution. |
| Dispute | `open → evidence_submitted → won/lost/expired` | Terminal rows cannot be rewritten; amount is no greater than the original. |

## Route assertions

1. Registration creates the auth subject and ledger execution subject.
2. Authentication provides an actor, tenant, correlation ID, and claims.
3. KYC approval synchronizes level and expiry to the execution gate.
4. Pay-in acceptance and duplicate/delayed callbacks converge on one ledger
   transaction and one outbox event.
5. P2P posting re-evaluates policy, subject state, authorization, idempotency,
   balances, locks, ledger entries, and outbox in that order.
6. Payout timeout records uncertainty; recovery queries the same vendor
   identity and reconciles before any retry.
7. Statement and reconciliation read committed ledger truth.
8. A dispute or reversal creates a new attributable transaction and leaves the
   original transaction immutable.

## Failure modes

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

## Verification commands

```sh
GOCACHE=/tmp/seev-go-cache go test ./services/ledger/internal/... ./services/auth/internal/... ./services/payout/internal/...
GOCACHE=/tmp/seev-go-cache go test -race ./services/ledger/internal/... ./services/payout/internal/...
KEEP_WORK_DIR=1 ./scripts/chaos-test.sh 2
KEEP_WORK_DIR=1 ./scripts/chaos-test.sh 8
```

The chaos commands require the local service dependencies. A retained deployed
run is required before marking the route runtime-accepted; CI artifacts prove
the repository gate but do not prove a production environment.
