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

Canonical deployment documents:

- [runtime contract](runtime-contract.md)
- [environment contract](environment-contract.md)
- [K1–K6 input contract](k1-input-contract.md)
- [vendor sandbox contract](vendor-sandbox.md)
- [machine-readable deployment inventory](../../deploy/inventory/)

## Plan 64 K0 deliverables

The generated [runtime contract](runtime-contract.md) consolidates the K0
service, port, dependency, route, storage, configuration, secret, health,
resource, and vendor-egress views. The [K1–K6 input contract](k1-input-contract.md)
maps downstream tracks to their machine-readable inputs.

Machine-readable contracts live in [deploy/inventory](../../deploy/inventory/).
Reproduce the redacted inventory and validator with:

~~~sh
make k0-inventory
make k0-inventory-check
~~~

K0 itself is the inventory contract. Its local acceptance paths have since
been exercised on disposable Compose/kind environments with synthetic data,
as indexed by the capability inventory. No cloud/staging apply or provider
certification evidence is claimed; those promotion gates remain pending.
