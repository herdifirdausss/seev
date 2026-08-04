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
  [critical-failure acceptance](../acceptance/critical-failures.md). A local
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
