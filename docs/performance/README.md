# Performance evidence

B0 load artifacts are disposable and live under `artifacts/load/`, which is
ignored by Git. Raw k6 time series, dumps, credentials, and service logs never
belong in Git. Only small redacted summaries may be committed under
`docs/performance/reports/`, with hashes linking them to the raw artifact
bundle. Capacity numbers are valid only for the named profile and Git/data
hashes.

Start with the [archived B0 protocol](../roadmap/archive/53-b0-load-capacity-gate.md),
the [baseline inventory](baseline/b0-inventory.yaml), and the
[capacity model](capacity-model.md).

The current preliminary evidence is [2026-07 baseline](reports/2026-07-baseline.md).
It records four short disposable runs and explicitly does not claim canonical
MSSL or production capacity.
