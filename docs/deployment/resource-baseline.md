# Resource baseline

K0 separates measured local behavior from Kubernetes sizing decisions. The
host baseline is constrained: approximately 8 GiB RAM and approximately 4–5
GiB free disk while Plan 63's disposable kind cluster remains present. Docker
samples must be isolated by Compose project and must not be read as a cloud
capacity claim.

| Profile | Required workload | Status | Evidence / owner |
|---|---|---|---|
| R0 | PostgreSQL, Redis, RabbitMQ idle | measured with a bounded local sample; 10-minute stabilization target deferred | [R0 CSV](../evidence/k0/resources/R0-20260802T235309Z.csv); K0 |
| R1 | nine services plus infrastructure idle | measured with a bounded local sample; 10-minute stabilization target deferred | [R1 CSV](../evidence/k0/resources/R1-20260802T235223Z.csv); K0 |
| R2 | synthetic registration/KYC/top-up/signed callback journey | measured with bounded resource sample and settled-status assertion | [R2 CSV](../evidence/k0/resources/R2-20260802T235807Z.csv), [journey record](../evidence/k0/verification/runtime-journeys.md); K0 |
| R3 | business journey | deferred as a clean profile: final failover probe was affected by reusable Redis circuit state | [journey record](../evidence/k0/verification/runtime-journeys.md); K0/K9 |
| R4 | admin journey | deferred: host CPU contention during native linking | [journey record](../evidence/k0/verification/runtime-journeys.md); K0/K9 |
| R5 | acknowledged disposable load smoke | deferred unless disk/memory budget permits | K0 |
| R6 | optional observability | deferred; measure separately under K7 | K7 |

The sampler records timestamp, CPU, memory, network I/O, block I/O, and PIDs.
Application-level connection counts, queue depth, latency, and startup timing
remain required additions to journey evidence. No final CPU/memory request or
limit is accepted from this document alone.

Initial classes are EDGE_LATENCY (gateway/auth), MONEY_CRITICAL
(ledger/payin/payout/fraud), ADMIN_LOW_TRAFFIC (admin-bff),
AUDIT_BACKGROUND (assurance), and VENDOR_BOUNDARY (vendor). The node
footprint formula is application requests plus data dependencies, edge/proxy,
system overhead, and 20–30% scheduling headroom.

Machine-readable status and connection-budget assumptions are in
[resource-baseline.yaml](../../deploy/inventory/resource-baseline.yaml).
