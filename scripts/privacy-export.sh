#!/usr/bin/env bash
# Compatibility shim → scripts/chaos/privacy-export.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/chaos/privacy-export.sh" "$@"
