#!/usr/bin/env bash
# Compatibility shim → scripts/ops/backup-manifest.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/ops/backup-manifest.sh" "$@"
