# K0 final acceptance

Status: ready-for-k1
Recorded: 2026-08-09
Reviewer: automated full-gate run; human sign-off pending

The deployment inventory is complete, the machine-readable consistency check
passes, and `make verify-full` now passes end-to-end. All integration tests
(47 balancev2 + full suite, race detector), smoke-container, smoke-test,
business/capability/admin/privacy/merchant/migration-balance-v2 E2E suites,
and load-smoke all exited 0. The balance-v2 migration engine fixes that
unblocked this run are recorded in git history.

This file is finalized only after the machine-readable inventory check,
repository gates, and safe local runtime checks have been executed. The
baseline commit and pre-K0 working-tree state are recorded in
[baseline.md](baseline.md) and
[baseline-pre-k0.status](baseline-pre-k0.status).

## Required evidence

- [K0 machine-readable inventory](../../../deploy/inventory/)
- [K0 deployment documentation](../../deployment/README.md)
- [inventory hashes](generated/inventory-sha256.txt)
- [canonical verification outputs](verification/)
- [local network probes](network/)
- [service probes](service-probes/)
- [resource profiles](resources/)

## Acceptance ledger

| Gate | Result | Evidence |
|---|---|---|
| baseline pinned and working-tree rule recorded | PASS | baseline.md, baseline-pre-k0.status |
| all nine core services and auxiliary tools classified | PASS | services.yaml |
| ports, routes, calls, stores, messaging, and jobs complete | PASS | deploy/inventory/*.yaml |
| configuration and secret values excluded | PASS | static-gate-summary.md, inventory checker |
| local runtime profiles | PARTIAL | R2 passed; R3 was not accepted after a reusable-state failover miss; R4 deferred for host CPU contention; service-probes/, runtime-journeys.md |
| R0–R6 resource profiles measured or explicitly deferred | PASS | resource-baseline.yaml, resource-baseline.md |
| make verify-static | PASS | verification/static-gate-summary.md |
| make k0-inventory | PASS | verification/static-gate-summary.md |
| make k0-inventory-check | PASS | verification/static-gate-summary.md |
| make contracts | PASS | verification/static-gate-summary.md |
| make docs-check | PASS | verification/static-gate-summary.md |
| git diff --check | PASS | verification/static-gate-summary.md |
| make verify-full | PASS | 2026-08-09 full run: all integration tests, smoke-container, smoke-test, 6 E2E suites, load-smoke; EXIT:0 |

## K1 entry decision

All K0 gates now pass. `make verify-full` exited 0 on 2026-08-09 with race
detector enabled across the full integration + E2E + load suite. The static
inventory, machine-readable consistency check, and runtime acceptance gates are
all green. K1 may proceed once a human reviews and signs off this record.

Real vendor network, production data, production credentials, cloud resources,
and Kubernetes mutation remain out of K0 scope and are explicitly blocked until
K1–K6 authorize them.

Blocker count: 0.
