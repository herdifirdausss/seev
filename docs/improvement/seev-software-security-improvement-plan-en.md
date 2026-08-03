# Seev Software Security Improvement Plan

> **Repository:** `herdifirdausss/seev`  
> **Repository status:** Not yet used in production  
> **Document purpose:** Provide a structured roadmap for evolving Seev from a security-focused reference project into a system with production-grade security foundations.  
> **Scope:** Software security, workload isolation, identity, secrets management, software supply-chain security, application security, infrastructure security, and operational assurance.

---

## 1. Executive Summary

Seev already has a strong security foundation for a personal or open-source portfolio project:

- A living and realistic threat model.
- Mutual TLS between services.
- Workload identity.
- Application-level envelope encryption.
- Database identity separation.
- Secure container baselines.
- Kubernetes network policies.
- Financial invariants and idempotency.
- Disaster-recovery drills.
- Security-aware integration, chaos, race, and contract testing.
- Private vulnerability reporting.

However, the repository is not ready for production because several risks still have a large blast radius:

1. The `dev-operator` private key is distributed to application pods.
2. Cryptographic secrets with different purposes are mounted too broadly.
3. JWT authentication uses a shared symmetric signing secret.
4. Production safety still depends on manual configuration discipline.
5. The software supply chain does not yet enforce SBOMs, signing, provenance, and admission verification.
6. Network policies do not fully reflect the actual service call graph.
7. Data-plane and operational-environment protections are incomplete.
8. There is not yet sufficient evidence from adversarial testing and independent security validation.

The primary principle of this roadmap is:

> Reduce blast radius and strengthen trust boundaries before adding more security tools.

---

# 2. Security Target State

The target state for Seev is:

- Compromise of one service does not automatically compromise other services.
- Each workload receives only its own identity and secrets.
- Services that verify JWTs cannot mint JWTs.
- Production configuration always fails closed.
- Every deployment artifact has verifiable integrity and provenance.
- All sensitive communication uses authenticated encryption.
- Sensitive operations are fully auditable.
- Keys, certificates, and credentials can be rotated safely.
- Recovery capability is proven through regular drills.
- Security controls have enforcement evidence, not only documentation.
- The threat model reflects the actual deployment environment.

---

# 3. Prioritization Model

## Priority Levels

| Level | Definition |
|---|---|
| P0 | Production blocker or issue that enables compromise across trust boundaries |
| P1 | High-risk issue that must be completed before public production |
| P2 | Advanced hardening and security-assurance improvement |
| P3 | Continuous improvement and security maturity work |

## Status Values

Use the following statuses during implementation:

- `NOT_STARTED`
- `IN_PROGRESS`
- `BLOCKED`
- `READY_FOR_REVIEW`
- `VERIFIED`
- `ACCEPTED_RISK`

## Definition of Done

A security task is not complete merely because the code has been implemented.

Every item should include:

1. Implementation.
2. Automated tests.
3. Negative tests.
4. Deployment verification.
5. Verification evidence.
6. Documentation or a runbook.
7. Review by someone other than the implementer, where possible.

---

# 4. Phase 0 — Establish the Security Baseline

**Objective:** Make the current security posture measurable and prevent undocumented security drift.

**Estimated effort:** 3–5 working days.

## 4.1 Create a Security Control Inventory

Create:

```text
docs/security/control-inventory.md
```

Minimum structure:

| Control | Owner | Location | Enforcement | Verification | Status |
|---|---|---|---|---|---|
| Internal mTLS | Platform | `pkg/tlsx` | Runtime | Integration test | Existing |
| JWT validation | Auth | Middleware | Runtime | Unit/integration test | Existing |
| Secret isolation | Platform | Helm | Deployment | Rendered manifest test | Missing |
| Artifact signing | Platform | CI/CD | Admission | Signature verification | Missing |

### Acceptance criteria

- Every security-critical control is recorded.
- Every control has an owner.
- Each control distinguishes `documented`, `implemented`, and `verified`.
- Controls without verification are explicitly marked as gaps.

---

## 4.2 Create a Security Risk Register

Create:

```text
docs/security/risk-register.md
```

Minimum fields:

- Risk ID.
- Asset.
- Threat scenario.
- Existing controls.
- Likelihood.
- Impact.
- Severity.
- Mitigation.
- Residual risk.
- Owner.
- Due date.
- Verification evidence.
- Status.

Add every P0, P1, and P2 finding from this plan.

---

## 4.3 Define a Production Security Profile

Introduce explicit environment profiles:

```text
local
test
staging
production
```

Document which behavior is allowed in each environment.

Example:

| Setting | Local | Staging | Production |
|---|---:|---:|---:|
| HTTP public edge | Allowed | Conditional | Forbidden without trusted TLS termination |
| PostgreSQL `sslmode=disable` | Allowed | Forbidden | Forbidden |
| Development certificates | Allowed | Forbidden | Forbidden |
| Insecure admin cookie | Allowed | Forbidden | Forbidden |
| Mock vendor | Allowed | Allowed with isolation | Forbidden |
| Plain HTTP Vault | Allowed | Forbidden | Forbidden |

