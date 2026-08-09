#!/usr/bin/env bash
# Compatibility shim → scripts/chaos/smoke-container.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/chaos/smoke-container.sh" "$@"
