#!/usr/bin/env bash
# Compatibility shim → scripts/chaos/dr-drill.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/chaos/dr-drill.sh" "$@"
