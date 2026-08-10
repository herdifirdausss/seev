# How to Add a Service

> **DEPLOY-010 contract.** Every fact about a service lives in exactly one authoritative
> source. Adding a service means updating ALL of the layers below — missing any one of
> them will fail the CI drift check (`make deployment-drift-check`).

## Authoritative ownership per fact

| Fact | Authoritative source | Derived consumers |
|---|---|---|
| Service identity / dependencies / ports | `deploy/inventory/` | Helm, Prometheus, GitOps |
| Kubernetes rendering | `deploy/helm/seev/values.yaml` | Rendered manifests, network policies |
| Network access contract | `values.yaml` → `services.<name>.network` | `networkpolicy.yaml` template |
| TLS cert identity | `Makefile` `CERT_IDENTITIES` + `tools/certgen/main.go` | `deploy/certs/` |
| Metrics collection | `deploy/observability/prometheus/prometheus.yml` | Grafana dashboards |
| Environment endpoints | Terraform outputs / secret manager | `values-staging.yaml` |
| Promotion revision | GitOps `targetRevision` | Argo CD |

---

## Checklist

Work through this list top-to-bottom. Each step names the file to change and
the CI check that validates it.

### Step 1 — Inventory (authoritative source)

- [ ] Add a service entry to [`deploy/inventory/services.yaml`](../../deploy/inventory/services.yaml):

  ```yaml
  - name: my-service
    current:
      command: ./services/my-service/cmd/my-service
      compose_service: my-service
      app_name: my-service-service
    roles:
      request_server: false
      grpc_server: true
  ```

- [ ] Add dependency edges to [`deploy/inventory/dependencies.yaml`](../../deploy/inventory/dependencies.yaml).
- [ ] Add port declarations to [`deploy/inventory/ports.yaml`](../../deploy/inventory/ports.yaml).
- [ ] If the service connects to a datastore, add entries to [`deploy/inventory/data-stores.yaml`](../../deploy/inventory/data-stores.yaml).

**CI check:** `make deployment-drift-check` — validates Inventory ↔ Helm coverage.

---

### Step 2 — Helm values (Kubernetes rendering)

Add the service block to [`deploy/helm/seev/values.yaml`](../../deploy/helm/seev/values.yaml) under `services:`:

```yaml
services:
  my-service:
    enabled: true
    image: my-service
    identity: my-service
    replicas: 1
    ports:
      - {name: http, port: 80XX, targetPort: 80XX}
    env:
      APP_PORT: "80XX"
    resources:
      requests: {cpu: 100m, memory: 128Mi}
      limits:   {cpu: 500m, memory: 512Mi}
    # DEPLOY-001: network contract — every field is required.
    network:
      ingressFrom: []          # list service names that may call this service
      egressToServices: []     # list service names this service may call
      datastores:
        postgres: false        # true only if this service owns a postgres database
        redis: false
        rabbitmq: false
```

**Rules:**
- `network.ingressFrom` / `egressToServices` must match the dependency edges in `dependencies.yaml`.
- `network.datastores` must match `data-stores.yaml`.
- `replicas: 2` minimum is required for staging.

**CI check:** `make network-contract-check` — validates the network section is present and consistent.

---

### Step 3 — Helm staging overlay

Add the service to [`deploy/helm/seev/values-staging.yaml`](../../deploy/helm/seev/values-staging.yaml):

```yaml
services:
  my-service:
    replicas: 2
```

**CI check:** `make deploy-readiness ENV=staging` — requires ≥ 2 replicas for all enabled services.

---

### Step 4 — TLS certificate identity

Two edits are needed:

1. **`Makefile`** — add the service name to `CERT_IDENTITIES`:

   ```makefile
   CERT_IDENTITIES := gateway auth ... my-service
   ```

2. **[`tools/certgen/main.go`](../../tools/certgen/main.go)** — add DNS SAN entries to `serviceDNSNames`:

   ```go
   var serviceDNSNames = map[string][]string{
       "my-service": {"my-service-service", "my-service-service.seev-app.svc.cluster.local"},
       // ...
   }
   ```

Then regenerate certs:

```bash
make certs
```

**CI check:** `make tls-contract-check` — verifies DNS SANs are present and per-service isolation holds.

---

### Step 5 — Prometheus scrape job

Add a scrape job to [`deploy/observability/prometheus/prometheus.yml`](../../deploy/observability/prometheus/prometheus.yml):

```yaml
- job_name: my-service-service
  scheme: https
  tls_config:
    ca_file: /certs/ca.pem
    cert_file: /certs/prometheus.pem
    key_file: /certs/prometheus-key.pem
    server_name: my-service-service
  metrics_path: /metrics
  static_configs:
    - targets: ["my-service-service:80XX"]
```

**CI check:** `make deployment-drift-check` — validates every Helm service has a Prometheus scrape job.

---

### Step 6 — Rendered manifest policy

The OPA policy in [`deploy/policy/seev.rego`](../../deploy/policy/seev.rego) validates every rendered Deployment for:
- `allowPrivilegeEscalation: false`
- `readOnlyRootFilesystem: true`
- `runAsNonRoot: true`
- ALL capabilities dropped
- resource requests + limits
- readiness + liveness probes
- `automountServiceAccountToken: false`
- explicit rollout `strategy.type`
- no placeholder env values

No changes to the policy file are needed unless the service has a documented exception.

**CI check:** `make rendered-manifests-check` — validates all Helm overlays against schema + policy.

---

### Step 7 — Verify everything

Run the full deployment contract suite locally before opening a PR:

```bash
make network-contract-check      # DEPLOY-001: per-service network contract
make deploy-readiness ENV=staging # DEPLOY-002: staging promotion gate
make rendered-manifests-check    # DEPLOY-006: schema + policy validation
make deployment-drift-check      # DEPLOY-010: Inventory ↔ Helm ↔ Prometheus
make tls-contract-check          # DEPLOY-004: cert SANs + isolation
make k8s-rolling-update          # DEPLOY-005: rolling update zero-5xx test (requires Kind cluster)
make k8s-hpa-scale               # DEPLOY-009: HPA scale-up/scale-down zero-5xx test (requires Kind cluster)
```

All five static checks run in CI on every PR that touches `deploy/`. The rolling update test runs as part of `make k8s-e2e` in the disposable Kind environment.

---

## What does NOT need to change

- The NetworkPolicy template (`templates/networkpolicy.yaml`) — it is generated from `values.yaml`'s `network:` section, so no manual edits are needed.
- The `seev.rego` policy — it applies to all Deployments automatically.
- The AppProject (`deploy/gitops/projects/seev.yaml`) — it already allows all namespaced Deployment resources.

## Before staging promotion

After the service is merged and running in dev:

1. Run `make deploy-readiness ENV=staging VALUES="values-staging.yaml values-staging-<env>.yaml"` — must PASS.
2. Resolve all `REPLACE_WITH_*` values in `values-staging.yaml` from the secret manager.
3. Update `targetRevision` in `deploy/gitops/applications/seev-staging.yaml` to the promoted commit SHA.
4. Apply the Argo CD Application manually — automated sync is disabled for staging.
