#!/usr/bin/env bash
# Chaos test suite for the seev ledger (docs/plan/12 Task T7).
#
# Empirical proof — not a design claim — that no money is lost when the
# server process or a dependency dies mid-flight. Each scenario asserts
# against fn_verify_ledger_balance()/v_account_balance_audit via psql, not
# just "it looked fine" observation.
#
# Requires: Docker running, this repo checked out, go toolchain available.
# Does NOT require the app to already be running — this script builds and
# manages its own server process(es) and docker-compose dependencies.
#
# Usage:
#   ./scripts/chaos-test.sh 1        # kill -9 mid-posting
#   ./scripts/chaos-test.sh 2        # broker (RabbitMQ) down
#   ./scripts/chaos-test.sh 3        # Postgres restart mid-traffic
#   ./scripts/chaos-test.sh 4        # Redis down
#   ./scripts/chaos-test.sh 5        # payout crash-mid-flight (docs/plan/23 Task T6)
#   ./scripts/chaos-test.sh 6        # payin down -> webhook 503 -> redelivery heals
#   ./scripts/chaos-test.sh 7        # fraud down fail-open + block-mode E2E
#   ./scripts/chaos-test.sh 8        # vendor failover: force-fail -> failover/pin -> recovery (docs/plan/40 Task T6)
#   ./scripts/chaos-test.sh 9        # Redis outage: selective hot-swap + fraud fail-closed (docs/plan/45 Task T4)
#   ./scripts/chaos-test.sh 10       # distributed breaker across two payout replicas (docs/plan/45 Task T4)
#   ./scripts/chaos-test.sh 11       # payout crash after command enqueue / after network timeout (docs/plan/45 Task T4)
#   ./scripts/chaos-test.sh all      # run all eleven in sequence
#
# Each scenario is independent and re-runs migrations against a throwaway
# schema state (it does NOT reset the docker volumes — accounts/balances
# accumulate across runs in the same way any real system would; assertions
# are always relative, never "balance must equal exactly X" in absolute
# terms unless the scenario provisions fresh accounts).
#
# Shared bootstrap (docker-compose up, migrations, server build/start/kill,
# token generation, ledger integrity assertions) lives in scripts/lib.sh —
# scripts/smoke-test.sh uses the exact same helpers, so a fix to how the
# stack is bootstrapped only needs to happen once.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

LIB_LOG_TAG="chaos"
LIB_WORK_DIR_PREFIX="chaos"
# shellcheck source=scripts/lib.sh
source "$ROOT_DIR/scripts/lib.sh"

trap cleanup EXIT

# ─── Scenario 1: kill -9 mid-posting ────────────────────────────────────────

scenario_1() {
	log "=== Scenario 1: kill -9 mid-posting ==="
	ensure_deps_up
	build_server
	start_server

	local user_id="c1a05000-0000-0000-0000-000000000001"
	local recipient_id="c1a05000-0000-0000-0000-0000000000f1"
	provision_user "$user_id" >/dev/null
	provision_user "$recipient_id" >/dev/null
	fund_user "$user_id"
	local token
	token="$(gen_token "$user_id")"

	log "firing 40 concurrent transfer_p2p requests, killing the server -9 partway through..."
	local results_file="$WORK_DIR/results.txt"
	: >"$results_file"

	for i in $(seq 1 40); do
		(
			code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
				-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
				-d "{\"idempotency_key\":\"chaos1-$i\",\"type\":\"transfer_p2p\",\"amount\":\"1\",\"target_user_id\":\"$recipient_id\"}" 2>/dev/null || echo "000")
			echo "chaos1-$i $code" >>"$results_file"
		) &
		if [ "$i" -eq 20 ]; then
			sleep 0.05
			kill_server_hard
		fi
	done
	wait

	log "restarting gateway and retrying any requests that didn't get a clean response..."
	# Only gateway was killed above (kill_server_hard is gateway-only) — the
	# other five processes never stopped. Calling start_server/start_services
	# here would try to rebind their still-held ports, fail immediately, and
	# silently overwrite their PID files with the new (already-dead) pids —
	# every later stop/kill in this run would then target that dead pid while
	# the real scenario-1-era process keeps running forever, invisible to the
	# rest of the suite. Real bug found reproducing docs/plan/34 T2 scenario
	# 5/6/7 failures that only appeared inside `all`: the resume-job/webhook/
	# fraud-hook checks in later scenarios were unknowingly talking to this
	# orphaned scenario-1 ledger/payin/fraud-service the entire time.
	build_server # rebuild not strictly needed but keeps binary path consistent
	start_gateway

	# Retry every request that did NOT get a 2xx the first time — same
	# idempotency key, so a request that DID post server-side before the
	# kill is a safe no-op retry (this IS the point of idempotency).
	while read -r key code; do
		if [ "${code:0:1}" != "2" ]; then
			retry_code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
				-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
				-d "{\"idempotency_key\":\"$key\",\"type\":\"transfer_p2p\",\"amount\":\"1\",\"target_user_id\":\"$recipient_id\"}" 2>/dev/null || echo "000")
			log "retried $key: original=$code retry=$retry_code"
		fi
	done <"$results_file"

	sleep 1
	assert_ledger_balanced
	assert_no_inconsistent_projections
	assert_no_stuck_pending_transactions

	stop_server_gracefully
}

# ─── Scenario 2: broker (RabbitMQ) down ─────────────────────────────────────

scenario_2() {
	log "=== Scenario 2: broker down, posting must still succeed ==="
	ensure_deps_up
	build_server
	start_server

	local user_id="c2a05000-0000-0000-0000-000000000002"
	local recipient_id="c2a05000-0000-0000-0000-0000000000f2"
	provision_user "$user_id" >/dev/null
	provision_user "$recipient_id" >/dev/null
	fund_user "$user_id"
	local token
	token="$(gen_token "$user_id")"

	log "stopping rabbitmq..."
	docker stop "$RABBITMQ_CONTAINER" >/dev/null

	log "posting 10 transactions while broker is down (must all succeed — outbox decouples posting from publish)..."
	local all_2xx=1
	for i in $(seq 1 10); do
		code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
			-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
			-d "{\"idempotency_key\":\"chaos2-$i\",\"type\":\"transfer_p2p\",\"amount\":\"1\",\"target_user_id\":\"$recipient_id\"}" 2>/dev/null || echo "000")
		if [ "${code:0:1}" != "2" ]; then
			all_2xx=0
			log "  request $i got $code (expected 2xx)"
		fi
	done
	if [ "$all_2xx" = "1" ]; then
		ok "all 10 postings succeeded with the broker down"
	else
		fail "some postings failed while the broker was down — outbox pattern not decoupling correctly"
	fi

	log "restarting rabbitmq and waiting for the relay to drain the outbox..."
	docker start "$RABBITMQ_CONTAINER" >/dev/null
	wait_for_container_healthy "$RABBITMQ_CONTAINER"

	local tries=24 pending_or_failed
	while [ "$tries" -gt 0 ]; do
		pending_or_failed="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT count(*) FROM outbox_events WHERE status IN ('pending','failed','processing');" | tr -d '[:space:]')"
		[ "$pending_or_failed" = "0" ] && break
		sleep 5
		tries=$((tries - 1))
	done

	local dead
	dead="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT count(*) FROM outbox_events WHERE status = 'dead';" | tr -d '[:space:]')"
	if [ "$pending_or_failed" = "0" ] && [ "$dead" = "0" ]; then
		ok "all outbox events reached 'published' after the broker recovered (none dead)"
	else
		fail "outbox did not fully drain: pending/failed=$pending_or_failed dead=$dead"
		psql_exec "$LEDGER_DB_NAME" -c "SELECT id, event_type, status, retry_count FROM outbox_events WHERE status != 'published';"
	fi

	# The drained events include 10 transfer_p2p postings — once the relay
	# has caught up, gateway's notify consumer must eventually see them too
	# (docs/plan/34 Task T2: "restart -> relay drain -> notifikasi sampai").
	# This is the same outbox->RabbitMQ->consumer path as the happy-path
	# business-e2e journey, just proven under a broker outage instead of a
	# clean run.
	await_notification "$token" "Transfer terkirim" || true

	assert_ledger_balanced
	assert_no_inconsistent_projections
	stop_server_gracefully
}

# ─── Scenario 3: Postgres restart mid-traffic ───────────────────────────────

scenario_3() {
	log "=== Scenario 3: Postgres restart mid-traffic ==="
	ensure_deps_up
	build_server
	start_server

	local user_id="c3a05000-0000-0000-0000-000000000003"
	local recipient_id="c3a05000-0000-0000-0000-0000000000f3"
	provision_user "$user_id" >/dev/null
	provision_user "$recipient_id" >/dev/null
	fund_user "$user_id"
	local token
	token="$(gen_token "$user_id")"

	log "firing traffic and restarting postgres mid-flight (bounded by POSTGRES_STATEMENT_TIMEOUT/POSTGRES_LOCK_TIMEOUT — requests must fail with a clear error, not hang)..."
	local during_restart_log="$WORK_DIR/during_restart.txt"
	: >"$during_restart_log"
	for i in $(seq 1 20); do
		(
			start=$(date +%s)
			code=$(curl -s --max-time 20 -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
				-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
				-d "{\"idempotency_key\":\"chaos3-$i\",\"type\":\"transfer_p2p\",\"amount\":\"1\",\"target_user_id\":\"$recipient_id\"}" 2>/dev/null || echo "000")
			elapsed=$(( $(date +%s) - start ))
			echo "chaos3-$i code=$code elapsed=${elapsed}s" >>"$during_restart_log"
		) &
		if [ "$i" -eq 10 ]; then
			sleep 0.1
			docker restart "$POSTGRES_CONTAINER" >/dev/null &
		fi
	done
	wait

	log "requests during restart:"
	cat "$during_restart_log"
	local hung
	hung=$(awk -F'elapsed=' '{gsub("s","",$2); if ($2+0 >= 20) print}' "$during_restart_log" | wc -l | tr -d '[:space:]')
	if [ "$hung" = "0" ]; then
		ok "no request hung past its client timeout during the Postgres restart"
	else
		fail "$hung request(s) took the full 20s timeout — may indicate a hang rather than a bounded failure"
	fi

	wait_for_container_healthy "$POSTGRES_CONTAINER"
	log "posting after Postgres has recovered..."
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"chaos3-post-recovery\",\"type\":\"transfer_p2p\",\"amount\":\"1\",\"target_user_id\":\"$recipient_id\"}" 2>/dev/null || echo "000")
	if [ "${code:0:1}" = "2" ]; then
		ok "posting succeeds again after Postgres recovered (code=$code)"
	else
		fail "posting after recovery got $code, expected 2xx"
	fi

	assert_ledger_balanced
	assert_no_inconsistent_projections
	assert_no_stuck_pending_transactions
	stop_server_gracefully
}

