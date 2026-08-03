# Analytics observability

Prometheus rules and the Grafana dashboard use bounded labels only: source,
table, operation, layer, result, severity, and check. They never label metrics
with user/account/transaction IDs, offsets, or raw error text. Every critical
alert links to a runbook under `docs/runbooks/`.
