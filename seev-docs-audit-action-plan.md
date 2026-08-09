# Seev `docs/` Audit and Action Plan

> Repository: `herdifirdausss/seev`
>
> Audit scope: `docs/` on the public `main` branch, reviewed 2026-08-09.
>
> Goal: make the documentation easier to trust, navigate, maintain, and verify without adding unnecessary documentation volume.
>
> This is a documentation-architecture audit, not a production-readiness assessment of the application itself.

---

## Executive Summary

Seev already has a strong documentation foundation:

- a clear documentation home;
- explicit `Current`, `Target`, and `Historical` concepts;
- separate reader paths for learning, engineering, operations, security, performance, and roadmap material;
- a concept-to-code traceability map;
- current-state documentation that distinguishes implementation from runtime acceptance;
- generated data-retention documentation;
- evidence-oriented operational and performance documentation;
- a documentation style guide and `make docs-check`.

The main problem is **not missing documentation**.

The main problem is that the documentation has grown faster than the navigation and lifecycle model around it.

The highest-value improvements are:

1. repair roadmap/index correctness immediately;
2. make every top-level documentation directory intentional and discoverable;
3. eliminate ambiguity between `reference/`, `architecture/`, `security/`, `threat-models/`, `roadmap/`, and `improvement/`;
4. convert document lifecycle/status into machine-checkable metadata;
5. strengthen `docs-check` so navigation drift, orphan documents, stale plan locations, and broken links fail CI;
6. turn `evidence/` and the large historical plans into lookup material rather than additional onboarding burden.

---

# 1. Findings

## P0 — Documentation Source-of-Truth Drift

### Finding

`docs/roadmap/README.md` declares itself the current index that separates active plans from archived history.

However, the current index contains contradictory state:

- plans 60/C4, 61/C5, and 62/C6 are shown inside **Active plans** while their status says they are archived;
- the index says the same list exists in `docs/roadmap/active/`;
- `docs/roadmap/active/` currently lists only plans 35, 56, and 65;
- the Plan 64/K0 link from the roadmap index currently resolves to a missing path.

This is the most important documentation issue because the roadmap is explicitly treated as the authoritative plan-status index.

### Risk

A contributor can:

- start work from an archived plan;
- miss genuinely active work;
- follow a dead link for K0;
- infer the wrong implementation priority;
- distrust the status model used throughout the rest of the documentation.

### Action

Fix this before doing broader documentation reorganization.

### Acceptance Criteria

- [ ] Every plan listed under `## Active plans` physically exists under `docs/roadmap/active/`.
- [ ] No archived plan is listed as active.
- [ ] Every active-plan link resolves.
- [ ] Plan 64/K0 either exists under the active directory or the roadmap is updated to its real canonical location.
- [ ] `docs/roadmap/README.md` and `docs/roadmap/active/README.md` contain the same active plan set.
- [ ] CI fails when these two indexes diverge.

---

## P0 — Broken-Link Detection Is Not Strong Enough

### Finding

The current documentation claims `make docs-check` passes, while at least one current roadmap link is externally observable as missing.

Regardless of whether this was caused by a recent move, stale index, or incomplete checker coverage, the documentation gate should make this class of error difficult to merge.

### Action

Extend the documentation checker to validate repository-relative links and plan-location invariants.

### Required Checks

`make docs-check` should fail on:

- [ ] missing relative `.md` targets;
- [ ] missing relative asset targets;
- [ ] missing explicitly referenced anchors where practical;
- [ ] links from current indexes to deleted files;
- [ ] active-plan entries whose file is not in `roadmap/active/`;
- [ ] archive entries whose file is not in `roadmap/archive/`;
- [ ] top-level documentation directories missing from the documentation home;
- [ ] top-level directories that require an index but do not have one.

### Suggested Output

```text
docs-check: broken link
source: docs/roadmap/README.md
target: docs/roadmap/active/64-k0-deployment-inventory-baseline.md
reason: target does not exist
```

Avoid silently skipping unresolved links.

---

# 2. Documentation Topology

