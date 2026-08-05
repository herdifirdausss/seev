{#
  ClickHouse has no Postgres-style database.schema nesting, so the warehouse
  migrations (analytics/clickhouse/migrations) create flat databases named
  exactly raw/staging/core/mart/control. dbt's built-in generate_schema_name
  instead combines the profile's default schema with each model's custom
  +schema config (e.g. "core_staging"), which does not match those databases
  and made every non-default-schema model fail with ACCESS_DENIED trying to
  auto-create a database dbt has no privilege to create (confirmed
  2026-08-05). This override uses the custom schema name verbatim.
#}
{% macro generate_schema_name(custom_schema_name, node) -%}
    {%- if custom_schema_name is none -%}
        {{ target.schema }}
    {%- else -%}
        {{ custom_schema_name | trim }}
    {%- endif -%}
{%- endmacro %}
