#!/usr/bin/env bash
# Compatibility shim → scripts/test/admin-e2e.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test/admin-e2e.sh" "$@"
