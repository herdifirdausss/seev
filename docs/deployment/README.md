# Seev deployment contract

This directory is the K0 contract consumed by the Kubernetes, Terraform, and
verification assets under [`deploy/kubernetes`](../../deploy/kubernetes/).
The matrices describe the current repository, not a production promise.

The source-of-truth order is executable behavior, application code,
`Dockerfile`/Compose, API contracts, configuration validation, reference docs,
then archived roadmap prose. `UNKNOWN` values are intentional blockers and
must not be silently filled in by a manifest.

Before a cloud deployment, generate local certificates and runtime secrets with
the existing Make targets, create Kubernetes Secrets from those files, and
replace every learning-only value in `deploy/helm/seev/values-*.yaml`.

Related evidence:

- [K0 inventory evidence](../evidence/k0-deployment-inventory.md)
- [K0 final acceptance](../evidence/k0/final-acceptance.md)
- [K0 baseline](../evidence/k0/baseline.md)
- [service and port matrix](service-port-matrix.md)
- [dependency matrix](dependency-matrix.md)
- [public route matrix](public-route-matrix.md)
- [vendor network matrix](vendor-network-matrix.md)

## Plan 64 K0 deliverables

The complete K0 set is split by concern:

- [service runtime inventory](service-runtime-inventory.md), [port/protocol matrix](port-protocol-matrix.md), and [health/lifecycle matrix](health-lifecycle-matrix.md);
- [public routes](public-route-matrix.md), [internal calls](internal-call-matrix.md), and [mTLS identities](mtls-identity-matrix.md);
- [data ownership](data-ownership-matrix.md), [messaging](messaging-matrix.md), [storage volumes](storage-volume-matrix.md), and [background jobs](background-job-matrix.md);
- [configuration](configuration-matrix.md), [secret/key handling](secret-key-matrix.md), and [vendor network](vendor-network-matrix.md);
- [image runtime](image-runtime-matrix.md), [resource baseline](resource-baseline.md), [feature scope](feature-scope.md), [risk register](deployment-risk-register.md), and the [K1–K6 input contract](k1-input-contract.md).

Machine-readable contracts live in [deploy/inventory](../../deploy/inventory/).
Reproduce the redacted inventory and validator with:

~~~sh
make k0-inventory
make k0-inventory-check
~~~

K0 is documentation, inventory, evidence, and validation only. It does not
create cloud resources or mutate Kubernetes. Local runtime checks must use
synthetic data and isolated disposable Compose projects.
