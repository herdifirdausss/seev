#!/usr/bin/env bash
# Kubernetes process-failure drill for a disposable Seev cluster.
#
# The script deletes exactly one Gateway pod, waits for the Deployment to
# replace it, and reruns the Kubernetes smoke assertions. It never deletes a
# Deployment, StatefulSet, namespace, or volume. Use it only against the
# local/disposable cluster selected by the current kubectl context.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
APP_NAMESPACE="${APP_NAMESPACE:-seev-app}"
PORT="${PORT:-8444}"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
	cat <<'EOF'
Usage: deploy/kubernetes/scripts/chaos.sh

Deletes one gateway-service pod in APP_NAMESPACE, verifies replacement and
readiness, then runs the Kubernetes smoke journey through the recovered edge.
The current kubectl context must point at a disposable Seev cluster.
EOF
	exit 0
fi

for command in "$KUBECTL_BIN" curl; do
	command -v "$command" >/dev/null || { echo "k8s-chaos: $command is required" >&2; exit 2; }
done

"$KUBECTL_BIN" get namespace "$APP_NAMESPACE" >/dev/null
"$KUBECTL_BIN" get deployment gateway-service -n "$APP_NAMESPACE" >/dev/null

victim="$("$KUBECTL_BIN" get pods -n "$APP_NAMESPACE" -l seev.io/service=gateway \
	-o jsonpath='{.items[0].metadata.name}')"
[[ -n "$victim" ]] || { echo 'k8s-chaos: no Gateway pod found' >&2; exit 1; }
victim_uid="$("$KUBECTL_BIN" get pod "$victim" -n "$APP_NAMESPACE" -o jsonpath='{.metadata.uid}')"
echo "k8s-chaos: deleting only gateway pod $victim (uid=$victim_uid)"
"$KUBECTL_BIN" delete pod "$victim" -n "$APP_NAMESPACE" --wait=false

"$KUBECTL_BIN" rollout status deployment/gateway-service -n "$APP_NAMESPACE" --timeout=180s

replacement=''
for _ in $(seq 1 60); do
	replacement="$("$KUBECTL_BIN" get pods -n "$APP_NAMESPACE" -l seev.io/service=gateway \
		-o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
	if [[ -n "$replacement" ]]; then
		replacement_uid="$("$KUBECTL_BIN" get pod "$replacement" -n "$APP_NAMESPACE" \
			-o jsonpath='{.metadata.uid}' 2>/dev/null || true)"
		if [[ -n "$replacement_uid" && "$replacement_uid" != "$victim_uid" ]]; then
			"$KUBECTL_BIN" wait -n "$APP_NAMESPACE" --for=condition=Ready \
				"pod/$replacement" --timeout=30s
			break
		fi
	fi
	sleep 2
done

[[ -n "$replacement" ]] || { echo 'k8s-chaos: replacement Gateway pod was not created' >&2; exit 1; }
replacement_uid="$("$KUBECTL_BIN" get pod "$replacement" -n "$APP_NAMESPACE" -o jsonpath='{.metadata.uid}')"
[[ "$replacement_uid" != "$victim_uid" ]] || {
	echo 'k8s-chaos: Gateway pod was not replaced after deletion' >&2
	exit 1
}

PORT="$PORT" "$ROOT_DIR/deploy/kubernetes/scripts/smoke.sh"
echo "k8s-chaos: Gateway pod replacement and recovered edge smoke passed (new uid=$replacement_uid)"
