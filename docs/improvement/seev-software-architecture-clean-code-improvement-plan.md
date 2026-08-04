# Seev Software Architecture and Clean Code Improvement Plan

## Document Status

- **Repository:** `herdifirdausss/seev`
- **Review perspective:** Software Architect and Clean Code Reviewer
- **Current lifecycle:** Pre-production / engineering portfolio / learning reference
- **Primary focus:** Directory structure, modularity, Single Responsibility Principle, code complexity, duplication, dependency management, and technical debt
- **Recommended execution model:** Incremental refactoring with no large-scale rewrite

---

## 1. Executive Summary

Seev already has a strong architectural foundation. Its service ownership, deployable boundaries, database ownership rules, explicit contracts, and automated architectural checks are significantly better than those found in many portfolio or pre-production repositories.

The most important architectural risk is not uncontrolled coupling between services. The primary risk is the gradual accumulation of complexity inside individual services, especially within orchestration functions, root module objects, callback handlers, recovery flows, and shared utility packages.

The recommended strategy is therefore:

1. Prevent new technical debt from being introduced.
2. Refactor the highest-risk money-movement orchestration functions.
3. Make dependencies and capabilities explicit.
4. Split broad service modules into focused use-case components.
5. Improve internal package cohesion without adding unnecessary microservices.
6. Convert architectural quality expectations into automated CI rules.

This plan intentionally avoids a full rewrite. Existing public facades and service boundaries should remain stable while their internal implementations are improved incrementally.

---

## 2. Current Architecture Assessment

### 2.1 Strengths to Preserve

The following characteristics should be treated as architectural assets and must not be weakened during refactoring:

- Clear ownership of deployable services.
- Explicit database ownership per service.
- Communication through HTTP, gRPC, or events rather than cross-service database access.
- Automated import-boundary validation.
- Separation between service entrypoints, service internals, `internal/platform`,
  API contracts, migrations, deployment files, and documentation.
- Local interfaces around external or cross-module dependencies.
- Public root-package facades for important modules.
- Strong verification culture through unit, integration, race, contract, smoke, privacy, and chaos tests.
- Financial invariants documented close to critical code paths.
- No need to create additional microservices solely to solve internal code organization problems.

### 2.2 Main Risks

The highest-priority risks are:

- Large orchestration functions with many branches and side effects.
- Root `Module` structs carrying too many dependencies and responsibilities.
- Runtime type assertions used to discover optional capabilities.
- Flat packages containing unrelated use cases.
- Long primitive parameter lists, especially in callback handling.
- Duplicated intake-control logic across Payin and Payout.
- Composition roots having broad access to implementation subpackages.
- Generic shared packages gradually becoming dumping grounds.
- CI not enforcing complexity, duplication, or function-size limits.
- Historical task comments and roadmap references adding source-code noise.

---

## 3. Target Architecture

The desired internal architecture for every deployable service is:

```text
Deployable Service Boundary
        ↓
Thin Public Module Facade
        ↓
Use-Case-Specific Application Services
        ↓
Small Explicit Ports and Policies
        ↓
Repositories and External Adapters
```

Example for Payout:

```text
payout.Module
├── creator.Create(command)
├── callbackHandler.Handle(callback)
├── recoveryService.Resume(cutoff)
├── intakeService.Apply(command)
├── retryService.Retry(command)
└── cancellationService.Cancel(command)
```

The public `payout.Module` may remain stable for callers, but it should delegate implementation work to smaller components with focused responsibilities.

The same pattern should be applied gradually to Payin, Auth, Ledger consumers, and other modules when justified by complexity.

---

## 4. Guiding Principles

All improvements should follow these rules:

### 4.1 Preserve Business Behavior First

Before changing critical flows, create characterization tests that capture current behavior. Refactoring must not change financial semantics, idempotency behavior, callback correlation, recovery guarantees, or ledger posting rules unless explicitly approved.

### 4.2 Refactor Vertically by Use Case

Prefer packages and components organized around use cases such as:

- Top-up creation.
- Callback processing.
- Payout submission.
- Recovery.
- Intake control.
- Privacy workflows.

Avoid creating excessive horizontal layers such as `manager`, `helper`, `service`, and `util` without clear ownership.

### 4.3 Make Dependencies Explicit

Capabilities required by a use case must be visible in constructors and interfaces. Required behavior must not silently depend on whether a runtime object happens to implement an additional interface.

### 4.4 Keep Public Facades Stable

Refactor behind stable module APIs where practical. This reduces migration risk and allows changes to be delivered incrementally.

