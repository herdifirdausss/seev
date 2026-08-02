# Plan 63 — Kubernetes Cloud Deployment Learning Roadmap

**Created:** 2026-07-29  
**Revised:** 2026-07-29 after architecture, security, networking, and cost review  
**Status:** Revised and recommended for execution  
**Project:** Seev  
**Primary learning target:** Deploy Seev on managed Kubernetes with Traefik,
controlled vendor callback ingress, controlled vendor egress, observability,
GitOps, and cloud-neutral infrastructure  
**Recommended first cloud:** Google Cloud Platform  
**Second-cloud portability target:** Amazon Web Services  
**Initial environment:** Non-production learning sandbox  
**Important:** This plan does not claim production readiness. It builds the
deployment foundation required before the wider production-readiness roadmap.

---

## 1. Goal

Build a realistic cloud deployment for Seev that teaches:

- Kubernetes workload deployment;
- cloud networking;
- Traefik as ingress controller/API gateway;
- Kubernetes Gateway API;
- static inbound IP;
- vendor callback IP allowlisting;
- callback signature verification;
- private workloads;
- static outbound IP;
- an explicit outbound proxy for VendorService;
- NetworkPolicy-based egress restriction;
- managed secrets;
- TLS and DNS;
- autoscaling;
- observability;
- CI/CD and GitOps;
- infrastructure as code;
- backup and recovery basics;
- cloud cost controls;
- AWS/GCP portability.

The roadmap intentionally starts as a learning environment.

---

## 1.1 Architecture review verdict

The original direction is fundamentally correct and should be retained:

- local Kubernetes before cloud;
- GCP before AWS;
- Traefik at the edge;
- callback source-IP restrictions plus application signatures;
- private nodes;
- static outbound NAT;
- forced VendorService egress through a proxy;
- infrastructure as code;
- GitOps only after manual deployment is understood.

The review found several material improvements:

1. Local NetworkPolicy tests must run on a CNI that actually enforces policy.
   Use `kind + Calico`; do not assume the default kind/k3d network enforces
   policies.
2. Vendor callback source IP must be preserved using the cloud load-balancer
   path and `externalTrafficPolicy: Local`.
3. Do not trust arbitrary `X-Forwarded-For`. With a direct L4 passthrough load
   balancer, prefer the actual connection source IP.
4. Use manually assigned Cloud NAT public IPs from the beginning. Switching
   from automatic to manual NAT allocation later does not preserve the old IP
   and disrupts active connections.
5. Use Squid as the first explicit forward proxy. Envoy is a useful later
   exercise, but it adds unnecessary policy complexity to the first deployment.
6. Keep Admin BFF non-public in the first cloud stage.
7. Do not deploy Prometheus, Loki, Tempo, and the full application/data stack
   to a tiny first cloud node without a resource budget.
8. Treat split callback ingress as an optional production-like exercise, not a
   prerequisite for the first successful cloud deployment.
9. Treat Terraform as provider-specific infrastructure with shared conventions,
   not as one false cloud-neutral module implementation.
10. Do not run AWS and GCP clusters concurrently. Finish and destroy GCP first,
    then use the remaining learning budget for AWS portability.
11. Traefik is the network edge and routing gateway. Business authentication,
    API-key scope, idempotency, financial policy, and vendor-signature truth
    remain in Seev services.
12. A shared cluster NAT IP proves stable egress, but it is not a unique
    VendorService identity because other workloads may use the same NAT IP.

### Recommended minimum viable cloud proof

```text
private GKE Standard zonal cluster
+ GKE Dataplane V2
+ one on-demand node pool
+ one reserved regional ingress IPv4
+ one manually assigned Cloud NAT IPv4
+ one Traefik deployment
+ public Gateway route
+ callback route with source-IP allowlist
+ Admin BFF private
+ Squid explicit forward proxy
+ VendorService default-deny egress
+ PostgreSQL/Redis/RabbitMQ in-cluster for sandbox only
+ lightweight metrics and logs
```

Only add Cloud SQL, split callback ingress, Argo CD, autoscaling, and AWS after
this proof is complete.

It must not immediately attempt:

- regional high availability;
- multi-region active-active;
- production customer data;
- real customer funds;
- every C1–C6 feature enabled;
- all databases running as independent managed instances;
- service mesh;
- eBPF-specific platform dependency;
- full production compliance;
- zero-trust perfection on the first deployment.

---

## 2. Recommended cloud order

## 2.1 Start with GCP

Use GCP first because:

- new eligible accounts currently receive a larger welcome credit than AWS;
- GKE provides a monthly cluster-management free-tier credit for one eligible
  Autopilot or zonal Standard cluster;
- GKE, Cloud NAT, Artifact Registry, Cloud DNS, Secret Manager, and Cloud SQL
  form a straightforward learning path;
- reserved static IP and Cloud NAT integrate directly with private GKE nodes;
- the same Kubernetes application manifests can later be moved to EKS.

Recommended learning configuration:

```text
GKE Standard
zonal cluster
VPC-native networking
private worker nodes
public control-plane endpoint initially restricted to approved addresses,
or access through Cloud Shell; private control-plane endpoint is a later step
GKE Dataplane V2
Regular release channel
one on-demand node pool
no Spot node pool in the first cloud proof
Cloud NAT with one manually assigned reserved regional IPv4
one reserved regional ingress IPv4
a pinned, currently supported Traefik 3.x minor and image digest
Kubernetes Gateway API version tested against the pinned Traefik release
Artifact Registry
Secret Manager when external secrets are introduced
PostgreSQL, RabbitMQ, and Redis initially in cluster
Cloud SQL PostgreSQL only after the first networking proof
```

Why Standard instead of Autopilot for the first implementation:

- easier to learn node pools;
- easier to understand node subnet and NAT behavior;
- more control over scheduling and daemon workloads;
- clearer relationship between node capacity and pod resource requests;
- easier experimentation with dedicated egress infrastructure.

Autopilot can become a later cost/operations comparison.

## 2.2 Port to AWS second

After the GCP deployment is stable, documented, and destroyed to stop
billing, reuse the cloud-independent Kubernetes application manifests on:

```text
Amazon EKS
private worker nodes
NAT Gateway with Elastic IP
Network Load Balancer
Amazon ECR
AWS Secrets Manager
Amazon RDS PostgreSQL when ready
```

The AWS port should prove that the application layer is not dependent on GCP
annotations or services.

Before creating EKS, verify that EKS is available under the selected AWS account
plan. AWS Free accounts expose only a subset of services; a Paid plan may be
required even when promotional credits remain available. Do not assume that a
Free plan automatically permits the complete EKS/NAT/NLB stack.

## 2.3 Current credit posture to recheck before signup

As of plan creation:

```text
GCP:
USD 300 welcome credit
90-day validity
GKE free-tier cluster-management credit for one eligible zonal Standard or
Autopilot cluster

AWS:
USD 100 initial credit for new eligible accounts
up to USD 100 additional credits through eligible exploration tasks
free plan lasting up to six months or until credits are exhausted
EKS standard cluster management is billed per cluster-hour
```

Cloud offers can change.

Recheck the official billing and eligible-service pages immediately before
creating an account.

Current review notes:

- Google Cloud still advertises a USD 300 welcome credit for 90 days.
- GKE currently provides USD 74.40 of monthly cluster-management credit,
  equivalent to one eligible Autopilot or zonal Standard cluster; compute,
  storage, NAT, load balancing, and data transfer remain billable.
