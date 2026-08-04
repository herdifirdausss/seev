# Critical-Failure Acceptance

## Scope

This document closes the repository-side implementation of the critical-failure
test and retained-artifact action item. It does not turn a CI or local run into
production evidence: a deployed production-shaped run remains an explicit
`evidence_required` gate.

The failure vocabulary is defined in the [golden-route failure
matrix](../engineering/golden-route-failure-matrix.md). The executable suite
also covers privacy-lifecycle and merchant relay failures in addition to the
core money-movement path.

## Executable coverage

| Surface | Command/workflow | Evidence produced |
|---|---|---|
| Unit, race, and service integration behavior | `go test -tags=integration -race -timeout 25m ./...` | Go JSON event stream and exit-code record in the CI integration artifact |
| Dependency/process failure matrix | `KEEP_WORK_DIR=1 ./scripts/chaos-test.sh all` | Scenario result text, stdout, and service logs in the retained work directory |
| Business route | `make business-e2e` | Full journey stdout and service logs when the work directory is retained |
| Privacy route | `make privacy-e2e` | Managed-wrapper stdout and service logs when the work directory is retained |
| Container round-trip | `make smoke-container` | Existing failure diagnostics artifact on a failed container smoke run |

The scheduled full-stack workflow runs the business, privacy, and all 23 chaos
scenarios on one runner so Docker and process state are preserved across the
journey. It records the selected-suite outcomes in the manifest even when a
suite is skipped or fails.

## CI artifact contract

Artifacts are uploaded on both successful and failed runs with a run ID and
attempt in the name:

| Artifact | Retention | Contents |
|---|---:|---|
| `integration-evidence-<run-id>-<attempt>` | 30 days | `go-test.jsonl`, `test-exit-code.txt`, and `run-manifest.txt` |
| `scheduled-full-stack-evidence-<run-id>-<attempt>` | 30 days | Manifest, per-suite status, top-level stdout/result text, and service logs |

The scheduled bundle is assembled by
[`scripts/ci/package-critical-failure-evidence.sh`](../../scripts/ci/package-critical-failure-evidence.sh).
Its allowlist copies only top-level `.log` and `.txt` files from each suite's
work directory. Binaries, PID files, generated certificates/private keys,
encrypted object-store contents, and nested directories are excluded. The
manifest is written by
[`scripts/ci/write-critical-failure-manifest.sh`](../../scripts/ci/write-critical-failure-manifest.sh)
and records the schema, commit, workflow run/attempt, event, suite, outcomes,
and generation time without recording test credentials.

## Local verification

Run the repository-side checks from a clean disposable environment:

```sh
go test -tags=integration -race -timeout 25m ./...
KEEP_WORK_DIR=1 ./scripts/chaos-test.sh all
KEEP_WORK_DIR=1 make business-e2e
KEEP_WORK_DIR=1 make privacy-e2e
```

The scripts print their work-directory path. Preserve that directory only for
postmortem inspection; do not commit it. CI uses the same work-directory
contract and packages the allowlisted files before upload.

## Production evidence gate

The repository implementation is complete, but the production-shaped run is
not claimable from source or CI configuration. Before the critical-failure
action is marked runtime-accepted, attach all of the following to the release
record:

- deployed environment, application commit, and image/container digests;
- workflow/run identifier and the selected critical-failure scenarios;
- the retained artifact URL and manifest;
- ledger-balance and duplicate-effect assertions;
- outbox-drain, reconciliation, alert, and runbook results;
- operator, platform, and service-owner sign-off.

Record the result in the [production-readiness scorecard](../engineering/production-readiness-scorecard.md)
and [production-readiness checklist](../operations/production-readiness-checklist.md).
Until that record exists, this action remains an external `evidence_required`
gate even when CI is green.
