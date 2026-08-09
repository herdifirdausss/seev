#!/usr/bin/env bash
# Compatibility shim → scripts/ops/vault-seed.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/ops/vault-seed.sh" "$@"
