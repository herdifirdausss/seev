#!/usr/bin/env bash
# Compatibility shim → scripts/chaos/rotation-drill.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/chaos/rotation-drill.sh" "$@"
