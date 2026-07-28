# 55 — Frontend Long-Term Roadmap

> [Documentation home](../README.md) · [Roadmap](README.md)
>
> Created: 2026-07-28. Last reviewed against the live repository: 2026-07-28.
>
> **Status: reference roadmap; do not execute this document directly.**
> A phase becomes executable only after its activation trigger and dependencies
> are satisfied and a separate numbered execution plan is created under
> `docs/roadmap/active/`.

This roadmap defines how Seev can evolve from a backend-first financial systems
reference into a browser-based learning and operations experience without
weakening the service, security, accounting, or evidence boundaries that already
exist.

The roadmap intentionally separates two levels of planning:

1. **This document defines direction.** It explains the phases, sequencing,
   outcomes, dependencies, triggers, anti-scope, and exit gates.
2. **Execution documents define implementation.** Each activated phase receives
   its own numbered plan containing locked decisions, exact files, tasks,
   migrations, tests, rollout, rollback, Definition of Done, and result evidence.

The first execution document activated from this roadmap is
[Plan 56 — F0 Frontend Platform Foundation](active/56-f0-frontend-platform-foundation.md).
Later phase numbers are assigned only when those phases are activated, so this
roadmap does not reserve a speculative sequence of repository plan numbers.

---

## 1. Product direction

Seev should not become only a visual clone of a consumer wallet. Its strongest
frontend direction is:

> An interactive financial systems platform that lets users perform money
> journeys, inspect accounting evidence, investigate operational state, observe
> failures, and understand how the system preserves invariants.

The long-term browser experience has four primary surfaces.

### 1.1 Wallet

A public-facing experience for:

- registration and authentication;
- profile and KYC state;
- wallet accounts and balances;
- fee quotation;
- top-up;
- peer-to-peer transfer;
- payout or withdrawal;
- schedules and notifications;
- transaction history and evidence.

The Wallet demonstrates the product journey without hiding the financial and
distributed-system behavior behind a single generic status.

### 1.2 Operations

An internal-facing experience for:

- operational summaries;
- payout investigation;
- payin event investigation;
- reconciliation;
- assurance findings;
- outbox and vendor-command recovery;
- fee, routing, policy, and fraud configuration;
- maker/checker actions;
- audit evidence.

Operations must use Admin BFF as its browser boundary. It must not create a
second authorization path or call service databases and internal service
listeners directly.

### 1.3 Failure Lab

A local-only learning environment for controlled scenarios such as:

- duplicate requests;
- duplicate callbacks;
- expired quotes;
- dependency outages;
- vendor timeouts;
- process restart after durable state changes;
- outbox delay;
- consumer redelivery;
- reconciliation mismatch;
- assurance findings;
- emergency intake pause and maker/checker resume.

The Failure Lab visualizes backend-owned evidence. The browser never decides
whether an invariant passed and never receives arbitrary command execution.

### 1.4 Learning and observability experience

A cross-cutting experience that connects:

- product state;
- workflow state;
- ledger entries;
- event delivery;
- reconciliation state;
- request and trace identifiers;
- dashboards, traces, logs, and runbooks;
- plain-language explanations of the engineering behavior.

This surface should help a new visitor understand why the architecture exists,
not merely show that the services are running.

---

## 2. Relationship to the repository roadmap

[Plan 42](42-long-term-roadmap.md) remains the repository-wide post-MVP roadmap.
This document is a focused sub-roadmap for browser product surfaces.

Frontend phases depend on existing or future backend tracks rather than
replacing them.

| Backend track or capability | Frontend relationship |
|---|---|
| A1 Observability | Supplies dashboards, traces, logs, and correlation targets for F6. |
| A5 Admin console | Existing htmx/Pico console remains the operational baseline during F3 and F4. |
| A6 Internal security | Required before browser exposure expands toward merchant or external operator models. |
| A8 Data lifecycle and privacy | Defines data minimization, retention, export, and masking constraints. |
| A9 API contracts | Required for stable live clients and compatibility gates from F1 onward. |
| A10 Product assurance | Supplies findings and intake controls for F3 and F4. |
| B0 Load and capacity | Provides evidence for browser polling, pagination, and operational query limits. |
| C1 Merchant/B2B API | Activates the merchant portal portion of F8. |
| C2 Data platform | Activates analytics and revenue dashboards in F8. |
| C3 Multi-channel notifications | Activates notification preferences and delivery diagnostics in F8. |
| C4 Multi-currency | Activates multi-currency wallet and FX presentation in F8. |
| C5 Advanced financial products | Activates savings, accrual, and richer schedule experiences in F8. |

Frontend work must not invent missing backend semantics. When a required read
model or action contract does not exist, the execution plan must either:

1. add a narrowly owned endpoint through the correct backend boundary;
2. explicitly defer the UI behavior; or
3. use labelled fixtures only for platform and component development.

---

## 3. How to use this roadmap

When a phase trigger is satisfied:

1. Record the evidence or conscious learning decision.
2. Re-inventory the live repository and relevant contracts.
3. Assign the next available repository plan number.
4. Create a self-contained execution document under `docs/roadmap/active/`.
5. Copy the phase anti-scope and dependency gates into that execution document.
6. Lock technical and product decisions before implementation tasks begin.
7. Define exact tests, rollout, rollback, acceptance evidence, and full repository
   verification.
8. Add the plan to both roadmap indexes.
9. Implement only the activated scope.
10. Move the completed plan to the archive after all acceptance evidence passes.
11. Update this roadmap's phase status and newly discovered dependencies.

A phase must not be activated merely because it appears next in the document.
Its trigger and prerequisites must be true.

