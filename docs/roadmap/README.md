# Roadmap and Decision History

> [Documentation home](../README.md) · **Roadmap**

> **Status: Current index.** This page separates active execution plans from
> completed or superseded history. Current runtime behavior is documented in
> the reference and operations sections.

## Active plans

These plans are active execution records. Some contain implemented foundations
with acceptance work still open; begin or extend one only when its stated
trigger, prerequisites, and owner decision are satisfied.

| # | Plan | Purpose | Status |
|---|---|---|---|
| 35 | [Optional local Kubernetes](active/35-phase6j-kubernetes.md) | Learn local orchestration with kind | Todo |
| 56 · F0 | [Frontend platform foundation](active/56-f0-frontend-platform-foundation.md) | Establish the browser product and shared frontend rules | Active — ready for execution; implementation not started |
| 59 · C3 | [Multi-channel notifications](active/59-c3-multi-channel-notifications.md) | Extend Gateway notifications with durable email, push, preferences, and replay | In progress — implementation present; acceptance evidence pending |
| 60 · C4 | [End-to-end multi-currency](active/60-c4-end-to-end-multi-currency.md) | Activate existing currency primitives across the full money journey | In progress — implementation present; runtime acceptance evidence pending |
| 61 · C5 | [Advanced financial products and period close](active/61-c5-advanced-financial-products-period-close.md) | Add controlled accrual, scheduled-failure policy, and top-up fees | In progress — implementation present; runtime acceptance evidence pending |
| 62 · C6 | [Zero-downtime migration engine](active/62-c6-zero-downtime-migration-engine.md) | Build evidence-driven expand/contract and cutover machinery | In progress — implementation present; runtime acceptance evidence pending |
| 64 · K0 | [Deployment inventory and Kubernetes readiness baseline](active/64-k0-deployment-inventory-baseline.md) | Freeze and verify the runtime contract before Kubernetes manifests | In progress — inventory committed; runtime acceptance partial and K1 handoff pending |
| 65 · A11 | [Database audit and hardening](active/65-a11-database-audit-and-hardening.md) | Repeated schema-safety, security, and business-completeness audit passes | In progress — Rounds 1–2 fixed; Round 3 findings documented, not yet fixed |

The same list is available in the [active-plan folder](active/README.md).

## Strategy

[Plan 42](42-long-term-roadmap.md) defines post-MVP tracks, activation
triggers, anti-scope, and evidence requirements. It is a planning framework,
not a promise that every track will be implemented.

[Plan 63](63-kubernetes-cloud-deployment-roadmap-v2.md) is the revised
cloud-deployment learning roadmap. Its first executable gate is active
[plan 64 · K0](active/64-k0-deployment-inventory-baseline.md); neither plan
claims that Kubernetes or cloud deployment exists today.

## Archive

Plan 58 · C2 is core done: [Data platform and revenue analytics](archive/58-c2-data-platform-revenue-analytics.md).
Plan 57 · C1 is complete: [Merchant/B2B API](archive/57-c1-merchant-b2b-api.md).
Plan 53 · B0 is core done: [Load and capacity gate](archive/53-b0-load-capacity-gate.md).
Plan 54 is core done: [VendorService boundary](archive/54-vendor-service-boundary.md).
Plan 52 · A9 is core done: [API contracts and schema evolution](archive/52-a9-api-contracts-schema-evolution.md).
Plan 51 · A8 is complete: [Data lifecycle and privacy](archive/51-a8-data-lifecycle-privacy.md).

The [archive index](archive/README.md) contains 57 files organized as 55
numbered entries; entry 45 has two supporting review records. Archived plans
preserve the assumptions and task wording from their original phase. They may
say “current” while describing an older system shape; use the
[current architecture](../reference/architecture.md) and
[service reference](../reference/services.md) for runtime truth.

## Status meanings

- **Todo** — an executable design exists, but implementation has not started.
- **In progress** — implementation or acceptance evidence is still being collected; the status note identifies the current boundary.
- **Done** — the tracked scope was implemented and moved to the archive.
- **Core done** — the safe vendor-neutral core is complete; optional external
  integration remains outside the repository.
- **Reference** — context or a decision record, not a task list.
- **Superseded** — retained for history; use its named replacement.

## Execution rules

1. Verify the activation trigger before starting an active plan.
2. Follow its locked decisions and named prerequisites; do not infer current
   behavior from an archived baseline.
3. Run the relevant tests after every task.
4. Preserve financial invariants: append-only entries, exact minor units,
   idempotency, and balanced transactions.
5. Move a plan to the archive only after its Definition of Done and repository
   verification pass.

The engineering requirements in the
[project guide](../development/project-guide.md) apply to every plan.

The frontend-specific strategy is recorded separately in [plan 55](55-frontend-long-term-roadmap.md);
its first executable foundation is active as [plan 56](active/56-f0-frontend-platform-foundation.md).
