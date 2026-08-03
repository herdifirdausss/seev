# Seev Application Security Improvement Plan

**Document type:** Application Security Engineering Plan  
**Repository:** `herdifirdausss/seev`  
**Status:** Pre-production / not yet used in production  
**Primary focus:** SQL injection, broken authentication and authorization, secret leakage, unsafe input handling, and end-to-end user-input data flows

---

## 1. Purpose

This plan converts the current AppSec assessment into an implementation roadmap for strengthening Seev before any production deployment involving real users, real credentials, or real money.

The plan focuses on verifying and improving the complete data path:

```text
External input
  → HTTP/gRPC boundary
  → parsing and validation
  → authentication
  → authorization
  → business logic
  → repository/query construction
  → PostgreSQL, Redis, file storage, or external vendor
  → logs, metrics, traces, and responses
```

The primary objectives are:

1. Prevent injection vulnerabilities.
2. Prevent account takeover and broken session behavior.
3. Prevent broken object-level and tenant-level authorization.
4. Prevent secrets and sensitive data from leaking.
5. Establish consistent input-validation rules.
6. Make security controls enforceable through automated tests and CI.
7. Produce auditable evidence that the repository is ready for a controlled pre-production environment.

---

## 2. Current Security Posture

Seev already demonstrates a stronger security baseline than most portfolio or learning repositories.

Existing strengths include:

- Parameterized PostgreSQL queries in reviewed repository paths.
- Password hashing using bcrypt.
- Opaque refresh tokens stored as hashes.
- JWT issuer and expiration validation.
- Tenant-aware repository interfaces.
- mTLS-oriented service identity.
- Restricted database application roles.
- HMAC-based callback verification.
- Encrypted PII and KYC documents.
- Request body size limits.
- Content-type restrictions for uploads.
- Threat-model and security-findings documentation.
- Dependency vulnerability checking.
- Race testing and static Go checks.

However, the repository should still be treated as **not production-ready** until high-impact authentication, configuration, scanning, and authorization controls are completed and verified.

---

## 3. Security Principles

All implementation work should follow these principles.

### 3.1 Deny by default

Authentication, authorization, file access, tenant access, and production configuration must fail closed unless explicitly allowed.

### 3.2 Validate at the boundary

Reject malformed, oversized, unsupported, or ambiguous input before it enters business logic.

### 3.3 Parameterize data, allowlist structure

User values must use query parameters. Dynamic SQL structure such as sort columns, table names, and operators must use strict server-side mappings.

### 3.4 Authorize every resource operation

Authentication proves identity. Every resource read or mutation must separately verify role, ownership, tenant, state, and operation permissions.

### 3.5 Minimize trust domains

A compromised service should not be able to impersonate users, sign arbitrary tokens, read unrelated databases, or decrypt unrelated data.

### 3.6 Never rely on documentation alone

A security control is considered implemented only when it is:

- Enforced by code or infrastructure.
- Covered by automated tests.
- Included in CI.
- Observable in logs or metrics where appropriate.
- Documented with an owner and failure behavior.

---

## 4. Target Outcomes

The plan is complete when the following conditions are met:

- A refresh token can be consumed only once under concurrent requests.
- Disabled, closed, or restricted accounts cannot continue sensitive operations with existing access tokens.
- JWT verification services cannot mint valid user tokens.
- JWT audience, issuer, purpose, expiration, and key identifier are validated.
- No user-controlled value reaches an SQL execution sink through string construction.
- All tenant-owned resources have negative cross-tenant tests.
- Production startup fails when development secrets or insecure transport settings are used.
- Secret scanning covers both the current tree and Git history.
- SAST, dependency scanning, container scanning, and security tests are mandatory CI checks.
- Sensitive values do not appear in logs, traces, metrics, errors, or build artifacts.
- Uploaded KYC files are quarantined and scanned before they become accessible.
- A repeatable DAST and authorization test suite runs against a disposable environment.
- All high-severity findings are closed or formally risk-accepted.

---

## 5. Priority Model

| Priority | Meaning | Required before |
|---|---|---|
| **P0** | Direct account takeover, privilege escalation, tenant isolation, secret, or production trust risk | Any external or production-like deployment |
| **P1** | Important defense-in-depth, abuse prevention, and sensitive-data protection | External beta or real user data |
| **P2** | Advanced hardening, operational maturity, and long-term assurance | General production readiness |
| **P3** | Continuous improvement and optimization | After initial production readiness |

---

# 6. Implementation Roadmap

## Phase 0 — Establish an AppSec Evidence Baseline

### Objective

Create a reliable inventory of security-sensitive entry points, trust boundaries, data stores, and existing controls before changing code.

