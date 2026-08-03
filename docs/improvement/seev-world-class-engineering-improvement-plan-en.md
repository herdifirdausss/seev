# Seev World-Class Engineering Improvement Plan

> Repository: `https://github.com/herdifirdausss/seev`  
> Current status: **strong pre-production engineering prototype and engineering reference**  
> Target: evolve Seev from a portfolio-grade repository into a system supported by **production-grade engineering evidence**

---

## 1. Executive Summary

Seev already has an unusually strong engineering foundation for a non-production repository:

- an append-only double-entry ledger;
- idempotent financial operations;
- a transactional outbox;
- recovery workers;
- reconciliation and assurance capabilities;
- explicit service and data ownership;
- API and event contract testing;
- race-condition testing;
- integration and end-to-end testing;
- chaos scenarios;
- disaster-recovery drills;
- a documented threat model;
- evidence-driven performance benchmarking.

The largest gaps are not missing features. They are:

1. several business and authorization invariants are not yet enforced end to end;
2. some capabilities exist in code and schema but have not completed runtime acceptance;
3. the repository has no production-shaped environment;
4. there is no real vendor integration, production traffic, operational history, or independent security review;
5. the operational value of running nine independently deployable services has not yet been proven.

The central strategy of this roadmap is:

> **Reduce breadth, increase depth, and prove one complete money-movement route end to end.**

---

## 2. Target Outcome

At the end of this roadmap, Seev should have:

- one golden money-movement journey proven against retries, crashes, duplicate callbacks, broker outages, and uncertain vendor outcomes;
- all critical financial invariants enforced in both the application and database layers where appropriate;
- all asynchronous execution paths using the same authorization and policy pipeline as synchronous requests;
- a staging environment that meaningfully resembles production;
- documented evidence for load testing, chaos testing, failover, backup, restore, and reconciliation;
- an auditable security baseline;
- operator-ready recovery and incident-response runbooks;
- concise documentation that allows a reviewer to evaluate the system’s engineering quality within five minutes.

---

## 3. Engineering Assessment

| Area | Current Condition | Target Condition |
|---|---|---|
| Financial correctness | Very strong | Every critical invariant has runtime and database evidence |
| Architecture | Clear boundaries, high runtime complexity | Preserve logical boundaries; justify deployment boundaries with evidence |
| Testing | Broad and mature | Add production-shaped acceptance testing |
| Reliability | Outbox, recovery, chaos, and DR exist | All critical scenarios are green and reproducible |
| Security | Strong foundation | Production enforcement and independent review |
| Performance | Evidence-driven | Validation on managed infrastructure |
| Business completeness | Incomplete | One complete vertical slice delivered end to end |
| Operability | Many components exist | Proven runbooks, alerts, SLOs, ownership, and drills |
| Production readiness | Not ready | Controlled-pilot readiness |

---

# 4. Guiding Principles

## 4.1 Correctness Over Throughput

No throughput improvement may:

- weaken ledger consistency;
- weaken idempotency guarantees;
- reduce auditability;
- convert an unknown vendor outcome into an assumed failure;
- depend on eventual reconciliation for an invariant that should be atomic.

## 4.2 Evidence Over Assumption

A capability is complete only when it is:

```text
Implemented
= reachable
+ authorized
+ observable
+ recoverable
+ tested end to end
```

Code, schema, and unit tests alone are not enough.

## 4.3 One Golden Route Before More Features

Before adding more product capabilities, one money-movement journey must be complete from entry point to financial settlement, recovery, and reconciliation.

## 4.4 Logical Boundaries Do Not Require Physical Distribution

Domain boundaries should remain explicit even when multiple modules are temporarily deployed together.

## 4.5 Failure Is a Normal State

Every workflow must define:

- retry policy;
- timeout behavior;
- idempotency behavior;
- unknown-state semantics;
- recovery ownership;
- alert thresholds;
- manual-intervention rules;
- reconciliation behavior.

---

# 5. Priority Classification

## P0 — Financial Correctness and Authorization

Items that could cause:

- unauthorized money movement;
- incorrect fees;
- KYC or policy bypass;
- invalid state transitions;
- duplicate financial movement;
- unbalanced ledger transactions;
- unapproved disbursements.

## P1 — Reliability and Operability

Items that could cause:

- stuck transactions;
- nondeterministic recovery;
- missing alerts;
- unusable backups;
- unclear operator actions.

## P2 — Production Engineering

Items required for:

- production-shaped staging;
- repeatable deployments;
- secret management;
- software-supply-chain security;
- scaling and failover.

## P3 — Business Expansion and Optimization

Items such as:

- additional product capabilities;
- advanced tenant-aware pricing;
- invoicing;
- settlement cycles;
- routing optimization;
- infrastructure optimization.

---

# 6. Roadmap Overview