- AWS currently advertises USD 100 initial credit and up to USD 100 additional
  credit for eligible new customers.
- EKS standard-support control-plane pricing remains a fixed per-cluster hourly
  charge.
- AWS NAT Gateway has both hourly and per-GB processing charges, making the AWS
  portability exercise materially more expensive if it is left running.

---

## 3. Cost strategy

## 3.1 Main cost traps

The components most likely to consume the learning credits are:

```text
worker nodes
managed PostgreSQL
NAT
load balancers
persistent disks
log ingestion and retention
cross-zone or internet data transfer
unused static IPs
multiple Kubernetes clusters
```

## 3.2 Learning modes

### Mode A — Local Kubernetes

Cost:

```text
no cloud cost
```

Components:

```text
kind with Calico
Traefik
PostgreSQL
Redis
RabbitMQ
Prometheus
Grafana
Loki
Tempo
mock vendor
egress proxy
```

Purpose:

- validate Kubernetes manifests;
- learn routing;
- test callback allowlist;
- test NetworkPolicy;
- test proxy behavior;
- test failure handling.

### Mode B — Cloud sandbox, cost-optimized

Components:

```text
one zonal GKE cluster
one on-demand node pool
one external load balancer
one manually assigned NAT public IP
PostgreSQL/Redis/RabbitMQ in cluster
Prometheus + Grafana only, or cloud-native basic monitoring
Loki and Tempo deferred until resource usage is measured
short log retention
no HA
Admin BFF not public
```

Purpose:

- learn cloud networking;
- prove static ingress/egress;
- prove TLS and DNS;
- prove image registry and GitOps.

Not suitable for real money.

### Mode C — Production-like cloud sandbox

Components:

```text
private multi-node cluster
managed PostgreSQL
managed Redis where available
durable RabbitMQ deployment or managed broker
two Traefik replicas
two egress proxy replicas
observability
backup and restore
autoscaling
```

Purpose:

- practice realistic operations;
- load and chaos tests;
- measure cost.

Still not a real production launch.

## 3.3 Cost guardrails

Before creating infrastructure:

- create budget alerts;
- set low budget thresholds;
- create a dedicated cloud project/account;
- avoid a regional Kubernetes control plane initially;
- use one cluster;
- use one public load balancer initially;
- reserve only the IPs actually used;
- cap log retention;
- configure Terraform destroy;
- label every resource;
- create a daily cost dashboard;
- stop or delete the environment when not being used;
- avoid creating both AWS and GCP clusters at the same time.

---

## 4. Target architecture

## 4.1 High-level architecture

```text
Internet users / merchants
          |
          v
Reserved static inbound IP
          |
          v
Cloud network load balancer
          |
          v
Traefik Public Gateway
          |
          v
       Gateway
          |
          v
Internal Kubernetes services
Auth / Ledger / Payin / Payout / Fraud / Assurance
          |
          v
PostgreSQL / Redis / RabbitMQ
```

Administrative path during the first cloud stage:

```text
Authorized operator
        |
        v
kubectl port-forward / Cloud Shell / authenticated tunnel
        |
        v
Admin BFF ClusterIP
```

Vendor callback path:

```text
Vendor callback source IP
          |
          v
Reserved static inbound IP
          |
          v
Traefik callback route
          |
          v
IP allowlist middleware
          |
          v
rate/body/header protections
          |
          v
VendorService callback endpoint
          |
          v
signature + timestamp + idempotency validation
```

Vendor outbound path:

```text
VendorService Pod
          |
          | NetworkPolicy allows only proxy
          v
Egress Proxy
          |
          | private node/subnet route
          v
Cloud NAT / NAT Gateway
          |
          | reserved static public IP
          v
Vendor API
```

Important:

```text
Egress proxy
!=
static IP provider
```

The NAT service provides the stable public source IP.

The proxy provides:

- destination allowlisting;
- protocol restrictions;
- centralized connection audit;
- per-vendor timeout policy;
- emergency pause;
- proof that VendorService cannot connect directly.

---

## 5. Ingress topology decisions

## 5.1 Phase-one topology

Use one Traefik deployment and one external load balancer.

Hostnames:

```text
api.dev.seev.example
callback.dev.seev.example
```

Routes:

```text
api host      -> Gateway
callback host -> VendorService callback routes only
```

Admin BFF remains `ClusterIP` in the first cloud stage and is accessed through
an authenticated administrative tunnel such as `kubectl port-forward`,
Cloud Shell, or a later identity-aware/private access path. Do not expose it
through the public load balancer merely for convenience.

Advantages:

- cheaper;
- one load balancer;
- easier first implementation.

Trade-off:

- one ingress failure domain;
- callback and public API share the same edge.

## 5.2 Phase-two topology

Split edge responsibilities:

```text
Traefik Public
- Gateway
- merchant API
- public frontend

Traefik Callback
- VendorService callback only
- dedicated static inbound IP
- dedicated allowlist
- stricter limits

Traefik Admin/Internal
- Admin BFF
- private load balancer or identity-aware access
```

Advantages:

- independent callback controls;
- independent static callback IP;
- smaller blast radius;
- cleaner trusted proxy configuration;
- easier vendor onboarding.

Trade-off:

- extra load balancer cost;
- more certificates and DNS;
- more operations.

## 5.3 Recommended timing

```text
Local:
single Traefik

First cloud deployment:
single Traefik

Production-like deployment:
split callback Traefik

Real production candidate:
split public, callback, and admin edges
```

---

## 6. Traefik design

## 6.1 Version and installation

Use a pinned, currently supported Traefik 3.x Helm-chart version and image
digest.

At review time, official documentation is available for Traefik 3.6, but the
repository should pin the exact minor version that passes the Seev compatibility
suite rather than following documentation `master` or an unbounded `v3`.

Do not use `latest`.

Installation requirements:

- two replicas in production-like mode;
- PodDisruptionBudget;
- topology spread;
- anti-affinity where capacity allows;
- non-root security context;
- read-only root filesystem where supported;
- restricted RBAC;
- dashboard disabled publicly;
- metrics enabled internally;
- access logs structured and redacted;
- separate entry points for HTTP and HTTPS;
- HTTP redirected to HTTPS;
- `externalTrafficPolicy: Local` on the external Traefik Service when
  original client IP preservation is required;
- forwarded-header insecure mode disabled;
- forwarded headers trusted only from a real upstream L7 proxy when one exists;
- PROXY protocol enabled only when the selected load-balancer path explicitly
  provides and requires it;
- no assumption that a direct L4 load balancer inserts a trustworthy
  `X-Forwarded-For`.

## 6.2 Gateway API

Prefer Kubernetes Gateway API resources after pinning and testing one
compatible Gateway API CRD version:

```text
GatewayClass
Gateway
HTTPRoute
GRPCRoute
ReferenceGrant where required
```

Use Traefik CRDs only for middleware features that are not cleanly represented
by core Gateway API filters.

## 6.3 Middleware chains

### Public API chain

```text
request ID
security headers
body-size limit
rate limit
in-flight request limit
CORS where required
compression where safe
access log
```

### Vendor callback chain

```text
trusted source IP extraction
IP allowlist
strict method
strict path
body-size limit
in-flight request limit
rate limit
request timeout
security headers
access log
```

### Admin chain

```text
private or identity-aware access
CSRF handled by Admin BFF
stricter rate limit
security headers
access audit
```

