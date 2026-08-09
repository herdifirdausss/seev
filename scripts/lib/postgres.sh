#!/usr/bin/env bash
# PostgreSQL helpers: psql execution, DB readiness, port detection.
# Sourced by scripts/lib.sh. Never executed directly.
#
# Requires: POSTGRES_CONTAINER, DB_USER, DB_NAME set by lib.sh config block.

psql_exec() {
    local database="${DB_NAME:-seev}"
    if [ "$#" -gt 0 ] && [[ "$1" != -* ]]; then
        database=$1
        shift
    fi
    docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d "$database" -v ON_ERROR_STOP=1 -t -A "$@"
}

detect_db_port() {
    DB_HOST_PORT="$(docker port "$POSTGRES_CONTAINER" 5432/tcp 2>/dev/null | head -1 | cut -d: -f2)"
    if [ -z "$DB_HOST_PORT" ]; then
        fail "could not detect host port for $POSTGRES_CONTAINER — is docker-compose up?"
        exit 1
    fi
    log "detected Postgres host port: $DB_HOST_PORT"
}

wait_for_redis_reachable() {
    local tries=60
    while [ "$tries" -gt 0 ]; do
        if redis-cli -h localhost -p "$REDIS_HOST_PORT" ping >/dev/null 2>&1; then
            return 0
        fi
        sleep 0.5
        tries=$((tries - 1))
    done
    fail "redis did not become reachable on localhost:$REDIS_HOST_PORT in time"
    return 1
}

ensure_service_dbs() {
    local service database role exists
    for service in ledger auth payin payout fraud gateway adminbff assurance vendor; do
        database="seev_${service}"
        role="${service}_app"
        exists="$(docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname = '$database'")"
        if [ "$exists" != "1" ]; then
            docker exec -i "$POSTGRES_CONTAINER" createdb -U "$DB_USER" "$database"
        fi
        docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d postgres -v ON_ERROR_STOP=1 <<SQL >/dev/null
            DO \$\$
            BEGIN
                IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '$role') THEN
                    EXECUTE format('CREATE ROLE %I LOGIN PASSWORD %L', '$role', '$role');
                END IF;
            END
            \$\$;
SQL
    done
}

ensure_app_role() {
    local database=$1 role=$2 password=$3
    docker exec -i "$POSTGRES_CONTAINER" psql -U "$DB_USER" -d "$database" -v ON_ERROR_STOP=1 <<SQL >/dev/null
        DO \$\$
        BEGIN
            IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '$role') THEN
                CREATE ROLE $role LOGIN PASSWORD '$password';
            END IF;
        END
        \$\$;
        GRANT app_service TO $role;
SQL
}