| Phase | Focus | Indicative Duration | Exit Criteria |
|---|---|---:|---|
| Phase 0 | Baseline and scope freeze | 1 week | Scope is explicit and backlog is classified |
| Phase 1 | Correctness closure | 2–4 weeks | All P0 issues are closed |
| Phase 2 | Golden money route | 3–5 weeks | One complete journey survives defined failures |
| Phase 3 | Runtime acceptance | 2–4 weeks | Critical capabilities have runtime evidence |
| Phase 4 | Production-shaped environment | 4–6 weeks | Staging, IaC, secrets, SLOs, and failover are in place |
| Phase 5 | Security and resilience hardening | 3–5 weeks | Security review and DR evidence are complete |
| Phase 6 | Controlled-pilot readiness | 2–4 weeks | Go/no-go gates are satisfied |
| Phase 7 | Simplification and scaling decisions | Continuous | Architecture decisions are supported by measured data |

Durations are indicative. Sequence and exit criteria are more important than calendar estimates.

---

# 7. Phase 0 — Establish the Engineering Baseline

## Objective

Pause unnecessary expansion and create one authoritative view of the repository’s actual state.

## 7.1 Freeze Non-Critical Feature Development

During Phases 0–2:

- do not add another payment method;
- do not add another database technology;
- do not add another message broker;
- do not add another service;
- do not introduce sharding;
- do not move to Kubernetes solely for portfolio value;
- do not build a full customer-facing frontend.

Exceptions are allowed only when directly required by the golden route.

## 7.2 Create a Capability Inventory

Create:

```text
docs/engineering/capability-inventory.md
```

Use a status model such as:

| Capability | Code | Schema | Unit | Integration | E2E | Chaos | Runtime Accepted | Production Ready |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| P2P transfer | Yes | Yes | Yes | Yes | Yes | Yes | Yes | No |
| Scheduled transfer | Yes | Yes | Yes | Partial | Partial | No | No | No |
| Dispute | Yes | Yes | Yes | Partial | No | No | No | No |

Rules:

- `Code = Yes` must not imply runtime readiness.
- `E2E = Yes` must reference reproducible test evidence.
- `Production Ready = Yes` must require operational and security sign-off.
- Unknown or partially verified states must be explicit.

## 7.3 Create a Risk Register

Create:

```text
docs/engineering/risk-register.md
```

Minimum fields:

- ID;
- description;
- affected component;
- severity;
- probability;
- detectability;
- current controls;
- residual risk;
- owner;
- target milestone;
- verification evidence;
- status.

## 7.4 Define Production Readiness

Create:

```text
docs/operations/production-readiness-checklist.md
```

Required categories:

- correctness;
- authorization;
- security;
- deployment;
- observability;
- recovery;
- data protection;
- vendor integration;
- capacity;
- compliance;
- operator readiness.

## Deliverables

- `docs/engineering/capability-inventory.md`
- `docs/engineering/risk-register.md`
- `docs/operations/production-readiness-checklist.md`
- GitHub labels: `P0`, `P1`, `P2`, `P3`
- one GitHub milestone per roadmap phase

## Exit Criteria

- every capability is classified;
- every known high-risk finding is tracked;
- no major feature is being developed outside the agreed roadmap;
- every P0 and P1 issue has an owner;
- planned, implemented, runtime-accepted, and production-ready statuses are not conflated.

---

# 8. Phase 1 — Financial Correctness Closure

## Objective

Close every path that can bypass policy checks, calculate money incorrectly, or produce invalid financial states.

---

## 8.1 Unify the Money-Movement Command Pipeline

### Problem

Scheduled or recurring transactions may call posting logic without passing through the policy path used by public APIs.

### Target Design

Every money-movement entry point must use one application pipeline:

```text
Actor Resolution
    ↓
Authentication Context
    ↓
Authorization and Policy Evaluation
    ↓
KYC and Limit Validation
    ↓
Request Normalization
    ↓
Idempotency Validation
    ↓
State and Balance Validation
    ↓
Deterministic Lock Acquisition
    ↓
Ledger Posting
    ↓
Outbox Write
    ↓
Commit
```

The same pipeline must serve:

- synchronous APIs;
- scheduled transactions;
- recurring transactions;
- administrator-triggered corrections;
- payout retries;
- recovery workers;
- bulk disbursements;
- reversals;
- refunds.

### Implementation Plan

1. Introduce a money-movement application-command interface.
2. Move policy evaluation out of transport handlers.
3. Define an `ExecutionContext` containing:
   - actor;
   - tenant;
   - source;
   - correlation ID;
   - authorization claims;
   - effective execution time;
   - request origin.
4. Make schedulers create commands rather than call posting services directly.
5. Evaluate policy again at execution time, not only when a schedule is created.
6. Persist policy-decision metadata in the audit trail.
7. Add metrics for denials grouped by reason and execution source.
8. Restrict low-level posting APIs so only the application-command layer can invoke them.
9. Add architectural tests that fail when forbidden direct dependencies are introduced.

### Required Tests

- KYC is valid when a schedule is created but expired when it executes;
- a transaction limit changes before execution;
- the account is blocked before execution;
- the tenant is disabled;
- a scheduled command is retried after a crash;
- the same command is executed twice;
- the same idempotency key is reused with a different payload;
- a recovery worker executes under insufficient privileges;
- an administrator attempts to bypass the normal policy path.

