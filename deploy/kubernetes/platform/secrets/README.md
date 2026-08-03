# Managed secret handoff

The chart intentionally references pre-created Kubernetes Secrets and never
renders secret values. In the local stage, `create-local-secrets.sh` creates
ephemeral learning Secrets from ignored files. In cloud, install and pin an
External Secrets operator, then apply the provider-specific `ExternalSecret`
objects for the names created by Terraform.

Required Kubernetes Secret contracts:

- `seev-app/seev-runtime-secrets`: `jwt-secret`, `internal-grpc-token`,
  `vendor-mockvendor-secret`, `vendor-mockvendor2-secret`;
- `seev-app` and `seev-data/seev-data-secrets`: `postgres-super-password`,
  `rabbitmq-password`, and one `<service>-postgres-password` per enabled
  service;
- `seev-app/seev-crypto-secrets`: versioned cryptox/idempotency key files;
- `seev-app/seev-mtls`: CA plus only the leaf identities required by the
  workload set;
- `seev-edge/seev-edge-tls`: certificate for the public and callback names;
- `seev-edge/seev-edge-backend-ca`: CA certificate under `ca.crt` for
  Traefik's verified connection to VendorService;
- `seev-edge/seev-edge-backend-client`: `tls.crt`/`tls.key` client identity
  accepted by VendorService (the local profile uses `dev-operator`);
- `seev-observability/seev-prometheus-mtls`: Prometheus scrape identity.

The first cloud stage may use one in-cluster PostgreSQL instance with separate
database users. Managed PostgreSQL and cloud secret rotation are K11 work and
must be tested with backup/restore before real data is considered.
