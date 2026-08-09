#!/usr/bin/env bash
# Service build, start, and stop helpers.
# Sourced by scripts/lib.sh (compatibility aggregator). Never executed directly.
#
# The full implementations remain in scripts/lib.sh during the incremental
# migration (P1.5). This file is the future home for:
#   - build_services / build_vendor_service
#   - start_gateway_service / start_ledger_service / start_auth_service
#   - start_payin_service / start_payout_service / start_payout_service_replica
#   - start_fraud_service / start_adminbff_service / start_assurance_service
#   - start_vendor_service
#   - stop_services / stop_services_keep_infra
#   - cleanup (trap handler)
#
# Migration note: extract from lib.sh in the PR corresponding to P1.5 Phase 2
# (PR 7/8/9 in the implementation sequence).
