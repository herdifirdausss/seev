# Seev DevOps Production Readiness Plan

> A roadmap for evolving Seev from an engineering sandbox and portfolio repository into a platform with a trusted delivery chain, resilient runtime, strong security controls, actionable observability, and verifiable production evidence.

## Document Status

| Field | Value |
|---|---|
| Repository | `https://github.com/herdifirdausss/seev` |
| Current status | Has never been used in production |
| Plan type | DevOps, Platform Engineering, SRE, and Production Readiness |
| Recommended horizon | 90–120 days |
| Primary objective | Establish one secure, repeatable, observable, recoverable, and auditable production path |
| Initial cloud strategy | Select one provider as the production reference; retain the other as a portability sandbox |

---

## 1. Executive Summary

Seev already has a DevOps foundation that is considerably stronger than the average portfolio repository:

- Layered CI verification.
- Integration and end-to-end testing.
- Container hardening.
- Kubernetes manifests and a Helm chart.
- Terraform configurations for AWS and GCP.
- An observability stack.
- Load, chaos, backup, restore, and PITR tooling.
- Runbooks and operational documentation.

However, the presence of these components does not automatically make the system production-ready. The largest gaps are:

1. There is no trusted release pipeline producing immutable, signed, and traceable artifacts.
2. Secrets and workload identities are too broadly distributed, including an operator private key mounted into application pods.
3. The runtime still contains multiple single points of failure.
4. Kubernetes and infrastructure validation are not strict enough.
5. SLOs are not yet fully connected to paging, ownership, and deployment decisions.
6. There is no cloud staging environment that provides production-like operational evidence.
7. There is no repeatable proof that the system survives critical failure scenarios.

This roadmap prioritizes the highest-ROI improvements and avoids adding more tools before the essential foundations are complete.

---

## 2. Target End State

The target software delivery flow is:

```text
Developer opens a pull request
        ↓
Automated verification and security checks
        ↓
Artifact is built once
        ↓
SBOM, vulnerability scan, provenance, and signature
        ↓
Immutable image digest is pushed to the registry
        ↓
The digest is promoted to development
        ↓
The same digest is promoted to staging
        ↓
Production approval
        ↓
Progressive deployment
        ↓
Automated health, SLO, and business-invariant verification
        ↓
Rollback or continue rollout
```

The target runtime is:

```text
Internet
   ↓
DNS / CDN / WAF / DDoS Protection
   ↓
Cloud Load Balancer / Ingress
   ↓
Gateway replicas distributed across multiple zones
   ↓
Application services using workload identities
   ↓
Managed PostgreSQL / Redis / Messaging
   ↓
Centralized logs, metrics, traces, and alerts
   ↓
Backups copied to a separate failure domain
```

---

## 3. Guiding Principles

### 3.1 Build once, promote many

A production artifact must not be rebuilt for each environment. One immutable digest must be promoted from development to staging and production.

### 3.2 Correctness before scalability

Money-movement correctness, idempotency, data integrity, and recoverability are always more important than maximum throughput.

### 3.3 Managed services for critical state

Do not run single-replica PostgreSQL, Redis, or messaging infrastructure inside a production Kubernetes cluster merely for tooling consistency.

### 3.4 Least privilege by default

Every workload should receive only the identity, secrets, network access, and database privileges it genuinely requires.

### 3.5 Evidence over configuration

YAML files are not proof of readiness. Every critical capability should have a test result, drill result, dashboard, report, or other verifiable evidence.

### 3.6 One production path first

Productionize one cloud provider deeply before attempting production parity across two providers.

### 3.7 Reliability is a release requirement

Deployment decisions should depend not only on passing tests, but also on health, error budget, latency, backlog, dependency saturation, and business correctness.

---

## 4. Priority Classification

| Priority | Meaning |
|---|---|
| P0 | Must be completed before accepting real production traffic |
| P1 | Strongly required for stable production operations |
| P2 | Improves efficiency, developer experience, and operational maturity |
| P3 | Long-term optimization after the platform is stable |

---

# Phase 0 — Baseline and Decision Lock

## Goal

Create an objective baseline, lock the scope, and prevent the roadmap from expanding across too many cloud providers or tools.

## Duration

3–5 days.

## Tasks

### DEVOPS-0001 — Select the production reference cloud

**Priority:** P0

Choose one of the following:

- GCP as the production reference.
- AWS as the production reference.

The second provider remains a portability sandbox and must not block production readiness.

**Decision criteria:**

- Team familiarity.
- Cost.
- Managed PostgreSQL capabilities.
- Managed Redis capabilities.
- Managed messaging capabilities.
- Workload identity support.
- Completeness of the current infrastructure implementation.

**Deliverable:**

```text
docs/adr/ADR-XXX-production-cloud-provider.md
```

**Acceptance criteria:**

- One provider is formally selected as the primary provider.
- The second provider is explicitly declared non-blocking.
- All following production-readiness tasks target the primary provider.

---

### DEVOPS-0002 — Create a production-readiness scorecard

**Priority:** P0

Create:

```text
docs/operations/production-readiness-scorecard.md
```

Minimum categories:

- Build and release.
- Supply-chain security.
- Infrastructure.
- Kubernetes runtime.
- Secrets and identity.
- Networking.
- Data services.
- Observability.
- Incident response.
- Backup and disaster recovery.
- Capacity and performance.
- Operational ownership.

