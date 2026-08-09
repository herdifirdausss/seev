#!/usr/bin/env bash
# Compatibility shim → scripts/test/schedule-policy-e2e.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test/schedule-policy-e2e.sh" "$@"
