# 56 — F0 Frontend Platform Foundation

> [Documentation home](../../README.md) · [Roadmap](../README.md) ·
> [Active plans](README.md)

> Related context:
> [42-long-term-roadmap.md](../42-long-term-roadmap.md),
> [55-frontend-long-term-roadmap.md](../55-frontend-long-term-roadmap.md),
> [archived plan 52](../archive/52-a9-api-contracts-schema-evolution.md),
> [current architecture](../../reference/architecture.md), and
> [service reference](../../reference/services.md).
>
> **Status: ready for execution; not implemented.** The activation trigger is a
> conscious product and learning decision made on 2026-07-28: Seev should gain a
> browser experience that makes money movement, operational evidence, and later
> failure recovery observable without weakening existing service boundaries.
>
> This is the first execution plan activated from the frontend roadmap. It creates the frontend platform only. It does not claim that the wallet
> journeys, a modern operator console, or the Failure Lab exist. Those product
> surfaces require separate execution plans after this foundation and their
> contract prerequisites pass.

## 1. Trigger and objective

Seev already demonstrates reliable money movement through executable backend
services, tests, mock integrations, operational controls, and an interactive
learning story. A first-time visitor can learn the architecture, but cannot yet
use a modern browser application to inspect the same ideas as structured product
state.

The repository also has an operator console. That console is intentionally
server-rendered by Admin BFF, uses embedded htmx and Pico CSS assets, and has no
Node runtime dependency. It is useful current behavior and must not be removed
merely because a new frontend stack is introduced.

The frontend platform solves a narrower first problem:

> Establish one secure, typed, testable, and maintainable browser foundation on
> which Seev can later build a Wallet experience, a modern Operations experience,
> and a local-only Failure Lab without allowing browsers to bypass Auth, Gateway,
> Admin BFF, or service ownership.

This is a platform plan, not a page-building sprint. Its main outputs are locked
boundaries, repository structure, domain primitives, reusable UI foundations,
API integration rules, deterministic test infrastructure, and repository gates.
A small fixture-backed reference screen proves those foundations, but it is not
presented as a live money journey.

### Measurable targets

1. The repository contains one pinned frontend workspace under `web/` with two
   independently buildable applications: `wallet` and `operator`.
2. `wallet` and `operator` share reviewed packages without importing each
   other's application code.
3. TypeScript strict mode, linting, formatting, unit tests, component tests,
   builds, and browser smoke tests run through repository-owned commands.
4. Money crosses the frontend boundary as an exact string or safe integer form,
   maps to a `bigint` minor-unit domain type, and is never calculated with binary
   floating point.
5. Backend transport objects are validated at runtime and mapped into frontend
   domain objects; generated or handwritten API DTOs do not leak directly into
   presentation components.
6. Browser trust boundaries are documented and tested: Wallet reaches only Auth
   and Gateway-facing surfaces, while Operator reaches only Admin BFF-facing
   surfaces.
7. The existing embedded Admin BFF console remains runnable and covered while
   the new operator application is incomplete.
8. A reusable design system covers money, status, identity, evidence, forms,
   tables, timelines, loading, empty, and failure states without relying on color
   alone.
9. Mock Service Worker fixtures let both applications run deterministically
   before live HTTP contracts are ready, and the UI clearly labels mocked data.
10. A fixture-backed transaction evidence screen demonstrates balanced entries,
    workflow status, ledger status, request identifiers, and accessible states.
11. No browser bundle contains server secrets, internal service credentials,
    operator session material, or unrestricted observability endpoints.
12. The frontend gate is added to CI and the full repository gate without
    weakening existing Go, protobuf, documentation, smoke, business, admin, or
    chaos verification.
13. Tool versions and package integrity are pinned; clean installation and
    deterministic builds succeed from a fresh checkout.
14. The final plan evidence records commands, artifact counts, test results,
    accessibility results, dependency review, and deferred follow-up plans.

## 2. Live repository facts

These facts were verified when this plan was written. Execution must recheck the
live tree before changing code or documentation.

### 2.1 Current product and service surfaces

- Seev is an open-source learning and engineering reference. It does not claim
  to be production-ready, certified, supported, or suitable for real money.
- Nine Go services are deployable: Gateway, Auth, Ledger, Payin, Payout, Fraud,
  Admin BFF, Assurance, and VendorService. VendorService owns active vendor
  connectivity and callback ingress; Payin and Payout retain business-state
  ownership.
- Auth owns registration, login, refresh tokens, profiles, roles, and KYC state.
- Gateway is the public composition edge for user money journeys,
  notifications, and selected Ledger operations.
- Admin BFF owns operator sessions, maker/checker interaction, typed admin
  proxying, audit evidence, and the current browser console.
- Service databases are private to their owning services. A browser must never
  query a service database or create an alternate ownership path.

### 2.2 Current browser behavior

- The repository does not contain a root `web/` workspace or a root
  `package.json`.
- Admin BFF contains `services/adminbff/internal/web/` with embedded Go templates and
  embedded assets.
- The current embedded assets include `htmx.min.js` and `pico.min.css`.
- Current templates include dashboard, catalog, maker, payout, and reconciliation
  pages.
- `services/adminbff/internal/web/web.go` states that the console has no CDN or Node
  runtime dependency and that operators use htmx requests to Admin BFF for live
  data.
- The current console is therefore a supported operational surface, not a
  disposable prototype.

### 2.3 Current contract state

- HTTP contracts are currently implicit in handlers and prose; no canonical
  OpenAPI baseline has been implemented yet.
- Active plan 52 defines the target OpenAPI, route inventory, uniform error,
  compatibility, deprecation, event schema, and conformance gates.
- The frontend cannot safely treat guessed response shapes as stable public
  contracts.
- Protobuf already has generation, lint, and breaking checks, but browsers do
  not consume protobuf directly in this plan.

### 2.4 Current repository verification

- The Makefile currently builds and verifies Go services, documentation,
  protobufs, migrations, local infrastructure, backups, smoke journeys, business
  journeys, admin journeys, and load-harness checks; `verify-chaos` provides the
  separate recovery scenarios.
- `make verify-full` starts from clean Docker volumes and is the authoritative
  repeatable non-chaos repository gate. `make verify-chaos` is the manual
  dependency-kill gate.
- No frontend lint, typecheck, unit, component, build, or Playwright targets are
  currently part of that gate.

### 2.5 Documentation truth rule

- Current documentation must describe executable behavior.
- Target architecture must remain visibly marked as target until acceptance
  evidence passes.
- This plan must not cause the root README, product tour, or architecture guide
  to imply that a Wallet application or modern Operations application exists
  before its own implementation plan completes.

## 3. Scope and anti-scope

### In scope

- a frontend-specific inventory of users, browser surfaces, API dependencies,
  current operator routes, and security boundaries;
- the `web/` workspace and deterministic package installation;
- separate Wallet and Operator application shells;
- shared TypeScript, lint, formatting, testing, and environment configuration;
- a shared design-system foundation and semantic financial components;
- exact money, identifier, timestamp, status, and API-error domain primitives;
- a transport-validation and domain-mapping layer;
- fixture contracts and Mock Service Worker handlers for deterministic local
  development;
- one fixture-backed transaction evidence reference screen;
- authentication architecture and cookie/session integration seams, without
  claiming the complete user authentication journey;
