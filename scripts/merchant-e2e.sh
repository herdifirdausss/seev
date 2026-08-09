#!/usr/bin/env bash
# Compatibility shim → scripts/test/merchant-e2e.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test/merchant-e2e.sh" "$@"