---

## 4. Global product and engineering rules

These rules apply to every frontend execution plan.

### G1 — Preserve service ownership

- Wallet may use only Auth and Gateway-facing browser contracts.
- Operations may use only Admin BFF-facing browser contracts.
- Browsers must not call Ledger, Payin, Payout, Fraud, or Assurance internal
  listeners directly.
- Browsers must not access service databases.

### G2 — Preserve the existing operator console during migration

The current server-rendered Admin BFF console remains supported until the modern
Operations application has proven feature parity, authorization parity, audit
parity, failure handling, and rollback behavior for a migrated capability.

Migration is domain-by-domain, not a big-bang replacement.

### G3 — Exact money only

- Monetary values cross transport boundaries as exact decimal strings or
  documented minor-unit strings or integers.
- Frontend domain calculations use `bigint` minor units or another reviewed exact
  representation.
- Binary floating-point must not be used for authoritative monetary arithmetic.
- The frontend never recalculates authoritative fees, balances, or settlement
  amounts from display values.

### G4 — Show separate sources of truth

The UI must not flatten distinct facts into one ambiguous `SUCCESS` label.
Where available, show independent state such as:

- workflow state;
- ledger posting state;
- held-funds state;
- external delivery state;
- notification state;
- reconciliation state;
- assurance state.

### G5 — Backend authorization remains authoritative

Frontend role and permission checks improve navigation and usability only.
Every read and mutation remains authorized by the owning backend boundary.

### G6 — No optimistic final money state

The UI may optimistically update harmless presentation state, but it must not
optimistically declare:

- a new authoritative balance;
- final transfer success;
- final payout success or failure;
- ledger posting completion;
- reconciliation resolution;
- maker/checker approval.

### G7 — Safe browser sessions

- Do not place access tokens, refresh tokens, operator session material, or CSRF
  secrets in URLs or readable persistent browser storage.
- Production-like web sessions should use reviewed HttpOnly cookie and server
  mediation patterns.
- Wallet and Operations should use separate application and session boundaries.

### G8 — Runtime validation and domain mapping

Transport payloads are validated before use. Generated or handwritten transport
DTOs are mapped into stable frontend domain objects rather than passed directly
through page components.

### G9 — Every state is designed

Every user-facing data surface must cover:

- loading;
- empty;
- success;
- validation failure;
- authentication failure;
- authorization failure;
- dependency failure;
- retryable failure;
- non-retryable failure;
- stale or uncertain state when applicable.

### G10 — Accessibility is part of completion

Critical journeys must work with keyboard navigation, visible focus, semantic
labels, screen readers, reduced motion, and status communication that does not
rely on color alone.

### G11 — Mocked behavior stays visibly mocked

Fixture-backed development is allowed before canonical live contracts are
available. The UI and documentation must clearly distinguish fixture behavior
from executable backend behavior.

### G12 — Documentation states current truth

README, architecture, product tour, screenshots, and demos must not imply that a
phase exists before its own acceptance evidence passes.

---

## 5. Target frontend topology

The target repository shape is directional and may be refined by the activated
foundation plan.

```text
seev/
├── api/
├── cmd/
├── internal/
├── docs/
├── migrations/
├── scripts/
├── web/
│   ├── apps/
│   │   ├── wallet/
│   │   └── operator/
│   ├── packages/
│   │   ├── ui/
│   │   ├── domain/
│   │   ├── api-client/
│   │   ├── money/
│   │   ├── testing/
│   │   └── config/
│   ├── package.json
│   ├── pnpm-workspace.yaml
│   └── tsconfig.base.json
└── docker-compose.yml
```

Target browser boundaries:

```text
Public browser
    └── Wallet application
            ├── Auth browser contract
            └── Gateway browser contract

Operator browser
    └── Operations application
            └── Admin BFF browser contract
                    ├── Auth admin contract
                    ├── Ledger admin contract
                    ├── Payin admin contract
                    ├── Payout admin contract
                    ├── Fraud admin contract
                    ├── Assurance admin contract
                    └── Gateway admin contract
```

The framework, workspace tooling, and detailed session topology are locked in F0,
not by this reference document.

---

## 6. Phase map

| Phase | Name | Primary outcome | Activation trigger | Current status |
|---|---|---|---|---|
| F0 | Product and platform foundation | Typed, secure, testable frontend workspace and product rules | Conscious decision to build a browser experience | Planned via [56](active/56-f0-frontend-platform-foundation.md) |
| F1 | Wallet read model and transaction explorer | Users can inspect accounts, balances, transactions, and accounting evidence | F0 complete and required live read contracts stable | Future |
| F2 | Interactive wallet journeys | Users can perform fee quote, transfer, top-up, and payout journeys safely | F1 complete and mutation/idempotency contracts stable | Future |
| F3 | Operations read-only investigation | Operators can investigate payout, payin, assurance, reconciliation, and delivery state | F0 complete and Admin BFF read contracts are sufficient | Future |
| F4 | Controlled operations and governance | Selected operator mutations migrate with maker/checker, audit, and rollback parity | F3 complete and one domain proves migration value | Future |
| F5 | Failure Lab | Local users can run allowlisted failure scenarios and inspect backend-owned evidence | F1/F2 or F3 provide stable evidence views and chaos harness is callable safely | Future |
| F6 | Observability and learning integration | Transactions connect to traces, logs, dashboards, runbooks, and explanations | Stable correlation identifiers and observability links exist | Future |
| F7 | Hardening, packaging, and public demonstration | Reproducible local demo and restricted hosted learning environment | Core Wallet, Operations, and selected Lab journeys are stable | Future |
| F8 | Trigger-based product expansion | Merchant, analytics, notification, multi-currency, and advanced-product surfaces | Corresponding backend C-track activation | Future |