### Acceptance Criteria

- no scheduler calls ledger posting logic directly;
- all money movements produce a policy-decision audit record;
- negative authorization tests are present;
- scheduled execution fails safely when policies change;
- architectural checks prevent future bypasses.

---

## 8.2 Implement Fee-Rule Maker-Checker Controls

### Problem

A single administrator can change a rule that affects future transaction pricing.

### Target

Use immutable, versioned fee rules with four-eyes approval.

### Suggested Data Model

```text
fee_rule_versions
- id
- tenant_id
- product
- calculation_type
- flat_minor_units
- percentage_bps
- minimum_minor_units
- maximum_minor_units
- effective_from
- effective_until
- status
- created_by
- approved_by
- approved_at
- supersedes_id
- version
- created_at
```

### Required Constraints

- `flat_minor_units >= 0`
- `percentage_bps >= 0`
- minimum must not exceed maximum;
- active versions must not overlap for the same scope;
- creator and approver must be different actors;
- unapproved rules must never be used;
- every transaction must persist the fee-rule version used;
- activated versions must be immutable;
- activation must be concurrency-safe.

### Required Tests

- negative flat fee is rejected;
- negative percentage is rejected;
- creator attempts to approve their own rule;
- overlapping active rules are rejected;
- two approval requests race;
- rollback to a previous version;
- repeated calculation produces the same result;
- historical transactions still reference the original version;
- the rule changes between request acceptance and execution.

### Acceptance Criteria

- every applied fee is traceable to an immutable version;
- fee changes require independent approval;
- historical fee calculations are reproducible;
- audit logs capture before, after, maker, checker, and activation time.

---

## 8.3 Enforce the Disbursement Approval Invariant

### Objective

The database must prevent a disbursement batch from reaching `processing` without valid approval.

### Actions

- add a check constraint or deferred constraint trigger;
- use explicit state-transition methods;
- add optimistic concurrency control;
- separate `created_by`, `submitted_by`, `approved_by`, and `processed_by`;
- prevent the same actor from acting as both maker and checker;
- emit an audit event for every transition;
- define allowed previous states;
- prevent updates to immutable approval metadata after processing begins.

### Required Tests

- direct SQL update attempts to move a batch to `processing` without approval;
- concurrent approval and cancellation;
- duplicate approval;
- processing begins before the approval transaction commits;
- a state transition is retried after a timeout;
- an actor tries to approve a batch they created;
- stale optimistic version is used.

### Acceptance Criteria

- invalid transitions fail in the database;
- application error mapping is deterministic;
- every transition produces an auditable event;
- invalid direct SQL changes cannot bypass the invariant.

---

## 8.4 Complete the Dispute Lifecycle

### Required Flow

```text
Dispute Opened
→ Evidence Requested
→ Evidence Submitted
→ Under Review
→ Won / Lost / Expired
→ Financial Adjustment
→ Reconciliation
```

### Tasks

- expose the lifecycle through the appropriate domain API;
- expose operator actions through the Admin BFF;
- implement a deadline worker;
- persist evidence metadata;
- constrain the dispute amount to the original transaction amount;
- define idempotency rules for every dispute action;
- add maker-checker approval for high-value resolutions;
- publish notification events;
- add complete audit trails;
- define terminal-state immutability;
- define retry behavior for financial adjustments;
- define compensation behavior if downstream notification fails.

### Required Tests

- dispute amount exceeds original transaction amount;
- duplicate dispute submission;
- evidence deadline passes;
- evidence is submitted after expiry;
- resolution is retried;
- worker crashes after the financial adjustment commits;
- duplicate callback arrives from an external network;
- concurrent win and loss decisions are attempted;
- adjustment posting fails;
- reconciliation detects an incorrect adjustment.

### Acceptance Criteria

- normal dispute handling requires no raw SQL;
- deadlines are processed automatically;
- operators can see current state, history, and next allowed action;
- financial adjustments reconcile correctly;
- duplicate and reordered actions do not create duplicate outcomes.

---

## 8.5 Strengthen Currency and Amount Type Safety

### Tasks

- use one canonical money type;
- avoid raw integers without currency context;
- validate minor-unit rules per currency;
- reject currency mismatches before posting;
- bind currency to ledger accounts or posting context;
- test zero-decimal and non-two-decimal currencies if multi-currency is supported;
- use decimal or rational representation for FX rates;
- store the rounding mode explicitly;
- prevent implicit currency conversion;
- document rounding responsibility at every boundary.

### Acceptance Criteria

- no financial calculation uses binary floating point;
- currency mismatch fails before ledger posting;
- rounding is deterministic and reproducible;
- the codebase does not expose ambiguous amount-only APIs.

---

## 8.6 Close Remaining Database and Authorization Findings

Review and close all high-priority findings, including:

