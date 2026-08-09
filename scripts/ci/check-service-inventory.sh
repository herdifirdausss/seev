#!/usr/bin/env bash
# CI gate: validates that config/service-inventory.tsv stays consistent with
# every operational consumer — postgres-init scripts, backup-manifest.sh,
# restore-cluster.sh, and the services/ directory itself. Fails clearly when:
#   - a services/*/migrations directory exists but is not in the registry;
#   - a backup_required=true service is absent from backup-manifest SERVICES;
#   - a backup_required=true service is absent from restore-cluster SERVICES;
#   - a migration_dir in the registry references a non-existent path.
#
# Usage: bash scripts/ci/check-service-inventory.sh
# Exit codes: 0 = OK, 1 = drift detected, 2 = precondition error
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

REGISTRY="config/service-inventory.tsv"
[ -f "$REGISTRY" ] || { echo "check-service-inventory: $REGISTRY not found" >&2; exit 2; }

ERRORS=0
fail() { echo "check-service-inventory: FAIL — $*" >&2; ERRORS=$((ERRORS + 1)); }
info() { echo "check-service-inventory: $*"; }

# Helper: look up a field from the TSV for a given service (bash 3 compatible).
# tsv_field <service> <field-number>  (1=service 2=migration_dir 3=database 4=app_role 5=backup_required 6=load_enabled 7=deployable)
tsv_field() {
    local svc="$1" field="$2"
    awk -F'\t' -v svc="$svc" -v f="$field" '
        /^#/ { next } /^service/ { next } NF==0 { next }
        $1==svc { print $f; exit }
    ' "$REGISTRY"
}

# Build a plain indexed array of all service names (bash 3 compatible).
REG_SERVICES=()
while IFS=$'\t' read -r service _migration_dir _database _app_role _backup _load _deployable; do
    [[ "$service" =~ ^# ]] && continue
    [[ "$service" =~ ^service ]] && continue
    [[ -z "$service" ]] && continue
    REG_SERVICES+=("$service")
done <"$REGISTRY"

info "registry contains ${#REG_SERVICES[@]} services: ${REG_SERVICES[*]}"

# ── 1. Every services/*/migrations directory must be registered ──────────────
for migration_path in services/*/migrations; do
    [ -d "$migration_path" ] || continue
    dir_name="$(basename "$(dirname "$migration_path")")"
    found=0
    for svc in "${REG_SERVICES[@]}"; do
        mdir="$(tsv_field "$svc" 2)"
        if [ "$mdir" = "$dir_name" ]; then
            found=1
            break
        fi
    done
    if [ "$found" -eq 0 ]; then
        fail "services/$dir_name/migrations exists but is not in $REGISTRY"
    fi
done

# ── 2. Every migration_dir in the registry must point to a real path ─────────
for svc in "${REG_SERVICES[@]}"; do
    mdir="$(tsv_field "$svc" 2)"
    if [ ! -d "services/$mdir/migrations" ]; then
        fail "service '$svc' references migration_dir '$mdir' but services/$mdir/migrations does not exist"
    fi
done

# Canonical script locations (root paths are compatibility shims).
BACKUP_MANIFEST="scripts/ops/backup-manifest.sh"
RESTORE_CLUSTER="scripts/ops/restore-cluster.sh"
[ -f "$BACKUP_MANIFEST" ] || { echo "check-service-inventory: $BACKUP_MANIFEST not found" >&2; exit 2; }
[ -f "$RESTORE_CLUSTER" ] || { echo "check-service-inventory: $RESTORE_CLUSTER not found" >&2; exit 2; }

# ── 3. backup-manifest.sh SERVICES must include every backup_required=true entry ─
MANIFEST_SERVICES_LINE="$(grep '^SERVICES=' "$BACKUP_MANIFEST" | head -1)"
for svc in "${REG_SERVICES[@]}"; do
    backup_required="$(tsv_field "$svc" 5)"
    [ "$backup_required" = "true" ] || continue
    if ! echo "$MANIFEST_SERVICES_LINE" | grep -qw "$svc"; then
        fail "service '$svc' is backup_required=true but is absent from SERVICES in $BACKUP_MANIFEST"
    fi
done

# ── 4. restore-cluster.sh SERVICES must include every backup_required=true entry ─
RESTORE_SERVICES_LINE="$(grep '^SERVICES=' "$RESTORE_CLUSTER" | head -1)"
for svc in "${REG_SERVICES[@]}"; do
    backup_required="$(tsv_field "$svc" 5)"
    [ "$backup_required" = "true" ] || continue
    if ! echo "$RESTORE_SERVICES_LINE" | grep -qw "$svc"; then
        fail "service '$svc' is backup_required=true but is absent from SERVICES in $RESTORE_CLUSTER"
    fi
done

# ── 5. backup-manifest.sh expected_databases Python list must match ──────────
for svc in "${REG_SERVICES[@]}"; do
    backup_required="$(tsv_field "$svc" 5)"
    [ "$backup_required" = "true" ] || continue
    if ! grep -q "\"${svc}\"" "$BACKUP_MANIFEST"; then
        fail "service '$svc' is backup_required=true but is absent from expected_databases in $BACKUP_MANIFEST"
    fi
done

# ── 6. postgres-init/02-service-dbs.sh must include every deployable service ─
INIT_DB_LINE="$(grep 'for service in' scripts/postgres-init/02-service-dbs.sh | head -1)"
for svc in "${REG_SERVICES[@]}"; do
    if ! echo "$INIT_DB_LINE" | grep -qw "$svc"; then
        fail "service '$svc' is in the registry but absent from postgres-init/02-service-dbs.sh"
    fi
done

if [ "$ERRORS" -gt 0 ]; then
    echo ""
    echo "check-service-inventory: $ERRORS error(s) found — fix the drift above." >&2
    echo "  Registry: $REGISTRY" >&2
    echo "  To add a service: update $REGISTRY, then update backup-manifest.sh, restore-cluster.sh," >&2
    echo "  and postgres-init/02-service-dbs.sh consistently." >&2
    exit 1
fi

info "all inventory consistency checks passed."