## 6.4 TLS

Use cert-manager rather than Traefik's built-in ACME storage for the
production-like path.

Local:

```text
self-signed or local CA
```

Cloud learning:

```text
Let's Encrypt staging
then Let's Encrypt production
```

Production-like:

```text
cloud-managed DNS challenge
separate certificates per edge
automatic renewal alerts
```

---

## 7. Vendor callback security

## 7.1 IP allowlist is not authentication

Callback acceptance requires all of:

```text
vendor source IP allowlist
HTTPS
signature validation
timestamp/freshness validation
idempotency
strict amount/currency/reference correlation
bounded body
stable error handling
replay detection
```

Vendor NAT or infrastructure can change.

The allowlist must be configurable and audited.

## 7.2 Source-IP correctness

A dangerous configuration is:

```text
trust every X-Forwarded-For header
```

An attacker could spoof the vendor IP.

Required approach:

1. use an external L4 passthrough load balancer for Traefik;
2. configure the Traefik `Service` with `externalTrafficPolicy: Local`;
3. verify that the Traefik Pod observes the original vendor source IP;
4. leave forwarded-header insecure mode disabled;
5. configure `forwardedHeaders.trustedIPs` only if an actual upstream L7 proxy
   is deliberately added;
6. use trusted PROXY-protocol sources only when the load balancer is configured
   for PROXY protocol;
7. verify the observed source IP and spoofing behavior with integration tests.

`externalTrafficPolicy: Local` preserves the original client IP, but it also
routes traffic only to nodes with a ready local Traefik endpoint. Health checks,
replica placement, and node-drain tests are therefore mandatory.

## 7.3 Route isolation

Use a dedicated hostname:

```text
callback.dev.seev.example
```

Expose only callback routes.

Do not expose:

```text
internal admin endpoints
health details
debug endpoints
VendorService outbound/vendor-management endpoints
```

## 7.4 Edge-layer allowlist strategy

During the single-load-balancer phase, apply the vendor CIDR allowlist through
Traefik because the same load balancer also serves the public API.

After callback ingress is split, enforce the CIDRs twice:

```text
cloud load-balancer source ranges/firewall
+
Traefik IPAllowList
```

The cloud control reduces traffic reaching the cluster; Traefik remains the
route-level defense and audit point.

## 7.5 Allowlist source

Store vendor CIDRs in:

```text
Git-managed environment configuration
or
a controlled generated secret/config
```

Changes require:

- vendor evidence;
- review;
- audit;
- canary test;
- expiry/review date.

## 7.6 Vendor without stable callback IP

If a vendor cannot provide stable CIDRs:

- signature validation remains mandatory;
- use a dedicated vendor client certificate if supported;
- consider cloud WAF/provider-specific controls;
- do not add `0.0.0.0/0` silently;
- document the residual risk.

## 7.7 Callback availability

At least two VendorService replicas in production-like mode.

Add:

- readiness probe;
- PodDisruptionBudget;
- topology spread;
- callback queue/retry behavior;
- synthetic callback monitor;
- alert on callback error/latency;
- callback DLQ/reconciliation.

---

## 8. Static outbound IP and proxy design

## 8.1 Baseline flow

```text
VendorService
-> egress-proxy.seev-egress.svc
-> cloud NAT
-> vendor
```

## 8.2 Proxy selection

Recommended initial proxy:

```text
Squid explicit forward proxy
```

Why Squid first:

- simpler destination-domain ACLs;
- simple CONNECT-only policy for HTTPS;
- lower configuration burden;
- easier proof that direct egress is blocked;
- the learning objective is Kubernetes egress control, not proxy internals.

Use Envoy later only when there is a specific objective to learn its dynamic
forward-proxy, RBAC, or richer telemetry model.

The proxy is an enforcement and audit hop. It is not the component that owns
the public static IP.

## 8.3 No TLS interception

Do not perform TLS man-in-the-middle.

The proxy transports TLS using CONNECT.

VendorService must configure its HTTP client to use the proxy explicitly.
Do not rely only on environment variables unless the exact HTTP library behavior
is covered by tests.

The failure policy is fail closed:

```text
proxy unavailable
-> vendor request fails safely
-> no direct internet fallback
```

VendorService remains responsible for:

- TLS verification;
- vendor hostname;
- request signing;
- mTLS client certificate where required;
- timeout;
- retry;
- response validation.

## 8.4 Destination policy

Allow only approved:

```text
vendor hostnames
vendor ports
DNS resolver
certificate/status infrastructure where required
```

Do not allow arbitrary internet destinations.

If hostname-based allowlisting is used, document DNS-rebinding limitations and
prefer stable vendor endpoints.

## 8.5 Kubernetes NetworkPolicy

VendorService egress is allowed only to:

```text
cluster DNS
egress proxy
OpenTelemetry collector
required internal services
```

VendorService cannot directly access:

```text
0.0.0.0/0
vendor public IP
arbitrary external DNS endpoint
```

The proxy may egress to approved vendor destinations.

Other services should not automatically be allowed to use the vendor proxy.

## 8.6 Proxy availability

Production-like mode:

```text
two replicas
PodDisruptionBudget
topology spread
resource limits
readiness probe
connection draining
bounded access logs
```

## 8.7 Proxy logging

Log:

```text
vendor alias
destination host
destination port
method or CONNECT
status class
duration
bytes
request ID
```

Do not log:

```text
authorization
signature
private payload
bank account data
full response body
client certificate key
```

## 8.8 Static source IP

### GCP

```text
private GKE nodes
-> Cloud NAT
-> manually assigned reserved regional external IPv4
```

Use manual NAT IP assignment from the first Cloud NAT creation. Automatically
allocated Cloud NAT addresses can later be removed, and switching from
automatic to manual assignment does not preserve those addresses and disrupts
active NAT connections.

The vendor allowlists the manually reserved NAT IP.

### AWS

```text
private EKS nodes
-> NAT Gateway
-> Elastic IP
```

The vendor allowlists the Elastic IP.

## 8.9 Shared versus dedicated egress IP

### First learning stage

One cluster NAT IP is acceptable.

It proves:

- the source IP is stable;
- the VendorService path is forced through the proxy;
- node and Pod replacement do not change the public egress IP.

It does **not** prove that the IP is unique to VendorService. Other workloads in
the same NAT scope can appear from the same public IP unless their egress is
also constrained.

### Production-like stage

Use a dedicated vendor-egress boundary when the vendor requires an IP unique to
VendorService.

Options:

```text
dedicated egress proxy VM/MIG with static IP
dedicated proxy node/subnet and NAT
cloud-native egress gateway pattern
```

Do not prematurely build a complex dedicated egress topology before the shared
NAT path is working.

---

## 9. Kubernetes namespace model

Recommended:

```text
seev-edge
seev-app
seev-data
seev-egress
seev-observability
seev-security
seev-jobs
```

### `seev-edge`

```text
Traefik
cert-manager route references
edge policies
```

### `seev-app`

```text
Gateway
Auth
Ledger
Payin
Payout
VendorService
Fraud
Assurance
Admin BFF
```

### `seev-data`

Learning mode:

```text
PostgreSQL
Redis
RabbitMQ
```

### `seev-egress`

```text
egress proxy
egress policy
proxy configuration
```

### `seev-observability`

```text
OpenTelemetry Collector
Prometheus
Grafana
Loki
Tempo
Alertmanager
```

### `seev-security`

