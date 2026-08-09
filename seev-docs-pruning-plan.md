# Seev Documentation Pruning Plan

> Scope: `docs/` on `main`, reviewed against the repository state on 2026-08-09.
>
> Objective: reduce documentation volume, remove stale/duplicated status documents, and keep only documentation that is useful for the current system.
>
> Guiding rule: **Git history is the archive. `docs/` is the current knowledge base.**

---

# 1. Target Documentation Policy

A Markdown document should stay in `main` only if it serves at least one of these purposes:

1. **Current reference** — explains behavior or contracts that exist now.
2. **Current operations** — needed to deploy, operate, recover, or troubleshoot the system.
3. **Current development guidance** — needed repeatedly by contributors.
4. **Active execution plan** — contains work that has not yet been completed.
5. **Durable architectural decision** — explains *why* a non-obvious decision was made and is still relevant.
6. **Generated current view** — generated from a machine-readable source and checked for freshness.

A document should **not** remain in `main` only because:

- it was once useful;
- it proves that a completed plan existed;
- it contains an old implementation checklist;
- it records a temporary review;
- it records an old status snapshot;
- its information already exists in code/config/tests/current documentation;
- Git history already preserves it.

---

# 2. Lifecycle Rules

## Active plan

Keep while work is genuinely open.

When completed:

1. update current reference/operations docs;
2. extract durable decisions into an ADR if necessary;
3. record one concise completion row in `roadmap/history.md`;
4. remove the full plan file from `main`.

Do **not** move every completed plan into a permanent Markdown archive.

---

## Core done but acceptance remains

Do not preserve a huge implementation plan only for a few remaining gates.

Instead:

1. mark implementation outcome in current reference;
2. extract remaining acceptance work into a small active follow-up;
3. delete the completed implementation plan;
4. retain only the currently actionable acceptance items.

---

## Superseded plan

Record:

```text
Plan ID | Superseded by | Date | Short reason
```

in `roadmap/history.md`, then delete the old plan.

Git history remains available if old reasoning is ever needed.

---

## Completed evidence

Do not keep per-phase entry-gate/final-acceptance Markdown forever.

Prefer:

- automated tests;
- CI artifacts;
- benchmark artifacts;
- GitHub PRs;
- release records;
- one current runtime-acceptance summary.

Keep a durable evidence document only when the result itself is operationally or legally useful.

---

## Temporary review / audit

Once triaged:

1. convert surviving findings into active plans/issues;
2. extract durable decisions if needed;
3. delete the review document.

A review should not become a second roadmap.

---

# 3. Recommended Target Top-Level Structure

Target:

```text
docs/
├── README.md
├── learn/
├── reference/
│   ├── README.md
│   ├── architecture.md
│   ├── services.md
│   ├── api-contracts.md
│   ├── events.md
│   ├── notifications.md
│   ├── analytics.md
│   ├── traceability.md
│   ├── glossary.md
│   └── decisions/
├── development/
├── deployment/
│   ├── README.md
│   └── runtime-contract.md       # generated
├── operations/
│   ├── README.md
│   └── runbooks/
├── performance/
├── security/
├── data/
├── portfolio/
└── roadmap/
    ├── README.md
    ├── history.md
    └── active/
```

Target top-level directories to eliminate:

```text
docs/acceptance/
docs/architecture/
docs/engineering/
docs/evidence/
docs/improvement/
docs/threat-models/
docs/roadmap/archive/
```

Their useful current content should be merged into one of the canonical locations above before deletion.

---

# 4. High-Confidence Cleanup

## A. `docs/improvement/`

### Recommendation: DELETE the directory after extracting open work

Current files are old cross-functional audit/improvement plans.

They compete with:

```text
docs/roadmap/
docs/engineering/improvement-plan-tracker.md
```

The engineering tracker already acts as an execution record for at least one of those source plans.

### Action

For each file:

```text
docs/improvement/*.md
```

- [ ] extract still-open items;
- [ ] map each item to an existing active plan, a new small plan, or a GitHub issue;
- [ ] extract a durable architectural decision only when needed;
- [ ] delete the original review/plan.

