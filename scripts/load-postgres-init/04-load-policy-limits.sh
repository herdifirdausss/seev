#!/bin/sh
set -eu
# Disposable-load-only override: production's policy_tier_limits
# (migrations/ledger/000022_policy_tier_limits.up.sql) caps a KYC level-1
# account at 20 transfer_p2p and 5 withdraw_initiate per day — correct
# fraud-prevention behavior in production, but it makes a single
# load-scenario sender (or a small, deliberately concentrated pool for a
# hot-account experiment) hit "policy limit exceeded (max_daily_count)"
# well before any real capacity ceiling, discovered live. Raise every
# level's limits generously for this disposable database only; the
# migration file itself, and every other environment, is untouched.
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "seev_load_ledger" <<-'EOSQL'
	UPDATE policy_tier_limits
	SET max_per_tx = 100000000000,
	    max_daily_amount = 100000000000,
	    max_daily_count = 1000000,
	    max_monthly_amount = 100000000000;
EOSQL