---

## 7. Dependency and sequencing map

```text
                           +----------------------+
                           | F0 Foundation        |
                           +----------+-----------+
                                      |
                    +-----------------+-----------------+
                    |                                   |
          +---------v----------+              +---------v----------+
          | F1 Wallet Read     |              | F3 Ops Read        |
          +---------+----------+              +---------+----------+
                    |                                   |
          +---------v----------+              +---------v----------+
          | F2 Wallet Actions  |              | F4 Ops Actions     |
          +---------+----------+              +---------+----------+
                    \                                   /
                     \                                 /
                      +---------------+----------------+
                                      |
                           +----------v-----------+
                           | F5 Failure Lab       |
                           +----------+-----------+
                                      |
                           +----------v-----------+
                           | F6 Learning + Obs    |
                           +----------+-----------+
                                      |
                           +----------v-----------+
                           | F7 Packaging/Demo    |
                           +----------+-----------+
                                      |
                           +----------v-----------+
                           | F8 Triggered Growth  |
                           +----------------------+
```

The diagram shows the safest default sequence, not a requirement that all work is
strictly serial.

Allowed parallelism after F0:

- F1 and F3 may proceed independently when their contracts are ready.
- Design-system and accessibility improvements may support both tracks.
- F6 correlation design may begin earlier, but full integration waits for stable
  transaction and operator detail surfaces.
- F5 scenario design may begin on paper, but its controller and UI wait for safe
  backend invocation and evidence contracts.

Disallowed shortcuts:

- F2 must not bypass F1 domain and evidence foundations.
- F4 must not migrate writes before F3 proves read parity and backend-owned
  available actions.
- F7 must not expose unrestricted operator or chaos controls publicly.
- F8 must not build UI for backend tracks that remain speculative.

---

# 8. Phase details

## F0 — Product and platform foundation

### Objective

Create the repository, security, contract, domain, design-system, testing, and CI
foundation needed by every later browser surface.

### Product outcome

The repository contains independently buildable Wallet and Operations shells,
shared reviewed packages, deterministic fixtures, and one reference evidence
screen. No live money journey is claimed yet.

### Main workstreams

1. Product vision, personas, journey inventory, information architecture, and
   browser boundary documentation.
2. Frontend workspace, pinned tools, strict TypeScript, lint, formatting, tests,
   builds, and repository commands.
3. Exact-money, identifiers, timestamps, statuses, and error domain primitives.
4. API transport validation, mapping, fixture contracts, and Mock Service Worker.
5. Shared UI foundations for financial data, evidence, forms, tables, timelines,
   loading, empty, and failure states.
6. Authentication architecture seams without claiming completed login flows.
7. Security, privacy, accessibility, dependency, and performance baselines.
8. Preservation and verification of the existing Admin BFF console.
9. CI and full-repository verification integration.

### Dependencies

- Current repository and Admin BFF behavior must be re-inventoried.
- Plan 52 contract direction must be understood.
- Fixture contracts must be explicitly temporary when canonical contracts are
  not implemented.

### Activation trigger

Satisfied by the conscious decision recorded on 2026-07-28 to build a browser
experience for Seev.

### Exit gate

- Workspace installs and builds deterministically.
- Wallet and Operations shells remain separate.
- Shared domain and UI packages have tests and examples.
- Exact money cannot accidentally degrade to floating-point arithmetic.
- Fixture-backed transaction evidence renders balanced entries and independent
  status dimensions.
- Browser trust boundaries are documented and testable.
- Existing Admin BFF console remains operational.
- Frontend checks are included in repository-owned commands and CI.
- Documentation does not claim live journeys.

### Anti-scope

- No complete registration, login, KYC, transfer, top-up, or payout journey.
- No modern replacement for the operator console.
- No live operator mutation.
- No Failure Lab controller.
- No public hosted demo.

### Execution plan

[Plan 56 — F0 Frontend Platform Foundation](active/56-f0-frontend-platform-foundation.md)
contains the locked implementation details.

---

## F1 — Wallet read model and transaction explorer

### Objective

Build the first genuinely useful public browser experience: a user can
authenticate and inspect authoritative account, balance, transaction, entry,
notification, profile, and KYC state.

### Why this phase comes first

Read-only financial evidence has high learning value and lower operational risk
than mutation flows. It also validates contracts, session handling, pagination,
money representation, status vocabulary, and error behavior before the frontend
can initiate financial changes.

### Primary journeys

1. Register or use a seeded local user.
2. Login and maintain a safe browser session.
3. View profile and KYC state.
4. View wallet accounts and available versus held balances.
5. Browse transaction history with server-side filters and pagination.
6. Open a transaction detail page.
7. Inspect workflow state, ledger state, related entries, fees, identifiers, and
   timestamps.
8. Browse account statements or ledger entries where a public contract permits.
9. Read and acknowledge notifications.

### Required product surfaces

```text
/wallet
/wallet/accounts
/wallet/accounts/:accountId
/wallet/transactions
/wallet/transactions/:transactionId
/wallet/notifications
/wallet/profile
/wallet/kyc
```

The exact routes may change in the execution plan, but the capability boundaries
remain.

### Required backend capabilities

- Safe Auth browser session integration.
- Current-user profile and KYC read contracts.
- Account and balance summary contract.
- Transaction list and detail contract.
- Entry or evidence detail sufficient to explain balanced movement.
- Notification list and read-state contract.
- Uniform error and request-correlation behavior.
- Server-side pagination and filter semantics for growing datasets.

