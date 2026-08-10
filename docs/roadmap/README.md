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
| 56 · F0 | [Frontend platform foundation](active/56-f0-frontend-platform-foundation.md) | Establish the browser product and shared frontend rules | Active — ready for execution; implementation not started |
| 65 · A11 | [Database audit and hardening](active/65-a11-database-audit-and-hardening.md) | Repeated schema-safety, security, and business-completeness audit passes | In progress — Round 3 critical findings implemented; governance decisions and integration acceptance remain |

The same list is available in the [active-plan folder](active/README.md).

## Strategy

[Plan 42](42-long-term-roadmap.md) defines post-MVP tracks, activation
triggers, anti-scope, and evidence requirements. It is a planning framework,
not a promise that every track will be implemented.

[Plan 63](63-kubernetes-cloud-deployment-roadmap-v2.md) is the revised
cloud-deployment learning roadmap. Its first completed gate was
[plan 64 · K0](history.md) (complete 2026-08-09);
local disposable Kubernetes acceptance evidence exists; cloud/staging apply
evidence remains pending and is not implied by the local result.

## Completed history

The [concise plan history](history.md) records completed and superseded
outcomes and points to their durable current references. Full implementation
plans are intentionally removed from `main`; Git history preserves them when
an investigation needs their original task wording.

## Status meanings

- **Todo** — an executable design exists, but implementation has not started.
- **In progress** — implementation or acceptance evidence is still being collected; the status note identifies the current boundary.
- **Done** — the tracked scope was implemented and recorded in plan history.
- **Core done** — the safe vendor-neutral core is complete; optional external
  integration remains outside the repository.
- **Reference** — context or a decision record, not a task list.
- **Superseded** — summarized in history; use its named replacement.

## Execution rules

1. Verify the activation trigger before starting an active plan.
2. Follow its locked decisions and named prerequisites; do not infer current
   behavior from a historical baseline.
3. Run the relevant tests after every task.
4. Preserve financial invariants: append-only entries, exact minor units,
   idempotency, and balanced transactions.
5. Summarize a completed plan in `history.md`, extract durable decisions into
   an ADR, and remove the full plan only after its Definition of Done and
   repository verification pass.

The engineering requirements in the
[project guide](../development/project-guide.md) apply to every plan.

The frontend-specific strategy is recorded separately in [plan 55](55-frontend-long-term-roadmap.md);
its first executable foundation is active as [plan 56](active/56-f0-frontend-platform-foundation.md).