- accessibility, security, privacy, dependency, and performance baselines;
- frontend CI jobs and Make targets;
- local Docker/host integration design, without making Node a dependency of Go
  service containers;
- migration rules that preserve the current Admin BFF console;
- documentation and acceptance evidence required before follow-up frontend plans.

### Out of scope

- implementing live registration, login, refresh, logout, KYC, top-up, transfer,
  withdrawal, notification, scheduling, or statement journeys;
- implementing a complete read-only Wallet MVP against live services;
- replacing or deleting the current htmx/Pico Admin BFF console;
- migrating maker/checker, payout recovery, reconciliation, assurance, or other
  operator mutations to the new application;
- creating new business API semantics solely for frontend convenience;
- bypassing Gateway or Admin BFF to call Ledger, Payin, Payout, Fraud, or
  Assurance directly from a browser;
- changing backend service ownership or database ownership;
- treating client-side role checks as authorization;
- storing access tokens, refresh tokens, operator session secrets, or CSRF
  secrets in `localStorage`, `sessionStorage`, URLs, or readable cookies;
- optimistic final balance changes or optimistic success for money movement;
- WebSocket, Server-Sent Events, or a general realtime event gateway;
- a hosted public demo, native mobile application, desktop application, or
  Progressive Web App installation flow;
- merchant/B2B, analytics, multi-currency, savings, lending, or other long-term
  product tracks;
- a local Failure Lab controller or arbitrary command execution from a browser;
- GraphQL, tRPC across Go services, direct browser gRPC, or replacing existing
  HTTP contracts;
- introducing Turborepo, Nx, a remote build cache, or a private package registry
  without measured need;
- production deployment certification or a claim that browser controls make the
  repository suitable for real money.

## 4. Delivery boundary and follow-up gates

Plan 56 ends when the platform foundation is green. It deliberately stops before
live financial journeys.

The expected sequence after this plan is:

1. **Read-only Wallet execution plan** — authentication integration, account
   summary, transaction list, transaction evidence, notifications, and KYC
   status against live canonical contracts.
2. **Interactive Wallet execution plan** — fee quote, transfer, top-up, and
   withdrawal journeys with idempotency, retry, and uncertain-state UX.
3. **Modern Operations execution plan** — read-only investigation first, then
   controlled maker/checker and recovery migrations after parity evidence.
4. **Failure Lab execution plan** — local-only allowlisted scenario controller,
   structured evidence, deterministic cleanup, and invariant verification.
5. **Deployment and public-demo execution plan** — only after security,
   sanitization, rate limits, reset behavior, and operational ownership are
   separately reviewed.

The follow-up plans are not created merely because Plan 56 completes. Each must
record its activation decision, live contract prerequisites, business purpose,
anti-scope, and acceptance evidence.

## 5. Prerequisites and dependency gates

### P1 — Repository truth must be re-inventoried

Before implementation, recheck:

- root files and directories;
- Admin BFF template, route, cookie, CSRF, and session behavior;
- Auth and Gateway public route behavior;
- plan 52 implementation status;
- current CI and Make targets;
- current supported developer platforms;
- documentation indexes and archived plan references.

A stale plan is not permission to overwrite newer repository behavior.

### P2 — Archived Plan 52 controls live HTTP integration

Tasks that create the workspace, design system, domain primitives, fixtures, and
application shells may proceed before plan 52 is complete.

Live API integration may begin only when one of these gates is true:

1. the relevant operation is canonical and covered by plan 52; or
2. an explicitly temporary, reviewed fixture contract is stored under
   `web/packages/api-client/testdata/`, names its backend owner, records the
   observed handler behavior, and is not described as a stable public contract.

The second option is allowed only for foundation testing. It cannot be used to
ship a live financial journey or to freeze accidental handler behavior.

### P3 — Existing Admin BFF console is the operational fallback

The embedded console remains the authoritative current browser experience for
operator work until a future plan proves feature parity, security parity,
accessibility, audit evidence, failure behavior, and rollback.

No Plan 56 task may:

- remove an existing template or htmx endpoint;
- change operator action semantics to serve the new shell;
- weaken CSRF, role, session, audit, or maker/checker controls;
- move Admin BFF credentials into the browser;
- require Node to start Admin BFF.

### P4 — Node and package-manager versions are pinned at execution

T0 must select:

- an actively supported Node.js LTS release;
- an exact Node patch version;
- an exact pnpm version through Corepack or the package-manager field;
- exact direct dependency versions and a committed lockfile.

Do not use `latest`, floating major tags, or unreviewed install scripts in CI.
The selected versions must support the current Next.js release chosen during T0
and the repository's documented developer platforms.

### P5 — Security review precedes live session integration

The target cookie/session design in this plan is locked, but a live Auth or
Admin BFF session flow is a follow-up implementation. Before that flow ships,
review:

- cookie domain, path, Secure, HttpOnly, and SameSite values;
- CSRF model;
- refresh rotation and replay behavior;
- logout and server-side invalidation;
- origin and CORS rules;
- proxy header trust;
- cache headers;
- clickjacking protection;
- redirection allowlists;
- operator re-authentication needs.

## 6. Target topology

### 6.1 Repository topology

```text
seev/
├── api/
├── cmd/
├── config/
├── deploy/
├── docs/
├── gen/
├── internal/
├── migrations/
├── internal/platform/
├── scripts/
├── web/
│   ├── apps/
│   │   ├── wallet/
│   │   └── operator/
│   ├── packages/
│   │   ├── api-client/
│   │   ├── config/
│   │   ├── domain/
│   │   ├── money/
│   │   ├── testing/
│   │   └── ui/
│   ├── package.json
│   ├── pnpm-lock.yaml
│   ├── pnpm-workspace.yaml
│   ├── tsconfig.base.json
│   └── README.md
├── Makefile
└── docker-compose.yml
```

The `web/` directory is one workspace inside the existing Go repository. It does
not turn Go packages into JavaScript packages and does not make backend builds
dependent on frontend development servers.

### 6.2 Browser trust topology

```text
Public browser
    |
    +--> Wallet application
            |
            +--> Wallet web server / same-origin route layer
                    |
                    +--> Auth public contract
                    +--> Gateway public contract

Operator browser
    |
    +--> Operator application
            |
            +--> Operator web server / same-origin route layer
                    |
                    +--> Admin BFF contract
                            |
                            +--> approved internal service clients
```

The arrows describe allowed network ownership. They do not mean every route is
implemented in Plan 56. Wallet never calls internal service admin listeners.
Operator never stores downstream service credentials or calls those services
from the browser.

### 6.3 Current and target operator coexistence

```text
Current, retained                         Target, foundation only

Operator browser                          Operator browser
      |                                         |
      v                                         v
Admin BFF HTML + htmx                    Operator application shell
      |                                         |
      +--> current BFF endpoints                +--> mocked contract in Plan 56
                                                +--> Admin BFF in a later plan
```

The left path remains current behavior. The right path is not allowed to replace
it until a later plan proves parity and rollback.

## 7. Locked design decisions

### K1 — One repository, one frontend workspace

Place all JavaScript and TypeScript code under `web/`. Keep one committed
`pnpm-lock.yaml` and one root frontend `package.json` inside that directory.

