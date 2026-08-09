# ADR: Analytics stack is a removable read-only projection

- Status: Accepted
- Date: 2026-08-04
- Scope: C2 analytical data platform architecture

## Context

The system needed revenue analytics and reconciliation views without coupling
the OLTP path to analytical query load or making the analytics stack a
dependency for money movement.

## Decision

The C2 analytics stack (Debezium → Redpanda CDC → ClickHouse → dbt → Metabase)
is a one-way, removable, read-only projection. No application service or
money-movement transaction depends on C2 availability.

Key rules:
- No reverse arrow: analytics never writes back to PostgreSQL.
- Ledger `ledger_entries` is immutable and is the financial fact source; C2
  is a derived view, not an authority.
- The CDC allowlist (`analytics/contracts/sources.yaml`) is the source contract;
  fields absent from it are never captured.
- HMAC pseudonymization removes identity before topic publication.
- `bi_readonly` database grants prevent Metabase from writing ClickHouse.
- Deduplication uses transport offset keys and deterministic latest-state keys
  to prevent double-counting on redelivery.

## Consequences

An analytics outage creates data lag but never blocks money movement or OLTP
writes. The stack can be reset or rebuilt from WAL without touching financial
data. Existing Ledger reporting and compliance views remain authoritative for
their existing purpose; C2 does not replace balances, statements, regulatory
views, settlement evidence, or authorization reads.
