#!/usr/bin/env bash
# Compatibility shim → scripts/test/interest-period-e2e.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test/interest-period-e2e.sh" "$@"