Reasons:

- backend and frontend contract changes can be reviewed in one pull request;
- fixtures and verification can stay synchronized;
- the repository keeps one local-demo story;
- package ownership remains visible without creating a second release process
  prematurely.

Do not add a root-level JavaScript lockfile. Go-only workflows must remain able
to identify and skip frontend work explicitly.

### K2 — Wallet and Operator are separate applications

Create `web/apps/wallet` and `web/apps/operator` from the beginning.

They may share packages, but they must not share:

- route trees;
- browser session assumptions;
- deployment environment variables;
- role navigation;
- operator-only observability links;
- sensitive error detail;
- application-specific feature modules.

A package-boundary test must fail if one application imports source from the
other application.

### K3 — Next.js App Router and strict TypeScript are the application baseline

Both applications use Next.js App Router and TypeScript strict mode.

The framework is used for:

- routing and layouts;
- server-side composition where it reduces browser credential exposure;
- same-origin route handling;
- static and dynamic rendering where appropriate;
- controlled environment separation;
- production builds.

Do not use Next.js Server Actions as an invisible replacement for documented
backend APIs. Money-moving and operator mutations remain explicit HTTP contract
operations with observable request, idempotency, authorization, and error
semantics.

### K4 — pnpm workspaces first; no task orchestrator without evidence

Use pnpm workspaces with ordinary package scripts. Do not introduce Turborepo,
Nx, a remote cache, or a package publishing system in Plan 56.

Reconsider an orchestrator only after measured clean and incremental build time,
package count, or CI duplication demonstrates a problem and a separate decision
records the tradeoff.

### K5 — Browsers use edge owners, never domain-service shortcuts

The allowed application dependencies are:

| Application | Allowed backend owners |
|---|---|
| Wallet | Auth and Gateway public surfaces |
| Operator | Admin BFF browser/admin surface |

Ledger, Payin, Payout, Fraud, Assurance, Redis, RabbitMQ, PostgreSQL, metrics
listeners, and internal gRPC endpoints are not browser dependencies.

When the UI needs joined information, the owning edge adds a typed composition
endpoint in a backend execution plan. The browser does not fan out across
service boundaries and reconstruct authority itself.

### K6 — The embedded Admin BFF console is retained until proven replacement

Plan 56 does not change the current console's startup, routes, templates, assets,
or Node-free property.

A future migration follows these stages:

1. retain current console as primary;
2. add modern console read-only views;
3. prove data and role parity;
4. migrate one mutation domain at a time;
5. preserve rollback and emergency access;
6. remove old routes only after explicit retirement evidence.

Visual preference is not sufficient evidence for removal.

### K7 — Sensitive sessions use server-managed, unreadable cookies

The target browser model uses Secure, HttpOnly, appropriately scoped cookies.
JavaScript must not receive refresh tokens, operator session secrets, or CSRF
secrets that are meant to remain server-only.

The exact Auth integration may use a Wallet web-server session facade or a
reviewed same-origin backend route layer. The Operator application uses Admin
BFF's server-side session contract rather than copying internal credentials.

Forbidden storage includes:

- `localStorage`;
- `sessionStorage`;
- IndexedDB;
- query strings;
- URL fragments;
- readable non-HttpOnly cookies;
- persisted client-state stores;
- analytics payloads.

### K8 — Money is exact from transport to presentation

The frontend domain representation is:

```ts
type CurrencyCode = string;

type Money = Readonly<{
  amountMinor: bigint;
  currency: CurrencyCode;
}>;
```

Transport contracts may encode `amount_minor` as a decimal string or a safe
integer according to the canonical API contract. The API mapper converts it to
`bigint` after validation.

Rules:

- no `parseFloat` for money;
- no multiplication by powers of ten using floating point;
- no implicit currency defaults;
- no arithmetic across different currencies;
- no business rounding in components;
- no fee calculation in the browser;
- no locale-formatted string is parsed back into domain money;
- formatting is centralized in `packages/money`;
- tests cover minimum, zero, large, negative-when-allowed, invalid, and currency
  mismatch cases.

### K9 — Status is multidimensional and backend-owned

A transaction must not be flattened into one invented `SUCCESS` flag when the
backend exposes separate truths.

The frontend domain may represent:

```ts
type TransactionTruth = Readonly<{
  workflowStatus: WorkflowStatus;
  ledgerStatus: LedgerStatus;
  deliveryStatus?: DeliveryStatus;
  reconciliationStatus?: ReconciliationStatus;
}>;
```

Status values come from canonical contracts. Components may map known values to
labels and explanations, but may not reinterpret an unknown value as success.
Unknown values render an explicit safe fallback and create bounded diagnostic
evidence without exposing payload data.

### K10 — Transport DTOs never become presentation models directly

Use this boundary:

```text
HTTP bytes
    -> runtime schema validation
    -> transport DTO
    -> explicit domain mapper
    -> domain object
    -> view model
    -> component
```

Generated OpenAPI types, when available, remain transport types. A backend field
rename or optional field addition should be absorbed at the mapper boundary
rather than spread across the component tree.

Validation failures must produce a controlled `ContractMismatchError` with:

- contract or operation ID;
- request correlation ID when safe;
- bounded reason code;
- no token, personal data, document content, or full response body.

### K11 — TanStack Query owns server state; local state stays local

Use TanStack Query for remote data, caching, retry control, invalidation, and
background refresh. Use React component state for local interaction. Zustand may
be used only for small cross-component UI state that is not authoritative server
state.

Do not place accounts, balances, transaction truth, operator permissions, or
backend action eligibility into a long-lived client store as a second source of
truth.

Retry rules are operation-specific:

- safe read operations may use bounded automatic retry for eligible dependency
  failures;
- money-moving and operator mutations do not retry automatically unless the
  canonical contract defines idempotency and the implementation preserves the
  same idempotency key;
- validation, authentication, authorization, KYC, and business rejections are
  not network retries;
- UI copy must distinguish retryable unavailability from permanent rejection.

### K12 — React Hook Form and Zod define form boundaries

Use React Hook Form for form state and Zod for client-side shape validation.
Client validation improves feedback but never replaces backend validation.

Every form must provide:

- associated labels;
- field-level and form-level errors;
- keyboard submission behavior;
- disabled and submitting states;
- duplicate-submit protection;
- preservation rules after retryable failure;
- explicit confirmation for sensitive actions;
- no logging of field values containing credentials, KYC data, or personal data.

### K13 — The design system is semantic, accessible, and repository-owned

Use Tailwind CSS and repository-owned shadcn/ui-derived source components as the
starting primitive set. Components are reviewed and committed; the application
does not depend on a runtime component CDN.

The shared UI package must include or define patterns for:

- money and signed amount display;
- status and severity badges with text and icon;
- identifiers with copy behavior;
- workflow/evidence timelines;
- ledger entry tables;
- data tables and pagination;
- filters;
- form controls;
- dialogs and destructive confirmations;
- skeleton, loading, empty, partial, stale, and error states;
- banners for mocked, degraded, paused, or uncertain behavior;
- accessible navigation and skip links.

Color is supplementary. Every state also has text and, where useful, an icon.

### K14 — Dates, time zones, identifiers, and locale are centralized

Use ISO 8601 timestamps at the transport boundary. Parse and format dates through
one shared module.

