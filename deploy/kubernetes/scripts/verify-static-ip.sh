#!/usr/bin/env bash
set -euo pipefail

KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
NAMESPACE="${NAMESPACE:-seev-edge}"
EXPECTED_IP="${EXPECTED_IP:-}"
actual_ip="$($KUBECTL_BIN get service traefik -n "$NAMESPACE" -o jsonpath='{.status.loadBalancer.ingress[0].ip}')"
if [[ -z "$actual_ip" ]]; then
  echo "Traefik has no external IP yet; run this after the cloud LoadBalancer is provisioned" >&2
  exit 1
fi
if [[ -n "$EXPECTED_IP" && "$actual_ip" != "$EXPECTED_IP" ]]; then
  echo "inbound IP changed: got $actual_ip, expected $EXPECTED_IP" >&2
  exit 1
fi
echo "static inbound IP verified: $actual_ip"