- KYC-state consistency constraints;
- unused or excessive database grants;
- dispute amount constraints;
- active-rule overlap protection;
- terminal-state immutability;
- audit-column immutability;
- tenant-boundary enforcement;
- privileged maintenance paths;
- direct access to internal posting APIs.

## Phase 1 Exit Criteria

Phase 1 is complete when:

- all P0 findings are closed;
- every money-movement route uses the unified command pipeline;
- critical invariants have database backstops where feasible;
- negative tests exist for every critical rule;
- no unresolved high-severity correctness issue remains;
- CI prevents reintroduction of known bypasses.

---

# 9. Phase 2 — Build One Golden Money Route

## Objective

Prove one complete financial journey rather than expanding many partially complete capabilities.

## Recommended Golden Route

```text
User Registration
→ Authentication
→ KYC Approval
→ Pay-in Request
→ Vendor Submission
→ Duplicate and Delayed Callback
→ Funds Become Available
→ P2P Transfer
→ Payout Request
→ Vendor Timeout / Unknown Outcome
→ Recovery
→ Reconciliation
→ Statement
→ Dispute or Reversal
```

---

## 9.1 Define an Explicit State Machine per Aggregate

For pay-in, payout, transfer, and dispute, document:

- states;
- allowed transitions;
- actor allowed to trigger each transition;
- preconditions;
- side effects;
- idempotency rules;
- timeout behavior;
- recovery action;
- terminal states;
- forbidden transitions.

Example:

| Current State | Event | Next State | Side Effect |
|---|---|---|---|
| `created` | submit | `submitted` | send vendor request |
| `submitted` | success callback | `succeeded` | post ledger movement |
| `submitted` | request timeout | `unknown` | schedule recovery |
| `unknown` | vendor query returns success | `succeeded` | post ledger movement |
| `unknown` | vendor query confirms failure | `failed` | release reservation |

Rules:

- states must not be updated through arbitrary repository methods;
- terminal-state changes require explicit correction workflows;
- every transition must be idempotent;
- the state machine must distinguish technical retry from business retry.

---

## 9.2 Create a Failure Matrix

Create:

```text
docs/engineering/golden-route-failure-matrix.md
```

Example:

| Failure Point | Expected State | Retry Safe | Recovery Owner | Alert |
|---|---|---:|---|---|
| Crash before DB commit | no state change | Yes | automatic retry | No |
| Crash after commit before publish | outbox pending | Yes | outbox worker | Yes when lag exceeds SLO |
| Vendor accepted but response was lost | unknown | Conditional | recovery worker | Yes |
| Callback is duplicated | unchanged | Yes | callback handler | metric only |
| Broker is unavailable | committed, outbox pending | Yes | outbox relay | Yes |
| Ledger posting is rejected | business failure | Yes | orchestrator | Yes |
| Reconciliation mismatch | quarantined | N/A | operator | Critical |

For every row, document:

- detection mechanism;
- retry interval;
- maximum retry age;
- escalation;
- operator action;
- data required to investigate;
- safe replay procedure.

---

## 9.3 Golden-Route Test Requirements

The route must test:

- duplicate HTTP request;
- duplicate message delivery;
- duplicate callback;
- out-of-order callback;
- callback with invalid signature;
- callback with changed payload;
- vendor timeout;
- vendor 5xx;
- connection reset after vendor acceptance;
- process termination before commit;
- process termination after commit;
- database restart;
- broker outage;
- Redis outage;
- outbox backlog;
- worker restart;
- stale lock;
- database deadlock and retry;
- same idempotency key with different payload;
- reconciliation mismatch;
- operator-initiated recovery;
- rolling deployment during in-flight transactions.

---

## 9.4 Create an Evidence Package

Store evidence under:

```text
artifacts/golden-route/<commit-hash>/
```

Include:

- test report;
- service logs;
- trace IDs;
- metrics snapshots;
- ledger-invariant report;
- outbox-drain report;
- reconciliation report;
- environment metadata;
- commit hash;
- seed version;
- test-scenario version;
- container or artifact digests.

## Phase 2 Exit Criteria

- the golden route is fully green;
- all critical failure points are exercised;
- the ledger remains balanced;
- no duplicate financial outcome occurs;
- every unknown state has a documented recovery path;
- the operator runbook has been executed successfully;
- evidence can be reproduced from a clean environment.

---

# 10. Phase 3 — Runtime Acceptance Closure

## Objective

Convert implemented-but-unverified capabilities into runtime-accepted capabilities.

## Candidate Capabilities

Prioritize:

1. scheduled transactions;
2. disputes;
3. payout recovery;
4. reconciliation;
5. notification delivery;
6. FX;
7. savings or accrual;
8. balance-migration control plane.

## Runtime Acceptance Checklist

Every accepted capability must be:

- reachable through a supported interface;
- authorized;
- tenant-safe;
- covered by integration tests;
- covered by an end-to-end test;
- observable through metrics;
- observable through structured logs;
- traceable through correlation context;
- protected by actionable alerts;
- governed by an explicit retry policy;
- recoverable through a documented runbook;
- supported by a data-migration plan;
- supported by rollback or roll-forward behavior;
- assigned to an operational owner.

