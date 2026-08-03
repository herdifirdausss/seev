# Plan 63 implementation status

**Scope:** repository implementation completed without creating external cloud
resources.

| Milestone | Repository result | External proof still required |
|---|---|---|
| K0 | deployment matrices and evidence contract | measured resource baseline under the chosen workload |
| K1 | existing distroless/non-root image retained; migration image added | registry vulnerability scan and digest attestation |
| K2 | kind config, Calico install, policy enforcement preflight, local bootstrap | Docker/kind execution on a host with the required tools |
| K3 | reusable Helm chart, schema, values-local/GCP/AWS overlays, explicit migration Job | Helm render/lint and live restart/reschedule proof |
| K4 | Traefik platform manifest, Gateway API routes, HTTPS redirect, middleware, Local source preservation | pinned image digest compatibility test and live client-IP proof |
| K5 | namespace default-deny and workload/data/proxy policy matrix | live denied-path tests on Calico |
| K6 | Squid ACL/config, VendorService fail-closed proxy seam, direct-egress verifier | real HTTP vendor adapter and controlled echo endpoint |
| K7 | lightweight Prometheus profile and metrics/alert config | live scrape/traces and resource measurement |
| K8 | separate GCP/AWS Terraform trees, labels, budget gates, destroy script | provider credentials, reviewed plans, remote state/lock setup |
| K9 | GCP private GKE/NAT/static-IP/DNS/IAM Terraform and overlay | `terraform apply`, DNS/TLS, NAT source-IP evidence, cost report |
| K10 | intentionally deferred; single-edge remains the first-stage topology | dedicated callback edge only if learning/vendor need justifies cost |
| K11 | managed-secret handoff examples; in-cluster data remains sandbox profile | Cloud SQL/private connectivity and restore drill |
| K12 | GitOps Application examples and deployment validation workflow | Argo CD installation, immutable registry push, promotion/rollback |
| K13 | optional HPA/PDB/topology hooks | measured HPA, node drain, replica-loss and singleton-worker tests |
| K14 | separate AWS EKS/NAT/ECR/Secrets Manager Terraform and overlay | GCP teardown, AWS account-plan approval, apply and compare cost |
| K15 | callback/egress verification scripts and safety guardrails | full chaos matrix against a disposable cluster |
| K16 | evidence template and explicit external gates | all required cloud and operational evidence |

No row above is a claim that GCP, AWS, GitOps, managed PostgreSQL, or a real
vendor has been exercised from this checkout. The implementation is designed
to make those claims testable without changing the application chart.

## Current local proof

On 2026-08-03, the disposable `seev-local` kind cluster was exercised with
Calico, Gateway API v1.4.0, and Traefik v3.6.2. Helm release revision 17
completed with the public Gateway reporting `Accepted=True` and
`Programmed=True`. The following checks passed:

- all nine core application Deployments, PostgreSQL, Redis, RabbitMQ, Traefik,
  Squid, and Prometheus became ready;
- the public API returned an application response through the Gateway;
- the callback route rejected an unsigned request and kept the private admin
  route inaccessible;
- the callback route checks verified POST-only routing, explicit source CIDRs,
  `externalTrafficPolicy: Local`, and explicit trusted-proxy CIDRs;
- the VendorService policy probe denied direct internet access and denied an
  unapproved Squid destination.

This is local disposable-cluster evidence only. It does not replace cloud
static-IP, NAT, DNS/TLS, managed-secret, registry, provider, or operational
recovery evidence.

## Required local evidence sequence

```sh
make docs-check
make helm-lint
make k8s-preflight
deploy/kubernetes/scripts/bootstrap-local.sh
make k8s-smoke
make k8s-verify-callback
make k8s-verify-egress
```

The sequence requires Docker, kind, kubectl, Helm, network access for pinned
CRDs/images, and sufficient local capacity. It intentionally creates only the
disposable `seev-local` kind cluster and local Kubernetes Secrets.
