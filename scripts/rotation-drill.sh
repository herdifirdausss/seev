#!/usr/bin/env bash
# Certificate rotation drill (docs/plan/49 K9/T6).
#
# A standalone, on-demand proof — NOT a permanent chaos scenario, NOT part
# of `make verify-full` — that cmd/certgen's `rotate` operation is:
#
#   1. zero-downtime: no process restart, no listener rebind, no dropped
#      baseline connections. A poll-based reload (docs/plan/49 K2 —
#      deliberately no fsnotify dependency) can still have a bounded
#      window right after rotation where a BRAND NEW client cert is
#      transiently rejected until this server's own next poll tick — the
#      drill asserts that window is short, bounded, and self-healing, not
#      that literally zero requests can ever land in it.
#   2. an actual rotation: a certificate minted BEFORE the rotation is
#      REJECTED by the server AFTER it (its issuing CA is no longer
#      trusted), not merely "a new cert also happens to work".
#
# Scope: exercises ledger-service's internal HTTPS listener only, not
# every service/gRPC too. pkg/tlsx.CertSource is the one shared object
# every tls.Config in a process (gRPC AND HTTP, server AND client) reads
# from — proving hot-reload here proves the same code path gRPC uses in
# the same process, so duplicating this against every listener/service
# would re-test the identical mechanism, not add new evidence.
#
# Requires: Docker running, this repo checked out, go toolchain available.
# Usage: ./scripts/rotation-drill.sh
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

LIB_LOG_TAG="rotation-drill"
LIB_WORK_DIR_PREFIX="rotation-drill"
# shellcheck source=scripts/lib.sh
source "$ROOT_DIR/scripts/lib.sh"

trap cleanup EXIT

ensure_deps_up
build_server
start_ledger_service

HEALTH_URL="https://localhost:$LEDGER_APP_PORT/health"
LOOP_LOG="$WORK_DIR/rotation-loop.log"

log "starting a continuous mTLS request loop against $HEALTH_URL (every 0.2s)..."
(
	# probe echoes curl's http_code, defaulting to "000" on any transport
	# failure. Written as `if ! code=$(...)` deliberately: under this
	# script's `set -e`, a bare `code=$(cmd)` with a failing cmd would kill
	# this whole background loop the instant curl first fails (e.g. right
	# when the CA rotates) — the `if !` form is one of the contexts set -e
	# exempts. A trailing `|| echo "000"` inside the substitution would
	# avoid that death but, since curl's own -w ALSO prints "000" on
	# failure before exiting non-zero, the two "000"s would concatenate on
	# one stdout into "000000" instead of one replacing the other.
	probe() {
		local out
		if ! out=$(curl_internal -s -o /dev/null -w '%{http_code}' --max-time 2 "$HEALTH_URL" 2>/dev/null); then
			out="000"
		fi
		printf '%s' "$out"
	}
	while true; do
		ts=$(date +%s.%N)
		code=$(probe)
		if [ "$code" != "200" ]; then
			# One immediate retry: a request that lands in the sub-millisecond
			# window while certgen is mid-rewrite of a cert file on disk is a
			# read race in THIS TEST CLIENT re-reading $CERT_DIR live, not
			# evidence of server-side downtime — retrying a moment later tells
			# the two apart instead of misreporting a harness artifact as a
			# rotation failure.
			code=$(probe)
		fi
		echo "$ts $code"
		sleep 0.2
	done
) >"$LOOP_LOG" 2>&1 &
LOOP_PID=$!

sleep 2
log "loop warmed up, snapshotting PRE-rotation dev-operator identity for the 'old cert rejected' check..."
OLD_CERT_DIR="$WORK_DIR/old-certs"
mkdir -p "$OLD_CERT_DIR"
cp "$CERT_DIR/dev-operator.pem" "$CERT_DIR/dev-operator-key.pem" "$CERT_DIR/ca.pem" "$OLD_CERT_DIR/"

log "rotating CA + reissuing every known-service leaf (certgen rotate)..."
ROTATE_TS=$(date +%s.%N)
"$CERTGEN_BIN" rotate --out "$CERT_DIR"

