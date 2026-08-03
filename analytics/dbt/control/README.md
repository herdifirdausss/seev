# dbt control models

Control evidence is persisted by the reconciliation runner and ClickHouse
bootstrap. dbt control models may expose bounded invocation/freshness summaries
but must not expose raw identifiers to BI users.