### Tasks

#### SEC-BASE-001 — Build an attack-surface inventory

Document every externally reachable or privileged interface:

- Public HTTP routes.
- Authentication and refresh routes.
- Admin routes.
- Merchant/B2B routes.
- Webhook and callback routes.
- Internal gRPC services.
- Metrics and health endpoints.
- File upload/download routes.
- Database migration commands.
- Worker consumers.
- CLI or maintenance tools.

For each interface, record:

- Authentication mechanism.
- Authorization requirement.
- Accepted content type.
- Maximum request size.
- Rate limit.
- Sensitive input fields.
- Data stores reached.
- External services called.
- Sensitive outputs.
- Audit-log requirement.

**Deliverable:** `docs/security/attack-surface-inventory.md`

**Acceptance criteria:**

- Every router registration maps to one inventory entry.
- Every entry has an owner and trust classification.
- Unauthenticated endpoints are explicitly justified.

---

#### SEC-BASE-002 — Create a user-input-to-sink data-flow map

Trace important user-controlled inputs to sensitive sinks.

Minimum flows:

1. Registration and login.
2. Refresh-token rotation.
3. User profile update.
4. Merchant API-key authentication.
5. Tenant-scoped resource access.
6. Pay-in request.
7. Transfer request.
8. Payout request.
9. Vendor callback.
10. KYC submission.
11. KYC document upload and download.
12. Admin operations.
13. Search, sorting, pagination, and export.
14. Webhook registration and delivery.

Mark the following sinks:

- SQL execution.
- Redis keys and commands.
- File paths.
- File parsers.
- HTTP requests to vendors or user-controlled URLs.
- Logs and traces.
- Message queues.
- Cryptographic operations.
- Shell or process execution, if any.

**Deliverable:** `docs/security/data-flow-review.md`

**Acceptance criteria:**

- Each sensitive sink identifies its input origin.
- Each path identifies validation, normalization, authentication, and authorization controls.
- Missing controls become tracked findings.

---

#### SEC-BASE-003 — Create a security findings register

Use a consistent record for every finding:

```text
ID
Title
Severity
Confidence
Affected component
Attack scenario
Business impact
Evidence
Recommended remediation
Owner
Target milestone
Status
Regression test
Risk acceptance, if applicable
```

**Deliverable:** update or extend the existing security findings register.

---

## Phase 1 — Fix Authentication and Session Integrity

### Objective

Remove high-impact authentication weaknesses before extending security tooling.

---

### SEC-AUTH-001 — Make refresh-token rotation atomic

**Priority:** P0  
**Risk:** Concurrent requests may produce multiple valid successor tokens.

### Required implementation

Move refresh-token consumption and successor creation into one database transaction.

Recommended flow:

```text
BEGIN

Atomically mark the presented token as consumed
WHERE revoked_at IS NULL
RETURNING user_id, token_family_id

If no row is returned:
  treat the token as reused or replayed
  revoke the token family
  write a security event
  return 401

Insert exactly one successor token
Link the consumed token to the successor

COMMIT
```

Possible implementation patterns:

- Conditional `UPDATE ... WHERE revoked_at IS NULL RETURNING ...`.
- `SELECT ... FOR UPDATE` followed by a guarded update.
- A dedicated repository method that owns the complete transaction.

### Required tests

- Two concurrent refresh attempts with the same token.
- Twenty concurrent refresh attempts with the same token.
- Replay of an already consumed token.
- Database failure between token consumption and successor insertion.
- Rollback behavior.
- Token-family revocation behavior.
- Go race test.

### Acceptance criteria

- Exactly one concurrent refresh succeeds.
- Exactly one valid successor exists.
- Reuse attempts generate a security event.
- Partial transaction failures do not leave an unusable or duplicated token chain.

---

### SEC-AUTH-002 — Enforce account status consistently

**Priority:** P0  
**Risk:** Disabled users may continue operations with an existing access token.

### Required implementation

Create one centralized account-status policy.

Example policy:

| Status | Login | Refresh | Read profile | Update profile | Financial operation |
|---|---:|---:|---:|---:|---:|
| Active | Allow | Allow | Allow | Allow | Allow |
| Disabled | Deny | Deny | Deny or minimal support view | Deny | Deny |
| Closing | Deny | Deny | Limited | Deny | Deny |
| Closed | Deny | Deny | Deny | Deny | Deny |

Do not duplicate slightly different status checks across handlers or services.

### Required tests

- Endpoint matrix for every account status.
- Existing access token after account disablement.
- Existing access token after role change.
- Existing access token after password reset.
- Existing access token after global logout.

### Acceptance criteria

