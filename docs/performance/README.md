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

The current evidence is the [2026-07-31 baseline](reports/2026-07-31-baseline.md),
which supersedes the [2026-07 baseline](reports/2026-07-baseline.md) for
numerical claims. It runs the full staircase-confirm-spike-soak protocol for
S1 (normal P2P), S3 (webhook burst), and S4 (mixed journey) — all three
confirmed and passing — plus S2 (hot-account), whose confirmed MSSL and
spike passed but whose 60-minute soak fails by design (§16.2: unbounded row
growth in two webhook-audit tables, root-caused and fixed for the
compliance/correctness angle; the growth itself is accepted capacity
behavior per a later retention-policy decision, not an outstanding bug).
It also runs real, directly-evidenced B1/B2/B3 experiments (all three
REJECT, each backed by a live A/B or growth-curve test rather than absence
of a test) and a Phase 5 resource-elasticity check (partial — see the
report's own §15.1 for what it does and does not resolve). It is still not
a canonical, fully complete B0 pass — see its own §26 Definition of Done for
exactly which boxes remain unchecked (HTTP req/s-vs-WU/s reporting had a gap
since closed, but retroactive coverage for this report's own historical runs
was not attempted; dataset manifest and sub-second lock-sampling tooling now
exist for future runs but weren't applied retroactively either).
