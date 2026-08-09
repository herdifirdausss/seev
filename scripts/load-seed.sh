#!/usr/bin/env bash
# Compatibility shim → scripts/load/load-seed.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/load/load-seed.sh" "$@"