### 4.5 Share Concepts, Not Merely Similar Code

Code should move to `internal/platform` only when:

- At least two modules need it.
- The concept has the same meaning in both modules.
- The code has the same lifecycle and ownership.
- Sharing it does not couple independent business changes.

### 4.6 Use Automated Architectural Enforcement

Important rules should be executable in tests or CI rather than relying only on documentation and reviewer memory.

---

## 5. Priority Matrix

| Priority | Workstream | Risk Addressed | Expected Impact |
|---|---|---|---|
| P0 | Complexity and duplication quality gates | Uncontrolled maintainability decline | Prevents new debt immediately |
| P0 | Critical Payin/Payout orchestration refactoring | Financial-flow complexity and regression risk | High reliability and testability gain |
| P0 | Explicit dependency capabilities | Hidden runtime behavior | Predictable startup and testing |
| P1 | Thin module facades | God objects and mixed responsibilities | Better modularity and change isolation |
| P1 | Vertical internal package boundaries | Flat package coupling | Stronger compiler-enforced cohesion |
| P1 | Intake-control consolidation | Cross-module duplication | Reduced maintenance cost |
| P1 | Composition-root restrictions | Implementation leakage | Better dependency discipline |
| P2 | Shared-package cleanup | Generic utility dumping ground | Clearer ownership and discoverability |
| P2 | Comment and ADR cleanup | Documentation drift and code noise | Easier maintenance and onboarding |
| P2 | Machine-readable architecture metadata | Documentation and rule divergence | Long-term consistency |

---

# 6. Implementation Roadmap

## Phase 0 — Baseline and Safety Net

### Objective

Establish objective measurements and behavior-preserving tests before refactoring.

### Duration

Approximately 3–5 engineering days.

### Tasks

#### 0.1 Record the Existing Maintainability Baseline

Collect and store:

- Cyclomatic complexity per function.
- Cognitive complexity per function.
- Function length.
- File length.
- Number of parameters.
- Duplication blocks.
- Package dependency graph.
- Package fan-in and fan-out.
- Existing lint findings.

Recommended output:

```text
docs/architecture/reports/maintainability-baseline.md
artifacts/architecture/complexity.json
artifacts/architecture/dependencies.svg
```

#### 0.2 Identify Critical Behavioral Scenarios

Create a matrix of behavior that must remain unchanged for Payin and Payout:

- Successful request.
- Duplicate request with identical payload.
- Duplicate request with conflicting payload.
- Duplicate vendor callback.
- Callback with mismatched amount.
- Callback with mismatched currency.
- Callback from an unexpected vendor.
- Expired intent.
- Fraud-service rejection.
- Fraud-service timeout or dependency failure.
- Fee-quote rejection.
- Ledger-posting failure.
- Crash after persistence but before hold.
- Crash after hold but before enqueue.
- Crash after vendor submission but before state update.
- Recovery of every resumable state.

#### 0.3 Add Characterization Tests

Add tests around the current high-risk functions before restructuring them.

Tests should verify:

- Returned result.
- State transition.
- Repository writes.
- Ledger calls.
- Event or vendor-command enqueue behavior.
- Idempotency behavior.
- Error classification.
- Audit and evidence persistence when applicable.

### Deliverables

- Maintainability baseline report.
- Critical-flow behavior matrix.
- Characterization test suite.
- Initial dependency graph.

### Exit Criteria

- Every targeted critical flow has tests for success, duplicate, rejection, and recovery behavior.
- Complexity measurements can be regenerated with one documented command.
- The current baseline is committed before enforcement is enabled.

---

## Phase 1 — Stop Technical Debt Growth

### Objective

Introduce automated quality gates using a baseline-and-ratchet strategy.

### Duration

Approximately 1 week.

### Tasks

#### 1.1 Enable Maintainability Linters

Evaluate and configure suitable Go tooling, such as:

- `cyclop` or `gocyclo` for cyclomatic complexity.
- `gocognit` for cognitive complexity.
- `funlen` for long functions.
- `dupl` for duplication.
- `maintidx` for maintainability index.
- `revive` for style and design smells.
- `staticcheck` for correctness and code quality.
- `errcheck` for ignored errors.
- `unused` for dead code.

Do not enable all rules as hard failures immediately.

#### 1.2 Define Initial Thresholds

Recommended initial thresholds for new or modified code:

| Metric | Warning | Failure Threshold |
|---|---:|---:|
| Cyclomatic complexity | Greater than 12 | Greater than 20 |
| Cognitive complexity | Greater than 15 | Greater than 25 |
| Function length | Greater than 60 LOC | Greater than 100 LOC |
| Parameter count | Greater than 5 | Greater than 8 |
| File length | Greater than 500 LOC | Greater than 800 LOC |