## P1 — Root Library Map Does Not Match the Actual Tree

### Finding

`docs/README.md` says:

> Each directory has one purpose and its own index.

The actual top-level tree also contains directories such as:

- `architecture/`
- `deployment/`
- `evidence/`
- `improvement/`
- `threat-models/`

that are not all represented in the main library map.

Some also do not currently have their own `README.md` index.

### Action

Choose one of the following rules and enforce it consistently.

### Recommended Rule

Every first-level directory under `docs/` must satisfy **one** of these conditions:

1. it is a documented public documentation category and appears in `docs/README.md`; or
2. it is an implementation/generated asset directory explicitly marked as such; or
3. it is removed/merged into another canonical category.

No unexplained first-level directory should exist.

### Acceptance Criteria

- [ ] `docs/README.md` covers every intended first-level documentation category.
- [ ] Every intended category has a `README.md`.
- [ ] Every directory README states:
  - purpose;
  - allowed content;
  - content that belongs elsewhere;
  - lifecycle/status expectation;
  - primary entry points.

---

## P1 — Architecture Documentation Has Two Homes

### Finding

Current architecture material exists in both:

```text
docs/reference/architecture.md
docs/architecture/data-platform.md
docs/architecture/notification-platform.md
```

The root documentation model says current system architecture belongs to `reference/`, but specialized architecture documents live in a separate `architecture/` directory.

This makes the canonical location unclear.

### Recommended End State

Prefer one current-architecture namespace:

```text
docs/reference/
  architecture.md
  architecture/
    README.md
    data-platform.md
    notification-platform.md
```

Alternative: keep `docs/architecture/`, but then make it a first-class documented category and make `docs/reference/architecture.md` an index/overview rather than a competing canonical architecture document.

### Migration Steps

- [ ] Decide which directory owns **current architecture**.
- [ ] Add an explicit canonical statement to the chosen index.
- [ ] Move specialized architecture files if necessary.
- [ ] Update backlinks.
- [ ] Leave temporary compatibility stubs if many internal links currently reference the old path.
- [ ] Add a docs-check rule preventing two documents from declaring the same canonical responsibility.

### Acceptance Criteria

A new contributor should answer:

> “Where does current architecture live?”

with exactly one path.

---

## P1 — Threat Models Have Two Homes

### Finding

Security/threat-model material currently exists in:

```text
docs/security/threat-model.md
docs/threat-models/data-platform.md
```

The security index already describes threat modeling as part of the security documentation domain, so `docs/threat-models/` behaves like a second namespace.

### Recommended End State

```text
docs/security/
  README.md
  threat-model.md
  threat-models/
    README.md
    data-platform.md
```

or:

```text
docs/security/
  threat-model.md
  data-platform-threat-model.md
```

### Acceptance Criteria

- [ ] `docs/security/README.md` indexes every current threat model.
- [ ] `docs/threat-models/` no longer exists as an unexplained top-level category.
- [ ] Each threat model declares its scope and relationship to the system-level threat model.

---

# 3. Improvement Plans vs Canonical Roadmap

## P1 — `docs/improvement/` Competes With `docs/roadmap/`

### Finding

`docs/improvement/` contains several large cross-functional review plans, including engineering, architecture, DevOps, DBA, security, QA/compliance, product, and business plans.

Some of these documents contain substantial executable roadmaps and proposed paths, while the repository already defines `docs/roadmap/` as the authoritative home for active execution plans and historical plan decisions.

This creates two planning systems:

```text
docs/improvement/
docs/roadmap/
```

Some improvement documents also propose paths that do not match the current documentation taxonomy, for example `docs/engineering/...`.

### Risk

The same finding can exist in multiple plans with different status, terminology, or implementation assumptions.

A future contributor cannot easily distinguish:

- a still-valid finding;
- an implemented recommendation;
- an obsolete assessment;
- an active roadmap item.

### Recommended Model

Treat improvement audits as **review snapshots**, not active roadmaps.

Recommended structure:

```text
docs/reviews/
  README.md
  archive/
    2026-08-engineering-review.md
    2026-08-architecture-review.md
    2026-08-devops-review.md
    ...
```

Each review should point to canonical roadmap issues/plans rather than maintaining a second active checklist.

If avoiding another top-level directory is preferred, use:

```text
docs/roadmap/archive/reviews/
```

### Triage Procedure

For every file currently under `docs/improvement/`:

1. identify each still-valid actionable finding;
2. map it to:
   - an existing active plan;
   - a new roadmap item;
   - a GitHub issue;
   - or `No action / superseded`;
3. add a disposition table near the top of the review;
4. mark the review `Historical` after triage;
5. move it out of the first-class active documentation path.

### Suggested Disposition Table

```markdown
| Finding | Disposition | Canonical tracker |
|---|---|---|
| Unified money command pipeline | Active | Plan XX / issue #... |
| Production-shaped staging | Covered | Plan 63 / K-series |
| Capability inventory | Superseded | docs/reference/current-state.md |
| Risk register | Planned | issue #... |
```

### Acceptance Criteria

- [ ] There is exactly one active planning system.
- [ ] Review/audit documents are immutable historical inputs after triage.
- [ ] No historical audit is presented as current execution truth.
- [ ] Every surviving action has one canonical tracker.

---

# 4. Machine-Readable Document Lifecycle

## P1 — Status Labels Are Strong but Mostly Human-Enforced

### Finding

The documentation style guide has good lifecycle semantics:

- `Current`
- `Target`
- `Historical`

Some important documents also contain reviewed dates and audience declarations.

However, these fields are not consistently structured across the entire tree.

### Action

Introduce lightweight document metadata.

YAML front matter is the simplest option:

```yaml
---
status: current
audience:
  - contributor
  - reviewer
owner: architecture
last_reviewed: 2026-08-09
canonical_for: current-system-architecture
supersedes: null
generated: false
---
```

If front matter is undesirable, use a machine-readable HTML comment block instead.

### Minimum Metadata

For maintained non-archive documents:

- `status`
- `audience`
- `owner`
- `last_reviewed`
- `canonical_for` when the document is a source of truth
- `generated`
- `plan_id` where relevant

### Acceptance Criteria

- [ ] `docs-check` can parse lifecycle metadata.
- [ ] Every `Current` document has `last_reviewed`.
- [ ] Every active plan has a stable `plan_id`.
- [ ] Every generated document declares its generator/source.
- [ ] No two current documents declare the same `canonical_for` value.

---

# 5. Generated Documentation Catalog

## P1 — Avoid Manually Maintaining a “Complete Document List”

### Finding

`docs/README.md` contains a section called `Complete document list`, but the repository contains substantially more documents than the curated list, especially under reference, deployment, evidence, performance, and roadmap areas.

The current list is useful as a curated route, but the word “Complete” creates a maintenance promise that is difficult to keep manually.

### Action

Choose one:

### Option A — Preferred

Rename it to:

```text
Core document index
```

and keep it intentionally curated.

Then generate a separate:

```text
docs/catalog.md
```

from the filesystem + metadata.

### Option B

Generate the entire `Complete document list` automatically.

### Generated Catalog Fields

```text
Path
Title
Status
Audience
Owner
Last reviewed
Canonical responsibility
Generated?
Plan/evidence relationship
```

### Acceptance Criteria

- [ ] Human-maintained navigation remains concise.
- [ ] Full inventory is generated.
- [ ] CI fails if the generated catalog is stale.

---

# 6. Evidence Discoverability

## P2 — `docs/evidence/` Needs an Index and Retention Rules

### Finding

`docs/evidence/` contains many implementation- and acceptance-specific files such as:

```text
c1-entry-gate.md
c1-final-acceptance.md
c2-entry-gate.md
c2-final-acceptance.md
c3-final-acceptance.md
c4-final-acceptance.md
c5-final-acceptance.md
c6-final-acceptance.md
k0-deployment-inventory.md
runtime-acceptance.md
```

