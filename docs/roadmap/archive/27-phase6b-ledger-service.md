# 27 — Phase 6b: Ledger Service Extraction

Read [plan 26](26-phase6a-foundations.md) first. This phase extracts the ledger and policy modules before any other service, because every other module depends on them. Later phases reuse the same protobuf, client-shim, multi-process, and boundary-enforcement pattern.

When complete, `cmd/ledger-service` runs against `seev_ledger` with gRPC on `:9091`, user HTTP on `:8090`, and internal admin HTTP on `:8091`. The shrinking monolith calls ledger only through gRPC or the ledger reverse proxy. `internal/policy` moves with ledger because `policy_limits` belongs to the ledger database.

## T1 — Ledger protobuf

Add `api/proto/seev/ledger/v1/ledger.proto` with:

- `Post`;
- `GetTransactionByIdempotencyKey`;
- `GetUserCurrency`;
- `ResolveFee`;
- `ProvisionUser`.

Mirror the processor command while representing amounts as decimal strings, UUIDs as strings, and metadata as `google.protobuf.Struct`. Generate and lint the code with Buf, then commit `gen/ledger/v1`.

**Tests:** deterministic `make proto` output and `make proto-lint`.

Status: not started.

## T2 — gRPC server in the ledger module

Add `internal/ledger/grpcserver` implementing the generated service. Convert protobuf messages to facade and processor types, map `Struct` to `map[string]any`, reject non-integral amounts, and map errors according to the master contract:

- typed ledger errors → `FailedPrecondition` with `ErrorInfo`;
- already-closed operations → `Aborted`;
- not found → `NotFound`;
- unexpected errors → `Internal`.

Expose `RegisterGRPC` on `ledger.Module`.

**Tests:** bufconn tests for every RPC and error branch, plus a real PostgreSQL integration test that posts `money_in` through gRPC and verifies a balanced ledger.

Status: not started.

## T3 — `pkg/ledgerclient` shim

Add a public client package that owns its own `Command` and `Transaction` types. It must not import `internal/ledger`. The client exposes `Post`, transaction lookup, currency lookup, fee resolution, and user provisioning.

Every returned gRPC error passes through `ledgererr.FromStatus`, so callers can still use `errors.As` for typed ledger errors and `errors.Is` for `ErrAlreadyClosed`.

**Tests:** bufconn round trips against a hand-written fake server, including wire-format and error behavior. Do not use the internal ledger module in these tests.

Status: not started.

## T4 — Re-type consumers and remove ledger imports

Update payin and payout interfaces to use `ledgerclient` types and `ledgererr` errors. Add context to payout fee resolution. Change auth provisioning to return only an error; callers currently ignore the account list. Update every mock and stub.

**DoD:** production packages under `internal/payin`, `internal/payout`, and `internal/auth` no longer import the ledger facade. The only permitted ledger-related import is the published events contract.

Status: not started.

## T5 — `cmd/ledger-service/main.go`

Create a standalone composition root that wires configuration, logging, tracing, PostgreSQL, Redis, RabbitMQ, policy, ledger, currencies, fee rules, and workers against `seev_ledger`.

Listeners:

- gRPC `:9091` with `grpcx` and `RegisterGRPC`;
- user HTTP `:8090` with health, readiness, JWT middleware, and ledger routes;
- internal HTTP `:8091` with metrics, admin ledger routes, and policy administration.

Add `-healthcheck` self-probe mode for a distroless container and preserve the existing graceful shutdown order.

**Tests:** standalone boot, gRPC health, HTTP health, readiness, and metrics.

Status: not started.

## T6 — Rewire the monolith as a client

Remove ledger and policy construction from `cmd/server`. Dial the ledger gRPC endpoint and inject `ledgerclient` into payin, payout, and auth. Replace the monolith's in-process ledger user routes with a path-preserving reverse proxy to `LEDGER_USER_API_URL`. The internal monolith router no longer mounts ledger or policy handlers.

Add `LEDGER_GRPC_ADDR`, `LEDGER_USER_API_URL`, and `INTERNAL_GRPC_TOKEN` configuration.

**Tests:** route proxy tests against an `httptest` backend and full business journey through the proxy.

Status: not started.

## T7 — Database cutover and runtime scripts

Apply `migrations/ledger` to `seev_ledger`, update scripts to accept an explicit database, grant the ledger app role, and point all ledger integrity assertions at `seev_ledger`.

Rename the host orchestration helper to `start_services`: build and launch ledger-service plus the monolith on their separate ports, with the monolith pointing at ledger. Add the ledger service to the Compose app profile with health dependencies.

**Tests:** smoke and business end-to-end tests with two processes and a fresh ledger database.

Status: not started.

## T8 — Boundary test v2

Add a service-to-module map to `boundary_test.go`. The ledger service may import ledger and policy; the remaining production modules may not import `internal/ledger`, except for the events contract. Service command packages may import their own module, config, shared packages, and generated protobufs.

Include a negative test: an intentionally illegal import must make the boundary suite fail.

Status: not started.

## Final verification

Run the master gate from plan 26 and then perform the service-failure check: killing ledger-service must make the monolith's readiness degrade and ledger routes return 502; restarting ledger-service must restore the system without manual data repair.
