# Core migration layer

Core facts and dimensions are owned by dbt models. ClickHouse role grants are
created before dbt builds; the layer is kept explicit for lineage and reset
procedures.