- The same policy is applied to all authenticated endpoints.
- Sensitive operations can revoke access before the JWT naturally expires.
- Status violations are auditable without logging sensitive data.

---

### SEC-AUTH-003 — Add immediate session revocation support

**Priority:** P1

Add a user-level `auth_version` or `session_version`.

Recommended behavior:

- Include the version in access-token claims.
- Store the current version in the user/session store.
- Increment it after:
  - account disablement,
  - password reset,
  - role change,
  - suspected compromise,
  - global logout.
- Validate it for high-risk operations.
- Optionally cache the value in Redis with a short TTL.

### Acceptance criteria

- A security-sensitive account change invalidates old tokens.
- The solution does not require maintaining every access token individually.
- Cache failure behavior is explicitly defined.

---

### SEC-AUTH-004 — Strengthen login abuse controls

**Priority:** P1

Implement layered controls:

- Per-IP limit.
- Per-account or normalized-email digest limit.
- Per-device/session limit where available.
- Global emergency limit.
- Bounded local fallback when Redis is unavailable.
- Progressive backoff for repeated failures.
- Security event for suspicious login patterns.

### Acceptance criteria

- Redis failure does not completely remove login protection.
- Reverse-proxy configuration cannot collapse all clients into one bucket.
- Forwarded IP headers are trusted only from known proxies.
- Rate-limit keys never contain plaintext passwords, tokens, or sensitive PII.

---

## Phase 2 — Reduce JWT and Service Trust Risk

### Objective

Prevent a compromised verification service from minting valid user or admin tokens.

---

### SEC-JWT-001 — Migrate from shared HS256 to asymmetric signing

**Priority:** P0

Recommended design:

- Auth service holds the private signing key.
- Other services receive only public verification keys.
- Use Ed25519/EdDSA, ES256, or RS256.
- Add a `kid` header.
- Support overlapping keys during rotation.
- Publish an internal JWKS or equivalent trusted key distribution mechanism.

### Required claims

- `iss`
- `aud`
- `sub`
- `exp`
- `iat`
- optional `nbf`
- `jti`
- token purpose/type
- session or auth version
- role or permission claims only when justified

### Acceptance criteria

- Verification-only services cannot sign accepted tokens.
- Unknown `kid` values are rejected.
- Unsupported algorithms are rejected.
- Keys can rotate without invalidating all active tokens immediately.

---

### SEC-JWT-002 — Enforce audience and token purpose

**Priority:** P0

Define separate trust contexts, for example:

- End-user API.
- Merchant API.
- Admin/operator API.
- Internal service API.

A token created for one audience must not be accepted by another.

### Required tests

- User token sent to admin endpoint.
- Admin token sent to merchant endpoint.
- Token with missing audience.
- Token with wrong audience.
- Token with missing purpose.
- Token signed with an unexpected algorithm.
- Token with an unknown key identifier.

---

### SEC-JWT-003 — Separate user, admin, merchant, and service credentials

**Priority:** P1

Avoid using one secret or one token format for all actors.

Use:

- User access tokens.
- Operator/admin identity with stronger authentication.
- Merchant API keys or signed requests.
- Service workload identity through mTLS or platform identity.
- Short-lived service authorization tokens only when necessary.

---

## Phase 3 — SQL Injection and Repository Safety

### Objective

Prove that user-controlled data cannot alter SQL structure.

---

### SEC-SQL-001 — Audit every SQL execution sink

**Priority:** P0

Search for all uses of:

- `Exec`
- `ExecContext`
- `Query`
- `QueryContext`
- `QueryRow`
- `QueryRowContext`
- transaction equivalents
- raw migration execution
- query-builder escape hatches
- `fmt.Sprintf` near SQL
- string concatenation near SQL

Classify each query as:

- Static SQL with bound parameters.
- Dynamic SQL with strict allowlisted structure.
- Unsafe or unclear.

### Acceptance criteria

- Every SQL sink is classified.
- No unreviewed dynamic SQL remains.
- Unsafe findings have regression tests.

---

### SEC-SQL-002 — Add static rules for SQL construction

**Priority:** P0

Add Semgrep or CodeQL rules that flag:

- `fmt.Sprintf` flowing into database execution.
- Concatenated strings flowing into database execution.
- Request values used as table or column names.
- Request values used directly in `ORDER BY`.
- Manually constructed `IN (...)` expressions.
- SQL fragments accepted from JSON, query parameters, or headers.

### Acceptance criteria

- CI fails on a deliberately added vulnerable fixture.
- Safe parameterized examples do not create excessive false positives.
- Suppressions require justification and review.

---

### SEC-SQL-003 — Standardize dynamic sorting and filtering

**Priority:** P1

Use server-owned mappings:

```go
var allowedSortColumns = map[string]string{
    "created_at": "created_at",
    "status":     "status",
    "amount":     "amount",
}
```

The client chooses a logical key, never an SQL fragment.

Also allowlist:

- Sort direction.
- Filter operators.
- Search fields.
- Page size.
- Cursor format.

### Acceptance criteria

- Invalid sort/filter values return a client error.
- No raw request value becomes SQL syntax.
- Pagination has a bounded maximum page size.

---

### SEC-SQL-004 — Add injection-focused integration tests

**Priority:** P1

Test common payload classes across search, sort, filter, IDs, and free-text fields:

```text
'
' OR '1'='1
1; SELECT pg_sleep(5)
UNION SELECT
comment markers
Unicode quote variants
encoded payloads
JSON-escaped payloads
```

The goal is not only to observe an error response. The test must verify:

- No second statement executes.
- Query duration is unaffected by injected delay attempts.
- Tenant boundaries remain intact.
- Database errors are not exposed.

---

## Phase 4 — Authorization and Tenant Isolation

### Objective

Prevent BOLA, IDOR, privilege escalation, and cross-tenant access.

---

### SEC-AUTHZ-001 — Build an authorization matrix

**Priority:** P0

For every protected operation, define:

- Actor type.
- Required role or permission.
- Required tenant.
- Ownership rule.
- Allowed resource states.
- Allowed account states.
- Audit requirement.

Example:

| Operation | Actor | Tenant check | Ownership check | Required role |
|---|---|---:|---:|---|
| Read merchant transaction | Merchant user | Yes | Optional by policy | Transaction read |
| Rotate API key | Merchant admin | Yes | N/A | Credential manage |
| Review KYC | Operator | N/A | Assigned scope | KYC reviewer |
| Download KYC document | Operator | N/A | Case authorization | KYC reviewer |
| Trigger payout | Merchant user | Yes | Account ownership | Payout create |

---

### SEC-AUTHZ-002 — Add cross-tenant negative tests

**Priority:** P0

Tenant A must attempt to read and mutate Tenant B resources:

- Transactions.
- Accounts.
- Webhooks.
- API keys.
- Payouts.
- Pay-ins.
- KYC records.
- Reports and exports.
- Quotas.
- Audit logs.

### Acceptance criteria

- Cross-tenant access is always denied.
- Responses do not reveal whether the target resource exists.
- No cross-tenant metadata appears in timing-sensitive errors, logs, or events.
- Repository methods require tenant context for tenant-owned objects.

---

### SEC-AUTHZ-003 — Prevent confused-deputy behavior between services

**Priority:** P1

For internal gRPC calls:

- Authenticate the caller service identity.
- Allowlist which service may call each method.
- Preserve the original user or tenant context only through signed or trusted metadata.
- Re-authorize in the owning service.
- Never treat network location alone as authorization.

### Acceptance criteria

- A valid certificate for an unauthorized service cannot invoke the method.
- Missing tenant/user context is rejected.
- Forged caller metadata is ignored unless bound to a trusted identity.

---

### SEC-AUTHZ-004 — Harden admin access

**Priority:** P1

Require:

- Strong operator identity.
- mTLS or platform workload identity.
- Short-lived admin token.
- Explicit role/permission checks.
- Step-up authentication for destructive operations.
- Audit logging.
- No shared admin account.

---

## Phase 5 — Input Validation and Parser Hardening

### Objective

Make malformed and dangerous input fail early and consistently.

---

### SEC-INPUT-001 — Create shared validation rules

**Priority:** P1

Define reusable validation for:

- IDs and UUIDs.
- Email.
- Full name.
- Phone number.
- Currency.
- Monetary amount.
- Idempotency key.
- Pagination.
- Date/time.
- URLs.
- Callback identifiers.
- Vendor references.
- File metadata.

Every rule should define:

- Normalization.
- Minimum and maximum length.
- Character restrictions where justified.
- Allowed enum values.
- Error behavior.
- Logging behavior.

---

### SEC-INPUT-002 — Normalize identity fields consistently

**Priority:** P1

For email:

- Parse as a mailbox address.
- Use only the parsed address.
- Trim surrounding whitespace.
- Normalize the domain consistently.
- Define local-part case behavior.
- Enforce a maximum length.
- Use the same canonical value for registration, login, uniqueness, and recovery.

Do not use provider-specific transformations globally.

---

### SEC-INPUT-003 — Replace untyped KYC payloads

**Priority:** P1

Replace `map[string]any` at the HTTP boundary with typed request structures.

For each KYC level, define:

- Required fields.
- Optional fields.
- Enum values.
- Date formats.
- Maximum lengths.
- Nested-object rules.
- Country-specific fields.
- Sensitive-field redaction.