### End State

```text
docs/improvement/  # removed
```

Do not create `docs/reviews/archive/`.

Git history is enough.

---

# 5. `docs/roadmap/archive/`

### Recommendation: REMOVE the permanent Markdown archive

The directory currently contains dozens of old plans, snapshots, reviews, and completed implementation tracks.

This is the largest source of documentation volume and historical cognitive load.

### Replace it with

```text
docs/roadmap/history.md
```

Example:

```markdown
| Plan | Outcome | Completed | Durable reference |
|---|---|---|---|
| 57 / C1 | Core B2B API implemented | 2026-08-04 | reference/api-contracts.md |
| 58 / C2 | Analytics core implemented | 2026-08-04 | reference/analytics.md |
| 60 / C4 | Scheduled transactions accepted | 2026-08-05 | reference/... |
| 61 / C5 | Dispute workflow accepted | 2026-08-05 | reference/... |
```

The history table should contain outcomes, not implementation steps.

### Migration Safety Rule

Before deleting each archived plan, check:

> Does this file contain a still-relevant design decision that is not represented in current docs?

If yes:

- extract that decision into an ADR;
- then delete the plan.

If no:

- delete directly.

### Likely Categories to Delete

- old `current-state` snapshots;
- old target architecture documents superseded by the actual implementation;
- completed implementation plans;
- old backlog reviews;
- completed phase plans;
- superseded plans;
- historical implementation evidence.

### End State

```text
docs/roadmap/
├── README.md
├── history.md
└── active/
```

No `archive/`.

---

# 6. Current Roadmap Cleanup

There is currently a status mismatch between the roadmap index and the physical active-plan directory.

The index still exposes some C4/C5/C6/K0 state in the active-plan area even though the active directory currently contains only plans 35, 56, and 65.

This is exactly the type of drift the pruning model should eliminate.

## Action

- [ ] make `roadmap/active/` the only location for executable plans;
- [ ] derive the active plan table from that directory/metadata;
- [ ] do not manually duplicate active status in multiple indexes;
- [ ] completed plans go to `history.md`, not `archive/`.

### Plan 35

`35-phase6j-kubernetes.md` is an older optional/local Kubernetes learning plan.

If Plan 63/K-series is now the canonical Kubernetes/cloud deployment direction:

- [ ] merge any still-useful local-learning steps into the deployment guide or K-series strategy;
- [ ] delete Plan 35.

Keep Plan 35 only if it still represents genuinely independent planned work.

### Plan 56

Frontend foundation is still active/planned.

**KEEP.**

### Plan 65

Database audit/hardening still contains open work.

**KEEP.**

### K0 / Plan 64

Current documentation indicates K0 still has unfinished runtime/handoff acceptance.

Do not call it finished merely to reduce files.

Instead:

- [ ] keep only the remaining executable K0 work active;
- [ ] remove completed implementation detail from the active document;
- [ ] when gates close, write one history row and delete the full plan.

---

# 7. `docs/architecture/`

### Recommendation: MERGE and REMOVE top-level directory

Current architecture should live under `reference/`.

## Keep as durable decision

### `adr-service-centric-repository-layout.md`

**KEEP, but move.**

Target:

```text
docs/reference/decisions/0001-service-centric-repository-layout.md
```

An accepted ADR is worth preserving because it explains *why* the repository is structured that way.

---

## Merge into current reference

### `data-platform.md`

Merge current behavior into:

```text
docs/reference/analytics.md
```

Then delete the original.

### `notification-platform.md`

Merge into:

```text
docs/reference/notifications.md
```

Then delete the original.

### `data-migration-platform.md`

Keep only current behavior.

Target:

```text
docs/reference/data-migration.md
```

or merge high-level architecture into `reference/architecture.md` and operational procedures into an operations runbook.

Do not keep a separate top-level architecture namespace.

### `repository-ownership.md`

Merge:

- service ownership → `reference/services.md`;
- “where to start changing code” → `development/code-map.md`.

Then delete.

---

## Delete snapshot/evidence files

