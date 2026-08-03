# Lightweight Kubernetes observability

This is the first-cloud profile: one short-retention Prometheus with structured
application/Traefik metrics. It deliberately does not install Loki or Tempo on
the small first sandbox. Use the existing Compose observability profile or a
later kube-prometheus/Loki/Tempo stage for the full local learning stack.

The Prometheus certificate is a scrape identity, not a public credential. The
`seev-prometheus-mtls` Secret must be created from the generated local certs or
the approved managed-secret workflow before applying this kustomization.
