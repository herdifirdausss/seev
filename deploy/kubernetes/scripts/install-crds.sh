#!/usr/bin/env bash
set -euo pipefail

KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
# Traefik v3.6's Gateway provider expects the standard Gateway API bundle
# that includes BackendTLSPolicy.
GATEWAY_API_VERSION="${GATEWAY_API_VERSION:-v1.4.0}"
TRAEFIK_VERSION="${TRAEFIK_VERSION:-v3.6.2}"

command -v "$KUBECTL_BIN" >/dev/null || { echo "kubectl is required" >&2; exit 2; }
"$KUBECTL_BIN" apply -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GATEWAY_API_VERSION}/standard-install.yaml"
# Traefik CRDs are used only for middleware that Gateway API cannot represent.
"$KUBECTL_BIN" apply -f "https://raw.githubusercontent.com/traefik/traefik/${TRAEFIK_VERSION}/docs/content/reference/dynamic-configuration/kubernetes-crd-definition-v1.yml"
"$KUBECTL_BIN" wait --for=condition=Established crd/gateways.gateway.networking.k8s.io --timeout=120s
"$KUBECTL_BIN" wait --for=condition=Established crd/backendtlspolicies.gateway.networking.k8s.io --timeout=120s
"$KUBECTL_BIN" wait --for=condition=Established crd/middlewares.traefik.io --timeout=120s
echo "Gateway API and Traefik CRDs installed"