Use the following statuses:

- `NOT_STARTED`
- `IN_PROGRESS`
- `BLOCKED`
- `PASS`
- `WAIVED`

**Acceptance criteria:**

- Every gate has an owner.
- Every gate has an evidence link.
- No production gate is marked complete based only on an unsupported claim.

---

### DEVOPS-0003 — Record the current baseline

**Priority:** P1

Record:

- Current CI duration.
- Flaky-test rate.
- Container image size.
- Number of secrets distributed to each workload.
- Number of application replicas.
- Current recovery-test results.
- Current benchmark results.
- Current unresolved chaos scenarios.
- Current security findings.

**Deliverable:**

```text
docs/operations/baselines/2026-devops-baseline.md
```

---

# Phase 1 — Secret Isolation and Workload Identity

## Goal

Remove high-risk shared credentials and ensure every workload has a narrowly scoped identity and secret set.

## Duration

1–2 weeks.

## Exit Criteria

- No operator private key is mounted into an application pod.
- No database superuser credential is available to a normal application workload.
- Secret distribution is documented per service.
- Workloads can access cloud resources without static cloud access keys.

---

### DEVOPS-1001 — Remove the operator private key from application pods

**Priority:** P0

Application pods must not mount or receive:

- Operator certificates.
- Operator private keys.
- Shared administrative identities.

**Implementation:**

1. Identify all mounts and environment variables associated with operator identity.
2. Move operator identity into a dedicated administrative tool or controlled operator job.
3. Create a separate service identity for each workload.
4. Ensure a compromised application pod cannot act as an operator.

**Acceptance criteria:**

- Rendered Helm manifests contain no operator private key in application pods.
- A policy test fails if the key is mounted again.
- Administrative flows continue to work through a dedicated identity.

---

### DEVOPS-1002 — Create a secret inventory and access matrix

**Priority:** P0

Create a matrix such as:

| Secret | Gateway | Auth | Ledger | Payin | Payout | Vendor | Assurance | Admin |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| JWT signing key |  |  |  |  |  |  |  |  |
| Database credential |  |  |  |  |  |  |  |  |
| Encryption key |  |  |  |  |  |  |  |  |
| Vendor credential |  |  |  |  |  |  |  |  |
| Operator credential |  |  |  |  |  |  |  |  |

**Rules:**

- Every secret must have an owner.
- Every secret must have a rotation interval.
- Every secret must have a revocation procedure.
- Every shared secret must have a written justification.

**Deliverable:**

```text
docs/security/secret-inventory.md
```

---

### DEVOPS-1003 — Integrate cloud workload identity

**Priority:** P0

Use one of the following:

- GCP Workload Identity Federation for GKE.
- AWS IAM Roles for Service Accounts.

**Acceptance criteria:**

- No long-lived cloud access key exists inside a pod.
- Kubernetes service accounts map to separate cloud identities per workload.
- Access is denied to unauthorized workloads.
- Cloud audit logs show the actual workload principal.

---

### DEVOPS-1004 — Use external secret delivery

**Priority:** P0

Implementation options:

- External Secrets Operator.
- Secrets Store CSI Driver.
- Provider-native secret integration.

**Requirements:**

- Secret values do not exist in Git.
- Secret values do not exist in Terraform state.
- Secrets can be rotated without rebuilding application images.
- Secret access is recorded in cloud audit logs.

---

### DEVOPS-1005 — Separate database roles

**Priority:** P0

Minimum roles:

- Runtime read/write role per service or service group.
- Read-only observability role.
- Migration role.
- Backup role.
- Restore role.
- Break-glass administrator.

**Acceptance criteria:**

- Runtime workloads have no superuser privileges.
- Migration jobs do not use `postgres` or an equivalent superuser.
- Unauthorized schema changes are rejected.

---

# Phase 2 — Trusted Build and Release Pipeline

## Goal

Produce immutable, traceable, signed, scanned artifacts that can only be created through a trusted workflow.

## Duration

1–2 weeks.

## Exit Criteria

- Each image is built only once.
- Every image has a digest, SBOM, provenance record, and signature.
- Staging and production use the same artifact digest.
- The cluster can reject artifacts that do not satisfy policy.

---

### DEVOPS-2001 — Create a release workflow

**Priority:** P0

Create a workflow separate from pull-request CI:

```text
.github/workflows/release.yml
```

Recommended triggers:

- A merge to `main` produces a candidate artifact.
- A SemVer tag produces a release artifact.
- Manual promotion changes only the environment digest.

Workflow stages:

1. Check out the trusted commit.
2. Verify that required tests passed.
3. Build the container image.
4. Generate the SBOM.
5. Scan for vulnerabilities.
6. Push the immutable artifact.
7. Generate provenance.
8. Sign the artifact.
9. Publish release metadata.

---

### DEVOPS-2002 — Use an immutable registry strategy

**Priority:** P0

Options:

- Google Artifact Registry.
- Amazon ECR.
- GHCR for a non-production reference environment.

**Requirements:**

- Mutable tags are restricted.
- Production deployments reference digests.
- A registry retention policy exists.
- Image deletion is protected.
- Registry audit logging is enabled.

---

### DEVOPS-2003 — Generate an SBOM

**Priority:** P0

Recommended formats:

- SPDX JSON.
- CycloneDX JSON.

The SBOM should include:

- Operating-system packages.
- Go modules.
- Build metadata.
- Image digest.
- Commit SHA.

**Acceptance criteria:**

- An SBOM exists for every release.
- The SBOM is linked to the exact image digest.
- The release fails if SBOM generation fails.

---

### DEVOPS-2004 — Add container vulnerability scanning

**Priority:** P0

Minimum scanning scope:

- Known operating-system vulnerabilities.
- Dependency vulnerabilities.
- Container and Kubernetes misconfiguration.
- Secret leakage.

**Initial policy:**

- Block `CRITICAL` vulnerabilities with an available fix.
- Block exploitable `HIGH` vulnerabilities on the runtime path.
- Allow exceptions only when they have an owner and expiration date.

---

### DEVOPS-2005 — Add artifact provenance and signing

**Priority:** P0

Use:

- GitHub artifact attestations.
- Sigstore/Cosign keyless signing.
- Provider-native signing where required.

**Acceptance criteria:**

- The signature can be verified against the image digest.
- Provenance identifies the repository, workflow, commit, and builder.
- The release fails if signing or attestation fails.

---

### DEVOPS-2006 — Enforce trusted-artifact admission policy

**Priority:** P1

Use one of the following:

- Kyverno.
- OPA Gatekeeper.
- Provider-native binary authorization.

Minimum policies:

- Reject images using the `latest` tag.
- Reject production images that are not digest-pinned.
- Reject unsigned images.
- Reject images from unapproved registries.
- Reject workloads without resource requests and limits.
- Reject privileged workloads.

---

# Phase 3 — Infrastructure Validation and Policy as Code

## Goal

Prevent invalid, insecure, or unintended infrastructure changes before they reach a cloud account or cluster.

## Duration

1 week.

---

### DEVOPS-3001 — Create a Terraform validation pipeline

**Priority:** P0

Add the following to CI:

```bash
terraform fmt -check
terraform init -backend=false
terraform validate
terraform test
terraform plan
```

**Requirements:**

- Plans use non-production, least-privilege credentials.
- Plan artifacts are retained for review.
- Destructive changes are clearly highlighted.
- Production apply runs only from an approved plan.

---

### DEVOPS-3002 — Validate rendered Helm manifests

**Priority:** P0

Add:

```bash
helm lint
helm template
kubeconform
```

Render at minimum:

- Default values.
- Development overlay.
- Staging overlay.
- Production overlay.

**Acceptance criteria:**

- All manifests validate against the target Kubernetes version.
- No duplicate resources are generated.
- No required field is missing.

---

### DEVOPS-3003 — Add Kubernetes policy tests

**Priority:** P0

Minimum policy set:

- `runAsNonRoot` is required.
- A read-only root filesystem is required.
- `RuntimeDefault` seccomp is required.
- Privilege escalation is forbidden.
- Linux capabilities must be dropped.
- Resource requests and limits are required.
- Host networking, host PID, and host-path mounts are forbidden.
- Service-account token automount defaults to false.
- Production images must be digest-pinned.
- Operator keys are forbidden in application namespaces.

---

### DEVOPS-3004 — Add IaC security scanning

**Priority:** P1

Scan:

- Terraform.
- Kubernetes manifests.
- Rendered Helm output.
- Dockerfiles.
- GitHub Actions workflows.

Block conditions such as:

- Public databases.
- Unrestricted ingress.
- Unencrypted storage.
- Public object storage.
- Privileged containers.
- Wildcard cloud permissions.
- Plain-text secrets.

---

### DEVOPS-3005 — Add a Kind deployment smoke test

**Priority:** P1

CI should be able to:

1. Create a disposable Kind cluster.
2. Install the rendered chart.
3. Wait for pods to become ready.
4. Run health checks.
5. Run basic service-to-service smoke tests.
6. Verify important NetworkPolicies when the chosen CNI supports enforcement.

---

# Phase 4 — Production-Like Staging Environment

## Goal

Provide a cloud environment sufficiently similar to production to generate meaningful operational evidence.

## Duration

1–2 weeks.

## Exit Criteria

- Staging is provisioned through IaC.
- Deployment runs through GitOps or a controlled pipeline.
- Managed stateful services are used.
- Staging can be destroyed and recreated repeatably.

---

### DEVOPS-4001 — Separate environment configuration

**Priority:** P1

Simple option:

```text
deploy/environments/dev/
deploy/environments/staging/
deploy/environments/production/
```

More mature option:

```text
seev-environments/
  dev/
  staging/
  production/
```

Each environment stores:

- Image digest.
- Values overlay.
- Replica count.
- Resource allocation.
- Feature flags.
- Endpoint references.
- Secret-object references, never secret values.

---

### DEVOPS-4002 — Provision managed PostgreSQL

**Priority:** P0

Requirements:

- Private networking.
- Encryption at rest.
- TLS in transit.
- Automated backups.
- Point-in-time recovery.
- Multi-zone availability for production.
- Audit logging.
- Query or performance insights.
- Connection limits and pool sizing.
- Separate migration and runtime users.

**Staging requirement:**

Use a configuration sufficiently close to production so that benchmarks and failure tests remain meaningful.

---

### DEVOPS-4003 — Provision managed Redis

**Priority:** P0

Requirements:

- Private endpoint.
- Authentication.
- TLS where available.
- Failover.
- Explicit memory-eviction policy.
- Explicit persistence strategy.
- Alerts for memory pressure, connection saturation, and replication lag.

**Important:**

Redis must not be the sole source of truth for money-movement correctness.

---

### DEVOPS-4004 — Provision managed messaging

**Priority:** P0

Options depend on the selected cloud and architecture:

- Managed RabbitMQ.
- Cloud Pub/Sub.
- Managed Kafka.
- Amazon MQ.

Requirements:

- Durable messaging.
- Dead-letter handling.
- Explicit retry strategy.
- Message-age monitoring.
- Queue-depth monitoring.
- Encryption.
- Least-privilege producer and consumer identities.

---

### DEVOPS-4005 — Implement production-like ingress and egress

**Priority:** P0

Implement:

- DNS.
- Managed TLS certificates.
- Cloud load balancing.
- WAF.
- DDoS protection.
- Static vendor-facing egress IPs.
- Callback source verification.
- Trusted proxy configuration.
- Rate limiting.

**Acceptance criteria:**

- Source-IP behavior is proven from outside the cluster.
- Vendor-facing outbound IPs remain stable.
- TLS rotation can be tested.
- Administrative endpoints are not publicly exposed without strong controls.

---

# Phase 5 — High-Availability Runtime

## Goal

Remove single points of failure and ensure that losing one pod or one node does not stop a critical flow.

## Duration

1–2 weeks.

---

### DEVOPS-5001 — Define a replica strategy per service

**Priority:** P0

Classify every service:

| Tier | Example | Minimum production replicas |
|---|---|---:|
| Tier 0 | Gateway, Ledger, critical payment orchestration | 3 |
| Tier 1 | Auth, Payin, Payout, Vendor | 2–3 |
| Tier 2 | Assurance, reporting, non-critical workers | 2 or workload-based |
| Tier 3 | Admin-only or scheduled workloads | 1 with an explicit recovery strategy |

Replica counts must consider:

- Zone distribution.
- Node drain.
- Rolling deployment.
- Peak traffic.
- Failure tolerance.

---

### DEVOPS-5002 — Add topology spread and anti-affinity

**Priority:** P0

Add:

- `topologySpreadConstraints` across zones.
- `topologySpreadConstraints` across hostnames.
- Soft or hard anti-affinity based on service criticality.

**Acceptance criteria:**

- Critical replicas are not all placed on one node.
- Losing one node does not stop a service.
- Losing one zone still leaves the required minimum capacity.

---

### DEVOPS-5003 — Configure correct PodDisruptionBudgets

**Priority:** P0

PDBs must align with replica counts and service criticality.

Examples:

- Three-replica service: `minAvailable: 2`.
- Two-replica service: `minAvailable: 1`.

Do not treat a PDB as protection from node crashes; it only controls voluntary disruptions.

---

### DEVOPS-5004 — Implement graceful shutdown and draining

**Priority:** P0

Every service must:

1. Stop accepting new traffic.
2. Complete safe in-flight requests.
3. Stop consuming new messages.
4. Return or requeue unacknowledged messages.
5. Close database connections.
6. Flush telemetry.
7. Exit before the termination grace period expires.

**Acceptance tests:**

- Kill a pod while a request is running.
- Drain a node while a worker is processing a message.
- Confirm that no money movement is duplicated and no message is lost.

---

### DEVOPS-5005 — Define an autoscaling strategy

**Priority:** P1

CPU-only HPA is not sufficient.

Recommended metrics:

- HTTP concurrency.
- Request latency.
- Queue depth.
- Oldest message age.
- Outbox lag.
- Assurance lag.
- Database-pool utilization.
- Vendor response latency.

Use:

- HPA v2.
- KEDA for event-driven workloads where appropriate.
- Cluster autoscaling.

---

### DEVOPS-5006 — Create a resource and capacity model

**Priority:** P1

For each service, record:

- CPU request.
- CPU limit, or the reason for not using one.
- Memory request.
- Memory limit.
- Baseline throughput.
- Peak throughput.
- Safe concurrency.
- Database connection requirements.
- Queue-consumer count.

**Deliverable:**

```text
docs/performance/capacity-model.md
```

---

# Phase 6 — Network Segmentation

## Goal

Move from namespace-level isolation to an explicit workload dependency graph.

## Duration

1 week.

---

### DEVOPS-6001 — Document the service call graph

**Priority:** P0

Create a diagram such as:

```text
Gateway   → Auth, Ledger, Payin, Payout, Fraud
Payin     → Ledger, Fraud, Vendor
Payout    → Ledger, Fraud, Vendor
Vendor    → Egress Proxy
Workers   → Approved database and broker endpoints
Admin     → Explicit administrative APIs only
```

**Deliverable:**

```text
docs/architecture/network-call-graph.md
```

---

### DEVOPS-6002 — Create NetworkPolicies per workload

**Priority:** P0

Each policy should define:

- Source pod selector.
- Destination pod selector.
- Port.
- Protocol.
- Namespace.
- External endpoint where necessary.

**Acceptance criteria:**

- Gateway cannot access the database directly unless required.
- Payin cannot call internal services outside the documented call graph.
- A compromised service cannot perform unrestricted lateral movement.

---