# ─── Scenario 4: Redis down ──────────────────────────────────────────────────

scenario_4() {
	log "=== Scenario 4: Redis down, then restarted with REDIS_ENABLED=false ==="
	log "NOTE (docs/plan/12 Task T7, superseded in part by docs/plan/45 Task T3):"
	log "this scenario only proves the OPERATOR-DRIVEN mitigation path — restarting"
	log "with REDIS_ENABLED=false to force the in-memory lock/limiter fallback."
	log "The rate limiter and policy counter no longer NEED that restart: since"
	log "docs/plan/45 T3, cache.FailoverLimiter/FailoverCounter hot-swap to memory"
	log "on Redis's first real-operation failure and recover automatically once"
	log "Redis is healthy again, WITHOUT any restart — see scenario 9, which is the"
	log "scenario that actually proves the hot-swap. Scheduler lock is unchanged by"
	log "T3 (still skip-tick, by design — docs/plan/45 K4 explicitly keeps memory"
	log "fallback OUT of multi-replica scheduler locking)."
	ensure_deps_up
	build_server
	start_server

	local user_id="c4a05000-0000-0000-0000-000000000004"
	local recipient_id="c4a05000-0000-0000-0000-0000000000f4"
	provision_user "$user_id" >/dev/null
	provision_user "$recipient_id" >/dev/null
	fund_user "$user_id"
	local token
	token="$(gen_token "$user_id")"

	log "stopping redis..."
	docker stop "$REDIS_CONTAINER" >/dev/null

	log "posting while redis is down — rate limiter must fail OPEN (traffic still served)..."
	local code
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"chaos4-1\",\"type\":\"transfer_p2p\",\"amount\":\"1\",\"target_user_id\":\"$recipient_id\"}" 2>/dev/null || echo "000")
	if [ "${code:0:1}" = "2" ]; then
		ok "traffic still served with Redis down (fail-open, code=$code)"
	else
		fail "traffic rejected while Redis was down, got $code — rate limiter must fail open"
	fi

	log "restarting the server process with REDIS_ENABLED=false (the operator's actual mitigation for a known outage — picks up the in-memory lock/limiter fallback fresh; REDIS_ENABLED=true, the default, is a required-dependency fail-fast per docs/plan/12 T1 and would refuse to start against an unreachable Redis)..."
	stop_server_gracefully
	# fraud-service is excluded here: it CAN start with Redis down (since
	# docs/plan/45 Task T3, cmd/fraud-service/main.go uses
	# cache.NewClientWithoutPing instead of an eager-ping constructor,
	# specifically so fraud-service boots and runs fail-closed rather than
	# refusing to start — see scenario 9), but this scenario simply doesn't
	# need it up: ledger's screening hook fails open when fraud-service is
	# unreachable at all (proven by scenario 7), so the remaining traffic
	# check below doesn't depend on fraud-service either way.
	REDIS_ENABLED=false start_ledger_service
	REDIS_ENABLED=false start_auth_service
	REDIS_ENABLED=false start_payin_service
	REDIS_ENABLED=false start_payout_service
	REDIS_ENABLED=false start_gateway

	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"chaos4-2\",\"type\":\"transfer_p2p\",\"amount\":\"1\",\"target_user_id\":\"$recipient_id\"}" 2>/dev/null || echo "000")
	if [ "${code:0:1}" = "2" ]; then
		ok "server still serves traffic after restart with Redis still down (code=$code)"
	else
		fail "server failed to serve traffic after restart with Redis down, got $code"
	fi

	docker start "$REDIS_CONTAINER" >/dev/null
	wait_for_container_healthy "$REDIS_CONTAINER"

	assert_ledger_balanced
	assert_no_inconsistent_projections
	stop_server_gracefully
}

# ─── Scenario 5: payout crash mid-flight (docs/plan/23 Task T6) ─────────────
#
# Four kill points, one per non-terminal payout_requests status:
#   - 'created' / 'held': reached by seeding the row directly via SQL rather
#     than catching a live process at that exact microsecond (the window
#     between repo.Insert committing and hold()'s first ledger.Post, or
#     between hold() returning and EnqueueInitialSubmit, is sub-millisecond
#     — not something `kill -9` fired from a separate bash process after a
#     `sleep` can hit deterministically). This tests the SAME resume code
#     path (ResumeStuck -> hold()/enqueueSubmit()) a request found in that
#     status would hit regardless of how it got there, which is what
#     correctness actually depends on — the row's origin (crash vs. direct
#     seed) is invisible to the recovery logic.
#   - 'submitted': reached via a REAL POST /api/v1/payout with
#     mock_mode=timeout — hold() succeeds, EnqueueInitialSubmit succeeds
#     (status='submitted', a 'pending' command durably enqueued), then the
#     relay's own async dispatch (docs/plan/45 Task T1 — provider.Submit is
#     never called anywhere else) genuinely errors (infra failure), leaving
#     the command 'failed'/backing-off exactly the way a real vendor timeout
#     would. wait_for_vendor_call/wait_for_vendor_command_status prove the
#     relay actually attempted and failed before the seeded destination is
#     rewritten to drop mock_mode — simulating the transient vendor outage
#     having cleared by the time the relay retries, the same way scenario
#     2/3 restart rabbitmq/postgres to simulate recovery of a real
#     dependency. The command's own next_attempt_at is also reset so the
#     relay's retry-poll (every 30s) doesn't have to wait out its natural
#     exponential backoff on top of the service restart.
#   - 'vendor_pending': reached via a REAL POST with mock_mode=async — the
#     vendor's own in-memory Pending cache entry only exists because a real
#     Submit call populated it (via the relay's async dispatch, awaited by
#     wait_for_payout_status); there is deliberately no HTTP-reachable way
#     to force mockvendor to resolve a Pending payout from outside the
#     process (CompletePending is a Go-only test method — see
#     internal/payout/payout_integration_test.go's
#     TestPayout_Create_Async_ResumeJobSettles for that side of the proof),
#     so THIS kill point proves resume polls it correctly (Query is called,
#     no money moves twice, nothing is silently dropped) rather than forcing
#     a terminal state — vendor_pending is legitimately allowed to still be
#     pending after one resume pass.
#
# In every case updated_at is backdated via SQL immediately after reaching
# the target status so the very next resume-job cron tick (<=60s after
# restart) already treats the row as stale enough to act on, instead of
# waiting out the job's own 1-minute staleness threshold on top of that.

backdate_payout() {
	psql_exec "$PAYOUT_DB_NAME" -c "UPDATE payout_requests SET updated_at = now() - interval '2 minutes' WHERE id = '$1';" >/dev/null
}