# GRACE_SECONDS bounds how long a request MAY transiently fail after
# rotation: pkg/tlsx polls for changes rather than reacting instantly
# (docs/plan/49 K2 — deliberately no fsnotify dependency), so a brand new
# client cert reissued by rotate can be rejected until this server's OWN
# CertSource next polls and reloads its CAPool. "Zero-downtime" here means
# no restart and no dropped EXISTING connection — not that literally zero
# requests can ever land in that bounded reload window, which no
# poll-based (as opposed to push-based) reload design can promise. 2x the
# 5s default poll interval is a generous bound; a real failure pattern
# would be sustained past it, not self-heal within it.
GRACE_SECONDS=10
log "letting the loop keep running through pkg/tlsx's poll-based hot-reload (grace window ${GRACE_SECONDS}s, waiting 20s total)..."
sleep 20

kill "$LOOP_PID" 2>/dev/null || true
wait "$LOOP_PID" 2>/dev/null || true

TOTAL=$(wc -l <"$LOOP_LOG" | tr -d ' ')
FAILCOUNT=$(grep -vc ' 200$' "$LOOP_LOG" || true)
log "continuous request loop: $TOTAL requests spanning the rotation window, $FAILCOUNT failed after retry"

# Three properties, not just a raw fail count:
#   1. no failure before rotation started (clean baseline)
#   2. every failure lands within GRACE_SECONDS after rotation (bounded)
#   3. the loop's tail (after the grace window) is a clean, sustained run
#      of successes (actually self-healed, not still failing when the
#      loop happened to end)
BASELINE_FAILS=$(awk -v rt="$ROTATE_TS" '$2!="200" && $1<rt' "$LOOP_LOG" | wc -l | tr -d ' ')
LATE_FAILS=$(awk -v rt="$ROTATE_TS" -v g="$GRACE_SECONDS" '$2!="200" && $1>rt+g' "$LOOP_LOG" | wc -l | tr -d ' ')
TAIL_TOTAL=$(awk -v rt="$ROTATE_TS" -v g="$GRACE_SECONDS" '$1>rt+g' "$LOOP_LOG" | wc -l | tr -d ' ')

if [ "$BASELINE_FAILS" -ne 0 ]; then
	fail "$BASELINE_FAILS request(s) failed BEFORE rotation even started — not a clean baseline, see $LOOP_LOG"
elif [ "$LATE_FAILS" -ne 0 ]; then
	fail "$LATE_FAILS request(s) still failing more than ${GRACE_SECONDS}s after rotation — not self-healing, see $LOOP_LOG"
elif [ "$TAIL_TOTAL" -eq 0 ]; then
	fail "no requests landed after the ${GRACE_SECONDS}s grace window — can't confirm the tail actually self-healed, see $LOOP_LOG"
else
	ok "zero-downtime proven: no dropped baseline requests, all $FAILCOUNT/$TOTAL transient failures landed within the ${GRACE_SECONDS}s poll-reload grace window, and the remaining $TAIL_TOTAL requests after it all succeeded — the server never restarted and self-healed without operator action"
fi

log "confirming a fresh request with the newly-issued dev-operator cert still succeeds..."
if ! NEW_CODE=$(curl_internal -s -o /dev/null -w '%{http_code}' --max-time 5 "$HEALTH_URL"); then
	NEW_CODE="000"
fi
if [ "$NEW_CODE" = "200" ]; then
	ok "post-rotation request with the NEW dev-operator cert succeeded (HTTP $NEW_CODE)"
else
	fail "post-rotation request with the NEW cert failed (HTTP $NEW_CODE)"
fi

log "confirming the OLD (pre-rotation) dev-operator cert is now REJECTED..."
set +e
OLD_STDERR="$WORK_DIR/old-cert-attempt.stderr"
curl -k --cacert "$OLD_CERT_DIR/ca.pem" --cert "$OLD_CERT_DIR/dev-operator.pem" --key "$OLD_CERT_DIR/dev-operator-key.pem" \
	-s -o /dev/null --max-time 5 "$HEALTH_URL" 2>"$OLD_STDERR"
OLD_CURL_EXIT=$?
set -e
if [ "$OLD_CURL_EXIT" -ne 0 ]; then
	ok "old cert correctly REJECTED — TLS handshake failed (curl exit $OLD_CURL_EXIT): $(tail -1 "$OLD_STDERR")"
else
	fail "old cert was NOT rejected — the pre-rotation cert/CA still authenticated successfully against $HEALTH_URL"
fi

log "rotation drill complete"
exit "$FAILED"
