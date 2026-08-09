#!/usr/bin/env bash
# Compatibility shim → scripts/test/capability-e2e.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test/capability-e2e.sh" "$@"
