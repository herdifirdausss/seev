# Project Guide

This guide defines the repository-wide constraints for contributors. Read it
before changing service boundaries, ledger behavior, shared infrastructure, or
the verification workflow.

## Service boundaries

- The deployable services are gateway, auth-service, ledger-service,
  payin-service, payout-service, fraud-service, and admin-bff-service.
- Each service owns its database. Cross-service database queries are forbidden;
  use the published HTTP, gRPC, or event contract instead.
- internal/ledger exposes its public facade from the package root. External
  packages must not import ledger repositories, processors, or other internal
  implementation packages.
- internal/ledger/events is the deliberate exception to the previous rule:
  it contains the versioned wire contract for ledger events and may be imported
  by consumers.
- pkg/ must not import internal/. Shared packages must remain independent of
  service implementations.
- Generated protobuf bindings under gen/ are part of the committed contract.
  Change the source under api/proto/, regenerate, lint, and run the breaking
  check together.

## Financial invariants

1. ledger_entries is append-only. Never update or delete an entry from
   application code; corrections are new compensating transactions.
2. Monetary values must not use float32 or float64. Use decimal.Decimal in Go
   and integer minor units where the schema requires them.
3. Every transaction must balance: total debit equals total credit.
4. Every posting command requires an idempotency key.
5. Do not reorder execTransfer in
   internal/ledger/service/handle/service.go without understanding the
   idempotency gate, lock order, validation order, balance projection, posting,
   and outbox guarantees documented in plans 04 and 14.
6. A service must never write another service's tables, including through a
   shared migration or administrative shortcut.

## Security rules

- Keep migration-owner and application database identities separate.
- Never commit real credentials, tokens, certificates, private keys, or local
  generated secrets.
- Preserve request authentication and authorization at public and internal
  boundaries. Internal gRPC authentication is not a replacement for user
  authorization.
- Keep public logs free of raw credentials, full idempotency keys, and other
  replayable values. Reuse pkg/logger masking helpers.
- Validate webhook signatures before any state change.
- Preserve fail-open or fail-closed behavior where it is explicitly part of a
  service contract; changing that behavior requires tests and documentation.
- Local defaults in .env.example and Compose are not production credentials.

## Build and verification

The normal pre-commit gate is:

~~~bash
go build ./...
go vet ./...
make lint
make test
make proto-lint
git diff --check
~~~

When protobuf contracts change, also run:

~~~bash
make proto
make proto-breaking
~~~

Integration tests require Docker:

~~~bash
go test -tags=integration -race ./...
~~~

The complete verification target resets Docker volumes before executing the
repository's build, static checks, unit tests, smoke journey, business journey,
and all chaos scenarios:

~~~bash
make verify-full
~~~

Use the full gate for changes to money movement, persistence, messaging,
service startup, container wiring, or shared test bootstrap. Documentation-only
changes may use the normal gate plus link and content validation.

## Operational test scripts

- scripts/lib.sh owns the shared host-binary bootstrap. Fix common startup,
  migrations, process lifecycle, and database helpers there.
- scripts/smoke-test.sh [ledger|payin|payout|all] verifies core live-server
  paths.
- scripts/business-e2e.sh verifies the end-user and operator business journey.
- scripts/admin-e2e.sh verifies the admin BFF session, CSRF, proxy mutation,
  and audit row journey.
- scripts/chaos-test.sh {1..11|all} verifies crash and dependency-failure
  behavior.
- scripts/smoke-container.sh verifies freshly built Compose application images
  rather than host binaries.

Do not copy bootstrap logic between these scripts. A fix to shared lifecycle
behavior belongs in scripts/lib.sh.

## Development conventions

- Use log/slog and request-scoped logging.
- Keep sentinel errors near their owning domain and wrap them with %w.
- Map domain errors to transport status codes at the HTTP or gRPC boundary.
- Use parameterized SQL; do not construct SQL with untrusted input.
- Keep unit tests beside the code and integration tests behind the integration
  build tag.
- Regenerate mocks after interface changes; do not maintain generated mocks by
  hand.
- Use standard-library net/http routing patterns used by the surrounding
  service.
- Preserve executable bits on shell scripts and run ShellCheck-compatible
  syntax.

## Runtime references

- .env.example is the source of local host-process configuration examples.
- docker-compose.yml is the source of container ports, profiles, and
  service-specific environment variables.
- Makefile is the source of supported build and verification commands.
- README.md is the current architecture and onboarding overview.
- docs/plan/ records implementation decisions, completed phases, and future
  work; older plans may describe the repository at an earlier phase.
- docs/runbooks/ contains operational recovery procedures.

When documentation and executable configuration disagree, verify behavior
against code, tests, Compose, and the Makefile, then update the documentation in
the same change.