### Acceptance criteria

- All unsafe development behavior is documented.
- The production profile has explicit security invariants.
- CI automatically verifies as many invariants as possible.

---

# 5. Phase 1 — P0 Blast-Radius Reduction

**Objective:** Remove compromise paths that cross service and privilege boundaries.

**Estimated effort:** 1–2 weeks.

---

## 5.1 Isolate Private Keys Per Workload

### Problem

Application pods receive mTLS material that does not belong to the workload, including the `dev-operator` private key.

### Target state

Each workload receives only:

- its own private key;
- its own certificate;
- a trusted CA bundle;
- no operator private key;
- no peer-service private keys.

Example:

```text
auth-mtls-secret
gateway-mtls-secret
ledger-mtls-secret
payin-mtls-secret
payout-mtls-secret
admin-bff-mtls-secret
```

### Tasks

- [ ] Inventory all certificates and private keys.
- [ ] Map each identity to its owning workload.
- [ ] Split Kubernetes Secrets per workload.
- [ ] Remove `dev-operator-key.pem` from every application pod.
- [ ] Create a dedicated operator workload.
- [ ] Restrict operator endpoints using exact workload identity.
- [ ] Ensure health probes do not require the operator private key.
- [ ] If probes require client certificates, create a dedicated low-privilege probe identity.
- [ ] Add rendered Helm-manifest tests.
- [ ] Add a test that fails when one pod contains more than one workload private key.
- [ ] Document certificate issuance and rotation.

### Automated checks

Rendered manifests must enforce:

```text
Application pod:
- contains exactly one workload private key;
- contains no operator private key;
- contains no peer-service private key;
- contains a trusted CA bundle;
- mounts key material read-only.
```

### Acceptance criteria

- Compromise of one pod does not expose another service identity.
- `dev-operator` credentials exist only in a dedicated operator environment.
- Incorrect SPIFFE or workload URI identities are rejected.
- Certificates belonging to other services are rejected.
- Expired certificates cause connection failure.
- Rotation succeeds without significant downtime.

### Evidence

- Rendered Helm manifests.
- Negative mTLS integration tests.
- Certificate-rotation test results.
- Updated threat model.

---

## 5.2 Isolate Secrets by Owner and Purpose

### Problem

Cryptographic secrets with different responsibilities are mounted too broadly across services.

### Target structure

```text
auth-pii-encryption-key
auth-pii-lookup-key
ledger-idempotency-hmac-key
gateway-api-key-pepper
admin-session-encryption-key
vendor-webhook-verification-key
```

### Tasks

- [ ] Inventory all secrets.
- [ ] Identify the owner of every secret.
- [ ] Identify all legitimate consumers.
- [ ] Split Secrets by service and purpose.
- [ ] Remove global cryptographic Secrets.
- [ ] Use read-only mounts.
- [ ] Prevent secrets from appearing in environment dumps.
- [ ] Prevent secrets from appearing in startup logs.
- [ ] Add CI checks for secret-to-workload mappings.
- [ ] Create a rotation procedure.
- [ ] Create an emergency-revocation procedure.

### Example mapping

| Secret | Owner | Consumer |
|---|---|---|
| Auth PII encryption key | Auth | Auth only |
| Auth lookup HMAC key | Auth | Auth only |
| Ledger idempotency HMAC key | Ledger | Ledger only |
| Merchant API-key pepper | Gateway | Gateway only |
| Admin session key | Admin | Admin BFF only |

### Acceptance criteria

- No global secret is shared across domains without a documented justification.
- Compromise of service A cannot decrypt data owned by service B.
- CI verifies secret mappings.
- Rotation is proven in staging.

---

## 5.3 Migrate JWTs from Symmetric to Asymmetric Signing

### Problem

With HS256, any service that can verify a token can also create one.

### Target architecture

```text
Auth service
  └── owns the private signing key

Gateway and application services
  └── receive public verification keys only
```

### Recommended algorithms

Priority order:

1. Ed25519/EdDSA.
2. ES256.
3. RS256 when compatibility is the main requirement.

### Required claims

- `iss`
- `sub`
- `aud`
- `iat`
- `nbf`
- `exp`
- `kid`
- minimal roles or permissions
- optional `jti` for high-risk tokens

### Tasks

- [ ] Select the signing algorithm.
- [ ] Create versioned signing keys.
- [ ] Add `kid`.
- [ ] Implement public-key distribution.
- [ ] Enforce strict issuer validation.
- [ ] Enforce strict audience validation.
- [ ] Configure bounded clock skew.
- [ ] Reduce access-token lifetime.
- [ ] Support overlapping keys during rotation.
- [ ] Create an emergency key-revocation procedure.
- [ ] Migrate all token verifiers.
- [ ] Remove the shared symmetric signing secret.
- [ ] Add malformed-token fuzzing.
- [ ] Add algorithm-confusion negative tests.
- [ ] Add wrong-key and unknown-`kid` tests.

### Migration strategy