### Acceptance criteria

- Unknown fields are rejected.
- Deeply nested or oversized structures are rejected.
- Validation errors do not echo complete sensitive values.
- The schema is documented in OpenAPI.

---

### SEC-INPUT-004 — Reject trailing or ambiguous JSON

**Priority:** P1

After decoding the expected JSON object, verify that the decoder reaches EOF.

Also:

- Parse media types using the standard MIME parser.
- Compare exact normalized media types.
- Reject unsupported charsets where necessary.
- Define behavior for duplicate JSON keys.
- Bound nesting depth if the decoder or schema allows deep structures.

---

### SEC-INPUT-005 — Add fuzz tests for security boundaries

**Priority:** P2

Fuzz:

- JWT parser.
- JSON request decoder.
- Idempotency-key parser.
- Money parser.
- Email normalization.
- Callback signature parser.
- File metadata parser.
- URL validation.
- Repository filter/sort parsing.

### Acceptance criteria

- No panic.
- No unbounded allocation.
- No unexpected acceptance of malformed input.
- Failures return controlled errors.

---

## Phase 6 — Secret and Sensitive-Data Protection

### Objective

Prevent credentials and sensitive financial or identity data from entering source control, runtime logs, or insecure configuration.

---

### SEC-SECRET-001 — Add mandatory secret scanning

**Priority:** P0

Run Gitleaks or an equivalent scanner against:

- Current working tree.
- Pull-request diff.
- Complete Git history.
- Generated CI artifacts where practical.

### Acceptance criteria

- CI fails on a committed canary secret.
- Known synthetic fixtures are narrowly allowlisted.
- Allowlist entries include justification.
- Real exposed secrets are revoked, not merely deleted from Git.

---

### SEC-SECRET-002 — Enforce production configuration policy

**Priority:** P0

When environment mode is production or staging, startup must fail if:

- JWT or internal secrets are placeholders.
- Secrets are shorter than the defined minimum.
- Database TLS is disabled.
- Redis or broker transport is insecure.
- Default local credentials are present.
- Development certificates are used.
- PII encryption keys are missing.
- Secret-provider integration is disabled.
- Public services lack trusted TLS termination configuration.

### Acceptance criteria

- A production-mode integration test covers every forbidden configuration.
- Failure messages identify the configuration name without printing the secret.
- Development defaults remain convenient only in local mode.

---

### SEC-SECRET-003 — Use per-service credentials

**Priority:** P1

Each service should have its own:

- Database user.
- Broker credential.
- Redis ACL identity where supported.
- mTLS identity.
- Secret path.
- Encryption-key access policy.

Compromise of one service must not expose unrelated service credentials.

---

### SEC-SECRET-004 — Add key rotation procedures

**Priority:** P2

Document and test rotation for:

- JWT signing keys.
- PII encryption keys.
- KYC document encryption keys.
- Merchant API keys.
- Vendor secrets.
- Database credentials.
- Internal service tokens.

The procedure must cover:

- Overlapping active keys.
- Rollback.
- Re-encryption or lazy migration.
- Audit trail.
- Emergency revocation.

---

## Phase 7 — Logging, Tracing, and Error Safety

### Objective

Make the system observable without leaking secrets or sensitive customer data.

---

### SEC-LOG-001 — Stop logging raw query values by default

**Priority:** P1

Prefer:

- Route templates.
- Allowlisted query names.
- Redacted query values.
- Stable request IDs.
- Tenant IDs only when policy allows.
- Hashed identifiers when correlation is required.

Redact keys containing terms such as:

- `token`
- `secret`
- `key`
- `password`
- `authorization`
- `signature`
- `code`
- `email`
- `account`
- `document`

---

### SEC-LOG-002 — Add sensitive-data leak tests

**Priority:** P1

Send canary values through:

- Headers.
- JSON body.
- Query string.
- Form fields.
- Multipart metadata.
- Vendor response.
- Error path.
- Panic path.
- gRPC metadata.

Verify absence from:

- Application logs.
- Audit logs.
- Traces.
- Metrics labels.
- CI output.
- Error responses.
- Dead-letter records.

---

### SEC-LOG-003 — Standardize secure error responses

**Priority:** P1

External responses should use stable error codes without exposing:

- SQL messages.
- File-system paths.
- Stack traces.
- Cryptographic errors.
- Vendor secrets.
- Internal hostnames.
- Tenant existence.
- Key identifiers that should remain private.

Detailed diagnostics should remain in protected logs with redaction.

---

## Phase 8 — File Upload and KYC Security

### Objective

Protect operators and downstream systems from malicious documents.

