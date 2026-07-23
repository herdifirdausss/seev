# 34 — Phase 6i: Full-Stack Verification, Chaos, and Documentation

Read [plan 26](26-phase6a-foundations.md) first. Prerequisite: [plan 33](33-phase6h-fee-rules.md). This is the closing phase for the service split; plan 35 remains optional.

## T1 — Final business journey

`business-e2e.sh` boots the six host binaries and verifies:

1. registration and login through auth-service;
2. database-configured top-up routing and a signed webhook;
3. a per-user P2P fee and both user notifications;
4. routed payout settlement with a fee and full-fee-free cancellation;
5. ledger balance, projection, notification, fee-revenue, reconciliation, and dead-outbox assertions.

### Result

The final journey passed from a fresh volume. The script also checks the fee-rule admin surface and fraud-event admin surface. Shared helpers such as JSON extraction and notification polling now live in `scripts/lib.sh` rather than being duplicated.

## T2 — Multi-service chaos suite

The final suite covers:

- ledger-service crash during payout;
- RabbitMQ outage with outbox drain after recovery;
- fraud-service outage with fail-open posting;
- payin-service outage with webhook 503 and vendor redelivery;
- existing Redis-down and PostgreSQL-restart scenarios.

Every scenario ends with ledger-balance and projection assertions.

### Result

The suite passed cleanly from fresh processes and a fresh volume. During investigation, the following real issues were found and fixed:

1. `stop_server_gracefully` stopped only the gateway. It was split into gateway-only and all-service shutdown helpers.
2. Shell `wait` did not wait for processes launched by a subshell, so shutdown could race the next startup. A real PID-gone poll with escalation was added.
3. The old ledger restart path restarted all six services even though only one had been killed, causing port conflicts and stale PID files. It now restarts only the affected service.
4. payout-service ignored `REDIS_ENABLED=false`, so it could not use its supported in-memory lock fallback. Its startup wiring now matches ledger-service.
5. The payout resume lock used a five-minute default timeout, longer than the chaos recovery window. The payout resume job now uses a dedicated 30-second job timeout.

Repeated runs showed no balance loss or duplication. One timing-only harness assertion was intermittent under laptop load, but ledger and projection invariants remained green and the issue did not reproduce on the following clean run.

## T3 — Full Compose smoke test

Start all infrastructure and six services with `docker compose --profile app up --build -d`. Verify that every container becomes healthy, then complete a public gateway journey:

```text
register/login → create top-up intent → signed mockvendor webhook
               → payin → ledger → settled balance
```

### Result

All nine containers (three infrastructure containers and six services) became healthy. A public gateway webhook returned 200, the payin intent became settled, and the user's ledger cash account increased by the expected minor-unit amount. The Compose stack was shut down cleanly afterward.

## T4 — Final documentation

Update the plan index and `PROJECT_GUIDE.md` with the six-service architecture, service-down recovery notes, and future work: admin BFF, mTLS, payout status outbox, routing and fee-rule caching, and real vendor adapters.

### Result

The documentation and plan index were updated. Each service has a short operational note describing what fails when it is down, what remains safe, and how to recover. The root README already reflected the six-service architecture.

## Final verification

```bash
make lint
make test
go vet ./...
go vet -tags=integration ./...
./scripts/smoke-test.sh
./scripts/business-e2e.sh
./scripts/chaos-test.sh all
docker compose --profile app up --build -d
```

The service split is complete when all of the above are green. Plan 35 is optional local Kubernetes practice.