### DEVOPS-6003 — Add automated network-policy tests

**Priority:** P1

Example matrix:

| Source | Destination | Expected |
|---|---|---|
| Gateway | Auth | Allow |
| Gateway | PostgreSQL | Deny unless required |
| Payin | Ledger | Allow |
| Payin | Admin | Deny |
| Assurance | External vendor | Deny unless required |
| Unknown pod | Any service | Deny |

---

# Phase 7 — Deployment and Promotion Governance

## Goal

Make deployments controlled, auditable, progressive, and safely reversible.

## Duration

1–2 weeks.

---

### DEVOPS-7001 — Implement GitOps promotion by digest

**Priority:** P0

Flow:

```text
Release workflow creates an image digest
        ↓
Automated PR updates the development digest
        ↓
Development verification
        ↓
PR promotes the same digest to staging
        ↓
Staging tests and approval
        ↓
PR promotes the same digest to production
```

There must be no rebuild between environments.

---

### DEVOPS-7002 — Protect deployment environments

**Priority:** P0

The production environment must have:

- Required reviewers.
- Branch protection.
- No self-approval where supported.
- Restricted environment secrets.
- A deployment audit trail.
- An emergency break-glass path.

---

### DEVOPS-7003 — Implement progressive deployment

**Priority:** P1

Choose one of the following:

- Canary deployment.
- Blue/green deployment.
- Progressive traffic shifting.

Example initial canary:

```text
5% traffic for 10 minutes
25% traffic for 15 minutes
50% traffic for 15 minutes
100% traffic
```

Progress only when:

- Error rate is healthy.
- Latency is healthy.
- Queue age is healthy.
- Database saturation is healthy.
- Business invariants remain valid.

---

### DEVOPS-7004 — Add automated rollback gates

**Priority:** P1

Rollback triggers may include:

- Fast SLO burn.
- Rising error rate.
- P99 latency exceeding its threshold.
- Crash loops.
- Continuously growing queue backlog.
- Ledger-invariant failure.
- Unhandled vendor failure.

Application rollback must not assume that a database schema can be rolled back automatically.

---

### DEVOPS-7005 — Deliver database migrations safely

**Priority:** P0

Use the expand-and-contract pattern:

1. Add a backward-compatible schema change.
2. Deploy an application version supporting both old and new schemas.
3. Backfill data.
4. Switch the read/write path.
5. Observe the new path.
6. Remove the old schema in a separate release.

Migration requirements:

- Dedicated migration role.
- Statement timeout.
- Lock timeout.
- Dry-run or preflight checks.
- Explicit approval for destructive migration.
- Auditable migration history.

---

# Phase 8 — Observability and SRE Operations

## Goal

Turn the observability stack into an operational system for detection, diagnosis, response, and release control.

## Duration

1–2 weeks.

---

### DEVOPS-8001 — Define a standard observability contract

**Priority:** P0

Every service must emit:

- Structured logs.
- Request ID.
- Trace ID.
- Service name.
- Environment.
- Release version or digest.
- Latency metrics.
- Error metrics.
- Saturation metrics.
- Dependency metrics.

Sensitive data must be masked.

---

### DEVOPS-8002 — Add business-critical metrics

**Priority:** P0

Minimum metrics:

- Payment-command success and failure.
- Ledger-posting success and failure.
- Duplicate-command detection.
- Outbox lag.
- Oldest queue-message age.
- Webhook-delivery latency.
- Vendor latency and error rate.
- Reconciliation mismatches.
- Assurance backlog.
- Pending-transaction age.
- Database connection-pool saturation.
- Database lock waits.

---

### DEVOPS-8003 — Implement SLO burn-rate alerting

**Priority:** P0

For every SLO, create:

- Fast-burn page.
- Medium-burn urgent alert.
- Slow-burn ticket.

Example SLOs:

- Ledger-posting availability.
- Payment-command success rate.
- Webhook-delivery latency.
- Notification success rate.
- API availability.

---

### DEVOPS-8004 — Configure alert routing and ownership

**Priority:** P0

Every alert must include:

- Severity.
- Service owner.
- Runbook URL.
- Dashboard URL.
- Escalation path.
- Expected response time.

Tests:

- Trigger a synthetic alert.
- Verify that it reaches the intended destination.
- Verify acknowledgment and escalation.

---

### DEVOPS-8005 — Add external synthetic monitoring

**Priority:** P1

Synthetic checks should originate outside the cluster and cover:

- Public health endpoint.
- Authentication flow.
- Payment-safe synthetic flow.
- Webhook-receiver reachability.
- TLS certificate expiration.
- DNS resolution.

Use synthetic transactions that never move real money.

---

### DEVOPS-8006 — Create dashboard tiers

**Priority:** P1

Create three dashboard layers:

1. **Executive and Business**
   - Transaction volume.
   - Success rate.
   - Pending volume.
   - Reconciliation status.

2. **Service Operations**
   - RED metrics.
   - Queue lag.
   - Dependency health.

3. **Infrastructure**
   - CPU, memory, and disk.
   - Node health.
   - Database saturation.
   - Network errors.

---

# Phase 9 — Backup, Restore, and Disaster Recovery

## Goal

Prove that critical data can be recovered within an agreed time and data-loss objective.

## Duration

1–2 weeks.

---

### DEVOPS-9001 — Define RTO and RPO

