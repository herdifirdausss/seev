#!/usr/bin/env bash
# Compatibility shim → scripts/test/migration-balance-v2-e2e.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test/migration-balance-v2-e2e.sh" "$@"