### `repository-structure-metrics.md`

**DELETE from Git.**

This is a high-churn generated snapshot.

Prefer:

```bash
make architecture-metrics
```

to print/generate it on demand or expose it as a CI artifact.

### `repository-structure-reorganization-evidence.md`

**DELETE after verifying current reference already represents the result.**

The migration is complete and its durable rationale is already better represented by the accepted ADR/current repository structure.

Git history preserves the implementation evidence.

---

# 8. `docs/threat-models/`

### Recommendation: MERGE and REMOVE

Current top-level folder contains the data-platform threat model while security already has its own threat model documentation.

Merge:

```text
docs/threat-models/data-platform.md
```

into:

```text
docs/security/threat-model.md
```

as a domain section, unless it is large enough to justify:

```text
docs/security/data-platform-threats.md
```

Prefer the single-document option if readability remains reasonable.

Then remove:

```text
docs/threat-models/
```

---

# 9. `docs/acceptance/`

### Recommendation: REMOVE feature-by-feature completed acceptance documents

Acceptance criteria are valuable while they are actionable.

Completed acceptance packets are not necessarily useful as permanent hand-maintained docs.

## Reusable rules

Move reusable acceptance rules/template into:

```text
docs/development/testing-and-acceptance.md
```

Then remove `_template.md`.

## Completed capability acceptance

For documents such as:

```text
scheduled-transactions.md
disputes.md
payout-recovery.md
reconciliation.md
```

when the capability is accepted:

1. make sure current behavior is in `reference/`;
2. make sure tests/traceability prove it;
3. record completion in `roadmap/history.md`;
4. delete the acceptance file.

## Security acceptance

Merge reusable expectations from:

```text
security.md
supply-chain.md
p0-p1-risk-gate.md
```

into security policy/checklist documentation.

`independent-security-review.md` should exist only while that review is actionable.

After the review closes:

- keep final durable findings/decisions;
- delete the temporary scope/gate file.

## `critical-failures.md`

Merge durable failure expectations into:

```text
docs/development/golden-route.md
```

or relevant operations runbooks.

Then delete.

### End State

```text
docs/acceptance/  # removed
```

---

# 10. `docs/evidence/`

### Recommendation: COLLAPSE aggressively

The current directory contains per-track entry gates, final acceptance documents, baselines, cutover evidence, deployment inventory, and implementation-status summaries.

This duplicates plan status and current-state information.

## Keep temporarily

For a track with genuinely unfinished runtime acceptance, keep only the smallest current evidence/checklist needed to finish it.

Examples may include current C2/C3/C5/C6/K0 acceptance gates while they remain open.

## Delete when the track closes

Remove:

```text
*-entry-gate.md
*-final-acceptance.md
```

after:

- acceptance result is represented in current traceability/current-state;
- automated evidence exists;
- a concise history row records completion.

## Delete status duplication

### `plan-63-implementation-status.md`

**DELETE.**

Implementation status belongs in one roadmap/current deployment source, not a separate status document.

## Consolidate runtime result

If a human-readable runtime acceptance summary remains useful, keep exactly one:

```text
docs/operations/runtime-acceptance.md
```

or:

```text
docs/reference/runtime-acceptance.md
```

Do not keep one summary per completed plan forever.

### End State

```text
docs/evidence/  # removed
```

CI/build artifacts are the detailed evidence store.

---

# 11. `docs/engineering/`

### Recommendation: MERGE current guidance and remove temporary tracking

Current folder mixes:

- engineering guidance;
- readiness status;
- risk tracking;
- migration/change evidence;
- decision records;
- capability inventory.

Those have different lifecycle requirements.

## Merge

### `golden-route.md` + `golden-route-failure-matrix.md`

Merge into:

```text
docs/development/golden-route.md
```

One document with sections:

```text
happy path
invariants
failure modes
recovery expectations
test references
```

Then delete both originals.

### `capability-inventory.md`

Merge current capability ownership into:

```text
docs/reference/services.md
```

or `reference/traceability.md`.

Then delete.

### `service-justification.md`

If it contains durable service-boundary decisions:

- convert unique decisions to ADRs;
- delete the working document.

### `p3-decision-register.md`

Each accepted durable decision should become a small ADR.

Delete the register after migration.

---

## Delete once no longer active

### `change-freeze.md`

A temporary change freeze should not become permanent documentation.

If the freeze is over:

**DELETE.**

### `improvement-plan-tracker.md`

This is useful only while the old improvement plan is being executed.

After surviving items move to canonical active plans/issues:

**DELETE.**

### `production-readiness-scorecard.md`

Avoid maintaining a second readiness status if operations already owns production readiness.

Merge any reusable criteria into:

```text
docs/operations/production-readiness-checklist.md
```

Then delete.

### `risk-register.md`

A risk register is highly dynamic.

Prefer:

- GitHub issues/projects;
- active roadmap metadata;
- an external project-management system.

Do not keep a manually aging risk register in docs unless the team actively reviews it as an operational control.

### End State

Likely:

```text
docs/engineering/  # removed
```

with durable content relocated to `reference/`, `development/`, `operations/`, and ADRs.

---

# 12. `docs/deployment/`

### Recommendation: KEEP the domain, MERGE most matrix files

This is one of the biggest opportunities to cut file count without losing information.

The current deployment documentation has many one-file-per-matrix documents while machine-readable deployment contracts already exist under `deploy/inventory`.

## Target

```text
docs/deployment/
├── README.md
├── runtime-contract.md       # generated from deploy/inventory
├── k1-input-contract.md      # only while actionable
└── vendor-sandbox.md         # if it contains reusable operational guidance
```

Generate `runtime-contract.md` with sections for:

```text
services
ports/protocols
public routes
internal calls
messaging
dependencies
configuration
secrets
storage
resources
health/lifecycle
vendor network access
mTLS identities
background jobs
data ownership
feature scope
```

Then remove the individual generated/manual matrix files.

Potential consolidation target includes files such as:

```text
background-job-matrix.md
configuration-matrix.md
data-ownership-matrix.md
dependency-matrix.md
environment-contract.md
feature-scope.md
health-lifecycle-matrix.md
image-runtime-matrix.md
internal-call-matrix.md
messaging-matrix.md
mtls-identity-matrix.md
port-protocol-matrix.md
public-route-matrix.md
resource-baseline.md
secret-key-matrix.md
service-port-matrix.md
service-runtime-inventory.md
storage-volume-matrix.md
vendor-network-matrix.md
```

### `deployment-risk-register.md`

Move active risks into the canonical issue/roadmap system.

Then delete.

### Important Rule

Machine-readable `deploy/inventory` is the source of truth.

Markdown is a generated view.

This removes the need to update many deployment `.md` files manually.

---

# 13. `docs/reference/`

### Recommendation: KEEP the category, reduce micro-documents

Reference is the correct home for current system truth.

But one subsystem should not require five to seven Markdown files unless there is a real navigation benefit.

---

## Notifications

Current notification material is spread across multiple files, plus a separate architecture document.

Recommended target:

```text
docs/reference/notifications.md
```

Sections:

```text
overview
current architecture
event sources
notification kinds
templates
providers
delivery
preferences
privacy
contracts
code locations
```

Merge candidates:

```text
c3-current-notification-inventory.md
c3-event-source-inventory.md
c3-privacy-inventory.md
notification-delivery.md
notification-kinds.md
notification-preferences.md
notification-providers.md
notification-templates.md
notifications.md
architecture/notification-platform.md
```

If the merged document becomes too large, split only into:

```text
notifications.md
notification-contracts.md
```

not nine separate current documents.

---

## Analytics

Recommended target:

```text
docs/reference/analytics.md
```

Merge candidates:

```text
analytics-correlation-matrix.md
analytics-dashboard-catalog.md
analytics-data-contracts.md
analytics-metrics.md
analytics-source-inventory.md
architecture/data-platform.md
```

If contracts are machine-generated, keep them generated rather than hand-maintained.

---

## Services

Review:

```text
current-services.md
services.md
architecture/repository-ownership.md
```