## Deliverable

Create one document per capability:

```text
docs/acceptance/<capability>.md
```

Template:

```markdown
# Capability Acceptance

## Scope
## Entry Points
## Authorization
## Data Ownership
## State Machine
## Invariants
## Idempotency
## Failure Modes
## Recovery
## Observability
## Test Evidence
## Known Limitations
## Accepted Residual Risk
## Sign-off
```

## Phase 3 Exit Criteria

- no capability is called complete without an acceptance document;
- README and current-state documentation clearly distinguish planned, coded, accepted, and production-ready;
- every critical capability has reproducible runtime evidence;
- acceptance gaps are visible in the capability inventory.

---

# 11. Phase 4 — Build a Production-Shaped Environment

## Objective

Test the system under conditions that are meaningfully closer to a real deployment.

---

## 11.1 Implement Infrastructure as Code

Use one primary tool, such as Terraform or OpenTofu.

Provision at minimum:

- network and private subnets;
- managed PostgreSQL;
- Redis;
- message broker;
- secret manager;
- KMS;
- workload identity;
- container runtime;
- object storage for backups and evidence;
- monitoring;
- centralized logging;
- DNS;
- certificate management.

Rules:

- the environment must be reproducible from source control;
- environment-specific values must not be hard-coded;
- manual console changes must be prohibited or documented as break-glass actions;
- state storage and locking must be protected.

---

## 11.2 Enforce Environment Separation

Minimum environments:

- local;
- test;
- staging;
- production.

Production mode must fail fast when:

- a mock vendor is enabled;
- default credentials are present;
- TLS is disabled;
- a local CA is configured;
- development secrets are detected;
- wildcard CORS is enabled;
- debug endpoints are exposed;
- migrations run from the application process;
- a database superuser is used;
- insecure development callbacks are configured.

---

## 11.3 Define a Safe Migration Strategy

Requirements:

- migrations run as a separate deployment job;
- backward-compatible expansion happens first;
- the application is deployed next;
- contract migration follows;
- cleanup happens only after observability proves safety;
- lock timeouts and statement timeouts are configured;
- pre-migration backups are taken for high-risk changes;
- migrations are rehearsed on production-like data;
- rollback or roll-forward behavior is documented;
- long-running backfills are resumable;
- schema ownership and privileges are verified.

---

## 11.4 Test Multi-Replica Behavior

Test:

- concurrent workers;
- duplicate scheduling;
- leader election where required;
- partition or queue ownership;
- total connection-pool pressure across replicas;
- graceful shutdown;
- in-flight request draining;
- redelivery after termination;
- rolling deployments;
- duplicate event handling;
- worker lease expiration;
- clock skew tolerance.

---

## 11.5 Integrate at Least One Real Vendor Sandbox

At least one adapter must use:

- sandbox credentials;
- the vendor’s real signing method;
- realistic timeout behavior;
- real callbacks;
- complete request and response mapping;
- replay protection;
- contract testing;
- a scheduled connectivity check;
- vendor-specific error classification;
- query-after-timeout recovery.

## Phase 4 Exit Criteria

- staging can be recreated from scratch;
- no manual secret injection is required;
- deployment is reproducible;
- multi-replica smoke tests are green;
- vendor-sandbox E2E is green;
- migration rehearsal succeeds;
- rollback or roll-forward has been tested;
- all production-mode safety checks are enforced.

---

# 12. Phase 5 — Observability, Security, and Resilience

## 12.1 Define Service-Level Objectives

Define SLOs around user journeys, not only individual services.

### Transfer Example

- request-acceptance availability;
- accepted-request latency p95;
- completed-transfer latency p95;
- duplicate financial outcome: zero;
- unresolved reconciliation mismatch above threshold: zero;
- outbox lag p99;
- recovery completion time.

### Payout Example

- submission success rate;
- terminal-resolution time;
- maximum unknown-state age;
- vendor-timeout rate;
- automatic-recovery success rate;
- manual-intervention rate.

Every SLO should define:

- indicator;
- objective;
- measurement window;
- error-budget policy;
- alerting threshold;
- owner.

---

## 12.2 Required Metrics

### Financial Metrics

- total posted debits and credits;
- unbalanced transaction count;
- reconciliation mismatch count;
- reconciliation mismatch age;
- duplicate idempotency attempts;
- conflicting idempotency payloads;
- corrections and reversals;
- fee-calculation failures;
- unauthorized financial-command attempts.

### Asynchronous Metrics

- outbox depth;
- oldest outbox age;
- retry count;
- dead-letter count;
- consumer lag;
- processing duration;
- stuck-state count;
- recovery success and failure counts;
- message redelivery count.

### Database Metrics

- connection utilization;
- transaction latency;
- lock wait;
- deadlock count;
- query p95 and p99;
- replication lag;
- checkpoint behavior;
- disk growth;
- transaction rollback rate.

### Vendor Metrics

- request latency;
- timeout count;
- error classification;
- unknown-state age;
- callback verification failure;
- duplicate callback;
- query-after-timeout result;
- vendor availability by operation.

