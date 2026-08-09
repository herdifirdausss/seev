#!/usr/bin/env bash
# Compatibility shim → scripts/chaos/privacy-e2e-host.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/chaos/privacy-e2e-host.sh" "$@"
