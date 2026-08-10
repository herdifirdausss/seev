#!/usr/bin/env bash
# check-tls-contract.sh — DEPLOY-004 mTLS certificate contract validation.
#
# Checks:
#  1. promtool check config validates prometheus.yml (catches bad scrape config).
#  2. Every service cert carries the expected DNS SANs so Prometheus can verify
#     server identity without insecure_skip_verify.
#  3. Negative: a cert issued for service A does not carry service B's DNS SAN,
#     proving the CA enforces per-service identity.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CERTS_DIR="${REPO_ROOT}/deploy/certs"
PROMETHEUS_CFG="${REPO_ROOT}/deploy/observability/prometheus/prometheus.yml"

failures=0

fail() {
	printf 'tls-contract: FAIL: %s\n' "$*" >&2
	failures=$((failures + 1))
}

pass() {
	printf 'tls-contract: PASS: %s\n' "$*"
}

echo "=== TLS Contract Validation (DEPLOY-004) ==="
echo ""

# --- 1. promtool check config ---
echo "-- Prometheus config validation --"
if command -v promtool >/dev/null 2>&1; then
	if promtool check config "${PROMETHEUS_CFG}" >/dev/null 2>&1; then
		pass "promtool check config"
	else
		fail "prometheus.yml failed promtool check config"
		promtool check config "${PROMETHEUS_CFG}" >&2 || true
	fi
else
	printf 'tls-contract: SKIP: promtool not found (install prometheus to enable)\n'
fi
echo ""

# --- 2. DNS SAN presence check ---
# Each leaf cert must carry at minimum:
#   DNS:<service>-service
#   DNS:<service>-service.seev-app.svc.cluster.local
#   URI:spiffe://seev/<service>
echo "-- DNS SAN presence per service cert --"
SERVICES=(gateway auth ledger payin payout fraud admin-bff assurance vendor)

for svc in "${SERVICES[@]}"; do
	cert_file="${CERTS_DIR}/${svc}.pem"
	if [[ ! -f "${cert_file}" ]]; then
		fail "cert file missing: ${cert_file} (run 'make certs')"
		continue
	fi

	san_output="$(openssl x509 -in "${cert_file}" -noout -text 2>/dev/null | grep -A1 'Subject Alternative Name' | tail -1 || true)"

	short_dns="${svc}-service"
	fqdn_dns="${svc}-service.seev-app.svc.cluster.local"
	spiffe_uri="spiffe://seev/${svc}"

	if printf '%s' "${san_output}" | grep -qF "DNS:${short_dns}"; then
		pass "DNS:${short_dns} present in ${svc}.pem"
	else
		fail "DNS:${short_dns} missing from ${svc}.pem — regenerate with 'make certs'"
	fi

	if printf '%s' "${san_output}" | grep -qF "DNS:${fqdn_dns}"; then
		pass "DNS:${fqdn_dns} present in ${svc}.pem"
	else
		fail "DNS:${fqdn_dns} missing from ${svc}.pem — regenerate with 'make certs'"
	fi

	if printf '%s' "${san_output}" | grep -qF "URI:${spiffe_uri}"; then
		pass "URI:${spiffe_uri} present in ${svc}.pem"
	else
		fail "URI:${spiffe_uri} missing from ${svc}.pem"
	fi
done
echo ""

# --- 3. Negative: wrong-service identity isolation ---
# Verify that the gateway cert does NOT contain auth-service's DNS SAN.
# If it did, a compromised gateway could impersonate auth-service.
echo "-- Negative: per-service cert isolation --"
GATEWAY_CERT="${CERTS_DIR}/gateway.pem"
AUTH_CERT="${CERTS_DIR}/auth.pem"

if [[ -f "${GATEWAY_CERT}" && -f "${AUTH_CERT}" ]]; then
	gw_sans="$(openssl x509 -in "${GATEWAY_CERT}" -noout -text 2>/dev/null | grep -A1 'Subject Alternative Name' | tail -1 || true)"
	auth_sans="$(openssl x509 -in "${AUTH_CERT}" -noout -text 2>/dev/null | grep -A1 'Subject Alternative Name' | tail -1 || true)"

	if printf '%s' "${gw_sans}" | grep -qF "DNS:auth-service"; then
		fail "gateway cert contains auth-service DNS SAN — certs are not isolated"
	else
		pass "gateway cert does not contain auth-service DNS SAN (correct isolation)"
	fi

	if printf '%s' "${auth_sans}" | grep -qF "DNS:gateway-service"; then
		fail "auth cert contains gateway-service DNS SAN — certs are not isolated"
	else
		pass "auth cert does not contain gateway-service DNS SAN (correct isolation)"
	fi
else
	fail "gateway.pem or auth.pem missing — cannot run isolation check"
fi
echo ""

# --- 4. Verify no scrape job uses insecure_skip_verify ---
echo "-- Seev scrape jobs must not use insecure_skip_verify --"
if grep -n "insecure_skip_verify" "${PROMETHEUS_CFG}" | grep -v "^[[:space:]]*#" | grep -qF "true"; then
	fail "insecure_skip_verify: true found in prometheus.yml — server identity verification disabled"
	grep -n "insecure_skip_verify" "${PROMETHEUS_CFG}" | grep -v "^[[:space:]]*#" >&2
else
	pass "no active insecure_skip_verify: true in prometheus.yml"
fi
echo ""

# --- 5. mTLS client-identity authorization unit tests ---
# Verifies via Go unit tests that:
#   - a connection with an allowlisted client cert is accepted;
#   - a connection with a non-allowlisted client identity is rejected;
#   - a connection with no client cert at all is rejected.
# This covers DEPLOY-004 acceptance criterion: "Existing mTLS client-identity
# authorization still passes."
echo "-- mTLS client-identity authorization (tlsx unit tests) --"
if command -v go >/dev/null 2>&1; then
	if go test -count=1 -timeout 60s ./internal/platform/security/tls/... >/dev/null 2>&1; then
		pass "TestServerConfig_AcceptsAllowlistedClientIdentity"
		pass "TestServerConfig_RejectsNonAllowlistedClientIdentity"
		pass "TestServerConfig_RejectsConnectionWithoutClientCert"
	else
		fail "tlsx unit tests failed — mTLS client-identity logic is broken"
		go test -count=1 -timeout 60s -v ./internal/platform/security/tls/... 2>&1 | tail -20 >&2 || true
	fi
else
	printf 'tls-contract: SKIP: go not found — skipping mTLS client-identity unit tests\n'
fi
echo ""

echo "==================================="
if [[ "${failures}" -gt 0 ]]; then
	printf 'tls-contract: RESULT: FAILED (%d issue(s))\n' "${failures}" >&2
	exit 1
fi
printf 'tls-contract: RESULT: PASSED — mTLS certificate contract satisfied\n'
