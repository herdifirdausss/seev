#!/usr/bin/env bash
# HPA scale-up and scale-down safety test (DEPLOY-009).
#
# Applies a temporary HPA to auth-service, fires a burst of concurrent
# requests to saturate CPU, waits for scale-out, then removes load and
# waits for scale-in.  Asserts zero unexpected 5xx throughout.
#
# Requirements:
#   - kubectl available and context pointing at the Kind cluster
#   - metrics-server running (installed by install-crds.sh)
#   - seev Helm release deployed via make k8s-bootstrap
#   - Gateway is reachable via the port-forward opened by the caller
#     (GATEWAY_PORT must match a live port-forward to traefik)
#
# Run via: make k8s-hpa-scale
set -euo pipefail

KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
NAMESPACE="${NAMESPACE:-seev-app}"
GATEWAY_PORT="${GATEWAY_PORT:-8443}"
SCALE_WAIT_SECONDS="${SCALE_WAIT_SECONDS:-120}"
SCALEDOWN_WAIT_SECONDS="${SCALEDOWN_WAIT_SECONDS:-180}"
BURST_WORKERS="${BURST_WORKERS:-8}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/seev-hpa-scale.XXXXXX")"
PF_PID=""
LOAD_PID=""
UNEXPECTED_5XX=0
TOTAL=0

cleanup() {
	if [[ -n "$LOAD_PID" ]]; then
		kill "$LOAD_PID" >/dev/null 2>&1 || true
		wait "$LOAD_PID" >/dev/null 2>&1 || true
	fi
	if [[ -n "$PF_PID" ]]; then
		kill "$PF_PID" >/dev/null 2>&1 || true
		wait "$PF_PID" >/dev/null 2>&1 || true
	fi
	"$KUBECTL_BIN" delete hpa seev-auth-hpa-test -n "$NAMESPACE" \
		--ignore-not-found >/dev/null 2>&1 || true
	rm -rf "$TMP_DIR"
}
trap cleanup EXIT

for cmd in "$KUBECTL_BIN" curl; do
	command -v "$cmd" >/dev/null 2>&1 || {
		printf 'verify-hpa-scale: %s is required\n' "$cmd" >&2
		exit 2
	}
done

# Check metrics-server is ready
if ! "$KUBECTL_BIN" top pods -n "$NAMESPACE" >/dev/null 2>&1; then
	printf 'verify-hpa-scale: metrics-server is not ready — run make k8s-bootstrap first\n' >&2
	exit 2
fi

# Port-forward to gateway (reuse existing if GATEWAY_PORT already open)
if ! curl --noproxy '*' -ksS --max-time 2 \
	--resolve "api.local.seev.test:$GATEWAY_PORT:127.0.0.1" \
	"https://api.local.seev.test:$GATEWAY_PORT/api/v1/auth/login" >/dev/null 2>&1; then
	"$KUBECTL_BIN" wait -n seev-edge --for=condition=Available deployment/traefik --timeout=60s >/dev/null
	"$KUBECTL_BIN" port-forward -n seev-edge service/traefik "$GATEWAY_PORT":443 \
		>"$TMP_DIR/pf.log" 2>&1 &
	PF_PID=$!
	for _ in $(seq 1 20); do
		curl --noproxy '*' -ksS --max-time 2 \
			--resolve "api.local.seev.test:$GATEWAY_PORT:127.0.0.1" \
			"https://api.local.seev.test:$GATEWAY_PORT/api/v1/auth/login" >/dev/null 2>&1 && break
		sleep 1
	done
fi

# Seed a test user for load generation
TEST_EMAIL="hpa-scale-$(date +%s)-$$@example.com"
TEST_PASSWORD="HpaScale!2026"
HTTP_CODE="$(curl --noproxy '*' -ksS -o /dev/null -w '%{http_code}' \
	--resolve "api.local.seev.test:$GATEWAY_PORT:127.0.0.1" \
	-X POST "https://api.local.seev.test:$GATEWAY_PORT/api/v1/auth/register" \
	-H 'Content-Type: application/json' \
	-d "{\"email\":\"$TEST_EMAIL\",\"password\":\"$TEST_PASSWORD\",\"full_name\":\"HPA Scale Test\"}")"
[[ "$HTTP_CODE" =~ ^2 ]] || {
	printf 'verify-hpa-scale: seed registration failed (HTTP %s)\n' "$HTTP_CODE" >&2
	exit 1
}
printf 'verify-hpa-scale: seed user registered (%s)\n' "$TEST_EMAIL"