**Priority:** P0

Initial example:

| System | RPO | RTO |
|---|---:|---:|
| Ledger database | ≤ 5 minutes | ≤ 60 minutes |
| Payment operational database | ≤ 5 minutes | ≤ 60 minutes |
| Redis cache | Rebuildable | ≤ 30 minutes |
| Message broker | Based on the durability model | ≤ 60 minutes |
| Observability data | ≤ 24 hours | ≤ 4 hours |

These values must be validated against business requirements.

---

### DEVOPS-9002 — Implement the backup architecture

**Priority:** P0

Requirements:

- Automated backups.
- Point-in-time recovery.
- Backup encryption.
- Backup-retention policy.
- Backup-deletion protection.
- Copies stored in a separate account, project, or failure domain.
- Dedicated backup-access role.
- Backup-integrity verification.

---

### DEVOPS-9003 — Automate restore drills

**Priority:** P0

Minimum drill coverage:

- Restore the latest backup.
- Perform PITR to a selected timestamp.
- Validate the schema.
- Validate row counts.
- Validate ledger invariants.
- Run a smoke test.
- Record restoration duration.

**Acceptance criteria:**

- RTO and RPO are met.
- Each drill produces an evidence artifact.
- Failed drills automatically create an issue or action item.

---

### DEVOPS-9004 — Create a disaster-recovery runbook

**Priority:** P0

The runbook must cover:

- Declaring a disaster.
- Communication ownership.
- Freezing writes or redirecting traffic.
- Restoration procedure.
- Reconnecting dependencies.
- Data validation.
- Resuming traffic.
- Post-recovery reconciliation.
- Postmortem initiation.

---

# Phase 10 — Performance and Capacity Evidence

## Goal

Define the safe operating envelope instead of merely finding the highest achievable RPS.

## Duration

1–2 weeks.

---

### DEVOPS-10001 — Produce a baseline benchmark report

**Priority:** P0

Create:

```text
docs/performance/reports/2026-baseline.md
```

Minimum scenarios:

- Normal P2P transfer.
- Hot-account contention.
- Webhook burst.
- Mixed pay-in, transfer, and payout.

Minimum metrics:

- RPS.
- p50, p95, and p99 latency.
- Error rate.
- Outbox lag.
- Queue age.
- Connection-pool utilization.
- Database lock waits.
- CPU and memory.

---

### DEVOPS-10002 — Define load-test profiles

**Priority:** P1

Profiles:

- Smoke load.
- Expected daily peak.
- Twice the expected peak.
- Stress until degradation.
- Multi-hour soak.
- Sudden traffic spike.

**Expected output:**

- First bottleneck.
- Degradation behavior.
- Recovery behavior.
- Safe concurrency.
- Scaling response.

---

### DEVOPS-10003 — Create a database-capacity model

**Priority:** P1

Record:

- Connection limit.
- Pool size per service.
- Maximum replica count.
- Expected queries per transaction.
- Slow queries.
- Lock contention.
- WAL volume.
- Checkpoint behavior.
- Storage growth.

Important formula:

```text
Total possible application connections
=
Sum(service replicas × maximum pool size)
```

The result must remain below the database connection budget with an explicit safety margin.

---

### DEVOPS-10004 — Add a performance-regression gate

**Priority:** P2

Fail CI or a scheduled performance workflow when:

- p95 or p99 latency regresses significantly.
- Error rate exceeds its threshold.
- Outbox lag increases significantly.
- Memory usage increases without justification.
- Throughput falls below the accepted tolerance.

Use thresholds broad enough to avoid flaky benchmark failures.

---

# Phase 11 — Chaos Engineering and Failure Evidence

## Goal

Prove that critical failure handling works repeatably in a production-like staging environment.

## Duration

1–2 weeks.

---

### DEVOPS-11001 — Close all unresolved chaos scenarios

**Priority:** P0

Every scenario must include:

- Hypothesis.
- Injection method.
- Expected state transition.
- Expected alert.
- Expected recovery behavior.
- Data-integrity validation.
- Result artifact.

No critical scenario may remain ambiguous before go-live.

---

### DEVOPS-11002 — Run runtime failure tests

**Priority:** P0

Minimum test set:

- Kill an application pod during an active request.
- Kill a worker while it is processing a message.
- Restart a node.
- Drain a node.
- Trigger database failover.
- Trigger Redis failover.
- Trigger broker failover.
- Simulate vendor timeout.
- Simulate duplicate vendor callbacks.
- Introduce network latency.
- Introduce DNS failure.
- Simulate object-storage outage.

---

### DEVOPS-11003 — Run security and credential-failure drills

**Priority:** P1

Drills:

- Rotate a database credential.
- Rotate an mTLS certificate.
- Revoke a compromised workload identity.
- Expire a vendor credential.
- Disable an operator account.
- Attempt unauthorized secret access.

---

### DEVOPS-11004 — Conduct game days

**Priority:** P1

A quarterly game day should include:

1. An unknown incident is presented to an operator.
2. The operator uses dashboards and runbooks to investigate.
3. Detection, diagnosis, mitigation, and recovery times are recorded.
4. Identified gaps become backlog items.
5. Runbooks are updated.

Ideally, the operator should not be the person who designed the failure scenario.

---

# Phase 12 — Operational Governance

## Goal

