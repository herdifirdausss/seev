#!/usr/bin/env bash
# Operator CLI for Plan 48 assurance surfaces.
set -euo pipefail

BASE_URL="${ASSURANCE_URL:-http://127.0.0.1:18096}"
TOKEN="${ASSURANCE_TOKEN:-}"

usage() {
	cat >&2 <<'EOF'
usage: product-assurance.sh <summary|list|run|acknowledge|resolve|pause|resume-request|resume-approve> [args]
  list [query]
  acknowledge <finding-id> <reason>
  resolve <finding-id> <reason>
  pause <payin|payout> <command-id> <revision> <reason>
  resume-request <payin|payout> <command-id> <revision> <reason>
  resume-approve <payin|payout> <resume-command-id>
EOF
}

if [ -z "$TOKEN" ]; then
	echo "ASSURANCE_TOKEN is required" >&2
	exit 2
fi

api() {
	curl -fsS -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' "$@"
}

json_escape() {
	local value="$1"
	value=${value//\\/\\\\}
	value=${value//\"/\\\"}
	value=${value//$'\n'/\\n}
	value=${value//$'\r'/\\r}
	value=${value//$'\t'/\\t}
	printf '%s' "$value"
}

require_uuid() {
	local value="$1" name="$2"
	if [ -z "$value" ]; then
		echo "error: $name is required" >&2; usage; exit 2
	fi
	if ! printf '%s' "$value" | grep -qE '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'; then
		echo "error: $name must be a UUID (got: $value)" >&2; exit 2
	fi
}

require_uint() {
	local value="$1" name="$2"
	if [ -z "$value" ]; then
		echo "error: $name is required" >&2; usage; exit 2
	fi
	if ! printf '%s' "$value" | grep -qE '^[0-9]+$'; then
		echo "error: $name must be a non-negative integer (got: $value)" >&2; exit 2
	fi
}

require_enum() {
	local value="$1" name="$2"
	shift 2
	if [ -z "$value" ]; then
		echo "error: $name is required" >&2; usage; exit 2
	fi
	local allowed
	for allowed in "$@"; do
		[ "$value" = "$allowed" ] && return 0
	done
	echo "error: $name must be one of: $* (got: $value)" >&2; exit 2
}

urlencode_query() {
	local raw="$1" encoded=""
	if command -v python3 >/dev/null 2>&1; then
		encoded="$(python3 -c "import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe='=&'))" "$raw")"
	else
		encoded="$(printf '%s' "$raw" | sed 's/ /%20/g; s/!/%21/g; s/"/%22/g; s/#/%23/g; s/\$/%24/g; s/+/%2B/g')"
	fi
	printf '%s' "$encoded"
}

command="${1:-}"
case "$command" in
summary)
	api "$BASE_URL/admin/assurance/summary"
	;;
list)
	query="${2:-}"
	if [ -n "$query" ]; then
		encoded_query="$(urlencode_query "$query")"
		api "$BASE_URL/admin/assurance/findings?$encoded_query"
	else
		api "$BASE_URL/admin/assurance/findings"
	fi
	;;
run)
	api -X POST "$BASE_URL/admin/assurance/runs"
	;;
acknowledge|resolve)
	id="${2:-}"; reason="${3:-}"
	require_uuid "$id" "finding-id"
	[ -n "$reason" ] || { echo "error: reason is required" >&2; usage; exit 2; }
	reason_json=$(json_escape "$reason")
	api -X POST "$BASE_URL/admin/assurance/findings/$id/$command" -d "{\"reason\":\"$reason_json\"}"
	;;
pause)
	flow="${2:-}"; id="${3:-}"; revision="${4:-}"; reason="${5:-}"
	require_enum "$flow" "flow" payin payout
	require_uuid "$id" "command-id"
	require_uint "$revision" "revision"
	[ -n "$reason" ] || { echo "error: reason is required" >&2; usage; exit 2; }
	reason_json=$(json_escape "$reason")
	api -X POST "$BASE_URL/admin/assurance/intake/$flow/pause" -d "{\"command_id\":\"$id\",\"expected_revision\":$revision,\"reason\":\"$reason_json\"}"
	;;
resume-request|resume)
	flow="${2:-}"; id="${3:-}"; revision="${4:-}"; reason="${5:-}"
	require_enum "$flow" "flow" payin payout
	require_uuid "$id" "command-id"
	require_uint "$revision" "revision"
	[ -n "$reason" ] || { echo "error: reason is required" >&2; usage; exit 2; }
	reason_json=$(json_escape "$reason")
	api -X POST "$BASE_URL/admin/assurance/intake/$flow/resume-requests" -d "{\"command_id\":\"$id\",\"expected_revision\":$revision,\"reason\":\"$reason_json\"}"
	;;
resume-approve|approve)
	flow="${2:-}"; id="${3:-}"
	require_enum "$flow" "flow" payin payout
	require_uuid "$id" "resume-command-id"
	api -X POST "$BASE_URL/admin/assurance/intake/$flow/resume-requests/$id/approve"
	;;
*) usage; exit 2 ;;
esac
