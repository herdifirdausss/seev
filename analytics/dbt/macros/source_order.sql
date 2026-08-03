{% macro source_order(source_lsn, partition, offset) -%}
    tuple(coalesce({{ source_lsn }}, toUInt64(0)), {{ partition }}, {{ offset }})
{%- endmacro %}
