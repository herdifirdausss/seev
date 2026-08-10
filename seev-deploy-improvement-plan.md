# Seev Deployment Improvement Plan

> Audit scope: `deploy/` on `main`  
> Audit date: 2026-08-09  
> Goal: improve deployment safety, security, repeatability, and production-readiness without adding unnecessary platform complexity.

## Executive Summary

The `deploy/` tree is already structurally strong. It has clear separation between Helm application manifests, Kubernetes platform/bootstrap assets, provider-specific Terraform, GitOps, observability, load testing, backup/recovery assets, and machine-readable deployment inventory.

The repository also already has several controls that should be preserved rather than rebuilt:

- non-production/local Kubernetes is explicitly separated from the production-shaped staging contract;
- Kubernetes workloads run non-root, use read-only root filesystems, drop Linux capabilities, use dedicated ServiceAccounts, and disable automatic ServiceAccount token mounting;
- default-deny NetworkPolicies exist;
- Helm overlays are linted in CI;
- Terraform modules are formatted and validated without contacting cloud accounts;
- deployment integration and platform E2E checks exist;
- load testing is automated;
- disaster-recovery drills already exercise latest restore and PITR paths and report RPO/RTO;
- managed PostgreSQL configurations already include HA/backup-related protections;
- secrets are intentionally kept outside committed deployment files.

Because those fundamentals are already present, the next improvements should focus on **closing permissive edges and turning the existing "production-shaped" contract into an enforceable promotion contract**.

---

# Priority Overview

| Priority | Improvement | Main Risk Reduced | Effort |
|---|---|---|---|
| P0 | Tighten service-to-service and datastore NetworkPolicies | Lateral movement / excessive blast radius | M |
| P0 | Add a strict staging promotion-readiness gate | Placeholder or unsafe config reaching staging | S-M |
| P0 | Harden GitOps promotion boundaries | Uncontrolled drift / deploying mutable `main` | M |
| P0 | Restore server identity verification for Prometheus mTLS | Metrics endpoint impersonation / incomplete TLS verification | M |
| P1 | Make rollout and draining behavior explicit | 5xx/errors during rollout or termination | M |
| P1 | Add rendered-manifest policy validation | Invalid or unsafe Kubernetes manifests passing Helm lint | M |
| P1 | Make Terraform state/bootstrap and CI identity repeatable | Manual infra bootstrap / long-lived credentials | M |
| P1 | Unify cloud backup ownership and restore evidence | Ambiguous recovery path across local/cloud | M |
| P2 | Evolve autoscaling beyond CPU-only signals | Poor scaling under IO/queue/latency pressure | M-L |
| P2 | Reduce deployment-contract drift across inventory/Helm/GitOps/observability | Documentation/config divergence | M |

---

# P0 — Must Complete Before Treating Staging as Deployment Evidence

## DEPLOY-001 — Enforce Least-Privilege Network Segmentation

### Current observation

`deploy/helm/seev/templates/networkpolicy.yaml` starts from a good default-deny posture, but two broad rules weaken the resulting segmentation:

- `app-allow-internal-services` applies to every application pod and allows ingress and egress to the entire application namespace.
- `app-allow-data` applies to every application pod and permits egress toward the data namespace.
- `deploy/helm/seev/templates/apps.yaml` currently labels every enabled service with `seev.io/db-access: "true"`.

The Redis and RabbitMQ access labels are already more selective. PostgreSQL and service-to-service access should follow the same model.

### Target state

Every allowed network edge is intentional and derived from the known service dependency contract.

Examples:

```text
gateway -> auth
gateway -> ledger
ledger  -> postgres
ledger  -> rabbitmq
vendor  -> squid
vendor  -X-> direct internet
admin-bff -> only explicitly required internal services
```

The exact matrix must come from the current deployment inventory/application behavior rather than from this example.

### Actions