1. Auth begins creating asymmetric tokens.
2. Verifiers temporarily accept both old and new tokens.
3. All clients move to the new token format.
4. Wait until all old tokens expire.
5. Disable the symmetric verifier.
6. Revoke and remove the shared secret.
7. Remove compatibility code.

### Acceptance criteria

- Only Auth owns the private signing key.
- Application services receive only public keys.
- Invalid `aud`, `iss`, `kid`, signatures, and algorithms are rejected.
- Key rotation succeeds without downtime.
- No application pod contains the old shared JWT secret.

---

## 5.4 Add a Fail-Closed Production Configuration Guard

### Problem

Unsafe settings can still reach production through human error.

### Implementation

Create a centralized validation package, for example:

```text
internal/configguard
```

When the environment is `production`, application startup must fail if:

- any secret still uses a placeholder value;
- a secret is too short;
- PostgreSQL uses `sslmode=disable`;
- insecure cookies are enabled;
- Vault uses HTTP;
- telemetry exporters use insecure transport;
- development CAs are used;
- development certificates are used;
- wildcard CORS is enabled;
- mock vendors are enabled;
- debug endpoints are enabled;
- the public edge does not declare trusted TLS termination;
- JWT issuer or audience is empty;
- trusted-proxy configuration is overly permissive;
- default database credentials are used;
- an admin endpoint is exposed without access restrictions.

### Tasks

- [ ] Define all production security invariants.
- [ ] Implement a centralized validator.
- [ ] Run validation before opening any listener.
- [ ] Add unit tests for every unsafe setting.
- [ ] Add integration tests proving the process exits with a non-zero status.
- [ ] Add a dedicated production-configuration CI job.
- [ ] Ensure errors are actionable without exposing secret values.

### Acceptance criteria

- No unsafe production configuration produces only a warning.
- Every P0 misconfiguration causes startup failure.
- Error messages are actionable and do not reveal sensitive values.

---

# 6. Phase 2 — Identity, Authentication, and Authorization Hardening

**Objective:** Strengthen human identity, machine identity, token lifecycle, and authorization.

**Estimated effort:** 1–2 weeks.

---

## 6.1 Make Refresh-Token Rotation Atomic

### Target transaction

```text
BEGIN
SELECT token FOR UPDATE
validate token state and token family
mark the current token as consumed
insert exactly one successor token
COMMIT
return the successor
```

### Required invariants

- One refresh token can have only one successor.
- A consumed refresh token cannot be used again.
- Replay revokes the token family or session according to policy.
- A token is never returned before the database transaction commits.
- Concurrent refresh attempts produce exactly one winner.

### Tasks

- [ ] Add row-level locking.
- [ ] Add a unique parent-token relationship.
- [ ] Add token-family IDs.
- [ ] Add consumed timestamps.
- [ ] Implement token-reuse detection.
- [ ] Implement family-level revocation.
- [ ] Add concurrent-refresh integration tests.
- [ ] Add crash-before-commit tests.
- [ ] Add crash-after-commit tests.
- [ ] Add telemetry that never records token values.

---

## 6.2 Strengthen Administrator Authentication

### Required controls

- Phishing-resistant MFA using WebAuthn or passkeys.
- Step-up authentication for high-risk operations.
- Short session lifetime.
- Idle timeout.
- Device and session visibility.
- Session revocation.
- Break-glass accounts.
- Dual control for selected operations.
- Mandatory reason or ticket reference for sensitive actions.

### High-risk operations

- Manual ledger correction.
- User-identity override.
- KYC approval or rejection.
- Key rotation.
- Credential reset.
- Vendor-configuration changes.
- Privilege grants.
- Security-setting changes.
- Sensitive data export.

### Acceptance criteria

- Password-only authentication is insufficient for privileged operators.
- Sensitive actions require re-authentication.
- Every privileged action is written to an immutable audit trail.
- Break-glass usage generates an alert.

---

## 6.3 Implement Explicit Authorization

### Tasks

- [ ] Map every endpoint to required permissions.
- [ ] Avoid authorization based only on role names.
- [ ] Use explicit permissions or capabilities.
- [ ] Enforce default deny.
- [ ] Separate authentication and authorization middleware.
- [ ] Add resource-ownership validation.
- [ ] Add cross-tenant negative tests.
- [ ] Add privilege-escalation tests.
- [ ] Add forbidden-state-transition tests.

### Recommended decision model

```text
Subject
+ Permission
+ Resource
+ Tenant
+ Context
= Authorization decision
```

---

## 6.4 Minimize Sensitive Data in JWTs

### Tasks

- [ ] Remove email when it is not required by all verifiers.
- [ ] Use opaque subject identifiers.
- [ ] Use audience-specific tokens.
- [ ] Avoid storing dynamic profile data in tokens.
- [ ] Never include credentials or sensitive PII.
- [ ] Prevent token values from entering logs or traces.
- [ ] Add telemetry scrubbing.

---

# 7. Phase 3 — Network and Data-Plane Security

**Objective:** Ensure network reachability and data-plane credentials follow least privilege.

