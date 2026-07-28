# B0 load and capacity runbook

> **Status: Current. Audience: developers and operators.** This runbook is
> limited to the disposable `local-small` profile and makes no production
> capacity claim.

## Preparation

1. Confirm Docker has the declared 4 CPU/4 GiB envelope and stop the optional
   Grafana/Loki/Tempo/Vault profiles.
2. Ensure local mTLS identities exist with `make certs`. Use a fresh run ID
   and synthetic dataset. Set
   `SEEV_LOAD_ACK=disposable-only`; never substitute a shared development or
   production URL/database.
3. Run `make load-lint`, then `make load-seed LOAD_SEED_KIND=journey` or
   `LOAD_SEED_KIND=ledger-size`. Keep generated artifacts under
   `artifacts/load/<run-id>/`.
4. Run `make load-smoke` to verify bootstrap health and lifecycle cleanup. For
   a business staircase, restore the declared seed separately and verify its
   manifest, seed hash, and integrity report before offering measured work.

## Abort and drain

Stop offered work if the K14 failure thresholds occur: unknown money outcome,
dead event/command, restart/OOM, health loss, five-minute outbox age, p99 above
10 seconds, or unexpected failures above 5%. Stop the generator first; keep the
services up long enough to drain queues, then run integrity/lifecycle checks.
An aborted run is retained as evidence and is never silently converted into a
passing summary.

## Invalid run and integrity failure

Mark a run invalid only for a documented host/generator failure such as sleep,
generator saturation, truncated telemetry, or a failed restore. Replace it
with a clean run. Any ledger/projection mismatch, duplicate monetary effect,
unknown payout outcome, dead load-caused event, or unresolved command is a
correctness failure: preserve the redacted manifest and stop the capacity
experiment until the separate correctness fix is reviewed.

## Bottleneck diagnosis

Use at least two independent signals: achieved throughput/latency plus pool
waits, PostgreSQL wait/lock samples, CPU/memory, outbox/queue age, or resolver
time. `cmd/loadprobe` records query IDs and monitoring classes only; do not
copy SQL parameters, request bodies, tokens, or service logs into reports.

## Cleanup and retention

Run `SEEV_LOAD_RUN_ID=<exact-run-id> make load-clean`. Cleanup may remove only
the exact load Compose project, named load volumes, and that run's artifact
directory. If `SEEV_LOAD_KEEP_STACK=1` was used for diagnosis, use the same run
ID explicitly before removing it.
Keep only redacted summaries and hashes under `docs/performance/`; raw JSONL,
database snapshots, credentials, and logs remain outside Git.
