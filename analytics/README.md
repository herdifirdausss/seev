# Seev C2 analytics platform

This directory is an optional, one-way analytical projection of the Ledger,
Payin, and Payout PostgreSQL databases. It is intentionally separate from the
application services and from the default Compose profile.

```text
PostgreSQL -> Debezium/Kafka Connect -> Redpanda -> ClickHouse raw
                                      -> ClickHouse staging/core/mart
                                      -> dbt tests and reconciliation
                                      -> Metabase (optional, read-only)
```

RabbitMQ remains the operational/domain-event boundary. No application request
may call Redpanda, ClickHouse, dbt, or Metabase, and C2 never writes to an
application database.

## Local workflow

Generate local-only secrets first:

```bash
make analytics-secret
make analytics-config-check
make analytics-up-core
make analytics-health
make analytics-source-setup
make analytics-connectors-validate
make analytics-connectors-apply
make analytics-clickhouse-migrate
make analytics-dbt-build
make analytics-reconcile
```

The application PostgreSQL cluster must be running before source setup or
connector application. Supply one password per least-privilege source role
(`ANALYTICS_LEDGER_PASSWORD`, `ANALYTICS_PAYIN_PASSWORD`, and
`ANALYTICS_PAYOUT_PASSWORD`). `analytics-core` itself can start without it; this is
useful for synthetic ClickHouse/dbt work and keeps analytics outages isolated
from OLTP.

The UI is opt-in:

```bash
make analytics-up-ui
```

All host bindings are loopback-only. `analytics-reset` removes only the
analytics Compose project and its named volumes; it does not touch application
volumes or replication slots. Slot deletion is an explicit runbook action.

## Data handling

The connector configurations use explicit table and column allowlists. Raw
payloads, callback bodies, payout destinations, credentials, authentication
data, KYC data, and raw error/request/response fields are excluded. Approved
user identifiers are transformed to deterministic HMAC pseudonyms in the
Kafka Connect SMT before they reach a topic. The salt is a required local
secret and is never committed.

Money remains signed integer minor units. Dates for business reporting use
`Asia/Jakarta`; source and warehouse timestamps remain UTC with microsecond
precision where the source provides it. Recognized fee revenue comes only from
posted Ledger entries belonging to approved fee accounts. Fee quotes are intent
and conversion facts, not revenue.

## Profiles and resource guardrails

See [compose/profiles.md](compose/profiles.md). The default profile does not
start any analytics component. The core profile is bounded for a laptop and
does not include Metabase. Component versions are pinned in
`compose/docker-compose.analytics.yml` and `connect/Dockerfile`.

## Evidence status

The implementation and fixtures are committed before runtime evidence is
collected. Commands that require Docker, source databases, or external images
are deliberately not run as part of code authoring. Evidence files therefore
distinguish implementation from executed proof and list the remaining runtime
gates.

Residual limitations remain: this local stack does not prove managed Kafka,
multi-node Redpanda/ClickHouse durability, production scale, audited revenue
recognition, real vendor invoices, or regulatory-report replacement.
