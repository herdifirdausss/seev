#!/usr/bin/env bash
# Compatibility shim → scripts/deploy/deployment-inventory-generate.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/deploy/deployment-inventory-generate.sh" "$@"
