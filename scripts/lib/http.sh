#!/usr/bin/env bash
# HTTP/curl helpers and JSON parsing (P2.1).
# Sourced by scripts/lib.sh. Never executed directly.
#
# Provides json_get / json_get_required using jq when available, with a
# sed-based fallback for string-only fields. The jq path is preferred because
# it handles whitespace, escaped characters, nested objects, and non-string
# values that the sed fallback cannot.

# curl_internal adds the mTLS client certificate when calling services over
# their INTERNAL (gRPC-gateway) listener. Requires CERT_DIR to be set.
#
# -k/--insecure is required, not optional: this repo's certs carry ONLY a
# URI SAN (spiffe://seev/<service>), never a DNS SAN — curl has no equivalent
# of Go's custom VerifyConnection hook that checks the chain then skips
# hostname matching. --cacert/--cert/--key still present a real client
# identity and the server still enforces its allowlist.
#
# Also rewrites http:// → https:// for convenience: internal callers can
# write http://localhost:PORT without thinking about the scheme.
curl_internal() {
    local args=() arg
    for arg in "$@"; do
        case "$arg" in
        http://*) args+=("https://${arg#http://}") ;;
        *) args+=("$arg") ;;
        esac
    done
    curl -k --cacert "$CERT_DIR/ca.pem" --cert "$CERT_DIR/dev-operator.pem" --key "$CERT_DIR/dev-operator-key.pem" "${args[@]}"
}

# json_get <jq-filter> — reads JSON from stdin, extracts value with jq.
# Falls back to the sed-based string extractor when jq is not available.
json_get() {
    local filter="$1"
    if command -v jq >/dev/null 2>&1; then
        jq -r "$filter"
    else
        # sed fallback: handles string fields only. For a nested jq path,
        # match its final object key in the compact response envelope.
        local key="${filter##*.}"
        sed -n "s/.*\"${key}\": *\"\([^\"]*\)\".*/\1/p"
    fi
}

# json_get_required <jq-filter> <description> — like json_get but exits 1
# when the extracted value is empty or null.
json_get_required() {
    local filter="$1" description="${2:-$1}"
    local value
    value="$(json_get "$filter")"
    if [ -z "$value" ] || [ "$value" = "null" ]; then
        echo "http.sh: required field '$description' is missing or null in JSON response" >&2
        return 1
    fi
    echo "$value"
}

# json_field extracts a value by key from JSON on stdin. Prefers jq when
# available (handles nested paths like "data.id" → .data.id, whitespace,
# escaping); falls back to matching the final string key with sed.
json_field() {
    local key="$1"
    if command -v jq >/dev/null 2>&1; then
        jq -r ".$key // empty"
    else
        local leaf="${key##*.}"
        sed -n "s/.*\"${leaf}\": *\"\([^\"]*\)\".*/\1/p"
    fi
}

# assert_http_status curls a URL and asserts the HTTP status code.
# Usage: assert_http_status <expected_code> <description> <curl args...>
assert_http_status() {
    local expected="$1" description="$2"
    shift 2
    local actual
    actual="$(curl -s -o /dev/null -w '%{http_code}' "$@")"
    if [ "$actual" = "$expected" ]; then
        ok "$description (HTTP $actual)"
    else
        fail "$description — expected HTTP $expected, got $actual"
    fi
}