```text
External Secrets
policy controller
security scanners where used
```

### `seev-jobs`

```text
database migration Jobs
smoke-test Jobs
backup Jobs
reconciliation Jobs
```

---

## 10. Kubernetes workload standards

Every Seev service requires:

```text
Deployment
ClusterIP Service
ServiceAccount
ConfigMap reference
Secret reference
resource request
resource limit
startup probe
readiness probe
liveness policy
security context
PodDisruptionBudget where replicas >= 2
topology spread
NetworkPolicy
metrics annotations/ServiceMonitor
graceful shutdown
```

## 10.1 Security context

Baseline:

```text
runAsNonRoot: true
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true where compatible
capabilities.drop: ALL
seccompProfile: RuntimeDefault
automountServiceAccountToken: false unless required
```

## 10.2 Service account

One service account per service.

Do not reuse `default`.

Use cloud workload identity only for services that need cloud APIs.

## 10.3 Resource policy

Start with measured local baselines.

Do not assign identical resources to every service.

Classify:

```text
public request service
money service
background worker
admin service
observability service
stateful dependency
```

## 10.4 Health probes

### Startup

Proves the process completed startup.

### Readiness

Proves the pod can safely receive traffic.

### Liveness

Use conservatively.

Do not restart a healthy-but-dependency-degraded money service merely because
an external vendor is unavailable.

## 10.5 Graceful shutdown

Requirements:

- stop accepting new requests;
- fail readiness first;
- drain in-flight requests;
- stop queue consumption;
- finish or release leased work;
- close DB/broker clients;
- complete within termination grace.

---

## 11. Stateful dependencies

## 11.1 Local and first cloud sandbox

To minimize cost:

```text
PostgreSQL in cluster
Redis in cluster
RabbitMQ in cluster
```

Use:

- StatefulSet/operator/Helm chart;
- persistent volumes;
- snapshot/backup experiment;
- clear non-production warning.

## 11.2 Production-like GCP stage

Recommended evolution:

```text
PostgreSQL -> Cloud SQL
Redis      -> Memorystore or retained in cluster for learning comparison
RabbitMQ   -> hardened in-cluster RabbitMQ or external managed RabbitMQ
```

To reduce cost, one Cloud SQL instance may host multiple service-owned
databases while preserving:

- separate database names;
- separate users;
- separate grants;
- no cross-service application queries.

This is a cost compromise, not perfect failure isolation.

## 11.3 Production-like AWS stage

```text
PostgreSQL -> Amazon RDS
Redis      -> ElastiCache
RabbitMQ   -> Amazon MQ or hardened in-cluster RabbitMQ
```

## 11.4 Migration jobs

Database migrations run as explicit Kubernetes Jobs or CI-controlled jobs.

They do not run automatically in every application pod.

Requirements:

- one migration identity;
- one execution;
- statement timeout;
- lock timeout;
- migration log;
- preflight;
- post-check;
- failure blocks app rollout where required.

---

## 12. Secrets and configuration

## 12.1 Local

Use:

```text
SOPS-encrypted files
or
local External Secrets provider
```

Do not commit plaintext secrets.

## 12.2 GCP

```text
Secret Manager
Workload Identity
External Secrets Operator
```

## 12.3 AWS

```text
AWS Secrets Manager or Parameter Store
IRSA / EKS Pod Identity
External Secrets Operator
```

## 12.4 Secret categories

```text
database credentials
RabbitMQ credentials
Redis credentials
JWT/signing keys
mTLS material
vendor credentials
vendor proxy credentials if any
webhook secrets
encryption keys
SMTP/push credentials
```

## 12.5 Rotation

The deployment must support:

- multiple active key versions;
- rolling restart or live reload;
- old-key grace period;
- revocation;
- audit;
- no downtime where the application contract permits.

---

## 13. DNS and certificates

## 13.1 DNS

Use:

```text
Cloud DNS on GCP
Route 53 on AWS
external-dns for Kubernetes-managed records
```

Separate zones or hostnames by environment.

## 13.2 Hostname plan

First cloud stage:

```text
api.dev.seev.example
callback.dev.seev.example
```

Later private or identity-aware stage:

```text
admin.dev.seev.example
grafana.dev.seev.example
```

`admin` and `grafana` are not public routes in the initial deployment. Creating
a DNS name does not authorize public exposure; the corresponding load balancer,
routing, authentication, and network controls must exist deliberately.

## 13.3 Certificates

Use cert-manager.

Configure:

- staging issuer;
- production issuer;
- DNS-01 where practical;
- renewal alert;
- certificate-expiry dashboard;
- separate callback certificate if edge is split.

---

## 14. Observability deployment

## 14.1 Metrics

Use:

```text
Prometheus
kube-state-metrics
node exporter
Traefik metrics
application metrics
RabbitMQ metrics
PostgreSQL exporter where appropriate
Redis metrics
egress proxy metrics
```

## 14.2 Logs

Use:

```text
Loki
Promtail or OpenTelemetry Collector
structured application logs
Traefik access logs
proxy access logs
Kubernetes events
```

Retention in learning cloud should be short.

## 14.3 Traces

Use:

```text
OpenTelemetry Collector
Tempo
```

Trace:

```text
public request
internal gRPC
RabbitMQ event
VendorService outbound request
callback
Ledger posting
```

## 14.4 Dashboards

Required:

```text
cluster capacity
pod health/restarts
Traefik traffic
public API latency/errors
callback traffic and rejections
VendorService outbound latency/errors
egress proxy connections
Cloud NAT/NAT Gateway health
RabbitMQ queue depth
database connections/latency
money journey SLO
```

## 14.5 Alerts

Learning alerts:

```text
pod crash loop
deployment unavailable
certificate expiry
Traefik 5xx spike
callback allowlist rejection spike
callback signature rejection spike
egress proxy unavailable
vendor outbound failure spike
queue lag
database connection saturation
persistent disk pressure
NAT/public IP unexpected change
budget threshold
```

---

## 15. CI/CD and GitOps

## 15.1 Build flow

```text
pull request
-> unit/integration/contracts
-> image build
-> image scan
-> push immutable image
-> update deployment image digest
-> deploy to dev
-> smoke/E2E
```

## 15.2 Registry

```text
GCP: Artifact Registry
AWS: ECR
```

## 15.3 GitOps

Recommended:

```text
Argo CD
```

Start only after manual Helm deployment is understood.

Stages:

### Stage 1

```text
helm upgrade --install
```

### Stage 2

```text
GitHub Actions updates environment manifest
Argo CD reconciles cluster
```

### Stage 3

```text
promotion pull request
staging approval
production-like approval
```

## 15.4 Deployment strategy

Initial:

```text
rolling update
```

Later:

```text
canary using separate Deployment/HTTPRoute weights
```

Financial database migrations remain separate from traffic rollout.

---

## 16. Repository structure

Recommended:

