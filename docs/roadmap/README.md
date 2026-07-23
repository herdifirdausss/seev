# Roadmap and Decision History

> [Documentation home](../README.md) · **Roadmap**

> **Status: Current index.** This page separates future execution plans from
> completed or superseded history. A plan is not current product behavior.

## Active plans

These plans are designed but not implemented. Begin one only when its stated
trigger, prerequisites, and owner decision are satisfied.

| # | Plan | Purpose | Status |
|---|---|---|---|
| 35 | [Optional local Kubernetes](active/35-phase6j-kubernetes.md) | Learn local orchestration with kind | Todo |
| 50 · A7 | [Backup, PITR, and disaster recovery](active/50-a7-backup-pitr-disaster-recovery.md) | Build and prove cluster-wide recovery | Todo |
| 51 · A8 | [Data lifecycle and privacy](active/51-a8-data-lifecycle-privacy.md) | Govern retention, export, encryption, and pseudonymization | Todo |
| 52 · A9 | [API contracts and schema evolution](active/52-a9-api-contracts-schema-evolution.md) | Add compatibility and deprecation gates | Todo |
| 53 · B0 | [Load and capacity gate](active/53-b0-load-capacity-gate.md) | Measure whether later scale work is justified | Todo |
| 54 | [VendorService boundary](active/54-vendor-service-boundary.md) | Isolate vendor connectivity and callback ingress | Todo |

The same list is available in the [active-plan folder](active/README.md).

## Strategy

[Plan 42](42-long-term-roadmap.md) defines post-MVP tracks, activation
triggers, anti-scope, and evidence requirements. It is a planning framework,
not a promise that every track will be implemented.

## Archive

The [archive index](archive/README.md) contains 50 files organized as 48
numbered entries; entry 45 has two supporting review records. Archived plans
preserve the assumptions and task wording from their original phase. They may
say “current” while describing an older system shape; use the
[current architecture](../reference/architecture.md) and
[service reference](../reference/services.md) for runtime truth.

## Status meanings

- **Todo** — an executable design exists, but implementation has not started.
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
