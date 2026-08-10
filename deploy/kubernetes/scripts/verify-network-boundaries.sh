#!/usr/bin/env bash
# Positive and negative Calico journeys for service, datastore, broker, and
# vendor-proxy boundaries. Run only after the local Helm release is ready.
set -euo pipefail

KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
NAMESPACE="seev-app"
IMAGE="busybox:1.38@sha256:dc2d74b28e4cf8984fa52af1f39bc7c3d9c73760b41a74d629f5d11b1ab28616"
PROBE_PREFIX="network-boundary-$$"
PROBES=()

cleanup() {
	for pod in "${PROBES[@]}"; do
		"$KUBECTL_BIN" delete pod "$pod" -n "$NAMESPACE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	done
}
trap cleanup EXIT

create_probe() {
	local pod="$1" label="${2:-}"
	local overrides
	overrides='{"spec":{"securityContext":{"runAsNonRoot":true,"runAsUser":65532,"runAsGroup":65532,"seccompProfile":{"type":"RuntimeDefault"}},"containers":[{"name":"probe","image":"'"$IMAGE"'","command":["sleep","300"],"securityContext":{"allowPrivilegeEscalation":false,"readOnlyRootFilesystem":true,"capabilities":{"drop":["ALL"]}}}]}}'
	PROBES+=("$pod")
	if [[ -n "$label" ]]; then
		"$KUBECTL_BIN" run "$pod" -n "$NAMESPACE" --image="$IMAGE" --labels="seev.io/service=$label" \
			--restart=Never --overrides="$overrides" --command -- sleep 300 >/dev/null
	else
		"$KUBECTL_BIN" run "$pod" -n "$NAMESPACE" --image="$IMAGE" \
			--restart=Never --overrides="$overrides" --command -- sleep 300 >/dev/null
	fi
	"$KUBECTL_BIN" wait -n "$NAMESPACE" --for=condition=Ready "pod/$pod" --timeout=120s >/dev/null
}

can_connect() {
	local pod="$1" host="$2" port="$3"
	"$KUBECTL_BIN" exec -n "$NAMESPACE" "$pod" -- nc -z -w 3 "$host" "$port" >/dev/null 2>&1
}

expect_allow() {
	local pod="$1" host="$2" port="$3" label="$4"
	if ! can_connect "$pod" "$host" "$port"; then
		printf 'network-boundaries: expected allow failed: %s (%s:%s)\n' "$label" "$host" "$port" >&2
		exit 1
	fi
	printf 'network-boundaries: allow: %s\n' "$label"
}

expect_deny() {
	local pod="$1" host="$2" port="$3" label="$4"
	if can_connect "$pod" "$host" "$port"; then
		printf 'network-boundaries: expected denial failed: %s (%s:%s)\n' "$label" "$host" "$port" >&2
		exit 1
	fi
	printf 'network-boundaries: deny: %s\n' "$label"
}

unlabeled="${PROBE_PREFIX}-unlabeled"
assurance="${PROBE_PREFIX}-assurance"
vendor="${PROBE_PREFIX}-vendor"
create_probe "$unlabeled"
create_probe "$assurance" assurance
create_probe "$vendor" vendor

expect_deny "$unlabeled" ledger-service.seev-app.svc.cluster.local 9091 "unlabeled pod to internal service"
expect_deny "$unlabeled" postgres.seev-data.svc.cluster.local 5432 "unlabeled pod to PostgreSQL"
expect_deny "$unlabeled" redis.seev-data.svc.cluster.local 6379 "unlabeled pod to Redis"
expect_deny "$unlabeled" rabbitmq.seev-data.svc.cluster.local 5672 "unlabeled pod to RabbitMQ"

expect_allow "$assurance" ledger-service.seev-app.svc.cluster.local 9091 "assurance to declared Ledger dependency"
expect_allow "$assurance" postgres.seev-data.svc.cluster.local 5432 "assurance to declared PostgreSQL dependency"
expect_deny "$assurance" redis.seev-data.svc.cluster.local 6379 "assurance to undeclared Redis"
expect_deny "$assurance" rabbitmq.seev-data.svc.cluster.local 5672 "assurance to undeclared RabbitMQ"
expect_deny "$assurance" gateway-service.seev-app.svc.cluster.local 8081 "assurance to undeclared Gateway dependency"

expect_allow "$vendor" payin-service.seev-app.svc.cluster.local 9092 "vendor to declared Payin dependency"
expect_allow "$vendor" squid.seev-egress.svc.cluster.local 3128 "vendor to approved egress proxy"
expect_deny "$vendor" redis.seev-data.svc.cluster.local 6379 "vendor to undeclared Redis"
expect_deny "$vendor" rabbitmq.seev-data.svc.cluster.local 5672 "vendor to undeclared RabbitMQ"

if "$KUBECTL_BIN" exec -n "$NAMESPACE" "$vendor" -- wget -T 3 -qO- https://example.com >/dev/null 2>&1; then
	printf 'network-boundaries: vendor direct internet access unexpectedly succeeded\n' >&2
	exit 1
fi
printf 'network-boundaries: deny: vendor direct internet; proxy-only boundary preserved\n'
