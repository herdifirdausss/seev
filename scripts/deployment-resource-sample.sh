#!/usr/bin/env bash
# Compatibility shim → scripts/deploy/deployment-resource-sample.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/deploy/deployment-resource-sample.sh" "$@"
