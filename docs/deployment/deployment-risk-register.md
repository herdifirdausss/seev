# Deployment risk register

K0 classifies every unresolved fact with an owner. UNKNOWN is not silently
converted into a manifest default.

| ID | Finding | Severity | Owner / downstream | Decision |
|---|---|---|---|---|
| K0-F-001 | Current Compose shares a certificate directory and private keys across services | high | K3 / platform-security | per-workload leaf mounts are mandatory |
| K0-F-002 | Real vendor hostname, CIDR, certificate, and callback source ranges are UNKNOWN | blocker for real vendor | K6 / vendor-integration | real vendor disabled; mock only |
| K0-F-003 | Direct VendorService egress fallback must remain disabled | high | K5/K6 | proxy-required contract; prove denial |
| K0-F-004 | Admin BFF and broker management must remain private | high | K4/K5 | no public route |
| K0-F-005 | Scheduler replica safety is mixed; some local locks are in-memory | high | K3/K9 | begin one replica; externalize lock/lease before scale |
| K0-F-006 | Resource profiles are local measurements, not cloud sizing | medium | K3/K9 | measure disposable profiles or explicitly defer |
| K0-F-007 | Runtime image is distroless and includes all migrations | medium | K1/K3 | document debug and migration-image choices |
| K0-F-008 | Backup repository and restore evidence are UNKNOWN | high | K7 | operations-only; no K0 recovery claim |
| K0-F-009 | Observability stack overhead is not mixed into app baseline | medium | K7 | separate R6 measurement |
| K0-F-010 | No production data or secret values may enter evidence | blocker | all tracks | validator and evidence policy enforce this |
| K0-F-011 | Resolved: the C3 notification channel packages are present in the current checkout, and both `make verify-static` and `make k0-inventory` pass | resolved | C3 implementation owner / K0 handoff | retain the record for traceability; K1 remains conditional on K0 runtime evidence and review |

K0 entry decision: the inventory contract and static gates are complete, but K1
is still conditional on runtime evidence and human review. Once that decision
is recorded, the first synthetic deployment can proceed to K1–K6 with the remaining
downstream-owned findings. The real vendor and production backup paths cannot
proceed until their rows leave the deferred state.

Source: [vendor-network.yaml](../../deploy/inventory/vendor-network.yaml),
[jobs.yaml](../../deploy/inventory/jobs.yaml),
[resource-baseline.yaml](../../deploy/inventory/resource-baseline.yaml), and
[secrets.yaml](../../deploy/inventory/secrets.yaml).
