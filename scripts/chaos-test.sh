#!/usr/bin/env bash
# Compatibility shim → scripts/chaos/chaos-test.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/chaos/chaos-test.sh" "$@"
