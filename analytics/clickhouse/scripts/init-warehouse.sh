#!/usr/bin/env sh
set -eu

clickhouse_client() {
  clickhouse-client --host clickhouse --multiquery "$@"
}

for migration in /migrations/*.sql; do
  echo "applying $(basename "$migration")"
  clickhouse_client < "$migration"
done

read_secret() {
  file=$1
  [ -s "$file" ] || { echo "required analytics secret is missing: $file" >&2; exit 2; }
  tr -d '\r\n' < "$file"
}

sql_quote() {
  printf "%s" "$1" | sed "s/'/''/g"
}

bi_password=$(read_secret "${CLICKHOUSE_BI_PASSWORD_FILE:?CLICKHOUSE_BI_PASSWORD_FILE is required}")
dbt_password=$(read_secret "${CLICKHOUSE_DBT_PASSWORD_FILE:?CLICKHOUSE_DBT_PASSWORD_FILE is required}")
reconciliation_password=$(read_secret "${CLICKHOUSE_RECONCILIATION_PASSWORD_FILE:?CLICKHOUSE_RECONCILIATION_PASSWORD_FILE is required}")
bi_password_sql=$(sql_quote "$bi_password")
dbt_password_sql=$(sql_quote "$dbt_password")
reconciliation_password_sql=$(sql_quote "$reconciliation_password")

clickhouse_client --query "CREATE USER IF NOT EXISTS metabase_bi IDENTIFIED WITH plaintext_password BY '$bi_password_sql'; GRANT bi_readonly TO metabase_bi;"
clickhouse_client --query "CREATE USER IF NOT EXISTS analytics_dbt IDENTIFIED WITH plaintext_password BY '$dbt_password_sql'; GRANT dbt_transform TO analytics_dbt;"
clickhouse_client --query "CREATE USER IF NOT EXISTS analytics_reconciliation IDENTIFIED WITH plaintext_password BY '$reconciliation_password_sql'; GRANT reconciliation_read TO analytics_reconciliation; GRANT reconciliation_write_control TO analytics_reconciliation;"

echo "ClickHouse raw, staging, core, mart, control layers and least-privilege roles are ready"