Rules:

- retain the original timestamp and offset when evidence requires it;
- show an explicit time zone in evidence and operator views;
- do not use browser-relative phrases as the only timestamp for financial or
  audit evidence;
- test daylight-saving transitions even if the default demo environment uses a
  zone without them;
- never truncate identifiers in copy-to-clipboard values;
- visually truncated IDs must expose the full value accessibly;
- request, trace, event, transaction, account, and idempotency identifiers remain
  distinct types or branded strings where practical.

### K15 — Final financial state is never optimistic

The UI may optimistically update nonfinancial preferences when a future contract
explicitly permits it. It must not optimistically:

- reduce or increase a balance;
- mark a posting as final;
- mark a withdrawal as failed after a timeout;
- mark a reconciliation item as resolved;
- mark a maker/checker action as approved;
- claim that an event was delivered.

After a mutation, the UI displays the authoritative returned state and then
revalidates the relevant query according to the contract.

### K16 — Mocked data is explicit and cannot silently reach production

Plan 56 uses Mock Service Worker for deterministic development and tests.

Every mock environment shows a visible `Mock data` or `Fixture mode` indicator.
Production builds fail if fixture mode, fixture credentials, unrestricted test
controls, or local-only service-worker registration is enabled.

Fixtures use synthetic values only. They contain no real personal data, access
tokens, refresh tokens, vendor secrets, internal tokens, or copied production
payloads.

### K17 — Environment variables are validated and classified

Add a typed environment module per application.

Classify each variable as:

- public build-time;
- public runtime;
- server-only runtime;
- test-only;
- local-only.

Only variables deliberately prefixed for browser exposure may enter browser
bundles. A CI scan verifies that known secret variable names and test credentials
are absent from built static assets.

Environment validation fails startup with the variable name and classification,
but not its secret value.

### K18 — Testing uses deterministic layers

Use:

- unit tests for domain, money, mapping, error, and formatting logic;
- component tests with Testing Library for semantic components and forms;
- Mock Service Worker for integration behavior;
- Playwright for application-shell and critical reference-screen behavior;
- automated accessibility checks plus manual keyboard review;
- production-build smoke tests;
- boundary tests for package imports and environment leakage.

Snapshot tests are allowed only for bounded stable markup or serialized contract
fixtures. They do not replace semantic assertions and cannot auto-accept money,
status, permission, or error changes.

### K19 — Supply-chain changes are pinned and reviewable

Plan 56 must add:

- exact package-manager pinning;
- a committed lockfile;
- lockfile integrity verification;
- dependency-license review compatible with Apache-2.0 distribution;
- vulnerability scanning with an explicit severity policy;
- automated dependency update configuration grouped by risk;
- no lifecycle script execution in CI unless reviewed and required;
- no remote runtime fonts, analytics, or scripts by default.

A vulnerability suppression names the package, advisory, exposure analysis,
owner, expiry, and removal condition.

### K20 — Wallet and Operator deployments remain separable

Even if both applications run on one developer machine, they must produce
separate build artifacts and accept separate origins and environment settings.

The target deployment assumes distinct origins or equivalent isolation so that:

- Wallet cookies are not sent to Operator unnecessarily;
- Operator security headers may be stricter;
- operator-only links and bundles are not exposed through Wallet;
- one application can roll back without rebuilding the other;
- public caching policy cannot affect operator pages.

Plan 56 does not select a cloud provider or claim a production deployment.

### K21 — Polling is the initial refresh mechanism

Do not introduce WebSocket or Server-Sent Events in the foundation.

Future live screens begin with bounded polling or explicit refresh where the
contract permits it. Realtime transport requires a measured UX or operational
need, event authorization rules, replay behavior, connection limits, and a
separate implementation decision.

### K22 — Observability is bounded and privacy-safe

Add frontend telemetry seams, not a third-party analytics product.

Allowed diagnostic fields include bounded values such as:

- application;
- route template;
- operation ID;
- result class;
- controlled error code;
- build version;
- request ID when already safe for the audience.

Forbidden telemetry fields include:

- raw route parameters containing user or transaction identifiers;
- names, emails, phone numbers, document identifiers, or KYC content;
- tokens, cookies, authorization headers, CSRF values, or idempotency keys;
- full request or response bodies;
- account balances or transaction amounts as metric labels.

The public Wallet surface must not receive internal Grafana, Tempo, Loki,
RabbitMQ, or database URLs. Operator deep links are a future reviewed feature.

### K23 — Documentation distinguishes current, target, fixture, and future

Plan 56 completion may document:

- the workspace as current;
- the application shells as current;
- the reference fixture screen as fixture-backed;
- the browser boundary and package rules as current engineering constraints.

It may not document live Wallet journeys, modern operator replacement, or
Failure Lab controls as current.

## 8. Proposed workspace detail

```text
web/
├── apps/
│   ├── wallet/
│   │   ├── app/
│   │   │   ├── layout.tsx
│   │   │   ├── page.tsx
│   │   │   ├── reference/
│   │   │   │   └── transaction/page.tsx
│   │   │   └── error.tsx
│   │   ├── components/
│   │   ├── features/
│   │   ├── lib/
│   │   ├── public/
│   │   ├── tests/
│   │   ├── next.config.ts
│   │   ├── package.json
│   │   └── tsconfig.json
│   └── operator/
│       ├── app/
│       ├── components/
│       ├── features/
│       ├── lib/
│       ├── public/
│       ├── tests/
│       ├── next.config.ts
│       ├── package.json
│       └── tsconfig.json
├── packages/
│   ├── api-client/
│   │   ├── src/
│   │   │   ├── auth/
│   │   │   ├── gateway/
│   │   │   ├── admin/
│   │   │   ├── errors/
│   │   │   ├── transport/
│   │   │   └── index.ts
│   │   ├── testdata/
│   │   └── package.json
│   ├── config/
│   │   ├── eslint/
│   │   ├── typescript/
│   │   ├── vitest/
│   │   └── package.json
│   ├── domain/
│   │   ├── src/
│   │   │   ├── identifiers.ts
│   │   │   ├── status.ts
│   │   │   ├── transaction.ts
│   │   │   └── index.ts
│   │   └── package.json
│   ├── money/
│   │   ├── src/
│   │   ├── tests/
│   │   └── package.json
│   ├── testing/
│   │   ├── fixtures/
│   │   ├── msw/
│   │   ├── playwright/
│   │   └── package.json
│   └── ui/
│       ├── src/
│       │   ├── primitives/
│       │   ├── financial/
│       │   ├── feedback/
│       │   └── index.ts
│       ├── stories/
│       ├── tests/
│       └── package.json
├── scripts/
│   ├── check-boundaries.mjs
│   ├── check-client-bundle.mjs
│   └── verify-clean-install.mjs
├── package.json
├── pnpm-lock.yaml
├── pnpm-workspace.yaml
├── tsconfig.base.json
└── README.md
```

Exact generated framework files may differ. The ownership and dependency
boundaries are locked; incidental file placement can change when the selected
framework version requires it.

## 9. Package dependency rules

Allowed dependency direction:

