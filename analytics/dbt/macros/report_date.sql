{% macro report_date(timestamp_expression) -%}
    toDate(toTimeZone({{ timestamp_expression }}, 'Asia/Jakarta'))
{%- endmacro %}