```text
deploy/
├── README.md
├── helm/
│   ├── seev/
│   │   ├── Chart.yaml
│   │   ├── values.yaml
│   │   ├── values-local.yaml
│   │   ├── values-gcp-dev.yaml
│   │   ├── values-aws-dev.yaml
│   │   └── templates/
│   └── platform/
├── kubernetes/
│   ├── gateway-api/
│   ├── network-policies/
│   ├── pod-security/
│   ├── smoke-tests/
│   └── examples/
├── platform/
│   ├── traefik/
│   ├── cert-manager/
│   ├── external-secrets/
│   ├── external-dns/
│   ├── argocd/
│   ├── observability/
│   ├── rabbitmq/
│   ├── redis/
│   └── egress-proxy/
├── terraform/
│   ├── modules/
│   │   ├── network/
│   │   ├── kubernetes/
│   │   ├── static-ingress-ip/
│   │   ├── static-egress-ip/
│   │   ├── registry/
│   │   ├── dns/
│   │   ├── secrets/
│   │   └── database/
│   ├── gcp/
│   │   └── dev/
│   └── aws/
│       └── dev/
├── gitops/
│   ├── applications/
│   └── environments/
│       ├── local/
│       ├── gcp-dev/
│       └── aws-dev/
└── scripts/
    ├── bootstrap-local.sh
    ├── bootstrap-gcp.sh
    ├── bootstrap-aws.sh
    ├── verify-ingress.sh
    ├── verify-callback-allowlist.sh
    ├── verify-egress-proxy.sh
    ├── verify-static-ip.sh
    ├── smoke.sh
    ├── chaos.sh
    └── destroy-cloud.sh
```

---

# Execution Roadmap

## K0 — Freeze deployment scope

### Goal

Define exactly what is deployed before writing Kubernetes YAML.

### Work

- inventory all Seev binaries;
- inventory ports and protocols;
- inventory health endpoints;
- inventory gRPC calls;
- inventory RabbitMQ exchanges/queues;
- inventory database ownership;
- inventory Redis use;
- inventory background workers;
- inventory scheduled jobs;
- inventory public routes;
- inventory callback routes;
- inventory vendor outbound destinations;
- inventory required secrets;
- record minimum local resource baseline.

### Deployment scope

First cloud deployment should enable:

```text
Gateway
Auth
Ledger
Payin
Payout
VendorService
Fraud
Assurance
Admin BFF
PostgreSQL
Redis
RabbitMQ
Traefik
egress proxy
basic observability
```

Advanced C1–C6 features remain disabled unless required for a deployment test.

### Deliverables

```text
docs/deployment/service-port-matrix.md
docs/deployment/dependency-matrix.md
docs/deployment/public-route-matrix.md
docs/deployment/vendor-network-matrix.md
docs/evidence/k0-deployment-inventory.md
```

### Acceptance

- [ ] every service port is known;
- [ ] every public route has an owner;
- [ ] callback routes are isolated;
- [ ] every outbound vendor hostname is known;
- [ ] every secret has an owner;
- [ ] resource baseline is recorded;
- [ ] disabled features are explicit.

---

## K1 — Container and runtime hardening

### Goal

Make every image Kubernetes-ready before creating a cluster.

### Work

- use multi-stage builds;
- run as non-root;
- use minimal runtime image;
- add CA certificates and timezone data;
- expose explicit application port;
- add startup/readiness endpoints;
- verify graceful SIGTERM;
- write logs to stdout/stderr;
- make filesystem writes explicit;
- use `/tmp` or mounted writable paths;
- add image labels;
- pin base images;
- add container vulnerability scan;
- build linux/amd64 initially;
- optionally add linux/arm64 after baseline.

### Acceptance

- [ ] every image runs as non-root;
- [ ] every service shuts down gracefully;
- [ ] no required state is stored in container filesystem;
- [ ] health endpoints distinguish startup and readiness;
- [ ] critical/high image findings are understood;
- [ ] images are reproducible.

---

## K2 — Local Kubernetes foundation

### Goal

Run Seev in `kind` with Calico before using cloud credits.

The default local CNI must not be assumed to enforce Kubernetes NetworkPolicy.

### Work

- create a multi-node kind cluster;
- disable or replace the default networking as required by the pinned Calico
  installation guide;
- install Calico;
- run an explicit NetworkPolicy enforcement preflight;
- install Gateway API CRDs;
- install Traefik;
- install cert-manager;
- deploy data dependencies;
- deploy services;
- use local DNS/hosts;
- install default-deny and allowlist NetworkPolicies;
- prove one denied connection actually fails before trusting later tests;
- run migrations as Jobs;
- add smoke tests;
- compare behavior with Docker Compose.

### Local routing

```text
api.local.seev.test
callback.local.seev.test
admin.local.seev.test
```

### Acceptance

- [ ] all services become ready;
- [ ] public API works through Traefik;
- [ ] internal services are not publicly exposed;
- [ ] migrations complete exactly once;
- [ ] RabbitMQ event journeys work;
- [ ] pod restart preserves required state;
- [ ] Docker Compose and Kubernetes business outputs match;
- [ ] the selected CNI demonstrably enforces ingress and egress NetworkPolicy.

---

## K3 — Helm application chart

### Goal

Create one repeatable application deployment interface.

### Work

- create umbrella/application Helm chart;
- define one service template pattern;
- allow per-service overrides;
- define config/secret references;
- define resources;
- define probes;
- define PDB/topology settings;
- define ServiceAccount;
- define NetworkPolicy;
- define migration Jobs;
- define feature flags;
- lint and template-test chart;
- add values schema.

### Avoid

- copying one large Deployment YAML nine times;
- embedding environment secrets;
- cloud-specific annotations in base values;
- enabling every service with identical resources.

### Acceptance

- [ ] chart renders for local/GCP/AWS;
- [ ] cloud-specific values are isolated;
- [ ] schema validation catches bad values;
- [ ] service resources can be tuned independently;
- [ ] no plaintext secret is rendered from Git.

---

## K4 — Traefik and API gateway behavior

### Goal

Establish public, callback, and admin routing.

### Work

- pin Traefik Helm chart/image and Gateway API CRD versions;
- configure the external Traefik Service with a provider overlay;
- set `externalTrafficPolicy: Local` for source-IP-sensitive cloud paths;
- configure GatewayClass;
- create Gateway;
- create HTTPRoutes;
- create GRPCRoutes only if public/internal routing requires them;
- configure TLS;
- configure public middleware;
- configure callback middleware;
- configure admin middleware;
- disable public dashboard;
- add Prometheus metrics;
- add structured access logs;
- test forwarded client IP;
- test body limits;
- test rate limits;
- test path isolation.

### Callback tests

```text
allowed source IP + valid signature      -> accepted
allowed source IP + invalid signature    -> rejected
blocked source IP + valid signature      -> rejected at edge
spoofed X-Forwarded-For                  -> rejected
oversized body                           -> rejected
wrong method/path                        -> rejected
duplicate callback                       -> idempotent behavior
```

### Acceptance

- [ ] only intended routes are external;
- [ ] the Traefik Service preserves client source IP using the selected
      cloud load-balancer path and `externalTrafficPolicy: Local`;
- [ ] source IP is trustworthy;
- [ ] callback allowlist is effective;
- [ ] spoofing test fails safely;
- [ ] Traefik dashboard is private;
- [ ] TLS works;
- [ ] access logs do not contain secrets.

---

## K5 — Kubernetes network isolation

### Goal

Make network access explicit.

### Work

- run a NetworkPolicy-enforcement preflight;
- default-deny ingress per namespace;
- default-deny egress;
- allow Traefik to intended services;
- allow internal service-to-service matrix;
- allow DB access only by owning services;
- allow Redis/RabbitMQ only to required services;
- allow DNS;
- allow telemetry;
- allow VendorService to egress proxy only;
- block direct public egress from VendorService;
- test denied paths.

### Acceptance

