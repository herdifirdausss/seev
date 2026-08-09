#!/usr/bin/env bash
# Compatibility shim → scripts/load/load-snapshot.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/load/load-snapshot.sh" "$@"