### Activation trigger

Activate only when:

1. F0 is complete or its required foundation gates are green;
2. the necessary Auth and Gateway read contracts are canonical or explicitly
   accepted for implementation;
3. Plan 52 has implemented the relevant contract and compatibility baseline, or
   the execution plan records a narrower approved alternative;
4. backend responses expose enough source-of-truth detail without frontend
   database access.

### Exit gate

- A seeded user can login and complete the read-only journey end to end.
- Account balances and held funds are displayed distinctly.
- Transaction list filtering and pagination remain stable across refresh.
- Transaction detail shows authoritative state without inventing conclusions.
- Debit and credit evidence can be understood and is balanced where the public
  model exposes both sides.
- Loading, empty, stale, auth, permission, validation, dependency, and unknown
  failures are covered.
- Critical routes pass unit, component, contract, accessibility, and browser E2E
  gates.
- No mutation flow is accidentally exposed.

### Anti-scope

- No transfer, top-up, payout, fee acceptance, or scheduling mutation.
- No operator screens.
- No direct public observability infrastructure links.
- No frontend-derived authoritative balance or fee.
- No realtime architecture unless read performance evidence requires it.

### Expected execution-plan detail

The future F1 execution document should lock:

- session topology;
- public route and query contracts;
- transport-to-domain mappings;
- pagination and filter semantics;
- transaction evidence model;
- notification read behavior;
- KYC-state vocabulary;
- cache and refetch policy;
- accessibility and E2E matrices;
- rollout from fixture to live contracts.

---

## F2 — Interactive wallet journeys

### Objective

Allow a user to safely initiate and follow the complete lifecycle of fee quotes,
peer-to-peer transfers, top-ups, and payouts while preserving idempotency,
uncertain outcomes, and backend authority.

### Primary journeys

#### Fee quotation

- Enter transaction details.
- Request an authoritative quote.
- Display fee, total debit, recipient amount, currency, and expiry.
- Require re-quote after expiration.
- Prevent the frontend from recalculating or silently replacing the quote.

#### Peer-to-peer transfer

- Select source account and destination.
- Enter exact amount and optional description.
- Review quote and transaction summary.
- Submit using an idempotency key.
- Handle client retry and duplicate submission safely.
- Navigate to authoritative transaction evidence.

#### Top-up

- Create a top-up intent.
- Show pending external action.
- In local mode, allow safe mock-provider completion through a dedicated test
  control.
- Show callback verification, posting, and finalization as distinct evidence.

#### Payout or withdrawal

- Validate KYC, limits, source account, amount, and quote.
- Confirm destination and total debit.
- Show hold creation, dispatch, external outcome, recovery, settlement, or
  cancellation.
- Treat timeout or unknown provider result as uncertain, not automatic failure.
- Prevent the user from being encouraged to create a second payout while the
  first outcome is unknown.

### Optional educational controls

Local-only controls may demonstrate:

- idempotency-key reuse;
- duplicate client submission;
- delayed response rendering;
- duplicate top-up callback;
- invalid callback data;
- vendor timeout;
- dependency outage.

They must be compile-time or configuration-gated, visibly labelled, and absent
from restricted or public production builds.

### Dependencies

- F1 domain, session, evidence, error, and transaction-detail foundations.
- Stable mutation contracts and idempotency behavior.
- Stable fee quote semantics.
- Public Gateway composition for actions that span internal services.
- Mock vendor controls that cannot become unrestricted production operations.

### Activation trigger

Activate when:

1. F1 has proven live read behavior;
2. mutation and error contracts are canonical;
3. idempotency semantics are documented and tested;
4. quote expiration and one-time consumption behavior are stable;
5. top-up and payout local mock integrations are deterministic enough for E2E;
6. the product decision accepts the additional security and support surface.

### Exit gate

One browser E2E journey proves:

```text
register/login
→ complete or satisfy local KYC
→ create and complete top-up
→ observe balance and evidence
→ request fee quote
→ transfer money
→ inspect balanced transaction evidence
→ request payout
→ observe hold
→ settle, cancel, or recover payout
→ inspect final balance and notifications
```

Additional exit requirements:

- Duplicate submissions create no duplicate financial effect.
- Quote expiry is explicit and tested.
- Unknown payout outcome is represented safely.
- Final balance is always revalidated from backend authority.
- Sensitive fields and destination details are appropriately masked.
- Local educational controls cannot be enabled accidentally in restricted builds.

### Anti-scope

- No frontend-owned fee engine.
- No optimistic final balance or transaction success.
- No arbitrary mock vendor command surface.
- No real-money provider or licensing claim.
- No advanced merchant, multi-currency, or savings products.

---

## F3 — Operations read-only investigation

### Objective

Build a modern read-only Operations application that helps operators understand
system health and investigate transactions without changing money or operational
state.

### Migration principle

The existing htmx/Pico Admin BFF console remains primary for live operations
until the modern application proves each relevant read surface. F3 does not
remove or bypass it.

### Primary surfaces

```text
/operator
/operator/payouts
/operator/payouts/:payoutId
/operator/payins
/operator/payins/:payinId
/operator/assurance
/operator/assurance/findings/:findingId
/operator/reconciliation
/operator/reconciliation/:batchId
/operator/outbox
/operator/vendor-commands
/operator/audit
/operator/system
```

### Dashboard questions

The application should help an operator answer:

1. Is intake open or paused?
2. Are any payout outcomes uncertain?
3. Are durable vendor commands or outbox events stuck?
4. Are payin or payout workflows inconsistent with ledger evidence?
5. Are reconciliation items unresolved?
6. Are external providers degraded?
7. Which sensitive actions happened recently and who performed them?