- [x] Use `deploy/inventory/dependencies.yaml`, `ports.yaml`, `data-stores.yaml`, and `vendor-network.yaml` as the starting dependency contract.
- [x] Add an explicit per-service network-access definition to Helm values or generate equivalent policy input from the deployment inventory.
- [x] Remove the namespace-wide `app-allow-internal-services` allow rule.
- [x] Replace it with service-specific ingress/egress policies.
- [x] Remove the blanket `app-allow-data` rule.
- [x] Only set `seev.io/db-access: "true"` for services that actually connect to PostgreSQL.
- [x] Preserve selective Redis and RabbitMQ access and verify the lists against the dependency inventory.
- [x] Preserve the vendor-to-Squid-only outbound path.
- [x] Add negative-path network tests to the disposable Kind E2E environment.
- [x] Add a CI contract test that fails when a newly added service has unspecified network access.

### Suggested implementation shape

Prefer data-driven configuration over adding one large handwritten NetworkPolicy per service.

For example:

```yaml
services:
  ledger:
    network:
      ingressFrom:
        - gateway
      egressTo:
        services: []
        postgres: true
        redis: true
        rabbitmq: true
        external: false
```

Do not use this exact matrix without validating the real dependencies.

### Acceptance criteria

- [x] No application-wide namespace-to-namespace allow policy remains.
- [x] An unauthorized application pod cannot connect to another internal service.
- [x] A service without DB ownership/dependency cannot connect to PostgreSQL.
- [x] A service without Redis/RabbitMQ dependency cannot connect to those datastores.
- [x] `vendor` cannot reach the public internet except through the approved egress proxy path.
- [x] All expected business flows still pass platform E2E tests.
- [x] At least one negative connectivity assertion exists for every security boundary.

---

## DEPLOY-002 — Separate Helm Validity From Promotion Readiness

### Current observation

`deploy/helm/seev/values-staging.yaml` is intentionally a production-shaped template, but it still contains unresolved values such as:

- immutable application image digest;
- immutable migration image digest;
- managed PostgreSQL host;
- managed Redis address;
- managed broker host;
- certified vendor callback CIDR;
- other environment-specific endpoints/CIDRs.

This is acceptable for a template, but `helm lint` only proves that the file is structurally valid. It does not prove it is safe to promote.

### Target state

There are two explicit checks:

1. **Template validity** — development/CI can lint a placeholder staging template.
2. **Promotion readiness** — an actual staging deployment cannot proceed while any placeholder, mutable image, mock dependency, or insecure production-only setting remains.

### Actions

- [x] Add `scripts/ci/check-deploy-readiness.sh`.
- [x] Add a corresponding `make deploy-readiness ENV=staging` target.
- [x] Fail promotion when `REPLACE_WITH_*`, `UNKNOWN`, or equivalent blocker values remain.
- [x] Require application and migration images to be immutable digests for staging.
- [x] Require `data.enabled=false` for staging.
- [x] Require the mock vendor to remain disabled.
- [x] Require PostgreSQL TLS mode to satisfy the staging contract.
- [x] Require non-empty certified callback CIDRs and trusted proxy CIDRs where applicable.
- [x] Require managed datastore endpoints rather than in-cluster development services.
- [x] Require critical stateless services to satisfy the minimum replica policy.
- [x] Keep secret **values** outside Git; only validate Secret/ExternalSecret references and required secret keys.
- [x] Make the promotion workflow call the readiness check immediately before deployment.
- [x] Emit a sanitized readiness report as CI evidence.

### Optional cleanup

Choose one of these models and document it:

**Option A — Template + generated runtime values**

```text
values-staging.template.yaml
        +
Terraform outputs / environment metadata
        +
secret references
        |
        v
generated values artifact
        |
        v
promotion readiness gate
        |
        v
Helm / Argo CD
```

**Option B — Committed non-secret staging values**

Commit stable non-secret environment values, while secrets continue to come from the secret manager.

Either model is acceptable. Avoid silently mixing both.

