#!/usr/bin/env bash
# Compatibility shim → scripts/test/business-e2e.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test/business-e2e.sh" "$@"
