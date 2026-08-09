#!/usr/bin/env bash
# Compatibility shim → scripts/test/topup-fee-e2e.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test/topup-fee-e2e.sh" "$@"