### Investigation evidence

A payout detail may include:

- payout identity, user, destination, amount, fee, and timestamps;
- workflow transitions;
- ledger hold and final posting references;
- selected route and vendor command attempts;
- responses and uncertain outcomes;
- recovery attempts;
- related assurance or reconciliation findings;
- correlated audit and trace identifiers;
- backend-provided available actions, displayed as information only in F3.

### Backend requirements

- Admin BFF remains the only browser integration edge.
- Summary endpoints exist where browser fan-out would be inefficient or unsafe.
- Lists provide pagination, filters, stable sort semantics, and bounded queries.
- Detail contracts provide evidence and backend-derived available actions.
- Operator sessions, CSRF, role mapping, and audit context remain server-owned.

### Activation trigger

Activate when:

1. F0 is complete;
2. the current operator console has enough value or complexity to justify a
   richer investigation experience;
3. Admin BFF can expose typed read contracts without duplicating downstream
   business logic;
4. the repository accepts maintaining both consoles during migration.

### Exit gate

- An operator can login through the modern shell.
- Core read-only dashboards and investigation pages work through Admin BFF.
- The modern view reaches evidence parity for the selected domains.
- No mutation is enabled.
- Permission failures and expired sessions fail safely.
- Existing htmx console remains tested and runnable.
- Operator E2E tests prove that no direct internal-service browser request occurs.

### Anti-scope

- No mutation migration.
- No direct service or database access.
- No frontend decision about retry, cancel, resolve, approve, or resume
  eligibility.
- No removal of current templates, routes, or embedded assets.
- No public exposure of operator data or observability links.

---

## F4 — Controlled operations and governance

### Objective

Migrate selected operator actions from the existing console to the modern
Operations application without losing authorization, maker/checker separation,
audit evidence, safe retries, or rollback capability.

### Migration unit

Actions migrate one domain at a time. A recommended order is:

1. low-risk acknowledgement and note actions;
2. assurance finding acknowledgement and resolution;
3. reconciliation resolution;
4. eligible payout recovery actions;
5. emergency intake pause;
6. maker/checker resume;
7. ledger adjustment proposals and approvals;
8. configuration changes such as fee, routing, policy, or fraud rules.

The actual order must be re-evaluated from live risk and usage evidence.

### Core interaction pattern

Every sensitive mutation should provide:

- backend-provided eligibility;
- exact affected resource and current version;
- impact summary;
- mandatory reason where appropriate;
- explicit review step;
- CSRF and session protection;
- idempotency or command identity where appropriate;
- authoritative response;
- resulting audit reference;
- safe behavior on retry, stale version, or uncertain network response.

### Maker/checker pattern

```text
Maker creates proposal
        ↓
Proposal persists as pending
        ↓
Different authorized checker reviews evidence
        ↓
Checker approves or rejects
        ↓
Backend performs or refuses the action
        ↓
Audit evidence links proposal, decision, and result
```

The UI may explain and enforce the expected flow, but backend identity and policy
must reject self-approval or unauthorized actions.

### Activation trigger

Activate a mutation domain only when:

1. F3 read-only evidence parity exists for that domain;
2. the modern UI provides meaningful usability or safety improvement;
3. Admin BFF exposes a typed, authorized, audited command contract;
4. backend returns available actions and conflict behavior;
5. current-console rollback remains possible;
6. E2E fixtures cover success, rejection, retry, stale version, timeout, and
   duplicate submission.

### Exit gate for each migrated domain

- Read parity is documented.
- Authorization parity is tested.
- Audit parity is demonstrated.
- Maker/checker separation is tested where required.
- Duplicate and uncertain submissions have safe outcomes.
- The old action remains available as rollback until the migration observation
  window passes.
- Removal of the old action, if desired, occurs in a separate reviewed cleanup.

### Anti-scope

- No bulk approval by default.
- No browser-only authorization.
- No arbitrary SQL or generic CRUD console.
- No direct database fixes.
- No optimistic success for sensitive actions.
- No big-bang removal of the existing operator console.

---

## F5 — Failure Lab

### Objective

Turn Seev's existing tests, mocks, chaos scenarios, recovery mechanisms, and
invariant checks into a controlled browser learning experience.

### Architecture direction

```text
Failure Lab UI
      ↓
Local-only Lab Controller
      ├── allowlisted scenario catalog
      ├── business E2E runner
      ├── chaos runner
      ├── mock provider controls
      ├── evidence collector
      ├── invariant verifier
      └── deterministic cleanup/reset
```

The controller is not a remote shell. It accepts scenario identifiers and typed
parameters only.

### Scenario levels

#### Level 1 — Product and idempotency

- duplicate transfer request;
- duplicate top-up callback;
- expired quote;
- insufficient funds;
- KYC or policy rejection.

#### Level 2 — Distributed systems

- delayed outbox publication;
- duplicate consumer delivery;
- dependency outage;
- vendor timeout;
- restart after durable command persistence;
- recovery worker continuation.

#### Level 3 — Operational integrity

- payin/ledger mismatch;
- payout/ledger mismatch;
- reconciliation difference;
- assurance finding;
- emergency intake pause;
- maker/checker resume;
- projection or statement rebuild where supported.

### Scenario result model

Every run should show:

1. scenario purpose;
2. prerequisites;
3. expected invariants;
4. commands requested;
5. durable records created;
6. events published or retried;
7. recovery activity;
8. final authoritative verification;
9. cleanup result.

### Activation trigger

Activate when:

- stable Wallet or Operations evidence views exist;
- existing scripts can be wrapped without arbitrary execution;
- scenarios are deterministic enough to reset;
- backend verifiers can produce structured pass/fail evidence;
- the local/test-only security boundary is reviewed.

### Exit gate

- The scenario catalog is allowlisted and typed.
- Browser input cannot execute arbitrary commands.
- Every scenario has deterministic setup and cleanup.
- Pass/fail comes from backend verification, not UI inference.
- Repeated runs do not corrupt later scenarios.
- Restricted and public builds omit dangerous controls.
- At least one scenario from each activated level is covered by browser E2E.

### Anti-scope

- No production chaos control.
- No arbitrary shell, SQL, container, or network command.
- No real vendor disruption.
- No scenario whose cleanup is undefined.
- No claim of exactly-once delivery; demonstrate idempotent effects instead.

---

## F6 — Observability and learning integration

### Objective

Connect product and operational evidence to the repository's dashboards, traces,
logs, metrics, and learning documentation so a user can understand both what
happened and why.

### Correlation experience

Where authorized, expose copyable identifiers such as:

- request ID;
- trace ID;
- transaction ID;
- workflow ID;
- event ID;
- idempotency reference;
- audit ID;
- reconciliation or assurance finding ID.

### Operator integrations

Operations may provide controlled deep links to:

- Tempo traces;
- correlated logs;
- Grafana dashboards;
- RabbitMQ operational pages;
- service health views;
- relevant runbooks.

Public Wallet must not receive internal infrastructure locations or unrestricted
operator evidence.

### Learning layers

A technical evidence section may support three levels:

1. **Product explanation** — what the user should understand.
2. **System explanation** — which workflow, ledger, and delivery boundaries were
   involved.
3. **Engineering evidence** — identifiers, entries, events, traces, and recovery
   records available to an authorized learner or operator.

### Activation trigger

Activate when:

- correlation identifiers are stable across required services;
- A1 observability components expose safe local links;
- Wallet and/or Operations detail pages have stable resource models;
- access-control rules distinguish public, learner, and operator evidence.

### Exit gate

- A transaction can be followed across permitted product and system evidence.
- Operator links open the correct trace, logs, dashboard, or runbook.
- Public surfaces reveal no internal infrastructure data.
- Missing observability systems fail gracefully.
- Documentation and UI explanations describe current executable behavior.
- Correlation navigation is covered by tests and sanitized fixtures.

### Anti-scope

- No replacement for Grafana, Tempo, Loki, or RabbitMQ UI.
- No high-cardinality frontend metrics using user or transaction identifiers.
- No raw secret, token, document, or full sensitive payload display.
- No public trace or log access.

---

## F7 — Hardening, packaging, and public demonstration

### Objective

Make the browser experience reproducible for local learners and safe enough for a
restricted hosted demonstration without implying production or real-money
readiness.

### Local packaging target

A repository-owned command should eventually provide a deterministic demo stack,
for example:

```text
make demo-up
```

The activated execution plan decides the final command and profiles. The stack
may include:

- PostgreSQL;
- Redis;
- RabbitMQ;
- Seev services;
- Wallet application;
- Operations application;
- optional observability;
- deterministic seed data;
- reset tooling.

### Seeded personas

Local demo data should provide stable identities for:

- a verified wallet user with transaction history;
- a recipient user;
- an operator maker;
- a distinct operator checker.

Credentials must be explicitly local/demo-only and never resemble a production
secret-management pattern.

### Hardening workstreams

- secure headers and Content Security Policy;
- cookie and session review;
- dependency and supply-chain review;
- accessibility audit;
- browser performance budgets;
- large-table and polling behavior;
- privacy and masking review;
- deterministic install and build;
- container and local developer experience;
- restricted public-demo reset, rate limit, and abuse controls;
- documentation and screenshots generated from current behavior.

### Public demonstration constraints

A hosted demonstration, if consciously activated, should be:

- mock-money only;
- isolated from production or personal data;
- periodically reset;
- rate-limited;
- restricted for operator actions;
- stripped of dangerous Failure Lab controls;
- stripped of unrestricted observability access;
- clearly labelled as an educational reference;
- monitored for cost and abuse.

### Activation trigger

Activate when the project has stable end-to-end value worth demonstrating:

- F1 and at least part of F2 are complete;
- F3 is complete for useful investigation surfaces;
- selected F4 or F5 capabilities are stable if included;
- security, privacy, and reset ownership are accepted.

### Exit gate

- Fresh checkout to working local demo is documented and tested.
- Seed and reset behavior is deterministic.
- Critical browser journeys pass full repository verification.
- Accessibility and security review findings are resolved or explicitly accepted.
- Public demo configuration cannot expose operator, chaos, secret, or internal
  observability capabilities unintentionally.
- Documentation preserves the non-production, mock-money disclaimer.

### Anti-scope

- No real-money deployment.
- No formal security, compliance, or availability certification claim.
- No unrestricted public operator console.
- No permanent demo data.
- No cloud architecture expansion without a separate evidence-based plan.

---

## F8 — Trigger-based product expansion

### Objective

Add browser product areas only after their corresponding backend business or
learning tracks are activated and implemented.

F8 is not one execution plan. Each activated product area receives a separate
roadmap or execution document.

### F8.1 Merchant portal

Activated by C1 after required internal security and contract foundations.
Potential scope:

- API key creation and rotation;
- scopes and quotas;
- webhook endpoint configuration;
- signing-secret lifecycle;
- delivery attempts and replay;
- sandbox transactions;
- merchant users and roles;
- usage summaries.

Anti-scope before activation: no frontend-only API key model and no exposure of
internal service contracts as merchant contracts.