Thresholds should become stricter after the first remediation cycle.

#### 1.3 Implement a Ratchet Policy

The repository should follow these rules:

- Existing debt is tracked in the baseline.
- New functions must meet the thresholds.
- Modified hotspot functions must not become worse.
- A PR should fail when it introduces a new violation.
- Existing violations should be removed incrementally.
- Any suppression must include a reason and an expiration or tracking issue.

#### 1.4 Add a Pull Request Maintainability Report

Each pull request should show:

- New complexity violations.
- Complexity changes in modified functions.
- New duplication blocks.
- New dependency-rule violations.
- New or removed lint suppressions.

### Deliverables

- Updated `.golangci.yml`.
- CI maintainability job.
- Baseline comparison script.
- Pull request quality summary.
- Documented suppression policy.

### Exit Criteria

- New high-complexity functions are blocked.
- Existing code remains buildable without mass suppressions.
- CI displays complexity and duplication changes for every pull request.

---

## Phase 2 — Improve Command and Callback Models

### Objective

Replace long primitive parameter lists and ambiguous inputs with explicit domain commands.

### Duration

Approximately 3–5 engineering days.

### Tasks

#### 2.1 Introduce a Normalized Payin Callback Model

Replace callback methods that accept many primitive values with a single immutable input model.

Example:

```go
type NormalizedPayinCallback struct {
    VendorID          string
    VendorReference   string
    InternalReference string
    Status            CallbackStatus
    Amount            Money
    ReceivedAt        time.Time
    Signature         string
    RawPayload        []byte
    Metadata          map[string]string
}
```

The exact fields should match repository semantics and must avoid exposing transport-specific DTOs to the application layer.

#### 2.2 Introduce Use-Case Commands

Create focused command objects such as:

```go
type CreateTopupCommand struct {
    IdempotencyKey string
    UserID         string
    AccountID      string
    Amount         Money
    RequestedRoute string
    Metadata       map[string]string
}
```

```go
type CreatePayoutCommand struct {
    IdempotencyKey string
    SourceAccount  string
    Destination    Destination
    Amount         Money
    RequestedRoute string
    Metadata       map[string]string
}
```

#### 2.3 Validate at the Boundary

Transport adapters should:

1. Parse HTTP, gRPC, or event input.
2. Validate syntax and required fields.
3. Convert to an application command.
4. Call the use-case service.
5. Translate typed errors to transport responses.

The application layer should validate business rules, not transport formatting.

### Deliverables

- Normalized callback type.
- Payin and Payout command types.
- Transport-to-command mapping tests.
- Removed long primitive argument lists in targeted flows.

### Exit Criteria

- No targeted callback handler has more than five direct parameters.
- Transport models do not leak into core orchestration logic.
- Commands can be constructed directly in unit tests.

---

## Phase 3 — Make Dependencies Explicit

### Objective

Eliminate hidden behavior caused by optional runtime type assertions for required business capabilities.

### Duration

Approximately 1 week.

### Tasks

#### 3.1 Inventory Runtime Capability Checks

Find all patterns such as:

```go
if capability, ok := dependency.(SomeCapability); ok {
    // optional behavior
}
```

Classify each capability as:

- Required.
- Truly optional.
- Temporary backward-compatibility behavior.
- Obsolete fallback.

#### 3.2 Introduce Explicit Ports

Examples:

```go
type CurrencyPolicy interface {
    ValidateCurrency(ctx context.Context, currency string) error
    UserCurrencyEnabled(ctx context.Context, userID, currency string) (bool, error)
}
```

```go
type FeeQuoteService interface {
    Resolve(ctx context.Context, command FeeQuoteCommand) (FeeQuote, error)
    Consume(ctx context.Context, quoteID string) error
}
```

```go
type LedgerPoster interface {
    Post(ctx context.Context, posting Posting) error
}
```

#### 3.3 Group Related Dependencies

Use dependency structs only when the grouped dependencies form a stable capability area.

Example:

```go
type TopupDependencies struct {
    Repository     TopupRepository
    CurrencyPolicy CurrencyPolicy
    FeeService     FeeQuoteService
    Ledger         LedgerPoster
    Router         TopupRouter
    VendorSessions VendorSessionService
}
```

Avoid using a dependency struct merely to hide an excessive constructor.

#### 3.4 Validate Required Capabilities at Startup

