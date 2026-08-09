#!/usr/bin/env bash
# User/funding/KYC fixture helpers and async wait helpers.
# Sourced by scripts/lib.sh. Never executed directly.

# wait_for_payout_status polls payout_requests.status until it matches want.
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

# wait_for_vendor_call polls payout_vendor_calls for the given outcome.
wait_for_vendor_call() {
	local id=$1 outcome=$2 tries=${3:-40}
	local count="0"
	while [ "$tries" -gt 0 ]; do
		count="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT count(*) FROM payout_vendor_calls WHERE payout_request_id = '$id' AND outcome = '$outcome';")"
		[ "$count" != "0" ] && return 0
		sleep 1
		tries=$((tries - 1))
	done
	fail "payout $id never recorded a vendor call with outcome '$outcome' (last count: $count)"
	return 1
}

wait_for_vendor_command_status() {
	local id=$1 want=$2 tries=${3:-40}
	local status=""
	while [ "$tries" -gt 0 ]; do
		status="$(psql_exec "$PAYOUT_DB_NAME" -c "SELECT status FROM payout_vendor_commands WHERE payout_request_id = '$id' AND status IN ('pending','processing','failed') LIMIT 1;")"
		[ "$status" = "$want" ] && return 0
		sleep 1
		tries=$((tries - 1))
	done
	fail "payout $id's live vendor command did not reach status '$want' in time (last seen: '$status')"
	return 1
}

provision_hold_account() {
	local user_id=$1
	psql_exec "$LEDGER_DB_NAME" -c "
        INSERT INTO accounts (id, owner_id, owner_type, type, currency, status, created_by)
        VALUES (gen_random_uuid(), '$user_id', 'user', 'hold', 'IDR', 'active', 'script_test')
        ON CONFLICT DO NOTHING;" >/dev/null
	psql_exec "$LEDGER_DB_NAME" -c "
        INSERT INTO account_balances (account_id)
        SELECT id FROM accounts WHERE owner_id = '$user_id' AND type = 'hold'
        ON CONFLICT DO NOTHING;" >/dev/null
}

fund_user() {
	local user_id=$1
	local amount=${2:-1000000}
	local cash current delta
	cash="$(cash_account_id "$user_id")"
	current="$(account_balance "$cash")"
	if ! [[ "$current" =~ ^[0-9]+$ ]]; then
		current=0
	fi
	delta=$((amount - current))
	if [ "$delta" -le 0 ]; then
		return 0
	fi
	local fund_token
	fund_token="$(gen_token "$user_id")"
	curl_internal -s -o /dev/null -X POST "http://localhost:$LEDGER_INTERNAL_PORT/api/v1/ledger/transactions" \
		-H "Authorization: Bearer $fund_token" -H "Content-Type: application/json" \
		-d "{\"idempotency_key\":\"fund-$user_id-$delta\",\"type\":\"money_in\",\"amount\":\"$delta\",\"metadata\":{\"gateway\":\"bca\"}}"
}

cash_account_id() {
	psql_exec "$LEDGER_DB_NAME" -c "SELECT id FROM accounts WHERE owner_id = '$1' AND type = 'cash' LIMIT 1;"
}

account_balance() {
	psql_exec "$LEDGER_DB_NAME" -c "SELECT balance FROM account_balances WHERE account_id = '$1';"
}

kyc_approve_l1() {
	local auth_port=$1 access_token=$2 refresh_token=$3
	curl -s -o /dev/null -X POST "http://localhost:$auth_port/api/v1/users/me/kyc" \
		-H "Authorization: Bearer $access_token" -H "Content-Type: application/json" \
		-d '{"level_requested":1}'
	curl -s -X POST "http://localhost:$auth_port/api/v1/auth/refresh" \
		-H "Content-Type: application/json" -d "{\"refresh_token\":\"$refresh_token\"}"
}

kyc_submit_l2_and_admin_approve() {
	local auth_port=$1 auth_internal_port=$2 admin_token=$3 access_token=$4 refresh_token=$5
	local submit_resp submission_id
	submit_resp="$(curl -s -X POST "http://localhost:$auth_port/api/v1/users/me/kyc" \
		-H "Authorization: Bearer $access_token" -H "Content-Type: application/json" \
		-d '{"level_requested":2}')"
	submission_id="$(echo "$submit_resp" | json_field data.id)"
	if [ -z "$submission_id" ]; then
		fail "KYC L2 submission did not return an id: $submit_resp"
		return 1
	fi
	curl_internal -s -o /dev/null -X POST "http://localhost:$auth_internal_port/api/v1/admin/kyc/submissions/$submission_id/approve" \
		-H "Authorization: Bearer $admin_token" -H "Content-Type: application/json"
	curl -s -X POST "http://localhost:$auth_port/api/v1/auth/refresh" \
		-H "Content-Type: application/json" -d "{\"refresh_token\":\"$refresh_token\"}"
}

await_notification() {
	local token=$1 title=$2 tries=20
	while [ "$tries" -gt 0 ]; do
		local resp
		resp="$(curl -s "http://localhost:$APP_PORT/api/v1/notifications" -H "Authorization: Bearer $token")"
		if echo "$resp" | grep -q "\"title\":\"$title\""; then
			ok "notification \"$title\" delivered"
			return 0
		fi
		sleep 0.5
		tries=$((tries - 1))
	done
	fail "notification \"$title\" never appeared within timeout"
	return 1
}

await_log_line() {
	local logfile=$1 pattern=$2 description=$3 tries=20
	while [ "$tries" -gt 0 ]; do
		grep -q "$pattern" "$logfile" 2>/dev/null && {
			ok "$description"
			return 0
		}
		sleep 0.5
		tries=$((tries - 1))
	done
	fail "$description — pattern '$pattern' never appeared in $logfile within timeout"
	return 1
}
