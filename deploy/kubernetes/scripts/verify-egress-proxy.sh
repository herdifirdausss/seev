#!/usr/bin/env bash
set -euo pipefail

KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
NAMESPACE="seev-app"
POD="egress-policy-probe-$$"
cleanup() {
  "$KUBECTL_BIN" delete pod "$POD" -n "$NAMESPACE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT

pod_overrides="{\"spec\":{\"securityContext\":{\"runAsNonRoot\":true,\"runAsUser\":65532,\"runAsGroup\":65532,\"seccompProfile\":{\"type\":\"RuntimeDefault\"}},\"containers\":[{\"name\":\"$POD\",\"image\":\"curlimages/curl:8.10.1\",\"command\":[\"sleep\",\"120\"],\"securityContext\":{\"allowPrivilegeEscalation\":false,\"readOnlyRootFilesystem\":true,\"capabilities\":{\"drop\":[\"ALL\"]}}}]}}"
"$KUBECTL_BIN" run "$POD" -n "$NAMESPACE" --image=curlimages/curl:8.10.1 \
  --labels=seev.io/service=vendor --restart=Never --overrides="$pod_overrides" --command -- sleep 120 >/dev/null
"$KUBECTL_BIN" wait -n "$NAMESPACE" --for=condition=Ready pod/"$POD" --timeout=120s
if "$KUBECTL_BIN" exec -n "$NAMESPACE" "$POD" -- curl -fsS --connect-timeout 3 https://example.com >/dev/null 2>&1; then
  echo "VendorService policy failed: direct internet connection succeeded" >&2
  exit 1
fi
if "$KUBECTL_BIN" exec -n "$NAMESPACE" "$POD" -- curl -fsS --connect-timeout 3 -x http://squid.seev-egress.svc.cluster.local:3128 https://example.com >/dev/null 2>&1; then
  echo "Squid policy failed: unapproved destination succeeded" >&2
  exit 1
fi
"$KUBECTL_BIN" get networkpolicy vendor-only-proxy -n "$NAMESPACE" >/dev/null
"$KUBECTL_BIN" get configmap squid-config -n seev-egress >/dev/null
echo "egress checks passed: direct access denied and unapproved proxy destination denied"