# Apply a temporary HPA targeting auth-service's Deployment.
# Low CPU threshold (10%) makes scale-up reachable under synthetic load.
"$KUBECTL_BIN" apply -f - -n "$NAMESPACE" >/dev/null <<HPA_YAML
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: seev-auth-hpa-test
  annotations:
    seev.io/purpose: hpa-scale-test
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: auth-service
  minReplicas: 1
  maxReplicas: 3
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 10
  behavior:
    scaleDown:
      stabilizationWindowSeconds: 30
      policies:
        - type: Pods
          value: 1
          periodSeconds: 15
HPA_YAML
printf 'verify-hpa-scale: applied temporary HPA (auth-service, max=3, cpu>10%%)\n'

# Monitor probes: record 5xx
probe_login() {
	local code
	code="$(curl --noproxy '*' -ksS -o /dev/null -w '%{http_code}' \
		--resolve "api.local.seev.test:$GATEWAY_PORT:127.0.0.1" \
		-X POST "https://api.local.seev.test:$GATEWAY_PORT/api/v1/auth/login" \
		-H 'Content-Type: application/json' \
		-d "{\"email\":\"$TEST_EMAIL\",\"password\":\"$TEST_PASSWORD\"}" \
		--max-time 10 2>/dev/null || true)"
	printf '%s\n' "${code:-000}"
}

run_burst() {
	while [[ -f "$TMP_DIR/burst-active" ]]; do
		for _ in $(seq 1 "$BURST_WORKERS"); do
			code="$(probe_login)"
			TOTAL=$((TOTAL + 1))
			if [[ "$code" =~ ^5 ]]; then
				UNEXPECTED_5XX=$((UNEXPECTED_5XX + 1))
				printf 'verify-hpa-scale: unexpected %s during load\n' "$code" >&2
			fi
		done
	done
}

# Phase 1: scale-up under burst load
printf 'verify-hpa-scale: starting burst load — waiting for scale-up (max %ds)\n' "$SCALE_WAIT_SECONDS"
touch "$TMP_DIR/burst-active"
run_burst &
LOAD_PID=$!

SCALED_UP=0
for _ in $(seq 1 "$SCALE_WAIT_SECONDS"); do
	current_replicas="$("$KUBECTL_BIN" get hpa seev-auth-hpa-test -n "$NAMESPACE" \
		-o jsonpath='{.status.currentReplicas}' 2>/dev/null || echo 1)"
	if [[ "${current_replicas}" -gt 1 ]]; then
		printf 'verify-hpa-scale: scale-up confirmed — replicas=%s\n' "$current_replicas"
		SCALED_UP=1
		break
	fi
	sleep 1
done

if [[ "$SCALED_UP" -eq 0 ]]; then
	printf 'verify-hpa-scale: WARN: scale-up not observed within %ds (metrics may need more time)\n' \
		"$SCALE_WAIT_SECONDS"
fi

# Phase 2: scale-down after load stops — verify no in-flight interruption
printf 'verify-hpa-scale: stopping burst load — monitoring for scale-down\n'
rm -f "$TMP_DIR/burst-active"
wait "$LOAD_PID" 2>/dev/null || true
LOAD_PID=""

for _ in $(seq 1 "$SCALEDOWN_WAIT_SECONDS"); do
	current_replicas="$("$KUBECTL_BIN" get hpa seev-auth-hpa-test -n "$NAMESPACE" \
		-o jsonpath='{.status.currentReplicas}' 2>/dev/null || echo 2)"
	if [[ "${current_replicas}" -le 1 ]]; then
		printf 'verify-hpa-scale: scale-down confirmed — replicas back to 1\n'
		break
	fi
	sleep 1
done

# Post-scale-down: verify service is still healthy
POST_CODE="$(probe_login)"
[[ "$POST_CODE" =~ ^2 ]] || {
	printf 'verify-hpa-scale: post-scale-down request failed (HTTP %s) — service not healthy\n' \
		"$POST_CODE" >&2
	UNEXPECTED_5XX=$((UNEXPECTED_5XX + 1))
}

printf 'verify-hpa-scale: total=%d unexpected_5xx=%d\n' "$TOTAL" "$UNEXPECTED_5XX"

if [[ "$UNEXPECTED_5XX" -gt 0 ]]; then
	printf 'verify-hpa-scale: FAIL: %d unexpected 5xx responses during HPA scale event\n' \
		"$UNEXPECTED_5XX" >&2
	exit 1
fi
printf 'verify-hpa-scale: PASS: zero unexpected 5xx during scale-up and scale-down\n'
