#!/usr/bin/env bash
# Compatibility shim → scripts/test/multi-currency-e2e.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test/multi-currency-e2e.sh" "$@"