### F8.2 Analytics and revenue portal

Activated by C2 when analytics workloads move away from OLTP or CDC learning is
chosen.

Potential scope:

- transaction volume;
- fee and revenue facts;
- success and failure rates;
- vendor performance;
- reconciliation gaps;
- unit economics;
- warehouse freshness and reconciliation to ledger totals.

Anti-scope before activation: no heavy browser analytics directly against OLTP.

### F8.3 Notification preferences

Activated by C3.

Potential scope:

- in-app, email, and push preferences;
- per-event preferences;
- digest configuration;
- delivery history;
- retry or failure diagnostics for authorized operators.

### F8.4 Multi-currency wallet

Activated by C4.

Potential scope:

- currency-specific accounts and balances;
- currency-specific limits;
- FX quote and expiry;
- source and destination amount clarity;
- settlement and position evidence;
- prevention of currency mixing.

### F8.5 Advanced financial products

Activated by C5.

Potential scope:

- savings products;
- accrual history;
- scheduled transfers;
- interest capitalization;
- top-up fees;
- period-close evidence;
- product statements.

### Activation and exit rules

Each F8 area must define:

- backend ownership and completed prerequisites;
- business and learning purpose;
- product journeys;
- data and privacy classification;
- financial invariants;
- operational controls;
- scale model;
- separate execution plan and DoD.

---

## 9. Release milestone map

Phases are engineering boundaries. Releases are user-visible checkpoints.

| Milestone | Required phases | Demonstrable outcome |
|---|---|---|
| M0 — Frontend foundation | F0 | Two buildable shells, shared financial UI/domain packages, deterministic fixtures, and a reference evidence screen. |
| M1 — Transaction Explorer | F1 | A user can login and inspect balances, transactions, entries, notifications, profile, and KYC state. |
| M2 — Interactive Wallet | F2 | A user can safely run top-up, transfer, and payout journeys with quotes, idempotency, holds, and uncertain-state UX. |
| M3 — Operations Explorer | F3 | An operator can investigate system, payout, payin, assurance, reconciliation, and delivery evidence. |
| M4 — Controlled Operations | F4 | Selected operator actions run through typed, authorized, audited, and maker/checker-safe flows. |
| M5 — Failure Lab | F5 | A learner can run controlled local failure scenarios and see authoritative invariant evidence. |
| M6 — Explainable System | F6 | Product state links to technical evidence, observability, runbooks, and plain-language explanations. |
| M7 — Reproducible Demo | F7 | A fresh checkout can run a deterministic local demo; an optional restricted hosted demo is safe and clearly educational. |
| M8 — Expanded Products | F8 | One or more backend-triggered merchant, analytics, notification, multi-currency, or financial-product surfaces exist. |

Every milestone should be publishable independently. The project should not wait
for F7 or F8 before showing useful progress.

---

## 10. Recommended delivery order

Default order:

1. Complete F0.
2. Activate F1 as the highest-value, lowest-risk live product slice.
3. Activate F2 after read models and transaction evidence are proven.
4. Activate F3 in parallel with or after F1 depending on Admin BFF contract
   readiness.
5. Activate F4 one operational domain at a time.
6. Activate F5 after evidence views and safe local scenario invocation exist.
7. Integrate F6 progressively once correlation models are stable.
8. Activate F7 only after the project has stable journeys worth packaging.
9. Activate F8 tracks only from real backend/product triggers.

Recommended Pareto priority:

```text
Highest impact first

1. F0 exact-money, contract, session, and UI foundation
2. F1 transaction detail and ledger evidence
3. F2 transfer with quote and idempotency
4. F2 payout with hold and uncertain-state UX
5. F3 payout investigation
6. F3 assurance and reconciliation investigation
7. F4 maker/checker and recovery
8. F5 duplicate-request and payout-recovery scenarios
9. F6 trace and runbook correlation
10. F7 local/public demonstration packaging
```

---

## 11. Global acceptance dimensions

Every phase execution document must include acceptance evidence for the dimensions
that apply.

### Product

- User and operator journeys are explicit.
- Success criteria are measurable.
- Empty, failure, stale, and uncertain states are defined.
- The UI does not contradict backend ownership.

### Financial integrity

- Exact money representation is preserved.
- Authoritative balances and fees come from backend contracts.
- Ledger evidence is not mutated or reinterpreted by the browser.
- Idempotency and duplicate behavior are tested.
- Holds and uncertain outcomes are represented accurately.

### Security

- Browser trust boundary is documented.
- Sessions, CSRF, cookies, and secrets are reviewed.
- Backend authorization is tested.
- Sensitive information is masked and excluded from logs.
- Operator and public applications remain separated.

### Contracts

- Runtime payload validation exists.
- Compatibility and deprecation behavior is defined.
- Transport DTOs map to domain objects.
- Fixture and live behavior are distinguishable.
- Contract tests cover success and error forms.

### Quality

- Unit, component, integration, and browser E2E tests are appropriate to risk.
- Accessibility is tested.
- Performance limits and pagination are explicit.
- Dependency installation and builds are deterministic.
- Full repository verification remains green.

### Operations

- Request and audit identifiers are discoverable.
- Mutation retry and uncertain-response behavior is safe.
- Rollback path exists for migrated operator capabilities.
- Runbooks and observability links are current where applicable.

### Documentation

- Current behavior and target behavior remain distinguishable.
- Roadmap and active-plan indexes are updated.
- Screenshots and examples are generated from executable behavior.
- Completed plans move to archive with result evidence.

---

## 12. Global risk register

### R1 — Frontend outruns API contracts

