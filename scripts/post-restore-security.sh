#!/usr/bin/env bash
# Compatibility shim → scripts/chaos/post-restore-security.sh
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/chaos/post-restore-security.sh" "$@"
