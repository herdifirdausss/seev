#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CLUSTER_NAME="${CLUSTER_NAME:-seev-local}"
KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
HELM_BIN="${HELM_BIN:-helm}"
KIND_CONFIG="${KIND_CONFIG:-$ROOT_DIR/deploy/kubernetes/kind-config.yaml}"

for command in kind "$KUBECTL_BIN" "$HELM_BIN" docker curl; do
  command -v "$command" >/dev/null || { echo "$command is required; this script creates no cloud resources" >&2; exit 2; }
done

if ! kind get clusters | grep -qx "$CLUSTER_NAME"; then
  kind create cluster --name "$CLUSTER_NAME" --config "$KIND_CONFIG"
fi
"$KUBECTL_BIN" config use-context "kind-$CLUSTER_NAME" >/dev/null

"$KUBECTL_BIN" apply -f https://raw.githubusercontent.com/projectcalico/calico/v3.29.2/manifests/calico.yaml
"$KUBECTL_BIN" rollout status daemonset/calico-node -n kube-system --timeout=300s
"$KUBECTL_BIN" rollout status deployment/calico-kube-controllers -n kube-system --timeout=300s

"$ROOT_DIR/deploy/kubernetes/scripts/networkpolicy-preflight.sh"
"$ROOT_DIR/deploy/kubernetes/scripts/install-crds.sh"

make -C "$ROOT_DIR" certs cryptox-secret
"$ROOT_DIR/deploy/kubernetes/scripts/create-local-secrets.sh"

for service in gateway-service auth-service ledger-service payin-service payout-service fraud-service vendor-service admin-bff-service assurance-service; do
  build_service="$service"
  if [[ "$service" == "gateway-service" ]]; then
    build_service=gateway
  fi
  docker build --build-arg SERVICE="$build_service" -t "seev/$service:dev" "$ROOT_DIR"
done
docker build -f "$ROOT_DIR/deploy/kubernetes/migrations.Dockerfile" -t seev/migrations:dev "$ROOT_DIR"
kind load docker-image --name "$CLUSTER_NAME" \
  seev/gateway-service:dev seev/auth-service:dev seev/ledger-service:dev \
  seev/payin-service:dev seev/payout-service:dev seev/fraud-service:dev \
  seev/vendor-service:dev seev/admin-bff-service:dev seev/assurance-service:dev \
  seev/migrations:dev

"$KUBECTL_BIN" apply -k "$ROOT_DIR/deploy/kubernetes/platform/traefik"
"$KUBECTL_BIN" apply -k "$ROOT_DIR/deploy/kubernetes/platform/observability"
"$HELM_BIN" upgrade --install seev "$ROOT_DIR/deploy/helm/seev" \
  --namespace seev-app --create-namespace \
  -f "$ROOT_DIR/deploy/helm/seev/values.yaml" \
  -f "$ROOT_DIR/deploy/helm/seev/values-local.yaml" \
  --wait --wait-for-jobs --timeout 15m
"$KUBECTL_BIN" rollout status deployment/traefik -n seev-edge --timeout=300s
echo "local Seev Kubernetes deployment is installed; run deploy/kubernetes/scripts/smoke.sh"
