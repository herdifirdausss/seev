# Traefik platform edge

The manifest pins the Traefik 3.6 minor used by the compatibility suite. For
an actual cloud deployment, replace the tag with the reviewed image digest and
keep the chart/image pair recorded in the evidence log.

The Service deliberately sets `externalTrafficPolicy: Local`. This is part of
the callback security boundary: the L4 load-balancer path must deliver the
vendor peer address to Traefik and VendorService. `forwardedHeaders.insecure`
and `proxyProtocol.insecure` remain disabled; no arbitrary
`X-Forwarded-For` is trusted.

Install Gateway API and Traefik CRDs before this kustomization. The bootstrap
script uses the pinned upstream Gateway API release and checks that the CRDs
exist before applying the platform resources.
