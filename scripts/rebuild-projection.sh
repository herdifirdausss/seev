#!/usr/bin/env bash
# Compatibility shim → scripts/ops/rebuild-projection.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/ops/rebuild-projection.sh" "$@"
