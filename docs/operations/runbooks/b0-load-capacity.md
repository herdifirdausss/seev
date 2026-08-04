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
   `LOAD_SEED_KIND=ledger-size` only when the declared dataset requires it.
   Keep generated artifacts under `artifacts/load/<run-id>/`.
4. Run `make load-smoke` to verify bootstrap health and lifecycle cleanup.
   W1, W2, W3, and W5 self-seed their disposable business fixtures; W4 and W7
   still require their declared input token or restored dataset.
5. For a business staircase, verify the manifest and dataset hash before
   offering measured work. After every phase, retain `outbox-summary.json`,
   `integrity-after.json`, `run-status.json`, and the patched k6 summary. A
   successful integrity check is necessary but does not make `gate_passed`
   true; resource and lock evidence remains required.

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
time. `tools/loadprobe` records query IDs and monitoring classes only; do not
copy SQL parameters, request bodies, tokens, or service logs into reports.

## Cleanup and retention

Run `SEEV_LOAD_RUN_ID=<exact-run-id> make load-clean`. Cleanup may remove only
the exact load Compose project, named load volumes, and that run's artifact
directory. If `SEEV_LOAD_KEEP_STACK=1` was used for diagnosis, use the same run
ID explicitly before removing it.
Keep only redacted summaries and hashes under `docs/performance/`; raw JSONL,
database snapshots, credentials, and logs remain outside Git.
