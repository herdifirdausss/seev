# Plan 64 — K0 Deployment Inventory and Kubernetes Readiness Baseline

**Created:** 2026-07-29  
**Status:** Inventory committed; static gate green; runtime acceptance partial
**Parent roadmap:** Plan 63 v2 — Kubernetes Cloud Deployment Roadmap  
**Track:** K0 — Freeze deployment scope  
**Primary objective:** Produce a complete, reproducible, evidence-backed inventory of Seev's deployable runtime before creating Kubernetes manifests  

The inventory artifacts are committed and the current static gate passes. K0
remains active because local runtime acceptance is partial and the K1 handoff
has not been authorized; see the [current-state inventory](../../reference/current-state.md)
and [K0 acceptance record](../../evidence/k0/final-acceptance.md).
**Repository:** `herdifirdausss/seev`  
**Expected current topology:** Nine core deployable Go services plus the optional local mock push provider, PostgreSQL, Redis, RabbitMQ, local object storage, backup tooling, and optional observability
**Execution environment:** Disposable local development environment only  
**Kubernetes changes authorized by this plan:** None  
**Cloud resources authorized by this plan:** None

---

## 1. Purpose

K0 prevents the Kubernetes deployment from being designed from incomplete assumptions.

The output of K0 is not a collection of informal notes. It is an executable and reviewable deployment contract describing:

- every deployable application;
- every listening port;
- every public, callback, admin, health, metrics, HTTP, and gRPC surface;
- every service-to-service call;
- every database and database identity;
- every Redis use;
- every RabbitMQ exchange, queue, routing key, publisher, and consumer;
- every background worker and schedule;
- every file, volume, and durable-storage requirement;
- every environment variable;
- every secret and key;
- every mTLS identity and allowed peer;
- every vendor callback CIDR and trusted-proxy assumption;
- every vendor outbound destination;
- every health and shutdown behavior;
- every image and runtime assumption;
- every resource baseline;
- every feature that will be enabled or disabled in the first Kubernetes deployment;
- every unresolved blocker that must be closed by K1–K6.

K0 must answer:

```text
What exactly will Kubernetes run?
What must be reachable?
What must never be reachable?
What state must survive?
What identity does every workload use?
What can each workload call?
What external traffic exists?
What secrets does it require?
How much CPU and memory does it actually need?
How does it start, become ready, stop, and recover?
```

K0 does not create:

- Helm templates;
- Kubernetes Deployments;
- Kubernetes Services;
- Gateway API resources;
- NetworkPolicies;
- cloud networks;
- GKE or EKS clusters;
- Cloud NAT or NAT Gateway;
- production secrets.

Those changes begin only after the K0 exit gate passes.

---

## 2. Why K0 is mandatory

A Compose-to-Kubernetes translation frequently fails because Compose hides important assumptions:

- `depends_on` is mistaken for runtime dependency management;
- loopback-published ports are treated as public Kubernetes ports;
- one container health check is used as startup, readiness, and liveness;
- environment defaults become production configuration;
- host-mounted certificates are copied into every Pod;
- Docker bridge source addresses are mistaken for real vendor CIDRs;
- local service names become undocumented internal DNS contracts;
- background workers are scaled as though they were ordinary HTTP replicas;
- local persistent volumes are moved without backup ownership;
- all service egress is left open;
- resource requests are invented instead of measured;
- one shared database credential is reused;
- migration execution is mixed into application startup;
- public, internal, callback, metrics, and admin endpoints are exposed through the same route.

K0 creates the input required to avoid those mistakes.

---

## 3. Locked K0 principles

1. Executable behavior is stronger evidence than prose.
2. Current documentation is still reviewed and reconciled with code.
3. No secret value is copied into K0 evidence.
4. Configuration keys may be recorded; credential contents may not.
5. Every matrix cell must have a source reference.
6. Unknown values are labeled `UNKNOWN`, never guessed.
7. Every `UNKNOWN` blocks the relevant downstream Kubernetes task.
8. A port is not public merely because the process listens on it.
9. `EXPOSE` in a Dockerfile is informational, not the exposure policy.
10. A Compose host-port mapping is not the Kubernetes Service design.
11. A health-check command is not automatically a correct liveness probe.
12. `depends_on` is not converted into init-container ordering by default.
13. Every data store has one owner.
14. Every application database identity is separate from migration identity.
15. Every public route has one owning service and one edge policy.
16. Vendor callback allowlisting does not replace signature verification.
17. VendorService is the only external-vendor protocol owner.
18. VendorService's outbound destination list must be finite for K6.
19. Admin BFF is private in the first cloud deployment.
20. Internal mTLS identity rules must survive the Kubernetes move.
21. Background workers are inventoried separately from request servers.
22. A worker's concurrency and singleton behavior must be known before replica count is chosen.
23. Resource baselines are measured under repeatable workloads.
24. Docker Compose limits are evidence inputs, not Kubernetes recommendations.
25. The optional observability profile is measured separately.
26. The first Kubernetes deployment keeps advanced C1–C6 features disabled unless a test explicitly needs them.
27. K0 does not change financial behavior.
28. K0 evidence is committed with the implementation that later consumes it.
29. The final inventory must be machine-checkable where practical.
30. The exit gate is binary: complete or not ready for K1.

---

## 4. Source-of-truth hierarchy

When sources disagree, use this order:

```text
1. Running behavior and tests
2. Application code
3. Dockerfile and docker-compose.yml
4. api/contracts, api/proto, api/events
5. .env.example and configuration validation
6. Current reference documentation
7. Archived roadmap documents
```

A mismatch must produce:

- a documented finding;
- the selected authoritative value;
- a follow-up owner;
- a documentation or code fix where appropriate.

Do not silently choose one source.

---

## 5. Current verified baseline seed

The following is a seed inventory from the current repository. K0 must verify every value against the pinned baseline commit.

### 5.1 Deployable services

| Service | Current container ports | Current database | Current primary responsibility |
|---|---|---|---|
| Gateway | HTTP `8080`, internal HTTP `8081` | `seev_gateway` | Public API composition, notification module, ledger-event consumption |
| Auth | HTTP `8082`, internal HTTP `8083` | `seev_auth` | Authentication, profiles, roles, KYC, privacy workflows |
| Ledger | user HTTP `8090`, internal HTTP `8091`, gRPC `9091` | `seev_ledger` | Double-entry posting, policies, fees, reporting, workers |
| Payin | HTTP/admin `8092`, gRPC `9092` | `seev_payin` | Top-up intent, vendor-session orchestration, callback correlation |
| Payout | HTTP/admin `8093`, gRPC `9093` | `seev_payout` | Withdrawal state machine, vendor dispatch, recovery |
| Fraud | HTTP/admin `8094`, gRPC `9094` | `seev_fraud` | Synchronous screening and asynchronous enrichment |
| Admin BFF | HTTP `8095` | `seev_adminbff` | Operator sessions, typed admin proxy, maker/checker, audit |
| Assurance | HTTP/admin `8096` | `seev_assurance` | Independent cross-service reconciliation and intake controls |
| VendorService | callback/admin HTTP `8098`, gRPC `9098` | `seev_vendor` | Vendor protocol, outbound attempt records, callback authentication and normalization |

The current repository declares all nine service binaries in one multi-stage Dockerfile and copies one selected binary into a distroless non-root runtime image.

### 5.2 Current local infrastructure

| Component | Current local port | Current purpose |
|---|---:|---|
| PostgreSQL | host `5433`, container `5432` | Service-owned databases |
| Redis | host `6380`, container `6379` | Rate limits, locks, velocity, coordination, caches |
| RabbitMQ | `5672` | AMQP event transport |
| RabbitMQ management | `15672` | Local management UI only |
| Auth object storage | local named volume | Encrypted KYC/export/closure artifacts |
| Backup agent | host `18097`, container `8097` | Local backup/PITR learning |
| Prometheus | optional profile | Metrics |
| Grafana | optional profile | Dashboards |
| Loki | optional profile | Logs |
| Tempo | optional profile | Traces |
| Alloy | optional profile | Telemetry collection |

### 5.3 Current container characteristics

Current verified seed:

```text
builder: golang:1.26.5-alpine
runtime: gcr.io/distroless/static-debian12:nonroot
CGO: disabled
runtime user: nonroot:nonroot
entrypoint: /app/service
health command: /app/service -healthcheck
migrations: copied into each application image
```