### Acceptance criteria

- [x] `helm lint` can still validate the staging template.
- [x] `make deploy-readiness ENV=staging` fails against unresolved placeholder values.
- [x] The same command passes against a fully resolved, secretless staging configuration.
- [x] Staging deployment cannot run without the readiness gate.
- [x] The promoted image identity is an immutable digest.

---

## DEPLOY-003 — Harden GitOps Environment and Promotion Boundaries

### Current observation

The current Argo CD Applications under `deploy/gitops/applications/` cover:

- local;
- AWS dev;
- GCP dev.

The provider dev Applications currently use:

```yaml
project: default
targetRevision: main
```

There is no staging Application in the current directory.

This is appropriate for a GitOps learning handoff, but it is not yet a strong staging promotion boundary.

### Target state

GitOps promotion uses:

- a dedicated Seev `AppProject`;
- explicitly allowed repositories;
- explicitly allowed destinations/namespaces;
- restricted cluster-scoped resource permissions;
- immutable or promotion-controlled revision references;
- a staging Application only after `DEPLOY-002` is complete.

### Actions

- [x] Create `deploy/gitops/projects/seev.yaml`.
- [x] Move Seev Applications from Argo CD's `default` project into the dedicated project.
- [x] Restrict source repositories to the Seev repository.
- [x] Restrict deployment destinations to intended namespaces/clusters.
- [x] Define allowed/denied cluster-scoped resource kinds deliberately.
- [x] Keep `main` acceptable only for local learning if desired.
- [x] For cloud/staging promotion, reference an immutable commit SHA, release tag, or controlled promotion branch.
- [x] Add `seev-staging.yaml` only after the staging readiness contract can pass.
- [x] Define the intended sync behavior per environment:
  - local: self-heal is fine;
  - dev: deliberate choice between manual or automated sync;
  - staging: controlled promotion with auditable revision.
- [x] Define pruning behavior explicitly instead of relying on an accidental default.
- [x] Add validation for all Argo CD YAML to deployment CI.
- [x] Verify that GitOps never contains plaintext secret values.

### Acceptance criteria

- [x] No cloud/staging Seev Application uses `project: default`.
- [x] Staging cannot deploy an arbitrary mutable `main` revision.
- [x] A Seev Application cannot deploy to an unapproved namespace/cluster.
- [x] The promoted Git revision and application image digest are both traceable.
- [x] Staging GitOps manifests pass the same readiness contract as manual Helm deployment.

---

## DEPLOY-004 — Remove the Prometheus TLS Server-Verification Exception

### Current observation

`deploy/observability/prometheus/prometheus.yml` deliberately uses mTLS client certificates while setting:

```yaml
insecure_skip_verify: true
```

for internal Seev scrape jobs.

The repository comments explain why: the current leaf certificates use SPIFFE URI SANs but do not carry DNS SANs matching service hostnames. As a result, Prometheus cannot perform normal hostname verification.

This means client identity is still authenticated by the application, but Prometheus is intentionally not verifying the server hostname.

### Target state

Prometheus validates both:

- the server certificate chain; and
- the server identity it intended to scrape.

### Actions

- [x] Decide the canonical workload certificate identity model for Kubernetes/Compose metrics endpoints.
- [x] Extend certificate issuance so internal service certificates include the required DNS SANs, while preserving SPIFFE URI identities if they are still useful for authorization.
- [x] Alternatively configure an explicitly supported `server_name` only when it maps to an identity present in the certificate.
- [x] Remove `insecure_skip_verify: true` from Seev application scrape jobs.
- [x] Add an automated certificate/SAN test for each internal listener.
- [x] Add `promtool check config` to observability validation if not already invoked by another target.
- [x] Add a negative test showing that Prometheus rejects a certificate signed by the CA but issued for the wrong service identity.

### Acceptance criteria