```text
apps/wallet   ----+
                  +--> packages/ui
apps/operator ----+--> packages/domain
                  +--> packages/money
                  +--> packages/api-client
                  +--> packages/testing (test code only)
                  +--> packages/config (tooling only)

packages/ui --------> packages/domain, packages/money
packages/api-client -> packages/domain, packages/money
packages/domain ----> packages/money
```

Forbidden dependency direction:

- shared packages importing application code;
- Wallet importing Operator;
- Operator importing Wallet;
- domain importing React, Next.js, query libraries, or browser APIs;
- money importing UI or transport code;
- production code importing `packages/testing`;
- UI making HTTP requests directly;
- application components parsing raw transport DTOs.

The boundary checker must operate from source paths and package metadata. A
TypeScript path alias alone is not a boundary control.

## 10. Execution tasks

Execute T0 → T1 → T2 → T3 → T4 → T5 → T6 → T7 → T8 → T9 → T10.

T3 and T4 may be developed in parallel after T2. T5 and T6 may be developed in
parallel after the package boundaries and domain conventions are green. T8
cannot be accepted until both application shells build. T10 is the only task
that marks the plan complete.

### T0 — Re-inventory the live repository and record the baseline

**Work**

1. Recheck the nine service entrypoints, public listeners, admin listeners, and
   local Compose topology.
2. Enumerate current Admin BFF HTML routes, htmx endpoints, templates, assets,
   session cookies, CSRF behavior, role checks, and audit behavior.
3. Enumerate Auth and Gateway routes that future Wallet plans are expected to
   consume, but do not freeze them as canonical contracts.
4. Recheck plan 52 status and identify which operations, errors, and examples
   are canonical, pending, or intentionally excluded.
5. Confirm that no `web/`, root JavaScript lockfile, or frontend CI job has been
   introduced since this plan was written.
6. Measure baseline repository commands and record wall-clock time for:
   `make docs-check`, `make test`, relevant vet/lint jobs, and `make verify-full`.
7. Select the exact supported Node.js, pnpm, Next.js, React, TypeScript, test,
   lint, and formatting versions after reviewing official compatibility and
   release support.
8. Record package licenses, install scripts, native dependencies, and known
   vulnerability posture before committing the lockfile.
9. Record supported local developer platforms and whether Docker-only frontend
   development is required or optional.
10. Store the baseline evidence in a Plan 56 result section or a small checked-in
    supporting artifact referenced by this plan.

**Required tests/checks**

- all inventoried current Admin BFF browser routes remain reachable using the
  existing admin E2E setup;
- no current console mutation is omitted from the inventory;
- all selected runtime/tool versions are exact and mutually compatible;
- dependency licenses are reviewable and distributable with the repository;
- no install step requires a secret or an unpinned remote script;
- `make docs-check` and `git diff --check` pass before implementation begins.

**Definition of done:** the implementation starts from a reviewed current-state
map, exact toolchain decision, known contract status, and measured verification
baseline rather than assumptions from this document.

### Result

_Pending implementation._

### T1 — Lock frontend product, security, and migration contracts

**Work**

1. Add `web/README.md` describing purpose, current limits, local commands,
   application boundaries, fixture mode, and links back to this plan.
2. Record the final application-origin and same-origin proxy design for Wallet
   and Operator.
3. Record the session target for each application, including cookie ownership,
   logout, refresh, expiry, CSRF, and no-browser-readable-token rules.
4. Record exact allowed backend owners per application.
5. Record current Admin BFF console preservation and future retirement gates.
6. Record the money, status, error, identifier, timestamp, and unknown-value
   frontend domain conventions.
7. Record mocked-versus-live data labeling rules.
8. Record environment classifications and required security headers.
9. Record package dependency rules and ownership.
10. Add a concise frontend section to the project guide only after the workspace
    exists; before that, keep target details in this plan.

**Required tests/checks**

- a reviewer can determine which backend each browser may call without reading
  application code;
- a reviewer can determine where tokens and cookies may exist;
- no design requires direct browser access to an internal listener;
- no design weakens current Admin BFF authorization, CSRF, audit, or
  maker/checker behavior;
- money and status ownership are explicit;
- all target statements remain labeled as target;
- documentation links and anchors pass `make docs-check`.

**Definition of done:** the browser trust boundary, session model, migration
safety, domain vocabulary, and documentation truth rules are explicit enough to
review before framework code expands.

### Result

_Pending implementation._

### T2 — Bootstrap the pinned workspace and application builds

**Work**

1. Create `web/package.json`, `pnpm-workspace.yaml`, the committed lockfile, and
   the exact package-manager pin.
2. Add the exact Node version file selected in T0.
3. Create Wallet and Operator Next.js applications with App Router and strict
   TypeScript.
4. Create shared `config`, `ui`, `domain`, `money`, `api-client`, and `testing`
   packages with explicit exports.
5. Configure package scripts for clean install, format check, lint, typecheck,
   unit/component tests, builds, and Playwright.
6. Configure TypeScript project settings so application code cannot rely on
   undeclared globals or unsafe implicit `any`.
7. Configure import aliases without hiding package ownership.
8. Add a boundary-checking script for allowed dependency direction.
9. Add application health/reference pages that contain no live backend claim.
10. Confirm Wallet and Operator produce separate production artifacts.

**Required tests/checks**

- `pnpm install --frozen-lockfile` succeeds from an empty package store;
- a second frozen install produces no lockfile or generated-source diff;
- Wallet and Operator build independently;
- TypeScript strict mode is enabled for every application and package;
- an intentional cross-application import fails the boundary test;
- an intentional production import from `packages/testing` fails;
- no application requires the other application's environment variables;
- built artifacts do not include fixture credentials or known secret names;
- Go build and test commands still work without starting Node;
- `git diff --check` passes.

**Definition of done:** a fresh checkout can deterministically install, check,
test, and independently build two empty but correctly isolated applications.

### Result

_Pending implementation._

### T3 — Build shared configuration and repository-owned quality commands

**Work**

1. Add shared ESLint configuration with TypeScript, React, hooks, accessibility,
   import-boundary, and security-relevant rules.
2. Add formatting configuration and a format-check command that does not mutate
   files in CI.
3. Add shared Vitest and Testing Library setup.
4. Add Playwright configuration with separate Wallet and Operator projects.
5. Add deterministic time, locale, reduced-motion, and network defaults for
   tests.
6. Add environment-schema validation per application.
7. Add scripts that scan client bundles for forbidden server-only environment
   names and fixture-only markers.
8. Add test-output and coverage directories to `.gitignore` without broad rules
   that hide source or contract files.
9. Define a documented coverage policy focused on critical domain logic rather
   than one repository-wide vanity percentage.
10. Add package scripts that are composable from Make and CI.

**Required tests/checks**

- lint catches an unsafe `any`, missing hook dependency, inaccessible control,
  and forbidden import in synthetic fixtures;
- format check fails on a deliberately malformed fixture and leaves the source
  unchanged;
- environment validation rejects missing, malformed, unknown, and browser-
  exposed secret variables;
- test time and locale are deterministic;
- Playwright can start each production-like application independently;
- bundle scanning detects a planted forbidden marker;
- normal browser bundles pass the scan;
- no test configuration requires internet access.

**Definition of done:** every frontend package uses one reviewed quality baseline,
and CI failures are deterministic, actionable, and safe to print.

### Result

_Pending implementation._

