#!/usr/bin/env bash
# Compatibility shim → scripts/deploy/deployment-inventory-check.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/deploy/deployment-inventory-check.sh" "$@"
