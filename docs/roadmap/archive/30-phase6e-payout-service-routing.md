# 30 — Phase 6e: Payout Service and Database-Driven Routing

Read [plan 26](26-phase6a-foundations.md) first. Prerequisite: [plan 29](29-phase6d-payin-service-routing.md) is complete. This phase follows the payin extraction pattern, with additional recovery requirements because it moves money out.

## Context

Move `internal/payout` to an internal binary and its own `seev_payout` database. The resume/polling worker and its Redis DB 0 distributed lock move with it. The client no longer sends a vendor; routing chooses one at creation time and stores it in `payout_requests.vendor` for later submit and poll steps.

## T1 — Payout protobuf and gRPC server

Add `PayoutService.CreatePayout(user_id, amount, destination, created_by)` and `GetPayout(id, user_id)`. Enforce ownership in the service and return `NotFound` for non-owners. Implement the gRPC server and test successful creation, insufficient funds, owner lookup, and non-owner lookup through bufconn.

Status: not started.

## T2 — Database-driven payout routing

Add `payout_vendor_gateways` and `payout_routing_rules` in migration `migrations/payout/000002_routing`, mirroring payin routing but with `flow='payout'`. Seed `mockvendor` and a fallback rule.

Resolve the route during `Create`, validate the selected vendor against the registry, store it in the request row, and resolve the ledger gateway through the database mapping. No match returns `NO_ROUTE`. Add admin CRUD for routing rules and vendor gateways.

**Tests:** routing precedence and filters plus a real database integration test proving that a rule controls payout creation.

Status: not started.

## T3 — `cmd/payout-service/main.go`

Wire `seev_payout` with role `payout_app`, Redis DB 0 for the resume-job lock, ledger client, vendor registry, gRPC `:9093`, and internal HTTP `:8093`. Start and stop the resume worker in this binary and provide `-healthcheck`.

Status: not started.

## T4 — Rewire the gateway

Forward payout create/get calls to gRPC while preserving existing JSON envelopes. Reject a client-supplied `vendor` field as an unknown field. Remove payout construction and worker startup from the gateway.

Status: not started.

## T5 — Database, scripts, Compose, and boundary cutover

Apply payout migrations to `seev_payout`, provision `payout_app`, add payout-service to host scripts and Compose, and register `payout-service: {payout}` in the boundary map. `internal/vendorgw` is a shared library and may be imported by both payin and payout.

Update the business journey to configure payout routing through the admin API, complete a fee-bearing withdrawal, and verify that cancellation refunds the full amount.

Status: not started.

## T6 — Cross-service crash recovery (mandatory)

Extend the payout chaos test to kill ledger-service between hold and settlement, restart it, and let payout-service recover through gRPC without double settlement. Also repeat the four payout kill points from plan 23 (`created`, `held`, `submitted`, and `vendor_pending`) after killing payout-service itself.

Every request must reach the correct terminal state, `fn_verify_ledger_balance` must return zero rows, and the `ErrAlreadyClosed` guard must be proven through the gRPC boundary rather than only in-process.

Status: not started.

## Final verification

Run the master gate and the cross-service crash suite, then continue to [plan 31](31-phase6f-fraud-service.md).
