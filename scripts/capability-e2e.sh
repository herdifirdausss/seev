#!/usr/bin/env bash
# Capability journey acceptance test for the repository-side gaps called out in
# docs/engineering/capability-inventory.md.  It intentionally uses the same
# host-binary bootstrap as business-e2e.sh, but exercises the less frequently
# used Ledger control planes through their real HTTP surfaces:
# scheduled execution, fee maker-checker, disbursement maker-checker, dispute
# lifecycle, reconciliation correction, and multi-currency FX.
#
# Requires Docker, the Go toolchain, and the checked-out repository.  It does
# not claim Kubernetes/cloud/vendor/DR acceptance; those remain separate live
# evidence gates.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

export LIB_LOG_TAG="capability-e2e"
export LIB_WORK_DIR_PREFIX="capability-e2e"
APP_PORT="${APP_PORT:-18200}"
INTERNAL_PORT="${INTERNAL_PORT:-18201}"

# Durable schedule occurrences are opt-in in the service configuration.  This
# journey must exercise the production C5 path, not the legacy compatibility
# runner.
export C5_FINANCIAL_PRODUCTS_ENABLED="${C5_FINANCIAL_PRODUCTS_ENABLED:-true}"
export DEFAULT_CURRENCY="${DEFAULT_CURRENCY:-IDR}"
export POLICY_CACHE_TTL="${POLICY_CACHE_TTL:-2s}"

# shellcheck source=scripts/lib.sh
# shellcheck disable=SC1091
source "$ROOT_DIR/scripts/lib.sh"

trap cleanup EXIT
# Keep a complete, secret-safe transcript in the run work directory.  Avoid
# process substitution here: the same script is used by restricted local
# sandboxes where /dev/fd is not available.
exec >"$WORK_DIR/capability-e2e.stdout.log" 2>&1

RUN_ID="$(date +%s)-$$"
RUN_STARTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
REQUEST_SEQ=0
LAST_CODE=""
LAST_BODY=""

USER_A=""
USER_B=""
TOKEN_A=""
TOKEN_B=""
MAKER_TOKEN=""
CHECKER_TOKEN=""
MAKER_ID=""
CHECKER_ID=""

# Run a request through either the public edge or the mTLS-protected internal
# listener and retain the response without requiring jq on the CI runner.
http_request() {
	local runner=$1 label=$2 file
	shift 2
	REQUEST_SEQ=$((REQUEST_SEQ + 1))
	file="$WORK_DIR/http-${REQUEST_SEQ}-${label}.body"
	LAST_CODE="$("$runner" -sS -o "$file" -w '%{http_code}' "$@" 2>"$file.err" || true)"
	LAST_BODY="$(<"$file")"
}

public_request() {
	http_request curl "$@"
}

internal_request() {
	http_request curl_internal "$@"
}

expect_success() {
	local label=$1
	if [[ "$LAST_CODE" == 2* ]]; then
		ok "$label (HTTP $LAST_CODE)"
		return 0
	fi
	fail "$label got HTTP $LAST_CODE: $LAST_BODY"
	return 1
}

expect_client_failure() {
	local label=$1
	if [[ "$LAST_CODE" == 4* ]]; then
		ok "$label rejected as expected (HTTP $LAST_CODE)"
		return 0
	fi
	fail "$label got HTTP $LAST_CODE, expected a 4xx: $LAST_BODY"
	return 1
}

require_id() {
	local label=$1 value=$2
	if [ -n "$value" ]; then
		return 0
	fi
	fail "$label did not return an id: $LAST_BODY"
	return 1
}

setup() {
	log "=== capability E2E: bootstrap isolated users and operators ==="
	ensure_deps_up
	build_server
	start_services

	USER_A="$(uuidgen | tr '[:upper:]' '[:lower:]')"
	USER_B="$(uuidgen | tr '[:upper:]' '[:lower:]')"
	MAKER_ID="$(uuidgen | tr '[:upper:]' '[:lower:]')"
	CHECKER_ID="$(uuidgen | tr '[:upper:]' '[:lower:]')"
	provision_user "$USER_A" >/dev/null
	provision_hold_account "$USER_A"
	provision_user "$USER_B" >/dev/null
	provision_hold_account "$USER_B"
	fund_user "$USER_A" 1000000

	TOKEN_A="$(gen_token "$USER_A")"
	TOKEN_B="$(gen_token "$USER_B")"
	MAKER_TOKEN="$(gen_token "$MAKER_ID" admin_maker)"
	CHECKER_TOKEN="$(gen_token "$CHECKER_ID" admin_checker)"
	ok "users, maker, and checker are provisioned"
}

