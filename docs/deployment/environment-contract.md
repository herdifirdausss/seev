# Environment contract

This contract is the deployment boundary for Seev. Local Compose may use
loopback addresses, disabled TLS, and mock adapters. `staging` and
`production` must use the same typed configuration surface with real private
endpoints and managed secret references.

## Environment identity

Every workload sets `APP_ENV` to exactly one of `development`, `staging`, or
`production`. The application configuration validator is authoritative for
the allowed values. The deployment gate additionally rejects unsafe values
before a release is promoted.

| Environment | Localhost/mock allowed | Postgres TLS | Vendor mode | Migration policy |
|---|---|---|---|---|
| `development` | yes | optional | mock only | Compose/bootstrap allowed |
| `staging` | no | `require` or `verify-full` | certified sandbox only | dedicated migration Job |
| `production` | no | `require` or `verify-full` | certified production provider | dedicated migration Job |

## Required runtime contract

All application workloads require the existing service-specific database,
`JWT_SECRET`, `JWT_ISSUER`, `INTERNAL_GRPC_TOKEN`, and current cryptographic
key material. A deployment must also provide:

- `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_PASSWORD`,
  `POSTGRES_DB`, and `POSTGRES_SSL_MODE`;
- `REDIS_ENABLED=true`, a private `REDIS_ADDR`, and Redis authentication when
  the workload uses distributed rate limits or locks;
- `RABBITMQ_HOST`, credentials, exchange, and TLS settings when the workload
  publishes or consumes events;
- `TLS_CERT_DIR` or an equivalent mounted identity bundle for internal mTLS;
- `VENDOR_EGRESS_PROXY_REQUIRED=true` and a private
  `VENDOR_EGRESS_PROXY_URL` for any real vendor route;
- `KYC_PROVIDER_URL` and `KYC_PROVIDER_TOKEN` for a staging/production KYC
  provider integration;
- secret-manager references for secret values. Plaintext secrets must not be
  stored in Terraform variables files, Helm values, logs, or Git.

The exact per-service names remain in the generated inventory at
[`deploy/inventory/configuration.yaml`](../../deploy/inventory/configuration.yaml)
and [`deploy/inventory/secrets.yaml`](../../deploy/inventory/secrets.yaml).

## Startup and rollout gates

1. Render the environment from the provider secret manager and ConfigMaps.
2. Run `scripts/ci/check-environment-contract.sh` against the rendered
   environment (the script never prints secret values).
3. Apply the versioned migration image as a separate, one-shot Job. The
   application image must not own schema migration.
4. Wait for database, broker, Redis, mTLS, and vendor-egress readiness.
5. Start at least two stateless replicas for each public workload, with
   readiness and liveness probes and a PodDisruptionBudget where supported.
6. Execute the golden-route acceptance suite and confirm dashboards, alerts,
   backup freshness, and rollback metadata before enabling money movement.

`deploy/kubernetes/migrations.Dockerfile` and the Helm migration Job are the
repository implementation. Cloud application, identity, and rollback evidence
must be retained per release; a local Compose run is not production evidence.
