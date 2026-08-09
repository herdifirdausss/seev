#!/usr/bin/env bash
# Compatibility shim → scripts/ops/backup-checksums-enable.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/ops/backup-checksums-enable.sh" "$@"