The composition root must fail fast when a required capability is missing.

Optional capabilities must be explicitly represented, for example:

```go
type OptionalRiskSignals struct {
    DeviceReputation DeviceReputationService
}
```

or through a documented no-op implementation when semantically valid.

#### 3.5 Remove Transitional Fallbacks

Every compatibility fallback should have:

- A tracking issue.
- An owner.
- Removal criteria.
- A target release or phase.

### Deliverables

- Capability inventory.
- Explicit interfaces.
- Updated constructors.
- Startup validation.
- Removed or documented runtime assertions.

### Exit Criteria

- Required behavior does not depend on concrete runtime types.
- Missing required capabilities fail during startup or wiring tests.
- Unit tests use the same explicit interfaces as production wiring.

---

## Phase 4 — Refactor Critical Payin Orchestration

### Objective

Reduce complexity in top-up creation, callback processing, financial resolution, and finalization.

### Duration

Approximately 2–3 weeks.

### Scope

Prioritize functions equivalent to:

- Top-up intent creation.
- Vendor callback handling.
- Top-up financial resolution.
- Ledger posting and finalization.

### Target Design

```text
TopupCreator.Create
├── ValidateCommand
├── VerifyIntakeAvailability
├── ResolveCurrency
├── VerifyAccountEligibility
├── ResolveRoute
├── ResolveFeeQuote
├── PersistIntent
└── CreateVendorSession
```

```text
PayinCallbackHandler.Handle
├── NormalizeCallback
├── CorrelateIntent
├── MatchExpectedValues
├── PersistEvidence
├── ResolveFinancialOutcome
├── PostLedgerEntries
└── FinalizeIntent
```

### Tasks

#### 4.1 Separate Pure Decision Logic from Side Effects

Extract pure policy components where possible:

- Callback matching.
- Status mapping.
- Expiry evaluation.
- Amount and currency comparison.
- Route eligibility.
- Fee calculation rules.

Pure logic should use table-driven unit tests.

#### 4.2 Introduce Typed Results

Examples:

```go
type CallbackMatchResult struct {
    IntentID     string
    Outcome      CallbackOutcome
    RejectReason string
}
```

```go
type TopupFinancials struct {
    Principal Money
    Fee       Money
    NetAmount Money
}
```

#### 4.3 Encapsulate State Transitions

Do not scatter raw string-status comparisons throughout handlers.

Prefer explicit transition policies:

```go
type TopupStateMachine interface {
    CanTransition(from, to TopupStatus) bool
    Apply(current Topup, event TopupEvent) (Topup, error)
}
```

A lighter switch-based implementation is acceptable if it remains centralized and fully tested.

#### 4.4 Separate Persistence Recovery from Business Decisions

Persistence retries and recovery of partially completed operations should not obscure the primary business path.

Introduce explicit recovery methods or compensating workflows where needed.

#### 4.5 Preserve Idempotency and Evidence

Refactoring must preserve:

- Duplicate callback handling.
- Callback evidence storage.
- Request-equality validation for reused idempotency keys.
- Exactly-once business effects within the documented system guarantees.
- Safe retry after process or dependency failure.

### Deliverables

- `TopupCreator`.
- `PayinCallbackHandler`.
- `CallbackMatcher`.
- `TopupFinalizer`.
- Focused tests per component.
- Updated architecture documentation.

### Exit Criteria

- No critical Payin orchestration function exceeds the agreed complexity threshold.
- Callback matching can be tested without a database or vendor dependency.
- Side-effect ordering is explicit and documented.
- Existing integration and chaos tests remain green.

---

## Phase 5 — Refactor Critical Payout Orchestration

### Objective

Split payout creation and recovery into focused application services and state-specific recovery strategies.

### Duration

Approximately 2–3 weeks.

### Target Design

```text
PayoutCreator.Create
├── ValidateCommand
├── ResolveCurrency
├── VerifySourceAccount
├── ScreenPayout
├── ResolveRoute
├── PersistRequest
├── ApplyFeeQuote
├── PlaceHold
└── EnqueueVendorCommand
```

Recovery target:

```text
PayoutRecoveryService
├── CreatedRecovery
├── HeldRecovery
├── SubmittedRecovery
└── VendorPendingRecovery
```

### Tasks

#### 5.1 Create a Dedicated Payout Creator

Move payout-creation behavior from the root module into a focused component.

The creator should coordinate policies and side effects but delegate implementation details.

#### 5.2 Extract Fraud Screening

Fraud screening should return a typed decision:

```go
type FraudDecision struct {
    Outcome FraudOutcome
    Reason  string
}
```

