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

command="${1:-}"
case "$command" in
summary)
	api "$BASE_URL/admin/assurance/summary"
	;;
list)
	query="${2:-}"
	if [ -n "$query" ]; then api "$BASE_URL/admin/assurance/findings?$query"; else api "$BASE_URL/admin/assurance/findings"; fi
	;;
run)
	api -X POST "$BASE_URL/admin/assurance/runs"
	;;
acknowledge|resolve)
	id="${2:-}"; reason="${3:-}"
	[ -n "$id" ] && [ -n "$reason" ] || { usage; exit 2; }
	reason_json=$(json_escape "$reason")
	api -X POST "$BASE_URL/admin/assurance/findings/$id/$command" -d "{\"reason\":\"$reason_json\"}"
	;;
pause)
	flow="${2:-}"; id="${3:-}"; revision="${4:-}"; reason="${5:-}"
	[ -n "$flow" ] && [ -n "$id" ] && [ -n "$revision" ] && [ -n "$reason" ] || { usage; exit 2; }
	reason_json=$(json_escape "$reason")
	api -X POST "$BASE_URL/admin/assurance/intake/$flow/pause" -d "{\"command_id\":\"$id\",\"expected_revision\":$revision,\"reason\":\"$reason_json\"}"
	;;
resume-request|resume)
	flow="${2:-}"; id="${3:-}"; revision="${4:-}"; reason="${5:-}"
	[ -n "$flow" ] && [ -n "$id" ] && [ -n "$revision" ] && [ -n "$reason" ] || { usage; exit 2; }
	reason_json=$(json_escape "$reason")
	api -X POST "$BASE_URL/admin/assurance/intake/$flow/resume-requests" -d "{\"command_id\":\"$id\",\"expected_revision\":$revision,\"reason\":\"$reason_json\"}"
	;;
resume-approve|approve)
	flow="${2:-}"; id="${3:-}"
	[ -n "$flow" ] && [ -n "$id" ] || { usage; exit 2; }
	api -X POST "$BASE_URL/admin/assurance/intake/$flow/resume-requests/$id/approve"
	;;
*) usage; exit 2 ;;
esac