but the directory itself does not currently provide the same reader-oriented index pattern used by `reference/`, `development/`, `security/`, and `learn/`.

### Action

Add:

```text
docs/evidence/README.md
```

### The Index Should Explain

- what qualifies as repository evidence;
- which evidence is current vs historical;
- whether evidence is immutable after acceptance;
- how evidence maps to roadmap plan IDs;
- what belongs in Git vs ignored runtime artifacts;
- where raw performance/chaos/log artifacts live;
- how hashes or run IDs connect committed summaries to disposable evidence.

### Suggested Table

```markdown
| Track | Entry gate | Final acceptance | Current status | Canonical plan |
|---|---|---|---|---|
| C2 | ... | ... | Core done | Plan 58 |
| C3 | ... | ... | Implemented, acceptance pending | Plan 59 |
| K0 | ... | ... | In progress | Plan 64 |
```

### Acceptance Criteria

A reviewer should be able to go from:

```text
plan -> evidence -> current-state claim
```

and back without searching filenames manually.

---

# 7. Current-State Freshness

## P2 — Reduce Manual Duplication of Status Facts

### Finding

Plan status appears in multiple places:

- `docs/reference/current-state.md`
- `docs/roadmap/README.md`
- `docs/roadmap/active/README.md`
- individual plans;
- evidence files;
- sometimes improvement/review documents.

Duplication is acceptable for reader convenience, but only if one copy is authoritative and the others are derived or automatically checked.

### Action

Define ownership:

```text
Plan lifecycle status      -> roadmap metadata
Implemented feature state  -> current-state
Acceptance evidence        -> evidence
Runtime procedures         -> operations
Architecture truth         -> reference
```

Then generate or validate summary tables across those boundaries.

### Suggested Invariant

For an archived plan:

```text
roadmap.status in {done, core_done, superseded}
=> file must not exist under roadmap/active/
```

For a `Current` feature claim:

```text
current-state evidence reference must exist
```

### Acceptance Criteria

- [ ] Status changes happen in one canonical place.
- [ ] Secondary indexes are generated or checked.
- [ ] Moving a plan from active to archive cannot leave stale active links.

---

# 8. Improve Active-Plan Readability

## P2 — Keep Deep Plans, Add a Fast Operational Header

Large plans are not inherently bad, especially as historical engineering records. The problem is making contributors scan thousands of lines to answer:

- Is this active?
- What is open?
- What is next?
- What is blocked?
- What evidence is missing?

### Action

Require every active plan to start with a compact execution header.

### Template

```markdown
# Plan XX — Title

> Status: In progress
> Owner: ...
> Last reviewed: YYYY-MM-DD
> Depends on: ...
> Current milestone: ...
> Next action: ...
> Blocking issue: none / ...
> Runtime acceptance: partial / pending / complete

## Open Work

- [ ] ...
- [ ] ...

## Exit Criteria

- [ ] ...

## Implementation Record

...
```

Do not rewrite long archived plans solely to shorten them.

### Acceptance Criteria

A contributor should identify the next executable action within 60 seconds.

---

# 9. File Naming and Language Consistency

## P2 — Normalize Naming Conventions

Current filenames mix styles such as:

```text
seev-...-plan-en.md
seev-...-improvement-plan.md
42-long-term-roadmap.md
55-frontend-long-term-roadmap.md
c2-final-acceptance.md
```

The content is already primarily English, so `-en` adds little value unless multilingual versions are planned.

### Recommended Convention

For plans:

```text
NN-track-short-title.md
```

For evidence:

```text
<track>-<gate-or-evidence>.md
```

For reviews:

```text
YYYY-MM-<scope>-review.md
```

For references:

```text
<domain>-<topic>.md
```

### Action

Do not mass-rename archived files without reason.

Apply the convention to:

- new files;
- active files when touched;
- files moved during the topology cleanup.

---

# 10. Documentation Automation

## P1 — Upgrade `docs-check` Into a Documentation Architecture Gate

Recommended checks:

```text
1. Markdown relative-link validation
2. Asset-link validation
3. Required metadata validation
4. Current-document review-date validation
5. Top-level directory/index coverage
6. Orphan document detection
7. Active/archive roadmap location validation
8. Duplicate plan ID detection
9. Duplicate canonical responsibility detection
10. Generated-file freshness validation
11. Generated catalog freshness validation
12. Optional anchor validation
13. Optional terminology rules from documentation-style.md
```

### Suggested Commands

```bash
make docs-check
make docs-catalog
make docs-fix-indexes   # optional generator, not an implicit mutator in CI
```

### CI Rule

CI should run:

```bash
make docs-check
git diff --exit-code
```

after generators where applicable.

Generated documentation should never silently change during CI.

---

# 11. Proposed Target Structure

This keeps the current philosophy but removes ambiguous namespaces.

```text
docs/
├── README.md
├── index.html
├── seev-story.svg
│
├── learn/
│   └── README.md
│
├── reference/
│   ├── README.md
│   ├── current-state.md
│   ├── architecture.md
│   ├── architecture/
│   │   ├── README.md
│   │   ├── data-platform.md
│   │   └── notification-platform.md
│   ├── ...
│   └── traceability.md
│
├── development/
│   └── README.md
│
├── deployment/
│   └── README.md
│
├── operations/
│   └── README.md
│
├── performance/
│   └── README.md
│
├── security/
│   ├── README.md
│   ├── threat-model.md
│   └── threat-models/
│       ├── README.md
│       └── data-platform.md
│
├── data/
│   └── README.md
│
├── evidence/
│   └── README.md
│
├── portfolio/
│   └── README.md
│
├── roadmap/
│   ├── README.md
│   ├── active/
│   │   └── README.md
│   └── archive/
│       └── README.md
│
└── reviews/                 # optional; historical audit inputs only
    ├── README.md
    └── archive/
```

### Directories to Remove as Ambiguous Top-Level Categories

After migration:

```text
docs/architecture/
docs/threat-models/
docs/improvement/
```

Their content is not deleted; it is moved into the canonical domain.

---

# 12. Execution Plan

## Phase 0 — Repair Trust-Critical Navigation

**Priority:** P0
**Recommended scope:** one small PR

### Tasks

- [ ] Repair the Plan 64/K0 canonical path or remove the stale link.
- [ ] Remove C4/C5/C6 from the Active Plans table.
- [ ] Ensure all genuinely active plans are under `roadmap/active/`.
- [ ] Synchronize `roadmap/README.md` with `roadmap/active/README.md`.
- [ ] Add broken relative-link checking to `docs-check`.

### Definition of Done

```text
roadmap index == active directory
all current roadmap links resolve
docs-check fails on a deliberately broken relative link
```

---

## Phase 1 — Make the Documentation Tree Intentional

**Priority:** P1

### Tasks

- [ ] Inventory every first-level `docs/` directory.
- [ ] Assign one purpose and lifecycle owner to each.
- [ ] Add missing README indexes.
- [ ] Add every retained category to the root library map.
- [ ] Consolidate current architecture into one namespace.
- [ ] Consolidate threat models under security.
- [ ] Decide final home for review/improvement snapshots.

### Definition of Done

Every top-level directory is both:

1. discoverable from `docs/README.md`; and
2. unambiguous about what belongs there.

---

## Phase 2 — Triage Legacy Improvement Plans

**Priority:** P1

### Tasks

For every `docs/improvement/*.md`:

- [ ] add assessment date;
- [ ] add lifecycle status;
- [ ] extract still-open findings;
- [ ] map each finding to one canonical tracker;
- [ ] mark implemented/superseded findings;
- [ ] move the audit to a historical review location;
- [ ] remove instructions that create obsolete documentation paths.

### Definition of Done

No file outside `docs/roadmap/active/` acts as a competing active roadmap.

---

## Phase 3 — Add Structured Metadata

**Priority:** P1

### Tasks

- [ ] define metadata schema;
- [ ] apply it to root indexes and all `Current` documents first;
- [ ] apply it to active plans;
- [ ] apply it to generated documents;
- [ ] extend `docs-check`.

