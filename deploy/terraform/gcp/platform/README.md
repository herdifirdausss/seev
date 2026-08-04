# GCP production platform module

This module is the managed-data-service layer beside the existing private GKE
foundation in `../dev`. It provisions private HA Cloud SQL PostgreSQL,
Memorystore Redis, Pub/Sub, encrypted/versioned Cloud Storage, KMS, Secret
Manager, and the supporting APIs.

It is opt-in and requires an externally generated database credential. Apply
only from a protected CI identity; populate ExternalSecrets from the generated
Secret Manager IDs and run the separate Kubernetes migration job before
application rollout.