### T4 — Implement exact financial and evidence domain primitives

**Work**

1. Implement `Money`, currency, and minor-unit constructors in
   `packages/money`.
2. Implement safe parsing from canonical string and safe-integer transport
   forms.
3. Implement exact addition, subtraction, comparison, sign, and same-currency
   guards needed for display verification.
4. Implement locale-aware formatting without changing the stored amount.
5. Implement branded or otherwise distinct identifiers for request, trace,
   transaction, event, account, and idempotency references.
6. Implement timestamp parsing and explicit-zone formatting.
7. Implement workflow, ledger, delivery, reconciliation, risk, and severity
   status domains with explicit unknown handling.
8. Implement a transaction-evidence domain model that can represent debit and
   credit entries without claiming a backend contract.
9. Implement a pure balanced-entry verification helper used only to explain
   fixture evidence; it must not become an alternative Ledger authority.
10. Document when a frontend assertion is educational presentation versus
    authoritative backend proof.

**Required tests**

- parse and format zero, positive, negative-when-allowed, and large minor-unit
  values exactly;
- reject decimal points, exponents, whitespace ambiguity, unsafe numbers,
  missing currency, unsupported input shapes, and mixed-currency arithmetic;
- formatting never changes the domain value;
- full identifier values survive visual truncation and copy operations;
- invalid timestamps fail closed;
- explicit time-zone output is deterministic;
- every known status maps to text and icon metadata;
- unknown status does not map to success;
- balanced and unbalanced fixture entry sets are distinguished exactly;
- property-based or generated tests cover arithmetic edge cases within the
  supported domain.

**Definition of done:** components can display exact financial and evidence data
without floating-point arithmetic, ambiguous identifiers, implicit time zones,
or invented success semantics.

### Result

_Pending implementation._

### T5 — Build the semantic design-system foundation

**Work**

1. Configure Tailwind CSS and the repository-owned component primitive baseline.
2. Add typography, spacing, focus, motion, border, elevation, and numeric-display
   conventions.
3. Add accessible application shell, navigation, skip link, page header, and
   content layout primitives.
4. Add `MoneyText`, `SignedMoney`, and amount-alignment patterns.
5. Add status, severity, risk, KYC, mocked-data, degraded, paused, and uncertain
   indicators with text and icon.
6. Add full-identifier, copy-button, and evidence-reference components.
7. Add transaction timeline and ledger-entry table components.
8. Add loading, skeleton, empty, partial, stale, dependency-error, contract-
   mismatch, authorization, and retryable-error patterns.
9. Add form controls, validation summaries, dialogs, and explicit confirmation
   patterns.
10. Add a component catalog or Storybook-equivalent development surface that is
    local-only and not required at application runtime.
11. Add responsive behavior for narrow mobile-width Wallet views and dense
    desktop Operator views.
12. Add dark-mode compatibility only if it does not delay semantic contrast and
    accessibility; otherwise document it as deferred rather than shipping an
    incomplete mode.

**Required tests/checks**

- all interactive components are keyboard reachable and visibly focused;
- dialogs trap and restore focus correctly;
- status is understandable without color;
- money columns align and use tabular numerals;
- copied identifiers retain full values;
- tables have semantic headings and accessible captions or labels;
- loading and skeleton states do not announce misleading final values;
- error summaries link to invalid fields;
- 360-pixel Wallet and common desktop Operator viewports have no unintended
  horizontal overflow;
- reduced-motion preferences are respected;
- automated accessibility checks report no serious or critical violations on
  the catalog's critical components;
- component tests do not depend on network or current wall-clock time.

**Definition of done:** product teams can construct financial screens from
semantic, accessible, tested components instead of duplicating ad hoc markup and
status meaning.

### Result

_Pending implementation._

### T6 — Implement the API boundary, errors, fixtures, and mocks

**Work**

1. Add a transport layer that accepts base URL, timeout, request ID propagation,
   credentials policy, and operation metadata without exposing server secrets.
2. Add runtime schemas for the small Plan 56 fixture contract.
3. Add explicit transport-to-domain mappers.
4. Add normalized errors for validation, authentication, authorization,
   business rejection, idempotency conflict, rate limit, dependency failure,
   timeout, contract mismatch, and unknown server failure.
5. Ensure human-readable backend messages are presentation text, not parser
   control flow.
6. Add bounded retry metadata but do not automatically retry mutations.
7. Add deterministic synthetic fixtures for a transaction with balanced debit
   and credit evidence, pending/uncertain state, empty state, authorization
   failure, dependency failure, and contract mismatch.
8. Add Mock Service Worker handlers for Wallet and Operator fixture surfaces.
9. Add a visible fixture-mode contract and disable fixture mode in production.
10. When plan 52 artifacts exist, add a documented generation seam but do not
    make generated DTOs the frontend domain model.
11. Add tolerant-reader tests for unknown optional response fields and strict
    validation for required meaning.

**Required tests**

- successful transport data maps to the expected domain object;
- unknown optional fields do not break a tolerant mapper;
- missing or malformed required money, status, timestamp, or identifier fields
  fail without rendering authoritative state;
- each normalized error maps to the correct retry and presentation class;
- a mutation is not automatically retried;
- request IDs are propagated when supplied and safely surfaced when returned;
- fixture handlers are deterministic across runs;
- fixture values contain no personal data or secrets;
- fixture mode is visibly labeled in development and test;
- a production build with fixture mode enabled fails;
- full raw response bodies are not logged on validation failure.

**Definition of done:** both applications can consume a controlled typed boundary
and exercise success and failure behavior without guessing live backend schemas
or coupling components to raw JSON.

### Result

_Pending implementation._

### T7 — Build the Wallet and Operator application shells

**Work**

1. Build a Wallet shell with product branding, primary navigation, responsive
   content layout, fixture-mode indicator, global error boundary, not-found
   state, and accessibility landmarks.
2. Build an Operator shell with distinct visual identity, denser navigation,
   operator-context placeholder, security notice area, fixture-mode indicator,
   global error boundary, and not-found state.
3. Add route groups that reserve future feature ownership without implementing
   live journeys.
4. Add application metadata and clear educational/non-production wording.
5. Add security-header configuration appropriate to each shell.
6. Add no-store behavior to placeholder routes that model authenticated or
   sensitive content.
7. Add server-only environment access and ensure client components receive only
   explicitly safe configuration.
8. Add separate build/version identifiers for Wallet and Operator.
9. Preserve a path back to current documentation and clearly label the reference
   screen as fixture-backed.
10. Do not add links from current operator documentation that imply the new shell
    replaces the embedded console.

**Required tests/checks**

- Wallet and Operator render independent navigation and identity;
- accessibility landmarks and skip links work;
- unknown routes render controlled not-found pages;
- global render errors show bounded recovery information and no stack trace in
  production mode;
- sensitive placeholder routes return no-store headers;
- application security headers pass the repository's expected policy checks;
- server-only values are absent from client bundles and rendered HTML;
- fixture mode is impossible to miss;
- each application can be built, started, and smoke-tested while the other is
  stopped;
- current Admin BFF console tests remain unchanged and green.

**Definition of done:** the repository has two clearly separated, secure-by-
default, accessible application shells without falsely presenting mock routes as
live product behavior.

### Result

_Pending implementation._