**Risk:** Pages become coupled to accidental handler payloads.

**Control:** F1 and later live integration require canonical or explicitly
accepted contracts, runtime validation, domain mapping, and compatibility gates.

### R2 — Modern UI weakens operator safety

**Risk:** A visually improved console loses CSRF, server sessions, maker/checker,
audit, or backend authorization behavior.

**Control:** Preserve the existing console, build read-only first, migrate one
action domain at a time, and require parity evidence.

### R3 — Financial state is oversimplified

**Risk:** The UI reports generic success while workflow, ledger, external, or
reconciliation facts disagree.

**Control:** Maintain independent status dimensions and use backend-owned
language.

### R4 — JavaScript money errors

**Risk:** Floating-point conversion or formatting changes authoritative values.

**Control:** Exact transport, `bigint` domain values, shared formatting, and
property/unit tests.

### R5 — Browser becomes a new internal integration layer

**Risk:** Wallet or Operations calls downstream services directly to accelerate
implementation.

**Control:** Enforce Auth/Gateway/Admin BFF boundaries through architecture,
client packages, CORS/network configuration, and E2E assertions.

### R6 — Failure Lab becomes dangerous

**Risk:** The browser gains arbitrary shell, database, container, or chaos access.

**Control:** Local-only allowlisted scenario controller, typed inputs, structured
outputs, deterministic cleanup, and restricted builds.

### R7 — Dual-console maintenance lasts indefinitely

**Risk:** htmx and modern Operations both require ongoing changes.

**Control:** Define domain migration criteria, observation windows, rollback,
and separate cleanup plans. Do not remove old behavior before parity.

### R8 — Scope expansion prevents usable releases

**Risk:** The project attempts Wallet, Operations, Lab, observability, merchant,
and analytics simultaneously.

**Control:** Every phase produces an independently demonstrable milestone and
honors explicit anti-scope.

### R9 — Hosted demo leaks sensitive capabilities

**Risk:** Public access exposes operator actions, infrastructure links, or
persistent data.

**Control:** Separate restricted configuration, mock money, reset, rate limits,
no dangerous controls, sanitized observability, and explicit disclaimer.

### R10 — Documentation claims future behavior

**Risk:** README and portfolio content describe roadmap targets as implemented.

**Control:** Documentation truth gate in every phase and archived result evidence
before status changes.

---

## 13. Decisions intentionally deferred to execution plans

This roadmap does not permanently lock details that require current evidence.
Each activated plan should decide or reconfirm:

- exact Next.js or alternative framework version;
- package manager and workspace tooling versions;
- direct-browser versus server-mediated Wallet session topology;
- API client generation approach after Plan 52;
- exact route hierarchy;
- polling intervals and realtime need;
- table virtualization thresholds;
- Storybook or component-catalog tooling;
- containerization of frontend development and builds;
- production or demo hosting provider;
- public evidence visibility tiers;
- modern-console migration order;
- Lab Controller implementation language and process boundary.

Deferral prevents this reference roadmap from becoming stale implementation
instruction.

---

## 14. Traceability from product capability to phase

| Capability | Owning phase |
|---|---|
| Frontend workspace and shared packages | F0 |
| Exact money and status domain primitives | F0 |
| Design system and fixture infrastructure | F0 |
| Authentication shell and safe session integration | F1, with architecture seams in F0 |
| Accounts, balances, transaction list, transaction evidence | F1 |
| Notifications and KYC read state | F1 |
| Fee quote and transfer | F2 |
| Top-up and payout | F2 |
| Operator dashboard and investigation | F3 |
| Assurance and reconciliation read views | F3 |
| Maker/checker and audited mutations | F4 |
| Payout recovery and intake controls | F4 |
| Controlled chaos and invariant visualization | F5 |
| Trace, log, dashboard, and runbook links | F6 |
| Learning explanations integrated with live evidence | F6 |
| Local demo, seeds, reset, and hosted demo hardening | F7 |
| Merchant portal | F8 / C1 |
| Analytics portal | F8 / C2 |
| Notification preferences | F8 / C3 |
| Multi-currency wallet | F8 / C4 |
| Savings and advanced products | F8 / C5 |

---

## 15. Checklist for every future frontend execution plan

- [ ] Activation evidence or conscious learning decision is written at the top.
- [ ] Live repository and contract facts are rechecked.
- [ ] The roadmap phase and milestone are named.
- [ ] Product users and journeys are explicit.
- [ ] Browser and backend ownership boundaries are explicit.
- [ ] Scope and copied anti-scope are explicit.
- [ ] Dependencies and prerequisite gates are measurable.
- [ ] Design decisions are locked before implementation tasks.
- [ ] Exact-money and authoritative-state rules are preserved.
- [ ] Authentication, authorization, CSRF, privacy, and masking are addressed.
- [ ] Loading, empty, stale, uncertain, and error states are covered.
- [ ] Unit, component, contract, integration, E2E, and accessibility tests are
      selected according to risk.
- [ ] Rollout and rollback are defined.
- [ ] Existing Admin BFF console compatibility is addressed where relevant.
- [ ] Full repository verification is defined.
- [ ] Documentation truth and roadmap index updates are included.
- [ ] Result evidence is recorded before archive.

---

## 16. Current next action

The roadmap itself is now defined. The only frontend phase currently activated
is F0 through:

- [Plan 56 — F0 Frontend Platform Foundation](active/56-f0-frontend-platform-foundation.md)

F1 and later must remain `Future` until their triggers are explicitly evaluated.
Completing F0 should produce the evidence needed to decide whether F1 is ready
for its own detailed execution plan.
