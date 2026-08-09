#!/usr/bin/env bash
# Compatibility shim → scripts/test/platform-e2e.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test/platform-e2e.sh" "$@"