- [ ] Gateway cannot query arbitrary service databases;
- [ ] one service cannot connect to another database;
- [ ] VendorService direct vendor connection fails;
- [ ] VendorService proxy path works;
- [ ] callback traffic reaches only VendorService;
- [ ] observability remains functional;
- [ ] policies survive pod restart.

---

## K6 — Egress proxy locally

### Goal

Prove forced proxy usage before cloud NAT.

### Work

- deploy Squid;
- configure explicit proxy;
- configure the VendorService adapter HTTP client explicitly with the proxy;
- test that clearing proxy configuration causes a safe failure rather than a
  direct connection;
- allow only mock-vendor hostname;
- add proxy metrics;
- add structured access logs;
- add emergency deny;
- inject proxy outage;
- inject vendor timeout;
- rotate proxy configuration;
- test TLS verification remains end-to-end.

### Acceptance

- [ ] direct VendorService internet access is blocked;
- [ ] proxy access succeeds;
- [ ] unapproved destination fails;
- [ ] TLS is not intercepted;
- [ ] proxy outage produces safe vendor error/retry;
- [ ] credentials/payload are absent from logs.

---

## K7 — Observability on Kubernetes

### Goal

Make deployment behavior visible before using cloud without forcing the same
resource-heavy stack into the first cloud sandbox.

### Local full-stack work

- install kube-prometheus-stack;
- install Loki;
- install Tempo;
- install OpenTelemetry Collector;
- collect Kubernetes and application metrics;
- collect Traefik/proxy metrics;
- build deployment dashboard;
- build callback dashboard;
- build vendor outbound dashboard;
- add initial alerts;
- create local alert receiver.

### First-cloud lightweight profile

Start with one of:

```text
Prometheus + Grafana + short-retention application logs
```

or:

```text
OpenTelemetry Collector + basic cloud-native monitoring/logging
```

Do not deploy Loki and Tempo to the first single-node cloud sandbox until the
application, database, broker, and monitoring resource budget has been
measured.

### Acceptance

- [ ] one request trace crosses edge and services;
- [ ] callback allowlist rejection is visible;
- [ ] proxy failure is visible;
- [ ] pod crash/restart is visible;
- [ ] queue lag is visible;
- [ ] no high-cardinality user IDs are metric labels.

---

## K8 — Provider-specific Terraform with shared conventions

### Goal

Create reproducible cloud infrastructure without pretending that GCP and AWS
resources share one implementation.

### Work

Use separate provider modules with shared naming, tagging, input, evidence, and
lifecycle conventions.

GCP modules first:

```text
network
subnets
static ingress IP
static egress IP
Kubernetes cluster
registry
DNS
secrets
database
budget alerts
```

AWS modules are implemented later as a separate provider tree.

Kubernetes Helm manifests remain cloud-independent; Terraform does not.

### Acceptance

- [ ] `terraform plan` is reviewable;
- [ ] state is remote and locked;
- [ ] secrets are not in state where avoidable;
- [ ] destroy is tested in sandbox;
- [ ] every resource is labeled/tagged;
- [ ] cost alerts exist before cluster creation.

---

## K9 — GCP sandbox deployment

### Goal

Deploy the cloud-neutral stack on GKE.

### Recommended first configuration

```text
GKE Standard zonal cluster
VPC-native networking
private worker nodes
Dataplane V2
Regular release channel
one on-demand node pool
approximately 4 vCPU / 16 GiB total starting capacity, adjusted from requests
autoscaling minimum 1, maximum 2 nodes
no Spot nodes initially
one manually reserved regional ingress IPv4
one manually reserved regional Cloud NAT IPv4
Artifact Registry
Cloud DNS
one external Traefik LoadBalancer
Admin BFF kept private
PostgreSQL/Redis/RabbitMQ in-cluster for sandbox only
lightweight cloud observability profile
```

Exact machine selection must follow measured pod requests.

### Work

- create GCP project;
- enable billing budget alerts;
- create VPC with primary node range and secondary Pod/Service ranges;
- create Cloud Router;
- reserve the regional static egress IP before creating Cloud NAT;
- create Cloud NAT using manual IP allocation;
- reserve static ingress IP;
- create VPC-native private-node GKE cluster with Dataplane V2 and the
  Regular release channel;
- keep the control-plane access model simple but restricted for the first
  sandbox; harden it after the workload path is proven;
- create registry;
- configure Workload Identity Federation for GKE;
- push images;
- install platform charts;
- deploy Seev;
- configure DNS/TLS;
- verify callback ingress;
- verify vendor sees NAT IP;
- run smoke/business E2E;
- record cost.

### Static egress verification

Use:

- a controlled echo endpoint in the mock vendor;
- provider logs;
- Cloud NAT logs where enabled.

Evidence:

```text
VendorService request source IP
=
reserved Cloud NAT IP
```

### Acceptance

- [ ] nodes have no public IP;
- [ ] public traffic enters only through load balancer;
- [ ] VendorService outbound uses static NAT IP;
- [ ] VendorService cannot bypass proxy;
- [ ] Traefik receives the original callback source IP;
- [ ] spoofed `X-Forwarded-For` does not bypass the allowlist;
- [ ] callback allowlist works with a real internet source;
- [ ] DNS and TLS work;
- [ ] restart and rescheduling work;
- [ ] budget dashboard is visible;
- [ ] environment can be destroyed and recreated.

---

## K10 — Optional split callback ingress

### Goal

Create a dedicated callback edge only after the single-edge deployment works
and either:

- the learning objective explicitly includes multi-edge isolation; or
- a real vendor requires a dedicated callback IP/control plane.

This stage adds another load-balancer cost and is not required for the first
successful cloud proof.

### Work

- create second Traefik deployment;
- create second reserved inbound IP;
- move callback hostname;
- configure `externalTrafficPolicy: Local`;
- configure strict source-IP handling;
- configure cloud load-balancer source ranges/firewall rules;
- retain Traefik IPAllowList as defense in depth;
- add dedicated middleware;
- set two replicas;
- add PDB/topology spread;
- remove callback route from public Traefik;
- run vendor cutover simulation;
- test DNS/IP transition.

### Acceptance

- [ ] public edge cannot reach callback route;
- [ ] callback edge exposes no public user route;
- [ ] callback IP is stable;
- [ ] allowlist and signature both work;
- [ ] rollback to prior callback endpoint is documented;
- [ ] extra cost is recorded.

---

## K11 — Managed database evolution

### Goal

Move state from in-cluster learning dependencies to production-like managed
services.

### Work

GCP:

```text
Cloud SQL PostgreSQL
Secret Manager credentials
private IP
connection pool sizing
backup/PITR
```

Keep service ownership using:

```text
separate database
separate user
separate grants
```

Initially, one instance may host multiple databases for cost control.

Migrate using:

- backup/restore or logical dump for sandbox;
- C6 machinery for a true zero-downtime practice later.

### Acceptance

- [ ] application nodes do not expose DB publicly;
- [ ] every service uses its own credential;
- [ ] cross-service grants fail;
- [ ] backup succeeds;
- [ ] restore succeeds;
- [ ] DB connection saturation is visible;
- [ ] migrations use a separate role.

---

## K12 — CI/CD and GitOps

### Goal

Eliminate local-machine deployment as authority.

### Work

- GitHub Actions builds images;
- push to Artifact Registry;
- scan images;
- update image digest;
- Argo CD deploys dev;
- add environment promotion;
- add migration stage;
- add smoke/E2E gate;
- add rollback;
- protect GitOps branch/path;
- record deployment evidence.

