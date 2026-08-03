#!/usr/bin/env bash
set -euo pipefail

if [[ "${CONFIRM_DESTROY:-}" != "seev-learning-sandbox" ]]; then
  echo "Set CONFIRM_DESTROY=seev-learning-sandbox to destroy the selected sandbox" >&2
  exit 2
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
provider="${1:-gcp}"
case "$provider" in
  gcp) terraform -chdir="$ROOT_DIR/deploy/terraform/gcp/dev" destroy -auto-approve ;;
  aws) terraform -chdir="$ROOT_DIR/deploy/terraform/aws/dev" destroy -auto-approve ;;
  *) echo "usage: $0 gcp|aws" >&2; exit 2 ;;
esac
echo "$provider sandbox destroy completed; verify the provider billing view and reserved IP inventory"