scenario_5() {
	log "=== Scenario 5: payout crash-mid-flight (docs/plan/30 Task T6) ==="
	ensure_deps_up
	build_server
	start_services

	local user_id
	user_id="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT gen_random_uuid();")"
	provision_user "$user_id" >/dev/null
	provision_hold_account "$user_id"
	fund_user "$user_id"
	local token
	token="$(gen_token "$user_id")"
	local cash_account
	cash_account="$(cash_account_id "$user_id")"
	local balance_before
	balance_before="$(account_balance "$cash_account")"

	# ── Kill point 1: 'created' — seeded, no ledger activity yet ──────────────
	log "--- kill point 1/4: created ---"
	local id_created
	id_created="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT gen_random_uuid();")"
	psql_exec "$PAYOUT_DB_NAME" -c "
		INSERT INTO payout_requests (id, user_id, amount, currency, vendor, destination, status, created_by, created_at, updated_at)
		VALUES ('$id_created', '$user_id', 10000, 'IDR', 'mockvendor', '{\"bank_code\":\"014\",\"account_no\":\"1\"}'::jsonb, 'created', 'chaos_test', now(), now());" >/dev/null
	backdate_payout "$id_created"
	kill_payout_hard
	start_payout_service

	# ── Kill point 2: 'held' — real hold_tx_id via a genuine withdraw_initiate ──
	log "--- kill point 2/4: held ---"
	local held_key="chaos5-held-$RANDOM"
	curl -s -o /dev/null -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"$held_key\",\"type\":\"withdraw_initiate\",\"amount\":\"10000\"}"
	local held_tx_id
	held_tx_id="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT id FROM ledger_transactions WHERE idempotency_key = '$held_key';")"
	local id_held
	id_held="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT gen_random_uuid();")"
	psql_exec "$PAYOUT_DB_NAME" -c "
		INSERT INTO payout_requests (id, user_id, amount, currency, vendor, destination, status, hold_tx_id, created_by, created_at, updated_at)
		VALUES ('$id_held', '$user_id', 10000, 'IDR', 'mockvendor', '{\"bank_code\":\"014\",\"account_no\":\"1\"}'::jsonb, 'held', '$held_tx_id', 'chaos_test', now(), now());" >/dev/null
	backdate_payout "$id_held"
	kill_payout_hard
	start_payout_service

	# ── Kill point 3: 'submitted' — real infra failure (mock_mode=timeout) ────
	log "--- kill point 3/4: submitted ---"
	local id_submitted
	id_submitted="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/payout" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d '{"amount":"10000","destination":{"bank_code":"014","account_no":"1","mock_mode":"timeout"}}' \
		| sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
	if [ -z "$id_submitted" ]; then
		fail "kill point 3: create request did not return an id"
	fi
	wait_for_payout_status "$id_submitted" "submitted" 10
	# docs/plan/45 Task T1: dispatch is async now (the relay's own ~1s poll
	# interval) — 'submitted' lands the instant Create enqueues, before the
	# relay has necessarily even attempted the vendor call yet. Wait for the
	# durable evidence that the vendor was ACTUALLY called and genuinely
	# timed out (an 'uncertain' payout_vendor_calls row) before rewriting
	# the mock destination, or this would race the relay's own first
	# dispatch attempt and could rewrite mock_mode away before it ever saw
	# the timeout at all.
	wait_for_vendor_call "$id_submitted" "uncertain" 15
	wait_for_vendor_command_status "$id_submitted" "failed" 10
	# Simulate the vendor outage clearing before resume retries — same role
	# as scenario 2/3 restarting rabbitmq/postgres.
	psql_exec "$PAYOUT_DB_NAME" -c "UPDATE payout_requests SET destination = '{\"bank_code\":\"014\",\"account_no\":\"1\"}'::jsonb WHERE id = '$id_submitted';" >/dev/null
	backdate_payout "$id_submitted"
	psql_exec "$PAYOUT_DB_NAME" -c "UPDATE payout_vendor_commands SET next_attempt_at = now() WHERE payout_request_id = '$id_submitted';" >/dev/null
	kill_payout_hard
	start_payout_service

	# ── Kill point 4: 'vendor_pending' — real async Submit, never completed ───
	log "--- kill point 4/4: vendor_pending ---"
	local id_pending
	id_pending="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/payout" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d '{"amount":"10000","destination":{"bank_code":"014","account_no":"1","mock_mode":"async"}}' \
		| sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
	if [ -z "$id_pending" ]; then
		fail "kill point 4: create request did not return an id"
	fi
	wait_for_payout_status "$id_pending" "vendor_pending" 10
	backdate_payout "$id_pending"
	kill_payout_hard
	start_payout_service
	# Simulate the external vendor having completed while payout-service was
	# down: forced back to 'submitted' with no live command (the original
	# command already completed toward vendor_pending, so it's neither live
	# nor dead) — docs/plan/45 Task T1's resume job inserts a fresh command
	# for this genuine gap (HasDeadCommand is false — the most recent
	# command 'completed', it didn't dead-letter), and the relay's own next
	# dispatch pass reaches a terminal state idempotently.
	psql_exec "$PAYOUT_DB_NAME" -c "UPDATE payout_requests SET status='submitted', destination = '{\"bank_code\":\"014\",\"account_no\":\"1\"}'::jsonb, updated_at=now()-interval '2 minutes' WHERE id='$id_pending';" >/dev/null

	# ── Ledger crash between a completed hold and settle ───────────────────
	log "--- ledger kill point: after hold, before settle ---"
	local ledger_key="chaos5-ledger-$RANDOM" ledger_hold_tx id_ledger
	curl -s -o /dev/null -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/transactions" -H "Authorization: Bearer $token" -H "Content-Type: application/json" -d "{\"idempotency_key\":\"$ledger_key\",\"type\":\"withdraw_initiate\",\"amount\":\"10000\"}"
	ledger_hold_tx="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT id FROM ledger_transactions WHERE idempotency_key='$ledger_key';")"
	id_ledger="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT gen_random_uuid();")"
	psql_exec "$PAYOUT_DB_NAME" -c "INSERT INTO payout_requests (id,user_id,amount,currency,vendor,destination,status,hold_tx_id,created_by,created_at,updated_at) VALUES ('$id_ledger','$user_id',10000,'IDR','mockvendor','{\"bank_code\":\"014\",\"account_no\":\"1\"}'::jsonb,'held','$ledger_hold_tx','chaos_test',now(),now()-interval '2 minutes');" >/dev/null
	kill_ledger_hard
	log "waiting for payout resume to attempt settle while ledger is unavailable..."
	sleep 65
	local during_ledger_down
	during_ledger_down="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT status FROM payout_requests WHERE id='$id_ledger';")"
	[ "$during_ledger_down" = "submitted" ] && ok "payout remained resumable while ledger was down" || fail "ledger-down payout status was '$during_ledger_down', expected submitted"
	start_ledger_service
	# Every resumable row may have recorded a fresh error timestamp while ledger
	# was unavailable. Make all rows from this isolated run eligible immediately.
	psql_exec "$PAYOUT_DB_NAME" -c "UPDATE payout_requests SET updated_at=now()-interval '2 minutes' WHERE user_id='$user_id' AND status IN ('created','held','submitted','vendor_pending');" >/dev/null
	# docs/plan/45 Task T1: any command that failed while ledger was down
	# (e.g. kill point 2/3's dispatch reaching the vendor successfully but
	# then failing to settle) is now backing off on its own exponential
	# schedule (up to ~15m at higher retry counts) — reset it too, or the
	# relay's retry-poll (every 30s) won't consider it eligible again until
	# that backoff naturally elapses, well past this scenario's own wait.
	psql_exec "$PAYOUT_DB_NAME" -c "UPDATE payout_vendor_commands SET next_attempt_at = now() WHERE payout_request_id IN (SELECT id FROM payout_requests WHERE user_id='$user_id' AND status IN ('created','held','submitted','vendor_pending'));" >/dev/null

	log "waiting for the resume job's cron tick (every 1 minute) to pick up all four requests..."
	sleep 65

	log "asserting terminal states for created/held/submitted, and correct polling for vendor_pending..."
	local final_created final_held final_submitted final_pending
	final_created="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT status FROM payout_requests WHERE id = '$id_created';")"
	final_held="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT status FROM payout_requests WHERE id = '$id_held';")"
	final_submitted="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT status FROM payout_requests WHERE id = '$id_submitted';")"
	final_pending="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT status FROM payout_requests WHERE id = '$id_pending';")"

	[ "$final_created" = "settled" ] && ok "kill point 1 (created) resolved to 'settled'" || fail "kill point 1 (created) ended at '$final_created', expected 'settled'"
	[ "$final_held" = "settled" ] && ok "kill point 2 (held) resolved to 'settled'" || fail "kill point 2 (held) ended at '$final_held', expected 'settled'"
	[ "$final_submitted" = "settled" ] && ok "kill point 3 (submitted) resolved to 'settled' after simulated vendor recovery" || fail "kill point 3 (submitted) ended at '$final_submitted', expected 'settled'"
	[ "$final_pending" = "settled" ] && ok "kill point 4 (vendor_pending) recovered to settled" || fail "kill point 4 ended at '$final_pending', expected settled"
	local final_ledger
	final_ledger="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT status FROM payout_requests WHERE id='$id_ledger';")"
	[ "$final_ledger" = "settled" ] && ok "ledger crash point recovered to settled" || fail "ledger crash point ended at '$final_ledger', expected settled"

	local no_stuck
	no_stuck="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT count(*) FROM payout_requests WHERE user_id = '$user_id' AND status IN ('created','held','submitted','vendor_pending');")"
	if [ "$no_stuck" = "0" ]; then
		ok "no payout request left stuck in created/held/submitted after resume"
	else
		fail "$no_stuck payout request(s) still stuck in a non-terminal, non-vendor_pending status"
	fi

	# All four payout-service kill points and the ledger-service kill point
	# settle exactly once, so cash must be down by exactly 50000.
	local balance_after
	balance_after="$(account_balance "$cash_account")"
	local expected_after=$((balance_before - 50000))
	if [ "$balance_after" = "$expected_after" ]; then
		ok "cash balance moved by exactly the expected amount (before=$balance_before after=$balance_after)"
	else
		fail "cash balance mismatch: before=$balance_before after=$balance_after expected=$expected_after — money lost or duplicated"
	fi

	assert_ledger_balanced
	assert_no_inconsistent_projections
	stop_services
}

# ─── Scenario 6: payin-service unavailable during webhook ──────────────────

