#!/usr/bin/env bash
set -euo pipefail

KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
NAMESPACE="${NAMESPACE:-seev-edge}"

"$KUBECTL_BIN" get middleware callback-source-allowlist -n "$NAMESPACE" -o jsonpath='{.spec.ipAllowList.sourceRange[*]}' | grep -q '/'
"$KUBECTL_BIN" get service traefik -n "$NAMESPACE" -o jsonpath='{.spec.externalTrafficPolicy}' | grep -qx Local
"$KUBECTL_BIN" get ingressroute seev-callback -n "$NAMESPACE" -o jsonpath='{.spec.routes[0].match}' | grep -q 'Method(`POST`)'
"$KUBECTL_BIN" get serverstransport vendor-mtls -n "$NAMESPACE" -o jsonpath='{.spec.certificatesSecrets[0]}' | grep -qx seev-edge-backend-client
expected_trusted_proxy_cidrs="${EXPECTED_TRUSTED_PROXY_CIDRS:-}"
actual_trusted_proxy_cidrs="$($KUBECTL_BIN get deployment vendor-service -n seev-app -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="VENDOR_CALLBACK_TRUSTED_PROXY_CIDRS")].value}')"
if [[ "$actual_trusted_proxy_cidrs" != "$expected_trusted_proxy_cidrs" ]]; then
  echo "unexpected callback trusted-proxy CIDRs: got '$actual_trusted_proxy_cidrs', expected '$expected_trusted_proxy_cidrs'" >&2
  exit 1
fi
echo "callback edge checks passed: explicit CIDR allowlist, POST-only mTLS route, Local source preservation, trusted proxy CIDRs explicit"
