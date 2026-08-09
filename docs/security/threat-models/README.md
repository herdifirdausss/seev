# Threat Models

> [Documentation home](../../README.md) · [Security](../README.md) · **Threat models**

> **Status: Current index.** This directory contains domain-specific threat
> models that expand on the system-level model in
> [`security/threat-model.md`](../threat-model.md).

## Purpose

The system-level threat model in `security/threat-model.md` covers assets,
trust boundaries, and top-level controls for the whole system. Each document
here covers one domain with its specific threats and controls.

## Index

| Document | Scope | Canonical plan |
|---|---|---|
| [Data-platform threat model](data-platform.md) | CDC replication, ClickHouse, analytics credentials | [ADR 0003](../../reference/decisions/0003-analytics-read-only-projection.md) |

## How domain threat models relate to the system model

Each domain threat model:

1. inherits the system-level asset classification and trust boundary definitions;
2. adds domain-specific threat entries not covered by the system model;
3. records the specific controls and their test/evidence owner.

A threat controlled at the system level (e.g., mTLS service identity) is not
repeated here unless the domain adds a domain-specific variant.