---

### SEC-FILE-001 — Introduce file quarantine

**Priority:** P1

New state flow:

```text
uploaded
  → quarantined
  → scanning
  → clean
  → available

or

uploaded
  → quarantined
  → rejected
```

A quarantined document must not be downloadable through normal admin workflows.

---

### SEC-FILE-002 — Add malware and file-structure scanning

**Priority:** P1

Scan documents in an isolated worker.

Controls should include:

- Antivirus or malware scanner.
- PDF structure validation.
- Maximum page count.
- Maximum image dimensions and pixel count.
- Metadata stripping.
- Image decode and safe re-encode where practical.
- File checksum.
- Scanner engine and signature version.
- Timeout and memory limit.
- No network access for the scanning process unless required.

---

### SEC-FILE-003 — Strengthen file type validation

**Priority:** P1

Do not rely only on filename or initial-byte MIME detection.

Verify:

- Declared content type.
- Detected content type.
- File signature.
- Successful parser validation.
- Allowed extension generated by the server.
- Maximum decompressed size.

### Acceptance criteria

- Polyglot and malformed test files are rejected or quarantined.
- Files are stored under opaque server-generated identifiers.
- Download responses use safe content disposition and content type.
- File paths never depend on raw user input.

---

## Phase 9 — CI/CD Security Gates

### Objective

Make security regressions difficult to merge.

---

### SEC-CI-001 — Add SAST

**Priority:** P0

Recommended minimum:

- CodeQL for Go.
- Semgrep security rules.
- `gosec`.
- Existing `go vet`.
- Existing race tests.

Required rule categories:

- SQL injection.
- Command execution.
- Path traversal.
- SSRF.
- Weak randomness.
- Hard-coded credentials.
- Unsafe TLS.
- JWT misuse.
- Log injection.
- File permission issues.
- Insecure temporary files.
- Cryptographic misuse.

---

### SEC-CI-002 — Strengthen dependency and supply-chain checks

**Priority:** P1

Add:

- `govulncheck`.
- Dependency review on pull requests.
- SBOM generation.
- Container image scan.
- Base-image digest pinning.
- GitHub Actions commit pinning.
- License policy where relevant.
- Build provenance and image signing.

---

### SEC-CI-003 — Create a security test workflow

**Priority:** P1

A disposable environment should run:

1. Unit tests.
2. Race tests.
3. Integration tests.
4. Authentication replay tests.
5. Cross-tenant authorization tests.
6. SQL injection tests.
7. Secret-leak tests.
8. Callback-signature tests.
9. Upload security tests.
10. DAST.

### Acceptance criteria

- Security checks are required branch-protection checks.
- High-severity failures cannot be bypassed without a recorded exception.
- CI artifacts do not contain secrets.

---

### SEC-CI-004 — Add security pull-request requirements

**Priority:** P2

Require a security-impact section for changes affecting:

- Authentication.
- Authorization.
- Database queries.
- Input parsing.
- File upload.
- Cryptography.
- Vendor callbacks.
- Secrets.
- Network exposure.
- Tenant-scoped data.

Suggested PR questions:

- What new input is accepted?
- What new sensitive sink is reached?
- How is authorization enforced?
- How is tenant isolation preserved?
- What negative tests were added?
- Could this change expose secrets or PII?
- What happens when dependencies fail?

---

## Phase 10 — DAST, Abuse Testing, and Independent Review

### Objective

Validate behavior in a running environment rather than relying only on source inspection.

---

### SEC-DAST-001 — Run API DAST

**Priority:** P1

Use the OpenAPI specification to scan:

- Authentication endpoints.
- Protected endpoints.
- Content-type handling.
- Method confusion.
- Oversized requests.
- Security headers.
- Error leakage.
- Injection payloads.
- Missing authorization.
- CORS behavior.

Tools may include OWASP ZAP or another API-focused scanner.

---

### SEC-DAST-002 — Build an authorization abuse suite

**Priority:** P1

Test:

- Missing token.
- Expired token.
- Wrong issuer.
- Wrong audience.
- Wrong token purpose.
- Modified role.
- Modified tenant.
- Disabled user.
- Cross-tenant object ID.
- Replayed API key.
- Revoked refresh token.
- Replayed callback.
- Admin endpoint with ordinary user token.
- Internal endpoint without allowed service identity.

---

### SEC-DAST-003 — Test SSRF and outbound request controls

**Priority:** P1

For every user-influenced outbound URL:

- Require HTTPS where appropriate.
- Allowlist schemes.
- Resolve and reject loopback, private, link-local, multicast, and metadata addresses.
- Re-check after redirects.
- Defend against DNS rebinding.
- Set connection, response, and total timeouts.
- Limit response size.
- Disable unnecessary redirects.
- Do not forward sensitive internal headers.