Ensure the system has clear ownership, an incident process, and auditable operational changes.

## Duration

1 week.

---

### DEVOPS-12001 — Create a service-ownership catalog

**Priority:** P0

Every service must have:

- Primary owner.
- Backup owner.
- Repository path.
- Dashboard.
- Alerts.
- Runbook.
- SLO.
- Dependencies.
- Data stores.
- Deployment process.

---

### DEVOPS-12002 — Define an incident-severity model

**Priority:** P0

Example:

| Severity | Condition | Response |
|---|---|---|
| SEV-1 | Incorrect money movement, broad outage, or data corruption | Immediate paging and incident command |
| SEV-2 | Major degradation, delayed settlement, or large backlog | Urgent response |
| SEV-3 | Limited impact with an available workaround | Business-hours resolution |
| SEV-4 | Minor issue with no customer impact | Normal backlog |

---

### DEVOPS-12003 — Establish a postmortem process

**Priority:** P1

A postmortem must cover:

- Customer impact.
- Financial impact.
- Timeline.
- Detection.
- Root causes.
- Contributing factors.
- What worked.
- What failed.
- Corrective actions.
- Owners and due dates.

Use a blameless approach without removing accountability.

---

### DEVOPS-12004 — Track DORA and operational metrics

**Priority:** P2

Track:

- Deployment frequency.
- Lead time for changes.
- Change-failure rate.
- Mean time to restore.
- Alert volume.
- False-positive rate.
- Repeated-incident rate.
- Backup-restore success rate.

---

# Phase 13 — Cost and Efficiency

## Goal

Optimize cost only after a reliable production baseline exists.

## Priority

P2–P3.

---

### DEVOPS-13001 — Implement cost allocation

Add labels or tags for:

- Environment.
- Service.
- Team.
- Cost center.
- Data classification.

Create dashboards for:

- Cost per environment.
- Cost per service.
- Database cost.
- Observability cost.
- Network-egress cost.
- Backup cost.

---

### DEVOPS-13002 — Right-size resources

Use historical evidence to adjust:

- CPU requests.
- Memory requests.
- Minimum replica counts.
- Autoscaling maximums.
- Database instance size.
- Data-retention periods.

Do not reduce critical redundancy merely to save a small amount of cost.

---

### DEVOPS-13003 — Schedule non-production resources

For sandbox environments:

- Shut down resources outside working hours where safe.
- Use ephemeral preview environments.
- Set TTLs for temporary resources.
- Configure budget alerts.
- Automate cleanup.

---

# 14. Recommended 90-Day Execution Order

## Days 1–15 — Security and artifact trust

- Lock the production-cloud decision.
- Create the readiness scorecard.
- Remove the operator key from application pods.
- Create the secret inventory.
- Implement workload identity.
- Implement external secret delivery.
- Create the release workflow.
- Generate SBOMs.
- Scan, attest, and sign images.

**Milestone:** A trusted release candidate can be produced.

---

## Days 16–30 — Validation and staging foundation

- Add Terraform validation and planning.
- Add Helm rendering and Kubeconform validation.
- Add policy as code.
- Add IaC security scanning.
- Add a Kind smoke deployment.
- Provision the staging network and cluster.
- Provision managed PostgreSQL, Redis, and messaging.

**Milestone:** A production-like staging environment exists and is reproducible through IaC.

---

## Days 31–45 — High availability and network security

- Run critical services with multiple replicas.
- Add topology spread.
- Configure PDBs.
- Test graceful shutdown.
- Implement exact workload NetworkPolicies.
- Implement cloud ingress, TLS, WAF, and static egress.
- Separate database roles.

**Milestone:** Losing one pod or node does not stop a critical path.

---

## Days 46–60 — Deployment governance and observability

- Implement GitOps digest promotion.
- Configure environment approval.
- Implement progressive deployment.
- Add rollback gates.
- Add business metrics.
- Add SLO burn-rate alerts.
- Configure alert routing.
- Add synthetic monitoring.

**Milestone:** Deployments can be performed safely and observed continuously.

---

## Days 61–75 — Disaster recovery, capacity, and chaos

- Define RTO and RPO.
- Implement cloud backup and PITR.
- Automate restore drills.
- Produce a baseline benchmark.
- Create the capacity model.
- Close unresolved chaos scenarios.
- Run runtime failure tests.

**Milestone:** Recovery behavior and the safe operating envelope are proven.

---

## Days 76–90 — Operational readiness review

- Create the service-ownership catalog.
- Define the incident-severity model.
- Review all runbooks.
- Conduct a game day.
- Conduct a credential-rotation drill.
- Complete an independent security review.
- Conduct a production-readiness review.
- Make the final go/no-go decision.

**Milestone:** Production readiness is decided from evidence rather than assumptions.

---

# 15. Pareto Backlog

When time is constrained, complete these five groups first.

## 1. Secret isolation and workload identity

Impact:

- Reduces blast radius.
- Removes shared high-privilege credentials.
- Improves auditability.

## 2. Immutable, signed release pipeline

Impact:

- Verifies the source of every artifact.
- Makes rollback deterministic.
- Prevents unknown images from reaching production.

## 3. Managed high-availability data services

Impact:

- Removes the largest single points of failure.
- Improves backup and failover capabilities.
- Reduces operational burden.

## 4. Production-like staging with DR and chaos evidence

