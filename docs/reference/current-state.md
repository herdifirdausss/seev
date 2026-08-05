# Current State

> [Documentation home](../README.md) · [Reference](README.md)
>
> **Status: Current. Reviewed: 2026-08-03 against the current `main` tip and the
> working tree.** This is an implementation inventory. “Implemented” means the
> code, migrations, and local contracts are present; it does not mean runtime
> acceptance or production readiness is complete.

## Runtime shape

Seev has nine core business services: Gateway, Auth, Ledger, Payin, Payout,
Fraud, Admin BFF, Assurance, and VendorService. The optional
`mock-push-provider` is a local notification sink on port 8097; it is not a
tenth business service. The optional C2 analytics stack is infrastructure for a
read-only projection, not another business service.

The [service reference](services.md) remains the detailed ownership and route
map. The [deployment inventory](../deployment/service-runtime-inventory.md)
and [port matrix](../deployment/service-port-matrix.md) describe the runtime
contract used by the K0 work.

## Feature status

| Track | Current boundary |
|---|---|
| C1 | Core merchant/B2B API is complete and archived. |
| C2 | CDC-to-OLAP, revenue facts, reconciliation, Metabase dashboards, and Prometheus metrics/alerts are implemented and runtime-verified against real data, including real outage/recovery evidence for Connect, Redpanda, ClickHouse, Metabase, and a full Docker-daemon crash; a deliberate incompatible-schema-change drill, a WAL-pressure drill, and a Grafana operational dashboard remain open — see [c2-final-acceptance.md](../evidence/c2-final-acceptance.md). |
| C3 | Durable in-app, email, push, digest, preferences, templates, and delivery controls are implemented in Gateway; runtime and acceptance evidence is pending. |
| C4 | IDR/USD account provisioning, explicit FX quotes/conversions, currency policy, positions, admin controls, and reconciliation paths are implemented in Ledger; runtime and acceptance evidence is pending. |
| C5 | Savings products/rates, daily accrual and period-close foundations, durable schedule occurrences/policies, and top-up fee paths are implemented in Ledger; runtime and acceptance evidence is pending. |
| C6 | The `account_balances_v2` migration control plane, backfill, dual-write/shadow-read, target-primary cutover, repair, and rollback machinery are implemented in Ledger; runtime and acceptance evidence is pending. |
| K0 | Deployment inventory artifacts are committed. Static verification is now green; local runtime acceptance remains partial and the K1 handoff is not yet authorized. |
| F0 | Frontend platform work remains planned and has not started. |
| A11 | Round 3 audit findings remain open; see [plan 65](../roadmap/active/65-a11-database-audit-and-hardening.md). |

## Implemented extensions

- C2 is opt-in and keeps OLTP authoritative. Its CDC, Redpanda/Kafka Connect,
  ClickHouse, dbt, Metabase, and reconciliation pieces are started through
  the analytics profiles and targets in the Makefile.
- C3 keeps notification ownership inside Gateway. Mailpit and
  `mock-push-provider` are local sinks selected by the notifications Compose
  profile; external delivery runs after the planning transaction commits.
- C4 keeps money ownership inside Ledger. Ordinary postings remain
  single-currency; cross-currency movement uses explicit, database-managed
  mock-rate quotes and linked FX legs for IDR and USD.
- C5 keeps savings, schedules, period-close evidence, and top-up fee
  settlement inside Ledger. No new application service was added.
- C6 keeps migration state and cutover controls inside Ledger. The reference
  target is the `account_balances_v2` projection, with bounded comparison
  evidence and rollback controls.

## Verification boundary

The current working tree passes the repository's static gate:

```text
make verify-static   PASS
make docs-check      PASS
```

The latest focused runtime regression run also passes:

```text
go test -tags=integration ./services/payin ./services/gateway/internal/notification ./operations/recovery/drreseed -count=1  PASS
Ledger/Auth currency and multi-owner regression tests                 PASS
```

These checks cover the post-C4 migration invariants, notification outbox
delivery, currency-aware policy-counter reseeding, currency error mapping,
and multi-owner payin/auth journeys. A fresh full `verify-full` run is still
required before runtime acceptance is declared complete.

This gate covers compilation, vet, module verification, CI lint, safe Go
modernizers, golangci-lint, vulnerability scanning, contracts, documentation,
and load-safety checks. It does not replace disposable runtime journeys,
chaos, resource profiling, real-provider certification, or production
readiness review. Those remain explicit follow-ups in the active plans and K0
evidence.

For the next review, use this page for the boundary, the [roadmap index](../roadmap/README.md)
for plan status, and the [traceability map](traceability.md) for code and test
evidence.