**Estimated effort:** 1–2 weeks.

---

## 7.1 Create Network Policies Per Service

### Current concern

Default deny is a strong foundation, but internal access should match the actual service call graph.

### Target example

```text
Gateway    → Auth, Pay-in, Payout, Admin BFF
Pay-in     → Ledger, Vendor Adapter
Payout     → Ledger, Vendor Adapter
Admin BFF  → explicitly approved services
Ledger     → Database, Message Broker
Worker     → Database, Message Broker
```

### Tasks

- [ ] Document the service call graph.
- [ ] Create one policy per service.
- [ ] Restrict destination ports.
- [ ] Restrict namespaces.
- [ ] Restrict pod labels.
- [ ] Use service-account-aware policy when supported.
- [ ] Restrict DNS egress.
- [ ] Restrict telemetry egress.
- [ ] Route vendor egress through an approved proxy or NAT.
- [ ] Add network-policy connectivity tests.
- [ ] Add denied-path tests.

### Acceptance criteria

- A service cannot access services it does not require.
- A service cannot access another service's database.
- Vendor traffic leaves through approved paths only.
- Denied flows are proven through tests.

---

## 7.2 Enable TLS and Per-Service Credentials for the Data Plane

### PostgreSQL

- Enable TLS.
- Validate server certificates.
- Use `verify-full` where possible.
- Separate application and migration identities.
- Use separate database users per service.
- Do not grant ownership to application users.
- Configure statement timeouts.
- Configure role-level connection limits.
- Audit privileged queries.

### Redis

- Enable TLS.
- Enable ACLs.
- Use separate credentials per service.
- Restrict key prefixes or commands where possible.
- Disable dangerous commands.
- Protect administrative endpoints.

### RabbitMQ

- Enable TLS.
- Use one account per service.
- Enforce virtual-host permissions.
- Restrict publish and consume routing keys.
- Disable remote guest access.
- Audit the management plane.

### OpenTelemetry

- Use TLS or mTLS.
- Redact credentials and PII.
- Restrict collector ingress.
- Restrict exporter destinations.
- Define retention policies.

### Acceptance criteria

- No shared data-plane credentials across services.
- Plaintext internal transport is not used in production.
- Compromise of one service credential does not grant access to the entire data plane.

---

## 7.3 Adopt a Secret Manager and KMS

### Target state

- Secrets are stored in an approved managed secret store.
- Master keys remain in a KMS or HSM.
- Workloads retrieve secrets based on workload identity.
- Access is audited.
- Rotation is supported.
- Master keys are never stored in images or Git.

### Tasks

- [ ] Select a secret manager.
- [ ] Select a KMS.
- [ ] Map IAM permissions.
- [ ] Use workload identity.
- [ ] Implement key versions.
- [ ] Implement rotation.
- [ ] Create a re-encryption job.
- [ ] Create an old-key revocation procedure.
- [ ] Create audit alerts.
- [ ] Test KMS unavailability.
- [ ] Define safe caching behavior.

---

# 8. Phase 4 — Software Supply-Chain Security

**Objective:** Ensure every artifact is traceable, verifiable, and rejected when unauthorized.

**Estimated effort:** 1 week.

---

## 8.1 Pin Relevant Dependencies and Build Inputs

### Tasks

- [ ] Pin GitHub Actions to commit SHAs.
- [ ] Pin base images by digest.
- [ ] Pin tool images by digest.
- [ ] Avoid mutable production tags.
- [ ] Use lockfiles or checksums.
- [ ] Review Dependabot coverage.
- [ ] Add dependency-review gates.
- [ ] Define an update cadence.

---

## 8.2 Generate SBOMs

Generate an SBOM for every release using:

- SPDX; or
- CycloneDX.

The SBOM must include:

- application dependencies;
- operating-system packages;
- base images;
- build metadata;
- artifact digest.

### Acceptance criteria

- Every production image has an SBOM.
- SBOMs are retained as release artifacts.
- Each SBOM maps to an immutable image digest.

---

## 8.3 Sign Images and Generate Provenance

### Tasks

- [ ] Use Cosign or an equivalent system.
- [ ] Use keyless signing where appropriate.
- [ ] Generate build provenance.
- [ ] Link provenance to source commits.
- [ ] Link provenance to the CI workflow.
- [ ] Retain artifact digests.
- [ ] Verify signatures before deployment.

### Target chain

```text
Source commit
→ protected CI workflow
→ reproducible build
→ SBOM
→ provenance
→ signed image
→ admission verification
→ deployment
```

---

## 8.4 Add Admission Control

Use a policy engine such as:

- Kyverno;
- OPA Gatekeeper;
- Sigstore Policy Controller;
- a platform-native admission policy.

Minimum policies:

- allow images only from approved registries;
- require image digests;
- require valid signatures;
- require valid provenance;
- forbid privileged containers;
- forbid root containers;
- require read-only root filesystems;
- require dropped capabilities;
- disable service-account-token automount unless required;
- require resource limits;
- forbid `hostPath`;
- forbid host networking.

