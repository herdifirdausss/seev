# Seev platform infrastructure

The provider-specific `dev` directories contain private network and Kubernetes
foundations. The adjacent `platform` modules add managed stateful services:

- AWS: RDS PostgreSQL, ElastiCache Redis, Amazon MQ RabbitMQ, S3, KMS,
  Secrets Manager, and CloudWatch logs;
- GCP: HA Cloud SQL PostgreSQL, Memorystore Redis, Pub/Sub, Cloud Storage, KMS,
  Secret Manager, and required APIs.

Both modules default to `enabled = false` because applying them creates paid
resources. They require explicit credentials when enabled and deliberately do
not write secret values into Terraform state beyond what the provider itself
requires. Use a protected CI state backend and a secret-generation workflow.

The platform contract is:

1. apply network and identity foundation;
2. apply exactly one provider platform module;
3. grant workload identity access only to the required secret objects/buckets;
4. install ExternalSecrets and network policies;
5. run the versioned migration image as a separate job;
6. verify readiness, SLO dashboards, backups, and rollback before enabling
   application replicas.

The production-shaped Kubernetes overlay is
`deploy/helm/seev/values-staging.yaml`. It disables the in-cluster data
StatefulSets, requires private managed endpoints and TLS, uses at least two
stateless replicas, and rejects the mock vendor. The `*-dev.yaml` overlays are
learning profiles and are not staging evidence.
