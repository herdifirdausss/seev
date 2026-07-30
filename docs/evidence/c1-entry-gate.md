# C1 Entry-Gate Evidence — Plan 57 T0

> [Documentation home](../../README.md) · [Roadmap](../roadmap/README.md) ·
> [Plan 57](../roadmap/archive/57-c1-merchant-b2b-api.md)

This is the T0 entry-gate re-inventory required by
[Plan 57 §2.2](../roadmap/archive/57-c1-merchant-b2b-api.md#22-required-entry-gate-evidence)
before any C1 implementation work may merge.

## Baseline

```text
commit: d20e5295ef0cdbbc44816af239c90c3d7514439b
date:   2026-07-28 23:01:01 +0700
branch: main
tree:   clean (git status --short empty)
```

## Entry-gate checklist results

| Requirement | Result | Evidence |
|---|---|---|
| `make contracts` passes from a clean tree | PASS | `contract-generate`/`contract-lint`/`contract-breaking`/`contract-test` all exit 0, no `api/openapi/dist` diff |
| Generated HTTP operation inventory has no unresolved drift | PASS | same run; `contract-generate` regenerates `api/openapi/dist/*.yaml` deterministically, `git status` clean after |
| Current event schemas and Protobuf semantic checks pass | PASS | `api/contracts` test suite (`event_compatibility_test.go`, `proto_semantics_test.go`, `proto_reserved_test.go`, `proto_rollout_test.go`) all green as part of `make contracts` |
| Every existing operation C1 will call/extend has a canonical contract entry | PASS (see inventory doc) | `api/contracts/surfaces.yaml` — ledger transfer/account, payin, payout, admin BFF operations C1 depends on are all already registered |
| Every C1-touched existing HTTP operation has a live fixture or a recorded same-PR task | N/A THIS TASK | No existing operation is touched by T0 (docs/inventory only); tracked per-operation in T1+ as contracts are added |
| A6 internal-auth and mTLS verification commands pass | PASS | `./scripts/smoke-test.sh all`, `./scripts/business-e2e.sh`, `./scripts/admin-e2e.sh` all boot all 9 services over the existing mTLS mesh (`pkg/tlsx`) and complete; no service failed a peer-cert check |
| Existing business, admin, callback, and smoke journeys remain green | PASS | see command log below — all three scripts: `=== ALL SMOKE ASSERTIONS PASSED ===`, `=== FULL BUSINESS JOURNEY PASSED ===`, `admin-e2e completed` with all `[ pass]` lines |
| Working branch contains no unrelated schema or topology migration | PASS | `git status --short` empty at baseline; migration heads recorded below are the actual current heads, not touched by this task |
| Exact baseline commit is recorded in this plan's evidence log | PASS | recorded above and in Plan 57 §32 |

**Gate disposition: PASS.** C1 implementation work (T1 onward) may begin.

## Command log

```text
$ make contracts
go run ./cmd/contractgenerate
go test ./api/contracts
ok  	github.com/herdifirdausss/seev/api/contracts	0.577s
go run ./cmd/contractcheck -mode breaking
go test ./pkg/httpcontract ./api/contracts
ok  	github.com/herdifirdausss/seev/pkg/httpcontract	(cached)
ok  	github.com/herdifirdausss/seev/api/contracts	0.273s
(exit 0)

$ make build
go build -trimpath -ldflags="-s -w" -o "bin/gateway" "./cmd/gateway"
(exit 0)

$ ./scripts/smoke-test.sh all
... 9 services booted over mTLS, ledger/payin/payout/admin journeys ...
=== ALL SMOKE ASSERTIONS PASSED ===

$ ./scripts/business-e2e.sh
... registration, topup, transfer, withdraw, fee quotes, failover, tracing ...
=== FULL BUSINESS JOURNEY PASSED — MVP end-user-to-daily-ops verified ===

$ ./scripts/admin-e2e.sh
... admin BFF login/CSRF/dead-command replay/audit/batch-2 panels ...
admin-e2e completed

$ docker compose down -v --remove-orphans
(clean teardown, no dangling containers/volumes)
```

## Migration heads at baseline (no unrelated migration present)

| Service | Latest migration |
|---|---|
| ledger | `000032_retention_scheduled_transactions` |
| auth | `000017_closure_finalize_grant` |
| payin | `000013_normalized_callbacks` |
| payout | `000013_retention_commands` |
| fraud | `000007_retention_screening_events` |
| gateway | `000003_retention_purge_functions` |
| adminbff | `000007_retention_purge_audit_log` |
| assurance | `000007_retention_remaining` |
| vendor | `000001_vendor_boundary` |

C1's own migrations (T2 onward) start at the next free number per service —
`gateway` is the primary target for new merchant tables (`000004_...`), per
Plan 57 §3.1's "Gateway-owned persistence only" rule.

## Cross-reference

Full contract, ownership, and reuse inventory:
[docs/reference/c1-current-contract-inventory.md](../reference/c1-current-contract-inventory.md).
