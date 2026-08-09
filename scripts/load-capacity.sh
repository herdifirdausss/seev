#!/usr/bin/env bash
# Compatibility shim → scripts/load/load-capacity.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/load/load-capacity.sh" "$@"
