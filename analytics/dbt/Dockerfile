# No first-party dbt-labs/ClickHouse image is published for this adapter —
# ghcr.io/dbt-labs/dbt-clickhouse does not exist and dbt-clickhouse 1.9.2 was
# never a real release (confirmed 2026-08-05: PyPI jumps 1.8.4 -> 1.10.0). We
# build our own bounded runner from pinned PyPI packages instead.
# Digest verified 2026-08-05.
FROM python:3.12-slim@sha256:57cd7c3a7a273101a6485ba99423ee568157882804b1124b4dd04266317710de

RUN pip install --no-cache-dir dbt-core==1.10.9 dbt-clickhouse==1.10.1

WORKDIR /workspace
