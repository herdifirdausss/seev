#!/usr/bin/env bash
# Compatibility shim → scripts/deploy/deployment-network-probe.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/deploy/deployment-network-probe.sh" "$@"
