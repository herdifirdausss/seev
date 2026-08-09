#!/usr/bin/env bash
# Compatibility shim → scripts/test/smoke-test.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test/smoke-test.sh" "$@"
