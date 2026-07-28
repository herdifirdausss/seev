#!/usr/bin/env bash
set -euo pipefail

case "${1:-}" in
	stanza-create)
		[[ "$#" -eq 1 ]] || { echo "pgbackrest: invalid stanza-create arguments" >&2; exit 2; }
		;;
	check|expire)
		[[ "$#" -eq 1 ]] || { echo "pgbackrest: invalid command arguments" >&2; exit 2; }
		;;
	info)
		[[ "$#" -eq 2 && "$2" == "--output=json" ]] || { echo "pgbackrest: info only supports --output=json" >&2; exit 2; }
		;;
	--type=full|--type=diff)
		[[ "$#" -eq 2 && "$2" == "backup" ]] || { echo "pgbackrest: backup requires --type=full|diff backup" >&2; exit 2; }
		;;
	*)
		echo "pgbackrest: unsupported command" >&2
		exit 2
		;;
esac

command -v docker >/dev/null 2>&1 || { echo "pgbackrest: docker is required in PATH" >&2; exit 2; }

# The secret is read inside the already-configured postgres container. It
# never becomes a host command argument or a Make recipe line.
exec docker compose exec -T postgres sh -c '
	set -eu
	export PGBACKREST_REPO1_CIPHER_PASS="$(cat /run/secrets/pgbackrest_repo_passphrase)"
	exec pgbackrest "$@"
' pgbackrest \
	--stanza=seev \
	--config=/etc/pgbackrest/pgbackrest.conf \
	"$@"
