# 32 — Phase 6g: Formalize the Gateway Service

Read [plan 26](26-phase6a-foundations.md) first. Prerequisite: [plan 31](31-phase6f-fraud-service.md).

After plans 27–31, the remaining `cmd/server` responsibilities are the public router, ledger proxy, payin and payout gRPC clients, webhook edge, and notification consumer/API. This phase gives that process its final name and database: `gateway-service` with `seev_gateway`.

## T1 — Rename and clean up

Rename `cmd/server` to `cmd/gateway`, update the binary name, Makefile, scripts, and Dockerfile defaults. Remove stale dependency fields left by extraction, while retaining the database for notifications, RabbitMQ for the notification consumer, and cache for rate limiting. Remove the unused 501 `GET /admin/users` placeholder and generic placeholder handler.

**Tests:** full unit suite and smoke test.

Status: not started.

## T2 — Notification database carve-out

Apply `migrations/gateway` to `seev_gateway` with role `gateway_app`. The notification queue and consumer behavior do not change; only the destination database connection moves.

**Tests:** notification integration and business end-to-end journey against the new database.

Status: not started.

## T3 — Final boundaries, build, and Compose

Finalize `boundary_test.go` with:

```text
gateway:       handler, notify
auth-service:  auth
ledger-service: ledger, policy
payin-service: payin
payout-service: payout
fraud-service: fraud
```

`vendorgw` remains a shared payin/payout library. The only cross-module production imports allowed are the published ledger events and generated wire contracts.

Add a `build-all` target, start all six processes in dependency order, and complete the Compose app profile with healthchecks and healthy-infrastructure dependencies. Document per-service environment variables. The expected development memory footprint is approximately 0.9–1.2 GB for the application services.

**DoD:** CI enforces the six-service map and a full Compose profile starts all services healthy.

Status: not started.

## T4 — Documentation

Update `PROJECT_GUIDE.md`, the root README, and the plan index with service names, ports, databases, call flows, boundary rules, and local startup instructions. Record future work such as an admin BFF, mTLS, payout status outbox, and routing/fee-rule caching.

Status: not started.

## Final verification

Run the master gate from plan 26 and a full Compose smoke test. Then continue to [plan 33](33-phase6h-fee-rules.md).