### Acceptance

- [ ] deployment is reproducible from Git;
- [ ] cluster drift is visible;
- [ ] image digest is immutable;
- [ ] failed smoke test blocks promotion;
- [ ] rollback is proven;
- [ ] developer laptop has no production authority.

---

## K13 — Autoscaling and resilience

### Goal

Learn safe Kubernetes scaling without weakening money correctness.

### Work

- measure resources;
- tune requests/limits;
- configure HPA for stateless public services;
- do not horizontally scale singleton workers without lease safety;
- validate RabbitMQ consumers;
- add cluster/node autoscaling;
- add PDB;
- add topology spread;
- test node drain;
- test rollout;
- test proxy replica loss;
- test Traefik replica loss;
- test data dependency loss.

### Acceptance

- [ ] Gateway scales horizontally;
- [ ] duplicate events remain safe;
- [ ] singleton jobs remain singleton;
- [ ] node drain does not break critical journey;
- [ ] callback remains available during one pod loss;
- [ ] proxy remains available during one pod loss;
- [ ] scale-down does not terminate work unsafely.

---

## K14 — Optional AWS portability deployment

### Goal

Prove the Kubernetes application layer is portable after GCP has been
documented and destroyed.

### Entry gate

- GCP final evidence is complete;
- GCP chargeable resources are destroyed;
- the AWS account plan is confirmed to permit EKS and required networking;
- budget alerts exist;
- the expected EKS, NLB, NAT, and compute burn is accepted.

### Work

- create EKS environment through AWS-specific Terraform;
- create private subnets;
- create NAT Gateway and Elastic IP;
- create NLB for Traefik;
- use instance or IP target mode deliberately;
- explicitly configure and test client-IP preservation because AWS defaults
  differ by target type;
- map static EIPs where required;
- use ECR;
- use AWS Secrets Manager;
- configure workload identity;
- deploy same Helm chart;
- apply AWS-only values;
- run same verification suite;
- compare cost and operational differences.

### Acceptance

- [ ] base Helm chart is unchanged except provider-neutral fixes discovered
  by the port;
- [ ] AWS annotations are overlay-only;
- [ ] inbound source IP is correct;
- [ ] outbound vendor IP is Elastic IP;
- [ ] callback allowlist works;
- [ ] EKS control-plane cost is recorded;
- [ ] GCP/AWS differences are documented.

---

## K15 — Security and chaos verification

### Goal

Prove the deployment fails safely.

### Scenarios

```text
spoof X-Forwarded-For
blocked callback IP
invalid callback signature
replayed callback
Traefik pod deletion
callback Traefik pod deletion
egress proxy deletion
Cloud NAT route/config failure
vendor DNS failure
vendor TLS failure
NetworkPolicy removed/changed
service tries another service DB
expired TLS certificate simulation
secret rotation
node drain
cluster restart/upgrade
RabbitMQ outage
PostgreSQL restart
Redis outage
```

### Acceptance

- [ ] no blocked callback reaches application;
- [ ] no invalid callback changes money;
- [ ] no direct vendor egress bypass exists;
- [ ] callback and outbound failures are observable;
- [ ] secret rotation recovers;
- [ ] money idempotency remains correct;
- [ ] runbooks are executable;
- [ ] no failure requires direct balance editing.

---

## K16 — Production-like readiness checkpoint

### Goal

Decide whether the deployment foundation is ready to feed the broader
production-readiness roadmap.

### Required evidence

```text
infrastructure reproducibility
static inbound IP
static outbound IP
callback allowlist
signature validation
forced egress proxy
NetworkPolicy isolation
TLS renewal
secret rotation
managed database backup/restore
GitOps deployment
rollback
observability
node drain
cloud cost
AWS/GCP portability result
```

### Exit decision

At this point:

```text
Deployment platform ready for production-readiness work
```

does not yet mean:

```text
Product ready for real customers or money
```

The next roadmap should then cover:

- legal/product operating model;
- real vendor certification;
- financial settlement;
- SRE/on-call;
- secure software supply chain;
- independent security testing;
- controlled pilot.

---

## 17. Callback route reference design

Conceptual Gateway API and Traefik arrangement:

```text
Gateway:
  public HTTPS listener
  callback HTTPS listener or separate Gateway

HTTPRoute:
  host callback.dev.seev.example
  path /callbacks/vendor-a/*
  backend VendorService

Middleware:
  vendor-a-ip-allowlist
  callback-rate-limit
  callback-inflight-limit
  callback-body-limit
  callback-security-headers
```

The exact manifest must be based on the pinned Traefik chart and current
Gateway API version.

---

## 18. NetworkPolicy reference matrix

| Source | Destination | Allowed purpose |
|---|---|---|
| Traefik Public | Gateway | Public API |
| Traefik Callback | VendorService | Vendor callbacks |
| Authorized admin tunnel or optional Traefik Admin | Admin BFF | Operator UI/API |
| Gateway | Auth/Ledger/Payin/Payout | Public orchestration |
| Payin/Payout | VendorService | Vendor operation |
| VendorService | Egress proxy | External vendor traffic |
| Egress proxy | Vendor public endpoints | Approved external egress |
| Services | RabbitMQ | Events |
| Owning service | Owning database | Persistence |
| Selected services | Redis | Cache/locks |
| All workloads | OTel Collector | Telemetry |
| All workloads | DNS | Resolution |

Everything else:

```text
deny
```

---

## 19. Static-IP test plan

## 19.1 Inbound

- reserve IP;
- bind LoadBalancer;
- verify DNS;
- delete/recreate Traefik pods;
- verify IP unchanged;
- upgrade Traefik;
- verify IP unchanged;
- document load-balancer recreation behavior.

## 19.2 Outbound

- reserve NAT IP;
- call mock vendor `/source-ip`;
- verify expected IP;
- reschedule VendorService;
- verify unchanged;
- reschedule proxy;
- verify unchanged;
- scale nodes;
- verify unchanged;
- rotate proxy config;
- verify unchanged.

## 19.3 Negative

- bypass proxy attempt fails;
- unapproved host fails;
- NAT IP change alert fires;
- callback spoofed source fails.

---

## 20. Detailed milestone order

Recommended sequence:

```text
Milestone 1
K0–K2
Local Kubernetes works

Milestone 2
K3–K6
Reusable Helm, Traefik, callback security, forced egress proxy

Milestone 3
K7–K9
Observability, Terraform, first GCP deployment

Milestone 4
K10–K11
Dedicated callback edge and managed PostgreSQL

Milestone 5
K12–K13
GitOps, autoscaling, resilience

Milestone 6
K14
Optional AWS portability after GCP teardown

Milestone 7
K15–K16
Chaos evidence and deployment-foundation acceptance
```

---

## 21. Recommended pull-request sequence

```text
PR 1  — Deployment inventory and container-readiness fixes
PR 2  — Local kind + Calico cluster and NetworkPolicy preflight
PR 3  — Seev Helm chart and per-service templates
PR 4  — Traefik Gateway API public routes
PR 5  — Vendor callback allowlist and trusted source-IP tests
PR 6  — Default-deny NetworkPolicies
PR 7  — Squid egress proxy and VendorService forced proxy configuration
PR 8  — Kubernetes observability stack and dashboards
PR 9  — Terraform GCP network, GKE, registry, DNS, and budgets
PR 10 — GCP deployment, static ingress, and Cloud NAT verification
PR 11 — Dedicated callback Traefik and static IP
PR 12 — Secret Manager and External Secrets
PR 13 — Managed PostgreSQL migration for sandbox
PR 14 — GitHub Actions and Argo CD
PR 15 — HPA, PDB, topology spread, and node-drain tests
PR 16 — Optional Terraform AWS EKS/NLB/NAT/ECR provider implementation
PR 17 — Optional AWS deployment verification after GCP teardown
PR 18 — Chaos, runbooks, cost evidence, and final acceptance
```

