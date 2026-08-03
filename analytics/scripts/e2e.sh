#!/usr/bin/env sh
set -eu

root_dir=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root_dir"

./analytics/scripts/health.sh
./analytics/connect/scripts/status-connectors.sh
./analytics/scripts/dbt.sh build --profiles-dir /workspace
ANALYTICS_RECONCILIATION_TIMEOUT=5m go run ./analytics/reconciliation/cmd/reconcile
printf 'analytics e2e completed; inspect control.reconciliation_runs for the persisted result\n'
