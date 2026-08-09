#!/usr/bin/env bash
# Service migration orchestration — fail-closed, per-migration state tracking.
# Sourced by scripts/lib.sh. Never executed directly.
#
# Requires: POSTGRES_CONTAINER, DB_USER, and every *_DB_NAME variable set
# by lib.sh's config block.

# _apply_service_migrations applies only the unapplied migrations for one
# service database using ON_ERROR_STOP=1 (fail-closed). Tracks state via the
# schema_migrations_<service> table that golang-migrate also writes, so
# reusing a local volume is safe — already-applied versions are detected and
# skipped, not replayed blindly. A dirty=true flag from a previous interrupted
# run is treated as a hard error (same policy as golang-migrate itself).
_apply_service_migrations() {
    local service="$1" db="$2" migration_dir="$3"
    [ -d "services/$migration_dir/migrations" ] || return 0

    docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d "$db" -v ON_ERROR_STOP=1 \
        -c "CREATE TABLE IF NOT EXISTS schema_migrations_${service} (version BIGINT NOT NULL PRIMARY KEY, dirty BOOLEAN NOT NULL);" \
        >/dev/null

    local dirty
    dirty="$(docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d "$db" -v ON_ERROR_STOP=1 -tAc \
        "SELECT dirty FROM schema_migrations_${service} ORDER BY version DESC LIMIT 1;" 2>/dev/null || echo "")"
    if [ "$dirty" = "t" ]; then
        log "ERROR: schema_migrations_${service} is in dirty state — a previous migration was interrupted."
        log "       Inspect the database and resolve manually, or run 'make volume-reset' to start fresh."
        return 1
    fi

    local current_version
    current_version="$(docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d "$db" -v ON_ERROR_STOP=1 -tAc \
        "SELECT COALESCE(MAX(version), 0) FROM schema_migrations_${service};" 2>/dev/null || echo "0")"
    current_version="${current_version:-0}"

    local applied=0 f ver
    for f in "services/$migration_dir/migrations"/*.up.sql; do
        [ -f "$f" ] || continue
        ver="$(basename "$f" | sed 's/^0*//' | cut -d_ -f1)"
        ver="${ver:-0}"
        [ "$ver" -le "$current_version" ] && continue

        docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d "$db" -v ON_ERROR_STOP=1 <<SQL >/dev/null
            DELETE FROM schema_migrations_${service};
            INSERT INTO schema_migrations_${service} (version, dirty) VALUES (${ver}, true);
SQL

        if ! docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d "$db" -v ON_ERROR_STOP=1 <"$f" >/dev/null; then
            log "ERROR: migration failed for ${service}: $(basename "$f")"
            log "       schema_migrations_${service} is now marked dirty=true."
            log "       Fix the error and re-run, or run 'make volume-reset' to start fresh."
            return 1
        fi

        docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d "$db" -v ON_ERROR_STOP=1 <<SQL >/dev/null
            DELETE FROM schema_migrations_${service};
            INSERT INTO schema_migrations_${service} (version, dirty) VALUES (${ver}, false);
SQL

        current_version="$ver"
        applied=$((applied + 1))
    done

    if [ "$applied" -gt 0 ]; then
        log "  ${service}: applied ${applied} migration(s), now at version ${current_version}"
    else
        log "  ${service}: already at version ${current_version}"
    fi
}

apply_migrations() {
    log "applying migrations (fail-closed; per-migration state tracking via schema_migrations_<service>)..."
    _apply_service_migrations ledger    "$LEDGER_DB_NAME"    ledger
    _apply_service_migrations auth      "$AUTH_DB_NAME"      auth
    _apply_service_migrations payin     "$PAYIN_DB_NAME"     payin
    _apply_service_migrations payout    "$PAYOUT_DB_NAME"    payout
    _apply_service_migrations fraud     "$FRAUD_DB_NAME"     fraud
    _apply_service_migrations gateway   "$GATEWAY_DB_NAME"   gateway
    _apply_service_migrations adminbff  "$ADMINBFF_DB_NAME"  adminbff
    _apply_service_migrations assurance "$ASSURANCE_DB_NAME" assurance
    _apply_service_migrations vendor    "$VENDOR_DB_NAME"    vendor-service

    local service_dir svc
    for service_dir in services/*/migrations; do
        [ -d "$service_dir" ] || continue
        svc="$(basename "$(dirname "$service_dir")")"
        case "$svc" in
        ledger | auth | payin | payout | fraud | gateway | adminbff | assurance | vendor-service) continue ;;
        esac
        _apply_service_migrations "$svc" "$DB_NAME" "$svc"
    done

    ensure_app_role "$DB_NAME"           seev_app      seev_app
    ensure_app_role "$LEDGER_DB_NAME"    ledger_app    ledger_app
    ensure_app_role "$AUTH_DB_NAME"      auth_app      auth_app
    ensure_app_role "$PAYIN_DB_NAME"     payin_app     payin_app
    ensure_app_role "$PAYOUT_DB_NAME"    payout_app    payout_app
    ensure_app_role "$FRAUD_DB_NAME"     fraud_app     fraud_app
    ensure_app_role "$GATEWAY_DB_NAME"   gateway_app   gateway_app
    ensure_app_role "$ADMINBFF_DB_NAME"  adminbff_app  adminbff_app
    ensure_app_role "$ASSURANCE_DB_NAME" assurance_app assurance_app
    ensure_app_role "$VENDOR_DB_NAME"    vendor_app    vendor_app
}