Dependency failure policy must be explicit:

- Fail closed.
- Fail open.
- Queue for manual review.
- Retry later.

#### 5.3 Extract Route Resolution

Vendor and network routing should be isolated from orchestration so that routing changes do not require editing the full payout flow.

#### 5.4 Create State-Specific Recovery Strategies

Each resumable state should have its own recovery implementation.

Example interface:

```go
type PayoutRecoveryStrategy interface {
    Supports(status PayoutStatus) bool
    Recover(ctx context.Context, payout Payout) error
}
```

A registry can map status to strategy and must fail fast on duplicate registrations.

#### 5.5 Make Side-Effect Ordering Explicit

Document and test the order of:

1. Request persistence.
2. Fee quote validation or consumption.
3. Balance hold.
4. Vendor-command persistence.
5. Dispatch.
6. State update.

Each crash boundary must have a documented recovery path.

### Deliverables

- `PayoutCreator`.
- Routing policy component.
- Fraud-screening port and decision model.
- Recovery strategy registry.
- Tests for every resumable status.

### Exit Criteria

- Adding a new recovery state does not require extending one large function.
- The payout creator is below the agreed complexity threshold.
- Every side-effect boundary has a test or documented recovery mechanism.
- No behavior regression is detected by integration and chaos tests.

---

## Phase 6 — Convert Root Modules into Thin Facades

### Objective

Reduce `Module` structs from god objects into stable API facades and lifecycle coordinators.

### Duration

Approximately 1–2 weeks per large module, performed incrementally.

### Candidate Modules

- Payin.
- Payout.
- Auth.

### Tasks

#### 6.1 Inventory Responsibilities

For each root module, list:

- Public operations.
- Repositories.
- External clients.
- Background jobs.
- Cryptographic capabilities.
- Routing policies.
- Privacy capabilities.
- Lifecycle responsibilities.

#### 6.2 Group by Reason to Change

Example Auth decomposition:

```text
auth.Module
├── registrationService
├── authenticationService
├── tokenService
├── kycService
├── privacyService
├── accountClosureCoordinator
└── lifecycleCoordinator
```

#### 6.3 Delegate Public Methods

Keep the public module surface stable where useful:

```go
func (m *Module) Register(ctx context.Context, cmd RegisterCommand) (...) {
    return m.registrationService.Register(ctx, cmd)
}
```

#### 6.4 Separate Background Worker Lifecycle

Workers, relays, and scheduled jobs should have explicit lifecycle interfaces:

```go
type Lifecycle interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
}
```

The module may coordinate them, but business methods should not depend on worker internals.

### Deliverables

- Responsibility inventory per module.
- Decomposed services.
- Thin root facade.
- Wiring tests.
- Lifecycle tests.

### Exit Criteria

- Each root module primarily delegates rather than implements complex workflows.
- A module field has a clear reason to exist.
- Business use cases can be unit-tested without constructing the entire module.

---

## Phase 7 — Improve Internal Package Cohesion

### Objective

Create compiler-enforced boundaries between unrelated use cases inside large services.

### Duration

Approximately 2–4 weeks, delivered package by package.

### Proposed Payin Structure

```text
services/payin/internal/
├── payin.go
├── callback/
│   ├── service.go
│   ├── matcher.go
│   ├── finalizer.go
│   └── service_test.go
├── topup/
│   ├── service.go
│   ├── command.go
│   ├── financials.go
│   └── service_test.go
├── intake/
│   ├── service.go
│   └── repository.go
├── routing/
│   ├── policy.go
│   └── policy_test.go
├── repository/
├── transport/
│   └── grpc/
└── internaltest/
```

### Proposed Payout Structure

```text
services/payout/internal/
├── payout.go
├── create/
│   ├── service.go
│   ├── command.go
│   └── service_test.go
├── callback/
├── recovery/
│   ├── service.go
│   ├── registry.go
│   └── strategies/
├── intake/
├── routing/
├── repository/
└── transport/
    └── grpc/
```

### Rules

- Root package exposes the stable facade.
- Subpackages should not import the root package if that creates a cycle.
- Shared models should be placed according to ownership, not convenience.
- Avoid an `entities` package containing unrelated domain objects.
- Avoid over-segmentation of files that contain only trivial wrappers.

### Deliverables

- Package ownership map.
- Migrated Payin and Payout use-case packages.
- Updated boundary tests.
- Updated dependency graph.

### Exit Criteria

