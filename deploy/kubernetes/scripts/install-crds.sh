#!/usr/bin/env bash
set -euo pipefail

KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
# Traefik v3.6's Gateway provider expects the standard Gateway API bundle
# that includes BackendTLSPolicy.
GATEWAY_API_VERSION="${GATEWAY_API_VERSION:-v1.4.0}"
TRAEFIK_VERSION="${TRAEFIK_VERSION:-v3.6.2}"

command -v "$KUBECTL_BIN" >/dev/null || {
	echo "kubectl is required" >&2
	exit 2
}
"$KUBECTL_BIN" apply -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GATEWAY_API_VERSION}/standard-install.yaml"
# Traefik CRDs are used only for middleware that Gateway API cannot represent.
"$KUBECTL_BIN" apply -f "https://raw.githubusercontent.com/traefik/traefik/${TRAEFIK_VERSION}/docs/content/reference/dynamic-configuration/kubernetes-crd-definition-v1.yml"
"$KUBECTL_BIN" wait --for=condition=Established crd/gateways.gateway.networking.k8s.io --timeout=120s
"$KUBECTL_BIN" wait --for=condition=Established crd/backendtlspolicies.gateway.networking.k8s.io --timeout=120s
"$KUBECTL_BIN" wait --for=condition=Established crd/middlewares.traefik.io --timeout=120s
echo "Gateway API and Traefik CRDs installed"

# metrics-server: required for HPA scale-up/scale-down tests (DEPLOY-009).
# Kind uses self-signed kubelet TLS so --kubelet-insecure-tls is required here.
METRICS_SERVER_VERSION="${METRICS_SERVER_VERSION:-v0.7.2}"
"$KUBECTL_BIN" apply -f "https://github.com/kubernetes-sigs/metrics-server/releases/download/${METRICS_SERVER_VERSION}/components.yaml"
"$KUBECTL_BIN" patch deployment metrics-server -n kube-system \
	--type=json -p '[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'
"$KUBECTL_BIN" rollout status deployment/metrics-server -n kube-system --timeout=120s
echo "metrics-server installed (kubelet-insecure-tls enabled for Kind)"