scenario_6() {
	log "=== Scenario 6: payin-service down -> 503 -> redelivery heals ==="
	ensure_deps_up
	build_server
	start_services

	local user_id="c6a05000-0000-0000-0000-000000000006"
	provision_user "$user_id" >/dev/null
	local cash before after body sig code
	cash="$(cash_account_id "$user_id")"
	before="$(account_balance "$cash")"
	body="{\"event_id\":\"chaos6-$RANDOM\",\"external_ref\":\"chaos6-ref\",\"user_id\":\"$user_id\",\"amount\":\"6000\",\"currency\":\"IDR\",\"occurred_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"payment.settled\"}"
	sig="$(printf '%s' "$body" | openssl dgst -sha256 -hmac 'script-test-mockvendor-secret-at-least-32-chars-long' -r | awk '{print $1}')"

	kill_payin_hard
	code=$(curl -s --max-time 10 -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/webhooks/mockvendor" -H "X-Mock-Signature: $sig" -H "Content-Type: application/json" -d "$body" || echo "000")
	[ "$code" = "503" ] && ok "gateway returned 503 while payin-service was down" || fail "gateway returned $code while payin-service was down, expected 503"

	start_payin_service
	# A real vendor's webhook redelivery is not a single immediate retry — it
	# keeps redelivering on a non-2xx response over the following minutes.
	# Model that here instead of one fixed-timing attempt: gateway's own gRPC
	# client channel to payin-service can still be reconnecting for a moment
	# right after start_payin_service returns (wait_for_service_up only proves
	# payin-service's OWN HTTP health endpoint is up, not that gateway's
	# already-dialed client has finished re-establishing that connection), so
	# the very first redelivery attempt can race that and legitimately see a
	# transient 503 even though the service is genuinely back.
	local redelivery_tries=10
	code="000"
	while [ "$redelivery_tries" -gt 0 ]; do
		code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/webhooks/mockvendor" -H "X-Mock-Signature: $sig" -H "Content-Type: application/json" -d "$body")
		[ "$code" = "200" ] && break
		sleep 1
		redelivery_tries=$((redelivery_tries - 1))
	done
	[ "$code" = "200" ] && ok "vendor redelivery accepted after payin-service restart" || fail "redelivery returned $code, expected 200"
	after="$(account_balance "$cash")"
	[ "$after" = "$((before + 6000))" ] && ok "redelivery credited exactly once ($before -> $after)" || fail "redelivery balance mismatch: before=$before after=$after"
	assert_ledger_balanced
	assert_no_inconsistent_projections
	stop_services
}

# ─── Scenario 7: fraud-service fail-open and block-mode E2E ─────────

scenario_7() {
	log "=== Scenario 7: fraud-service down fails open; block mode rejects pre-write — across all three flows (docs/plan/37 Task T6) ==="
	export SCREENING_MODE=block
	export SCREENING_AMOUNT_THRESHOLD=100
	export SCREENING_VELOCITY_MAX_PER_HOUR=0
	ensure_deps_up
	build_server
	start_services

	local user_id recipient_id token cash before after_fail_open after_block code admin_token event_count
	user_id="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT gen_random_uuid();")"
	recipient_id="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT gen_random_uuid();")"
	provision_user "$user_id" >/dev/null
	provision_user "$recipient_id" >/dev/null
	provision_hold_account "$user_id"
	token="$(gen_token "$user_id")"
	cash="$(cash_account_id "$user_id")"

	# ─── Fraud-service DOWN: all three flows must fail OPEN ─────────────
	kill_fraud_hard
	fund_user "$user_id"
	before="$(account_balance "$cash")"

	log "-- P2P transfer while fraud-service is down --"
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"fraud-down-p2p-$RANDOM\",\"type\":\"transfer_p2p\",\"amount\":\"50\",\"target_user_id\":\"$recipient_id\"}")
	after_fail_open="$(account_balance "$cash")"
	[ "$code" = "201" ] && [ "$after_fail_open" = "$((before - 50))" ] \
		&& ok "P2P transfer posted while fraud-service was down (fail-open)" \
		|| fail "fraud-down P2P transfer code=$code balance=$before->$after_fail_open"
	grep -q 'screening check error, failing open' "$LEDGER_LOG" \
		&& ok "ledger logged the fail-open screening error at ERROR path" \
		|| fail "ledger log did not contain the fail-open screening error"

	log "-- Topup webhook while fraud-service is down --"
	local topup_body topup_sig topup_before topup_after
	topup_before="$after_fail_open"
	topup_body="{\"event_id\":\"chaos7-down-$RANDOM\",\"external_ref\":\"chaos7-down-ref\",\"user_id\":\"$user_id\",\"amount\":\"6000\",\"currency\":\"IDR\",\"occurred_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"payment.settled\"}"
	topup_sig="$(printf '%s' "$topup_body" | openssl dgst -sha256 -hmac 'script-test-mockvendor-secret-at-least-32-chars-long' -r | awk '{print $1}')"
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/webhooks/mockvendor" \
		-H "X-Mock-Signature: $topup_sig" -H "Content-Type: application/json" -d "$topup_body")
	topup_after="$(account_balance "$cash")"
	[ "$code" = "200" ] && [ "$topup_after" = "$((topup_before + 6000))" ] \
		&& ok "topup webhook posted while fraud-service was down (fail-open)" \
		|| fail "fraud-down topup webhook code=$code balance=$topup_before->$topup_after"
	grep -q 'payin: screening check error, failing open' "$PAYIN_LOG" \
		&& ok "payin logged the fail-open screening error at ERROR path" \
		|| fail "payin log did not contain the fail-open screening error"

	log "-- Payout create while fraud-service is down --"
	local payout_resp payout_id
	payout_resp="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/payout" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d '{"amount":"50","destination":{"bank_code":"014","account_no":"1"}}')"
	payout_id="$(echo "$payout_resp" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
	[ -n "$payout_id" ] || fail "fraud-down payout create unexpected response: $payout_resp"
	wait_for_payout_status "$payout_id" "settled" 10
	ok "payout created while fraud-service was down and settled via the relay (fail-open, $payout_id)"
	grep -q 'payout: screening check error, failing open' "$PAYOUT_LOG" \
		&& ok "payout logged the fail-open screening error at ERROR path" \
		|| fail "payout log did not contain the fail-open screening error"

	# ─── Fraud-service back up, in BLOCK mode: all three must reject BEFORE any write ───
	start_fraud_service
	# ledger/payin/payout each hold a long-lived grpc.ClientConn to
	# fraud-service (pkg/grpcx.Dial's lazy-reconnect, intentional per its own
	# comment) established back when THEY started — it does NOT get
	# re-dialed just because fraud-service restarts. When fraud-service was
	# killed above, each of those connections entered grpc-go's own
	# transient-failure/backoff cycle; the very NEXT request against a
	# connection still mid-backoff can get a genuine (if short-lived)
	# "connection refused" even though the new fraud-service process is
	# already listening — reproduced empirically running this scenario
	# standalone, twice, both times on the P2P block-mode call specifically
	# (the first fraud-dependent call issued after the restart). This is
	# expected grpc-go client behavior, not a fraud-service or ledger bug —
	# give the default backoff (~1s, jittered) room to complete before
	# issuing the block-mode assertions below.
	sleep 3
	local velocity_key velocity_tries=30
	velocity_key=""
	while [ "$velocity_tries" -gt 0 ]; do
		velocity_key="$(redis-cli -h localhost -p "$REDIS_HOST_PORT" -n 1 --scan --pattern "fraud:velocity:$user_id:*" | head -1)"
		[ -n "$velocity_key" ] && break
		sleep 0.2
		velocity_tries=$((velocity_tries - 1))
	done
	if [ -n "$velocity_key" ]; then
		ok "posted events populated velocity counter in Redis DB 1"
	else
		fail "posted events did not populate fraud velocity counter"
		tail -40 "$FRAUD_LOG" || true
	fi

	log "-- P2P transfer in block mode (amount >= threshold) --"
	local before_block
	before_block="$(account_balance "$cash")"
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"fraud-block-p2p-$RANDOM\",\"type\":\"transfer_p2p\",\"amount\":\"100\",\"target_user_id\":\"$recipient_id\"}")
	after_block="$(account_balance "$cash")"
	[ "$code" = "422" ] && [ "$after_block" = "$before_block" ] \
		&& ok "block-mode P2P transfer rejected without moving money" \
		|| fail "block-mode P2P transfer code=$code balance=$topup_after->$after_block"
	event_count="$(psql_exec "$FRAUD_DB_NAME" -c "SELECT count(*) FROM screening_events WHERE user_id='$user_id' AND verdict='blocked' AND currency='IDR';")"
	[ "$event_count" -ge "1" ] && ok "blocked P2P screening event persisted in seev_fraud" \
		|| fail "expected at least one blocked fraud event for P2P, found $event_count"

	log "-- Topup webhook in block mode (amount >= threshold) --"
	local block_topup_id block_topup_body block_topup_sig block_topup_status
	block_topup_body="{\"event_id\":\"chaos7-block-$RANDOM\",\"external_ref\":\"chaos7-block-ref\",\"user_id\":\"$user_id\",\"amount\":\"6000\",\"currency\":\"IDR\",\"occurred_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"payment.settled\"}"
	block_topup_id="$(echo "$block_topup_body" | sed -n 's/.*"event_id":"\([^"]*\)".*/\1/p')"
	block_topup_sig="$(printf '%s' "$block_topup_body" | openssl dgst -sha256 -hmac 'script-test-mockvendor-secret-at-least-32-chars-long' -r | awk '{print $1}')"
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/webhooks/mockvendor" \
		-H "X-Mock-Signature: $block_topup_sig" -H "Content-Type: application/json" -d "$block_topup_body")
	local after_block_topup
	after_block_topup="$(account_balance "$cash")"
	[ "$code" = "200" ] && [ "$after_block_topup" = "$after_block" ] \
		&& ok "block-mode topup webhook acked 200 without moving money (non-retriable business decision)" \
		|| fail "block-mode topup webhook code=$code balance=$after_block->$after_block_topup"
	block_topup_status="$(psql_exec "$PAYIN_DB_NAME" -c "SELECT status FROM payin_webhook_events WHERE vendor_event_id='$block_topup_id';")"
	[ "$block_topup_status" = "blocked" ] && ok "blocked topup event persisted with status='blocked' in seev_payin" \
		|| fail "expected payin_webhook_events.status='blocked', got '$block_topup_status'"

	log "-- Payout create in block mode (amount >= threshold) --"
	local payout_count_before payout_count_after block_payout_resp block_payout_code
	payout_count_before="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT count(*) FROM payout_requests WHERE user_id='$user_id';")"
	block_payout_resp="$(curl -s -w '\nHTTP_CODE:%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/payout" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d '{"amount":"6000","destination":{"bank_code":"014","account_no":"1"}}')"
	block_payout_code="$(echo "$block_payout_resp" | grep -o 'HTTP_CODE:[0-9]*' | cut -d: -f2)"
	payout_count_after="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT count(*) FROM payout_requests WHERE user_id='$user_id';")"
	[ "$block_payout_code" = "422" ] && echo "$block_payout_resp" | grep -q "SCREENING_BLOCKED" \
		&& ok "block-mode payout create rejected with 422 SCREENING_BLOCKED" \
		|| fail "block-mode payout create code=$block_payout_code, expected 422 SCREENING_BLOCKED: $block_payout_resp"
	[ "$payout_count_after" = "$payout_count_before" ] && ok "no new payout_requests row was created for the blocked attempt" \
		|| fail "payout_requests row count changed on a blocked attempt: $payout_count_before -> $payout_count_after"

	admin_token="$(gen_token "$(psql_exec "$LEDGER_DB_NAME" -c "SELECT gen_random_uuid();")" admin)"
	code=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:$FRAUD_ADMIN_PORT/api/v1/admin/fraud/events?user_id=$user_id" -H "Authorization: Bearer $admin_token")
	[ "$code" = "200" ] && ok "fraud admin events endpoint serves the moved audit row" || fail "fraud admin events returned $code"
	assert_ledger_balanced
	assert_no_inconsistent_projections
	stop_services
	unset SCREENING_MODE SCREENING_AMOUNT_THRESHOLD SCREENING_VELOCITY_MAX_PER_HOUR
}

