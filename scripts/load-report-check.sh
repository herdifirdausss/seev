#!/usr/bin/env bash
# Compatibility shim → scripts/load/load-report-check.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/load/load-report-check.sh" "$@"