### Definition of Done

The repository can programmatically answer:

```text
Which docs are current?
Which docs are active plans?
Which docs are generated?
Which docs have not been reviewed recently?
Which document is canonical for a topic?
```

---

## Phase 4 — Generate Catalogs and Cross-Checks

**Priority:** P2

### Tasks

- [ ] rename manually curated `Complete document list` to `Core document index`;
- [ ] generate `docs/catalog.md`;
- [ ] generate/check active plan summary;
- [ ] add `docs/evidence/README.md`;
- [ ] validate plan -> evidence -> current-state links.

### Definition of Done

Adding or moving a document cannot silently make it undiscoverable.

---

## Phase 5 — Improve Maintainer UX

**Priority:** P2

### Tasks

- [ ] add compact execution headers to active plans;
- [ ] normalize naming for new/touched files;
- [ ] document generated/static asset ownership;
- [ ] optionally separate documentation-site assets from prose if `index.html` continues to grow.

---

# 13. Recommended PR Sequence

Avoid one large documentation rewrite.

## PR 1 — Roadmap correctness

```text
fix roadmap active/archive state
repair Plan 64 link/path
add relative-link validation
```

## PR 2 — Documentation topology contract

```text
update docs/README.md library map
add missing directory indexes
document category ownership
```

## PR 3 — Canonical architecture/security locations

```text
consolidate architecture docs
consolidate threat models
preserve compatibility links
```

## PR 4 — Improvement-plan triage

```text
convert improvement plans into historical review inputs
map surviving findings to roadmap/issues
```

## PR 5 — Metadata and generated catalog

```text
add lifecycle metadata
generate docs/catalog.md
enforce metadata and orphan checks
```

## PR 6 — Evidence navigation

```text
add evidence index
validate plan/evidence/current-state relationships
```

---

# 14. Priority Matrix

| Priority | Improvement | Why |
|---|---|---|
| P0 | Fix roadmap active/archive mismatch | Roadmap is the declared source of truth |
| P0 | Fix Plan 64/K0 broken link | Blocks an explicitly active deployment track |
| P0 | Add broken-link checking | Prevents recurrence |
| P1 | Align root map with actual tree | Removes navigation ambiguity |
| P1 | Consolidate architecture namespace | One canonical current architecture |
| P1 | Consolidate threat-model namespace | One canonical security hierarchy |
| P1 | Retire `improvement/` as an active planning system | Prevents competing roadmaps |
| P1 | Add structured lifecycle metadata | Makes truth machine-verifiable |
| P1 | Strengthen `docs-check` | Converts conventions into guarantees |
| P2 | Add evidence index | Improves auditability |
| P2 | Generate full catalog | Prevents orphan docs |
| P2 | Add fast headers to active plans | Improves execution speed |
| P2 | Normalize file naming over time | Reduces cognitive overhead |
| P3 | Reorganize static site assets | Nice cleanup, not correctness-critical |

---

# 15. What Not to Do

Do **not**:

- rewrite every archived plan;
- merge all documentation into one giant README;
- delete historical reasoning merely because it is outdated;
- make every document part of onboarding;
- create another parallel planning folder;
- add more status terminology without removing ambiguity;
- rely only on human review for link and lifecycle consistency;
- mass-rename hundreds of historical references without a migration benefit.

The current documentation is already rich. The objective is to make it **more trustworthy and cheaper to maintain**, not larger.

---

# 16. Final Target

The documentation system should have these invariants:

```text
one documentation home
one current-system reference hierarchy
one security hierarchy
one active roadmap
one archive
one evidence model
one machine-readable lifecycle model
one CI gate enforcing all of the above
```

A contributor should be able to determine within a few minutes:

1. what Seev currently implements;
2. what is actually active work;
3. what is historical;
4. where the source of truth for a topic lives;
5. which code/test/evidence proves a claim;
6. whether a document is stale;
7. what the next executable task is.

That is the most valuable next stage for the current `docs/` tree.
