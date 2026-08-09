#!/usr/bin/env bash
# Compatibility shim → scripts/ops/restore-cluster.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/ops/restore-cluster.sh" "$@"
