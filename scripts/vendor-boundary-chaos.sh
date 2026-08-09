#!/usr/bin/env bash
# Compatibility shim → scripts/chaos/vendor-boundary-chaos.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/chaos/vendor-boundary-chaos.sh" "$@"
