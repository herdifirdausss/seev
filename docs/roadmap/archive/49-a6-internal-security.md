# 49 — Track A6: Internal Security

> Derived from track **A6** in [42-long-term-roadmap.md](../42-long-term-roadmap.md).
>
> **Status: complete.** The final isolated-Compose security gate passed on
> 2026-07-21. This track covers service-plane security: mTLS, service
> identity, secret loading, threat modeling, and evidence-based security
> review. Human-admin 2FA/SSO remains a separate follow-up.

## 1. Trigger and objective

Before this track, internal gRPC used plaintext transport and an optional
shared token. An empty `INTERNAL_GRPC_TOKEN` disabled authentication entirely.
Internal HTTP listeners and Prometheus metrics were also plain HTTP, service
certificates did not exist, and application secrets were supplied only as
environment variables.

Track A6 closes those service-plane gaps before the repository exposes future
partner-facing surfaces. It uses only open-source components already suitable
for local development: Go's standard TLS/X.509 libraries, Docker Compose,
HashiCorp Vault in development mode, and the repository's existing tooling.
It does not introduce a database migration.

Security debts addressed:

| Debt | Resolution |
| --- | --- |
| Plaintext gRPC and unverified service identity | mTLS with URI SAN allowlists in T2 |
| Empty internal token accepted every gRPC call | Fail-closed token validation in T2 |
| Plaintext internal HTTP and metrics | mTLS HTTP listeners and Prometheus scrape in T3 |
| Secrets scattered through environment configuration | Vault development-mode overlay in T4 |
| No explicit threat model | STRIDE-style threat model and finding register in T1 |
| CORS wildcard and optional JWT issuer | Evidence-based review and fixes in T5/T6 |
| No tested certificate rotation procedure | Live rotation drill in T6 |

## 2. Scope and boundaries

This track secures service-to-service communication and local operational
secrets. It does not:

- provide production HSM/KMS, Vault HA, auto-unseal, or dynamic secrets;
- terminate public edge TLS for the development gateway and auth listeners;
- replace Kubernetes cert-manager or a production service mesh;
- implement admin SSO, 2FA, or WebAuthn;
- alter ledger transfer ordering, RLS, failover rules, vendor behavior,
  messaging contracts, or fraud policy semantics;
- place private keys or secrets in Git, logs, or CI artifacts.

Vault is intentionally a development-mode learning path. It is ephemeral,
re-seeded on startup, and uses an HTTP listener in the local Compose network.
Production Vault hardening is explicitly deferred.

## 3. Locked design decisions

### K1 — A living threat model

`docs/security/threat-model.md` records assets, trust boundaries, service
flows, STRIDE-style threats, and a `TM-nn` finding register. The document is
updated when live verification discovers topology drift or a new finding; it
is not treated as a substitute for checking the code.

### K2 — Shared TLS package with hot reload

`pkg/tlsx` owns certificate loading, polling-based hot reload, client and
server TLS configuration, URI SAN extraction, certificate-chain validation,
and allowlist checks. It is usable by `pkg/grpcx` and by internal HTTP
clients and servers without importing any `internal/*` package.

The server validates the peer certificate on every handshake. The client
validates the CA chain and expected service identity. Because the development
certificates use URI SANs rather than DNS SANs, the package performs explicit
identity verification instead of relying on hostname verification.

### K3 — Go certificate generator

`cmd/certgen` provides `init-ca`, `issue --service <name>`, and `rotate`.
Identities use SPIFFE-style URI SANs:

```text
spiffe://seev/<service>
```

The supported identities include gateway, auth, ledger, payin, payout, fraud,
admin-bff, assurance, `dev-operator`, and `prometheus`. The CA lifetime is
30 days and leaf certificates last 72 hours. `make certs` is idempotent and
does not regenerate a still-fresh certificate. Private keys are written only
to the ignored `deploy/certs/` directory.

### K4 — Explicit per-hop allowlists

Every internal listener rejects a certificate signed by the trusted CA if its
URI SAN is not allowed for that listener. The important gRPC relationships
are:

| Server | Allowed callers |
| --- | --- |
| Ledger gRPC | gateway, auth, payin, payout, assurance |
| Payin gRPC | gateway, assurance |
| Payout gRPC | gateway, assurance |
| Fraud gRPC | ledger, payin, payout |

HTTP listeners use equivalent allowlists for dev-operator, Prometheus,
admin-bff, and the gateway-to-ledger proxy. A valid certificate from the
repository CA is not sufficient by itself; the identity must be allowed for
the specific listener.