schedule_flow() {
	log "=== scheduled transactions: create -> run -> durable succeeded occurrence ==="
	local run_date schedule_id run_response occurrences
	run_date="$(date +%Y-%m-%d)"

	public_request "schedule-create" -X POST "http://localhost:$APP_PORT/api/v1/ledger/schedules" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d "{\"type\":\"transfer_p2p\",\"amount\":\"1000\",\"currency\":\"IDR\",\"target_user_id\":\"$USER_B\",\"schedule_kind\":\"once\",\"run_at_date\":\"$run_date\",\"timezone\":\"Asia/Jakarta\",\"local_time\":\"00:01\",\"missed_run_policy\":\"run_once_latest\",\"max_fee_amount\":1000000,\"fee_mode\":\"current_policy_with_consent_cap\"}"
	expect_success "scheduled transfer created" || return 1
	schedule_id="$(printf '%s' "$LAST_BODY" | json_field id)"
	require_id "schedule" "$schedule_id" || return 1

	internal_request "schedule-run" -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/schedules/run?date=$run_date" \
		-H "Authorization: Bearer $CHECKER_TOKEN" -H "Content-Type: application/json" \
		-d '{}'
	expect_success "admin ran the durable schedule dispatcher" || return 1
	run_response="$LAST_BODY"
	if printf '%s' "$run_response" | grep -Eq '"executed"[[:space:]]*:[[:space:]]*[1-9]'; then
		ok "schedule dispatcher executed at least one occurrence"
	else
		fail "schedule dispatcher did not execute an occurrence: $run_response"
		return 1
	fi

	public_request "schedule-occurrences" -X GET "http://localhost:$APP_PORT/api/v1/ledger/schedules/$schedule_id/occurrences" \
		-H "Authorization: Bearer $TOKEN_A"
	expect_success "scheduled occurrence history is readable" || return 1
	occurrences="$LAST_BODY"
	if printf '%s' "$occurrences" | grep -q '"status":"succeeded"'; then
		ok "scheduled occurrence reached succeeded"
	else
		fail "scheduled occurrence did not reach succeeded: $occurrences"
		return 1
	fi
}

