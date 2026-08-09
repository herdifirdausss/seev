#!/usr/bin/env bash
# Compatibility shim → scripts/ops/product-assurance.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/ops/product-assurance.sh" "$@"