---

## 8.5 Separate Runtime and Migration Artifacts

### Problem

Runtime images should not contain migration capabilities they do not need.

### Target

```text
seev-runtime:<digest>
seev-migration:<digest>
```

The migration job should:

- use a dedicated service account;
- use a migration-specific database role;
- run as a one-shot job;
- produce an audit trail;
- never run from the application runtime;
- never expose public endpoints.

---

# 9. Phase 5 — Application Security Hardening

**Objective:** Strengthen endpoint, parser, upload, webhook, and abuse-control security.

**Estimated effort:** 1–2 weeks.

---

## 9.1 Add Security Scanning to CI

### Secret scanning

Use one of:

- Gitleaks;
- TruffleHog.

Block:

- private keys;
- tokens;
- credentials;
- cloud secrets;
- database passwords;
- accidental `.env` files.

### SAST

Use:

- CodeQL;
- Semgrep;
- `gosec` as an additional signal.

### Container scanning

Use:

- Trivy;
- Grype.

### Infrastructure-as-code scanning

Use:

- Checkov;
- KICS;
- Kubescape.

### Policy

- Baseline existing findings.
- Block new critical or high-confidence issues.
- Require triage for medium findings.
- Require a reason and expiry date for suppressions.

---

## 9.2 Fuzz Security-Critical Components

Fuzz the following:

- JWT parser.
- JWT claim validator.
- mTLS identity parser.
- URI SAN validation.
- Encryption-envelope parser.
- Key-version parser.
- Webhook payload parser.
- Signature verification.
- Idempotency-key parser.
- Monetary-value parser.
- State-machine transition handlers.
- Multipart upload parser.
- Callback-status mapper.

### Acceptance criteria

- Fuzz targets run periodically in CI.
- Crash corpora are preserved.
- Every valid finding produces a regression test.
- Parsers never panic on malformed input.

---

## 9.3 Harden the KYC and File-Upload Pipeline

### Target flow

```text
Upload
→ request-size limit
→ magic-byte validation
→ allowed-format parsing
→ random object name
→ quarantine storage
→ malware scanning
→ content sanitization
→ metadata stripping
→ clean status
→ reviewer access
```

### Controls

- Private object storage.
- Never use the original filename as the storage key.
- Short-lived signed URLs.
- Separate origin for downloads.
- `Content-Disposition: attachment`.
- Strict MIME-type enforcement.
- Maximum image pixel count.
- Maximum PDF page count.
- Decompression-bomb protection.
- Download auditing.
- Retention and deletion policies.

---

## 9.4 Harden Webhook Processing

### Required controls

- Signature validation.
- Timestamp validation.
- Replay window.
- Idempotency.
- Constant-time comparison.
- Raw-body verification.
- Vendor-specific secrets.
- Key-rotation support.
- Source allowlists as defense in depth, not primary trust.
- Dead-letter handling.
- Alerts for repeated invalid signatures.

### Negative tests

- Modified payload.
- Wrong secret.
- Old timestamp.
- Future timestamp.
- Replayed callback.
- Duplicate callback.
- Invalid encoding.
- Missing signature.
- Oversized body.
- Conflicting vendor identity.

---

## 9.5 Improve Abuse and Credential-Stuffing Protection

### Controls

- Rate limits by IP.
- Rate limits by account.
- Rate limits by device.
- Progressive delays.
- Compromised-password screening.
- Risk-based login.
- MFA challenges.
- Detection of distributed attacks.
- Fallback protection when Redis is unavailable.

### Important design note

Fail-open rate limiting can be acceptable for selected low-risk endpoints. Login, password-reset, OTP, and credential endpoints require layered fallback protection so a Redis outage does not remove all abuse controls.

---

# 10. Phase 6 — Observability, Detection, and Audit Security

**Objective:** Detect compromise and retain useful evidence without leaking sensitive data.

**Estimated effort:** 1 week.

---

## 10.1 Define a Security Logging Standard

Create:

```text
docs/security/security-logging-standard.md
```

Log at least:

- failed logins;
- token replay;
- refresh-token reuse;
- authorization denial;
- administrator login;
- high-risk administrator actions;
- secret-access failure;
- certificate rejection;
- webhook-signature failure;
- suspicious rate-limit patterns;
- key rotation;
- certificate rotation;
- data export;
- manual ledger correction;
- break-glass usage.

Never log:

- access tokens;
- refresh tokens;
- session tokens;
- passwords;
- private keys;
- plaintext PII;
- complete KYC documents;
- raw card information;
- complete API keys;
- encryption keys.

---

## 10.2 Create a Tamper-Evident Audit Trail

For privileged actions, capture:

- actor;
- identity-assurance level;
- action;
- resource;
- before-and-after values;
- reason;
- request ID;
- timestamp;
- source;
- correlation ID;
- integrity evidence.

Consider:

- hash chaining;
- immutable storage;
- WORM retention;
- export to a dedicated security account;
- separation of duties.

---

## 10.3 Add Security Detection Rules

Create alerts for:

- repeated failed mTLS identities;
- wrong JWT audiences;
- unknown `kid` values;
- repeated invalid webhooks;
- admin access from new devices;
- abnormal data exports;
- services accessing unusual destinations;
- sudden spikes in secret retrieval;
- certificates approaching expiry;
- failed key rotations;
- repeated idempotency conflicts;
- ledger-reconciliation mismatches;
- unexpected privilege grants;
- break-glass usage.

---

# 11. Phase 7 — Adversarial Testing

**Objective:** Prove that security boundaries withstand realistic attacks.

**Estimated effort:** 1–2 weeks for the first cycle, then repeated periodically.

---

## 11.1 Compromised-Pod Simulation

Select a low-privilege service.

Prove that an attacker inside that pod cannot:

- read another service's private key;
- read the operator private key;
- mint JWTs;
- decrypt PII owned by another domain;
- read another service's database;
- publish to arbitrary queues;
- access admin endpoints;
- reach arbitrary internet destinations.

Retain:

- commands used;
- expected denial;
- actual output;
- timestamp;
- environment;
- artifact digest.

---

## 11.2 Authentication Attack Testing

Test:

- brute force;
- credential-stuffing simulation;
- JWT tampering;
- algorithm confusion;
- expired tokens;
- wrong audiences;
- wrong issuers;
- stolen refresh tokens;
- concurrent refresh;
- session fixation;
- CSRF;
- privilege escalation;
- account-recovery abuse.

---

## 11.3 Business-Logic Security Testing

Test:

- duplicate transfers;
- replayed payouts;
- changed amount under the same idempotency key;
- state-transition bypass;
- negative amounts;
- integer-overflow boundaries;
- cross-tenant access;
- callbacks arriving before request state exists;
- callbacks arriving after terminal state;
- vendor timeouts and retries;
- unbalanced ledger postings;
- abuse of manual administrator corrections.

---

## 11.4 Independent Penetration Testing

Minimum scope:

- public APIs;
- admin application;
- authentication flows;
- KYC upload;
- webhook endpoints;
- authorization;
- tenant isolation;
- infrastructure configuration;
- business logic.

Before real production use, obtain an independent review from someone who did not implement the primary controls.

---

# 12. Phase 8 — Operational Security and Incident Readiness

**Objective:** Ensure the team can respond to compromise, not only attempt to prevent it.

**Estimated effort:** 1 week for the initial baseline.

---

## 12.1 Incident-Response Runbooks

Create runbooks for:

- JWT signing-key compromise.
- Workload private-key compromise.
- Database-credential compromise.
- Merchant API-key compromise.
- Vendor webhook-secret compromise.
- KMS outage.
- Secret-manager outage.
- Data leakage.
- Malicious administrator.
- Software supply-chain compromise.
- Container-image compromise.
- Ransomware or destructive activity.

Each runbook should cover:

1. Detection.
2. Containment.
3. Credential revocation.
4. Key rotation.
5. Blast-radius assessment.
6. Evidence preservation.
7. Recovery.
8. Stakeholder communication.
9. Post-incident review.

---

## 12.2 Rotation Drills

Run drills for:

- JWT signing-key rotation.
- mTLS CA or intermediate rotation.
- Workload-certificate rotation.
- Database-password rotation.
- RabbitMQ credential rotation.
- Redis credential rotation.
- Data-encryption-key rotation.
- Merchant API-key pepper rotation.
- Vendor webhook-key rotation.

### Acceptance criteria

- No secret is hard-coded.
- Rotation does not require source-code changes.
- Downtime stays within the target.
- Old credentials can be revoked.
- Audit evidence is retained.

---

## 12.3 Secure Disaster Recovery

Extend existing backup and PITR drills with:

- backup encryption;
- access separation;
- restore authorization;
- restore auditing;
- isolated restore environments;
- credential rotation after restore;
- validation that deleted credentials are not restored as active;
- validation that revoked tokens do not become active again;
- financial reconciliation after restore.

---

# 13. CI/CD Security Gate Design

Recommended pipeline:

```text
Pull Request
├── unit tests
├── race tests
├── lint
├── SAST
├── secret scanning
├── dependency review
├── IaC scanning
├── Helm-render policy tests
├── authorization negative tests
└── security regression tests

Main Branch
├── integration tests
├── contract tests
├── mTLS negative tests
├── database-isolation tests
├── chaos tests
└── release-candidate build

Release
├── immutable image build
├── final-image scan
├── SBOM generation
├── provenance generation
├── image signing
└── publish by digest

Deployment
├── admission signature verification
├── admission policy verification
├── production-configuration validation
├── staged rollout
├── smoke tests
└── post-deployment security checks
```

---

# 14. Required Security Test Matrix