fee_approval_flow() {
	log "=== fee approvals: maker draft -> submit -> distinct checker approval ==="
	local rule_id version_id status

	internal_request "fee-create" -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/ledger/fee-rules" \
		-H "Authorization: Bearer $MAKER_TOKEN" -H "Content-Type: application/json" \
		-d "{\"tx_type\":\"transfer_pocket\",\"currency\":\"IDR\",\"user_id\":\"$USER_B\",\"flat_minor_units\":321,\"fee_gateway\":\"platform\",\"reason\":\"capability-e2e-$RUN_ID\"}"
	expect_success "fee rule draft created" || return 1
	rule_id="$(printf '%s' "$LAST_BODY" | json_field rule_id)"
	version_id="$(printf '%s' "$LAST_BODY" | json_field id)"
	require_id "fee rule" "$rule_id" || return 1
	require_id "fee rule version" "$version_id" || return 1

	internal_request "fee-submit" -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/ledger/fee-rules/$version_id/submit" \
		-H "Authorization: Bearer $MAKER_TOKEN" -H "Content-Type: application/json" -d '{}'
	expect_success "fee rule submitted by maker" || return 1

	internal_request "fee-approve" -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/ledger/fee-rules/$version_id/approve" \
		-H "Authorization: Bearer $CHECKER_TOKEN" -H "Content-Type: application/json" \
		-d "{\"reason\":\"capability-e2e approval\"}"
	expect_success "fee rule approved by a distinct checker" || return 1
	status="$(printf '%s' "$LAST_BODY" | json_field status)"
	if [ "$status" = "approved" ]; then
		ok "approved fee version is active"
	else
		fail "fee version status was '$status', expected approved: $LAST_BODY"
		return 1
	fi
}

disbursement_flow() {
	log "=== disbursement: import -> reject pre-approval -> approve -> run ==="
	local manifest batch_id run_response
	manifest="$WORK_DIR/disbursement-$RUN_ID.csv"
	printf 'user_id,amount,currency,note\n%s,2500,IDR,capability-e2e\n' "$USER_A" >"$manifest"

	internal_request "disbursement-create" -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/disbursements" \
		-H "Authorization: Bearer $MAKER_TOKEN" -F "file=@$manifest"
	expect_success "disbursement manifest imported" || return 1
	batch_id="$(printf '%s' "$LAST_BODY" | json_field id)"
	require_id "disbursement batch" "$batch_id" || return 1

	internal_request "disbursement-run-before-approval" -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/disbursements/$batch_id/run" \
		-H "Authorization: Bearer $MAKER_TOKEN" -H "Content-Type: application/json" -d '{}'
	expect_client_failure "disbursement cannot run before checker approval" || return 1

	internal_request "disbursement-approve" -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/disbursements/$batch_id/approve" \
		-H "Authorization: Bearer $CHECKER_TOKEN" -H "Content-Type: application/json" -d '{}'
	expect_success "disbursement approved by a distinct checker" || return 1

	internal_request "disbursement-run" -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/disbursements/$batch_id/run" \
		-H "Authorization: Bearer $CHECKER_TOKEN" -H "Content-Type: application/json" -d '{}'
	expect_success "approved disbursement batch processed" || return 1
	run_response="$LAST_BODY"
	if printf '%s' "$run_response" | grep -Eq '"posted"[[:space:]]*:[[:space:]]*1'; then
		ok "disbursement posted exactly one item"
	else
		fail "disbursement run did not post one item: $run_response"
		return 1
	fi
}

recon_flow() {
	log "=== reconciliation: import mismatch -> create append-only correction request ==="
	local manifest batch_id item_id adjustment_id report report_date
	manifest="$WORK_DIR/recon-$RUN_ID.csv"
	report_date="$(date +%Y-%m-%d)"
	printf 'external_ref,amount,settled_at\ncapability-missing-%s,777,%sT00:00:00Z\n' "$RUN_ID" "$report_date" >"$manifest"

	internal_request "recon-import" -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/recon/batches" \
		-H "Authorization: Bearer $MAKER_TOKEN" \
		-F gateway=bca -F "report_date=$report_date" -F "file=@$manifest"
	expect_success "reconciliation batch imported and matched" || return 1
	batch_id="$(printf '%s' "$LAST_BODY" | json_field id)"
	require_id "reconciliation batch" "$batch_id" || return 1

	internal_request "recon-report" -X GET "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/recon/batches/$batch_id" \
		-H "Authorization: Bearer $MAKER_TOKEN"
	expect_success "reconciliation mismatch report is readable" || return 1
	report="$LAST_BODY"
	if printf '%s' "$report" | grep -q 'missing_internal'; then
		ok "reconciliation report exposes missing_internal mismatch"
	else
		fail "reconciliation report did not expose missing_internal: $report"
		return 1
	fi

	item_id="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT id FROM recon_items WHERE batch_id = '$batch_id' ORDER BY created_at LIMIT 1;")"
	require_id "reconciliation item" "$item_id" || return 1
	internal_request "recon-resolve" -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/recon/items/$item_id/resolve" \
		-H "Authorization: Bearer $MAKER_TOKEN" -H "Content-Type: application/json" \
		-d '{"type":"adjustment_suspense_credit","reason":"capability-e2e mismatch review"}'
	expect_success "reconciliation resolution created a pending correction" || return 1
	adjustment_id="$(printf '%s' "$LAST_BODY" | json_field adjustment_id)"
	require_id "reconciliation adjustment" "$adjustment_id" || return 1
}

dispute_flow() {
	log "=== disputes: open -> evidence -> terminal resolution with audit trail ==="
	local transfer_key original_tx_id dispute_id status_changes
	transfer_key="capability-dispute-$RUN_ID"

	public_request "dispute-source-transfer" -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"$transfer_key\",\"type\":\"transfer_p2p\",\"amount\":\"1000\",\"currency\":\"IDR\",\"target_user_id\":\"$USER_B\"}"
	expect_success "posted transaction created as dispute source" || return 1
	original_tx_id="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT id FROM ledger_transactions WHERE idempotency_key = '$transfer_key' AND status = 'posted' LIMIT 1;")"
	require_id "dispute source transaction" "$original_tx_id" || return 1

	internal_request "dispute-open" -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/disputes" \
		-H "Authorization: Bearer $MAKER_TOKEN" -H "Content-Type: application/json" \
		-d "{\"original_tx_id\":\"$original_tx_id\",\"dispute_ref\":\"capability-dispute-$RUN_ID\",\"card_network\":\"visa\",\"reason_code\":\"10.4\",\"amount\":\"1000\",\"currency\":\"IDR\"}"
	expect_success "chargeback dispute opened" || return 1
	dispute_id="$(printf '%s' "$LAST_BODY" | json_field id)"
	require_id "dispute" "$dispute_id" || return 1

	internal_request "dispute-evidence" -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/disputes/$dispute_id/evidence" \
		-H "Authorization: Bearer $MAKER_TOKEN" -H "Content-Type: application/json" \
		-d "{\"evidence_ref\":\"object://capability-e2e/$RUN_ID/evidence\"}"
	expect_success "dispute evidence submitted" || return 1

	internal_request "dispute-resolve" -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/disputes/$dispute_id/resolve" \
		-H "Authorization: Bearer $CHECKER_TOKEN" -H "Content-Type: application/json" \
		-d '{"status":"lost","reason":"capability-e2e terminal decision"}'
	expect_success "dispute resolved by a distinct checker" || return 1
	status_changes="$LAST_BODY"

	internal_request "dispute-get" -X GET "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/disputes/$dispute_id" \
		-H "Authorization: Bearer $CHECKER_TOKEN"
	expect_success "resolved dispute is queryable" || return 1
	if printf '%s' "$LAST_BODY" | grep -q '"status":"lost"' && printf '%s' "$status_changes" | grep -q '"status":"lost"'; then
		ok "dispute terminal state and resolution response are recorded"
	else
		fail "dispute terminal state was not lost: $LAST_BODY"
		return 1
	fi

	internal_request "dispute-history" -X GET "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/disputes/$dispute_id/status-changes" \
		-H "Authorization: Bearer $CHECKER_TOKEN"
	expect_success "dispute status-change audit trail is readable" || return 1
}

fx_flow() {
	log "=== FX: enable USD -> quote IDR/USD -> atomic conversion -> reconcile ==="
	local quote_id source_amount target_amount conversion_id from to pairs

	public_request "fx-enable-usd" -X POST "http://localhost:$APP_PORT/api/v1/ledger/currencies/USD/enable" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" -d '{}'
	expect_success "USD wallet enabled" || return 1

	public_request "fx-pairs" -X GET "http://localhost:$APP_PORT/api/v1/ledger/fx/pairs" \
		-H "Authorization: Bearer $TOKEN_A"
	expect_success "active FX pairs are listed" || return 1
	pairs="$LAST_BODY"
	if printf '%s' "$pairs" | grep -q 'USDIDR'; then
		ok "seeded USDIDR pair is active"
	else
		fail "USDIDR pair missing: $pairs"
		return 1
	fi

	public_request "fx-quote" -X POST "http://localhost:$APP_PORT/api/v1/ledger/fx/quotes" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-H "Idempotency-Key: capability-fx-quote-$RUN_ID" \
		-d '{"source_currency":"IDR","target_currency":"USD","source_amount":"16000"}'
	expect_success "FX quote created" || return 1
	quote_id="$(printf '%s' "$LAST_BODY" | json_field id)"
	source_amount="$(printf '%s' "$LAST_BODY" | json_field source_amount)"
	target_amount="$(printf '%s' "$LAST_BODY" | json_field target_amount)"
	require_id "FX quote" "$quote_id" || return 1
	[ -n "$source_amount" ] && [ -n "$target_amount" ] || {
		fail "FX quote omitted expected amounts: $LAST_BODY"
		return 1
	}

	public_request "fx-conversion" -X POST "http://localhost:$APP_PORT/api/v1/ledger/fx/conversions" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-H "Idempotency-Key: capability-fx-conversion-$RUN_ID" \
		-d "{\"quote_id\":\"$quote_id\",\"expected_source_amount\":\"$source_amount\",\"expected_target_amount\":\"$target_amount\",\"idempotency_key\":\"capability-fx-conversion-$RUN_ID\"}"
	expect_success "FX conversion posted atomically" || return 1
	conversion_id="$(printf '%s' "$LAST_BODY" | json_field id)"
	require_id "FX conversion" "$conversion_id" || return 1
	printf '%s' "$LAST_BODY" | grep -q '"status":"posted"' || {
		fail "FX conversion status was not posted: $LAST_BODY"
		return 1
	}

	from="$(date -u +%Y-%m-%dT00:00:00Z)"
	to="$(date -u +%Y-%m-%dT23:59:59Z)"
	internal_request "fx-reconcile" -X GET "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/admin/fx/reconciliation?from=$from&to=$to&limit=100" \
		-H "Authorization: Bearer $CHECKER_TOKEN"
	expect_success "FX conversion reconciliation report is readable" || return 1
	if printf '%s' "$LAST_BODY" | grep -q "$conversion_id"; then
		ok "FX reconciliation report contains the conversion evidence"
	else
		fail "FX reconciliation report omitted $conversion_id: $LAST_BODY"
		return 1
	fi
}

payout_unknown_state_flow() {
	log "=== payout recovery: vendor timeout -> pinned unknown state -> same-vendor recovery ==="
	local admin_token payout_id vendor call_count destination accepted_calls
	admin_token="$(gen_token "$(uuidgen | tr '[:upper:]' '[:lower:]')" admin)"

	# Keep this journey deterministic: the migration's default route remains as
	# a fallback, while priority 10/11 explicitly model the primary and backup
	# vendors used by the recovery proof.
	psql_exec "$PAYOUT_DB_NAME" -c "DELETE FROM payout_routing_rules WHERE priority IN (10, 11);" >/dev/null
	curl_internal -s -o /dev/null -X PUT "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/vendor-gateways/mockvendor" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" \
		-d '{"gateway":"bca"}'
	curl_internal -s -o /dev/null -X PUT "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/vendor-gateways/mockvendor2" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" \
		-d '{"gateway":"gopay"}'
	curl_internal -s -o /dev/null -X POST "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/routing-rules" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" \
		-d '{"flow":"payout","priority":10,"enabled":true,"currency":"IDR","vendor":"mockvendor"}'
	curl_internal -s -o /dev/null -X POST "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/routing-rules" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" \
		-d '{"flow":"payout","priority":11,"enabled":true,"currency":"IDR","vendor":"mockvendor2"}'

	curl_internal -sS -o /dev/null -X POST "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/vendors/mockvendor/force-fail" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" \
		-d '{"fail":true}'

	public_request "payout-unknown-create" -X POST "http://localhost:$APP_PORT/api/v1/payout" \
		-H "Authorization: Bearer $TOKEN_A" -H "Content-Type: application/json" \
		-d '{"amount":"1000","destination":{"bank_code":"014","account_no":"1","mock_mode":"timeout"}}'
	expect_success "payout with an uncertain vendor response was accepted for async processing" || return 1
	payout_id="$(printf '%s' "$LAST_BODY" | json_field id)"
	require_id "unknown-state payout" "$payout_id" || return 1
	wait_for_payout_status "$payout_id" "submitted" 10 || return 1
	wait_for_vendor_call "$payout_id" "uncertain" 20 || return 1
	wait_for_vendor_command_status "$payout_id" "failed" 15 || return 1
	vendor="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT vendor FROM payout_requests WHERE id = '$payout_id';")"
	call_count="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT count(*) FROM payout_vendor_calls WHERE payout_request_id = '$payout_id' AND outcome = 'uncertain';")"
	if [ "$vendor" = "mockvendor" ] && [ "$call_count" = "1" ]; then
		ok "unknown payout is pinned to mockvendor after one uncertain call"
	else
		fail "unknown payout pinning was not preserved (vendor=$vendor uncertain_calls=$call_count)"
		return 1
	fi

	# Remove the simulated transport failure without changing the persisted
	# vendor. Re-seal the destination so the next relay attempt represents the
	# same vendor request after the outage clears; a failover to mockvendor2
	# would violate the anti-double-payout contract.
	destination="$(CRYPTOX_KEY_V1="$CRYPTOX_KEY_V1" "$CRYPTOX_FIXTURE_BIN" payout payout_requests destination "$payout_id" '{"bank_code":"014","account_no":"1"}')"
	psql_exec "$PAYOUT_DB_NAME" -c "UPDATE payout_requests SET destination_ciphertext = decode('$destination','hex'), destination_key_version = 1, updated_at = now() - interval '2 minutes' WHERE id = '$payout_id'; UPDATE payout_vendor_commands SET next_attempt_at = now() WHERE payout_request_id = '$payout_id';" >/dev/null
	curl_internal -sS -o /dev/null -X POST "http://localhost:$PAYOUT_ADMIN_PORT/admin/payout/vendors/mockvendor/force-fail" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json" \
		-d '{"fail":false}'

	# A restart proves the durable recovery worker, rather than an in-memory
	# request retry, drives the same-vendor attempt to completion.
	kill_payout_hard
	start_payout_service
	wait_for_payout_status "$payout_id" "settled" 35 || return 1
	accepted_calls="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT count(*) FROM payout_vendor_calls WHERE payout_request_id = '$payout_id' AND outcome = 'accepted';")"
	vendor="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT vendor FROM payout_requests WHERE id = '$payout_id';")"
	if [ "$vendor" = "mockvendor" ] && [ "$accepted_calls" -ge 1 ]; then
		ok "unknown payout recovered and settled through the pinned mockvendor after service restart"
	else
		fail "unknown payout did not settle through the pinned vendor (vendor=$vendor accepted_calls=$accepted_calls)"
		return 1
	fi
}