---

## 12.3 Design Actionable Alerts

Every alert must include:

- impact;
- severity;
- threshold;
- likely causes;
- first checks;
- safe actions;
- escalation path;
- dashboard;
- runbook.

Avoid alerts such as:

```text
Error count is high
```

Prefer:

```text
Payouts in unknown state for more than 15 minutes exceeded the threshold.
A vendor acknowledgement may have been lost.
Run the payout unknown-state recovery playbook.
```

Alert design principles:

- page only when human action is required;
- use tickets or dashboards for non-urgent trends;
- suppress duplicate alerts;
- test alerts during drills;
- link directly to the relevant trace or filtered dashboard.

---

## 12.4 Harden Secrets and Identity

Implement:

- workload identity;
- short-lived credentials;
- external secret management;
- automatic certificate rotation;
- no credentials in the repository;
- secret-access auditing;
- secret-rotation drills;
- scoped database roles;
- separate runtime and migration identities;
- break-glass accounts with strong audit controls;
- revocation procedures;
- certificate-expiry alerts.

---

## 12.5 Strengthen the Software Supply Chain

Implement:

- dependency pinning;
- `govulncheck`;
- container vulnerability scanning;
- SBOM generation;
- artifact signing;
- build provenance;
- minimal base images;
- non-root containers;
- read-only filesystems where feasible;
- minimum CI permissions;
- protected release branches;
- verified third-party GitHub Actions;
- reproducible release metadata.

---

## 12.6 Perform an Independent Security Review

Minimum scope:

- authentication;
- authorization;
- tenant isolation;
- webhook verification;
- mTLS identity;
- secret lifecycle;
- SSRF;
- injection;
- mass assignment;
- replay attacks;
- privilege escalation;
- administrator operations;
- audit-log tampering;
- financial-abuse cases;
- rate limiting;
- denial-of-service boundaries;
- dependency and build-chain risks.

Track every finding with:

- severity;
- exploitability;
- business impact;
- remediation;
- owner;
- due date;
- verification evidence;
- accepted residual risk when not remediated.

---

## 12.7 Validate Disaster Recovery

A DR drill must prove:

- backups are valid;
- restore succeeds;
- PITR succeeds;
- services reconnect;
- secrets and certificates remain available;
- cross-database data remains consistent;
- the ledger remains balanced;
- outbox processing resumes;
- duplicate financial movement does not occur;
- RPO and RTO are measured;
- operator instructions are sufficient.

## Phase 5 Exit Criteria

- SLOs are defined and measurable;
- critical alerts have tested runbooks;
- secret-rotation drill succeeds;
- independent security findings are closed or formally accepted;
- DR drill meets the target RPO and RTO;
- the critical chaos suite is green;
- operators can perform recovery without developer-only knowledge.

---

# 13. Phase 6 — Controlled-Pilot Readiness

## Objective

Determine whether Seev is safe enough for a tightly limited pilot.

## Recommended Pilot Scope

Use:

- one internal tenant;
- synthetic money or very small transaction values;
- one vendor;
- one currency;
- one pay-in flow;
- one payout flow;
- low transaction limits;
- a restricted user group;
- daily reconciliation;
- manual review of exceptional states;
- strict kill-switch criteria.

## Go/No-Go Checklist

### Correctness

- [ ] Ledger remains balanced
- [ ] Every posting is idempotent
- [ ] Conflicting idempotency payloads are rejected
- [ ] Fee versions are immutable
- [ ] Approval invariants are active
- [ ] Scheduled execution passes through policy checks
- [ ] Reconciliation is green
- [ ] No unresolved P0 issue remains

### Reliability

- [ ] Outbox recovery is proven
- [ ] Duplicate callbacks are safe
- [ ] Unknown vendor states are recoverable
- [ ] Broker outages are recoverable
- [ ] Database restarts are recoverable
- [ ] Rolling deployment is safe
- [ ] Critical chaos scenarios are green
- [ ] Stuck-state detection is active

### Security

- [ ] Production secrets are external
- [ ] Default credentials are rejected
- [ ] TLS is enforced
- [ ] Database roles are least-privilege
- [ ] Administrator maker-checker is active
- [ ] Security review is complete
- [ ] Audit logs are immutable or tamper-evident
- [ ] Vendor callbacks have replay protection

### Operations

- [ ] Dashboards are ready
- [ ] Alerts are ready
- [ ] Runbooks are ready
- [ ] On-call ownership is defined
- [ ] Incident severity levels are defined
- [ ] Backup restore has been tested
- [ ] Reconciliation ownership is defined
- [ ] Pilot rollback and kill-switch procedures are tested

### Capacity

- [ ] Expected traffic is defined
- [ ] Staging load test is complete
- [ ] Soak test is green
- [ ] Connection-pool sizing is safe
- [ ] Database headroom is available
- [ ] Scale thresholds are documented
- [ ] Vendor rate limits are understood

## Phase 6 Exit Criteria

The pilot may begin only when:

- all P0 and P1 issues are closed;
- no unknown critical risk remains;
- every go/no-go item has evidence;
- the pilot has an explicit owner;
- rollback and stop conditions are documented.

---

# 14. Phase 7 — Architecture Simplification Review

## Objective

Evaluate whether nine independently deployable services provide enough operational value to justify their complexity.

## Service-Justification Framework

A service should normally remain independently deployable when it satisfies at least two of the following:

1. it requires a distinct security boundary;
2. it owns independent data;
3. it needs meaningful failure isolation;
4. it has a significantly different scaling profile;
5. it has a different release cadence;
6. it has separate team ownership;
7. it performs a distinct privileged operation.

## Review Questions

For every service:

- does it have its own database because of a real domain need or merely because of an architectural pattern?
- must its failures be isolated?
- does it scale differently?
- does independent deployment reduce risk or add risk?
- is local development unnecessarily heavy?
- are there too many database connection pools?
- is contract churn high?
- is the transaction path too chatty?
- is distributed tracing sufficient?
- can deployment be consolidated without weakening logical boundaries?
- does the service have enough operational ownership to justify independence?

## Possible Consolidation Model

The logical architecture may remain:

```text
Gateway
Auth
Payin
Payout
Ledger
Vendor
Fraud
Assurance
Admin
```

A smaller physical deployment model might be:

```text
Edge Deployment
- Gateway
- Auth
- Admin BFF

Money Movement Deployment
- Payin
- Payout
- Fraud orchestration

Ledger Deployment
- Ledger
- Assurance worker

External Integration Deployment
- VendorService
```

This is only an example. The final decision must be based on profiling, ownership, deployment risk, and failure analysis.

## Phase 7 Exit Criteria

- every independent service has a written justification;
- operational cost is measured;
- rejected and accepted consolidation options are documented;
- simplification decisions preserve domain boundaries and correctness.

---

# 15. Recommended Backlog

## P0

- [ ] Route scheduled transactions through the policy engine
- [ ] Introduce the unified money-movement command pipeline
- [ ] Implement fee-rule maker-checker
- [ ] Add non-negative fee database constraints
- [ ] Add the disbursement-approval database invariant
- [ ] Add dispute-amount constraints
- [ ] Expose dispute APIs and operator flows
- [ ] Implement the dispute-deadline worker
- [ ] Strengthen currency type safety
- [ ] Close the remaining non-green chaos scenario
- [ ] Remove or restrict all direct posting paths
- [ ] Add architectural tests for forbidden bypasses

## P1

- [ ] Document golden-route state machines
- [ ] Create the failure matrix
- [ ] Implement operator recovery APIs
- [ ] Implement a stuck-state scanner
- [ ] Define an outbox-age SLO
- [ ] Define an unknown-vendor-state SLO
- [ ] Add reconciliation alerts
- [ ] Complete recovery runbooks
- [ ] Define an incident-severity model
- [ ] Preserve DR-drill evidence
- [ ] Complete runtime-acceptance documents
- [ ] Add regular alert and runbook drills

## P2

- [ ] Implement Infrastructure as Code
- [ ] Provision managed PostgreSQL staging
- [ ] Introduce an external secret manager
- [ ] Introduce workload identity
- [ ] Separate migration jobs from application startup
- [ ] Generate SBOMs and sign artifacts
- [ ] Add multi-replica tests
- [ ] Integrate a real vendor sandbox
- [ ] Centralize logs
- [ ] Build SLO dashboards
- [ ] Complete an independent security review
- [ ] Add release provenance

## P3

- [ ] Implement tenant-aware pricing
- [ ] Add merchant invoices and statements
- [ ] Add settlement cycles
- [ ] Add rolling reserves
- [ ] Define dormant-account handling
- [ ] Integrate a real FX-rate source
- [ ] Consolidate duplicated routing logic
- [ ] Apply capacity-driven optimization
- [ ] Build a customer-facing frontend
- [ ] Add additional vendors

---

# 16. Definition of Done

A financial capability is complete only when all of the following are satisfied.

## Design

- the state machine is documented;
- invariants are documented;
- authorization is documented;
- idempotency semantics are documented;
- failure modes are documented;
- recovery ownership is documented.

## Implementation

- application validation exists;
- database constraints exist where feasible;
- structured audit events exist;
- metrics exist;
- trace propagation exists;
- retry and timeout behavior is explicit;
- a recovery path exists;
- terminal-state behavior is explicit.

## Verification

- unit tests;
- integration tests;
- race and concurrency tests;
- negative tests;
- end-to-end tests;
- crash and retry tests;
- reconciliation tests;
- authorization bypass tests.

## Operations

- alert;
- dashboard;
- runbook;
- owner;
- rollback or roll-forward plan;
- accepted residual risk;
- pilot kill-switch when applicable.

## Evidence

- commit hash;
- environment metadata;
- test report;
- logs and traces;
- invariant report;
- acceptance sign-off;
- artifact or image digest.

---

# 17. Engineering Metrics

Do not measure progress only through feature count or commit count.