# ─── Scenario 8: vendor failover (docs/plan/40 Task T6) ─────────────────────
#
# Proves the anti-double-payout failover contract end to end against real
# running services, not just orchestrate.go's own unit/integration tests:
# a vendor-level fault (force-fail, not a process kill) must (a) route the
# NEXT new payout to the surviving vendor, (b) NEVER fail over an
# already-in-flight payout whose Submit attempt landed 'uncertain' — it
# stays pinned to the original vendor forever, and only the relay's own
# retry (docs/plan/45 Task T1 — ClaimFailedCommandsForRetry, not the resume
# job, which no longer calls the vendor at all) against that SAME vendor can
# ever settle it, and (c) never produce two settles for any payout
# regardless of how many vendors were involved.
#
# BREAKER_FAILURE_THRESHOLD=1 makes the breaker trip on the very first
# force-fail-induced Submit failure — deterministic and fast, instead of
# waiting out the default threshold of 5.
#
# mockvendor2 is seeded at priority 1001 (mockvendor's own seed migration,
# 000002_routing.up.sql, is priority 1000) — deliberately a HIGHER priority
# number than mockvendor's, since ResolveCandidates orders ASC (smallest
# number = tried first, docs/plan/40 Task T2); this makes mockvendor2 a true
# FALLBACK behind mockvendor, not a replacement for it. This differs from
# the doc's own shorthand "priority 2" (which would have made mockvendor2
# tried FIRST, backwards from the scenario's intent) for that reason.
scenario_8() {
	log "=== Scenario 8: vendor failover — force-fail mockvendor, new payout routes to mockvendor2, in-flight payout stays pinned, resume settles it once recovered ==="
	export BREAKER_FAILURE_THRESHOLD=1
	ensure_deps_up
	build_server
	start_services

	local admin_token
	admin_token="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin)"

	log "-- seeding mockvendor2 (gateway + routing rule, priority 1001, global fallback) --"
	curl -s -o /dev/null -X PUT "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/vendor-gateways/mockvendor2" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d '{"gateway":"gopay"}'
	psql_exec "$PAYOUT_DB_NAME" -c "DELETE FROM payout_routing_rules WHERE priority = 1001;" >/dev/null
	local route_json route_id
	route_json="$(curl -s -X POST "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/routing-rules" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" \
		-d '{"flow":"payout","priority":1001,"enabled":true,"vendor":"mockvendor2"}')"
	route_id="$(echo "$route_json" | json_field id)"
	[ -n "$route_id" ] && ok "admin seeded mockvendor2 routing rule ($route_id)" || fail "mockvendor2 routing rule creation failed: $route_json"

	local user_id token
	user_id="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT gen_random_uuid();")"
	provision_user "$user_id" >/dev/null
	provision_hold_account "$user_id"
	fund_user "$user_id" 500000
	token="$(gen_token "$user_id")"
	local cash
	cash="$(cash_account_id "$user_id")"

	log "-- force-failing mockvendor --"
	curl -s -o /dev/null -X POST "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/vendors/mockvendor/force-fail" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d '{"fail":true}'

	log "-- creating payout #1 while mockvendor is force-failed (still the top-priority candidate) --"
	local resp1 id1 vendor1
	resp1="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/payout" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d '{"amount":"10000","destination":{"bank_code":"014","account_no":"1"}}')"
	id1="$(echo "$resp1" | json_field id)"
	[ -n "$id1" ] && ok "payout #1 created ($id1)" || fail "payout #1 create did not return an id: $resp1"

	# docs/plan/45 Task T1: dispatch is async now (the relay's own ~1s poll
	# interval) — Create returns the instant hold+enqueue lands, before the
	# relay has necessarily attempted the vendor call yet. Wait for the
	# durable evidence (the recorded 'uncertain' outcome) before reading
	# status/vendor/breaker state that only exists after that dispatch.
	wait_for_vendor_call "$id1" "uncertain" 15

	vendor1="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT vendor FROM payout_requests WHERE id = '$id1';")"
	local status1
	status1="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT status FROM payout_requests WHERE id = '$id1';")"
	[ "$vendor1" = "mockvendor" ] && [ "$status1" = "submitted" ] \
		&& ok "payout #1 pinned to mockvendor, status='submitted' (uncertain, force-fail is a transport error)" \
		|| fail "payout #1 unexpected vendor='$vendor1' status='$status1', expected vendor=mockvendor status=submitted"

	local outcome1
	outcome1="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT outcome FROM payout_vendor_calls WHERE payout_request_id = '$id1' ORDER BY created_at DESC LIMIT 1;")"
	[ "$outcome1" = "uncertain" ] && ok "payout #1's vendor call recorded outcome='uncertain'" \
		|| fail "payout #1's latest vendor call outcome was '$outcome1', expected 'uncertain'"

	log "-- asserting admin health reports mockvendor open --"
	local health_resp
	health_resp="$(curl -s "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/vendors/health" -H "Authorization: Bearer $admin_token")"
	echo "$health_resp" | grep -q '"vendor":"mockvendor","state":"open"' \
		&& ok "admin vendor health reports mockvendor as open" \
		|| fail "admin vendor health did not report mockvendor open: $health_resp"

	log "-- creating payout #2: routing must skip mockvendor (open) straight to mockvendor2 --"
	local resp2 id2 vendor2 status2
	resp2="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/payout" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d '{"amount":"10000","destination":{"bank_code":"014","account_no":"2"}}')"
	id2="$(echo "$resp2" | json_field id)"
	[ -n "$id2" ] && ok "payout #2 created ($id2)" || fail "payout #2 create did not return an id: $resp2"

	wait_for_payout_status "$id2" "settled" 10
	vendor2="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT vendor FROM payout_requests WHERE id = '$id2';")"
	status2="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT status FROM payout_requests WHERE id = '$id2';")"
	[ "$vendor2" = "mockvendor2" ] && [ "$status2" = "settled" ] \
		&& ok "payout #2 routed straight to mockvendor2 and settled" \
		|| fail "payout #2 unexpected vendor='$vendor2' status='$status2', expected vendor=mockvendor2 status=settled"

	log "-- recovering mockvendor; the relay's own retry must settle payout #1 against the SAME vendor --"
	curl -s -o /dev/null -X POST "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/vendors/mockvendor/force-fail" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d '{"fail":false}'
	backdate_payout "$id1"
	# docs/plan/45 Task T1: payout #1's retry is now the relay's own
	# ClaimFailedCommandsForRetry poll (every 30s), not the resume job's
	# submit-retry — resume no longer calls the vendor at all. The command's
	# exponential backoff (FailCommand) can otherwise take up to ~90s
	# (30s * 2^1 * up to 1.5x jitter) before it's naturally eligible again;
	# resetting next_attempt_at makes it immediately eligible on the relay's
	# very next 30s tick instead of racing that jitter.
	psql_exec "$PAYOUT_DB_NAME" -c "UPDATE payout_vendor_commands SET next_attempt_at = now() WHERE payout_request_id = '$id1';" >/dev/null

	log "waiting for the relay's retry-poll tick (every 30s) to retry payout #1..."
	sleep 65

	local final_status1 final_vendor1
	final_status1="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT status FROM payout_requests WHERE id = '$id1';")"
	final_vendor1="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT vendor FROM payout_requests WHERE id = '$id1';")"
	[ "$final_status1" = "settled" ] && ok "payout #1 settled after mockvendor recovered (resume job retried the SAME vendor)" \
		|| fail "payout #1 final status was '$final_status1', expected 'settled'"
	[ "$final_vendor1" = "mockvendor" ] && ok "payout #1's vendor column never changed — stayed pinned to mockvendor throughout" \
		|| fail "payout #1's vendor column changed to '$final_vendor1' — it must NEVER fail over once uncertain"

	log "-- asserting no payout ever received two settles --"
	local settle_count1 settle_count2
	settle_count1="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT count(*) FROM ledger_transactions WHERE idempotency_key = 'payout:$id1:settle';")"
	settle_count2="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT count(*) FROM ledger_transactions WHERE idempotency_key = 'payout:$id2:settle';")"
	[ "$settle_count1" = "1" ] && ok "payout #1 has exactly one settle transaction" \
		|| fail "payout #1 has $settle_count1 settle transactions, expected exactly 1"
	[ "$settle_count2" = "1" ] && ok "payout #2 has exactly one settle transaction" \
		|| fail "payout #2 has $settle_count2 settle transactions, expected exactly 1"

	local after
	after="$(account_balance "$cash")"
	[ "$after" = "480000" ] && ok "cash balance reflects exactly two settles, no more (500000 funded - 2x10000)" \
		|| fail "unexpected cash balance $after, expected 480000"

	assert_ledger_balanced
	assert_no_inconsistent_projections
	stop_services
	unset BREAKER_FAILURE_THRESHOLD
}

