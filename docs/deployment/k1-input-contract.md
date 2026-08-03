# K1–K6 input contract

This document is the stable hand-off from K0. Downstream plans consume the
machine-readable inventories and evidence hashes instead of rediscovering
baseline facts.

Entry is conditional: [K0 final acceptance](../evidence/k0/final-acceptance.md)
must report a passing static gate. The current worktree is held at K0 because
`cmd/gateway/main.go` references notification channel packages that are not
present; see K0-F-011 in the [risk register](deployment-risk-register.md).

| Track | Required input | K0 source |
|---|---|---|
| K1 container readiness | images, runtime user, writable paths, health/lifecycle, CA/timezone, migration decision | [image-runtime-matrix.md](image-runtime-matrix.md), [health-lifecycle-matrix.md](health-lifecycle-matrix.md), [services.yaml](../../deploy/inventory/services.yaml) |
| K2 kind and data dependencies | services, ports, dependencies, stores, volumes, synthetic scope, resource limits | [ports.yaml](../../deploy/inventory/ports.yaml), [dependencies.yaml](../../deploy/inventory/dependencies.yaml), [data-stores.yaml](../../deploy/inventory/data-stores.yaml) |
| K3 Helm | canonical names, ports, config/secret names, mounts, workers, probes, resources, migration Job | [services.yaml](../../deploy/inventory/services.yaml), [configuration.yaml](../../deploy/inventory/configuration.yaml), [secrets.yaml](../../deploy/inventory/secrets.yaml), [jobs.yaml](../../deploy/inventory/jobs.yaml) |
| K4 Traefik | public/callback/private routes, TLS hosts, source-IP, body/rate policy | [routes.yaml](../../deploy/inventory/routes.yaml), [feature-scope.md](feature-scope.md) |
| K5 NetworkPolicy | call matrix, database ownership, Redis/RabbitMQ, DNS, telemetry, callback/admin ingress | [dependencies.yaml](../../deploy/inventory/dependencies.yaml), [mtls-identity-matrix.md](mtls-identity-matrix.md) |
| K6 Squid | VendorService selector, proxy port, provider host/port contract, CONNECT, no-direct-fallback proof | [vendor-network.yaml](../../deploy/inventory/vendor-network.yaml), [deployment-risk-register.md](deployment-risk-register.md) |

Evidence reproduction:

~~~sh
make k0-inventory
make k0-inventory-check
~~~

Inventory hashes are written to
[inventory-sha256.txt](../evidence/k0/generated/inventory-sha256.txt).
Runtime evidence must remain redacted and synthetic-only.