K0 must classify which of these are retained, changed in K1, or moved to a dedicated migration image/job.

### 5.4 Current internal security seed

- internal HTTP and gRPC use mTLS;
- service identity is based on SPIFFE-style URI SANs;
- listeners use explicit peer allowlists;
- an internal gRPC token also exists;
- local Compose mounts the generated certificate directory into application containers;
- real Kubernetes secret distribution is not yet designed;
- VendorService currently has application-level callback CIDR validation;
- local callback CIDRs include local/bridge ranges and are not cloud values.

### 5.5 Current important workers seed

Known workers include:

```text
Auth:
- KYC apply retry
- sanctions re-screen

Ledger:
- outbox relay
- verifier
- daily snapshot
- interest accrual
- scheduled transaction runner
- other retention/recovery jobs to inventory

Payout:
- uncertain-result / resume recovery

Admin BFF:
- session cleanup

Assurance:
- periodic correlation and finding lifecycle

Gateway:
- ledger event consumer for notification behavior

Fraud:
- ledger event consumer
```

K0 must enumerate the exact full set from code.

---

## 6. K0 output structure

K0 produces:

```text
docs/deployment/
├── README.md
├── service-runtime-inventory.md
├── port-protocol-matrix.md
├── public-route-matrix.md
├── internal-call-matrix.md
├── data-ownership-matrix.md
├── messaging-matrix.md
├── background-job-matrix.md
├── storage-volume-matrix.md
├── configuration-matrix.md
├── secret-key-matrix.md
├── mtls-identity-matrix.md
├── vendor-network-matrix.md
├── health-lifecycle-matrix.md
├── image-runtime-matrix.md
├── resource-baseline.md
├── feature-scope.md
├── deployment-risk-register.md
└── k1-input-contract.md

docs/evidence/k0/
├── baseline.md
├── compose-normalized.yaml
├── command-output/
├── service-probes/
├── network/
├── resources/
├── verification/
├── generated/
└── final-acceptance.md
```

Recommended machine-readable inventories:

```text
deploy/inventory/
├── services.yaml
├── ports.yaml
├── dependencies.yaml
├── routes.yaml
├── data-stores.yaml
├── messaging.yaml
├── jobs.yaml
├── configuration.yaml
├── secrets.yaml
├── vendor-network.yaml
└── first-deployment-scope.yaml
```

Machine-readable files must not contain secret values.

---

## 7. K0 execution phases

```text
K0-T0  Pin baseline and prepare evidence workspace
K0-T1  Enumerate deployable processes
K0-T2  Inventory ports, protocols, and bind behavior
K0-T3  Inventory public, callback, admin, health, and metrics routes
K0-T4  Inventory internal service calls
K0-T5  Inventory databases, Redis, RabbitMQ, and storage
K0-T6  Inventory background workers and singleton behavior
K0-T7  Inventory configuration, secrets, keys, and certificates
K0-T8  Inventory vendor ingress and egress
K0-T9  Inventory startup, readiness, shutdown, and recovery
K0-T10 Inventory image and filesystem behavior
K0-T11 Measure CPU, memory, connections, and storage baseline
K0-T12 Freeze first Kubernetes deployment feature scope
K0-T13 Classify gaps and downstream ownership
K0-T14 Generate K1–K6 input contracts
K0-T15 Run final verification and close K0
```

---

# K0-T0 — Pin baseline and prepare evidence workspace

## 8. Objective

Make the inventory reproducible and prevent later repository changes from silently changing the result.

### 8.1 Work

Record:

```bash
git rev-parse HEAD
git branch --show-current
git status --short
git log -1 --format='%H%n%aI%n%s'
go version
docker version
docker compose version
uname -a
```

Record the operating system, CPU architecture, available memory, Docker memory allocation, and filesystem free space.

Create:

```text
docs/evidence/k0/baseline.md
```

Required fields:

```text
commit
branch
working tree status
date
operator
machine OS
CPU
RAM
Docker engine
Docker Compose
Go version
test-data policy
```

### 8.2 Clean-tree rule

Preferred:

```text
git status --short = empty
```

If not empty:

- list every changed file;
- explain why;
- do not use uncommitted runtime changes as an undocumented baseline.

### 8.3 Evidence collection rule

Store command output as plain text.

Do not store:

- `.env`;
- secret files;
- certificates;
- tokens;
- database dumps containing user data.

### 8.4 Acceptance

- [ ] exact commit is pinned;
- [ ] machine context is recorded;
- [ ] evidence directory exists;
- [ ] secret-handling rule is documented;
- [ ] working tree state is explicit;
- [ ] all later K0 files reference this baseline.

---

# K0-T1 — Enumerate deployable processes

## 9. Objective

Identify every long-running process and one-shot executable that Kubernetes may need to run.

### 9.1 Discovery commands

```bash
find cmd -mindepth 1 -maxdepth 1 -type d -print | sort
go list ./cmd/...
docker compose config --services
docker compose --profile app config --services
grep -nE 'SERVICE:|profiles: \["app"\]' docker-compose.yml
```

Classify each `cmd/` entry as:

```text
long-running service
one-shot migration
certificate tool
backup utility
local mock
test helper
administrative CLI
code generator
```

### 9.2 Application service record

For each of the nine services record:

```text
canonical service name
Go entrypoint
Docker build SERVICE value
Compose service name
image name
binary name
application owner
database owner
request-server role
worker role
public exposure class
first-deployment enabled state
```

### 9.3 Naming inconsistency review

Known naming examples to reconcile:

```text
gateway entrypoint: cmd/gateway
Compose name: gateway-service
APP_NAME: gateway-service

admin entrypoint: cmd/admin-bff-service
Compose name: admin-bff-service
```

Create one canonical Kubernetes name.

Recommended names:

```text
gateway
auth
ledger
payin
payout
fraud
admin-bff
assurance
vendor
```

Keep application identity mapping explicit:

```text
Kubernetes name != automatic mTLS identity
```

### 9.4 One process per Pod decision

For each service confirm:

- request server and workers run in one process; or
- worker can be separated by configuration; or
- dedicated worker binary exists.

Do not decide replica count yet.

### 9.5 Deliverables

```text
docs/deployment/service-runtime-inventory.md
deploy/inventory/services.yaml
```

### 9.6 Required service record template

```yaml
name: ledger
current:
  command: ./cmd/ledger-service
  compose_service: ledger-service
  image: seev/ledger-service
  binary: /app/service
  app_name: ledger-service
roles:
  request_server: true
  background_workers: true
ownership:
  database: seev_ledger
exposure:
  public: false
  callback: false
  admin: true
  internal: true
first_deployment:
  enabled: true
unknowns: []
sources:
  - Dockerfile
  - docker-compose.yml
  - cmd/ledger-service
```

### 9.7 Acceptance

- [ ] every `cmd/` entry is classified;
- [ ] nine application services map to exactly nine canonical names;
- [ ] no hidden long-running process remains;
- [ ] every one-shot tool has a Kubernetes relevance decision;
- [ ] request-server and worker roles are distinguished;
- [ ] naming inconsistencies are resolved;
- [ ] first-deployment enabled state is recorded.

---

# K0-T2 — Inventory ports, protocols, and bind behavior

## 10. Objective

Build the authoritative port matrix used by Helm Services and NetworkPolicies.

### 10.1 Required dimensions

For every listener record:

```text
service
container port
protocol: HTTP / HTTPS / gRPC / AMQP / PostgreSQL / Redis
transport: TCP / UDP
bind address
TLS mode
mTLS required
authentication
route class
health path or health command
metrics path
intended Kubernetes Service exposure
```

### 10.2 Static discovery

Use:

```bash
grep -R --line-number -E \
  'APP_PORT|INTERNAL_APP_PORT|GRPC_PORT|ListenAndServe|net.Listen|grpc.NewServer' \
  cmd internal pkg config docker-compose.yml .env.example
```

Inspect:

- configuration defaults;
- server construction;
- TLS wrappers;
- health router;
- metrics router;
- Compose mappings;
- Dockerfile `EXPOSE`.

### 10.3 Runtime verification

Start disposable infrastructure and app profile:

```bash
make docker-up
make migrate-up-all
docker compose --profile app up --build -d
docker compose --profile app ps
```

