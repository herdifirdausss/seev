#!/usr/bin/env bash
# Post-restore session/token revocation fence (docs/roadmap/active/50 T5, decision
# K11) — PITR can resurrect refresh tokens and admin sessions that were
# legitimately revoked AFTER the recovery target. Run this after
# scripts/restore-cluster.sh + cmd/drverify pass and cmd/drreseed has
# reseeded Redis, but BEFORE the public gateway/auth listeners are ever
# enabled (K10 item 6's ordering) — this script itself refuses to run if
# it can already reach one, exactly mirroring
# scripts/rebuild-projection.sh's own "refuse while the app is live"
# guard.
#
# Dry-run by default: prints the count of tokens/sessions that WOULD be
# revoked and changes nothing. Pass --confirm to actually act. Every log
# line is a count or a timestamp — never a token value, a session id (the
# adminbff sessions.id column IS the live opaque session credential
# itself, unlike auth's hashed refresh tokens), or a user's email.
#
# Runtime secrets (JWT_SECRET, INTERNAL_GRPC_TOKEN, Vault-sourced values)
# are never read from or written to any table this script touches —
# confirmed by reading internal/config's actual loader
# (docs/roadmap/active/50 T5 Result item 6): they come from the environment/Vault
# at process startup, independent of any PostgreSQL backup.
#
# Usage:
#   ./scripts/post-restore-security.sh              # dry-run (default)
#   ./scripts/post-restore-security.sh --confirm     # actually revoke
#
# With no POSTGRES_* env set, defaults to the local docker-compose dev
# stack (container seev-postgres-1, schema-owner role — this script needs
# to bypass RLS across every user, not act as one restricted app role, so
# it uses the bootstrap superuser exactly like migrations do, not
# app_service).

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

CONFIRM=0
if [ "${1:-}" = "--confirm" ]; then
	CONFIRM=1
fi

APP_HEALTH_URL="${APP_HEALTH_URL:-http://localhost:${APP_PORT:-8080}/health}"

POSTGRES_HOST="${POSTGRES_HOST:-}"
POSTGRES_PORT="${POSTGRES_PORT:-5433}"
POSTGRES_MIGRATE_USER="${POSTGRES_MIGRATE_USER:-seev}"
POSTGRES_MIGRATE_PASSWORD="${POSTGRES_MIGRATE_PASSWORD:-seev}"
POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-seev-postgres-1}"

log() { printf '\033[1;34m[post-restore-security]\033[0m %s\n' "$*"; }
ok() { printf '\033[1;32m[ pass]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[ FAIL]\033[0m %s\n' "$*"; }

# run_psql <database> <sql...> — connects as the schema-owner role, which
# is the actual Postgres bootstrap superuser (docs/roadmap/active/50 T1 Result) and
# therefore bypasses RLS unconditionally, unlike app_service under FORCE
# ROW LEVEL SECURITY — this script must act across every user's rows, not
# as any one restricted app role.
run_psql() {
	local database=$1
	shift
	if [ -n "$POSTGRES_HOST" ]; then
		PGPASSWORD="$POSTGRES_MIGRATE_PASSWORD" psql -h "$POSTGRES_HOST" -p "$POSTGRES_PORT" \
			-U "$POSTGRES_MIGRATE_USER" -d "$database" -v ON_ERROR_STOP=1 -tA "$@"
	else
		docker exec -i -e PGPASSWORD="$POSTGRES_MIGRATE_PASSWORD" "$POSTGRES_CONTAINER" \
			psql -U "$POSTGRES_MIGRATE_USER" -d "$database" -v ON_ERROR_STOP=1 -tA "$@"
	fi
}

# ─── Step 1: refuse to run while the app is live ───────────────────────────

if curl -sf -o /dev/null --max-time 2 "$APP_HEALTH_URL" 2>/dev/null; then
	fail "the app is responding at $APP_HEALTH_URL — public traffic must not be enabled before this fence runs (K10 item 6). Stop the app first."
	exit 1
fi
log "app is not responding at $APP_HEALTH_URL — proceeding"

if [ "$CONFIRM" -eq 0 ]; then
	log "DRY RUN (pass --confirm to actually revoke) — counting only, nothing will change"
fi

# ─── Step 2: refresh tokens ─────────────────────────────────────────────────
# Same predicate internal/auth/repository/refresh_token_repository.go's
# RevokeAllForUser already uses (revoked_at IS NULL AND expires_at > now()),
# widened from one user to every user — a soft revoke (UPDATE, never
# DELETE), audit-preserving, identical semantics to the proven live path.

LIVE_TOKENS=$(run_psql seev_auth -c "SELECT count(*) FROM auth_refresh_tokens WHERE revoked_at IS NULL AND expires_at > now();")
log "auth_refresh_tokens: $LIVE_TOKENS live token(s) found"

if [ "$CONFIRM" -eq 1 ] && [ "$LIVE_TOKENS" != "0" ]; then
	run_psql seev_auth -c "UPDATE auth_refresh_tokens SET revoked_at = now() WHERE revoked_at IS NULL AND expires_at > now();" >/dev/null
	REMAINING_TOKENS=$(run_psql seev_auth -c "SELECT count(*) FROM auth_refresh_tokens WHERE revoked_at IS NULL AND expires_at > now();")
	if [ "$REMAINING_TOKENS" != "0" ]; then
		fail "auth_refresh_tokens: $REMAINING_TOKENS token(s) still live after revocation"
		exit 1
	fi
	ok "auth_refresh_tokens: revoked $LIVE_TOKENS token(s), 0 remain live"
fi

# ─── Step 3: admin BFF sessions ─────────────────────────────────────────────
# adminbff's sessions table has no soft-revoke column at all (unlike
# auth_refresh_tokens) — the only lifecycle operations are per-row
# DeleteSession and CleanupSessions (a bulk DELETE of already-expired
# rows). This is the same DELETE shape, widened to every row regardless
# of expiry, since sessions.id IS the live opaque credential itself (not
# hashed) and every one of them may have been legitimately ended after
# this restore's recovery target.

LIVE_SESSIONS=$(run_psql seev_adminbff -c "SELECT count(*) FROM sessions;")
log "sessions: $LIVE_SESSIONS live admin session(s) found"

if [ "$CONFIRM" -eq 1 ] && [ "$LIVE_SESSIONS" != "0" ]; then
	run_psql seev_adminbff -c "DELETE FROM sessions;" >/dev/null
	REMAINING_SESSIONS=$(run_psql seev_adminbff -c "SELECT count(*) FROM sessions;")
	if [ "$REMAINING_SESSIONS" != "0" ]; then
		fail "sessions: $REMAINING_SESSIONS session(s) still present after deletion"
		exit 1
	fi
	ok "sessions: deleted $LIVE_SESSIONS session(s), 0 remain"
fi

if [ "$CONFIRM" -eq 0 ]; then
	log "dry run complete — re-run with --confirm to actually revoke the counts above"
else
	log "post-restore security fence complete — safe to enable the public gateway/auth listeners"
fi