- [x] Internal Seev scrape jobs work with server verification enabled.
- [x] No Seev application scrape job requires `insecure_skip_verify: true`.
- [x] Wrong-service certificates fail verification.
- [x] Existing mTLS client-identity authorization still passes.

---

# P1 — Reliability and Repeatability

## DEPLOY-005 — Make Deployment Rollout and Connection Draining Explicit

### Current observation

Application pods already have:

```yaml
terminationGracePeriodSeconds: 35
```

and strong probes/security settings, but the application Deployment template does not currently define an explicit rollout `strategy` or `preStop` lifecycle hook.

Kubernetes defaults are valid, but a money-movement system benefits from making rollout semantics an explicit deployment contract.

### Target state

A rollout does not route new traffic to a terminating pod and does not remove too much capacity during an update.

### Actions

- [x] Add configurable Deployment rollout settings.
- [x] Start with a conservative stateless policy such as:
  - `type: RollingUpdate`;
  - `maxUnavailable: 0`;
  - `maxSurge: 1`;
  - configurable per service if later required.
- [x] Add `minReadySeconds`.
- [x] Add `progressDeadlineSeconds`.
- [x] Verify application graceful shutdown behavior before blindly adding sleep-based `preStop`.
- [x] If endpoint/LB propagation needs additional drain time, add a small documented `preStop` drain hook.
- [x] Ensure readiness becomes false before the process stops accepting new business traffic.
- [x] Test rolling updates under concurrent synthetic transactions.
- [x] Assert zero unexpected 5xx/timeouts and no duplicated transaction execution during rollout.
- [x] Keep the PDB, but make its disruption budget configurable for future replica counts rather than permanently hard-coding `minAvailable: 1`.

### Acceptance criteria

- [x] Rollout parameters are explicit in rendered Deployments.
- [x] A rolling update preserves the intended minimum capacity.
- [x] Graceful termination is proven by test rather than assumed from the 35-second grace period.
- [x] The rollout E2E test does not create duplicate or partially processed business operations.

---

## DEPLOY-006 — Add Rendered Kubernetes Policy Validation

### Current observation

Deployment CI already performs:

- Terraform `fmt`;
- Terraform `validate`;
- Helm lint for base/local/AWS-dev/GCP-dev/staging;
- deployment integration checks;
- platform E2E checks;
- ShellCheck.

This is a strong baseline.

The next useful layer is validating the **rendered Kubernetes objects and repository-specific deployment invariants**, not adding another generic lint command.

### Target state

A rendered manifest must satisfy both Kubernetes schemas and Seev-specific deployment policy before merge/promotion.

### Actions

- [x] Render every Helm overlay to a temporary artifact in CI.
- [x] Validate rendered resources against Kubernetes schemas.
- [x] Add policy-as-code checks using one selected tool, for example Conftest/OPA, Kyverno CLI, or another lightweight validator.
- [x] Encode only high-value rules initially:
  - containers run as non-root;
  - privilege escalation disabled;
  - root filesystem read-only where supported;
  - capabilities dropped;
  - no host networking/host PID/host IPC;
  - resource requests/limits required;
  - readiness/liveness/startup probes present where expected;
  - ServiceAccount token automount disabled unless explicitly required;
  - non-local images use immutable digests;
  - prohibited placeholder values cannot pass promotion;
  - public Services/routes are allowlisted;
  - namespace default-deny policies remain present.
- [x] Validate Argo CD manifests in the same workflow.
- [x] Keep policy exceptions explicit, narrow, documented, and testable.

### Acceptance criteria

- [x] A syntactically valid but policy-unsafe Helm change fails CI.
- [x] All supported overlays render and validate.
- [x] Policy violations explain the resource and failed invariant clearly.
- [x] Local-only exceptions cannot silently become staging exceptions.

---

## DEPLOY-007 — Make Terraform Bootstrap and CI Identity Reproducible

### Current observation

The Terraform contract correctly requires a protected remote state backend. The provider dev documentation currently asks the operator to create/configure the state backend separately before `terraform init`.

