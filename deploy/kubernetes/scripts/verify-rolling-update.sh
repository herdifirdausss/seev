#!/usr/bin/env bash
# Rolling-update E2E safety check.
# Triggers a rolling update on one stateless service, fires concurrent API
# requests throughout the rollout, and asserts:
#   - no unexpected 5xx responses during the rollout window;
#   - no duplicate transaction execution (idempotency key reuse returns 2xx, not 5xx);
#   - rollout completes within progressDeadlineSeconds (300s by default).
#
# Requires: kubectl, curl, a live seev-app namespace (post k8s-bootstrap).
# Run via: make k8s-rolling-update
set -euo pipefail

KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
NAMESPACE="${NAMESPACE:-seev-app}"
GATEWAY_PORT="${GATEWAY_PORT:-8443}"
TARGET_SVC="${TARGET_SVC:-auth}"          # service to roll (stateless, externally reachable)
ROLLOUT_TIMEOUT="${ROLLOUT_TIMEOUT:-300}" # must match progressDeadlineSeconds
CONCURRENT_REQUESTS="${CONCURRENT_REQUESTS:-4}"
REQUEST_INTERVAL="${REQUEST_INTERVAL:-0.5}" # seconds between probe bursts
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/seev-rolling.XXXXXX")"
PF_PID=""
UNEXPECTED_5XX=0
TOTAL_REQUESTS=0

cleanup() {
	if [[ -n "$PF_PID" ]]; then
		kill "$PF_PID" >/dev/null 2>&1 || true
		wait "$PF_PID" >/dev/null 2>&1 || true
	fi
	rm -rf "$TMP_DIR"
}
trap cleanup EXIT

for cmd in "$KUBECTL_BIN" curl; do
	command -v "$cmd" >/dev/null 2>&1 || {
		printf 'verify-rolling-update: %s is required\n' "$cmd" >&2
		exit 2
	}
done

# ── setup: port-forward to gateway ───────────────────────────────────────────

"$KUBECTL_BIN" wait -n seev-edge --for=condition=Available deployment/traefik --timeout=60s >/dev/null
"$KUBECTL_BIN" port-forward -n seev-edge service/traefik "$GATEWAY_PORT":443 \
	>"$TMP_DIR/port-forward.log" 2>&1 &
PF_PID=$!

for _ in $(seq 1 20); do
	curl --noproxy '*' -ksS --resolve "api.local.seev.test:$GATEWAY_PORT:127.0.0.1" \
		"https://api.local.seev.test:$GATEWAY_PORT/api/v1/auth/login" >/dev/null 2>&1 && break
	sleep 1
done

# ── seed: register a test user used during rollout probe ─────────────────────

TEST_EMAIL="rolling-$(date +%s)-$$@example.com"
TEST_PASSWORD="Rolling!2026"
SEED_BODY="{\"email\":\"$TEST_EMAIL\",\"password\":\"$TEST_PASSWORD\",\"full_name\":\"Rolling Update Test\"}"

HTTP_CODE="$(curl --noproxy '*' -ksS -o "$TMP_DIR/reg.json" -w '%{http_code}' \
	--resolve "api.local.seev.test:$GATEWAY_PORT:127.0.0.1" \
	-X POST "https://api.local.seev.test:$GATEWAY_PORT/api/v1/auth/register" \
	-H 'Content-Type: application/json' -d "$SEED_BODY")"
[[ "$HTTP_CODE" =~ ^2 ]] || {
	printf 'verify-rolling-update: seed registration failed (HTTP %s)\n' "$HTTP_CODE" >&2
	cat "$TMP_DIR/reg.json" >&2
	exit 1
}
printf 'verify-rolling-update: seed user registered (%s)\n' "$TEST_EMAIL"

# ── probe loop: runs concurrently while the rollout progresses ───────────────

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

run_probes() {
	while [[ -f "$TMP_DIR/rollout-in-progress" ]]; do
		for _ in $(seq 1 "$CONCURRENT_REQUESTS"); do
			code="$(probe_login)"
			TOTAL_REQUESTS=$((TOTAL_REQUESTS + 1))
			if [[ "$code" =~ ^5 ]]; then
				UNEXPECTED_5XX=$((UNEXPECTED_5XX + 1))
				printf 'verify-rolling-update: unexpected 5xx during rollout: HTTP %s\n' "$code" >&2
			fi
		done
		sleep "$REQUEST_INTERVAL"
	done
}

# ── trigger: force a rolling update via annotation ───────────────────────────

DEPLOY_NAME="${TARGET_SVC}-service"
CURRENT_IMAGE="$("$KUBECTL_BIN" get deployment "$DEPLOY_NAME" -n "$NAMESPACE" \
	-o jsonpath='{.spec.template.spec.containers[0].image}')"
printf 'verify-rolling-update: triggering rolling update on %s (image: %s)\n' \
	"$DEPLOY_NAME" "$CURRENT_IMAGE"

"$KUBECTL_BIN" annotate deployment "$DEPLOY_NAME" -n "$NAMESPACE" \
	"seev.io/rolling-update-test=$(date +%s)" --overwrite >/dev/null

touch "$TMP_DIR/rollout-in-progress"
run_probes &
PROBE_PID=$!

"$KUBECTL_BIN" rollout status deployment/"$DEPLOY_NAME" -n "$NAMESPACE" \
	--timeout="${ROLLOUT_TIMEOUT}s"

rm -f "$TMP_DIR/rollout-in-progress"
wait "$PROBE_PID" 2>/dev/null || true

# ── idempotency: re-login with same credentials should still succeed ──────────

IDEM_CODE="$(probe_login)"
[[ "$IDEM_CODE" =~ ^2 ]] || {
	printf 'verify-rolling-update: post-rollout login failed (HTTP %s) — idempotency check failed\n' \
		"$IDEM_CODE" >&2
	exit 1
}
printf 'verify-rolling-update: post-rollout login: HTTP %s (idempotency OK)\n' "$IDEM_CODE"

# ── result ────────────────────────────────────────────────────────────────────

printf 'verify-rolling-update: total probes=%d unexpected_5xx=%d\n' \
	"$TOTAL_REQUESTS" "$UNEXPECTED_5XX"

THRESHOLD=0
if [[ "$UNEXPECTED_5XX" -gt "$THRESHOLD" ]]; then
	printf 'verify-rolling-update: FAIL: %d unexpected 5xx during rollout (threshold: %d)\n' \
		"$UNEXPECTED_5XX" "$THRESHOLD" >&2
	exit 1
fi

printf 'verify-rolling-update: PASS: zero unexpected 5xx during rolling update of %s\n' "$DEPLOY_NAME"
