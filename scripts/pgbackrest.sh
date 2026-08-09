#!/usr/bin/env bash
# Compatibility shim → scripts/ops/pgbackrest.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/ops/pgbackrest.sh" "$@"