The platform modules also deliberately keep secret values out of normal committed configuration.

### Target state

A new operator can bootstrap remote state and CI cloud identity from a small, auditable, one-time path without inventing steps manually.

### Actions

- [x] Add a separate bootstrap layer, for example:

```text
deploy/terraform/
  bootstrap/
    aws/
    gcp/
  aws/
    dev/
    platform/
  gcp/
    dev/
    platform/
```

- [x] Keep bootstrap state separate from workload/platform state.
- [x] Provision/configure:
  - versioned encrypted remote state storage;
  - state locking/concurrency protection supported by the chosen backend;
  - least-privilege CI deployment identity;
  - audit logging required for infrastructure changes.
- [x] Prefer short-lived workload/federated CI authentication over reusable long-lived cloud keys.
- [x] Add example backend configuration files with no secret material.
- [x] Document state ownership and naming per environment/provider.
- [x] Add a CI check preventing accidental local `.tfstate`, plan files, or credential files from being committed.
- [x] Document break-glass/manual apply procedure separately from normal CI promotion.

### Acceptance criteria

- [x] A clean account/project can bootstrap Terraform state by following one documented path.
- [x] Normal CI deployment does not require a long-lived static cloud access key.
- [x] Concurrent state mutation is protected.
- [x] Terraform state/plan files cannot be accidentally committed.
- [x] Bootstrap and application/platform state have separate blast radii.

---

## DEPLOY-008 — Unify Backup Ownership Across Local and Cloud Deployment Paths

### Current observation

Recovery engineering is already stronger than average:

- there is a real pgBackRest-based local DR path;
- the repository runs scheduled `latest` and PITR disaster-recovery drills;
- the drill records measured RPO/RTO;
- GCP Cloud SQL enables backups and PITR;
- AWS RDS enables backups, Multi-AZ, deletion protection, and final snapshots.

At the same time, `deploy/backup/manifests/` is currently empty except for `.gitkeep`, which can imply an unfinished Kubernetes backup mechanism even though the intended cloud path is provider-managed.

### Target state

It is obvious which component owns backup and recovery for each environment.

### Actions

- [x] Add `deploy/backup/README.md` with an environment ownership matrix:

```text
local/dev Compose -> pgBackRest test/recovery path
GCP staging/prod  -> Cloud SQL backup + PITR
AWS staging/prod  -> RDS automated backup/PITR + final snapshot policy
```

- [x] Remove the empty `manifests/` directory if Kubernetes CronJob backup is not part of the target architecture.
- [x] If Kubernetes backup agents are intentionally planned, document exactly what they back up that the managed database service does not.
- [x] Define required RPO, RTO, and retention in one machine-readable recovery policy.
- [x] Compare Terraform backup settings against that policy in CI.
- [x] Extend the existing DR evidence model so cloud restore drills can produce the same sanitized RPO/RTO result shape.
- [x] Before production, run provider-native restores into isolated disposable recovery resources.
- [x] Verify application-level invariants after restore, not only database availability.
- [x] Document who deletes recovery resources and how cost leakage is prevented.

### Acceptance criteria

- [x] There is one unambiguous backup owner per datastore/environment.
- [x] No empty directory suggests a backup mechanism that does not exist.
- [x] Terraform retention/PITR settings satisfy the declared recovery policy.
- [x] Restore success includes business-invariant verification.
- [x] Cloud restore evidence can be compared with the existing local DR drill.

---

# P2 — Optimize After the P0/P1 Contracts Are Stable

## DEPLOY-009 — Evolve HPA From CPU-Only to Workload-Aware Signals

### Current observation

The Helm HPA currently uses CPU utilization with scale-up and scale-down stabilization windows.

That is a sensible first baseline. It should not be replaced until real load evidence exists.

### Target state

Each autoscaled service scales using the smallest useful set of signals that reflect actual saturation.

### Actions

