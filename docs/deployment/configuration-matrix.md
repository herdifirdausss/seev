# Configuration matrix

The generated configuration inventory records names and ownership, never
values. Generated keys are sourced from .env.example, Go environment
lookups, and Compose wiring. Sensitive names map to Kubernetes Secrets;
non-sensitive names are ConfigMap candidates pending K3 reload/requiredness
review.

| Category | Examples | Target | K0 decision |
|---|---|---|---|
| service identity and ports | APP_NAME, APP_PORT, INTERNAL_PORT | ConfigMap | per-workload |
| database and broker endpoints | POSTGRES_*, RABBITMQ_*, REDIS_* | ConfigMap plus Secret references | owner-specific |
| timeouts and worker controls | *_TIMEOUT, WORKER_ENABLED, intervals | ConfigMap | startup-time read unless code proves reload |
| auth and policy flags | JWT_*, KYC_*, feature flags | split | secret material isolated |
| cryptographic and credentials | *_KEY, *_TOKEN, *_SECRET, pepper, credentials | Secret | never logged |
| TLS paths and identities | CA, leaf files, cert directory | Secret/volume | per-workload mount |
| vendor boundary | callback CIDRs, proxy endpoint, provider mode | split | real provider disabled |
| telemetry and alerting | OTLP endpoint, alert URL | ConfigMap plus Secret if credentialed | optional/non-blocking |

Use [configuration.yaml](../../deploy/inventory/configuration.yaml) as the K3
input. required, default, and reloadable status remain explicit UNKNOWN where
code/config validation has not yet established them; downstream owners are K3
and platform-security.
