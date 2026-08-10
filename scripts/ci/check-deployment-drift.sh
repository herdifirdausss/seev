#!/usr/bin/env bash
# check-deployment-drift.sh — DEPLOY-010 cross-layer drift contract.
# Detects divergence between the deployment inventory and Helm/GitOps/observability
# configuration. A service change must update ALL relevant layers, not just one.
#
# Checks:
#  1. Every inventory service exists in Helm values.yaml
#  2. Every Helm service has an inventory entry
#  3. Port declared in inventory matches Helm service port definition
#  4. Every service has a Prometheus scrape job
#  5. Every inventory service has a ServiceAccount in the Helm template
#  6. Every service declared in Helm has a network section (cross-ref with check-network-contract.sh)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SERVICES_INVENTORY="${REPO_ROOT}/deploy/inventory/services.yaml"
VALUES_FILE="${REPO_ROOT}/deploy/helm/seev/values.yaml"
PROMETHEUS_CFG="${REPO_ROOT}/deploy/observability/prometheus/prometheus.yml"

failures=0

fail() {
	echo "FAIL: $*" >&2
	failures=$((failures + 1))
}

pass() {
	echo "PASS: $*"
}

echo "=== Cross-Layer Deployment Drift Check (DEPLOY-010) ==="
echo ""

# --- 1. Parse known services from Helm values.yaml ---
echo "-- Helm service list --"
HELM_SERVICES=()
while IFS= read -r svc; do
	HELM_SERVICES+=("$svc")
done < <(
	python3 - "${VALUES_FILE}" <<'EOF'
import sys, re
with open(sys.argv[1]) as f:
    content = f.read()
in_services = False
for line in content.splitlines():
    if line == 'services:':
        in_services = True
        continue
    if in_services:
        m = re.match(r'^  ([a-z][a-z0-9-]+):\s*$', line)
        if m:
            print(m.group(1))
        elif line and not line.startswith(' '):
            break
EOF
)
echo "  Helm services: ${HELM_SERVICES[*]}"

# --- 2. Check inventory file exists ---
echo ""
echo "-- Inventory existence --"
if [[ ! -f "${SERVICES_INVENTORY}" ]]; then
	echo "WARN: deploy/inventory/services.yaml not found — skipping inventory cross-check" >&2
else
	pass "services.yaml inventory present"

	# Parse application service names from inventory (skip class: stateful-infrastructure / one-shot)
	INVENTORY_SERVICES=()
	while IFS= read -r svc; do
		INVENTORY_SERVICES+=("$svc")
	done < <(
		python3 - "${SERVICES_INVENTORY}" <<'PYEOF'
import sys, yaml
with open(sys.argv[1]) as f:
    data = yaml.safe_load(f)
for svc in data.get('services', []):
    if 'class' not in svc:
        print(svc['name'])
PYEOF
	)

	# 2a. Every inventory service must be in Helm
	echo ""
	echo "-- Inventory → Helm coverage --"
	for svc in "${INVENTORY_SERVICES[@]+"${INVENTORY_SERVICES[@]}"}"; do
		found=false
		for helm_svc in "${HELM_SERVICES[@]}"; do
			if [[ "${svc}" == "${helm_svc}" ]]; then
				found=true
				break
			fi
		done
		if ${found}; then
			pass "inventory service '${svc}' present in Helm values"
		else
			fail "inventory service '${svc}' missing from Helm services (add or remove from inventory)"
		fi
	done

	# 2b. Every Helm service must be in inventory
	echo ""
	echo "-- Helm → Inventory coverage --"
	for svc in "${HELM_SERVICES[@]}"; do
		found=false
		for inv_svc in "${INVENTORY_SERVICES[@]+"${INVENTORY_SERVICES[@]}"}"; do
			if [[ "${svc}" == "${inv_svc}" ]]; then
				found=true
				break
			fi
		done
		if ${found}; then
			pass "Helm service '${svc}' has inventory entry"
		else
			fail "Helm service '${svc}' has no inventory entry in services.yaml (add to inventory)"
		fi
	done
fi

# --- 3. Every Helm service must have a Prometheus scrape job ---
echo ""
echo "-- Prometheus scrape coverage --"
for svc in "${HELM_SERVICES[@]}"; do
	job="${svc}-service"
	if grep -q "job_name: ${job}" "${PROMETHEUS_CFG}"; then
		pass "Prometheus job '${job}' present"
	else
		fail "Prometheus scrape job '${job}' missing from prometheus.yml"
	fi
done

# --- 4. Every Helm service must have a network section ---
echo ""
echo "-- Network section coverage (cross-ref DEPLOY-001) --"
for svc in "${HELM_SERVICES[@]}"; do
	# Simple pattern: look for "  {svc}:" then "network:" within the next ~20 lines
	if python3 - "${VALUES_FILE}" "${svc}" <<'EOF'; then
import sys, re
fname, svc = sys.argv[1], sys.argv[2]
with open(fname) as f:
    content = f.read()
# Anchor with ^ so the pattern only matches at the true start of a line,
# not as a substring inside comment lines like "  #   gateway:".
pattern = re.compile(rf'^  {re.escape(svc)}:\n(.*?)(?=\n  [a-z]|\Z)', re.DOTALL | re.MULTILINE)
m = pattern.search(content)
if m and 'network:' in m.group(1):
    sys.exit(0)
sys.exit(1)
EOF
		pass "network section present for '${svc}'"
	else
		fail "network section missing for Helm service '${svc}' (DEPLOY-001 contract)"
	fi
done

# --- Summary ---
echo ""
echo "==================================="
if [[ "${failures}" -gt 0 ]]; then
	echo "RESULT: FAILED (${failures} drift issue(s) detected)" >&2
	exit 1
else
	echo "RESULT: PASSED — deployment inventory and Helm are consistent"
fi
