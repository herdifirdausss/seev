# K1–K6 input contract

This document is the stable hand-off from K0. Downstream plans consume the
machine-readable inventories and evidence hashes instead of rediscovering
baseline facts.

Entry is conditional on a passing static gate and reviewed runtime boundary.
The repository records passing disposable local Kubernetes journeys and the
current static/inventory gates. Cloud/staging apply evidence, resolved
environment values, and human promotion sign-off remain pending; local
acceptance must not be presented as cloud readiness.

| Track | Required input | K0 source |
|---|---|---|
| K1 container readiness | images, runtime user, writable paths, health/lifecycle, CA/timezone, migration decision | [runtime contract](runtime-contract.md), [services.yaml](../../deploy/inventory/services.yaml) |
| K2 kind and data dependencies | services, ports, dependencies, stores, volumes, synthetic scope, resource limits | [ports.yaml](../../deploy/inventory/ports.yaml), [dependencies.yaml](../../deploy/inventory/dependencies.yaml), [data-stores.yaml](../../deploy/inventory/data-stores.yaml) |
| K3 Helm | canonical names, ports, config/secret names, mounts, workers, probes, resources, migration Job | [services.yaml](../../deploy/inventory/services.yaml), [configuration.yaml](../../deploy/inventory/configuration.yaml), [secrets.yaml](../../deploy/inventory/secrets.yaml), [jobs.yaml](../../deploy/inventory/jobs.yaml) |
| K4 Traefik | public/callback/private routes, TLS hosts, source-IP, body/rate policy | [routes.yaml](../../deploy/inventory/routes.yaml), [first-deployment-scope.yaml](../../deploy/inventory/first-deployment-scope.yaml) |
| K5 NetworkPolicy | call matrix, database ownership, Redis/RabbitMQ, DNS, telemetry, callback/admin ingress | [dependencies.yaml](../../deploy/inventory/dependencies.yaml), [runtime contract](runtime-contract.md) |
| K6 Squid | VendorService selector, proxy port, provider host/port contract, CONNECT, no-direct-fallback proof | [vendor-network.yaml](../../deploy/inventory/vendor-network.yaml), [vendor sandbox contract](vendor-sandbox.md) |

Evidence reproduction:

~~~sh
make k0-inventory
make k0-inventory-check
~~~

Runtime evidence must remain redacted and synthetic-only. Git records the
reviewed inventory version; regenerated local hashes are disposable evidence.
