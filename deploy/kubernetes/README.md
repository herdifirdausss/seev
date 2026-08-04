# Seev Kubernetes deployment

This is the executable K2–K9 learning path for Plan 63. The application chart
is in [`../helm/seev`](../helm/seev); platform edge resources are in
[`platform/traefik`](platform/traefik).

## Local path

Prerequisites: Docker, kind, kubectl, Helm 3, curl, OpenSSL, and enough local
resources for nine services plus PostgreSQL, Redis, RabbitMQ, Traefik, and
Squid.

```sh
make certs cryptox-secret
deploy/kubernetes/scripts/bootstrap-local.sh
deploy/kubernetes/scripts/smoke.sh
deploy/kubernetes/scripts/verify-callback-allowlist.sh
deploy/kubernetes/scripts/verify-egress-proxy.sh
```

The bootstrap creates a three-node kind cluster with the default CNI disabled,
installs Calico, and runs a preflight that proves a denied connection really
fails. It then installs pinned Gateway API/Traefik CRDs, creates only local
learning secrets, loads locally built images, and deploys the chart.

The local edge is accessed through a port-forward. It does not claim to prove a
cloud load balancer's source-IP behavior; `externalTrafficPolicy: Local` and a
cloud static-IP check are explicit acceptance items for K9.

## Cloud path

Use a provider-specific Terraform tree first, then the same chart with a
provider overlay:

```sh
terraform -chdir=../terraform/gcp/dev init
terraform -chdir=../terraform/gcp/dev plan
helm upgrade --install seev ../helm/seev -n seev-app \
  -f ../helm/seev/values.yaml -f ../helm/seev/values-gcp-dev.yaml
deploy/kubernetes/scripts/verify-static-ip.sh
```

Replace every `REPLACE_WITH_*` value, create managed secrets before the Helm
release, and pass immutable image digests. The Terraform is intentionally
reviewable and cost-guarded; it does not contain credentials or automatically
create a cloud project/account.

## Security boundaries

- Traefik is the only public Kubernetes Service.
- `externalTrafficPolicy: Local` preserves the direct L4 peer address.
- forwarded headers and PROXY protocol are not trusted unless a provider
  overlay explicitly enables a known upstream path.
- callback source CIDR filtering is defense in depth; VendorService still
  verifies signatures, timestamps, and idempotency. The callback route uses a
  Traefik `IngressRoute` with verified upstream mTLS because VendorService's
  HTTP listener requires a client certificate.
- VendorService has no external egress policy except DNS, internal Seev
  dependencies, and Squid on TCP 3128.
- Squid permits only configured vendor domains and does not intercept TLS.
- Admin BFF remains ClusterIP/private in the first cloud stage.
- Secrets are pre-created, never rendered by the chart, and never committed.

## Known learning-stage limitations

The current repository's mock vendor is in-process, so no real outbound HTTP
adapter exists to exercise through Squid yet. The `internal/platform/resilience/egressproxy` seam and
the fail-closed VendorService configuration are ready for the first real
adapter. Managed PostgreSQL, Cloud NAT source-IP evidence, GitOps, and AWS are
external follow-on stages and are not simulated by local manifests.