Do not create one giant “Kubernetes deployment” PR.

---

## 22. Skills gained per milestone

### Milestone 1

- Docker image hardening;
- Kubernetes primitives;
- services and probes;
- StatefulSet basics.

### Milestone 2

- Traefik;
- Gateway API;
- TLS;
- source IP;
- callback security;
- NetworkPolicy;
- forward proxy.

### Milestone 3

- Prometheus/Grafana/Loki/Tempo;
- Terraform;
- GCP VPC;
- GKE;
- Cloud NAT;
- Artifact Registry;
- cost controls.

### Milestone 4

- edge isolation;
- managed PostgreSQL;
- private connectivity;
- backup/restore.

### Milestone 5

- GitOps;
- release promotion;
- autoscaling;
- disruption handling.

### Milestone 6

- EKS;
- NLB;
- Elastic IP;
- NAT Gateway;
- cloud portability.

### Milestone 7

- incident thinking;
- chaos testing;
- security verification;
- operational evidence.

---

## 23. Final definition of done

This Kubernetes deployment roadmap is complete only when:

### Cluster and deployment

- [ ] all required services deploy through one reusable application chart;
- [ ] local NetworkPolicy enforcement has been proven with kind + Calico;
- [ ] local, GCP, and AWS values are separated;
- [ ] images run non-root;
- [ ] probes and graceful shutdown work;
- [ ] migrations run explicitly;
- [ ] cluster state can be recreated.

### Ingress

- [ ] Traefik uses pinned versions;
- [ ] HTTPS is mandatory;
- [ ] the Traefik Service preserves client source IP using the selected
      cloud load-balancer path and `externalTrafficPolicy: Local`;
- [ ] source IP is trustworthy;
- [ ] callback route is isolated;
- [ ] callback vendor CIDRs are allowlisted;
- [ ] spoofed forwarded headers fail;
- [ ] callback signature/idempotency remain mandatory;
- [ ] static inbound IP is proven.

### Egress

- [ ] VendorService cannot connect directly to internet;
- [ ] VendorService HTTP client is explicitly configured for the egress
      proxy;
- [ ] VendorService uses the egress proxy;
- [ ] proxy destination policy exists;
- [ ] proxy does not intercept TLS;
- [ ] static NAT source IP is proven;
- [ ] vendor outbound IP remains stable after pod/node changes;
- [ ] proxy outage is safe and observable.

### Security

- [ ] namespaces use default-deny policy;
- [ ] database access follows ownership;
- [ ] workloads use distinct service accounts;
- [ ] secrets come from managed or encrypted sources;
- [ ] Traefik dashboard is not public;
- [ ] no plaintext secret is committed;
- [ ] pod security baseline is enforced.

### Reliability

- [ ] pod restart is safe;
- [ ] node drain is safe;
- [ ] Traefik and proxy replica loss are safe;
- [ ] RabbitMQ/DB restart recovery is understood;
- [ ] duplicate callback/event behavior remains idempotent;
- [ ] PDB and topology policies are tested.

### Observability

- [ ] metrics, logs, and traces are visible;
- [ ] callback rejection is visible;
- [ ] vendor egress is visible;
- [ ] queue and database health are visible;
- [ ] cost/budget alerts exist;
- [ ] alerts link to runbooks.

### Delivery

- [ ] images are built in CI;
- [ ] immutable digest is deployed;
- [ ] GitOps reconciliation works;
- [ ] failed E2E blocks promotion;
- [ ] rollback is proven;
- [ ] local laptop is not deployment authority.

### Cloud

- [ ] GCP deployment is reproducible;
- [ ] Cloud NAT was created with manual reserved-IP assignment from day one;
- [ ] GCP credit consumption is measured;
- [ ] environment can be destroyed;
- [ ] AWS portability is either implemented after the entry gate or
      explicitly deferred with rationale;
- [ ] when implemented, AWS static ingress/egress is verified;
- [ ] base application chart is cloud-neutral;
- [ ] provider-specific behavior is isolated.

---

## 24. Final evidence log

| Evidence | Commit / artifact | Result | Notes |
|---|---|---:|---|
| Deployment inventory |  |  |  |
| Container non-root |  |  |  |
| Local Kubernetes E2E |  |  |  |
| Helm render/lint |  |  |  |
| Public Traefik route |  |  |  |
| Callback allowlist |  |  |  |
| Spoofed X-Forwarded-For |  |  |  |
| Callback signature |  |  |  |
| Default-deny policy |  |  |  |
| Direct vendor egress blocked |  |  |  |
| Egress proxy success |  |  |  |
| Proxy destination block |  |  |  |
| Local observability |  |  |  |
| Terraform GCP plan |  |  |  |
| GKE deployment |  |  |  |
| GCP static inbound IP |  |  |  |
| GCP static outbound IP |  |  |  |
| Callback edge split |  |  |  |
| Secret Manager integration |  |  |  |
| Managed PostgreSQL restore |  |  |  |
| Argo CD deployment |  |  |  |
| Rollback |  |  |  |
| Node-drain test |  |  |  |
| HPA test |  |  |  |
| Terraform AWS plan |  |  |  |
| EKS deployment |  |  |  |
| AWS NLB source IP |  |  |  |
| AWS NAT Elastic IP |  |  |  |
| Chaos matrix |  |  |  |
| Cost report |  |  |  |
| Final clean-tree gate |  |  |  |

---

## 25. Immediate next action

Do not create the GKE cluster first.

Start with:

```text
K0 deployment inventory
-> K1 container readiness
-> K2 local kind + Calico cluster
-> K3 reusable Helm chart
-> K4 Traefik routing with source-IP preservation design
-> K5 callback allowlist and default-deny policies
-> K6 Squid egress proxy
```

Only after the local tests prove:

```text
callback source IP filtering works
VendorService cannot bypass proxy
all services have resource/probe definitions
business E2E passes through Traefik
```

create the GCP project and start consuming the free credit.

The first cloud objective is narrowly defined:

```text
Deploy Seev on private GKE
with one static inbound IP,
one static Cloud NAT outbound IP,
Traefik public/callback routing,
and forced VendorService egress through a proxy.
```

Everything else is an incremental milestone after that proof.

---

## 26. Review source notes

The revised recommendations were checked against current official documentation
available on 2026-07-29:

- Google Cloud Free Program and GKE pricing;
- GKE Dataplane V2 and NetworkPolicy behavior;
- Kubernetes client source-IP and `externalTrafficPolicy` behavior;
- GKE Service LoadBalancer source-IP guidance;
- Cloud NAT manual IP assignment behavior;
- Traefik 3.x Gateway API and IPAllowList documentation;
- AWS Free Tier plan limitations;
- Amazon EKS pricing;
- AWS Load Balancer Controller client-IP and NLB target-type behavior;
- AWS NAT Gateway pricing.

Revalidate cloud offers, supported versions, annotations, and provider behavior
again at implementation time.