### K5 — Internal token authentication fails closed

The shared internal token remains defense in depth below mTLS. Every gRPC
server and client now requires both a non-empty token and a non-nil TLS
configuration. The previous no-op branch for an empty token was removed.
Compose, the local harness, and nightly workflows generate and pass the
token explicitly.

### K6 — mTLS covers internal HTTP and metrics

The following listeners use mTLS in the local stack:

```text
gateway       :8081
ledger        :8090 and :8091
auth          :8083
payin         :8092
payout        :8093
fraud         :8094
admin-bff     :8095
assurance     :8096
```

The public gateway and auth development listeners remain outside this track.
The gateway-to-ledger proxy, admin BFF downstream clients, health checks,
the shell harness, and Prometheus all use the appropriate client identity.

### K7 — Vault development-mode overlay

An opt-in Compose `secrets` profile runs Vault on `127.0.0.1:18200`.
`scripts/vault-seed.sh` writes KV v2 entries idempotently. When both
`VAULT_ADDR` and `VAULT_TOKEN` are configured, Vault values override matching
environment keys and missing keys fall back to the environment. A configured
but unreachable Vault is a hard error; an unseeded path falls back to the
environment so a fresh development instance remains usable.

CI and nightly workflows continue to generate environment secrets. The Vault
profile is not silently inserted into those workflows.

### K8 — Evidence-based security review

The review checks authentication and authorization boundaries, IDOR behavior,
webhook forgery/replay/size handling, rate-limit keying, CORS, JWT issuer
validation, and algorithm confinement. Each finding receives a severity,
reproduction evidence, and an owner task in the threat-model register.

### K9 — A separate certificate rotation drill

`scripts/rotation-drill.sh` rotates certificates while a live service is
under request load. It verifies that new certificates work after reload,
old certificates are rejected, and transient failures stay within the
polling grace window. Rotation is an operational security procedure, not a
money-safety chaos scenario.

### K10 — Low-cardinality TLS metrics

`pkg/tlsx` exposes certificate expiry and handshake-failure metrics. Identity
labels come only from the fixed service identity set. Unverified certificate
names are recorded as `unknown`, so an attacker cannot inject arbitrary
Prometheus label values.

## 4. Implementation results

### T1 — Threat model and finding register

Added `docs/security/threat-model.md` with eight asset categories, seven
trust-boundary groups, the live service topology, STRIDE analysis for gRPC
and HTTP hops, and findings `TM-01` through `TM-10`.

The live review corrected the original service count: assurance added three
gRPC client relationships and one HTTP listener. It was added to the
certificate and allowlist scope rather than being left as an undocumented
exception. Prometheus coverage was also corrected to include admin-bff and
assurance.

### T2 — mTLS for gRPC and token fail-closed

Added `pkg/tlsx`, `cmd/certgen`, generated local certificates, and the gRPC
TLS wiring. All gRPC dial sites and listeners now load service identities and
validate the expected peer identity. Negative tests prove that a missing
certificate and a certificate outside the listener allowlist are rejected.

The TLS package has unit tests using real `tls.Listen` and `tls.Dial` paths,
not only in-memory stubs. The gRPC package also fails fast when the internal
token or TLS configuration is missing.

### T3 — mTLS for HTTP and metrics

Added TLS-aware HTTP servers, internal clients, health checks, and a shared
`curl_internal` harness helper. All internal shell requests were migrated to
HTTPS while public gateway/auth development endpoints remain plain as
documented.

Prometheus now scrapes all service metrics over HTTPS with its own client
identity, including the previously missing admin-bff and assurance jobs. The
gateway proxy and admin BFF use target-specific client configurations so each
peer identity remains explicit.

The live migration found and fixed three harness issues: curl needed an
explicit hostname-verification bypass because the dev certificates use URI
SANs, one fee URL was stored and reused outside the automatic sweep, and
metric assertions passed plain-HTTP URLs to a helper. Smoke, business E2E,
admin E2E, and all 14 chaos scenarios passed after those fixes.

### T4 — Vault integration

Added the optional Vault Compose profile, `scripts/vault-seed.sh`, and the
configuration seam used by all service loaders. The overlay is per key rather
than all-or-nothing, so an unseeded key still uses the existing environment
configuration.

The seed script was tested for idempotency and for KV v2's replace semantics.
The auth service's three related secrets are written together so a later
write cannot remove keys written by an earlier write. Unit tests cover
precedence, fallback, malformed responses, invalid tokens, and unreachable
Vault. Integration tests cover both environment-only and seeded Vault paths
with a real Vault container.

