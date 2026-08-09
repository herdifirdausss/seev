#!/usr/bin/env bash
# Compatibility shim → scripts/ops/grant-app-role.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/ops/grant-app-role.sh" "$@"