### T8 — Deliver the fixture-backed transaction evidence reference screen

**Work**

1. Add a Wallet reference route such as `/reference/transaction` that consumes
   only the reviewed fixture contract.
2. Show transaction identity, amount, workflow status, ledger status, delivery
   status, reconciliation status, timestamps, and request/evidence identifiers.
3. Show balanced debit and credit entries using the shared table.
4. Explain in nearby prose that workflow truth and Ledger truth answer different
   questions.
5. Add fixture variants for settled, processing, uncertain, failed, empty,
   dependency-unavailable, unauthorized, and contract-mismatch states.
6. Add a safe copy interaction for full identifiers.
7. Add refresh behavior against the mock handler to prove query invalidation and
   loading transitions.
8. Add a developer-only fixture selector outside production builds.
9. Add a compact Operator reference route that proves the same shared components
   can render denser evidence without importing Wallet application code.
10. Link the screen only from `web/README.md` and local fixture navigation until
    a live Wallet plan completes.

**Required tests/checks**

- the settled fixture shows balanced entries and all truth dimensions;
- the uncertain fixture does not use failure or safe-to-retry wording;
- the failed fixture does not imply that missing Ledger evidence was reversed;
- unknown statuses render an explicit unknown state;
- dependency failure preserves the last safe screen state only when query-cache
  rules permit it and labels it stale;
- unauthorized state exposes no transaction evidence;
- contract mismatch exposes no raw payload;
- copy interactions return full identifiers;
- screen-reader output names amount, currency, entry direction, status, and
  timestamp meaning;
- keyboard and automated accessibility checks pass;
- no test performs floating-point money arithmetic;
- production builds contain no fixture selector or mock service worker.

**Definition of done:** one visible reference screen proves the shared domain,
API, query, UI, accessibility, failure, and build boundaries while remaining
honestly fixture-backed.

### Result

_Pending implementation._

### T9 — Integrate frontend checks with Make, CI, and local development

**Work**

1. Add Make targets with clear help text:

   ```text
   make web-install
   make web-format-check
   make web-lint
   make web-typecheck
   make web-test
   make web-build
   make web-e2e
   make web-check
   ```

2. Make `web-check` run every non-browser frontend gate without silently
   installing dependencies or modifying the lockfile.
3. Add a dedicated frontend CI job using the exact Node and pnpm versions.
4. Cache the pnpm store by lockfile hash without caching built output as a source
   of truth.
5. Run Wallet and Operator production builds in CI.
6. Run Playwright smoke/reference tests in an isolated job with retained failure
   artifacts that contain no secrets.
7. Add `web-check` to the appropriate repository pull-request gate.
8. Add the complete frontend gate to `make verify-full` at a position that fails
   before destructive Docker-volume reset when possible.
9. Preserve all existing Go, documentation, protobuf, smoke, business, admin,
   and chaos commands.
10. Add optional local development commands for starting Wallet and Operator
    without changing `make docker-up` semantics.
11. Decide through evidence whether Compose profiles for frontend production
    builds belong in Plan 56 or a follow-up packaging plan; do not add them only
    for symmetry.

**Required tests/checks**

- every Make target returns a nonzero exit status on a synthetic failure;
- CI uses frozen lockfile installation;
- CI performs no implicit dependency upgrade;
- frontend checks run without access to production secrets;
- Playwright failure artifacts are scanned for cookies, authorization headers,
  and fixture credentials;
- `make verify-full` still begins backend business verification from clean
  Docker volumes;
- removing an existing backend gate causes a repository policy test or review
  check to fail;
- Go-only local commands remain documented and usable;
- measured CI and local timings are recorded and compared with the T0 baseline.

**Definition of done:** frontend quality is a first-class repository gate, not a
separate best-effort workflow, and it does not weaken or hide existing backend
verification.

### Result

_Pending implementation._

### T10 — Documentation, security evidence, and final acceptance

**Work**

1. Update the roadmap and active-plan indexes to include Plan 56 while it is
   active.
2. Update the repository layout and requirements only after the workspace and
   commands exist.
3. Add current frontend engineering guidance to the project guide.
4. Document local setup, fixture mode, application boundaries, package ownership,
   and troubleshooting in `web/README.md`.
5. Document that the existing Admin BFF console remains current and retained.
6. Run dependency-license, vulnerability, bundle-secret, accessibility,
   keyboard, responsive, clean-install, clean-build, and production-smoke review.
7. Run controlled failures:
   - broken lockfile integrity;
   - forbidden cross-application import;
   - browser-exposed server secret;
   - floating-point money helper;
   - unknown status;
   - malformed required API field;
   - production fixture-mode enablement;
   - inaccessible status indicator;
   - automatic mutation retry;
   - client call to a forbidden internal owner.
8. Record artifact counts, package graph, direct dependency count, bundle sizes,
   test counts, accessibility findings, install/build timings, and known
   deferred risks.
9. Record the exact activation criteria for the read-only Wallet follow-up plan.
10. Move Plan 56 to the archive only after every acceptance item has evidence and
    all index/status wording is updated.

**Required final gate**

```text
pnpm --dir web install --frozen-lockfile
make web-format-check
make web-lint
make web-typecheck
make web-test
make web-build
make web-e2e
make docs-check
git diff --check
GOCACHE=/tmp/seev-go-cache go build ./...
GOCACHE=/tmp/seev-go-cache go vet ./...
GOCACHE=/tmp/seev-go-cache go vet -tags=integration ./...
GOCACHE=/tmp/seev-go-cache make test
GOCACHE=/tmp/seev-go-cache make lint
make proto
make proto-lint
make proto-breaking
make verify-full
```

If plan 52 contract tooling has completed when T10 runs, include its mandatory
contract targets in the final gate. Do not duplicate or weaken plan 52's own
merge-base compatibility policy here.

**Definition of done:** the repository has a reproducible and reviewed frontend
platform, two isolated application shells, exact financial domain primitives,
semantic accessible UI, safe fixture integration, and mandatory quality gates;
all documentation states clearly that live Wallet journeys and modern operator
replacement remain future work.

### Result

_Pending implementation._

## 11. Required acceptance matrix

| Area | Acceptance evidence |
|---|---|
| Repository | `web/` workspace is present; no unintended root lockfile; clean diff |
| Toolchain | exact Node and pnpm versions; frozen install; committed lockfile |
| Applications | Wallet and Operator build and start independently |
| Boundaries | import checks and browser-owner tests reject forbidden paths |
| Existing console | current Admin BFF HTML/htmx admin E2E remains green |
| Money | exact parsing/arithmetic/formatting tests; no floating-point path |
| Status | multidimensional known/unknown behavior tests |
| API | runtime validation, mapping, normalized errors, tolerant-reader tests |
| Fixtures | deterministic synthetic data; visible labeling; production exclusion |
| UI | semantic components, keyboard behavior, responsive behavior |
| Accessibility | automated critical-page checks plus manual keyboard evidence |
| Security | cookie/session design, bundle scan, headers, no sensitive telemetry |
| Supply chain | license review, vulnerability policy, install-script review |
| CI | frontend checks are mandatory and retain safe diagnostics |
| Documentation | current/target/fixture wording and indexes are correct |
| Final verification | full repository gate passes from a clean state |