For each container:

```bash
docker inspect <container>
```

Because the application image is distroless, use one of:

- container network inspection from the host;
- a temporary diagnostic container in the same network;
- Docker inspection;
- application health requests.

Do not alter the production image merely to run `netstat`.

### 10.4 Port classifications

Required exposure classes:

```text
PUBLIC_EDGE
CALLBACK_EDGE
PRIVATE_ADMIN
INTERNAL_HTTP
INTERNAL_GRPC
HEALTH_METRICS
DATA_PRIVATE
LOCAL_TOOL_ONLY
NOT_EXPOSED
```

### 10.5 Initial seeded classification

| Service | Port | Seed classification | K0 verification |
|---|---:|---|---|
| Gateway | 8080 | `PUBLIC_EDGE` | Verify route inventory |
| Gateway | 8081 | `INTERNAL_HTTP` + health/metrics | Verify admin/internal handlers |
| Auth | 8082 | public auth/profile through Gateway or direct route decision | Freeze in T12 |
| Auth | 8083 | `INTERNAL_HTTP` | Verify health/admin KYC routes |
| Ledger | 8090 | internal user-facing API consumed by Gateway | Not direct internet |
| Ledger | 8091 | `INTERNAL_HTTP` | Admin/closure/health |
| Ledger | 9091 | `INTERNAL_GRPC` | mTLS |
| Payin | 8092 | private admin/internal HTTP | Not internet |
| Payin | 9092 | `INTERNAL_GRPC` | mTLS |
| Payout | 8093 | private admin/internal HTTP | Not internet |
| Payout | 9093 | `INTERNAL_GRPC` | mTLS |
| Fraud | 8094 | private admin/internal HTTP | Not internet |
| Fraud | 9094 | `INTERNAL_GRPC` | mTLS |
| Admin BFF | 8095 | `PRIVATE_ADMIN` | ClusterIP in first cloud stage |
| Assurance | 8096 | private admin/internal HTTP | Not internet |
| VendorService | 8098 | `CALLBACK_EDGE` plus private admin/health surface to isolate | Verify routes |
| VendorService | 9098 | `INTERNAL_GRPC` | mTLS |

### 10.6 Bind-address verification

A process listening on `0.0.0.0` inside a Pod is normal. Exposure is controlled by Kubernetes Services and edge routes.

Record listeners that bind only to loopback because those may fail in a Pod when traffic arrives through a Service.

### 10.7 Dockerfile EXPOSE finding

The current generic runtime image exposes all application ports.

K0 records:

```text
EXPOSE is informational.
K1 may keep it, narrow it per image, or remove it.
It is not used as the Kubernetes exposure source of truth.
```

### 10.8 Deliverables

```text
docs/deployment/port-protocol-matrix.md
deploy/inventory/ports.yaml
docs/evidence/k0/service-probes/
```

### 10.9 Acceptance

- [ ] every listener is identified;
- [ ] static and runtime results match;
- [ ] bind addresses are known;
- [ ] TLS and mTLS behavior is known;
- [ ] public/callback/admin/internal classifications are explicit;
- [ ] no data-store management port is marked public;
- [ ] Dockerfile `EXPOSE` is not treated as policy;
- [ ] every port has a downstream K3/K4/K5 owner.

---

# K0-T3 — Inventory routes and exposure

## 11. Objective

Determine which HTTP paths may cross the Kubernetes edge.

### 11.1 Route sources

Inspect:

```text
api/contracts/
api/openapi/
internal/*/http.go
internal/handler/
cmd/* server wiring
docs/reference/services.md
scripts/smoke-test.sh
scripts/business-e2e.sh
scripts/admin-e2e.sh
```

### 11.2 Required route fields

```text
host class
path
method
owner service
current direct listener
authentication
authorization
rate limit
body limit
idempotency requirement
callback signature requirement
IP allowlist requirement
public exposure decision
first-deployment enabled state
```

### 11.3 Route classes

```text
PUBLIC_UNAUTHENTICATED
PUBLIC_AUTHENTICATED
PUBLIC_IDEMPOTENT_MONEY
VENDOR_CALLBACK
ADMIN_PRIVATE
INTERNAL_ONLY
HEALTH
METRICS
DEBUG_FORBIDDEN
```

### 11.4 Gateway rule

The first Kubernetes deployment should normally expose end-user API routes through Gateway.

K0 must identify any current direct-public Auth endpoint assumption.

Decision options:

```text
A. Traefik routes /api/v1/auth/* directly to Auth
B. Gateway proxies all public Auth traffic
```

Do not leave both active accidentally.

### 11.5 Vendor callback rule

Seed route:

```text
POST /webhooks/{vendor}
```

Required controls:

```text
Traefik source IP allowlist
VendorService application CIDR validation
HMAC/signature validation
timestamp/freshness
bounded request body
idempotent durable callback inbox
strict owner correlation in Payin/Payout
```

K0 records exact current paths and vendors.

### 11.6 Admin rule

Initial cloud deployment:

```text
Admin BFF has no public HTTPRoute.
```

Access method is deferred to a controlled tunnel/private edge.

All service `/admin/...` routes remain private and are reached through Admin BFF or internal operations.

### 11.7 Health and metrics rule

Health and metrics endpoints must not be placed on a public `HTTPRoute`.

Kubernetes probes access Pod ports directly.

Prometheus accesses an internal Service/Pod endpoint.

### 11.8 Debug-route scan

Search for:

```bash
grep -R --line-number -E \
  '/debug|pprof|metrics|health|ready|live|swagger|openapi' \
  cmd internal pkg
```

Classify every result.

### 11.9 Deliverables

```text
docs/deployment/public-route-matrix.md
deploy/inventory/routes.yaml
```

### 11.10 Acceptance

- [ ] every current route is classified;
- [ ] every public route has an edge owner;
- [ ] direct Auth routing decision is explicit;
- [ ] callback routes are complete;
- [ ] admin routes are private;
- [ ] health/metrics are private;
- [ ] debug endpoints are absent or forbidden;
- [ ] first-deployment routes are frozen.

---

# K0-T4 — Inventory internal service calls

## 12. Objective

Build the allowlist used by Kubernetes Services, mTLS peer policy, and NetworkPolicy.

### 12.1 Call types

Inventory:

```text
HTTP
HTTPS/mTLS HTTP
gRPC/mTLS
RabbitMQ event
Redis
PostgreSQL
object storage
external HTTP
```

### 12.2 Discovery methods

Search configuration keys:

```bash
grep -R --line-number -E \
  '_URL|_ADDR|_HOST|GRPC_ADDR|SERVICE_URL|INTERNAL_API_URL' \
  .env.example docker-compose.yml internal cmd
```

Search clients:

```bash
grep -R --line-number -E \
  'grpc\.Dial|NewClient|http\.Client|RoundTripper|Transport|Rabbit|Redis|sql\.Open' \
  internal pkg cmd
```

Inspect protobuf service definitions and contract inventory.

### 12.3 Seed call matrix

Expected calls to verify:

```text
Gateway -> Ledger gRPC
Gateway -> Ledger HTTP
Gateway -> Payin gRPC
Gateway -> Payout gRPC

Auth -> Ledger gRPC
Auth -> Ledger internal HTTP
Auth -> Payin internal HTTP
Auth -> Payout internal HTTP
Auth -> Fraud internal HTTP
Auth -> Gateway internal HTTP

Payin -> Ledger gRPC
Payin -> Fraud gRPC
Payin -> VendorService gRPC

Payout -> Ledger gRPC
Payout -> Fraud gRPC
Payout -> VendorService gRPC

VendorService -> Payin gRPC callback delivery
VendorService -> Payout gRPC callback delivery
VendorService -> approved external vendors

Admin BFF -> Auth public login
Admin BFF -> Auth internal admin
Admin BFF -> Ledger internal
Admin BFF -> Payin internal
Admin BFF -> Payout internal
Admin BFF -> Fraud internal
Admin BFF -> Gateway internal

Assurance -> Ledger gRPC read-only
Assurance -> Payin gRPC read-only/control
Assurance -> Payout gRPC read-only/control

Ledger -> Fraud gRPC
Ledger -> RabbitMQ
Fraud -> RabbitMQ
Gateway -> RabbitMQ
```

Verify direction and necessity.

