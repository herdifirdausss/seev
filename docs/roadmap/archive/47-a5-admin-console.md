# 47 — Track A5: Admin Console

Status: T1–T6 and the full-stack gate complete (2026-07-20). The console uses a thin BFF, server-side admin sessions, maker/checker roles, append-only audit logs, and Go templates with htmx.

## Trigger and goals

Plans 36–41, fee rules, KYC, vendor health, and payout resilience are complete, but daily operations still require curl and service-specific admin tokens. The goal is a browser-based operational surface without moving business logic or database ownership into the BFF.

## Locked design

### K1 — HTTP-only admin BFF

Add `admin-bff-service` on container port 8095 and host port 18095. It has no gRPC listener. The boundary map owns `internal/adminbff`; other services cannot import it.

### K2 — Thin HTTP clients

The BFF calls the existing HTTP admin routes of the six services. This is a documented exception to the usual synchronous-gRPC rule because the admin contracts are already HTTP-only and frozen. Typed clients perform request/response mapping, timeouts, and downstream error translation; they contain no business logic.

### K3 — Own database

Use `seev_adminbff` with role `adminbff_app` and migrations under `migrations/adminbff`. The BFF may access only its own `sessions` and `audit_log` tables.

### K4 — Server-side sessions and CSRF

Login forwards credentials to auth-service, accepts only admin roles, stores an opaque random session ID server-side, and sets an HttpOnly/Secure/SameSite cookie. Sessions have 30-minute idle and 8-hour absolute TTLs. Every mutating form requires a per-session CSRF token; logout revokes the database row.

### K5 — Short-lived downstream identity

Mint a fresh admin JWT for every downstream call with a 60-second TTL and the operator's real subject, email, and role. Do not store long-lived auth-service tokens in the BFF.

### K6 — Maker/checker enforcement downstream

Add `admin_maker` and `admin_checker`; keep `admin` as a superuser. Ledger enforces maker permissions for adjustment creation/reconciliation resolution and checker permissions for approval/rejection. Self-approval protection remains unchanged. Every other admin surface accepts all three roles.

### K7 — Append-only audit log

Audit every non-GET BFF mutation with operator identity, route pattern, target service, resource ID, outcome, request ID, and a masked summary. Do not store raw bodies, secrets, or unnecessary PII. Audit-write failure is fail-open for the BFF mutation but increments a metric and logs an error. There is no audit update/delete endpoint.

### K8 — Embedded UI

Use `html/template`, htmx, and vendored PicoCSS with `go:embed`. No Node, CDN, WebSocket, or SSE. Use lightweight polling for operational queues.

### K9 — Dead vendor-command payout operations

Add payout admin routes to list dead vendor commands, replay one, and replay all with a bounded cap and age filter. This closes the manual-SQL gap from plan 45.

### K10 — Minimal observability

Reuse standard RED metrics and add only the audit-write failure metric and one dashboard panel. No new chaos scenario is needed because the BFF is not on the money path; the full existing chaos suite remains mandatory.

## T1 — Service and database scaffold

Add the command, module, config loader, database, migrations, Compose service, health/metrics, Makefile target, CI enumeration, host helper, boundary entry, and environment block.

**Result:** the seventh service and `seev_adminbff` database passed build, boundary, Compose config, and isolated health checks.

## T2 — Login, sessions, CSRF, and token minting

Implement the thin auth client, session repository, expiry cleanup job, cookie middleware, CSRF validation, downstream token minting, and login/logout pages.

**Result:** admin login, non-admin denial, idle/absolute expiry, logout revocation, CSRF rejection, and token claims/TTL tests passed.

## T3 — Roles, ledger enforcement, and audit

Add maker/checker roles and optional bootstrap users, update every service's admin authorization, enforce the matrix directly in ledger, and add the BFF audit middleware.

**Result:** direct ledger tests prove maker cannot approve and checker cannot create, while admin can do both and self-approval remains blocked. BFF mutations create masked audit rows; GET requests do not.

## T4 — Typed clients and payout dead-command proxy

Implement one HTTP client per downstream service, route proxying, and payout list/replay endpoints. Reconcile live routes with the historical plan inventory; live code is authoritative.

**Result:** client happy/4xx/5xx/timeout tests, route-drift handling, payout dead-command list/replay, and BFF integration passed.

## T5 — Operations panels

Vendor htmx and PicoCSS, then add maker-checker, stuck payout/dead-command, reconciliation, fee/routing, KYC review, vendor health, fraud-event, and audit panels.

**Result:** embedded assets were verified and the browser panels worked without Node or direct service database access.

## T6 — Admin E2E and closure

Add `scripts/admin-e2e.sh` using `scripts/lib.sh` once: start dependencies and BFF, log in with cookie/CSRF, run maker-checker, KYC review, dead-command replay, and audit assertions. Update README, project guide, plan index, and roadmap.

**Result:** admin E2E, full verification, smoke-container, and chaos scenarios 1–14 passed from clean volumes. The plan index and A5 status are complete.

## Constraints

The BFF must never connect to another service database. Keep `execTransfer`, RLS, AMQP, fraud semantics, and lib.sh lifecycle unchanged. Verify external asset versions and checksums at execution time. Never place secrets in templates, logs, or audit records. Use the next free auth migration number.

## Definition of done

- [x] All three gates pass from clean volumes.
- [x] Maker/checker enforcement is proven directly in ledger, not only in the UI.
- [x] Mutations are audited and GETs are not.
- [x] All operational panels work in the browser without Node.
- [x] BFF database boundary is enforced.
- [x] Dead payout commands can be listed and replayed through HTTP.
- [x] Existing admin fixtures and full money-flow gates remain green.
