#!/bin/sh
# Wraps the official postgres entrypoint so the pgBackRest repository
# passphrase (docs/plan/50 K3) reaches every pgbackrest invocation this
# container makes on its own — most importantly archive_command, which
# PostgreSQL's server process runs as a direct child, inheriting whatever
# environment this wrapper set up before exec'ing into the real entrypoint.
#
# The passphrase is read from a Compose secret file (never baked into
# pgbackrest.conf, never logged) and exported as PGBACKREST_REPO1_CIPHER_PASS
# — pgBackRest reads any `repo1-cipher-pass`-equivalent PGBACKREST_-prefixed
# environment variable as an override, so this line never needs to appear in
# a static, readable config file.
set -eu

PASSPHRASE_FILE="${PGBACKREST_REPO_PASSPHRASE_FILE:-/run/secrets/pgbackrest_repo_passphrase}"
if [ -f "$PASSPHRASE_FILE" ]; then
    export PGBACKREST_REPO1_CIPHER_PASS
    PGBACKREST_REPO1_CIPHER_PASS="$(cat "$PASSPHRASE_FILE")"
else
    echo "entrypoint.sh: $PASSPHRASE_FILE not found — run 'make backup-secret' first. Starting WITHOUT pgBackRest encryption configured; archive_command will fail until the secret exists." >&2
fi

exec docker-entrypoint.sh "$@"
