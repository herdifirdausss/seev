#!/usr/bin/env bash
# Compatibility shim → scripts/chaos/runtime-acceptance.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/chaos/runtime-acceptance.sh" "$@"