## 12. Activation gate for the next frontend plan

The read-only Wallet execution plan may begin only after all of the following are
true:

1. Plan 56 is complete and archived.
2. Wallet and Operator builds are independently green in CI.
3. Money, status, identifier, timestamp, and error primitives are stable enough
   for a first live vertical slice.
4. The Auth and Gateway operations needed for that slice are canonical under
   plan 52, or plan 52 has an explicitly approved sequencing decision that
   provides equivalent reviewed contract evidence.
5. The live browser session and CSRF design has passed security review.
6. The Wallet vertical slice has a named backend owner for each operation.
7. The product journey does not require direct browser access to an internal
   service.
8. The fixture screen remains available as a deterministic test reference but is
   not confused with live data.
9. The next plan names its exact first journey and anti-scope rather than trying
   to implement all Wallet features at once.

The recommended first live vertical slice is:

```text
authenticated user
    -> account summary
    -> transaction list
    -> transaction evidence detail
```

It is read-only. Top-up, transfer, and withdrawal mutations remain a later plan
because they require stronger idempotency, retry, confirmation, uncertain-state,
and authoritative-refresh acceptance criteria.

## 13. Rollback strategy

Plan 56 adds a new workspace and CI surface without changing current backend
runtime behavior.

Rollback rules:

- a broken Wallet or Operator shell can be removed from a deployment without
  changing Go service containers;
- current Admin BFF templates remain available throughout the plan;
- frontend CI may be reverted only with an explicit repository decision and
  must not be silently skipped after the plan is complete;
- lockfile or package regressions are reverted as one reviewed workspace change;
- no database migration is created by this plan;
- no money or operator action depends on the fixture reference screen;
- fixture mode cannot become an operational fallback for live data;
- backend routes are not modified solely to keep a partially completed frontend
  build alive.

## 14. Risks and mitigations

### R1 — Frontend work freezes accidental HTTP behavior

**Risk:** handwritten types encode an inconsistent handler response as the
permanent contract.

**Mitigation:** plan 52 owns canonical HTTP behavior. Plan 56 uses clearly
labeled temporary fixtures and explicit mapping boundaries.

### R2 — Two applications create premature complexity

**Risk:** Wallet and Operator duplicate configuration and slow one developer.

**Mitigation:** share tooling and semantic packages, but keep application trust
boundaries separate. Do not add a task orchestrator until measured need exists.

### R3 — The modern console weakens current operator safety

**Risk:** new UI code bypasses server-rendered CSRF, role, audit, or maker/checker
controls.

**Mitigation:** Plan 56 performs no live operator mutation. The current console
remains authoritative, and future migration occurs one domain at a time.

### R4 — Money enters JavaScript as an unsafe number

**Risk:** precision loss or browser fee logic creates misleading values.

**Mitigation:** exact transport validation, `bigint` minor units, central money
package, lint/review rules, and mutation tests.

### R5 — Client state becomes a second source of truth

**Risk:** cached or optimistic values appear more final than backend facts.

**Mitigation:** server-state ownership, no optimistic financial finality,
multidimensional status, and authoritative revalidation.

### R6 — Fixtures are mistaken for live behavior

**Risk:** screenshots or documentation imply that a product journey exists.

**Mitigation:** persistent fixture-mode labeling, production exclusion, limited
navigation, and current/target documentation rules.

### R7 — JavaScript supply-chain risk expands the repository

**Risk:** compromised, abandoned, incompatible, or restrictively licensed
packages enter the build.

**Mitigation:** minimal direct dependencies, exact pins, committed lockfile,
license review, vulnerability policy, update automation, and install-script
review.

### R8 — Frontend gates make the full repository too slow

**Risk:** developers bypass checks because clean installation and browser tests
are expensive.

**Mitigation:** measure T0/T10 timings, separate fast `web-check` from browser
E2E, cache only the package store, and keep the authoritative full gate explicit.
Do not weaken correctness to meet an arbitrary duration.

### R9 — Framework server code becomes an undocumented backend

**Risk:** Next.js routes accumulate business rules, authorization, or financial
state and form a shadow service.

**Mitigation:** the web server may protect credentials, compose same-origin
requests, and map presentation concerns. Business authorization, fees, money
movement, workflow state, and operator eligibility remain in Go service owners.

### R10 — Sensitive data leaks through telemetry or test artifacts

**Risk:** browser errors, Playwright traces, screenshots, or logs capture tokens
or personal data.

**Mitigation:** synthetic fixtures, bounded logging, artifact scans, no raw body
logging, test-only credentials, and explicit retention/review rules.

### R11 — Accessibility is deferred until pages multiply

**Risk:** semantic defects become embedded in every feature.

**Mitigation:** component-level accessibility, keyboard evidence, status without
color, and a reference screen before live feature plans.

### R12 — The plan becomes a claim of production readiness

**Risk:** a polished frontend is interpreted as certification for real money.

**Mitigation:** preserve repository disclaimers, mock-only Plan 56 behavior, and
separate legal permission from technical readiness in every public entry point.

## 15. Deferred decisions

The following decisions require evidence from later plans:

- exact production hosting platform;
- whether Wallet needs a dedicated Go or Next.js browser BFF beyond same-origin
  route handling;
- whether Operator and Admin BFF share one origin or use a reviewed cross-origin
  session model;
- OpenAPI TypeScript generator selection after plan 52 artifacts exist;
- Storybook versus another component catalog if dependency review rejects it;
- dark mode as a supported feature;
- localization beyond the first documented locale;
- realtime transport;
- PWA/offline behavior;
- public demo reset and abuse controls;
- source-map publication and protected error-reporting service;
- operator trace/log deep links;
- monorepo task orchestration or remote caching;
- independent frontend release versioning.

A deferred decision must not be implemented through an incidental dependency
default.

## 16. Plan completion checklist

```text
[ ] T0 live repository baseline recorded
[ ] exact Node and pnpm versions pinned
[ ] direct dependencies and licenses reviewed
[ ] Wallet and Operator boundary decisions documented
[ ] current Admin BFF console preservation verified
[ ] `web/` workspace created
[ ] frozen clean install passes
[ ] Wallet builds independently
[ ] Operator builds independently
[ ] cross-application import test passes
[ ] strict TypeScript passes
[ ] lint and format checks pass
[ ] exact money domain tests pass
[ ] status and unknown-value tests pass
[ ] timestamp and identifier tests pass
[ ] API validation and mapping tests pass
[ ] normalized error tests pass
[ ] fixture data is synthetic and deterministic
[ ] fixture mode is visibly labeled
[ ] fixture mode is excluded from production
[ ] semantic UI component tests pass
[ ] keyboard review passes
[ ] automated accessibility review passes
[ ] responsive reference screens pass
[ ] server-only environment bundle scan passes
[ ] security-header checks pass
[ ] dependency vulnerability policy passes
[ ] frontend Make targets exist
[ ] frontend CI is mandatory
[ ] existing backend gates remain mandatory
[ ] current Admin BFF admin E2E remains green
[ ] documentation indexes are updated
[ ] current/target/fixture wording is accurate
[ ] full repository verification passes from clean state
[ ] next-plan activation criteria are recorded
[ ] Plan 56 result sections contain acceptance evidence
[ ] Plan 56 is moved to archive only after all items pass
```