---

### SEC-DAST-004 — Conduct an independent penetration test

**Priority:** P2

Perform after P0 and P1 remediations, before real-money production use.

Scope should include:

- Account takeover.
- JWT and session handling.
- Tenant isolation.
- Business logic abuse.
- Idempotency.
- Vendor callbacks.
- Admin operations.
- KYC document handling.
- Secrets and deployment.
- Rate-limit bypass.
- Race conditions.

---

# 7. Recommended Delivery Sequence

## Milestone A — Security correctness

Complete first:

1. `SEC-AUTH-001` Atomic refresh-token rotation.
2. `SEC-AUTH-002` Account-status enforcement.
3. `SEC-JWT-002` Audience and token-purpose enforcement.
4. `SEC-SQL-001` SQL sink audit.
5. `SEC-AUTHZ-001` Authorization matrix.
6. `SEC-AUTHZ-002` Cross-tenant tests.
7. `SEC-SECRET-001` Full-history secret scan.
8. `SEC-SECRET-002` Production configuration policy.

**Exit condition:** No known P0 finding remains open.

---

## Milestone B — Automated prevention

Complete next:

1. `SEC-JWT-001` Asymmetric JWT signing.
2. `SEC-SQL-002` SQL static rules.
3. `SEC-CI-001` SAST.
4. `SEC-CI-002` Supply-chain checks.
5. `SEC-CI-003` Security workflow.
6. `SEC-INPUT-001` Shared validation.
7. `SEC-LOG-002` Sensitive-data leak tests.
8. `SEC-AUTH-004` Layered authentication throttling.

**Exit condition:** The main security invariants are enforced by required CI checks.

---

## Milestone C — Sensitive-data and operational hardening

Complete next:

1. File quarantine and scanning.
2. Per-service credentials.
3. Key rotation.
4. Session revocation.
5. Secure logging and error handling.
6. SSRF/outbound request testing.
7. DAST.
8. Container and SBOM validation.

**Exit condition:** The system is suitable for controlled staging with production-like security controls and synthetic data.

---

## Milestone D — External assurance

Complete last:

1. Threat-model update.
2. Security architecture review.
3. Independent penetration test.
4. Remediation verification.
5. Formal risk acceptance for remaining non-critical findings.
6. Production-readiness security sign-off.

**Exit condition:** All high-severity findings are closed, and remaining risks have documented owners and acceptance.

---

# 8. Suggested GitHub Issue Breakdown

Create one issue for each item below.

## Authentication

- `[P0][AppSec] Make refresh-token rotation atomic`
- `[P0][AppSec] Enforce disabled-account policy across all endpoints`
- `[P1][AppSec] Add session-version based revocation`
- `[P1][AppSec] Add layered login throttling and Redis fallback`

## JWT and service trust

- `[P0][AppSec] Add JWT audience and token-purpose validation`
- `[P0][AppSec] Migrate JWT signing from HS256 to asymmetric keys`
- `[P1][AppSec] Separate user, merchant, admin, and service credentials`
- `[P2][AppSec] Implement JWT key rotation with kid and JWKS`

## SQL and data access

- `[P0][AppSec] Audit all SQL execution sinks`
- `[P0][AppSec] Add Semgrep rules for unsafe SQL construction`
- `[P1][AppSec] Standardize allowlisted sorting and filtering`
- `[P1][AppSec] Add SQL injection integration tests`

## Authorization

- `[P0][AppSec] Document the authorization matrix`
- `[P0][AppSec] Add cross-tenant negative test suite`
- `[P1][AppSec] Enforce caller identity on internal gRPC methods`
- `[P1][AppSec] Harden admin authentication and authorization`

## Validation

- `[P1][AppSec] Introduce shared domain validators`
- `[P1][AppSec] Normalize email consistently`
- `[P1][AppSec] Replace untyped KYC payloads with typed schemas`
- `[P1][AppSec] Reject trailing JSON and ambiguous media types`
- `[P2][AppSec] Add fuzzing for security-sensitive parsers`

## Secrets and configuration

- `[P0][AppSec] Add full-history Gitleaks scanning`
- `[P0][AppSec] Fail production startup on insecure configuration`
- `[P1][AppSec] Introduce per-service credentials`
- `[P2][AppSec] Document and test key rotation`

## Logging and privacy

- `[P1][AppSec] Redact raw query parameters`
- `[P1][AppSec] Add canary secret leakage tests`
- `[P1][AppSec] Standardize external error responses`

## File security