- Callback code cannot directly access unrelated top-up internals without an explicit interface.
- Package dependencies form an acyclic and understandable graph.
- A new engineer can identify a use case’s implementation from the directory structure.

---

## Phase 8 — Consolidate Intake-Control Duplication

### Objective

Remove duplicated Payin and Payout intake-control mechanics without coupling their business policies.

### Duration

Approximately 3–5 engineering days.

### Tasks

#### 8.1 Compare Semantics Before Extraction

Confirm that both modules share the same meaning for:

- Open and closed states.
- Revision handling.
- Optimistic concurrency.
- Command application.
- Validation.
- Persistence guarantees.

#### 8.2 Extract Only the Generic State Machine

Potential shared package:

```text
internal/platform/intakectl/
├── state.go
├── command.go
├── transition.go
└── transition_test.go
```

Keep the following inside each service:

- Proto mapping.
- Repository adapter.
- Database table ownership.
- Authorization.
- Audit events.
- Service-specific error mapping.

#### 8.3 Add Contract Tests

Use a common test suite against both adapters to ensure consistent generic behavior while allowing service-specific rules.

### Deliverables

- Shared intake transition primitive.
- Payin adapter.
- Payout adapter.
- Contract tests.
- Documentation of intentionally unshared behavior.

### Exit Criteria

- Generic transition logic has one implementation.
- Payin and Payout can still evolve authorization and audit behavior independently.
- The shared package does not import either business module.

---

## Phase 9 — Restrict Composition-Root Access

### Objective

Prevent `cmd` packages from depending on arbitrary module implementation details.

### Duration

Approximately 1 week.

### Tasks

#### 9.1 Define Allowed Imports

A composition root should normally import:

- Root module packages.
- Explicit `wiring` packages.
- Approved adapter constructors.
- Shared infrastructure packages.

It should not freely import:

- Internal repository implementations.
- Domain internals.
- Unexported-use-case packages.
- Test helpers.

#### 9.2 Introduce Wiring Packages Where Helpful

Example:

```text
services/payout/internal/wiring/
├── module.go
└── dependencies.go
```

This package may assemble repositories, clients, jobs, and the root facade.

#### 9.3 Strengthen Boundary Tests

Add rules such as:

- `cmd/<service>` may import only its service root, its wiring package, and approved infrastructure.
- A service wiring package may not be imported by another deployable service.
- Repository packages may not be imported directly by transport packages unless explicitly intended.

### Deliverables

- Import allowlist or rule set.
- Wiring packages where required.
- Updated architecture tests.
- Removed direct implementation imports from `cmd`.

### Exit Criteria

- Composition roots cannot bypass public module boundaries accidentally.
- Wiring remains understandable without exposing implementation details to unrelated packages.

---

## Phase 10 — Clean Up Shared Packages

### Objective

Prevent `internal/platform` from becoming a generic shared-code dumping ground.

### Duration

Approximately 1–2 weeks.

### Tasks

#### 10.1 Inventory Every Shared Package

For each package, document:

- Consumers.
- Owner.
- Reason for sharing.
- Stability expectation.
- Whether it contains business or infrastructure concepts.

#### 10.2 Remove Generic Names

Review packages such as:

- `generalutil`.
- `generalerror`.

Possible replacements:

- PostgreSQL error mapping → `pgerr` or `database/pgerr`.
- UUID helpers → `uuidx`.
- SQL helpers → `sqlutil` or `database`.
- Metadata utilities → owning transport or contract package.
- Generic value helpers → owning domain package.

#### 10.3 Move Single-Consumer Code Back to Its Owner

A utility used by one module should normally live inside that module.

#### 10.4 Prohibit Business Logic in Generic Infrastructure Packages

Add review and boundary rules preventing `internal/platform` from importing
business modules or encoding service-specific decisions.

### Deliverables

- Shared-package ownership inventory.
- Renamed or split generic packages.
- Removed unused utilities.
- Updated import paths and tests.

### Exit Criteria

- Every `internal/platform` package has a clear purpose and at least one justified consumer set.
- No package name relies on words such as `general`, `common`, or `misc` without a precise abstraction.
- Shared code does not encode hidden business ownership.

---

## Phase 11 — Improve Architecture Documentation and Comments

### Objective

Keep source comments focused on invariants while moving historical context into stable architecture records.

### Duration

Approximately 3–5 engineering days.

### Tasks

#### 11.1 Classify Existing Comments

Classify comments as:

- Business invariant.
- Implementation explanation.
- Historical task reference.
- Temporary workaround.
- Redundant narration.

#### 11.2 Preserve High-Value Comments

