#!/usr/bin/env bash
set -euo pipefail

KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
PORT="${PORT:-8443}"
PF_PID=""
cleanup() {
  if [[ -n "$PF_PID" ]]; then
    kill "$PF_PID" >/dev/null 2>&1 || true
    wait "$PF_PID" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

for namespace in seev-app seev-data seev-egress seev-edge; do
  "$KUBECTL_BIN" get namespace "$namespace" >/dev/null
done
"$KUBECTL_BIN" wait -n seev-edge --for=condition=Available deployment/traefik --timeout=180s
"$KUBECTL_BIN" wait -n seev-edge --for=condition=Programmed=True gateway/seev-public --timeout=180s
MIGRATION_JOB="$($KUBECTL_BIN get jobs -n seev-app -l seev.io/job=migrations --sort-by=.metadata.creationTimestamp -o name | tail -n 1)"
if [[ -z "$MIGRATION_JOB" ]]; then
  echo "no migration Job found" >&2
  exit 1
fi
"$KUBECTL_BIN" wait -n seev-app --for=condition=Complete "$MIGRATION_JOB" --timeout=180s
for deployment in gateway-service auth-service ledger-service payin-service payout-service fraud-service vendor-service admin-bff-service assurance-service; do
  "$KUBECTL_BIN" wait -n seev-app --for=condition=Available "deployment/$deployment" --timeout=180s
done
"$KUBECTL_BIN" wait -n seev-data --for=jsonpath='{.status.readyReplicas}'=1 statefulset/postgres --timeout=180s
"$KUBECTL_BIN" wait -n seev-data --for=jsonpath='{.status.readyReplicas}'=1 statefulset/redis --timeout=180s
"$KUBECTL_BIN" wait -n seev-data --for=jsonpath='{.status.readyReplicas}'=1 statefulset/rabbitmq --timeout=180s

"$KUBECTL_BIN" port-forward -n seev-edge service/traefik "$PORT":443 >/tmp/seev-traefik-port-forward.log 2>&1 &
PF_PID=$!
for _ in {1..30}; do
  if curl --noproxy '*' -ksS --resolve "callback.local.seev.test:${PORT}:127.0.0.1" "https://callback.local.seev.test:${PORT}/" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

callback_status="$(curl --noproxy '*' -ksS -o /dev/null -w '%{http_code}' \
  --resolve "callback.local.seev.test:${PORT}:127.0.0.1" \
  -X POST -H 'Content-Type: application/json' -d '{}' \
  "https://callback.local.seev.test:${PORT}/webhooks/mockvendor")"
if [[ "$callback_status" != "401" ]]; then
  echo "callback route returned $callback_status; expected application signature rejection (401)" >&2
  exit 1
fi

admin_status="$(curl --noproxy '*' -ksS -o /dev/null -w '%{http_code}' \
  --resolve "callback.local.seev.test:${PORT}:127.0.0.1" \
  "https://callback.local.seev.test:${PORT}/api/v1/admin/audit")"
if [[ "$admin_status" == "200" ]]; then
  echo "unexpected public admin route" >&2
  exit 1
fi
public_status="$(curl --noproxy '*' -ksS -o /dev/null -w '%{http_code}' \
  --resolve "api.local.seev.test:${PORT}:127.0.0.1" \
  -X POST -H 'Content-Type: application/json' -d '{}' \
  "https://api.local.seev.test:${PORT}/api/v1/auth/login")"
if [[ "$public_status" == "404" || "$public_status" == "502" || "$public_status" == "503" ]]; then
  echo "public API route returned $public_status; expected an application response" >&2
  exit 1
fi
echo "Kubernetes smoke passed: workloads and Gateway ready, public API routed, callback isolated, unsigned callback rejected"
