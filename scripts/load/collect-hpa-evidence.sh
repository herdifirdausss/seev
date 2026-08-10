#!/usr/bin/env bash
# Collect per-service saturation metrics from a running load test and emit a
# machine-readable HPA evidence report.
#
# Usage (after load-test.sh leaves Prometheus reachable):
#   PROM_ADDR=http://localhost:9090 ./scripts/load/collect-hpa-evidence.sh
#
# Output: deploy/load/hpa-evidence.yaml (or EVIDENCE_OUT path)
#
# The evidence file documents which metric saturates first per service so that
# HPA target selection is load-test backed rather than assumed.  See
# deploy/load/hpa-evidence.schema.json for the format contract.
set -euo pipefail

PROM_ADDR="${PROM_ADDR:-http://localhost:9090}"
EVIDENCE_OUT="${EVIDENCE_OUT:-deploy/load/hpa-evidence.yaml}"
WINDOW="${WINDOW:-5m}"   # Prometheus range over which to average

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

command -v curl >/dev/null 2>&1 || {
	printf 'collect-hpa-evidence: curl is required\n' >&2
	exit 2
}
command -v python3 >/dev/null 2>&1 || {
	printf 'collect-hpa-evidence: python3 is required\n' >&2
	exit 2
}

prom_query() {
	local q="$1"
	curl -sf --data-urlencode "query=$q" \
		"${PROM_ADDR}/api/v1/query" 2>/dev/null | \
		python3 -c "
import sys, json
data = json.load(sys.stdin)
results = data.get('data', {}).get('result', [])
if results:
    val = results[0]['value'][1]
    print(float(val))
else:
    print('NaN')
"
}

SERVICES=(gateway auth ledger payin payout fraud vendor assurance)
TIMESTAMP="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
COMMIT="$(git -C "$root_dir" rev-parse HEAD 2>/dev/null || echo unknown)"

cat >"$root_dir/$EVIDENCE_OUT" <<HEADER
# HPA saturation evidence collected by scripts/load/collect-hpa-evidence.sh
# Generated: ${TIMESTAMP}
# Source commit: ${COMMIT}
# Prometheus window: ${WINDOW}
# Review: for each service, the column with the highest value_fraction is the
# metric that saturates first. HPA targets in values.yaml should be set lower
# than 1.0 to allow headroom before SLO breach.
schema_version: "1"
collected_at: "${TIMESTAMP}"
commit: "${COMMIT}"
prometheus_window: "${WINDOW}"
services:
HEADER

for svc in "${SERVICES[@]}"; do
	# CPU fraction: rate of container cpu usage / requested cpu
	cpu_frac="$(prom_query "avg(rate(container_cpu_usage_seconds_total{container=\"${svc}-service\"}[${WINDOW}])) / avg(kube_pod_container_resource_requests{container=\"${svc}-service\",resource=\"cpu\"})" || echo NaN)"

	# Memory fraction: current usage / requested memory
	mem_frac="$(prom_query "avg(container_memory_working_set_bytes{container=\"${svc}-service\"}) / avg(kube_pod_container_resource_requests{container=\"${svc}-service\",resource=\"memory\"})" || echo NaN)"

	# Request rate (rps): rate of HTTP requests
	rps="$(prom_query "sum(rate(http_requests_total{service=\"${svc}-service\"}[${WINDOW}]))" || echo NaN)"

	# p95 latency in ms
	p95_ms="$(prom_query "histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket{service=\"${svc}-service\"}[${WINDOW}])) by (le)) * 1000" || echo NaN)"

	# Saturating metric: highest fraction column
	saturating="$(python3 - "$cpu_frac" "$mem_frac" <<'PYEOF'
import sys
vals = {"cpu": sys.argv[1], "memory": sys.argv[2]}
best = None
best_v = -1.0
for k, v in vals.items():
    try:
        fv = float(v)
        if fv > best_v:
            best_v = fv
            best = k
    except ValueError:
        pass
print(best if best else "unknown")
PYEOF
)"

	cat >>"$root_dir/$EVIDENCE_OUT" <<SVC
  ${svc}:
    cpu_fraction: ${cpu_frac}
    memory_fraction: ${mem_frac}
    request_rate_rps: ${rps}
    p95_latency_ms: ${p95_ms}
    saturating_metric: ${saturating}
SVC
done

printf 'collect-hpa-evidence: wrote %s\n' "$EVIDENCE_OUT"
printf 'collect-hpa-evidence: review saturating_metric per service and set HPA targets accordingly\n'
