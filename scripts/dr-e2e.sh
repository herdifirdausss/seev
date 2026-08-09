#!/usr/bin/env bash
# Compatibility shim → scripts/chaos/dr-e2e.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/chaos/dr-e2e.sh" "$@"
