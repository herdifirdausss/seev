#!/usr/bin/env bash
# Ledger/projection integrity assertions and Prometheus metric checks.
# Sourced by scripts/lib.sh. Never executed directly.

assert_ledger_balanced() {
	local rows
	rows="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT count(*) FROM fn_verify_ledger_balance('-infinity','infinity');" | tr -d '[:space:]')"
	if [ "$rows" = "0" ]; then
		ok "fn_verify_ledger_balance() found 0 unbalanced transactions"
	else
		fail "fn_verify_ledger_balance() found $rows unbalanced transaction(s) — MONEY LOST OR CREATED"
		psql_exec "$LEDGER_DB_NAME" -c "SELECT * FROM fn_verify_ledger_balance('-infinity','infinity');"
	fi
}

assert_no_inconsistent_projections() {
	local rows
	rows="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT count(*) FROM v_account_balance_audit WHERE is_consistent = false;" | tr -d '[:space:]')"
	if [ "$rows" = "0" ]; then
		ok "v_account_balance_audit: all accounts consistent"
	else
		fail "v_account_balance_audit found $rows inconsistent account(s)"
		psql_exec "$LEDGER_DB_NAME" -c "SELECT * FROM v_account_balance_audit WHERE is_consistent = false;"
	fi
}

assert_no_stuck_pending_transactions() {
	local rows
	rows="$(psql_exec "$LEDGER_DB_NAME" -c "SELECT count(*) FROM ledger_transactions WHERE status = 'pending';" | tr -d '[:space:]')"
	if [ "$rows" = "0" ]; then
		ok "no ledger_transactions stuck in 'pending' (no partial writes)"
	else
		fail "$rows ledger_transactions stuck in 'pending' — partial write detected"
		psql_exec "$LEDGER_DB_NAME" -c "SELECT id, type, status, created_at FROM ledger_transactions WHERE status = 'pending';"
	fi
}

assert_metric_value() {
	local url=$1 metric=$2 expected=$3 desc=$4
	shift 4
	local matches="" value
	local fetch=curl
	case "$url" in
		https://*) fetch=curl_internal ;;
	esac
	matches="$("$fetch" -s "$url" | grep "^${metric}{")"
	local label
	for label in "$@"; do
		matches="$(printf '%s\n' "$matches" | grep -F -- "$label")"
	done
	value="$(printf '%s\n' "$matches" | head -1 | awk '{print $2}')"
	if [ "$value" = "$expected" ]; then
		ok "$desc"
	else
		fail "$desc — got '$value', expected '$expected' (metric=$metric labels=[$*] url=$url)"
	fi
}