Impact:

- Converts documentation into evidence.
- Reveals failures that do not appear locally.
- Tests actual recovery behavior.

## 5. SLO-driven alerting and deployment

Impact:

- Reduces silent failures.
- Improves incident detection.
- Prevents a bad release from reaching all traffic.

---

# 16. Go-Live Checklist

## Build and Supply Chain

- [ ] The production image is built by a trusted workflow.
- [ ] The image uses an immutable digest.
- [ ] An SBOM is available.
- [ ] Vulnerability scanning satisfies policy.
- [ ] Provenance is available.
- [ ] The image signature is verified successfully.
- [ ] Production rejects unsigned images.
- [ ] Staging and production use the same artifact digest.

## Identity and Secrets

- [ ] No operator key exists in application pods.
- [ ] Cloud access uses workload identity.
- [ ] Secrets are delivered from an external secret store.
- [ ] Database roles are separated.
- [ ] Secret rotation has been tested successfully.
- [ ] Break-glass access exists and is audited.

## Infrastructure

- [ ] Terraform plans are reviewed before apply.
- [ ] The production database is private and highly available.
- [ ] Redis has failover.
- [ ] Messaging has durability and failover.
- [ ] Backups exist in a separate failure domain.
- [ ] Budget and cost alerts are configured.

## Kubernetes

- [ ] Critical services have multiple replicas.
- [ ] Replicas are distributed across nodes and zones.
- [ ] PDBs match service criticality.
- [ ] Graceful shutdown is tested.
- [ ] Resource requests and limits are defined.
- [ ] Restricted security policies are satisfied.
- [ ] NetworkPolicies match the exact service call graph.

## Deployment

- [ ] Promotion uses immutable digests.
- [ ] Production deployment requires approval.
- [ ] Progressive deployment is available.
- [ ] Rollback has been tested.
- [ ] Database migrations are backward compatible.
- [ ] Deployment history is auditable.

## Observability

- [ ] Logs, metrics, and traces are connected.
- [ ] Business-critical metrics are available.
- [ ] SLOs are defined.
- [ ] Burn-rate alerts are active.
- [ ] Alert routing has been tested.
- [ ] Alerts link to runbooks.
- [ ] Synthetic checks run from outside the cluster.

## Disaster Recovery

- [ ] RTO and RPO are approved.
- [ ] Latest-backup restore succeeds.
- [ ] Point-in-time recovery succeeds.
- [ ] Data integrity after restore is validated.
- [ ] The DR runbook has been exercised.
- [ ] Restore duration satisfies the target.

## Performance and Reliability

- [ ] A baseline benchmark report exists.
- [ ] The safe operating envelope is known.
- [ ] The database connection budget is validated.
- [ ] Peak-load tests pass.
- [ ] Soak tests pass.
- [ ] Critical chaos scenarios are green.
- [ ] Losing one pod or node does not stop a critical flow.

## Operations

- [ ] Every service has an owner.
- [ ] An incident-severity model exists.
- [ ] An escalation path exists.
- [ ] A postmortem process exists.
- [ ] A game day has been completed.
- [ ] A security review has been completed.
- [ ] The final readiness review returns `GO`.

---

# 17. Definition of Done per Work Item

A DevOps work item is complete only when:

1. The implementation exists.
2. Automated tests exist where practical.
3. Documentation exists.
4. Security impact has been reviewed.
5. Relevant observability exists.
6. A rollback or recovery path exists.
7. Evidence is linked from the production-readiness scorecard.
8. The capability does not depend on the tribal knowledge of one person.

---

# 18. Suggested Repository Structure

```text
docs/
  adr/
  architecture/
    network-call-graph.md
  operations/
    production-readiness-scorecard.md
    service-catalog.md
    incident-severity.md
    baselines/
      2026-devops-baseline.md
    runbooks/
  performance/
    capacity-model.md
    reports/
      2026-baseline.md
  security/
    secret-inventory.md
    workload-identity.md
    artifact-trust.md

deploy/
  environments/
    dev/
    staging/
    production/
  policies/
    kubernetes/
    terraform/
  helm/
  terraform/

.github/
  workflows/
    ci.yml
    release.yml
    infrastructure-plan.yml
    deploy-dev.yml
    deploy-staging.yml
    deploy-production.yml
    dr-drill.yml
    chaos-staging.yml
```

---

# 19. Final Recommendation

Seev does not need more tooling variety at this stage. The repository already contains enough components. The best next step is to create one trusted production path and produce real operational evidence.

The most effective sequence is:

```text
Secret isolation
    ↓
Trusted artifact pipeline
    ↓
Production-like staging
    ↓
Managed highly available data services
    ↓
Progressive deployment
    ↓
SLOs and paging
    ↓
DR, performance, and chaos evidence
    ↓
Production-readiness review
```

Success should not be measured by the number of YAML files, dashboards, or workflows added. It should be measured by the ability to answer the following questions with verifiable evidence:

- Where did the production artifact come from?
- Who authorized it to run?
- What happens when one component fails?
- How quickly is a problem detected?
- Do money and data remain consistent?
- Can the system be recovered within the agreed target?
- Can another operator run the system without depending on its original author?

When every question can be answered with repeatable evidence, Seev will have moved from a strong engineering portfolio into a platform with production-grade operational maturity.
