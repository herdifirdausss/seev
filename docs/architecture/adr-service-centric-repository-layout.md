# ADR: Service-centric repository layout

- Status: Accepted
- Date: 2026-08-04
- Scope: Repository structure only

## Context

The modular monolith had one Go module, but service entrypoints, business
packages, migrations, operational programs, and contract artifacts were spread
across unrelated top-level directories. A runtime name therefore did not give
contributors an obvious source-code home.

## Decision

Keep one Go module and make `services/<service>/` the primary home for every
deployable business service. A service owns its composition root, private
implementation, migrations, and README. Use capability-oriented packages
inside `internal/`: `transport/http` and `transport/grpc` for inbound
protocols, a domain-named package for use cases, repository packages for
persistence ports and adapters, and worker packages for background processing.
A generic `internal/service` bucket is not allowed.

Use the following top-level taxonomy:

- `internal/platform/` for domain-neutral application infrastructure.
- `internal/testkit/` for repository-wide integration helpers.
- `contracts/` for protobuf, HTTP, event, and compatibility artifacts.
- `gen/go/` for committed generated Go bindings.
- `tools/` for developer and CI utilities.
- `operations/` for support agents and recovery workflows.
- `tests/` for repository-wide verification.

The logical service name `vendor` remains unchanged for databases, deployment,
and configuration. Its physical source root is `services/vendor-service/`
because a literal Go `vendor/` directory has special import semantics.

## Rules

1. Business code stays inside the service that owns its data and decisions.
2. Cross-service code uses contracts, generated clients, or explicit public
   facades; it does not import another service's `internal/` package.
3. Shared platform code must remain domain-neutral and must not import
   `services/`.
4. Interfaces live with their consuming feature; repository implementations
   stay behind that port.
5. New generic buckets such as `application/`, `common/`, `utils/`, `helpers/`,
   `handler/`, and `service/` are rejected by architecture tests.
6. Structural moves do not change business behavior, schemas, wire contracts,
   service identities, or operational semantics.

The executable checks are the service registry and boundary tests in
[`tests/architecture/`](../../tests/architecture/) plus the root
[`boundary_test.go`](../../boundary_test.go).

## Consequences

Contributors can find a service by its runtime name, and migrations and
service-specific documentation are collocated with the implementation. Build
and test commands can target one service without a mapping table. The single
Go module remains convenient for repository-wide refactoring, while the
service roots are ready for a future multi-module split if independent release
or ownership requirements justify it.

The structure is intentionally not symmetrical: a service creates only the
capability directories it needs. Ledger remains decomposed by financial
responsibility, while smaller services can stay shallow.
