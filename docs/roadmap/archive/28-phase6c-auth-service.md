# 28 — Phase 6c: Auth Service Extraction

Read [plan 26](26-phase6a-foundations.md) first. Prerequisite: [plan 27](27-phase6b-ledger-service.md) is complete and auth's `Provisioner` uses `pkg/ledgerclient`.

`internal/auth` already has a clean boundary and only depends on the provisioning interface. This phase moves it to its own public binary and database:

- public listener `:8082` for register, login, refresh, and `/users/me`;
- internal listener `:8083` for health and metrics;
- database `seev_auth` with role `auth_app`.

All services continue to verify JWTs locally with the shared `JWT_SECRET`; they do not call auth for token introspection.

## T1 — `cmd/auth-service/main.go`

Wire configuration, logging, `seev_auth`, the ledger gRPC client, `auth.NewModule`, and `EnsureBootstrapAdmin`. Auth does not use RabbitMQ. Move the existing public routes into the auth service while preserving request ID, logging, recovery, security headers, CORS, IP rate limiting, JSON validation, and JWT middleware behavior. Add `-healthcheck` mode.

**Tests:** register, login, refresh rotation, and authenticated profile access through the standalone router.

**DoD:** auth-service provisions ledger accounts through gRPC and runs independently.

Status: not started.

## T2 — Database cutover and gateway cleanup

Apply `migrations/auth` to `seev_auth`, provision `auth_app`, and remove auth construction, bootstrap-admin wiring, and auth routes from the gateway. Extend `scripts/lib.sh` to start auth-service and update `business-e2e.sh` so registration and login happen at `:8082`, while the resulting JWT is used at the gateway.

**Tests:** the full business journey proves a JWT issued by auth-service is accepted by the gateway and ledger service.

**DoD:** password and refresh-token data live only in `seev_auth`; the gateway has no auth database dependency.

Status: not started.

## T3 — Boundary and Compose updates

Register `auth-service: {auth}` in `boundary_test.go`; its command may import only auth, config, shared packages, and generated protobufs. Add the Compose service with ports 8082/8083, healthcheck, and dependencies on PostgreSQL and ledger-service.

**DoD:** the three-service topology is enforced by CI and starts successfully.

Status: not started.

## Final verification

Run the master gate and verify register → login → top-up → transfer with a JWT issued by auth-service and accepted by both gateway and ledger-service. Then continue to [plan 29](29-phase6d-payin-service-routing.md).
