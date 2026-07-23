# 29 — Phase 6d: Payin Service and Database-Driven Top-Up Routing

Read [plan 26](26-phase6a-foundations.md) first. Prerequisite: [plan 28](28-phase6c-auth-service.md) is complete.

This phase moves payin to the internal `payin-service` and introduces database-driven top-up routing. The public webhook edge remains in the gateway and forwards the vendor, headers, and exact raw body bytes over gRPC.

The vendor is no longer supplied by the client or hard-coded in `cmd/server`. An admin-configured rule selects it using currency, amount range, user override, priority, enabled state, and a fallback.

## T1 — Payin protobuf and gRPC server

Add `PayinService` with:

- `HandleWebhook(vendor, headers, raw_body)`;
- `CreateTopupIntent(user_id, amount)` — no vendor field;
- `GetTopupIntent(id, user_id)`.

Preserve the webhook result contract: success, ignored non-settled event, and acknowledged business failure are successful responses; unknown vendor is `NotFound`; invalid signature is `Unauthenticated`; infrastructure failure is `Internal` or `Unavailable`.

**Tests:** bufconn coverage for every outcome.

Status: not started.

## T2 — Database-driven routing

Add migration `migrations/payin/000003_routing` with:

- `payin_vendor_gateways(vendor, gateway)`;
- `payin_routing_rules` containing flow, priority, enabled, currency, inclusive amount bounds, optional user override, and vendor.

Seed `mockvendor → bca` and an all-purpose fallback rule at priority 1000. Resolve in one query, preferring a user-specific rule and then the lowest priority. Disabled rules are ignored; no match returns `ErrNoRoute`, mapped to HTTP 422 with `NO_ROUTE`.

`CreateTopupIntent` resolves and stores the vendor. Webhook processing resolves the ledger gateway from the database mapping rather than a hard-coded map. Admin endpoints manage routing rules and vendor gateways, validating vendors against the registry and gateways against the payin-owned valid-gateway list or a client-level validation contract without importing ledger subpackages.

**Tests:** resolution matrix for user overrides, currency and amount filters, disabled rules, fallback, and no route; PostgreSQL integration test proving a real rule controls intent creation.

**DoD:** no top-up path contains a hard-coded vendor or gateway, and an operator can change routing without deploying.

Status: not started.

## T3 — `cmd/payin-service/main.go`

Wire `seev_payin` with role `payin_app`, the configured vendor registry, ledger client, gRPC `:9092`, and internal HTTP `:8092` for admin routes, health, and metrics. Payin does not use RabbitMQ or Redis in this phase. Add `-healthcheck`.

Status: not started.

## T4 — Rewire the gateway

Forward webhook requests with raw bytes and headers to payin-service and map gRPC outcomes back to the existing 200/401/404/503 HTTP contract. Forward top-up create/get requests over gRPC while preserving their JSON response envelopes. Remove the in-process payin module from the gateway.

**Tests:** fake-service handler tests for all status mappings and a raw-body byte-preservation test.

Status: not started.

## T5 — Database, scripts, Compose, and boundary cutover

Apply `migrations/payin` to `seev_payin`, provision `payin_app`, add payin-service to host scripts and Compose, and register `payin-service: {payin, vendorgw}` in the boundary map.

Update `business-e2e.sh` to create a routing rule through the payin admin API, complete a routed top-up, and verify the negative case where all rules are disabled returns `NO_ROUTE`.

Status: not started.

## Final verification

Run the master gate and payin chaos scenario: stop payin-service, verify the webhook returns 503, restart it, deliver the vendor retry, and confirm one credit plus a clean ledger verifier. Then continue to [plan 30](30-phase6e-payout-service-routing.md).
