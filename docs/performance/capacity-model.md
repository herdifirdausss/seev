# B0 capacity model

Status: **pending canonical measurements**.

This document is the stable output location for Plan 53 capacity evidence. It
does not contain a production-capacity claim. A value may be added only after
three clean confirmation runs and the required soak, integrity, drain, and
resource evidence are committed under `docs/performance/reports/`.

## Declared profile

| Field | Value |
| --- | --- |
| Profile | `local-small` |
| CPU envelope | 4 logical CPUs |
| Docker memory | 4 GiB |
| Container memory cap | 3.52 GiB aggregate; 768 MiB per container |
| Database | Disposable `seev_load_*` databases only |
| Workloads | W1–W7 from the archived B0 protocol |
| Capacity scope | This profile, Git SHA, dataset hash, and host fingerprint only |

## Results

No MSSL, saturation knee, planning limit, or B1–B3 decision is recorded yet.
The load harness safety, schema, scenario, seed, report, and disposable smoke
gates are implemented; canonical staircase, confirmation, spike, soak, and
ledger-size runs remain operator-controlled evidence.

## Required evidence for the first update

- three independent clean confirmation runs per canonical workload;
- p50/p95/p99, offered/achieved WU/s, dropped work, resource/pool/SQL/lock,
  outbox/queue, drain, and integrity values;
- raw artifact hashes and the exact profile, Git SHA, and dataset hash;
- an explicit `ACTIVATE` or `REJECT` decision for B1, B2, and B3 using the
  unchanged thresholds in the archived Plan 53;
- a planning limit no greater than MSSL/2, clearly scoped to `local-small`.

