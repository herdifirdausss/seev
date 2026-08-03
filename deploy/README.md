# Deployment assets

| Area | Location | Purpose |
|---|---|---|
| Kubernetes application | [`helm/seev`](helm/seev) | reusable application/data/egress chart |
| Kubernetes platform | [`kubernetes/platform`](kubernetes/platform) | Traefik edge and provider overlays |
| Local cluster | [`kubernetes`](kubernetes) | kind + Calico bootstrap and verification |
| Cloud infrastructure | [`terraform`](terraform) | separate GCP/AWS provider trees |
| Existing local Compose | [`../docker-compose.yml`](../docker-compose.yml) | reference and parity baseline |

The Kubernetes path is a non-production learning sandbox. Read
[`docs/deployment/README.md`](../docs/deployment/README.md) before changing
routes, callback CIDRs, database ownership, or egress destinations.
