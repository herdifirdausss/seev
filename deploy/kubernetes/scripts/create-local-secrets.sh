#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
HELM_RELEASE_NAME="${HELM_RELEASE_NAME:-seev}"
HELM_RELEASE_NAMESPACE="${HELM_RELEASE_NAMESPACE:-seev-app}"

command -v "$KUBECTL_BIN" >/dev/null || { echo "kubectl is required" >&2; exit 2; }
command -v openssl >/dev/null || { echo "openssl is required" >&2; exit 2; }

for namespace in seev-app seev-data seev-egress seev-edge seev-observability; do
  "$KUBECTL_BIN" create namespace "$namespace" --dry-run=client -o yaml | "$KUBECTL_BIN" apply -f - >/dev/null
done

for namespace in seev-app seev-data seev-egress seev-edge; do
  "$KUBECTL_BIN" label namespace "$namespace" \
    app.kubernetes.io/managed-by=Helm --overwrite >/dev/null
  "$KUBECTL_BIN" annotate namespace "$namespace" \
    meta.helm.sh/release-name="$HELM_RELEASE_NAME" \
    meta.helm.sh/release-namespace="$HELM_RELEASE_NAMESPACE" --overwrite >/dev/null
done

random_secret() { openssl rand -hex 32; }

# Secret creation is intentionally idempotent. Re-running bootstrap must not
# rotate a database password behind a running PostgreSQL volume; operators who
# need rotation should do it as an explicit credential-rotation procedure.
existing_secret_value() {
  local namespace=$1 secret=$2 key=$3 encoded value
  encoded="$($KUBECTL_BIN get secret "$secret" -n "$namespace" -o "jsonpath={.data.${key}}" 2>/dev/null || true)"
  [ -n "$encoded" ] || return 1
  value="$(printf '%s' "$encoded" | base64 --decode 2>/dev/null || true)"
  [ -n "$value" ] || return 1
  printf '%s' "$value"
}

reuse_or_random() {
  existing_secret_value "$@" || random_secret
}

POSTGRES_SUPER_PASSWORD="${POSTGRES_SUPER_PASSWORD:-$(reuse_or_random seev-data seev-data-secrets postgres-super-password)}"
RABBITMQ_PASSWORD="${RABBITMQ_PASSWORD:-$(reuse_or_random seev-data seev-data-secrets rabbitmq-password)}"

data_args=(
  --from-literal=postgres-super-password="$POSTGRES_SUPER_PASSWORD"
  --from-literal=rabbitmq-password="$RABBITMQ_PASSWORD"
)
for service in gateway auth ledger payin payout fraud admin-bff assurance vendor; do
  data_args+=(--from-literal="${service}-postgres-password=$(reuse_or_random seev-data seev-data-secrets "${service}-postgres-password")")
done
for namespace in seev-data seev-app; do
  "$KUBECTL_BIN" create secret generic seev-data-secrets -n "$namespace" "${data_args[@]}" --dry-run=client -o yaml | "$KUBECTL_BIN" apply -f - >/dev/null
done

JWT_SECRET="${JWT_SECRET:-$(reuse_or_random seev-app seev-runtime-secrets jwt-secret)}"
INTERNAL_GRPC_TOKEN="${INTERNAL_GRPC_TOKEN:-$(reuse_or_random seev-app seev-runtime-secrets internal-grpc-token)}"
MOCKVENDOR_SECRET="${MOCKVENDOR_SECRET:-$(reuse_or_random seev-app seev-runtime-secrets vendor-mockvendor-secret)}"
MOCKVENDOR2_SECRET="${MOCKVENDOR2_SECRET:-$(reuse_or_random seev-app seev-runtime-secrets vendor-mockvendor2-secret)}"
KYC_PROVIDER_TOKEN="${KYC_PROVIDER_TOKEN:-$(reuse_or_random seev-app seev-runtime-secrets kyc-provider-token)}"
"$KUBECTL_BIN" create secret generic seev-runtime-secrets -n seev-app \
  --from-literal=jwt-secret="$JWT_SECRET" \
  --from-literal=internal-grpc-token="$INTERNAL_GRPC_TOKEN" \
  --from-literal=kyc-provider-token="$KYC_PROVIDER_TOKEN" \
  --from-literal=vendor-mockvendor-secret="$MOCKVENDOR_SECRET" \
  --from-literal=vendor-mockvendor2-secret="$MOCKVENDOR2_SECRET" \
  --dry-run=client -o yaml | "$KUBECTL_BIN" apply -f - >/dev/null