### T5 — Live pentest-style review

The review confirmed:

- mTLS rejects unauthenticated and unauthorized internal callers before HTTP;
- JWT and role checks still reject a valid but insufficient user token;
- IDOR attempts return ownership-safe responses;
- invalid webhook signatures are rejected and duplicate event IDs do not
  double-post money;
- the mock vendor accepts stale signed test events by design, so timestamp
  binding is recorded as an accepted risk for this test double;
- the original rate-limit key incorrectly included the ephemeral TCP port;
- CORS allowed `*` by default;
- a missing configured JWT issuer was accepted;
- HS256 algorithm confinement was correctly enforced;
- request-body logging truncated the body before webhook verification, causing
  oversized requests to fail with a misleading authentication error.

The last four issues were carried into T6 with explicit findings and
reproduction evidence. No review claim was left as design-only evidence.

### T6 — Fixes, rotation, and final gate

Resolved the actionable findings:

- CORS is API-safe by default and non-production origins require an explicit
  allowlist.
- `JWT_ISSUER` is required in every environment and is included in generated
  tokens.
- Rate limiting keys by IP without the ephemeral port and keeps the
  forwarded-address header out of the security decision.
- The logger restores the complete request body to the handler while keeping
  log output truncated.
- The TLS server refreshes its CA pool on every handshake, so rotation does
  not require reconstructing the `tls.Config` or restarting the process.
- TLS expiry and handshake-failure metrics and the security dashboard panel
  are available.
- Runbooks were added for certificate rotation, Vault seeding, and handshake
  failure response.

The rotation drill initially exposed a frozen server-side CA pool. After the
fix, the same TLS configuration accepted the new certificate and rejected the
old certificate. Transient failures stayed within the expected two-poll
grace window and the request loop recovered completely.

The final isolated-Compose gate passed build, vet, lint, unit and integration
tests, smoke, business E2E, admin E2E, and all 14 chaos scenarios. The stack
was removed using its isolated Compose project name after verification.

## 5. Additional finding outside the original scope

The final money-safety gate exposed a race in the fraud velocity store when
Redis became a network black hole. A slow Redis attempt could outlive the
fraud decision budget and be treated as a raw deadline error, allowing the
fail-open path to run.

The fix bounds each Redis attempt with a 150 ms sub-deadline. A hanging-store
unit test and two live reruns of chaos scenario 9 confirmed fail-closed
behavior. Shared cache rate-limit code was intentionally left unchanged
because it did not exhibit the same failure and had a much broader blast
radius.

## 6. Operational constraints

1. Private CA and leaf keys must remain under ignored `deploy/certs/` and must
   never appear in Git, logs, or CI artifacts.
2. Internal mTLS identity is based on URI SAN, not a certificate common name.
3. Every new internal hop must be added to the threat model and allowlist
   matrix before implementation.
4. Any new secret source must preserve the environment-only fallback path and
   must not silently downgrade a configured Vault failure.
5. Prometheus labels must remain low-cardinality and use a fixed identity set.
6. Public edge TLS, production Vault hardening, and admin human-identity
   hardening remain separate work.

## 7. Definition of Done

- [x] All gRPC and internal HTTP hops, including admin-bff and assurance,
      require mTLS and verify URI SAN identities.
- [x] Missing internal tokens and TLS configuration fail closed.
- [x] `docs/security/threat-model.md` contains the live topology, findings,
      severity, evidence, and accepted residual risks.
- [x] Vault development mode, idempotent seeding, Vault-over-environment
      precedence, and environment-only fallback are tested.
- [x] The pentest-style review was executed against a live stack with
      command/output evidence.
- [x] Certificate rotation was tested under live traffic, including rejection
      of the old certificate.
- [x] TLS expiry and handshake-failure observability is available.
- [x] No private key or secret is tracked by the repository.
- [x] Project documentation and runbooks describe the new security posture.
- [x] The isolated final gate is green across build, tests, smoke, E2E,
      admin, chaos, container, proto, lint, and vet checks.

## 8. Deferred follow-ups

- Admin-console 2FA, SSO, and WebAuthn remain a separate user-plane task.
- Production Vault TLS, persistence, HA, and auto-unseal remain deployment
  work.
- Public edge TLS termination remains deployment/Kubernetes work described by
  [35-phase6j-kubernetes.md](../active/35-phase6j-kubernetes.md).