command_policy_flow() {
	log "=== command policy boundary: allow, deny, public API -> scheduler -> approved admin execution ==="
	local public_decisions scheduler_decisions disbursement_decisions denied_decisions malformed_decisions

	# Exercise the execution-time subject gate through the public route. The
	# executor must record a denied decision before it rejects the command, and
	# restoring the projection keeps the rest of this disposable run usable.
	psql_exec "$LEDGER_DB_NAME" -c "UPDATE money_movement_execution_subjects SET status = 'disabled', updated_at = now() WHERE user_id = '$USER_B';" >/dev/null
	public_request "command-policy-disabled-subject" -X POST "http://localhost:$APP_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $TOKEN_B" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"capability-policy-denied-$RUN_ID\",\"type\":\"transfer_p2p\",\"amount\":\"1\",\"target_user_id\":\"$USER_A\"}"
	expect_client_failure "disabled subject was rejected at the shared command boundary" || return 1
	psql_exec "$LEDGER_DB_NAME" -c "UPDATE money_movement_execution_subjects SET status = 'active', updated_at = now() WHERE user_id = '$USER_B';" >/dev/null

	# The command executor writes the immutable decision before the low-level
	# posting service runs.  Check the three materially different callers used
	# by this journey, keyed to the freshly provisioned subject and run window,
	# so this remains safe when the disposable Docker database is reused.
	public_decisions="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT count(*) FROM money_movement_policy_decisions WHERE created_at >= '$RUN_STARTED_AT' AND user_id = '$USER_A' AND source = 'public-api' AND allowed = true AND correlation_id <> '' AND request_origin <> '';")"
	scheduler_decisions="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT count(*) FROM money_movement_policy_decisions WHERE created_at >= '$RUN_STARTED_AT' AND user_id = '$USER_A' AND source = 'scheduler' AND allowed = true AND correlation_id <> '' AND request_origin <> '';")"
	disbursement_decisions="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT count(*) FROM money_movement_policy_decisions WHERE created_at >= '$RUN_STARTED_AT' AND user_id = '$USER_A' AND source = 'bulk-disbursement' AND allowed = true AND correlation_id <> '' AND request_origin <> '';")"
	denied_decisions="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT count(*) FROM money_movement_policy_decisions WHERE created_at >= '$RUN_STARTED_AT' AND user_id = '$USER_B' AND source = 'public-api' AND allowed = false AND reason = 'subject_disabled' AND correlation_id <> '' AND request_origin <> '';")"
	malformed_decisions="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT count(*) FROM money_movement_policy_decisions WHERE created_at >= '$RUN_STARTED_AT' AND user_id = '$USER_A' AND (source = '' OR correlation_id = '' OR request_origin = '');")"

	if [ "$public_decisions" -ge 1 ] && [ "$scheduler_decisions" -ge 1 ] && [ "$disbursement_decisions" -ge 1 ] && [ "$denied_decisions" -ge 1 ]; then
		ok "public, scheduler, approved bulk-disbursement, and denied callers entered the shared policy boundary"
	else
		fail "missing command-policy audit coverage (public=$public_decisions scheduler=$scheduler_decisions disbursement=$disbursement_decisions denied=$denied_decisions)"
		return 1
	fi
	if [ "$malformed_decisions" -eq 0 ]; then
		ok "command-policy audit decisions retain source, correlation, and request origin"
	else
		fail "command-policy audit contains $malformed_decisions malformed decision(s)"
		return 1
	fi
}

setup
schedule_flow
fee_approval_flow
disbursement_flow
recon_flow
dispute_flow
fx_flow
payout_unknown_state_flow
command_policy_flow

assert_ledger_balanced
assert_no_inconsistent_projections

stop_services

echo
if [ "$FAILED" = "0" ]; then
	printf '\033[1;32m=== CAPABILITY E2E PASSED — scheduled, governed, reconciled, disputed, and FX paths verified ===\033[0m\n'
else
	printf '\033[1;31m=== ONE OR MORE CAPABILITY E2E ASSERTIONS FAILED ===\033[0m\n'
	exit 1
fi
