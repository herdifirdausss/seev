# Analytics fixtures

Fixtures are synthetic and safe to replay in a disposable analytics project.
They cover insert/update/delete envelopes, heartbeat metadata, compatible and
incompatible schema changes, deterministic pseudonym output, duplicate
transport identity, Ledger totals, and reconciliation pass/fail evidence.

`ledger-entry-update-forbidden.json` is intentionally a critical incident
fixture: `ledger_entries` is immutable, so a real delete/update must never be
accepted as an ordinary current-state change.