# ─── Scenario 9: Redis outage — selective hot-swap + fraud fail-closed ─────
#
# docs/plan/45 Task T4, bullet 1. Proves the docs/plan/45 Task T3 design
# (K4) against REAL running processes, not just pkg/cache's own unit tests:
# the ledger rate limiter and policy velocity counter hot-swap from Redis to
# an in-process memory fallback the INSTANT a real operation fails — no
# restart, unlike scenario 4's operator-driven REDIS_ENABLED=false mitigation
# — and recover back to Redis automatically once it's healthy again (also no
# restart). fraud-service's velocity store gets NO such fallback by design:
# it fails CLOSED (503 DEPENDENCY_UNAVAILABLE) while Redis is down instead of
# silently degrading to an approximate/no-op check.
#
# Three independent, deliberately DECOUPLED proofs (using different
# transaction types/ports so one check's fraud/rate-limit/policy config
# never contaminates another's assertions):
#   - rate limiter: burst topup-intent creates (POST /api/v1/topup never
#     screens fraud — that happens at webhook settlement in payin, a
#     different service entirely) against the gateway's public router.
#   - policy counter: a low max_daily_count seeded on withdraw_initiate (NOT
#     transfer_p2p, which is the one type ledger screens for fraud pre-tx —
#     picking withdraw_initiate means this proof never touches fraud at all)
#     via ledger-service's own internal port.
#   - fraud fail-closed: transfer_p2p in block mode with a real velocity
#     rule registered (requires a Redis-backed store lookup), which is the
#     ONE flow in this scenario that's supposed to depend on Redis being
#     reachable at all.
scenario_9() {
	log "=== Scenario 9: Redis outage — selective hot-swap (rate limiter/policy counter) + fraud fail-closed, recovery without restart (docs/plan/45 Task T3/T4) ==="
	export SCREENING_MODE=block
	export SCREENING_AMOUNT_THRESHOLD=100000000
	export SCREENING_VELOCITY_MAX_PER_HOUR=1000
	export POLICY_CACHE_TTL=2s
	ensure_deps_up
	build_server
	start_services

	local admin_token user_id recipient_id token code
	admin_token="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin)"
	user_id="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT gen_random_uuid();")"
	recipient_id="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT gen_random_uuid();")"
	provision_user "$user_id" >/dev/null
	provision_user "$recipient_id" >/dev/null
	provision_hold_account "$user_id"
	fund_user "$user_id" 5000000
	token="$(gen_token "$user_id")"

	log "-- baseline (Redis up): all three primitives report backend=redis --"
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/topup" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" -d '{"amount":"10000"}')
	[ "${code:0:1}" = "2" ] && ok "baseline topup intent create succeeded (code=$code)" || fail "baseline topup intent create got $code"
	assert_metric_value "http://localhost:$INTERNAL_PORT/metrics" cache_redis_backend_active 1 \
		"gateway rate limiter backend=redis before outage" 'primitive="rate_limiter"' 'backend="redis"'

	log "-- seeding max_daily_count=1 on withdraw_initiate (policy_limits_max_daily_count_check requires > 0 — 0 is rejected at the DB layer, confirmed empirically) --"
	curl -s -o /dev/null -X PUT "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/admin/policy/limits" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" \
		-d "{\"user_id\":\"$user_id\",\"transaction_type\":\"withdraw_initiate\",\"max_daily_count\":1,\"enabled\":true}"
	sleep 3
	# withdraw_initiate is in publicUserTypes (internal/ledger/transport/
	# http.go), so it's ALSO fraud-screened on the public router — every
	# publicUserTypes call is (http.go's fraud check has no per-type
	# discriminator beyond "public router", despite the flow name it passes
	# to fraudcheck.Client being the literal string "p2p_transfer" for every
	# type). policy.Check runs BEFORE fraud.Check (http.go:571 vs :590) and
	# short-circuits on its own rejection — so THIS baseline call both
	# consumes the one allowed use (Record() only fires after Post()
	# succeeds, i.e. after fraud ALSO allows) and proves the counter's
	# backend=redis while Redis is still reachable.
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"chaos9-wd-baseline\",\"type\":\"withdraw_initiate\",\"amount\":\"10000\"}")
	[ "${code:0:1}" = "2" ] && ok "baseline withdraw_initiate under the 1-use cap succeeded (code=$code)" || fail "baseline withdraw_initiate got $code, expected 2xx"
	assert_metric_value "http://localhost:$LEDGER_INTERNAL_PORT/metrics" cache_redis_backend_active 1 \
		"ledger policy counter backend=redis before outage" 'primitive="policy_counter"' 'backend="redis"'

	log "-- baseline (Redis up): fraud velocity screening on transfer_p2p succeeds --"
	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"chaos9-p2p-baseline\",\"type\":\"transfer_p2p\",\"amount\":\"1\",\"target_user_id\":\"$recipient_id\"}")
	[ "${code:0:1}" = "2" ] && ok "baseline P2P transfer (fraud screening reachable) succeeded (code=$code)" || fail "baseline P2P transfer got $code"
	assert_metric_value "http://localhost:$FRAUD_ADMIN_PORT/metrics" cache_redis_backend_active 1 \
		"fraud velocity store backend=redis before outage" 'primitive="fraud_velocity"' 'backend="redis"'

	log "stopping redis..."
	docker stop "$REDIS_CONTAINER" >/dev/null
	# docker stop returning is not, by itself, proof the port is already
	# unreachable from THIS host's TCP stack — reproduced once (isolated
	# Compose project gate run) as a spurious 2xx on the very first
	# fraud-dependent call below, consistent with a request racing a socket
	# that hadn't fully torn down yet. Poll until a real client actually
	# fails to reach it before proceeding, rather than trusting a fixed delay.
	local redis_down_tries=25
	while [ "$redis_down_tries" -gt 0 ] && redis-cli -h localhost -p "$REDIS_HOST_PORT" ping >/dev/null 2>&1; do
		sleep 0.2
		redis_down_tries=$((redis_down_tries - 1))
	done
	redis-cli -h localhost -p "$REDIS_HOST_PORT" ping >/dev/null 2>&1 \
		&& fail "redis still answered PING after docker stop — the outage below would race a live Redis" \
		|| ok "redis confirmed unreachable before proceeding"

	log "-- Redis down: rate limiter must keep enforcing from memory (burst of 11 topup-intent creates) --"
	# RateLimitByIPAndPath keys on r.RemoteAddr, which includes the ephemeral
	# source port (pkg/middleware/rate_limit.go:73) — 11 SEPARATE curl
	# invocations would each open its own TCP connection and land on 11
	# different keys, never actually exercising the SAME bucket. Chaining 11
	# request specs with --next inside ONE curl invocation reuses a single
	# persistent HTTP/1.1 connection (same source port throughout, verified
	# against a real net/http keep-alive server while writing this scenario)
	# while still giving one clean %{http_code} line per request — every
	# request lands on the identical rate-limit key, the way one real client
	# reusing a connection would.
	local i rl_success=0 rl_429=0 rl_args=() rl_output
	for i in $(seq 1 11); do
		rl_args+=(-s -o /dev/null -w '%{http_code}\n' -X POST \
			-H "Authorization: Bearer $token" -H "Content-Type: application/json" -d '{"amount":"10000"}' \
			"http://localhost:$APP_PORT/api/v1/topup")
		[ "$i" -lt 11 ] && rl_args+=(--next)
	done
	rl_output="$(curl "${rl_args[@]}")"
	while IFS= read -r code; do
		if [ "$code" = "429" ]; then rl_429=$((rl_429 + 1)); else rl_success=$((rl_success + 1)); fi
	done <<<"$rl_output"
	[ "$rl_success" -ge "1" ] && ok "rate limiter still serves traffic from memory with Redis down ($rl_success/11 succeeded)" \
		|| fail "expected at least one successful topup create among 11 with Redis down, got 0"
	[ "$rl_429" -ge "1" ] && ok "rate limiter still actively enforces from memory with Redis down ($rl_429/11 got 429)" \
		|| fail "expected at least one 429 among 11 rapid topup creates with Redis down — limiter may have silently bypassed"
	assert_metric_value "http://localhost:$INTERNAL_PORT/metrics" cache_redis_backend_active 1 \
		"gateway rate limiter hot-swapped to backend=local with Redis down" 'primitive="rate_limiter"' 'backend="local"'
	grep -q 'cache: redis backend degraded' "$GATEWAY_LOG" \
		&& ok "gateway logged the rate-limiter degrade transition" || fail "gateway log missing the rate-limiter degrade line"

	log "-- Redis down: policy counter's Get() is still consulted from memory, AND fraud fails CLOSED — both provable from ONE call --"
	# With Redis down, policy.Check's counter.Get() degrades to a FRESH
	# memory counter (cur=0, unaware of Redis's real cur=1 recorded at
	# baseline — pkg/cache/failover.go's FailoverCounter is deliberately not
	# a continuation of Redis's state). 0+1=1, not >1, so policy ALLOWS this
	# call on its own — it's fraud.Check (http.go:590, right after policy)
	# that then fails CLOSED, since the velocity rule is registered and its
	# store is unreachable. This one call therefore proves both: the counter
	# was genuinely exercised from memory (metric transition below, not a
	# silent no-op) AND the overall request still correctly fails closed —
	# no money moves regardless of which layer says no.
	local cash before_fc wd2_resp wd2_code
	cash="$(cash_account_id "$user_id")"
	before_fc="$(account_balance "$cash")"
	wd2_resp="$(curl -s -w '\n%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"chaos9-wd-2\",\"type\":\"withdraw_initiate\",\"amount\":\"10000\"}")"
	wd2_code="$(echo "$wd2_resp" | tail -1)"
	[ "$wd2_code" = "503" ] && echo "$wd2_resp" | grep -q "DEPENDENCY_UNAVAILABLE" \
		&& ok "withdraw_initiate fails closed with 503 DEPENDENCY_UNAVAILABLE while fraud's Redis dependency is down (policy itself allowed it — memory counter was consulted, not bypassed)" \
		|| fail "withdraw_initiate with Redis down got: $wd2_resp — expected 503 DEPENDENCY_UNAVAILABLE"
	assert_metric_value "http://localhost:$LEDGER_INTERNAL_PORT/metrics" cache_redis_backend_active 1 \
		"ledger policy counter hot-swapped to backend=local with Redis down" 'primitive="policy_counter"' 'backend="local"'
	grep -q "screening dependency unavailable, failing closed" "$LEDGER_LOG" \
		&& ok "ledger logged the fail-closed screening decision" || fail "ledger log missing the fail-closed screening line"
	local after_fc
	after_fc="$(account_balance "$cash")"
	[ "$after_fc" = "$before_fc" ] && ok "fail-closed rejection moved no money ($before_fc unchanged)" \
		|| fail "balance moved on a fail-closed rejection: $before_fc -> $after_fc"

	log "restarting redis (no service process restart)..."
	docker start "$REDIS_CONTAINER" >/dev/null
	wait_for_container_healthy "$REDIS_CONTAINER"
	log "waiting for the background probe loop's 2-consecutive-success recovery (docs/plan/45 K4 hysteresis, ~10-15s)..."
	sleep 20

	log "-- Redis back: all three primitives recover to backend=redis WITHOUT any process restart --"
	assert_metric_value "http://localhost:$INTERNAL_PORT/metrics" cache_redis_backend_active 1 \
		"gateway rate limiter recovered to backend=redis without restart" 'primitive="rate_limiter"' 'backend="redis"'
	assert_metric_value "http://localhost:$LEDGER_INTERNAL_PORT/metrics" cache_redis_backend_active 1 \
		"ledger policy counter recovered to backend=redis without restart" 'primitive="policy_counter"' 'backend="redis"'
	assert_metric_value "http://localhost:$FRAUD_ADMIN_PORT/metrics" cache_redis_backend_active 1 \
		"fraud velocity store recovered to backend=redis without restart" 'primitive="fraud_velocity"' 'backend="redis"'

	log "-- Redis back: the policy counter resumes enforcement from Redis's TRUE count (1, from baseline) — the outage's phantom memory count of 0 is correctly discarded, never merged back --"
	local wd3_resp wd3_code
	wd3_resp="$(curl -s -w '\n%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"chaos9-wd-3\",\"type\":\"withdraw_initiate\",\"amount\":\"10000\"}")"
	wd3_code="$(echo "$wd3_resp" | tail -1)"
	[ "$wd3_code" = "422" ] && echo "$wd3_resp" | grep -q "policy limit exceeded (max_daily_count)" \
		&& ok "post-recovery withdraw_initiate correctly rejected against Redis's real count, not the outage's phantom memory count" \
		|| fail "post-recovery withdraw_initiate got: $wd3_resp — expected 422 policy limit exceeded (max_daily_count)"

	code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"chaos9-p2p-recovered\",\"type\":\"transfer_p2p\",\"amount\":\"1\",\"target_user_id\":\"$recipient_id\"}")
	[ "${code:0:1}" = "2" ] && ok "P2P transfer succeeds again once Redis recovered — no restart needed (code=$code)" \
		|| fail "post-recovery P2P transfer got $code, expected 2xx"

	assert_ledger_balanced
	assert_no_inconsistent_projections
	stop_services
	unset SCREENING_MODE SCREENING_AMOUNT_THRESHOLD SCREENING_VELOCITY_MAX_PER_HOUR POLICY_CACHE_TTL
}

