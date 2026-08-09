#!/usr/bin/env bash
# Local mTLS certificate generation and management helpers.
# Sourced by scripts/lib.sh (compatibility aggregator). Never executed directly.
#
# The full implementations remain in scripts/lib.sh during the incremental
# migration (P1.5). This file is the future home for:
#   - generate_certs / ensure_certs
#   - CERT_DIR management
#
# Migration note: extract from lib.sh in the PR corresponding to P1.5 Phase 2.
