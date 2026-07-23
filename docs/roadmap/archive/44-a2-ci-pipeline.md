# 44 — Track A2 (CI): Full-Stack Gates in GitHub Actions

Status: complete. This document records the CI half of Track A2 from
[plan 42](../42-long-term-roadmap.md). Local Kubernetes remains the separate,
optional [plan 35](../active/35-phase6j-kubernetes.md).

## Outcome

The repository now has two complementary GitHub Actions workflows:

- `.github/workflows/ci.yml` is the required push and pull-request gate. It
  gives documentation-only changes a fast path and runs the full runtime gate
  for every other change.
- `.github/workflows/nightly.yml` keeps its historical filename but runs
  automatically once a week. It can also be dispatched manually for the
  business journey, the chaos suite, or both.

The initial plan was written when Seev had six deployable services and eight
chaos scenarios. The completed implementation follows the live topology:
eight service images, eleven healthy Compose containers including
infrastructure, and fourteen chaos scenarios.

## Final CI policy

- Use GitHub-hosted `ubuntu-24.04` runners with least-privilege permissions,
  concurrency control, and explicit timeouts.
- Treat one `ci-gate` job as the branch-protection result. It verifies that
  heavy jobs succeeded for runtime changes and were deliberately skipped for
  documentation-only changes.
- Pin every third-party action to a full commit SHA and keep a readable version
  comment beside it. A repository script enforces this policy.
- Build images with Docker Bake and GitHub Actions caching, load them into the
  runner, and never push them to a registry from this gate.
- Generate disposable credentials per scheduled run and mask them before they
  enter the workflow environment.
- Keep observability, Vault, real vendors, cloud deployment, GitOps, and
  Kubernetes outside the CI track.

## T1 — Container smoke gate

Implemented `scripts/smoke-container.sh` and `make smoke-container`.

The script:

1. generates the local mTLS certificates;
2. starts PostgreSQL, Redis, RabbitMQ, and all eight service containers;
3. waits for all eleven expected containers to become healthy;
4. exercises registration, login, top-up intent creation, a signed vendor
   webhook, settlement, and the resulting ledger balance;
5. supports preloaded CI images through `SEEV_SMOKE_NO_BUILD=1`; and
6. collects bounded diagnostics and cleans up through a trap on failure.

Result: the smoke gate validates built container images rather than host
binaries and is reusable both locally and in CI.

## T2 — Required push and pull-request workflow

`.github/workflows/ci.yml` contains:

| Job | Responsibility |
|---|---|
| `changes` | Classify the diff as documentation-only or runtime-affecting. |
| `lint-and-test` | Run actionlint, ShellCheck, action pin validation, golangci-lint, and race-enabled unit tests. |
| `integration` | Run the tagged integration suite with testcontainers. |
| `smoke-container` | Build all eight images once, verify their revision labels, and run the container smoke journey. |
| `ci-gate` | Produce one unambiguous required result for both the fast and full paths. |

Failure diagnostics from the smoke job are uploaded with bounded retention.
Checkout credentials are not persisted, and each job has only the permissions
it needs.

Result: runtime changes cannot pass because a required heavy job was silently
absent, while documentation-only changes do not spend time building eight
images.

## T3 — Scheduled full-stack workflow

`.github/workflows/nightly.yml` runs on Monday at 02:17 WIB and supports manual
dispatch. The automatic cadence was changed from nightly to weekly to fit the
repository's runner-cost budget; the filename remains unchanged so existing
links and searches continue to work.

Business E2E and all chaos scenarios run sequentially in one job because
GitHub-hosted jobs do not share a machine or Docker state. The workflow resets
volumes between suites, writes a step summary, uploads diagnostics only on
failure, and then returns a failing job result when either selected suite
fails.

Result: expensive recovery behavior is exercised regularly without making
every pull request wait for the complete chaos suite.

## T4 — Documentation and supply-chain closure

The root README, contributor guide, project guide, and operational tooling
guide now describe the local and CI gates. Dependabot tracks GitHub Actions
updates, while `scripts/ci/check-action-pins.sh` prevents floating action tags
from entering a workflow.

Result: contributors can tell which checks run locally, on each runtime
change, and on the weekly schedule, and action updates remain explicit review
events.

## Verification evidence

The implementation is directly represented by:

- `.github/workflows/ci.yml`;
- `.github/workflows/nightly.yml`;
- `.github/dependabot.yml`;
- `scripts/smoke-container.sh`;
- `scripts/ci/check-action-pins.sh`;
- the `smoke-container` and `verify-full` Make targets; and
- the CI description in [OPERATIONS.md](../../operations/README.md#4-cicd-github).

Remote workflow-run status is external, time-varying evidence and is therefore
not frozen into this plan. Check the repository's Actions page for the current
branch result.

## Definition of done

- [x] Documentation-only and runtime changes take explicit, testable paths.
- [x] Runtime CI runs lint/tests, integration tests, and an eight-image smoke
  journey.
- [x] One required gate rejects failed, cancelled, or unexpectedly skipped
  runtime jobs.
- [x] The weekly/manual workflow runs the business and chaos suites with fresh
  credentials and failure diagnostics.
- [x] Third-party actions are SHA-pinned and checked in-repository.
- [x] No image is pushed and no cloud deployment credential is required.
- [x] Plan index and roadmap status reflect completion.