### 12.4 Call-policy record

For each edge record:

```text
source
destination
protocol
port
client package
authentication
mTLS source identity
server allowlist
timeout
retry
circuit breaker
fail-open/fail-closed
required for readiness
first-deployment enabled
```

### 12.5 Readiness dependency warning

Do not mark a Pod unready merely because an optional external vendor is unavailable.

Record dependencies as:

```text
BOOT_REQUIRED
READINESS_REQUIRED
OPERATION_REQUIRED
OPTIONAL_DEGRADED
BACKGROUND_ONLY
```

### 12.6 Deliverables

```text
docs/deployment/internal-call-matrix.md
deploy/inventory/dependencies.yaml
```

### 12.7 Acceptance

- [ ] every internal call is directional;
- [ ] protocol and port are known;
- [ ] mTLS identity is known;
- [ ] timeouts and retries are known;
- [ ] fail-open/fail-closed is known;
- [ ] readiness dependency is classified;
- [ ] no cross-service database call exists;
- [ ] matrix can generate K5 NetworkPolicy inputs.

---

# K0-T5 — Inventory databases, Redis, RabbitMQ, and storage

## 13. Objective

Define all stateful dependencies and their ownership.

### 13.1 Database inventory

For every service record:

```text
database name
application user
migration user
migration path
connection pool
statement timeout
lock timeout
idle-in-transaction timeout
required extensions
schema ownership
backup inclusion
retention
estimated size
write/read criticality
```

Seed:

```text
Gateway     -> seev_gateway
Auth        -> seev_auth
Ledger      -> seev_ledger
Payin       -> seev_payin
Payout      -> seev_payout
Fraud       -> seev_fraud
Admin BFF   -> seev_adminbff
Assurance   -> seev_assurance
Vendor      -> seev_vendor
```

### 13.2 Database identity verification

Run ownership and grants queries from the migration identity.

Verify:

- application role cannot migrate;
- migration role owns schema or required objects;
- one application role cannot read another service database;
- Assurance has no Payin/Payout/Ledger database credential.

Store only role names and grant results.

### 13.3 Migration inventory

Record:

```text
migration directory
current version
up command
down policy
one-shot duration
required lock
failure behavior
application compatibility window
```

K0 must decide the K3 input:

```text
one migration Job per database
or
one controlled migration runner iterating service databases
```

The first recommendation is one Job per owner database or one serial owner-aware migration workflow, not application-startup migration.

### 13.4 Redis inventory

For every consumer record:

```text
service
Redis database number
key prefix
purpose
durability required
safe behavior when Redis unavailable
TTL
lock semantics
memory-growth risk
```

Known seed:

```text
Gateway: rate limiting/cache/event support — verify
Auth: cache/session/coordination use — verify
Ledger: coordination/idempotency-related use — verify
Payin: optional distributed breaker, Redis DB 0
Payout: breaker/coordination, Redis DB 0
Fraud: velocity, Redis DB 1
```

Do not depend on Redis database numbers for strong tenant/service isolation in a future managed service without review.

### 13.5 RabbitMQ inventory

Record every:

```text
exchange
exchange type
publisher
routing key
queue
consumer
dead-letter exchange
dead-letter queue
prefetch
consumer concurrency
retry/dead policy
message TTL
durability
publisher confirm
idempotency strategy
```

Start with `ledger.events`, then discover all declarations from code.

Commands may include:

```bash
docker exec <rabbitmq-container> rabbitmqctl list_exchanges
docker exec <rabbitmq-container> rabbitmqctl list_queues \
  name durable auto_delete messages consumers arguments
docker exec <rabbitmq-container> rabbitmqctl list_bindings
```

Use a disposable local environment.

### 13.6 Object and filesystem storage

Inventory all writes outside PostgreSQL/Redis/RabbitMQ:

```text
Auth object store
backup repository
certificate files
encryption key files
temporary files
generated exports
closure artifacts
migration files
observability storage
```

For each path:

```text
writer
reader
sensitive
durability
backup
retention
filesystem mode
read-only compatibility
Kubernetes volume need
cloud replacement
```

### 13.7 Deliverables

```text
docs/deployment/data-ownership-matrix.md
docs/deployment/messaging-matrix.md
docs/deployment/storage-volume-matrix.md
deploy/inventory/data-stores.yaml
deploy/inventory/messaging.yaml
```

### 13.8 Acceptance

- [ ] every service database is listed;
- [ ] app and migration identities are separate;
- [ ] Redis use is complete;
- [ ] RabbitMQ topology is complete;
- [ ] event retry/idempotency is known;
- [ ] every filesystem write is known;
- [ ] every durable path has a K3/K11 decision;
- [ ] no hidden cross-service data access exists.

---

# K0-T6 — Inventory background workers and singleton behavior

## 14. Objective

Prevent accidental duplicate financial workers when Deployments scale.

### 14.1 Worker record

For every worker:

```text
service
worker name
source file
trigger
interval/cron
enabled configuration
batch size
concurrency
lease/lock
singleton scope
idempotency
retry
dead state
shutdown behavior
manual trigger
metrics
alert
replica-scaling safety
```

### 14.2 Worker categories

```text
SAFE_MULTI_REPLICA
SAFE_WITH_DB_LEASE
SAFE_WITH_REDIS_LOCK
SINGLE_ACTIVE_REQUIRED
ONE_SHOT_JOB
MANUAL_ONLY
UNKNOWN_BLOCKER
```

### 14.3 Known worker seed

#### Auth

```text
KYC apply retry
sanctions re-screen
privacy/export/closure workers to verify
```

#### Ledger

```text
transactional outbox relay
trial-balance verifier
daily snapshot
interest accrual
scheduled transaction runner
retention
reconciliation/disbursement workers to verify
```

#### Payout

```text
resume/recovery worker
vendor-command recovery/dead handling
```

#### Admin BFF

```text
session cleanup every five minutes
```

#### Assurance

```text
periodic correlation
alert delivery/retry where implemented
```

#### Gateway

```text
notification event consumer
retention where implemented
```

#### Fraud

```text
ledger event consumer
```

#### VendorService

```text
callback delivery retry
outbound uncertain-attempt recovery
retention where implemented
```

K0 must prove the exact list.

### 14.4 Replica-design output

For each service produce an initial deployment-mode recommendation:

```text
combined server+worker one replica
combined server+worker multiple replicas safe
split worker required before horizontal scaling
feature disabled in first deployment
```

Do not implement the split in K0.

### 14.5 Scheduled-timezone inventory

Record:

- timezone;
- clock source;
- DST assumptions;
- interval versus cron;
- startup catch-up behavior.

Initial cloud target should use UTC at infrastructure level unless application contracts explicitly require another timezone.

### 14.6 Deliverables

```text
docs/deployment/background-job-matrix.md
deploy/inventory/jobs.yaml
```

### 14.7 Acceptance

- [ ] every worker is listed;
- [ ] every worker has a safety category;
- [ ] singleton mechanism is known;
- [ ] retry and idempotency are known;
- [ ] shutdown behavior is known;
- [ ] scale safety is known;
- [ ] first-deployment enabled state is known;
- [ ] no `UNKNOWN_BLOCKER` remains for enabled workers.

---

# K0-T7 — Inventory configuration, secrets, keys, and certificates

## 15. Objective

Create the input for ConfigMaps, Kubernetes Secrets, External Secrets, and mTLS distribution without recording secret contents.

### 15.1 Configuration discovery

Sources:

```text
.env.example
docker-compose.yml
internal/config
cmd/* startup
tests
scripts
```

Generate key names:

```bash
grep -RhoE 'getenv\("[A-Z0-9_]+"\)' internal cmd pkg | sort -u
grep -RhoE '\$\{[A-Z0-9_]+' docker-compose.yml | tr -d '${' | sort -u
```

Adapt commands to the actual config implementation.

### 15.2 Configuration classification

For each key:

```text
service
key
description
required
default
sensitive
environment-specific
reloadable
startup-only
validation
Kubernetes target
owner
```

Kubernetes target:

```text
ConfigMap
Secret
ExternalSecret
Downward API
Service DNS
hard-coded safe constant
remove/deprecate
```

### 15.3 Secret categories

Expected categories:

```text
database passwords
RabbitMQ password
Redis password if enabled
JWT signing secret
internal gRPC token
field-encryption keys
lookup/HMAC keys
Ledger idempotency digest key
export KEK
closure KEK
backup password
backup repository passphrase
mTLS private keys
vendor HMAC/API credentials
KYC provider token
SMTP/push provider credentials
alert webhook URL/token
Admin bootstrap credentials
```

### 15.4 Secret ownership

For each secret record:

```text
owning service
consuming service
shared or dedicated
required to boot
optional feature
rotation support
multiple-version support
file or environment input
current local generator
Kubernetes/cloud target
```

### 15.5 Shared-key finding

Current local configuration shares some key material across multiple services by design.

K0 must classify:

```text
must remain shared
may become per-service
must be split before cloud
```

Do not split cryptographic semantics casually.

### 15.6 Certificate inventory

Current internal identity uses:

```text
shared CA
one leaf identity per service
URI SAN
listener peer allowlist
```

Record:

```text
service identity URI
certificate filename
key filename
CA filename
server/client usage
allowed incoming identities
required outgoing identity
rotation command
current TTL
reload/restart behavior
```

### 15.7 Mount-scope finding

Compose currently mounts a certificate directory.

K0 must determine whether each container can read other services' private keys.

If yes, create a K1/K3 blocker:

```text
Kubernetes must mount only the workload's own leaf key/certificate plus CA.
```

### 15.8 Secret-value scanner

Run the repository's canonical secret and vulnerability checks, then use targeted searches such as:

```bash
git grep -nE 'change-me|BEGIN .*PRIVATE KEY|password\s*='
```

Record findings without copying secret values.

### 15.9 Deliverables

```text
docs/deployment/configuration-matrix.md
docs/deployment/secret-key-matrix.md
docs/deployment/mtls-identity-matrix.md
deploy/inventory/configuration.yaml
deploy/inventory/secrets.yaml
```

### 15.10 Acceptance

- [ ] every configuration key has an owner;
- [ ] every secret is identified;
- [ ] no value appears in evidence;
- [ ] boot-required versus optional is known;
- [ ] key-sharing semantics are known;
- [ ] every mTLS identity is recorded;
- [ ] peer allowlists are recorded;
- [ ] per-workload certificate mount requirement is explicit;
- [ ] rotation/restart behavior is known.

---

# K0-T8 — Inventory vendor ingress and egress

## 16. Objective

Produce the exact input for Traefik callback policy, VendorService NetworkPolicy, Squid ACLs, and cloud static-IP design.

### 16.1 Vendor registry

For every configured adapter record:

```text
canonical vendor name
operations: payin / payout / query / callback
enabled flag
base URL
hostname
port
protocol
TLS behavior
mTLS requirement
authentication method
request-signing method
callback path
callback-signing method
callback CIDRs
timeout
retry
idempotency support
sandbox/production distinction
DNS stability
```

Mock vendors are included because they become K6 test targets.

### 16.2 Callback inventory

Current seed:

```text
VendorService owns POST /webhooks/{vendor}.
```

For each vendor determine:

```text
exact path
HTTP method
maximum body
content type
signature header
timestamp header
event/reference ID
allowed CIDRs
trusted-proxy behavior
duplicate response behavior
owner callback destination
```

### 16.3 Source-IP code review

Trace:

```text
request RemoteAddr
X-Forwarded-For behavior
trusted proxy CIDR configuration
CIDR parser
first/last forwarded address selection
default behavior
fail-open/fail-closed
```

Important current seed:

```text
VENDOR_CALLBACK_CIDRS exists.
VENDOR_CALLBACK_TRUSTED_PROXY_CIDRS is documented in .env.example.
Compose may not currently set the trusted-proxy key.
```

Resolve the actual behavior from code and tests.

### 16.4 Egress inventory

Search all VendorService outbound clients.

Record every DNS name.

No wildcard such as:

```text
*.vendor.com
0.0.0.0/0
arbitrary URL from database
```

may be accepted into K6 without explicit risk classification.

If the adapter base URL is database-configurable, identify:

- validation;
- scheme restrictions;
- hostname allowlist;
- SSRF defense;
- operator authorization.

### 16.5 Proxy compatibility

For each HTTP client verify:

```text
supports explicit proxy URL
CONNECT works for HTTPS
TLS verification remains enabled
custom transport does not bypass proxy
retry uses same proxy
no direct fallback
mTLS client cert works through CONNECT when required
```

### 16.6 DNS and certificate dependencies

Squid and NetworkPolicy need a deliberate DNS path.

Record whether vendors require:

- public DNS;
- private DNS;
- fixed IP;
- certificate revocation/status endpoint access;
- separate authentication host.

### 16.7 Static-IP semantics

K0 documents:

```text
Squid provides forced routing and audit.
Cloud NAT provides stable public source IP.
Shared Cloud NAT does not prove unique VendorService identity.
```

### 16.8 Deliverables

```text
docs/deployment/vendor-network-matrix.md
deploy/inventory/vendor-network.yaml
docs/evidence/k0/network/
```

### 16.9 Acceptance

- [ ] every callback route is known;
- [ ] every callback CIDR is known or explicitly mock-only;
- [ ] source-IP derivation is understood;
- [ ] forwarded-header behavior is understood;
- [ ] every outbound hostname is known;
- [ ] every outbound port is known;
- [ ] proxy compatibility is known;
- [ ] direct fallback behavior is known;
- [ ] K4/K5/K6 have complete policy inputs.

---

# K0-T9 — Inventory startup, readiness, shutdown, and recovery

## 17. Objective

Produce the input for startup probes, readiness probes, conservative liveness probes, rollout timing, and termination grace.

### 17.1 Current health mechanism

Current Compose seed:

```text
/app/service -healthcheck
interval: 5 seconds
timeout: 5 seconds
retries: 10
```

K0 must determine what the command actually checks for each service.

### 17.2 Probe dimensions

For each service record:

```text
startup completion condition
readiness condition
liveness condition
health command behavior
health HTTP path
metrics path
dependency checks
timeout
initial startup duration
failure semantics
```

### 17.3 Readiness policy

Classify dependency checks:

```text
local process only
own database
Redis
RabbitMQ
required internal service
optional internal service
external vendor
```

Recommended principle:

- own required database may affect readiness;
- external vendor outage must not make VendorService Pod unready;
- RabbitMQ outage may degrade event delivery without necessarily disabling all HTTP money queries;
- readiness behavior must follow existing fail-open/fail-closed contracts.

K0 records behavior; K1/K3 implement final probes.

### 17.4 Startup timing measurement

Measure at least five starts:

```text
cold image/container start
warm restart
database available
database initially unavailable then recovers
dependency unavailable
```

Record median and maximum.

### 17.5 Shutdown review

Search:

```bash
grep -R --line-number -E \
  'SIGTERM|SIGINT|signal.Notify|Shutdown\(|GracefulStop|Close\(' \
  cmd internal pkg
```

Record:

```text
HTTP drain
gRPC graceful stop
RabbitMQ consumer stop
worker lease release
DB close
Redis close
shutdown timeout
exit code
```

### 17.6 Termination test

For each long-running service:

1. start full stack;
2. initiate representative request/work;
3. send `docker stop --time ...`;
4. observe logs and state;
5. verify no duplicate/missing financial effect;
6. record required Kubernetes `terminationGracePeriodSeconds`.

### 17.7 Recovery classification

For each dependency:

```text
startup required?
reconnect automatically?
requires restart?
backoff bounded?
alert exists?
```

### 17.8 Deliverables

```text
docs/deployment/health-lifecycle-matrix.md
docs/evidence/k0/service-probes/
```

### 17.9 Acceptance

- [ ] healthcheck semantics are known;
- [ ] startup and readiness are distinguishable;
- [ ] liveness is conservative;
- [ ] external vendor is not an accidental liveness dependency;
- [ ] startup durations are measured;
- [ ] shutdown behavior is measured;
- [ ] termination grace recommendation exists;
- [ ] queue/worker shutdown is safe.

---

# K0-T10 — Inventory image and filesystem behavior

## 18. Objective

Define exact K1 container-hardening work.

### 18.1 Image inventory

Record per image:

```text
Dockerfile target/build arg
base image and digest/tag
runtime user
entrypoint
working directory
binary path
migrations included
CA certificates
timezone data
writable paths
temporary paths
shell availability
debug method
architecture
image size
vulnerability result
```

