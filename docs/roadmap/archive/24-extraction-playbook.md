# 24 — Service Extraction Playbook

Reference only. Do not execute this playbook until an evidence-based extraction trigger from [plan 21](21-service-topology-review.md) is met. The goal is to make a future split a controlled checklist rather than a research project.

Read this together with [plan 21](21-service-topology-review.md) for the service map and locked decisions, and [plan 01](01-target-architecture.md) for the boundary rules.

## Gate: prerequisites before any split

All of the following must be true:

- An extraction trigger is supported by metrics, incidents, load, team ownership, or compliance evidence, and that evidence is recorded in the split PR.
- `boundary_test.go` has been green for at least three months without adding exceptions.
- The module does not share tables with other modules; verify table references outside `internal/<module>`.
- Request IDs, OpenTelemetry export, and webhook alerts are operational. Network hops in a money path require active tracing.
- Staging can run the monolith and split topology side by side for comparison and rollback.

## Generic per-service checklist

### Phase A — Prepare in the monolith

1. Freeze the contract: facade methods, internal routes, events consumed and published, and database tables.
2. Implement an HTTP client shim alongside the in-process facade implementation. Select `inproc` or `http` through configuration and test the monolith calling its own internal API in staging.
3. Create a service-specific database role with access only to the module's prefixed tables and approved reference data.
4. Ensure event consumers use the module's published event contract instead of querying another module's tables.

### Phase B — Split the process, keep one database

5. Add `cmd/<module>-service/main.go` with the module's configuration, database role, workers, and HTTP/gRPC server. Disable the in-process module and switch consumers to the HTTP shim.
6. Cut over in staging first, then in production with the monolith available as a configuration-based fallback.
7. Observe one complete business cycle, including snapshots, reconciliation, and verification, before moving data.

### Phase C — Split the data

8. Move the module's prefixed tables to a new database. For the expected volume, a short cutover window is preferred over the complexity of dual writes.
9. Give the service its own migration timeline.
10. Give the service its own outbox and relay if it publishes events. Never share `outbox_events` across databases.

### Phase D — Clean up

11. Remove the module implementation from the monolith, leave only the HTTP shim if required, revoke old grants, update boundary tests, and update the service map and this playbook.

## Service-specific notes

| Service | Extraction notes |
|---|---|
| Payin | Most likely first candidate because adding vendors may require frequent releases. Move `internal/vendorgw` with it and make the webhook route its edge. |
| Payout | Move the state machine and resume worker together. Keep the K3 ledger guard in the ledger service; do not duplicate it. |
| Fraud | Replace the ledger hook with a strict-timeout, fail-open HTTP client and move the rule engine and `screening_events`. Use `ledger.transaction.posted.v1` for asynchronous enrichment. |
| Ledger | Extract last, if ever. All other services depend on it, so moving it changes every client boundary at once. |
| Internal admin | Build a new BFF that calls each service; this is not an extraction of a current module. |
| User-facing | Create `internal/auth` first, then move the public router and auth together as a BFF. |

## Frozen internal API inventory (baseline 2026-07-12)

All routes below are on the internal `:8081` listener, under `/api/v1`, and are protected by JWT and handler-specific admin authorization. Changes after this date are API changes and must be reviewed for compatibility.

| Area | Routes |
|---|---|
| Posting | `POST /ledger/transactions`, `GET /ledger/transactions/{id}` |
| Accounts | `GET /ledger/accounts`, balance, entries, statement, and pocket routes |
| Outbox | Replay one dead event or replay all dead events |
| Maker-checker | Adjustment create/list/approve/reject/get routes |
| Reconciliation | Batch create/get and item resolve routes |
| Schedules | Admin run trigger plus user CRUD |
| Disbursement | Create, run, and get routes |
| Savings | Savings settings update and list routes |
| Screening | `GET /ledger/admin/screening/events` |
| Reports | `GET /ledger/admin/reports/{position\|mutation\|recon}` |
| Policy | `/admin/policy/limits...` |
| Payin | Event list and replay routes |
| Payout | Request list, cancel, and retry routes |
| Operations | `GET /metrics` |

## Future `internal/auth` outline

When the user-facing service is needed, add:

- `auth_users`, `auth_credentials`, and `auth_refresh_tokens` tables;
- an `auth.Module` facade with `Register`, `Login`, `Refresh`, and `Me`;
- JWT claims compatible with the existing middleware (`UserID`, `Role`, and issuer);
- a successful registration flow that provisions the ledger account and publishes `auth.user.registered.v1` through an auth-owned outbox;
- real implementations for the current `/auth/login`, `/auth/register`, and `/users/me` placeholders.

Auth and the public router move together when the user-facing service is extracted. The monolith can continue verifying the same issuer and signing secret during the transition.
