#!/usr/bin/env bash
# Compatibility shim → scripts/test/privacy-e2e.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test/privacy-e2e.sh" "$@"
