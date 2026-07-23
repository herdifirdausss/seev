# 31 — Phase 6f: Fraud Service Extraction

Read [plan 26](26-phase6a-foundations.md) first. Prerequisite: [plan 30](30-phase6e-payout-service-routing.md).

Screening currently lives inside ledger as `internal/ledger/screening` and implements a synchronous, fail-open `PrePostHook`. This phase moves the rules and `screening_events` to an internal fraud service while preserving that seam in ledger. Velocity changes from counting screening attempts to counting transactions that were actually posted; this is an intentional semantic improvement.

## T1 — Standalone `internal/fraud` module

Create the standard fraud module structure with rules, repository, model, facade, and errors. Move the amount-threshold, velocity, mode, and metrics logic from ledger. The facade accepts transaction type, user, amount, and currency and returns a block verdict and reason.

The velocity rule reads Redis keys such as `fraud:velocity:<user>:<hour>`. The counter is incremented by the event consumer in T3, not during `Screen`.

**Tests:** threshold and velocity behavior in off, monitor, and block modes.

**DoD:** fraud has no ledger dependency except the published events contract needed by its consumer.

Status: not started.

## T2 — Fraud protobuf, database, and ledger cleanup

Add `FraudService.Screen` and its gRPC server. Move `screening_events` to `migrations/fraud/000001_screening_events` and add currency, grants, and RLS. Remove the ledger-owned screening migration, repository, event-list route, rule construction, and screening configuration. Ledger retains only the hook interface and client implementation.

**Tests:** full unit suite after the ownership change and migration up/down verification.

Status: not started.

## T3 — Event-driven velocity consumer

Declare and consume `ledger.events.fraud` for `ledger.transaction.posted.v1`, following the notification consumer's topology and retry pattern. Decode `TransactionPosted` and increment the Redis DB 1 counter with a TTL of at least two hours.

**Tests:** handler unit tests with a fake counter and real RabbitMQ integration proving a posted transaction increments velocity.

Status: not started.

## T4 — Ledger gRPC hook

Keep a small `internal/ledger/screening/grpchook.go` package that implements `PrePostHook`. Call fraud-service with a 500 ms context timeout. Any error, including timeout or unavailable service, is returned to the existing fail-open pipeline. Configure the hook only when `FRAUD_GRPC_ADDR` is set; an empty address creates no hooks and preserves old behavior.

**Tests:** blocking response propagation and fail-open behavior when fraud is unavailable, using bufconn.

Status: not started.

## T5 — Fraud service runtime and end-to-end checks

Wire `seev_fraud` with role `fraud_app`, Redis DB 1, RabbitMQ, gRPC `:9094`, and internal HTTP `:8094` for admin events, health, and metrics. Add the service to scripts, Compose, and the boundary map.

Required scenarios:

- stopping fraud-service does not stop ledger posting, and the ledger records a fail-open error;
- block mode rejects an over-threshold transfer and records the event in `seev_fraud`.

Status: not started.

## Final verification

Run the master gate, fraud event-consumer integration test, and both fail-open and block-mode end-to-end scenarios. Then continue to [plan 32](32-phase6g-gateway-service.md).