### 18.2 Current positive seed

```text
distroless static runtime
non-root user
CGO disabled
trimpath
stripped binary
single selected service binary copied
```

### 18.3 Current review items

#### All binaries built for each image

The current builder loop compiles all nine services before copying one selected binary.

K0 records build duration and cache behavior.

K1 decides whether to:

- keep simple shared build;
- use BuildKit target/bake optimization;
- build only selected service.

#### All ports exposed

The generic image declares all known ports.

K1 decides whether to narrow or ignore.

#### Migrations copied into every service image

K0 inventories whether runtime code needs migration files.

K1/K3 decides:

```text
dedicated migration image
or
shared application image used by migration Job
```

#### Distroless debugging

No shell is expected.

Record approved debugging approach:

- ephemeral debug container;
- Kubernetes ephemeral container;
- logs/metrics/traces;
- dedicated debug image outside production path.

#### Filesystem writes

Run each service with a read-only root filesystem in a disposable test where possible.

Record failures.

Known possible writes:

```text
Auth object store
temporary files
CA/certificate reads
export artifacts
```

### 18.4 Image measurement

```bash
docker images --digests | grep seev/
docker history --no-trunc <image>
docker inspect <image>
```

Run the repository image scan target.

### 18.5 Deliverables

```text
docs/deployment/image-runtime-matrix.md
docs/evidence/k0/generated/image-inventory.json
```

### 18.6 Acceptance

- [ ] every image is listed;
- [ ] base/runtime identity is known;
- [ ] writable paths are known;
- [ ] distroless debugging method is defined;
- [ ] migration-file need is known;
- [ ] image size/build duration is measured;
- [ ] K1 tasks are explicit;
- [ ] no image behavior is guessed.

---

# K0-T11 — Measure resource baseline

## 19. Objective

Produce evidence-based initial CPU/memory requests and limits for K3.

K0 does not set final Kubernetes resources.

### 19.1 Why Compose values are insufficient

Current Compose does not provide complete CPU and memory limits for every application service.

Some services or observability components have memory limits.

There are no complete per-service CPU baselines.

Therefore, K0 must measure.

### 19.2 Required workload profiles

#### Profile R0 — Infrastructure idle

```text
PostgreSQL
Redis
RabbitMQ
```

#### Profile R1 — Application idle

```text
all nine services
infrastructure
advanced features disabled
observability disabled
```

#### Profile R2 — Smoke journey

```text
make smoke-test
```

#### Profile R3 — Business journey

```text
make business-e2e
```

#### Profile R4 — Admin journey

```text
make admin-e2e
```

#### Profile R5 — Disposable load smoke

Use only the repository's disposable profile and acknowledgment:

```bash
SEEV_LOAD_ACK=disposable-only make load-smoke
```

Confirm the canonical command from the current Makefile before execution.

#### Profile R6 — Optional observability

Measure full observability separately.

Do not mix its overhead into the application baseline.

### 19.3 Measurements

For each service/profile collect:

```text
CPU average
CPU peak
memory working set
memory peak
process RSS where available
goroutine count
open file descriptors
DB open/in-use/idle connections
Redis connections
RabbitMQ channels/connections
request throughput
p95/p99 latency
queue depth
disk growth
startup time
```

### 19.4 Collection tools

Possible tools:

```bash
docker stats --no-stream
docker inspect
Prometheus
application metrics
PostgreSQL pg_stat_activity
RabbitMQ management/CLI
Redis INFO clients
time
```

Use a sampling script rather than one single `docker stats` snapshot.

Recommended duration:

```text
idle: 10 minutes after stabilization
journey: full journey plus 5-minute recovery
load: complete bounded load window
```

### 19.5 Resource recommendation method

Initial Kubernetes request seed:

```text
CPU request:
steady p95 CPU plus safety margin

Memory request:
steady high-percentile working set plus margin

Memory limit:
measured peak plus larger margin, after leak review

CPU limit:
optional and evidence-based; avoid accidental throttling of latency-sensitive money services
```

Do not mechanically use:

```text
request = limit
same values for all services
```

### 19.6 Initial class output

Classify services:

```text
EDGE_LATENCY
MONEY_CRITICAL
WORKER_HEAVY
ADMIN_LOW_TRAFFIC
AUDIT_BACKGROUND
VENDOR_BOUNDARY
```

### 19.7 Connection-budget output

Create:

```text
service replica assumption
× max DB connections
= total potential connections
```

Do the same for Redis and RabbitMQ.

This becomes a K3/K9 capacity guard.

### 19.8 Node-footprint estimate

Calculate:

```text
sum application requests
+ data dependency requests
+ Traefik
+ Squid
+ Kubernetes/system overhead
+ monitoring
+ 20–30% scheduling headroom
```

Determine whether the initial approximately 4-vCPU/16-GiB node target remains reasonable.

Do not force the result to match the roadmap assumption.

### 19.9 Deliverables

```text
docs/deployment/resource-baseline.md
docs/evidence/k0/resources/
deploy/inventory/resource-baseline.yaml
```

### 19.10 Acceptance

- [ ] all six required profiles are measured or explicitly deferred;
- [ ] each service has idle and journey data;
- [ ] peaks and steady state are distinguished;
- [ ] database/Redis/RabbitMQ connections are counted;
- [ ] observability overhead is separate;
- [ ] initial request/limit ranges are recommended;
- [ ] node-footprint estimate exists;
- [ ] no load test touched non-disposable data.

---

# K0-T12 — Freeze first Kubernetes deployment scope

## 20. Objective

Prevent Kubernetes work from becoming mixed with feature activation.

### 20.1 First deployment purpose

The first deployment proves infrastructure behavior:

```text
routing
service discovery
mTLS
persistence
messaging
callback ingress
forced vendor egress
health
shutdown
observability
```

It is not a demonstration of every roadmap feature.

### 20.2 Required enabled components

```text
Gateway
Auth
Ledger
Payin
Payout
Fraud
Admin BFF
Assurance
VendorService
PostgreSQL
Redis
RabbitMQ
Traefik
Squid
basic metrics/logs
migration Jobs
mock vendors
```

### 20.3 Initial edge exposure

```text
PUBLIC:
api.dev.seev.example -> Gateway and explicit Auth decision

CALLBACK:
callback.dev.seev.example -> VendorService /webhooks/{vendor}

PRIVATE:
Admin BFF
health
metrics
RabbitMQ management
PostgreSQL
Redis
all service admin endpoints
all gRPC
all internal HTTP
```

### 20.4 Feature flags

Create a complete table:

```text
feature
service
local current default
first Kubernetes value
reason
test that requires it
```

Recommended disabled unless needed:

```text
real vendor adapters
real KYC provider
USD/FX
interest capitalization
scheduled transaction execution
top-up fee
production email/push
advanced migration engine
C2 data platform
Vault dev mode
full observability in first cloud
```

Keep existing core money journey enabled with mock vendors.

### 20.5 Worker enablement

Do not disable workers required for correctness.

Examples:

```text
Ledger outbox: enabled
Payout recovery: enabled
Assurance: enabled but bounded
optional interest: disabled
optional schedules: disabled unless test target
```

Actual keys must come from T6/T7.

### 20.6 Data policy

Use:

```text
synthetic users
synthetic money
mock vendors
disposable volumes
no copied production data
```

### 20.7 Deliverables

```text
docs/deployment/feature-scope.md
deploy/inventory/first-deployment-scope.yaml
```

### 20.8 Acceptance

- [ ] enabled services are explicit;
- [ ] public routes are explicit;
- [ ] advanced features are explicitly disabled;
- [ ] required correctness workers remain enabled;
- [ ] only synthetic data is allowed;
- [ ] first deployment has one test objective;
- [ ] no feature is enabled accidentally through a local default.

---

# K0-T13 — Classify gaps and downstream ownership

## 21. Objective

Turn findings into bounded K1–K6 work instead of fixing everything inside K0.

### 21.1 Finding severity

```text
BLOCKER
HIGH
MEDIUM
LOW
INFORMATIONAL
```

### 21.2 Ownership tracks

```text
K1 container/runtime hardening
K2 local kind + Calico
K3 Helm chart
K4 Traefik/Gateway API
K5 NetworkPolicy
K6 Squid egress proxy
K7 observability
K8 Terraform
K9 GCP
LATER production readiness
```

