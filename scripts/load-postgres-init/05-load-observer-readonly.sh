#!/bin/sh
set -eu
# load_observer (01-load-control-role.sh) only carries pg_monitor — system
# catalog/stat views, never regular table SELECT. cmd/loaddataset (§24.1's
# dataset-manifest gap) connects as load_observer (the same OBSERVER_DSN
# cmd/loadprobe already uses) but needs to read accounts/ledger_transactions/
# ledger_entries/account_balances/schema_migrations_ledger — discovered live
# as "permission denied" on every one of them. app_readonly (created by
# ledger's own migrations, 03-load-service-migrations.sh above, which always
# runs "ledger" first) already carries SELECT on every ledger table via each
# migration's own GRANT; membership resolves against the role's CURRENT
# privileges, not a snapshot taken now, so granting it here — after ledger's
# migrations already ran earlier in this same init sequence — is sufficient
# and needs no per-table listing.
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "seev_load_ledger" <<-'EOSQL'
	DO $$
	BEGIN
	  IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'app_readonly') THEN
	    GRANT app_readonly TO load_observer;
	  END IF;
	END
	$$;
	-- schema_migrations_ledger (created directly by
	-- 03-load-service-migrations.sh, not by any migration file) is never
	-- covered by app_readonly's per-migration GRANTs — needs its own,
	-- confirmed live as the one remaining "permission denied" after the
	-- app_readonly membership grant above.
	GRANT SELECT ON schema_migrations_ledger TO load_observer;
EOSQL
