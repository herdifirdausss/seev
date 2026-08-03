# K0 final acceptance

Status: not-ready-for-k1-static-gate-blocked
Recorded: 2026-08-03T00:57:52Z
Reviewer: Codex automated evidence review; human sign-off pending

The deployment inventory is complete and its machine-readable consistency
check passes, but the K0 exit gate is not closed. The latest
`make verify-static` run cannot compile the current worktree because the
modified `cmd/gateway/main.go` imports the absent packages
`internal/notify/channel/email` and `internal/notify/channel/push`. This
unresolved C3 mismatch is recorded as K0-F-011; it was not introduced or
silently changed by K0.

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
| all nine services and auxiliary tools classified | PASS | services.yaml |
| ports, routes, calls, stores, messaging, and jobs complete | PASS | deploy/inventory/*.yaml |
| configuration and secret values excluded | PASS | static-gate-summary.md, inventory checker |
| local runtime profiles | PARTIAL | R2 passed; R3 was not accepted after a reusable-state failover miss; R4 deferred for host CPU contention; service-probes/, runtime-journeys.md |
| R0–R6 resource profiles measured or explicitly deferred | PASS | resource-baseline.yaml, resource-baseline.md |
| make verify-static | BLOCKED | verification/static-gate-summary.md; K0-F-011 |
| make k0-inventory | BLOCKED | verification/static-gate-summary.md; K0-F-011 |
| make contracts | PASS | verification/static-gate-summary.md |
| make docs-check | PASS | verification/static-gate-summary.md |
| git diff --check | PASS | verification/static-gate-summary.md |
| make verify-full | DEFERRED | verification/static-gate-summary.md; K0/K9 host capacity guard |

## K1 entry decision

K1 must not start from this worktree until K0-F-011 is resolved and
`make verify-static` passes. After that rerun, K1–K6 may consume this contract
for the first synthetic deployment only. Real vendor network, production data,
production credentials, cloud resources, and Kubernetes mutation remain out of
K0 scope. The deferred R3/R4/R5/R6 and full verification items remain
explicitly owned follow-ups; they are not silently treated as passes.

Blocker count: 1 (K0-F-011).
