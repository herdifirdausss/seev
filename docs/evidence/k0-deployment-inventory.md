# K0 deployment inventory evidence

**Scope:** repository inspection for the Plan 63 Kubernetes sandbox.

This evidence records the implementation baseline without copying secret
values. It is intentionally marked as a learning deployment, not a production
readiness assertion.

## Verified baseline

- Nine core deployable Go services are built by the root `Dockerfile`; the
  optional `mock-push-provider` support binary is built for the notifications
  profile but is not counted as a business service.
- The runtime image is `gcr.io/distroless/static-debian12:nonroot`, with
  `CGO_ENABLED=0`, `/app/service` as entrypoint, and migrations copied into the
  image.
- Application listeners, ownership, and exposure decisions are in
  [service-port-matrix](../deployment/service-port-matrix.md).
- Public, callback, and private admin routes are in
  [public-route-matrix](../deployment/public-route-matrix.md).
- PostgreSQL, Redis, RabbitMQ, and vendor boundary relationships are in
  [dependency-matrix](../deployment/dependency-matrix.md).
- Vendor callback source policy is implemented in
  `services/vendor-service/internal/callback.go`: the connection peer is authoritative
  unless the immediate peer is explicitly trusted for forwarded headers.
- Callback signature verification and durable inbox/idempotency remain inside
  VendorService; Traefik is not the business-authentication owner.
- Local Compose certificates and generated key material are ignored by Git and
  are never included in this evidence.

## Deliberate first-stage scope

Enabled: Gateway, Auth, Ledger, Payin, Payout, Fraud, VendorService, Assurance,
Admin BFF (private), PostgreSQL, Redis, RabbitMQ, Traefik, Squid, and a small
metrics/logging profile.

Deferred: managed PostgreSQL, split callback edge, Argo CD, HPA/node autoscaler,
Loki/Tempo in the first cloud sandbox, real vendor certification, and AWS
resource creation. These are represented by provider overlays and follow-on
verification scripts, but must not be mistaken for completed external proof.

## Blocking unknowns

The following cannot be guessed in manifests:

- real vendor callback CIDRs;
- real vendor outbound hostnames and TLS identities;
- cloud project/account identifiers and approved operator CIDRs;
- final image registry and digest values;
- production secret-manager object names;
- measured resource baseline under the selected business workload.

## Reproduction commands

```sh
make certs
make cryptox-secret
make observability-secret
deploy/kubernetes/scripts/bootstrap-local.sh
deploy/kubernetes/scripts/smoke.sh
```

The commands above require the local Docker daemon and the pinned tools listed
in `deploy/kubernetes/README.md`. `bootstrap-local.sh` refuses to create cloud
resources.