## Correctness Metrics

- unresolved financial-invariant findings;
- idempotency-conflict count;
- duplicate financial outcomes;
- reconciliation-mismatch age;
- unbalanced ledger count;
- unauthorized command attempts.

## Reliability Metrics

- oldest outbox age;
- stuck transaction count;
- unknown vendor-state age;
- automatic-recovery success rate;
- manual-intervention rate;
- dead-letter age.

## Delivery Metrics

- runtime-accepted capability ratio;
- mean time from code complete to runtime accepted;
- escaped defects;
- rollback rate;
- flaky-test rate;
- acceptance-document completeness.

## Performance Metrics

- validated sustainable workload;
- p50, p95, and p99 latency;
- error rate;
- database lock wait;
- connection-pool saturation;
- outbox lag;
- resource headroom;
- long-duration soak stability.

## Operability Metrics

- alert actionability rate;
- runbook success rate;
- mean time to detect;
- mean time to recover;
- DR RPO and RTO achievement;
- percentage of incidents resolved without ad hoc database modification.

---

# 18. Documentation Deliverables

Add or standardize the following structure:

```text
docs/
├── engineering/
│   ├── capability-inventory.md
│   ├── risk-register.md
│   ├── golden-route.md
│   ├── golden-route-failure-matrix.md
│   ├── service-justification.md
│   └── production-readiness-scorecard.md
├── acceptance/
│   ├── scheduled-transactions.md
│   ├── disputes.md
│   ├── payout-recovery.md
│   └── reconciliation.md
├── operations/
│   ├── production-readiness-checklist.md
│   ├── payout-unknown-state-runbook.md
│   ├── reconciliation-mismatch-runbook.md
│   ├── outbox-backlog-runbook.md
│   ├── database-failover-runbook.md
│   └── incident-severity.md
└── portfolio/
    └── engineering-proof.md
```

Documentation rules:

- link every acceptance claim to evidence;
- state known limitations explicitly;
- record rejected alternatives and the evidence behind rejection;
- distinguish current state from target state;
- keep operational runbooks executable and command-oriented.

---

# 19. Portfolio Packaging

After enough engineering evidence exists, create:

```text
docs/portfolio/engineering-proof.md
```

Title:

```text
Evaluate Seev in Five Minutes
```

Recommended sections:

1. **Problem**  
   How can a wallet move money effectively once across retries, crashes, delayed callbacks, and unreliable external vendors?

2. **Architecture**  
   One diagram with no more than nine boxes.

3. **Correctness**  
   Ledger invariants, idempotency, transaction boundaries, and database constraints.

4. **Failure Handling**  
   Outbox, recovery, unknown states, and reconciliation.

5. **Evidence**  
   CI, race tests, chaos tests, DR drills, benchmarks, and rejected optimizations.

The purpose is not to display every feature. It is to provide fast, credible evidence of engineering judgment.

---

# 20. Anti-Goals

Do not prioritize the following yet:

- a large-scale rewrite;
- Kubernetes without a demonstrated need;
- Kafka without a measured bottleneck;
- database sharding;
- another distributed cache;
- full event sourcing;
- active-active multi-region deployment;
- a large customer-facing frontend;
- many vendor integrations;
- compliance claims without formal assessment;
- production-ready claims without real evidence.

---

# 21. Recommended First 30 Days

## Week 1

- freeze scope;
- complete the capability inventory;
- complete the risk register;
- map every direct posting path;
- classify all P0 findings;
- define the golden route;
- create phase milestones.

## Week 2

- implement the unified command pipeline;
- enforce scheduled-transaction policy checks;
- add negative tests;
- audit policy decisions;
- add architectural checks preventing direct posting.

## Week 3

- implement fee maker-checker;
- add fee database constraints;
- add the disbursement-approval invariant;
- add direct-SQL invalid-state tests;
- close related authorization findings.

## Week 4

- expose dispute APIs;
- implement the deadline worker;
- implement operator actions;
- add retry and crash tests;
- rerun the incomplete chaos scenario;
- publish Phase 1 evidence.

---

# 22. Recommended 60–90 Day Outcome

By day 90, a realistic target is:

- all P0 issues closed;
- one golden route fully green;
- runtime acceptance completed for critical capabilities;
- production-shaped staging available;
- one vendor sandbox connected;
- staging load and soak-test evidence available;
- critical chaos and DR drills green;
- initial independent security review complete;
- production-readiness scorecard published;
- five-minute engineering proof page complete.

---

# 23. Final Recommendation

Seev does not need more technology to become stronger.

It needs:

1. **correctness closure;**
2. **runtime proof;**
3. **production-shaped execution;**
4. **operational evidence;**
5. **simplification based on measured reality.**

The recommended strategy is:

> Complete one vertical slice until it is genuinely production-shaped, then use the resulting evidence to decide which features, services, and optimizations should come next.

Following this approach can evolve Seev from an exceptional portfolio repository into an engineering case study that is difficult for senior engineers, staff engineers, hiring managers, and technical reviewers to dismiss.
