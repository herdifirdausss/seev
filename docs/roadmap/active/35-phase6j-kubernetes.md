# 35 — Phase 6j (Optional): Local Kubernetes with kind

> [Documentation home](../../README.md) · [Roadmap](../README.md) · [Active plans](README.md)

Read [plan 26](../archive/26-phase6a-foundations.md) first. Prerequisite: [plan 34](../archive/34-phase6i-verification.md). This phase is optional learning work; the system is complete without it.

On the 3.9 GB development machine, kind replaces Compose. Do not run both full environments at the same time.

## Context

Move the six-service topology from Docker Compose to a local kind cluster. The exercise covers per-service manifests, PostgreSQL initialization with multiple databases, migration Jobs, secrets, and NodePorts for the public services.

## T1 — Base manifests

Create `deploy/k8s/` with a `seev` namespace, a Deployment and ClusterIP Service for gateway, auth, ledger, payin, payout, and fraud, and NodePorts for gateway and auth. Use the existing service Dockerfile with a service argument, small resource requests/limits, and liveness/readiness probes through `-healthcheck` or HTTP health endpoints.

Add PostgreSQL as a StatefulSet with a persistent volume and initialization ConfigMap, plus Redis and RabbitMQ Deployments. Store JWT, internal gRPC, and per-service database credentials in Secrets; put non-sensitive service addresses in ConfigMaps.

**Tests:** apply the manifests with `kubectl apply -k deploy/k8s` to a clean kind cluster and wait for every pod to become Ready.

Status: not started.

## T2 — Migration Jobs and tooling

Add one migration Job per service using the service migration directory and database role. Ensure Jobs complete before application Deployments become ready. Add Makefile targets:

- `kind-up` — create the cluster and apply manifests;
- `kind-load` — build images and load them into kind;
- `kind-down` — remove the cluster.

**Tests:** run `make kind-up` end to end with Compose stopped.

Status: not started.

## T3 — Verification

Port-forward gateway and auth, then run the core business subset: register, login, top-up, transfer, and notification delivery.

**DoD:** the core business journey passes against Kubernetes.

Status: not started.

## Final verification

Run the master gate plus the kind business journey. Update the plan index when the optional Kubernetes exercise is complete.
