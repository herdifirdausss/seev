#!/usr/bin/env bash
# Compatibility shim → scripts/ops/migrate.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/ops/migrate.sh" "$@"
