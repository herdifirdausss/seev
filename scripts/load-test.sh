#!/usr/bin/env bash
# Compatibility shim → scripts/load/load-test.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/load/load-test.sh" "$@"
