#!/usr/bin/env bash
# Regression guard for P0.1: proves that apply_migrations() in scripts/lib.sh
# cannot silently swallow a migration error.
#
# Spins up an isolated postgres container, injects a syntactically invalid
# migration file, sources the relevant helpers, and asserts that the helper
# exits non-zero. Does NOT require the full docker-compose stack — just a
# single postgres container on an ephemeral port.
#
# Usage (CI):
#   bash scripts/ci/check-migration-fail-closed.sh
#
# Exit codes:
#   0  — regression test passed (bad migration correctly causes failure)
#   1  — regression detected (bad migration was silently ignored)
#   2  — precondition/setup error
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

CONTAINER="seev-migration-ci-test-$$"
PORT="54399"
DB_USER="seev"
DB_NAME="seev_ci_test"
SERVICE="ci_test"

cleanup() {
	docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
	rm -rf "$WORK_DIR"
}
WORK_DIR="$(mktemp -d)"
trap cleanup EXIT INT TERM

echo "check-migration-fail-closed: starting isolated postgres on port $PORT..."
docker run -d --name "$CONTAINER" \
	-e POSTGRES_USER="$DB_USER" \
	-e POSTGRES_PASSWORD="$DB_USER" \
	-e POSTGRES_DB="$DB_NAME" \
	-p "${PORT}:5432" \
	postgres:16-alpine \
	>/dev/null

# Wait for postgres to be ready.
READY=0
for _ in $(seq 1 30); do
	if docker exec "$CONTAINER" psql -U "$DB_USER" -d "$DB_NAME" -c "SELECT 1" >/dev/null 2>&1; then
		READY=1
		break
	fi
	sleep 1
done
[ "$READY" -eq 1 ] || {
	echo "check-migration-fail-closed: postgres did not become ready" >&2
	exit 2
}

# Create a fake migration directory with a valid first migration and an
# intentionally broken second one.
FAKE_MIGRATIONS="$WORK_DIR/migrations"
mkdir -p "$FAKE_MIGRATIONS"
cat >"$FAKE_MIGRATIONS/000001_create_test.up.sql" <<'SQL'
CREATE TABLE ci_test_table (id SERIAL PRIMARY KEY, val TEXT);
SQL
cat >"$FAKE_MIGRATIONS/000002_invalid_syntax.up.sql" <<'SQL'
THIS IS NOT VALID SQL AND MUST CAUSE A FAILURE;
SQL

# Inline the migration helper — mirrors _apply_service_migrations from lib.sh
# but targets our throwaway container.
_apply_service_migrations_test() {
	local service="$1" db="$2" migration_dir="$3"
	[ -d "$migration_dir" ] || return 0

	docker exec -i "$CONTAINER" psql -U "$DB_USER" -d "$db" -v ON_ERROR_STOP=1 \
		-c "CREATE TABLE IF NOT EXISTS schema_migrations_${service} (version BIGINT NOT NULL PRIMARY KEY, dirty BOOLEAN NOT NULL);" \
		>/dev/null

	local dirty
	dirty="$(docker exec -i "$CONTAINER" psql -U "$DB_USER" -d "$db" -v ON_ERROR_STOP=1 -tAc \
		"SELECT dirty FROM schema_migrations_${service} ORDER BY version DESC LIMIT 1;" 2>/dev/null || echo "")"
	if [ "$dirty" = "t" ]; then
		echo "  ERROR: dirty migration state for $service" >&2
		return 1
	fi

	local current_version
	current_version="$(docker exec -i "$CONTAINER" psql -U "$DB_USER" -d "$db" -v ON_ERROR_STOP=1 -tAc \
		"SELECT COALESCE(MAX(version), 0) FROM schema_migrations_${service};" 2>/dev/null || echo "0")"
	current_version="${current_version:-0}"

	local f ver
	for f in "$migration_dir"/*.up.sql; do
		[ -f "$f" ] || continue
		ver="$(basename "$f" | sed 's/^0*//' | cut -d_ -f1)"
		ver="${ver:-0}"
		[ "$ver" -le "$current_version" ] && continue

		docker exec -i "$CONTAINER" psql -U "$DB_USER" -d "$db" -v ON_ERROR_STOP=1 <<SQL >/dev/null
            DELETE FROM schema_migrations_${service};
            INSERT INTO schema_migrations_${service} (version, dirty) VALUES (${ver}, true);
SQL

		if ! docker exec -i "$CONTAINER" psql -U "$DB_USER" -d "$db" -v ON_ERROR_STOP=1 <"$f" >/dev/null 2>&1; then
			echo "  migration failed for $service: $(basename "$f")" >&2
			return 1
		fi

		docker exec -i "$CONTAINER" psql -U "$DB_USER" -d "$db" -v ON_ERROR_STOP=1 <<SQL >/dev/null
            DELETE FROM schema_migrations_${service};
            INSERT INTO schema_migrations_${service} (version, dirty) VALUES (${ver}, false);
SQL
		current_version="$ver"
	done
}

echo "check-migration-fail-closed: running migrations with invalid SQL (expect failure)..."
if _apply_service_migrations_test "$SERVICE" "$DB_NAME" "$FAKE_MIGRATIONS" 2>/dev/null; then
	echo "check-migration-fail-closed: REGRESSION — invalid migration was silently ignored!" >&2
	echo "  apply_migrations() must fail closed on SQL errors." >&2
	exit 1
fi
echo "check-migration-fail-closed: PASS — invalid migration correctly caused failure."

echo "check-migration-fail-closed: verifying dirty flag was written..."
DIRTY="$(docker exec "$CONTAINER" psql -U "$DB_USER" -d "$DB_NAME" -tAc \
	"SELECT dirty FROM schema_migrations_${SERVICE} ORDER BY version DESC LIMIT 1;" 2>/dev/null || echo "")"
if [ "$DIRTY" != "t" ]; then
	echo "check-migration-fail-closed: REGRESSION — dirty flag not set after failed migration (got: '$DIRTY')." >&2
	exit 1
fi
echo "check-migration-fail-closed: PASS — dirty flag correctly written."

echo "check-migration-fail-closed: verifying rerun detects dirty state..."
if _apply_service_migrations_test "$SERVICE" "$DB_NAME" "$FAKE_MIGRATIONS" 2>/dev/null; then
	echo "check-migration-fail-closed: REGRESSION — dirty state was not detected on re-run!" >&2
	exit 1
fi
echo "check-migration-fail-closed: PASS — dirty state correctly detected on re-run."

echo "check-migration-fail-closed: all assertions passed."
