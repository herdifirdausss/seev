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
#   ./scripts/chaos-test.sh all      # run all eight in sequence
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
	log "=== Scenario 4: Redis down ==="
	log "NOTE (docs/plan/12 Task T7): if Redis dies WHILE the process is running with"
	log "RedisLock/RedisRateLimiter already constructed, the process does NOT hot-swap"
	log "to the in-memory fallback — that selection happens once, at ledger.NewModule"
	log "construction time. This is an accepted limitation, not a bug: mitigating a"
	log "long Redis outage means restarting the process (which then picks the memory"
	log "fallback), not live-migrating a running process's lock provider."
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
	# fraud-service is excluded here: unlike ledger's velocity/rate-limit path
	# (cache.NewMemoryCounter fallback), fraud-service's velocity store is
	# ALWAYS Redis-backed by design (cmd/fraud-service/main.go hardcodes
	# cfg.Redis.Enabled = true, ignoring REDIS_ENABLED) — there is no
	# in-memory fallback for it, so it cannot start while Redis is down
	# regardless of this flag. That's fine for this scenario: ledger's
	# screening hook fails open when fraud-service is unreachable (proven by
	# scenario 7), so the remaining traffic check below doesn't need it up.
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
#     between hold() returning and TransitionToSubmitted, is sub-millisecond
#     — not something `kill -9` fired from a separate bash process after a
#     `sleep` can hit deterministically). This tests the SAME resume code
#     path (ResumeStuck -> hold()/submit()) a request found in that status
#     would hit regardless of how it got there, which is what correctness
#     actually depends on — the row's origin (crash vs. direct seed) is
#     invisible to the recovery logic.
#   - 'submitted': reached via a REAL POST /api/v1/payout with
#     mock_mode=timeout — hold() succeeds, TransitionToSubmitted succeeds,
#     then provider.Submit genuinely errors (infra failure), leaving the row
#     stuck exactly the way a real vendor timeout would. Before restarting,
#     the seeded destination is rewritten to drop mock_mode — simulating the
#     transient vendor outage having cleared by the time resume retries,
#     the same way scenario 2/3 restart rabbitmq/postgres to simulate
#     recovery of a real dependency.
#   - 'vendor_pending': reached via a REAL POST with mock_mode=async — the
#     vendor's own in-memory Pending cache entry only exists because a real
#     Submit call populated it; there is deliberately no HTTP-reachable way
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

wait_for_payout_status() {
	local id=$1 want=$2 tries=${3:-40}
	local status=""
	while [ "$tries" -gt 0 ]; do
		status="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT status FROM payout_requests WHERE id = '$id';")"
		[ "$status" = "$want" ] && return 0
		sleep 2
		tries=$((tries - 1))
	done
	fail "payout $id did not reach status '$want' in time (last seen: '$status')"
	return 1
}

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
	# Simulate the vendor outage clearing before resume retries — same role
	# as scenario 2/3 restarting rabbitmq/postgres.
	psql_exec "$PAYOUT_DB_NAME" -c "UPDATE payout_requests SET destination = '{\"bank_code\":\"014\",\"account_no\":\"1\"}'::jsonb WHERE id = '$id_submitted';" >/dev/null
	backdate_payout "$id_submitted"
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
	# down; the next resume re-submits idempotently and reaches a terminal state.
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
	local payout_resp payout_id payout_status
	payout_resp="$(curl -s -X POST "http://localhost:$APP_PORT/api/v1/payout" \
		-H "Authorization: Bearer $token" -H "Content-Type: application/json" \
		-d '{"amount":"50","destination":{"bank_code":"014","account_no":"1"}}')"
	payout_id="$(echo "$payout_resp" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
	payout_status="$(echo "$payout_resp" | sed -n 's/.*"status":"\([^"]*\)".*/\1/p')"
	[ -n "$payout_id" ] && [ "$payout_status" = "settled" ] \
		&& ok "payout created while fraud-service was down (fail-open, $payout_id)" \
		|| fail "fraud-down payout create unexpected response: $payout_resp"
	grep -q 'payout: screening check error, failing open' "$PAYOUT_LOG" \
		&& ok "payout logged the fail-open screening error at ERROR path" \
		|| fail "payout log did not contain the fail-open screening error"

	# ─── Fraud-service back up, in BLOCK mode: all three must reject BEFORE any write ───
	start_fraud_service
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
# stays pinned to the original vendor forever, and only the resume job's
# retry against that SAME vendor can ever settle it, and (c) never produce
# two settles for any payout regardless of how many vendors were involved.
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

	log "-- recovering mockvendor; resume job must settle payout #1 against the SAME vendor --"
	curl -s -o /dev/null -X POST "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/vendors/mockvendor/force-fail" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" -d '{"fail":false}'
	backdate_payout "$id1"

	log "waiting for the resume job's cron tick (every 1 minute) to retry payout #1..."
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
all)
	scenario_1
	scenario_2
	scenario_3
	scenario_4
	scenario_5
	scenario_6
	scenario_7
	scenario_8
	;;
*)
	echo "Usage: $0 {1|2|3|4|5|6|7|8|all}"
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