- `[P1][AppSec] Add KYC document quarantine`
- `[P1][AppSec] Add malware and document-structure scanning`
- `[P1][AppSec] Strengthen file type and parser validation`

## CI/CD and testing

- `[P0][AppSec] Add CodeQL, Semgrep, and gosec`
- `[P1][AppSec] Add SBOM and container scanning`
- `[P1][AppSec] Add a required security test workflow`
- `[P1][AppSec] Add API DAST`
- `[P2][AppSec] Add security review requirements to pull requests`

---

# 9. Security Test Matrix

| Security area | Unit | Integration | Concurrency | SAST | DAST |
|---|---:|---:|---:|---:|---:|
| Refresh rotation | Yes | Yes | Yes | Partial | Yes |
| JWT validation | Yes | Yes | No | Yes | Yes |
| Account status | Yes | Yes | No | Partial | Yes |
| SQL injection | Partial | Yes | Optional | Yes | Yes |
| Tenant isolation | Partial | Yes | Optional | Partial | Yes |
| Secret leakage | Yes | Yes | No | Yes | Partial |
| File upload | Yes | Yes | Optional | Yes | Yes |
| Callback verification | Yes | Yes | Replay tests | Yes | Yes |
| Rate limiting | Yes | Yes | Yes | No | Yes |
| SSRF | Yes | Yes | DNS/redirect tests | Yes | Yes |
| Error redaction | Yes | Yes | No | Partial | Yes |

---

# 10. Security Metrics

Track the following metrics in the repository or security dashboard:

- Open findings by severity.
- Mean age of open high-severity findings.
- Percentage of protected endpoints with authorization tests.
- Percentage of tenant-owned resources with cross-tenant negative tests.
- Number of SQL sinks reviewed.
- Number of dynamic SQL suppressions.
- Secret-scanning coverage for Git history.
- SAST findings introduced per pull request.
- Dependency vulnerabilities by severity.
- Container vulnerabilities by severity.
- Refresh-token reuse events.
- Failed login rate.
- Rate-limit activation rate.
- Disabled-user access attempts.
- Callback signature failures.
- Replayed callback attempts.
- Rejected or malicious file uploads.
- Sensitive-data leak test pass rate.

Avoid using sensitive values as metric labels.

---

# 11. Definition of Done for Security Work

A security task is not complete until:

- The vulnerable behavior is reproduced or clearly modeled.
- The remediation is implemented.
- Positive and negative tests are added.
- A regression test would fail on the previous implementation.
- Logging and metrics do not expose sensitive data.
- Documentation and threat models are updated.
- CI executes the new test or scanner.
- The finding register contains closure evidence.
- Any residual risk is explicitly documented.

---

# 12. Production Security Gate

Seev should not process real credentials, PII, KYC documents, or money until all of the following are true:

## Mandatory

- [ ] All P0 findings are closed.
- [ ] Refresh-token race tests pass.
- [ ] Disabled-user and revocation tests pass.
- [ ] JWT audience and purpose are enforced.
- [ ] Verification services cannot sign accepted JWTs.
- [ ] SQL sink audit is complete.
- [ ] Cross-tenant test suite passes.
- [ ] Gitleaks scans the full Git history.
- [ ] SAST is a required CI check.
- [ ] Production startup rejects insecure defaults.
- [ ] Database, Redis, broker, and external traffic use approved secure transport.
- [ ] Per-service production credentials are provisioned.
- [ ] PII and document encryption keys use approved key management.
- [ ] Logs and traces pass canary secret tests.
- [ ] KYC files are quarantined and scanned.
- [ ] DAST has no unresolved high-severity finding.
- [ ] An independent security review has been completed.

## Recommended

- [ ] Session-version revocation is enabled.
- [ ] Key rotation has been tested.
- [ ] SBOM is generated for every release.
- [ ] Container images are scanned and signed.
- [ ] Security alerts have owners and escalation paths.
- [ ] Backup and restore security has been tested.
- [ ] Incident-response procedures cover credential theft and account takeover.

---

# 13. Immediate Next Actions

The highest-value implementation order is:

1. Make refresh-token rotation atomic.
2. Enforce account status on every authenticated route.
3. Add cross-tenant authorization tests.
4. Audit all SQL execution sinks.
5. Add full-history secret scanning.
6. Add CodeQL, Semgrep, and `gosec`.
7. Enforce production configuration fail-closed behavior.
8. Add JWT audience and purpose.
9. Migrate to asymmetric JWT signing.
10. Standardize input validation and sensitive-data redaction.
11. Add KYC quarantine and malware scanning.
12. Run DAST and an independent penetration test.

This sequence addresses the largest account-takeover, privilege, tenant-isolation, injection, and secret-management risks before investing in lower-priority hardening.
