#!/usr/bin/env sh
set -eu

: "${ANALYTICS_CONFIRM_BACKFILL:?set ANALYTICS_CONFIRM_BACKFILL=disposable to run a replay/backfill}"
[ "$ANALYTICS_CONFIRM_BACKFILL" = disposable ] || { echo "backfill confirmation mismatch" >&2; exit 2; }

echo 'Backfill is replay-only: pause dbt, record offsets, run a new approved source snapshot or retained-topic replay, then rebuild and reconcile.'
echo 'No application database is written by this script.'