### 21.3 Expected findings to confirm

Potential expected findings:

1. Compose certificate-directory mount may expose more leaf keys than one workload needs.
2. Healthcheck command may not distinguish startup/readiness/liveness.
3. Generic image exposes all ports.
4. Migrations are copied into every image.
5. Full background-worker set may not be safe under multiple replicas.
6. Resource limits are incomplete.
7. Local callback CIDRs are not cloud callback CIDRs.
8. Trusted-proxy behavior must be reconciled with Traefik and `externalTrafficPolicy: Local`.
9. Admin BFF secure-cookie setting differs between local and TLS deployment.
10. PostgreSQL SSL is disabled locally.
11. Redis authentication may be disabled locally.
12. RabbitMQ local credentials are not cloud credentials.
13. Object-store persistence is local-volume based.
14. Full observability exceeds the first cloud resource budget.
15. Vendor adapters need explicit proxy support tests.
16. Docker Compose `depends_on` has no Kubernetes equivalent and should not be copied as startup ordering.

These are hypotheses until verified.

### 21.4 Risk record template

```markdown
## K0-FINDING-001 — Example title

- Severity:
- Evidence:
- Current behavior:
- Kubernetes risk:
- Required decision:
- Owner track:
- Acceptance test:
- Blocks:
- Status:
```

### 21.5 Deliverables

```text
docs/deployment/deployment-risk-register.md
```

### 21.6 Acceptance

- [ ] every gap has severity;
- [ ] every gap has an owner;
- [ ] every blocker has an acceptance test;
- [ ] K0 does not absorb unrelated implementation work;
- [ ] no unowned `UNKNOWN` remains;
- [ ] K1–K6 scope is generated from evidence.

---

# K0-T14 — Generate K1–K6 input contracts

## 22. Objective

Make the next plans consume a stable interface.

### 22.1 K1 input — Container readiness

Required:

```text
image list
runtime user
writable paths
health semantics
shutdown timing
CA/timezone needs
migration-image decision
build inefficiencies
vulnerability baseline
```

### 22.2 K2 input — Local kind + Calico

Required:

```text
service list
ports
data dependencies
persistent volumes
first feature scope
test commands
local DNS names
resource estimate
```

### 22.3 K3 input — Helm

Required:

```text
canonical names
container ports
config keys
secret keys
volume mounts
service accounts
replica-safety classification
migration Jobs
resources
probes
```

### 22.4 K4 input — Traefik

Required:

```text
public routes
callback routes
private routes
TLS hosts
source-IP requirement
body/rate limits
trusted-proxy rule
```

### 22.5 K5 input — NetworkPolicy

Required:

```text
internal call matrix
database ownership
Redis/RabbitMQ clients
DNS
telemetry
callback ingress
admin access
default-deny exceptions
```

### 22.6 K6 input — Squid

Required:

```text
VendorService Pod selector
proxy port
vendor hostnames/ports
DNS behavior
CONNECT policy
explicit client proxy config
no-direct-fallback test
logging/redaction fields
```

### 22.7 K0 contract file

Create:

```text
docs/deployment/k1-input-contract.md
```

It contains links and hashes of the final inventory files.

### 22.8 Acceptance

- [ ] K1 has complete runtime input;
- [ ] K2 has complete topology input;
- [ ] K3 has complete values input;
- [ ] K4 has complete route input;
- [ ] K5 has complete connection input;
- [ ] K6 has complete egress input;
- [ ] downstream plans need no rediscovery of baseline facts.

---

# K0-T15 — Final verification and close

## 23. Objective

Prove the inventory is complete and repository behavior remains unchanged.

### 23.1 Repository verification

Run the canonical current commands:

```bash
make verify-static
make contracts
make docs-check
git diff --check
```

Because K0 affects deployment documentation and possibly inventory scripts, run:

```bash
make verify-full
```

Keep chaos separate unless K0 changed runtime behavior—which it should not.

### 23.2 Runtime verification

From clean disposable volumes:

```bash
make docker-up
make migrate-up-all
docker compose --profile app up --build -d
make smoke-test
make business-e2e
make admin-e2e
```

Run privacy and load profiles according to the repository's current safe instructions.

### 23.3 Inventory consistency checks

Recommended script:

```text
scripts/deployment-inventory-check.sh
```

Checks:

- every Compose app service is in `services.yaml`;
- every Dockerfile service build value is in `services.yaml`;
- every inventory port appears in code/config;
- no duplicate port owner without documented reason;
- every database has one owner;
- every public route is in `routes.yaml`;
- every configured vendor hostname is in `vendor-network.yaml`;
- no inventory file contains secret values;
- all Markdown links work.

### 23.4 Final acceptance document

Create:

```text
docs/evidence/k0/final-acceptance.md
```

Include:

```text
baseline commit
verification results
inventory file hashes
remaining findings
blocker count
K1 entry decision
reviewer
date
```

### 23.5 Acceptance

- [ ] full repository gate passes;
- [ ] business behavior is unchanged;
- [ ] inventory consistency script passes;
- [ ] no secret value exists in artifacts;
- [ ] blocker count for starting K1 is zero or explicitly accepted;
- [ ] final acceptance is signed/recorded;
- [ ] K0 status changes to complete.

---

## 24. Detailed deliverable templates

### 24.1 Service runtime inventory

```markdown
## Service: VendorService

### Identity

- Canonical Kubernetes name:
- Current Compose name:
- Go entrypoint:
- APP_NAME:
- Docker build argument:
- Image:
- Binary:

### Roles

- HTTP server:
- gRPC server:
- Callback edge:
- Background workers:
- Admin handlers:

### Ownership

- Database:
- Tables:
- External protocol:
- Secrets:

### Dependencies

- Inbound:
- Outbound internal:
- Outbound external:
- RabbitMQ:
- Redis:

### Lifecycle

- Startup:
- Readiness:
- Shutdown:
- Replica safety:

### First deployment

- Enabled:
- Public:
- Callback:
- Private:
- Feature flags:

### Evidence

- Files:
- Tests:
- Runtime command:
```

### 24.2 Port matrix

| Service | Name | Port | Protocol | TLS | Authentication | Bind | Exposure | Probe/metrics | Evidence |
|---|---|---:|---|---|---|---|---|---|---|

### 24.3 Internal call matrix

| Source | Destination | Protocol/port | mTLS identity | Timeout | Retry | Failure posture | Readiness class | Evidence |
|---|---|---|---|---:|---|---|---|---|

### 24.4 Secret matrix

| Key name | Owner | Consumers | Required | Input form | Shared? | Rotation | Kubernetes target | Never log |
|---|---|---|---|---|---|---|---|---|

### 24.5 Worker matrix

| Service | Worker | Trigger | Concurrency | Lock/lease | Idempotency | Retry | Replica safety | First deployment |
|---|---|---|---:|---|---|---|---|---|

### 24.6 Vendor matrix

| Vendor | Direction | Host | Port | Proxy | Auth/signing | Callback path | Callback CIDRs | Timeout | Enabled |
|---|---|---|---:|---|---|---|---|---:|---|

---

## 25. Machine-readable schema recommendations

### 25.1 `services.yaml`

```yaml
version: 1
baseline_commit: "<git-sha>"
services:
  - name: gateway
    source:
      command: cmd/gateway
      compose: gateway-service
      image: seev/gateway-service
    roles:
      http: true
      grpc: false
      workers: true
    database:
      name: seev_gateway
      app_user: gateway_app
    exposure:
      public: true
      callback: false
      admin: false
    enabled_in_first_deployment: true
```

### 25.2 `ports.yaml`

```yaml
version: 1
listeners:
  - service: gateway
    name: public-http
    port: 8080
    transport: tcp
    protocol: http
    tls: false
    kubernetes_exposure: public-edge
```

Internal application TLS behind Traefik must be recorded from actual runtime behavior rather than assumed from the public example.

### 25.3 `dependencies.yaml`

```yaml
version: 1
calls:
  - source: payin
    destination: vendor
    protocol: grpc
    port: 9098
    mtls: true
    readiness_class: operation-required
```

### 25.4 `vendor-network.yaml`

