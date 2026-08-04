# AWS production platform module

This module is the managed-data-service layer that sits beside the existing
private EKS foundation in `../dev`. It provisions private, encrypted,
multi-AZ Postgres, Redis, RabbitMQ, object storage, KMS, secrets, and logs.

It is opt-in because applying it creates billable infrastructure. The module
fails validation unless credentials are explicitly supplied when enabled.
Credentials must come from a secret-generation workflow; do not commit a
`tfvars` file.

The EKS foundation's workload-identity role should be granted read access to
the generated secret ARNs, and the Kubernetes ExternalSecret examples under
`deploy/kubernetes/platform/secrets` should populate runtime configuration.
Run migrations as the separate `deploy/kubernetes/migrations.Dockerfile` job
before application rollout.