Keep comments that explain why ordering or validation matters.

Example:

```go
// Consume the quote before placing the hold so a rejected or expired quote
// cannot leave funds stranded in a held state.
```

#### 11.3 Move Historical Decisions to ADRs

Create stable ADRs for important decisions, such as:

- Payin callback correlation rules.
- Payout hold-before-dispatch ordering.
- Runtime capability removal.
- Intake-control consolidation.
- Root-facade and internal-package policy.

Use stable references such as `ADR-023`, not archived roadmap file paths.

#### 11.4 Track Temporary Workarounds

Every temporary workaround comment should include:

- Tracking issue.
- Removal condition.
- Owner when appropriate.

### Deliverables

- Updated comments in critical flows.
- New or updated ADRs.
- Comment policy in contribution guidelines.

### Exit Criteria

- Critical code explains invariants without requiring roadmap history.
- Temporary compatibility behavior is traceable.
- Archived documentation is not required to understand normal source code.

---

## Phase 12 — Machine-Readable Architecture Governance

### Objective

Reduce drift between documentation, source layout, deployables, and boundary tests.

### Duration

Approximately 1–2 weeks.

### Proposed Metadata

Create a file such as:

```text
architecture/services.yaml
```

Example:

```yaml
services:
  payin:
    binary: cmd/payin
    module: services/payin
    database_owner: payin
    allowed_dependencies:
      - contracts/clients/ledger
      - internal/platform/messaging
    exposed_contracts:
      - contracts/proto/payin

  payout:
    binary: cmd/payout
    module: services/payout
    database_owner: payout
    allowed_dependencies:
      - contracts/clients/ledger
      - internal/platform/messaging
    exposed_contracts:
      - contracts/proto/payout
```

### Generate or Validate

Use the metadata to:

- Validate deployable-to-module ownership.
- Validate allowed imports.
- Generate a service ownership table.
- Generate architecture diagrams.
- Detect documentation drift.
- Validate migration ownership.

### Deliverables

- Architecture metadata schema.
- Validation tool.
- Generated ownership documentation.
- CI drift check.

### Exit Criteria

- Service ownership is declared once and validated automatically.
- Boundary tests and documentation consume the same source of truth where practical.

---

# 7. Function-Level Refactoring Playbook

Use the following process for every high-complexity function.

## Step 1 — Document Observable Behavior

Record:

- Inputs.
- Outputs.
- State transitions.
- External calls.
- Ordering constraints.
- Retry behavior.
- Idempotency behavior.
- Failure classifications.

## Step 2 — Add Characterization Tests

Test the existing behavior before changing the structure.

## Step 3 — Identify Decision Clusters

Group branches by responsibility, for example:

- Validation.
- Correlation.
- Policy decision.
- Persistence.
- External side effect.
- Finalization.
- Recovery.

## Step 4 — Extract Pure Policies First

Move deterministic decision logic into pure functions or small policy objects.

## Step 5 — Introduce Typed Intermediate Results

Avoid passing loosely related primitives between steps.

## Step 6 — Preserve Transaction and Side-Effect Boundaries

Do not accidentally move external calls inside database transactions or split atomic operations without a documented replacement guarantee.

## Step 7 — Reduce Branching in the Orchestrator

The final orchestration function should read as a business workflow rather than an implementation transcript.

## Step 8 — Re-run All Verification Layers

At minimum:

- Unit tests.
- Race tests.
- Static analysis.
- Integration tests.
- Contract tests.
- Relevant chaos scenarios.

---

# 8. Proposed CI Quality Gates

## Required on Every Pull Request

- Build all Go binaries.
- Run unit tests.
- Run race tests for changed critical packages or the full repository when feasible.
- Run `go vet`.
- Run configured static analysis.
- Run boundary tests.
- Run contract checks.
- Compare complexity against baseline.
- Detect new duplication.
- Detect new dependency cycles.
- Detect new lint suppressions.

## Required for Critical Money-Movement Changes

- Payin or Payout integration tests.
- Idempotency scenarios.
- Callback replay tests.
- Recovery tests.
- Failure-injection or chaos tests for changed side-effect boundaries.

## Suggested Pull Request Output

```text
Architecture Quality Summary

Modified functions: 18
New complexity violations: 0
Resolved complexity violations: 2
New duplication blocks: 0
Dependency cycles: 0
Boundary violations: 0
New lint suppressions: 0
Critical-flow tests: passed
```

---

# 9. Definition of Done

The architecture-improvement program can be considered successful when:

- No new business-critical function exceeds the agreed complexity limit.
- All long callback parameter lists have been replaced by explicit models.
- Required capabilities are constructor-visible and validated at startup.
- Root module objects primarily delegate to focused services.
- Critical Payin and Payout flows are decomposed into testable use-case components.
- Recovery behavior is implemented per state rather than in one large conditional function.
- Internal package boundaries reflect business use cases.
- Composition roots cannot arbitrarily import module internals.
- Payin/Payout intake-control duplication is removed or explicitly justified.
- Generic shared packages have been renamed, split, or moved to clear owners.
- Architectural decisions are recorded in stable ADRs.
- Complexity, duplication, and dependency rules are continuously enforced by CI.
- All financial invariants, integration tests, and chaos tests continue to pass.

---

# 10. Success Metrics

Track the following metrics monthly or after every roadmap phase:

| Metric | Initial Target |
|---|---:|
| New functions above complexity threshold | 0 |
| Critical functions above CC 20 | Reduced by at least 50% in first cycle |
| Functions with more than 8 parameters | 0 in critical flows |
| New duplication blocks | 0 |
| Dependency cycles | 0 |
| Untracked runtime capability assertions | 0 |
| Unowned shared packages | 0 |
| Critical flow characterization coverage | 100% of identified scenarios |
| Architecture boundary violations | 0 |
| Expired lint suppressions | 0 |

Do not use code-coverage percentage as the only success metric. Critical behavior coverage and failure-path coverage are more important than achieving a high repository-wide percentage without meaningful assertions.

---

# 11. Recommended Execution Order

The recommended order based on risk and return is:

1. Establish the maintainability baseline.
2. Add characterization tests for critical money flows.
3. Enable complexity and duplication ratchets.
4. Introduce command and callback models.
5. Make currency, fee, fraud, and ledger capabilities explicit.
6. Refactor Payin creation and callback processing.
7. Refactor Payout creation and recovery.
8. Convert Payin, Payout, and Auth modules into thin facades.
9. Introduce vertical internal packages.
10. Consolidate intake-control mechanics.
11. Restrict composition-root imports.
12. Clean up shared packages and comments.
13. Add machine-readable architecture governance.

---

# 12. Recommended First Pull Requests

## PR 1 — Maintainability Baseline and Reporting

Include:

- Complexity reporting.
- Duplication reporting.
- Dependency graph generation.
- Baseline document.
- No hard failures yet.

## PR 2 — CI Complexity Ratchet

Include:

- Threshold configuration.
- Baseline comparison.
- Failure only for newly introduced violations.
- Suppression policy.

## PR 3 — Payin Callback Command Model

Include:

- `NormalizedPayinCallback`.
- Transport mapping.
- Existing behavior tests.
- No major workflow rewrite.

## PR 4 — Explicit Payin Capabilities

Include:

- `CurrencyPolicy`.
- `FeeQuoteService`.
- Startup validation.
- Removal of selected runtime type assertions.

## PR 5 — Extract Payin Callback Matcher

Include:

- Pure callback matching logic.
- Table-driven tests.
- Existing persistence and finalization left unchanged.

## PR 6 — Extract Payout Recovery Strategies

Include:

- Recovery registry.
- One strategy per existing state.
- Characterization tests.
- No state-machine semantic changes.

This pull-request sequence minimizes risk by separating structural changes from behavioral changes.

---

# 13. Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Refactoring changes financial behavior | Add characterization tests before moving code |
| Too many abstractions reduce readability | Extract only cohesive policies and use cases |
| CI becomes noisy and ignored | Use baseline ratcheting and actionable output |
| Shared package creates new coupling | Share only stable concepts with common ownership |
| Package split creates import cycles | Define dependency direction before moving files |
| Constructor growth becomes excessive | Group only cohesive capability sets; avoid service locators |
| Temporary compatibility paths remain forever | Require owner, issue, and removal criteria |
| Architecture documentation drifts | Generate or validate it from machine-readable metadata |
| Rewrite delays feature delivery | Deliver in small behavior-preserving pull requests |

---

# 14. Final Recommendation

Seev does not need a new architecture or additional microservices at this stage. It needs controlled consolidation of its existing architecture.

The highest-return actions are:

1. Add automated complexity and duplication ratchets.
2. Refactor the most complex Payin and Payout orchestration paths.
3. Replace hidden runtime capability discovery with explicit dependencies.
4. Turn large root modules into thin facades.
5. Introduce vertical package boundaries around real business use cases.

These changes will preserve Seev's strong service-level architecture while making its internal code easier to understand, test, extend, and eventually operate in a production environment.
