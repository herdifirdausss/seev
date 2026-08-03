#!/usr/bin/env bash
set -euo pipefail

KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
NAMESPACE="seev-policy-preflight"
SERVER="policy-server"
CLIENT="policy-client"

cleanup() {
  "$KUBECTL_BIN" delete namespace "$NAMESPACE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT

"$KUBECTL_BIN" delete namespace "$NAMESPACE" --ignore-not-found --wait=true >/dev/null 2>&1 || true
"$KUBECTL_BIN" create namespace "$NAMESPACE" >/dev/null
"$KUBECTL_BIN" run "$SERVER" -n "$NAMESPACE" --image=nginx:1.27-alpine --labels=app=policy-server --port=80 --restart=Never >/dev/null
"$KUBECTL_BIN" run "$CLIENT" -n "$NAMESPACE" --image=busybox:1.36.1 --restart=Never -- sleep 3600 >/dev/null
"$KUBECTL_BIN" expose pod "$SERVER" -n "$NAMESPACE" --name="$SERVER" --port=80 --target-port=80 >/dev/null
"$KUBECTL_BIN" wait -n "$NAMESPACE" --for=condition=Ready pod/"$SERVER" pod/"$CLIENT" --timeout=180s

if ! "$KUBECTL_BIN" exec -n "$NAMESPACE" "$CLIENT" -- wget -q -T 3 -O- "http://$SERVER" >/dev/null; then
  echo "NetworkPolicy preflight setup failed: baseline connection did not work" >&2
  exit 1
fi

cat <<'POLICY' | "$KUBECTL_BIN" apply -f -
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: deny-policy-server
  namespace: seev-policy-preflight
spec:
  podSelector:
    matchLabels:
      app: policy-server
  policyTypes: [Ingress]
  ingress: []
POLICY

sleep 5
if "$KUBECTL_BIN" exec -n "$NAMESPACE" "$CLIENT" -- wget -q -T 3 -O- "http://$SERVER" >/dev/null; then
  echo "NetworkPolicy enforcement preflight failed: denied connection succeeded" >&2
  exit 1
fi
echo "NetworkPolicy enforcement proven by a denied connection"
