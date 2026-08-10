#!/usr/bin/env sh
set -eu

if [ -n "${DBT_CLICKHOUSE_PASSWORD_FILE:-}" ]; then
	[ -s "$DBT_CLICKHOUSE_PASSWORD_FILE" ] || {
		echo "dbt ClickHouse password file is missing" >&2
		exit 2
	}
	DBT_CLICKHOUSE_PASSWORD=$(tr -d '\r\n' <"$DBT_CLICKHOUSE_PASSWORD_FILE")
	export DBT_CLICKHOUSE_PASSWORD
fi

exec "$@"