- [x] Keep CPU as the initial fallback signal.
- [x] Use existing load profiles to collect:
  - CPU;
  - memory;
  - request rate;
  - p95/p99 latency;
  - in-flight requests;
  - queue depth/consumer lag where applicable;
  - DB pool saturation.
- [x] Identify which metric becomes saturated first per service.
- [x] Add custom/external HPA metrics only for services where CPU is demonstrably insufficient.
- [x] Configure different HPA bounds per service instead of assuming one global min/max is optimal forever.
- [x] Validate scale-up against burst workloads.
- [x] Validate scale-down against delayed jobs and in-flight transaction safety.
- [x] Record the evidence used for each target value.

### Acceptance criteria

- [x] HPA targets are backed by load-test evidence.
- [x] Scale-out occurs before the service breaches its latency/error SLO.
- [x] Scale-in does not interrupt in-flight transactions.
- [x] No custom metric is added without a clear operational purpose.

---

## DEPLOY-010 — Turn Deployment Inventory Into a Cross-Layer Drift Contract

### Current observation

`deploy/inventory/` already contains machine-readable service, dependency, port, route, secret, datastore, messaging, resource, and vendor-network information.

That is valuable, but the highest return comes when changes to those contracts are checked against the actual manifests that consume them.

### Target state

A change to a service boundary cannot update only one of:

- inventory;
- Helm;
- GitOps;
- observability;
- Terraform integration;
- deployment documentation.

### Actions

- [x] Define which files are authoritative for each fact.
- [x] Do **not** make every file a source of truth.
- [x] Suggested ownership:
  - service dependencies/ports/data access -> deployment inventory;
  - Kubernetes rendering mechanics -> Helm;
  - environment infrastructure endpoints/identity -> Terraform outputs;
  - promotion revision -> GitOps;
  - alert thresholds/SLOs -> observability configuration.
- [x] Extend existing inventory validation to compare high-value cross-layer facts.
- [x] Detect at minimum:
  - inventory service missing from Helm;
  - port mismatch;
  - missing ServiceAccount;
  - missing Prometheus target/service discovery;
  - NetworkPolicy dependency not represented in inventory;
  - public route not represented in route inventory;
  - secret consumer not represented in secret inventory.
- [x] Add a short "how to add a service" checklist that names every contract that must change.
- [x] Keep generated files clearly marked if generation is introduced.

### Acceptance criteria

- [x] A new service cannot be merged with only a Deployment and no inventory contract.
- [x] Port/network/secret drift is detected automatically.
- [x] Each deployment fact has exactly one documented authoritative owner.
- [x] Onboarding documentation explains the dependency chain from inventory to deployment.

---

# Suggested Execution Order

Do not implement all items in parallel. Use the following order to minimize rework.

## Phase 1 — Security Boundary Closure

- [x] DEPLOY-001 — Least-privilege NetworkPolicies
- [x] DEPLOY-004 — Prometheus server identity verification

Why first: these change fundamental trust boundaries. They should stabilize before promotion automation becomes more permissive.

## Phase 2 — Promotion Contract

- [x] DEPLOY-002 — Staging readiness gate
- [x] DEPLOY-006 — Rendered manifest/policy validation
- [x] DEPLOY-003 — GitOps hardening + staging Application

Why second: the system should know what "deployable" means before GitOps is allowed to deploy it automatically.

## Phase 3 — Runtime Reliability

- [x] DEPLOY-005 — Explicit rollout/draining
- [x] Run rollout tests with the existing disposable E2E/load environment.

## Phase 4 — Cloud Operational Repeatability

- [x] DEPLOY-007 — Terraform bootstrap/state/CI identity
- [x] DEPLOY-008 — Backup ownership + provider-native restore evidence

## Phase 5 — Evidence-Driven Optimization

- [x] DEPLOY-009 — Workload-aware autoscaling
- [x] DEPLOY-010 — Cross-layer drift contract

---

# Recommended CI End State

