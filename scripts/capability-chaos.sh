#!/usr/bin/env bash
# Compatibility shim → scripts/chaos/capability-chaos.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/chaos/capability-chaos.sh" "$@"