# ─── Scenario 10: distributed breaker across two payout replicas ──────────
#
# docs/plan/45 Task T4, bullet 2. Two REAL payout-service processes (not two
# separately-provisioned stacks) sharing one Postgres DB and one Redis
# instance, with BREAKER_DISTRIBUTED=true (docs/plan/45 Task T2).
#
# mockvendor's force-fail flag is a PER-PROCESS in-memory switch on that
# process's own vendorgw.Registry — it is NOT shared between replicas the
# way the breaker's OWN state is. Force-failing only replica A's mockvendor
# and then creating a payout is a genuine RACE: both replicas' relays poll
# the SAME payout_vendor_commands table (FOR UPDATE SKIP LOCKED, by design,
# docs/plan/45 K1/K2), so replica B can claim and dispatch the command
# FIRST against ITS OWN, still-healthy mockvendor and settle it before
# replica A ever gets a chance — reproduced empirically while writing this
# scenario (payout settled in ~100ms via whichever replica won the claim,
# never recording an 'uncertain' outcome at all). Force-failing BOTH
# replicas' mockvendor instances removes the race AND is the more faithful
# simulation anyway: a real vendor outage is symmetric across every
# replica, not scoped to whichever one an admin happened to poke. The
# actual thing being proven — that replica B's OWN admin health endpoint
# reports the vendor open via shared Redis state — holds regardless of
# which replica's relay instance is the one that actually dispatched the
# failing call. Then Redis itself is stopped: both replicas must degrade to
# their own embedded local HealthTracker fallback and keep answering
# requests, never crash.
scenario_10() {
	log "=== Scenario 10: distributed breaker across two payout replicas (docs/plan/45 Task T2/T4) ==="
	export BREAKER_DISTRIBUTED=true
	export BREAKER_FAILURE_THRESHOLD=1
	ensure_deps_up
	build_server
	# Unlike a per-process HealthTracker (dies with the process, so every
	# OTHER scenario/run starts clean), a DistributedBreaker's state lives in
	# Redis and is NOT reset by ensure_deps_up (docker volumes persist
	# across runs by this whole suite's own design, see header). A prior
	# scenario 10 run (this scenario is the only one that ever sets
	# BREAKER_DISTRIBUTED=true) can leave mockvendor 'open' in Redis from
	# its own force-fail, still visible minutes later on the very next run —
	# confirmed via `redis-cli keys 'breaker:payout:*'` after a standalone
	# run of this scenario. Clear it before priming so this scenario's own
	# force-fail is what trips the breaker, not stale state from a previous
	# run of itself.
	redis-cli -h localhost -p "$REDIS_HOST_PORT" -n 0 del breaker:payout:state:mockvendor breaker:payout:probe:mockvendor >/dev/null
	start_services
	start_payout_service_replica

	local admin_token user_id token
	admin_token="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin)"
	user_id="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT gen_random_uuid();")"
	provision_user "$user_id" >/dev/null
	provision_hold_account "$user_id"
	fund_user "$user_id" 500000
	token="$(gen_token "$user_id")"

	log "-- priming each replica's breaker with one Snapshot call (the backend gauge has no value until a real call resolves) --"
	curl -s -o /dev/null "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/vendors/health" -H "Authorization: Bearer $admin_token"
	curl -s -o /dev/null "http://localhost:$PAYOUT2_ADMIN_PORT/admin/payout/vendors/health" -H "Authorization: Bearer $admin_token"
	assert_metric_value "http://localhost:$PAYOUT_ADMIN_PORT/metrics" vendorgw_breaker_backend 1 \
		"replica A (primary) breaker backend=redis before any vendor call" 'namespace="payout"' 'backend="redis"'
	assert_metric_value "http://localhost:$PAYOUT2_ADMIN_PORT/metrics" vendorgw_breaker_backend 1 \
		"replica B breaker backend=redis before any vendor call" 'namespace="payout"' 'backend="redis"'

	log "-- force-failing mockvendor on BOTH replicas (avoids the claim-race described above) and tripping the breaker --"
	curl -s -o /dev/null -X POST "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/vendors/mockvendor/force-fail" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d '{"fail":true}'
	curl -s -o /dev/null -X POST "http://localhost:$PAYOUT2_ADMIN_PORT/admin/payout/vendors/mockvendor/force-fail" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d '{"fail":true}'

	local resp id
	resp="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/payout" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d '{"amount":"10000","destination":{"bank_code":"014","account_no":"1"}}')"
	id="$(echo "$resp" | json_field id)"
	[ -n "$id" ] && ok "payout created via replica A ($id)" || fail "payout create via replica A did not return an id: $resp"
	wait_for_vendor_call "$id" "uncertain" 15

	local health_a health_b
	health_a="$(curl -s "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/vendors/health" -H "Authorization: Bearer $admin_token")"
	echo "$health_a" | grep -q '"vendor":"mockvendor","state":"open"' \
		&& ok "replica A reports mockvendor open" \
		|| fail "replica A did not report mockvendor open: $health_a"

	health_b="$(curl -s "http://localhost:$PAYOUT2_ADMIN_PORT/admin/payout/vendors/health" -H "Authorization: Bearer $admin_token")"
	echo "$health_b" | grep -q '"vendor":"mockvendor","state":"open"' \
		&& ok "replica B reports mockvendor open via the SAME shared Redis breaker state as replica A — state converged across replicas" \
		|| fail "replica B did not report mockvendor open — distributed breaker state did not converge: $health_b"

	curl -s -o /dev/null -X POST "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/vendors/mockvendor/force-fail" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d '{"fail":false}'
	curl -s -o /dev/null -X POST "http://localhost:$PAYOUT2_ADMIN_PORT/admin/payout/vendors/mockvendor/force-fail" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d '{"fail":false}'

	log "stopping redis: both replicas must degrade to their own local fallback without crashing..."
	docker stop "$REDIS_CONTAINER" >/dev/null
	local redis_down_tries=25
	while [ "$redis_down_tries" -gt 0 ] && redis-cli -h localhost -p "$REDIS_HOST_PORT" ping >/dev/null 2>&1; do
		sleep 0.2
		redis_down_tries=$((redis_down_tries - 1))
	done

	local code_a code_b
	code_a=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/vendors/health" -H "Authorization: Bearer $admin_token")
	code_b=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:$PAYOUT2_ADMIN_PORT/admin/payout/vendors/health" -H "Authorization: Bearer $admin_token")
	[ "$code_a" = "200" ] && ok "replica A still answers admin health with Redis down (code=$code_a, no crash)" \
		|| fail "replica A admin health returned $code_a with Redis down — expected 200"
	[ "$code_b" = "200" ] && ok "replica B still answers admin health with Redis down (code=$code_b, no crash)" \
		|| fail "replica B admin health returned $code_b with Redis down — expected 200"
	assert_metric_value "http://localhost:$PAYOUT_ADMIN_PORT/metrics" vendorgw_breaker_backend 1 \
		"replica A breaker degraded to backend=local with Redis down" 'namespace="payout"' 'backend="local"'
	assert_metric_value "http://localhost:$PAYOUT2_ADMIN_PORT/metrics" vendorgw_breaker_backend 1 \
		"replica B breaker degraded to backend=local with Redis down" 'namespace="payout"' 'backend="local"'
	kill -0 "$(cat "$PAYOUT_PID_FILE")" 2>/dev/null && ok "replica A process still alive with Redis down" || fail "replica A process died with Redis down"
	kill -0 "$(cat "$PAYOUT2_PID_FILE")" 2>/dev/null && ok "replica B process still alive with Redis down" || fail "replica B process died with Redis down"

	docker start "$REDIS_CONTAINER" >/dev/null
	wait_for_container_healthy "$REDIS_CONTAINER"

	assert_ledger_balanced
	assert_no_inconsistent_projections
	stop_payout_replica
	stop_services
	unset BREAKER_DISTRIBUTED BREAKER_FAILURE_THRESHOLD
}