"$KUBECTL_BIN" create secret generic seev-crypto-secrets -n seev-app \
  --from-file=cryptox_key_v1="$ROOT_DIR/deploy/cryptox/secrets/cryptox_key_v1" \
  --from-file=cryptox_lookup_key="$ROOT_DIR/deploy/cryptox/secrets/cryptox_lookup_key" \
  --from-file=ledger_idempotency_key_v1="$ROOT_DIR/deploy/cryptox/secrets/ledger_idempotency_key_v1" \
  --from-file=merchant_api_key_pepper="$ROOT_DIR/deploy/cryptox/secrets/merchant_api_key_pepper" \
  --dry-run=client -o yaml | "$KUBECTL_BIN" apply -f - >/dev/null

cert_args=(
  --from-file=ca.pem="$ROOT_DIR/deploy/certs/ca.pem"
  --from-file=dev-operator.pem="$ROOT_DIR/deploy/certs/dev-operator.pem"
  --from-file=dev-operator-key.pem="$ROOT_DIR/deploy/certs/dev-operator-key.pem"
)
for service in gateway auth ledger payin payout fraud admin-bff assurance vendor; do
  cert_args+=(--from-file="$service.pem=$ROOT_DIR/deploy/certs/$service.pem")
  cert_args+=(--from-file="$service-key.pem=$ROOT_DIR/deploy/certs/$service-key.pem")
done
"$KUBECTL_BIN" create secret generic seev-mtls -n seev-app "${cert_args[@]}" --dry-run=client -o yaml | "$KUBECTL_BIN" apply -f - >/dev/null
prometheus_args=(
  --from-file=ca.pem="$ROOT_DIR/deploy/certs/ca.pem"
  --from-file=prometheus.pem="$ROOT_DIR/deploy/certs/prometheus.pem"
  --from-file=prometheus-key.pem="$ROOT_DIR/deploy/certs/prometheus-key.pem"
)
"$KUBECTL_BIN" create secret generic seev-prometheus-mtls -n seev-observability "${prometheus_args[@]}" --dry-run=client -o yaml | "$KUBECTL_BIN" apply -f - >/dev/null

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
openssl req -x509 -nodes -newkey rsa:2048 -days 7 \
  -keyout "$tmp_dir/tls.key" -out "$tmp_dir/tls.crt" \
  -subj "/CN=seev-local" \
  -addext "subjectAltName=DNS:api.local.seev.test,DNS:callback.local.seev.test,DNS:admin.local.seev.test" >/dev/null 2>&1
"$KUBECTL_BIN" create secret tls seev-edge-tls -n seev-edge \
  --cert="$tmp_dir/tls.crt" --key="$tmp_dir/tls.key" \
  --dry-run=client -o yaml | "$KUBECTL_BIN" apply -f - >/dev/null

"$KUBECTL_BIN" create secret generic seev-edge-backend-ca -n seev-edge \
  --from-file=ca.crt="$ROOT_DIR/deploy/certs/ca.pem" \
  --dry-run=client -o yaml | "$KUBECTL_BIN" apply -f - >/dev/null
"$KUBECTL_BIN" create secret tls seev-edge-backend-client -n seev-edge \
  --cert="$ROOT_DIR/deploy/certs/dev-operator.pem" \
  --key="$ROOT_DIR/deploy/certs/dev-operator-key.pem" \
  --dry-run=client -o yaml | "$KUBECTL_BIN" apply -f - >/dev/null
echo "local Kubernetes secrets created without printing secret values"
