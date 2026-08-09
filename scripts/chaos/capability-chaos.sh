#!/usr/bin/env bash
# Capability-specific fault-injection gate.
#
# The core chaos suite proves generic money-movement, dependency, privacy, and
# merchant-relay recovery.  This wrapper turns the capability E2E journey into
# a recovery drill as well: each durable capability boundary gets an actual
# service kill, a fail-closed probe, and a restart before the happy-path
# assertion continues.  Keeping this as a separate entry point makes it
# possible to run the longer capability drill without changing the default
# behavior of capability-e2e.sh for fast local development.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

case "${1:-application}" in
application)
	CAPABILITY_E2E_CHAOS=1 "$ROOT_DIR/scripts/capability-e2e.sh"
	;;
platform)
	"$ROOT_DIR/scripts/ci/platform-chaos.sh"
	;;
all)
	CAPABILITY_E2E_CHAOS=1 "$ROOT_DIR/scripts/capability-e2e.sh"
	"$ROOT_DIR/scripts/ci/platform-chaos.sh"
	cat <<'EOF'
capability-chaos: application and IaC/workload-identity drills passed.
capability-chaos: run deploy/kubernetes/scripts/chaos.sh against a disposable
                  kind cluster for the Kubernetes pod-replacement drill.
capability-chaos: run scripts/dr-e2e.sh for the isolated restore/destruction
                  drill; it is intentionally separate because it owns Docker
                  volumes and backup repositories.
EOF
	;;
*)
	echo "Usage: $0 {application|platform|all}" >&2
	exit 2
	;;
esac
