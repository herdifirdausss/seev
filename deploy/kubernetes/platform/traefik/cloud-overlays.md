# Traefik cloud overlays

Use exactly one provider overlay after the corresponding Terraform outputs are
known:

```sh
kubectl apply -k deploy/kubernetes/platform/traefik \
  # local: no cloud annotation
kubectl apply -k deploy/kubernetes/platform/traefik \
  # GCP: use kustomize edit or replace the placeholder first
```

GCP uses a reserved regional address and the L4 RBS path; AWS uses an NLB with
an explicitly selected target mode and Elastic IP allocation. Both retain
`externalTrafficPolicy: Local`. Do not enable PROXY protocol or trusted
forwarded headers until a live source-IP test proves the selected provider path.
