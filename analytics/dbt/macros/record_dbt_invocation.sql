{#
  Populates control.dbt_invocations (plan 58 section 19.2/24.4) so a
  Prometheus exporter or dashboard can report the latest dbt result without
  parsing run logs. ClickHouse's DateTime64 JSON/SQL parser rejects an
  RFC3339 'Z' suffix (see analytics/reconciliation/cmd/reconcile/main.go for
  the same fix on the Go side), so timestamps use strftime without one.
#}
{% macro record_dbt_invocation(results) %}
  {%- if execute -%}
    {%- set failure_count = results | selectattr("status", "in", ["error", "fail", "skipped"]) | list | length -%}
    {%- set result = "success" if failure_count == 0 else "failed" -%}
    {%- set insert_sql -%}
      insert into control.dbt_invocations
        (invocation_id, environment, result, started_at, finished_at, model_count, failure_count, artifact_path)
      values (
        '{{ invocation_id }}',
        '{{ target.name }}',
        '{{ result }}',
        '{{ run_started_at.strftime("%Y-%m-%d %H:%M:%S.%f") }}',
        '{{ modules.datetime.datetime.utcnow().strftime("%Y-%m-%d %H:%M:%S.%f") }}',
        {{ results | length }},
        {{ failure_count }},
        ''
      )
    {%- endset -%}
    {% do run_query(insert_sql) %}
  {%- endif -%}
{% endmacro %}