| Test | Unit | Integration | Staging | Periodic Drill |
|---|---:|---:|---:|---:|
| JWT wrong signature | Yes | Yes | Optional | No |
| JWT wrong audience | Yes | Yes | Yes | No |
| mTLS wrong workload identity | Yes | Yes | Yes | Yes |
| Secret isolation | No | Manifest | Yes | Yes |
| Cross-service database access denied | No | Yes | Yes | Yes |
| Refresh-token race | Yes | Yes | Optional | No |
| Webhook replay | Yes | Yes | Yes | Yes |
| Malicious KYC file | Yes | Yes | Yes | Yes |
| Key rotation | No | Yes | Yes | Yes |
| Certificate rotation | No | Yes | Yes | Yes |
| Backup restore | No | No | Yes | Yes |
| Compromised-pod lateral movement | No | No | Yes | Yes |
| Signed-artifact verification | No | CI | Yes | Yes |

---

# 15. Recommended Security Documentation Structure

```text
docs/security/
├── README.md
├── threat-model.md
├── control-inventory.md
├── risk-register.md
├── production-security-profile.md
├── identity-and-access.md
├── cryptography.md
├── secret-management.md
├── workload-identity.md
├── secure-configuration.md
├── security-logging-standard.md
├── incident-response.md
├── key-rotation-runbook.md
├── certificate-rotation-runbook.md
├── compromised-pod-drill.md
├── supply-chain.md
├── penetration-test-scope.md
└── evidence/
    ├── mTLS/
    ├── secret-isolation/
    ├── key-rotation/
    ├── restore/
    └── adversarial-testing/
```

---

# 16. Suggested Security ADRs

Create:

1. `ADR-SEC-001` — Per-workload mTLS identity and private-key isolation.
2. `ADR-SEC-002` — Asymmetric JWT signing and key distribution.
3. `ADR-SEC-003` — Service-owned secret and cryptographic-key boundaries.
4. `ADR-SEC-004` — Fail-closed production configuration.
5. `ADR-SEC-005` — Managed secret store and KMS.
6. `ADR-SEC-006` — Signed build artifacts and deployment verification.
7. `ADR-SEC-007` — Per-service network policies.
8. `ADR-SEC-008` — Atomic refresh-token rotation.
9. `ADR-SEC-009` — KYC untrusted-content processing.
10. `ADR-SEC-010` — Privileged administrator authentication and dual control.
11. `ADR-SEC-011` — Tamper-evident security audit trail.
12. `ADR-SEC-012` — Security evidence and control-verification standards.

---

# 17. Recommended Implementation Order

## Sprint 1 — Critical Isolation

- Split workload mTLS Secrets.
- Remove the operator key from application pods.
- Split cryptographic Secrets.
- Add Helm-manifest assertions.
- Add the production startup guard.

## Sprint 2 — Tokens and Identity

- Implement asymmetric JWTs.
- Add issuer and audience validation.
- Implement signing-key rotation.
- Make refresh-token rotation atomic.
- Add authentication adversarial tests.

## Sprint 3 — Data Plane

- Create per-service network policies.
- Create per-service database credentials.
- Enable Redis ACLs and TLS.
- Enable RabbitMQ TLS and virtual-host permissions.
- Secure OpenTelemetry transport.

## Sprint 4 — Supply Chain

- Pin images by digest.
- Generate SBOMs.
- Generate provenance.
- Sign images.
- Add admission verification.
- Split runtime and migration images.

## Sprint 5 — Application Hardening

- Add secret scanning.
- Add SAST.
- Add container scanning.
- Add IaC scanning.
- Add fuzzing.
- Build the KYC quarantine pipeline.
- Review webhook replay protection.

## Sprint 6 — Operational Assurance

- Add administrator MFA.
- Add tamper-evident audit trails.
- Add detection rules.
- Run key-rotation drills.
- Run certificate-rotation drills.
- Run compromised-pod simulations.
- Prepare for independent penetration testing.

---

# 18. High-Level Backlog

| ID | Work Item | Priority | Effort | Impact |
|---|---|---:|---:|---:|
| SEC-001 | Remove operator key from application pods | P0 | Medium | Critical |
| SEC-002 | Split mTLS Secrets per workload | P0 | Medium | Critical |
| SEC-003 | Split cryptographic Secrets per service and purpose | P0 | Medium | Critical |
| SEC-004 | Migrate JWTs to asymmetric signing | P0 | High | Critical |
| SEC-005 | Add a fail-closed production configuration guard | P0 | Medium | Critical |
| SEC-006 | Make refresh-token rotation atomic | P1 | Medium | High |
| SEC-007 | Create per-service Kubernetes NetworkPolicies | P1 | Medium | High |
| SEC-008 | Add PostgreSQL TLS and credential isolation | P1 | Medium | High |
| SEC-009 | Add Redis TLS and ACLs | P1 | Medium | High |
| SEC-010 | Add RabbitMQ TLS and permissions | P1 | Medium | High |
| SEC-011 | Adopt a managed secret manager and KMS | P1 | High | High |
| SEC-012 | Generate SBOMs | P1 | Low | High |
| SEC-013 | Add image signing and provenance | P1 | Medium | High |
| SEC-014 | Add admission verification | P1 | Medium | High |
| SEC-015 | Add secret scanning | P1 | Low | High |
| SEC-016 | Add SAST and container scanning | P1 | Low | High |
| SEC-017 | Build a KYC upload quarantine pipeline | P1 | High | High |
| SEC-018 | Add WebAuthn for administrators | P1 | High | High |
| SEC-019 | Define a security logging standard | P2 | Low | Medium |
| SEC-020 | Build a tamper-evident audit trail | P2 | High | High |
| SEC-021 | Add security-critical fuzzing | P2 | Medium | Medium |
| SEC-022 | Run compromised-pod simulations | P2 | Medium | High |
| SEC-023 | Conduct independent penetration testing | P2 | Medium | High |
| SEC-024 | Create incident-response runbooks | P2 | Medium | High |
| SEC-025 | Run rotation drills | P2 | Medium | High |