```yaml
version: 1
vendors:
  - name: mockvendor
    enabled_in_first_deployment: true
    outbound:
      scheme: https
      hosts:
        - mock-vendor.local
      ports:
        - 443
      proxy_required: true
      direct_fallback_allowed: false
    callback:
      path: /webhooks/mockvendor
      methods:
        - POST
      source_cidrs:
        - <local-test-cidr>
      signature_required: true
```

---

## 26. Recommended helper scripts

K0 may add safe read-only scripts.

### 26.1 `scripts/deployment-inventory-generate.sh`

Responsibilities:

- normalize Compose;
- list app services;
- list configured ports;
- extract non-sensitive environment key names;
- list images;
- emit generated JSON/YAML drafts.

It must not:

- read secret file contents;
- emit `.env` values;
- mutate infrastructure.

### 26.2 `scripts/deployment-resource-sample.sh`

Responsibilities:

- sample Docker stats at a bounded interval;
- write timestamped CPU/memory CSV;
- record container names and image revisions;
- stop cleanly.

### 26.3 `scripts/deployment-network-probe.sh`

Responsibilities:

- probe known local ports;
- verify expected allowed calls;
- verify expected denied direct routes where current local controls exist;
- never call a real vendor.

### 26.4 `scripts/deployment-inventory-check.sh`

Responsibilities are described in T15.

All scripts require:

```text
set -eu
ShellCheck compatibility
no secret output
bounded execution
clear cleanup
```

---

## 27. K0 review checklist by role

### 27.1 Application reviewer

- [ ] service list correct;
- [ ] route list correct;
- [ ] configuration list correct;
- [ ] worker list correct;
- [ ] shutdown behavior correct.

### 27.2 Data reviewer

- [ ] database ownership correct;
- [ ] migration identity correct;
- [ ] Redis use correct;
- [ ] RabbitMQ topology correct;
- [ ] storage durability correct.

### 27.3 Security reviewer

- [ ] public surfaces minimal;
- [ ] secrets complete;
- [ ] mTLS identities complete;
- [ ] callback controls complete;
- [ ] vendor egress list finite;
- [ ] no secret values in evidence.

### 27.4 Platform reviewer

- [ ] ports usable in Kubernetes;
- [ ] probes can be designed;
- [ ] resource baseline usable;
- [ ] replica-safety classification usable;
- [ ] volume needs usable;
- [ ] K1–K6 inputs complete.

For a solo project, the same person may perform all reviews at separate times, but the checklist remains separate.

---

## 28. K0 execution order

Recommended exact order:

```text
Session 1
T0 baseline
T1 deployables
T2 ports
T3 routes

Session 2
T4 internal calls
T5 data stores and messaging
T6 workers

Session 3
T7 config/secrets/mTLS
T8 vendor network
T9 lifecycle

Session 4
T10 image/filesystem
T11 resource measurements

Session 5
T12 feature scope
T13 findings
T14 downstream contracts
T15 final verification
```

This is an ordering guide, not a time commitment.

Do not rush T11 resource measurement to fit one session.

---

## 29. Recommended pull-request sequence

K0 may remain one documentation-focused PR if small enough, but the preferred sequence is:

```text
PR 1 — Baseline, service, port, route, and dependency inventory
PR 2 — Data, messaging, worker, secret, mTLS, and vendor inventory
PR 3 — Lifecycle, image, resource baseline, feature scope, and final gate
```

No PR should include Kubernetes manifests.

---

## 30. Risks specific to K0

### 30.1 Documentation drift

Mitigation:

- machine-readable inventory;
- consistency script;
- file references;
- baseline SHA.

### 30.2 Secret leakage

Mitigation:

- record key names only;
- redact command output;
- scan evidence;
- never archive `.env`.

### 30.3 Incomplete dynamic topology

RabbitMQ queues or workers may be created only when a feature runs.

Mitigation:

- execute core E2E;
- inspect code declarations;
- compare before/after runtime topology.

### 30.4 Resource baseline distortion

One short sample can miss peaks.

Mitigation:

- multiple profiles;
- time series;
- repeated runs;
- record environment.

### 30.5 Accidental feature activation

Mitigation:

- freeze feature scope;
- use disposable data;
- no real credentials;
- review effective Compose configuration.

### 30.6 Treating local TLS as cloud design

Mitigation:

- inventory semantics;
- defer Kubernetes secret distribution to K3;
- do not copy host mount layout blindly.

---

## 31. K0 completion criteria

K0 is complete only when all required criteria pass.

### Baseline

- [ ] exact commit recorded;
- [ ] clean/known working tree;
- [ ] environment recorded;
- [ ] evidence handling defined.

### Runtime

- [ ] every deployable process classified;
- [ ] canonical names frozen;
- [ ] request and worker roles known.

### Network

- [ ] every port known;
- [ ] every route classified;
- [ ] every internal call known;
- [ ] callback ingress known;
- [ ] vendor egress known;
- [ ] no unknown enabled traffic edge.

### Data

- [ ] database ownership complete;
- [ ] migration identity complete;
- [ ] Redis use complete;
- [ ] RabbitMQ topology complete;
- [ ] storage volumes complete.

### Security

- [ ] every secret key identified;
- [ ] no secret value recorded;
- [ ] mTLS identities complete;
- [ ] certificate mount risk classified;
- [ ] callback source-IP logic understood.

### Lifecycle

- [ ] health semantics understood;
- [ ] startup measured;
- [ ] shutdown measured;
- [ ] worker replica safety complete.

### Resources

- [ ] idle profile measured;
- [ ] business journey measured;
- [ ] disposable load measured;
- [ ] observability measured separately;
- [ ] connection budget exists;
- [ ] node-footprint estimate exists.

### Scope

- [ ] first deployment services frozen;
- [ ] edge routes frozen;
- [ ] advanced features disabled explicitly;
- [ ] synthetic-data policy frozen.

### Handoff

- [ ] K1 input complete;
- [ ] K2 input complete;
- [ ] K3 input complete;
- [ ] K4 input complete;
- [ ] K5 input complete;
- [ ] K6 input complete;
- [ ] final verification passes;
- [ ] no unowned blocker remains.

---

## 32. K0 evidence log

| Evidence | Commit / artifact | Result | Notes |
|---|---|---:|---|
| Baseline SHA and environment |  |  |  |
| Deployable process inventory |  |  |  |
| Canonical service naming |  |  |  |
| Port runtime verification |  |  |  |
| Route classification |  |  |  |
| Public exposure freeze |  |  |  |
| Internal call matrix |  |  |  |
| Database ownership |  |  |  |
| Migration identity |  |  |  |
| Redis inventory |  |  |  |
| RabbitMQ runtime topology |  |  |  |
| Storage/volume inventory |  |  |  |
| Worker inventory |  |  |  |
| Worker replica safety |  |  |  |
| Configuration-key inventory |  |  |  |
| Secret-key inventory |  |  |  |
| mTLS identity matrix |  |  |  |
| Certificate mount review |  |  |  |
| Vendor callback inventory |  |  |  |
| Vendor outbound inventory |  |  |  |
| Proxy compatibility review |  |  |  |
| Health semantics |  |  |  |
| Graceful-shutdown test |  |  |  |
| Image inventory |  |  |  |
| Read-only filesystem review |  |  |  |
| Idle resource baseline |  |  |  |
| Business resource baseline |  |  |  |
| Disposable load baseline |  |  |  |
| Connection budget |  |  |  |
| Node-footprint estimate |  |  |  |
| First-deployment feature scope |  |  |  |
| Risk register |  |  |  |
| Inventory consistency check |  |  |  |
| `make verify-full` |  |  |  |
| Final K0 acceptance |  |  |  |

---

## 33. Immediate first actions

Execute only these first:

```bash
git rev-parse HEAD
git status --short
go version
docker version
docker compose version

mkdir -p docs/evidence/k0/command-output
docker compose --profile app config \
  > docs/evidence/k0/compose-normalized.yaml

find cmd -mindepth 1 -maxdepth 1 -type d -print | sort \
  > docs/evidence/k0/command-output/cmd-directories.txt

docker compose --profile app config --services \
  > docs/evidence/k0/command-output/compose-app-services.txt
```

Then create the first two authoritative matrices:

```text
service-runtime-inventory.md
port-protocol-matrix.md
```

Do not start Helm, Traefik, Calico, Terraform, GKE, or AWS work until K0 is closed.