Prefer one canonical:

```text
reference/services.md
```

with:

```text
service
responsibility
entry point
storage
events
dependencies
owner/code location
```

Delete overlapping service inventories.

---

## Completed C1 design docs

For completed C1-specific design/current-contract documents:

- merge still-current contracts into `api-contracts.md`;
- extract durable decisions into ADRs if necessary;
- delete the phase-specific design documents.

A current reference should describe the system, not the historical phase that produced it.

---

## KEEP

High-value documents that should remain distinct include:

```text
architecture.md
api-contracts.md
events.md
glossary.md
services.md
traceability.md
```

`traceability.md` is especially valuable because it can replace a large amount of narrative evidence by linking behavior to code/tests.

---

# 14. `docs/operations/runbooks/`

### Recommendation: KEEP runbooks, consolidate by operational domain

The runbook directory currently has many small alert/symptom-specific files.

Do not delete operational knowledge, but reduce fragmentation.

## Target Rule

Prefer:

> one runbook per operational domain, with anchor sections per alert/symptom.

Example:

```text
runbooks/
├── database.md
├── cache.md
├── messaging.md
├── payouts.md
├── merchants.md
├── analytics.md
├── notifications.md
├── migrations.md
├── backup-restore.md
├── disaster-recovery.md
├── security.md
└── certificates-and-crypto.md
```

Instead of many separate:

```text
analytics-*.md
notification-*.md
merchant-*.md
migration-*.md
```

Alert definitions can link directly to:

```text
docs/operations/runbooks/notifications.md#provider-timeout
```

### Keep separate

A runbook deserves its own file when:

- the failure is high-stakes;
- procedure is long;
- access/safety constraints differ;
- the procedure is independently exercised.

Backup/restore, DR, credential/crypto rotation, and critical database recovery are examples that may justify separate files.

---

# 15. Performance Documentation

### Recommendation: KEEP only current accepted baselines

Performance documentation already treats raw artifacts as disposable.

Apply the same rule to committed Markdown summaries.

Keep:

```text
README.md
capacity-model.md
latest accepted baseline
current investigation report, only if active
```

Delete superseded baseline Markdown once a newer accepted baseline replaces it.

Git history retains the old numbers.

Do not make current readers decide which of several historical baseline reports is authoritative.

---

# 16. Small Categories

## `docs/data/`

**KEEP.**

Generated retention documentation backed by machine-readable config is a good model.

---

## `docs/portfolio/`

**KEEP.**

Small, intentionally curated, and serves a separate audience.

---

## `docs/learn/`

**KEEP for now.**

It serves intentionally different learning paths and is not the main source of stale operational status.

This is low priority for pruning.

---

## `docs/security/`

**KEEP.**

Security policy/threat material has long-lived value.

But:

- merge the duplicate top-level `threat-models/`;
- delete temporary review scope/gate documents after the review closes;
- avoid per-plan security evidence files.

---

# 17. Immediate Deletion/Merge Queue

## Wave 1 — Lowest Risk

These have the clearest cleanup value.

- [ ] Delete `docs/architecture/repository-structure-reorganization-evidence.md` after confirming no unique durable decision remains.
- [ ] Stop committing `docs/architecture/repository-structure-metrics.md`; generate on demand/CI.
- [ ] Merge `docs/architecture/notification-platform.md` into `docs/reference/notifications.md`.
- [ ] Merge `docs/threat-models/data-platform.md` into `docs/security/threat-model.md`.
- [ ] Delete `docs/evidence/plan-63-implementation-status.md`.
- [ ] Merge `docs/engineering/golden-route-failure-matrix.md` into the golden-route document.
- [ ] Merge engineering production readiness criteria into the operational checklist.
- [ ] Remove temporary `change-freeze.md` if the freeze has ended.
- [ ] Triage and delete `docs/improvement/*.md`.

---

## Wave 2 — Historical Plan Cleanup