---

# 19. Production Security Acceptance Checklist

## Identity and Secrets

- [ ] Every application pod contains only its own private key.
- [ ] No application pod contains the operator private key.
- [ ] Secrets are separated by owner and purpose.
- [ ] JWTs use asymmetric signing.
- [ ] Verifiers do not have access to private signing keys.
- [ ] Signing-key rotation has been tested.
- [ ] mTLS certificate rotation has been tested.
- [ ] Secret rotation has been tested.
- [ ] Production secrets come from an approved secret manager.

## Application

- [ ] Refresh-token rotation is atomic.
- [ ] Incorrect JWT issuers and audiences are rejected.
- [ ] Administrators use phishing-resistant MFA.
- [ ] Authorization is default deny.
- [ ] Cross-tenant negative tests pass.
- [ ] Webhook replay-protection tests pass.
- [ ] File uploads pass through quarantine and scanning.
- [ ] Credential endpoints have rate-limit fallback controls.
- [ ] Sensitive data does not enter logs or traces.

## Infrastructure

- [ ] Network policies exist per service.
- [ ] PostgreSQL uses TLS and per-service credentials.
- [ ] Redis uses TLS and ACLs.
- [ ] RabbitMQ uses TLS and per-service permissions.
- [ ] OpenTelemetry transport is secure.
- [ ] Public TLS termination is verified.
- [ ] Admin endpoints are not public by default.
- [ ] Egress is restricted.

## Supply Chain

- [ ] GitHub Actions are pinned to SHAs.
- [ ] Base images are pinned by digest.
- [ ] Final-image vulnerability scans pass.
- [ ] SBOMs are available.
- [ ] Provenance is available.
- [ ] Images are signed.
- [ ] Admission controllers verify artifacts.
- [ ] Runtime and migration artifacts are separated.

## Operational Assurance

- [ ] The threat model matches the actual deployment.
- [ ] The risk register is current.
- [ ] Compromised-pod drills pass.
- [ ] Signing-key compromise drills pass.
- [ ] Certificate-compromise drills pass.
- [ ] Backup restore and PITR drills pass.
- [ ] Security alerts reach the correct responders.
- [ ] Incident-response runbooks exist.
- [ ] Independent penetration testing is complete.
- [ ] All P0 issues are closed.
- [ ] All P1 issues are closed or have formally accepted residual risk.

---

# 20. Success Metrics

## Isolation

Measure:

- average number of secrets per workload;
- number of private keys per workload;
- number of services able to mint JWTs;
- number of services able to decrypt another domain's PII;
- number of reachable network destinations per service.

Targets:

```text
Private keys per workload: 1
JWT signing authorities: 1 logical authority
Cross-domain decryption capability: 0
Operator private keys in application pods: 0
```

## Supply Chain

Measure:

- percentage of signed images;
- percentage of images with SBOMs;
- percentage of deployments using digests;
- percentage of releases with provenance;
- number of mutable production tags.

Targets:

```text
Signed images: 100%
SBOM coverage: 100%
Digest-pinned production deployments: 100%
Mutable production tags: 0
```

## Detection and Response

Measure:

- mean time to detect security events;
- mean time to revoke compromised credentials;
- successful key-rotation drill rate;
- successful certificate-rotation drill rate;
- percentage of privileged actions with complete audit records.

## Secure Development

Measure:

- number of new critical and high findings;
- average remediation time;
- fuzzing execution time;
- security-test coverage of critical boundaries;
- percentage of threat scenarios with verification evidence.

---

# 21. Final Recommendation

Seev already demonstrates security design that is well above the average portfolio repository. The next step should not be to add as many security features as possible. The priority should be to prove that the trust boundaries already designed are genuinely enforced.

The highest-value order of work is:

1. Remove the operator private key from application workloads.
2. Separate secrets and keys according to service ownership.
3. Replace symmetric JWT signing with asymmetric signing.
4. Enforce fail-closed production configuration.
5. Restrict network access and data-plane credentials per service.
6. Build a verifiable software supply chain.
7. Validate the system through compromised-pod simulations and independent penetration testing.
8. Build operational security through rotation drills and incident-response exercises.

The system should only be considered close to production-ready after all P0 items are complete, all P1 items are complete or have formally accepted residual risk, and every critical security boundary has retained verification evidence.