# ─── Scenario 11: payout crash after command enqueue / after network
# timeout ─────────────────────────────────────────────────────────────────
#
# docs/plan/45 Task T4, bullet 3, as two distinct sub-cases matching the
# doc's own wording exactly (each already-broader in scenario 5's own
# kill-matrix, but here isolated and asserted more sharply):
#
#   A. Crash AFTER the initial command is durably enqueued, BEFORE the relay
#      ever attempts to dispatch it. Seeded directly via SQL in the same
#      state EnqueueInitialSubmit's own transaction would have left behind
#      (status='submitted', a single 'pending' payout_vendor_commands row) —
#      same justification as scenario 5's kill points 1/2: the recovery
#      code path (the relay's own poll-and-claim loop) cannot distinguish a
#      seeded row from a real crash at that exact point.
#   B. Crash AFTER a real network timeout has already landed (a genuine
#      'uncertain' vendor call + a 'failed'/backing-off command, both
#      confirmed durable before the kill). Proves: (1) the relay's retry is
#      at-least-once — the SAME durable command row eventually reaches
#      'completed', no duplicate command is created for a request that
#      never failed over; (2) exactly one ledger settle effect; (3) the
#      vendor column never changes — pinned per docs/plan/40 the instant the
#      first call landed 'uncertain', regardless of the crash in between.
scenario_11() {
	log "=== Scenario 11: payout crash after command enqueue / after network timeout (docs/plan/45 Task T1/T4) ==="
	ensure_deps_up
	build_server
	start_services

	local user_id token cash balance_before
	user_id="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT gen_random_uuid();")"
	provision_user "$user_id" >/dev/null
	provision_hold_account "$user_id"
	fund_user "$user_id"
	token="$(gen_token "$user_id")"
	cash="$(cash_account_id "$user_id")"
	balance_before="$(account_balance "$cash")"

	log "--- sub-case A: crash after command enqueue, before dispatch ---"
	local held_key_a="chaos11-held-$RANDOM" held_tx_id_a id_a
	curl -s -o /dev/null -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"$held_key_a\",\"type\":\"withdraw_initiate\",\"amount\":\"10000\"}"
	held_tx_id_a="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT id FROM ledger_transactions WHERE idempotency_key = '$held_key_a';")"
	log "crashing payout-service, THEN seeding the row it would have durably committed a split second earlier..."
	kill_payout_hard
	id_a="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT gen_random_uuid();")"
	psql_exec "$PAYOUT_DB_NAME" -c "
		INSERT INTO payout_requests (id, user_id, amount, currency, vendor, destination, status, hold_tx_id, created_by, created_at, updated_at)
		VALUES ('$id_a', '$user_id', 10000, 'IDR', 'mockvendor', '{\"bank_code\":\"014\",\"account_no\":\"1\"}'::jsonb, 'submitted', '$held_tx_id_a', 'chaos_test', now(), now());" >/dev/null
	psql_exec "$PAYOUT_DB_NAME" -c "
		INSERT INTO payout_vendor_commands (id, payout_request_id, command_key, vendor, attempt, status, retry_count, max_retries, next_attempt_at, created_at, updated_at)
		VALUES (gen_random_uuid(), '$id_a', 'payout:$id_a:submit:1', 'mockvendor', 1, 'pending', 0, 8, now(), now(), now());" >/dev/null
	log "payout $id_a seeded with status='submitted' + one 'pending' command while the process is down — same recovery code path as a real crash landing at that exact point (scenario 5's own kill points 1/2 use the identical justification)"
	log "restarting payout-service — the relay's own poll loop (1s) must claim and dispatch the pre-seeded command..."
	start_payout_service

	wait_for_payout_status "$id_a" "settled" 15
	local vendor_a command_count_a settle_count_a
	vendor_a="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT vendor FROM payout_requests WHERE id = '$id_a';")"
	command_count_a="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT count(*) FROM payout_vendor_commands WHERE payout_request_id = '$id_a';")"
	settle_count_a="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT count(*) FROM ledger_transactions WHERE idempotency_key = 'payout:$id_a:settle';")"
	[ "$vendor_a" = "mockvendor" ] && ok "sub-case A: settled against the enqueued vendor (mockvendor)" || fail "sub-case A: unexpected vendor '$vendor_a'"
	[ "$command_count_a" = "1" ] && ok "sub-case A: exactly one command row — the pre-seeded row was dispatched, not duplicated" \
		|| fail "sub-case A: expected exactly 1 payout_vendor_commands row, found $command_count_a"
	[ "$settle_count_a" = "1" ] && ok "sub-case A: exactly one ledger settle transaction" \
		|| fail "sub-case A: expected exactly 1 settle transaction, found $settle_count_a"

	log "--- sub-case B: crash after a real network timeout has already landed ---"
	local id_b
	id_b="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/payout" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d '{"amount":"10000","destination":{"bank_code":"014","account_no":"1","mock_mode":"timeout"}}' \
		| sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
	[ -n "$id_b" ] || fail "sub-case B: create request did not return an id"
	wait_for_payout_status "$id_b" "submitted" 10
	wait_for_vendor_call "$id_b" "uncertain" 15
	wait_for_vendor_command_status "$id_b" "failed" 10
	local command_count_before_crash
	command_count_before_crash="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT count(*) FROM payout_vendor_commands WHERE payout_request_id = '$id_b';")"

	log "crashing payout-service exactly now — the network timeout has already landed and been recorded durably"
	kill_payout_hard

	log "simulating the vendor outage clearing (same role as scenario 2/3 restarting rabbitmq/postgres) and restarting..."
	psql_exec "$PAYOUT_DB_NAME" -c "UPDATE payout_requests SET destination = '{\"bank_code\":\"014\",\"account_no\":\"1\"}'::jsonb WHERE id = '$id_b';" >/dev/null
	psql_exec "$PAYOUT_DB_NAME" -c "UPDATE payout_vendor_commands SET next_attempt_at = now() WHERE payout_request_id = '$id_b';" >/dev/null
	start_payout_service

	wait_for_payout_status "$id_b" "settled" 40
	local vendor_b command_count_after settle_count_b
	vendor_b="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT vendor FROM payout_requests WHERE id = '$id_b';")"
	command_count_after="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT count(*) FROM payout_vendor_commands WHERE payout_request_id = '$id_b';")"
	settle_count_b="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT count(*) FROM ledger_transactions WHERE idempotency_key = 'payout:$id_b:settle';")"
	[ "$vendor_b" = "mockvendor" ] && ok "sub-case B: vendor column never changed across the crash+retry — stayed pinned to mockvendor" \
		|| fail "sub-case B: vendor column changed to '$vendor_b' — must never move once 'uncertain'"
	[ "$command_count_after" = "$command_count_before_crash" ] && ok "sub-case B: no duplicate command row was created by the crash+restart ($command_count_after total)" \
		|| fail "sub-case B: command row count changed across the crash ($command_count_before_crash -> $command_count_after) — retry duplicated the durable command"
	[ "$settle_count_b" = "1" ] && ok "sub-case B: exactly one ledger settle transaction despite the crash + at-least-once retry" \
		|| fail "sub-case B: expected exactly 1 settle transaction, found $settle_count_b"

	local balance_after expected_after
	balance_after="$(account_balance "$cash")"
	expected_after=$((balance_before - 20000))
	[ "$balance_after" = "$expected_after" ] && ok "cash balance moved by exactly the expected amount (before=$balance_before after=$balance_after)" \
		|| fail "cash balance mismatch: before=$balance_before after=$balance_after expected=$expected_after — money lost or duplicated"

	assert_ledger_balanced
	assert_no_inconsistent_projections
	stop_services
}

# ─── Main ────────────────────────────────────────────────────────────────────

case "${1:-}" in
1) scenario_1 ;;
2) scenario_2 ;;
3) scenario_3 ;;
4) scenario_4 ;;
5) scenario_5 ;;
6) scenario_6 ;;
7) scenario_7 ;;
8) scenario_8 ;;
9) scenario_9 ;;
10) scenario_10 ;;
11) scenario_11 ;;
all)
	scenario_1
	scenario_2
	scenario_3
	scenario_4
	scenario_5
	scenario_6
	scenario_7
	scenario_8
	scenario_9
	scenario_10
	scenario_11
	;;
*)
	echo "Usage: $0 {1|2|3|4|5|6|7|8|9|10|11|all}"
	exit 2
	;;
esac

echo
if [ "$FAILED" = "0" ]; then
	printf '\033[1;32m=== ALL CHAOS ASSERTIONS PASSED ===\033[0m\n'
	exit 0
else
	printf '\033[1;31m=== ONE OR MORE CHAOS ASSERTIONS FAILED ===\033[0m\n'
	exit 1
fi
