#!/usr/bin/env sh
set -eu

root_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
connector_dir="$root_dir/connectors"

command -v jq >/dev/null 2>&1 || {
  echo "validate-connectors: jq is required" >&2
  exit 2
}

for file in "$connector_dir"/*.json; do
  jq -e '(.name | type == "string") and (.config | type == "object")' "$file" >/dev/null
  table_allowlist=$(jq -r '.config["table.include.list"] // empty' "$file")
  column_allowlist=$(jq -r '.config["column.include.list"] // empty' "$file")
  [ -n "$table_allowlist" ] || { echo "validate-connectors: empty table allowlist in $file" >&2; exit 1; }
  [ -n "$column_allowlist" ] || { echo "validate-connectors: empty column allowlist in $file" >&2; exit 1; }
  case "$table_allowlist$column_allowlist" in
    *\**|*\?*) echo "validate-connectors: wildcard allowlist in $file" >&2; exit 1 ;;
  esac
  jq -e '([.config["database.password"], .config["transforms.pseudonymize.salt.file"]] | all(. != null))' "$file" >/dev/null
  if jq -r '.config["table.include.list"], .config["column.include.list"]' "$file" | grep -Eiq 'raw_(payload|request|response)|password_hash|token|secret|credential|private_key|access_key|refresh_token|session|cookie|account_number|document|kyc|error_message'; then
    echo "validate-connectors: prohibited configuration pattern in $file" >&2
    exit 1
  fi
done

echo "connector JSON allowlists are explicit and contain no prohibited field patterns"