- [ ] Create `docs/roadmap/history.md`.
- [ ] Extract durable decisions from archived plans into ADRs.
- [ ] Add one completion/supersession row per plan.
- [ ] Delete `docs/roadmap/archive/`.
- [ ] Correct active-plan index generation.
- [ ] Review whether Plan 35 is superseded by the K-series roadmap.

---

## Wave 3 — Acceptance/Evidence Cleanup

For every completed track:

- [ ] update current reference;
- [ ] verify traceability/tests;
- [ ] add history row;
- [ ] delete entry-gate document;
- [ ] delete final-acceptance document;
- [ ] delete implementation-status document.

For incomplete acceptance:

- [ ] retain only open gates;
- [ ] remove completed implementation narrative.

Then eliminate top-level:

```text
docs/acceptance/
docs/evidence/
```

---

## Wave 4 — Current Reference Consolidation

- [ ] Merge notification documents.
- [ ] Merge analytics documents.
- [ ] Merge service ownership/service inventory documents.
- [ ] Merge completed phase-specific design docs into domain reference.
- [ ] Move ADRs into `reference/decisions/`.
- [ ] Delete `docs/architecture/`.

---

## Wave 5 — Deployment Generation

- [ ] Build one generated `deployment/runtime-contract.md`.
- [ ] Generate it from `deploy/inventory`.
- [ ] Remove individual matrix Markdown.
- [ ] Add CI freshness check.

---

## Wave 6 — Runbook Consolidation

- [ ] Merge analytics runbooks.
- [ ] Merge notification runbooks.
- [ ] Merge merchant runbooks.
- [ ] Merge migration runbooks.
- [ ] keep high-risk independent procedures separate.
- [ ] update alert links to document anchors.

---

# 18. New `docs-check` Rules

Pruning will regress unless the repository prevents document sprawl.

Add these checks.

## Orphan documents

Every hand-maintained Markdown document must be reachable from:

- `docs/README.md`;
- a category README;
- an active roadmap;
- or an explicit allowlist.

Otherwise fail:

```text
docs-check: orphan document
docs/foo/bar.md
```

---

## No completed plan in active docs

```text
status=done
=> file cannot live under roadmap/active/
```

---

## No permanent roadmap archive

Disallow new files under:

```text
docs/roadmap/archive/
```

---

## One canonical status source

Disallow multiple hand-maintained documents that independently claim plan status.

Generate summary tables where possible.

---

## Generated inventory freshness

If a document is based on machine-readable config:

```text
source config -> generator -> markdown view
```

CI should regenerate and require:

```bash
git diff --exit-code
```

---

## Optional document budget

Introduce a simple code-review question:

> Does this need a new `.md`, or can it become a section in an existing canonical document?

New Markdown should require a reason for being independently maintained.

---

# 19. Definition of Done

The cleanup is successful when:

- [ ] `docs/improvement/` no longer exists;
- [ ] `docs/roadmap/archive/` no longer exists;
- [ ] `docs/architecture/` no longer exists as a competing current namespace;
- [ ] `docs/threat-models/` no longer exists;
- [ ] completed acceptance/evidence packets are removed;
- [ ] deployment matrices are generated into a consolidated view;
- [ ] one subsystem normally has one current reference document;
- [ ] one operational domain normally has one runbook;
- [ ] active plans contain only unfinished work;
- [ ] completed plans are represented by concise history rows;
- [ ] durable decisions are represented by ADRs;
- [ ] current behavior is represented by reference docs and executable tests;
- [ ] detailed historical implementation is retrieved through Git history, not browsed in `docs/`.

---

# 20. Desired Mental Model

After pruning:

```text
I need to understand the system
    -> reference/

I need to change the system
    -> development/

I need to operate the system
    -> operations/ + deployment/

I need security guidance
    -> security/

I need to know what is currently being built
    -> roadmap/active/

I need to know what was completed
    -> roadmap/history.md

I need to know why an architectural choice exists
    -> reference/decisions/

I need old implementation details
    -> Git history
```

If a contributor needs to search through dozens of completed plans to understand the system today, the documentation model has failed.

The desired end state is **less documentation, stronger canonical ownership, and more truth derived from code/config/tests instead of manually synchronized Markdown**.