A deployment-related PR should converge on a pipeline similar to:

```text
1. inventory/schema checks
2. Terraform fmt + validate
3. IaC security/policy checks
4. Helm lint
5. Helm render for every supported overlay
6. Kubernetes schema validation
7. Seev policy-as-code validation
8. platform integration contract
9. disposable platform E2E
10. promotion-readiness check (only for promotable environment artifacts)
11. GitOps manifest validation
```

Scheduled/operational workflows remain separate:

```text
- load/capacity smoke
- disaster-recovery game day
- provider-native restore rehearsal
- drift/readiness verification
```

---

# Changes I Would Not Make Yet

Avoid these until there is evidence that the current approach cannot satisfy requirements:

- [x] Do not introduce a service mesh only to solve the current NetworkPolicy or certificate problem.
- [x] Do not replace Helm with another templating system.
- [x] Do not replace Terraform with another IaC framework.
- [x] Do not create a custom Kubernetes operator for Seev deployment.
- [x] Do not move all observability into Kubernetes solely for architectural symmetry.
- [x] Do not add multiple policy engines; choose one.
- [x] Do not add custom HPA metrics before load evidence identifies a real CPU-scaling gap.
- [x] Do not create a second backup mechanism when provider-native backup is already the intended cloud owner.

The repository will benefit more from **stronger contracts and automated verification** than from additional infrastructure products.

---

# Definition of Done for "Production-Shaped Deployment"

A staging deployment should only be described as production-shaped evidence when all of the following are true:

- [x] No unresolved deployment placeholders.
- [x] Immutable application and migration image digests.
- [x] Managed private datastores with required transport security.
- [x] Mock vendor disabled.
- [x] Certified callback/trusted-proxy CIDRs resolved.
- [x] Dedicated GitOps project and controlled revision.
- [x] Least-privilege service/network/datastore access.
- [x] Prometheus verifies server identity.
- [x] Explicit safe rollout behavior.
- [x] Rendered Kubernetes manifests pass schema + repository policy checks.
- [x] Secrets come from the approved secret-management path.
- [x] Backup/PITR policy is enabled and restore evidence exists.
- [x] Readiness, SLO, load, rollback, and DR evidence are attached to the promotion.
- [x] No learning-only overlay is used as staging evidence.

---

# First Pull Requests to Create

If converting this plan directly into work items, start with these PRs:

### PR 1 — `deploy: enforce least-privilege network policy contract`

Scope:

- service-specific internal connectivity;
- selective PostgreSQL access labels;
- negative Kind connectivity tests;
- CI network-contract validation.

### PR 2 — `deploy: add staging promotion readiness gate`

Scope:

- placeholder detection;
- immutable image checks;
- staging security/replica/datastore invariants;
- `make deploy-readiness ENV=staging`.

### PR 3 — `deploy: harden internal observability TLS verification`

Scope:

- DNS/service identity SAN strategy;
- remove Prometheus `insecure_skip_verify`;
- positive and wrong-identity negative TLS tests.

### PR 4 — `gitops: isolate seev applications and pin promotable revisions`

Scope:

- dedicated `AppProject`;
- destination/resource restrictions;
- controlled cloud revisions;
- staging Application after readiness gate exists.

### PR 5 — `deploy: make rollout safety explicit`

Scope:

- configurable RollingUpdate strategy;
- minimum readiness / progress deadline;
- tested graceful draining;
- configurable PDB policy.

---

# Final Recommendation

The current `deploy/` directory does **not** need a large restructuring.

The highest-value next step is to preserve the existing folder boundaries and turn the deployment assets into a stronger executable contract:

```text
inventory
   ↓
least-privilege policies
   ↓
rendered manifests
   ↓
policy + readiness validation
   ↓
immutable GitOps promotion
   ↓
runtime / recovery evidence
```

That path improves security, onboarding, repeatability, and future production readiness while keeping the system understandable and avoiding premature platform complexity.
